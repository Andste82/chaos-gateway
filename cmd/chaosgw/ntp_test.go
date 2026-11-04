package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/ntpserver"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.LocalAddr().(*net.UDPAddr).Port
}

func TestTheNTPListenAddressIsAHostAndAPort(t *testing.T) {
	for _, tc := range []struct {
		in   string
		host string
		port int
		ok   bool
	}{
		{":123", "", 123, true}, {"10.0.0.1:12301", "10.0.0.1", 12301, true}, {"123", "", 0, false}, {":0", "", 0, false},
		{":70000", "", 0, false}, {":ntp", "", 0, false},
	} {
		host, port, err := splitListen(tc.in)
		if (err == nil) != tc.ok || (tc.ok && (host != tc.host || port != tc.port)) {
			t.Errorf("%q: %q %d %v", tc.in, host, port, err)
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"ntp", "--listen", "nonsense"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "--listen") {
		t.Errorf("exit %d: %s", code, errb.String())
	}
	if code := run([]string{"ntp", "--no-such-flag"}, &out, &errb); code != 2 {
		t.Errorf("exit %d", code)
	}
}

func TestTheNTPHealthCheckNeedsAServerThatAnswers(t *testing.T) {
	port := freeUDPPort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	var out, errb bytes.Buffer
	if code := run([]string{"ntp", "--health", "--listen", listen}, &out, &errb); code == 0 {
		t.Fatalf("a health check without a server succeeded: %s", out.String())
	}
	srv := ntpserver.New(ntpserver.Options{BasePort: port})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ntpserver.ListenUDP("127.0.0.1")) }()
	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("no server")
	}
	out.Reset()
	// a server without a configuration answers the probe: it is up
	if code := run([]string{"ntp", "--health", "--listen", listen}, &out, &errb); code != 0 || !strings.Contains(out.String(), "ok") {
		t.Errorf("exit %d: %s %s", code, out.String(), errb.String())
	}
	cancel()
	<-done
}

// The command starts without an API (it registers when the API appears), answers its probe, and stops on SIGTERM.
func TestTheNTPServerRunsWithoutTheAPIAndStopsOnSIGTERM(t *testing.T) {
	port := freeUDPPort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	done := make(chan int, 1)
	var out, errb syncWriter
	token := t.TempDir() + "/token"
	if err := os.WriteFile(token, []byte("svc"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		done <- run([]string{"ntp", "--listen", listen, "--nts-ke-listen", "", "--namespace-check=false", "--api", "http://127.0.0.1:1",
			"--service-token-file", token}, &out, &errb)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for ntpserver.Probe(listen) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("the server does not answer: %s", errb.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d: %s", code, errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the server does not stop")
	}
	if !strings.Contains(errb.String(), "the NTP server starts") {
		t.Errorf("log: %s", errb.String())
	}
}

// syncWriter is a buffer that the command writes while the test reads.
type syncWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
func (w *syncWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }

// runNTPUntilProbed starts `chaosgw ntp` with the given extra flags, waits until it answers its probe, and returns a function
// that stops it with SIGTERM and returns its log.
func runNTPUntilProbed(t *testing.T, extra ...string) (stop func() string) {
	t.Helper()
	port := freeUDPPort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	done := make(chan int, 1)
	var out, errb syncWriter
	token := t.TempDir() + "/token"
	if err := os.WriteFile(token, []byte("svc"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"ntp", "--listen", listen, "--namespace-check=false", "--api", "http://127.0.0.1:1", "--service-token-file", token}, extra...)
	go func() { done <- run(args, &out, &errb) }()
	deadline := time.Now().Add(5 * time.Second)
	for ntpserver.Probe(listen) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("the server does not answer: %s", errb.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func() string {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("exit %d: %s", code, errb.String())
			}
		case <-time.After(15 * time.Second):
			t.Fatal("the server does not stop")
		}
		return errb.String()
	}
}

// The production path of NTS key establishment: the command listens on TCP --nts-ke-listen, accepts a connection and closes
// it without a word (the redirect of TCP 4460 leads devices here; plan §2.18).
func TestTheNTPServerAcceptsAndClosesNTSKeyEstablishmentConnections(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	stop := runNTPUntilProbed(t, "--nts-ke-listen", addr)
	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		if conn, err = net.Dial("tcp4", addr); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing listens on the NTS key-establishment port: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, err := conn.Read(make([]byte, 16)); n != 0 || err == nil || strings.Contains(err.Error(), "timeout") {
		t.Errorf("the server said something or kept the connection: %d bytes, %v", n, err)
	}
	_ = conn.Close()
	if log := stop(); strings.Contains(log, "cannot listen for NTS") {
		t.Errorf("log: %s", log)
	}
}

// A key-establishment port that cannot be opened is no reason to stop the time service: it is logged and the server answers.
func TestAnNTSKeyEstablishmentPortInUseDoesNotStopTheNTPServer(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	stop := runNTPUntilProbed(t, "--nts-ke-listen", l.Addr().String())
	time.Sleep(100 * time.Millisecond)
	if log := stop(); !strings.Contains(log, "cannot listen for NTS key establishment") {
		t.Errorf("log: %s", log)
	}
}
