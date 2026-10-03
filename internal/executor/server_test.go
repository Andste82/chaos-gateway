package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startServer runs a server on a socket in a temp directory. The directory is short: socket paths
// are limited to ~100 bytes.
func startServer(t *testing.T, fr Runner, mod func(*Server)) (string, *Executor) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cgx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "e.sock")
	l, err := Listen(path, 0o660, -1)
	if err != nil {
		t.Fatal(err)
	}
	e := newExec(t, fr)
	// the tests run as whatever user the CI runner is: that user is the peer on both ends
	s := &Server{Exec: e, Auth: AllowUIDs(uint32(os.Getuid()))}
	if mod != nil {
		mod(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = s.Serve(ctx, l) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	return path, e
}

func dial(t *testing.T, path string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), path, selfOpts())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientServerRoundTrip(t *testing.T) {
	fr := &fakeRunner{}
	path, _ := startServer(t, fr, nil)
	c := dial(t, path)
	ctx := context.Background()

	out, err := c.Do(ctx, mustDecode(t, assignWan), mustDecode(t, tcWan))
	if err != nil || out.Generation != 1 || out.Completed != 2 {
		t.Fatalf("%+v %v", out, err)
	}
	if g, err := c.Generation(ctx); err != nil || g != 1 {
		t.Fatalf("generation %d %v", g, err)
	}
	var rules []map[string]any
	fr.respond = func(Command) (Result, error) { return Result{Stdout: `[{"priority":0,"table":"local"}]`}, nil }
	if _, err := c.Read(ctx, Read{What: ReadRules}, &rules); err != nil || len(rules) != 1 {
		t.Fatalf("%v %v", rules, err)
	}
	// a connection serves many requests
	for i := 0; i < 5; i++ {
		if _, err := c.Do(ctx, mustDecode(t, tcWan)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestErrorCodesTravelToTheClient(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolNft {
			return Result{Exit: 1, Stderr: "Error: syntax\n"}, nil
		}
		return Result{}, nil
	}}
	path, _ := startServer(t, fr, nil)
	c := dial(t, path)
	ctx := context.Background()
	if _, err := c.Do(ctx, mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	code := func(err error) string {
		var re *RemoteError
		if !errors.As(err, &re) {
			t.Fatalf("not a remote error: %v", err)
		}
		return re.Code
	}
	_, err := c.Do(ctx, mustDecode(t, `{"type":"tc","entries":[{"object":"qdisc","action":"delete","dev":"eth0","parent":"root"}]}`))
	if code(err) != CodeScope {
		t.Errorf("scope violation: %v", err)
	}
	out, err := c.Do(ctx, mustDecode(t, nftOp))
	if code(err) != CodeCommand || !strings.Contains(err.Error(), "syntax") || out.Generation != 2 {
		t.Errorf("command failure: %+v %v", out, err)
	}
	// the connection survives errors
	if _, err := c.Generation(ctx); err != nil {
		t.Fatal(err)
	}
}

// rawConn speaks the protocol by hand, for what the Client refuses to send.
type rawConn struct {
	net.Conn
	r *bufio.Reader
}

func rawDial(t *testing.T, path string) *rawConn {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return &rawConn{c, bufio.NewReader(c)}
}

func (r *rawConn) send(t *testing.T, line string) {
	t.Helper()
	if _, err := r.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
}

func (r *rawConn) frame(t *testing.T) *Frame {
	t.Helper()
	line, err := r.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var f Frame
	if err := json.Unmarshal(line, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func (r *rawConn) closed() bool {
	_, err := r.r.ReadByte()
	return err != nil
}

func TestServerSendsHelloFirst(t *testing.T) {
	path, _ := startServer(t, &fakeRunner{}, nil)
	rc := rawDial(t, path)
	f := rc.frame(t)
	if f.Hello == nil || f.Hello.Protocol != ProtocolVersion || f.Hello.Role != "executor" {
		t.Fatalf("%+v", f)
	}
}

func TestServerRefusesAClientWithAnotherProtocolVersion(t *testing.T) {
	fr := &fakeRunner{}
	path, e := startServer(t, fr, nil)
	rc := rawDial(t, path)
	rc.frame(t)
	rc.send(t, `{"hello":{"protocol":99,"role":"client"}}`)
	f := rc.frame(t)
	if f.Error == nil || f.Error.Code != CodeProtocol {
		t.Fatalf("%+v", f)
	}
	if !rc.closed() {
		t.Error("the server must close the connection after a mismatch")
	}
	// a request that follows the refusal is never executed
	rc2 := rawDial(t, path)
	rc2.frame(t)
	rc2.send(t, `{"hello":{"protocol":99,"role":"client"}}`)
	// the server may already have closed the connection: a failing write is the expected outcome
	_, _ = rc2.Write([]byte(`{"request":{"id":1,"ops":[` + nftOp + `]}}` + "\n"))
	rc2.frame(t)
	if len(fr.commands()) != 0 || e.Generation() != 0 {
		t.Fatal("a refused client got an operation executed")
	}
}

func TestServerRefusesRequestsBeforeHello(t *testing.T) {
	fr := &fakeRunner{}
	path, _ := startServer(t, fr, nil)
	rc := rawDial(t, path)
	rc.frame(t)
	rc.send(t, `{"request":{"id":1,"ops":[`+nftOp+`]}}`)
	if f := rc.frame(t); f.Error == nil {
		t.Fatalf("%+v", f)
	}
	if len(fr.commands()) != 0 {
		t.Fatal("an operation ran without a handshake")
	}
}

func TestServerTimesOutWithoutClientHello(t *testing.T) {
	path, _ := startServer(t, &fakeRunner{}, func(s *Server) { s.HelloTimeout = 50 * time.Millisecond })
	rc := rawDial(t, path)
	rc.frame(t)
	if f := rc.frame(t); f.Error == nil {
		t.Fatalf("%+v", f)
	}
}

func TestClientRefusesAServerWithAnotherProtocolVersion(t *testing.T) {
	path, _ := startServer(t, &fakeRunner{}, func(s *Server) { s.Protocol = 2 })
	_, err := Dial(context.Background(), path, selfOpts())
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("got %v", err)
	}
	// the other direction: a client with a newer version is refused by the server
	path, _ = startServer(t, &fakeRunner{}, nil)
	_, err = Dial(context.Background(), path, DialOptions{Auth: selfOpts().Auth, Protocol: 2})
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestPeerCredentialsAreChecked(t *testing.T) {
	// the server refuses a peer its policy does not allow; here: everybody
	path, e := startServer(t, &fakeRunner{}, func(s *Server) {
		s.Auth = func(c Cred) error { return errors.New("not allowed") }
	})
	_, err := Dial(context.Background(), path, selfOpts())
	var re *RemoteError
	if !errors.As(err, &re) || re.Code != CodeForbidden {
		t.Fatalf("got %v", err)
	}
	if e.Generation() != 0 {
		t.Error("nothing may run")
	}

	// the client refuses a server whose credentials it does not trust
	path, _ = startServer(t, &fakeRunner{}, nil)
	_, err = Dial(context.Background(), path, DialOptions{Auth: func(c Cred) error { return errors.New("untrusted") }})
	if err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("got %v", err)
	}
}

func TestPeerCredReportsOurOwnIdentity(t *testing.T) {
	path, _ := startServer(t, &fakeRunner{}, nil)
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	cred, err := PeerCred(c)
	if err != nil {
		t.Fatal(err)
	}
	if int(cred.UID) != os.Getuid() || int(cred.PID) != os.Getpid() {
		t.Fatalf("%+v: the server end is this process (uid %d, pid %d)", cred, os.Getuid(), os.Getpid())
	}
	if _, err := PeerCred(&net.TCPConn{}); err == nil {
		t.Error("a non-Unix connection has no peer credentials")
	}
}

func TestAllowUIDs(t *testing.T) {
	a := AllowUIDs(65532)
	for uid, ok := range map[uint32]bool{0: true, 65532: true, 1000: false, 65534: false} {
		if (a(Cred{UID: uid}) == nil) != ok {
			t.Errorf("uid %d: allowed=%v, want %v", uid, !ok, ok)
		}
	}
}

func TestMalformedAndInvalidRequests(t *testing.T) {
	fr := &fakeRunner{}
	path, e := startServer(t, fr, nil)
	rc := rawDial(t, path)
	rc.frame(t)
	rc.send(t, `{"hello":{"protocol":1,"role":"client"}}`)

	rc.send(t, `{"request":{"id":7,"ops":[{"type":"shell","command":"id"}]}}`)
	f := rc.frame(t)
	if f.Response == nil || f.Response.OK || f.Response.Error.Code != CodeInvalid || f.Response.ID != 7 {
		t.Fatalf("%+v", f)
	}
	// one invalid operation rejects the whole request: the valid first one did not run
	rc.send(t, `{"request":{"id":8,"ops":[`+nftOp+`,{"type":"nft_apply","ruleset":{"nftables":[{"flush":{"ruleset":null}}]}}]}}`)
	f = rc.frame(t)
	if f.Response == nil || f.Response.OK || f.Response.Error.Code != CodeInvalid || !strings.Contains(f.Response.Error.Message, "ops[1]") {
		t.Fatalf("%+v", f)
	}
	if len(fr.commands()) != 0 || e.Generation() != 0 {
		t.Fatal("nothing may run when a request has an invalid operation")
	}
	// garbage ends the connection
	rc.send(t, `this is not json`)
	if !rc.closed() {
		t.Error("a malformed frame must close the connection")
	}
}

func TestOversizedLineClosesTheConnection(t *testing.T) {
	path, _ := startServer(t, &fakeRunner{}, nil)
	rc := rawDial(t, path)
	rc.frame(t)
	rc.send(t, `{"hello":{"protocol":1,"role":"client"}}`)
	go func() { // the server stops reading at the limit: the write may fail, which is fine
		chunk := []byte(strings.Repeat("x", 1<<20))
		for i := 0; i < maxLine/len(chunk)+2; i++ {
			if _, err := rc.Write(chunk); err != nil {
				return
			}
		}
	}()
	if !rc.closed() {
		t.Error("an oversized line must close the connection")
	}
}

func TestListenReplacesAStaleSocketButNothingElse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s")
	l, err := Listen(path, 0o660, -1)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o660 {
		t.Errorf("mode %v", st.Mode())
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close() // leaves the socket file behind, like a crashed executor
	l2, err := Listen(path, 0o660, -1)
	if err != nil {
		t.Fatalf("a stale socket must be replaced: %v", err)
	}
	_ = l2.Close()

	file := filepath.Join(dir, "regular")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(file, 0o660, -1); err == nil {
		t.Fatal("a regular file must not be replaced")
	}
	if b, _ := os.ReadFile(file); string(b) != "x" {
		t.Fatal("the file was touched")
	}
}

func TestClientContextCancel(t *testing.T) {
	block := make(chan struct{})
	fr := &fakeRunner{respond: func(Command) (Result, error) { <-block; return Result{}, nil }}
	path, _ := startServer(t, fr, nil)
	defer close(block)
	c := dial(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Do(ctx, mustDecode(t, nftOp))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

// selfOpts trusts this process's own user as the executor: in the tests both ends are the same
// process, and CI does not run as root.
func selfOpts() DialOptions { return DialOptions{Auth: AllowUIDs(uint32(os.Getuid()))} }
