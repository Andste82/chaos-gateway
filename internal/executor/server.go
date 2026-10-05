package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/version"
)

// Server serves the executor protocol on a Unix socket.
type Server struct {
	Exec *Executor
	// Auth decides from the peer credentials whether a caller may connect; nil allows root only.
	Auth func(Cred) error
	Log  *slog.Logger
	// HelloTimeout bounds the wait for the client's hello.
	HelloTimeout time.Duration
	// Protocol overrides ProtocolVersion; for tests of the mismatch handling only.
	Protocol int

	mu    sync.Mutex
	conns map[net.Conn]struct{}

	watchesMu sync.Mutex
	watches   int
}

// acquireWatch reserves one of maxWatches concurrent watch slots.
func (s *Server) acquireWatch() bool {
	s.watchesMu.Lock()
	defer s.watchesMu.Unlock()
	if s.watches >= maxWatches {
		return false
	}
	s.watches++
	return true
}

func (s *Server) releaseWatch() {
	s.watchesMu.Lock()
	defer s.watchesMu.Unlock()
	s.watches--
}

// Listen creates the Unix socket at path with the given mode. A stale socket file from an earlier
// run is replaced; any other file at that path is an error.
func Listen(path string, mode os.FileMode, owner int) (net.Listener, error) {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		// a live executor owns the socket: two executors would break the serialization
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("another executor is listening on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		_ = l.Close()
		return nil, err
	}
	if owner >= 0 {
		if err := os.Chown(path, owner, -1); err != nil {
			_ = l.Close()
			return nil, err
		}
	}
	return l, nil
}

func (s *Server) proto() int {
	if s.Protocol != 0 {
		return s.Protocol
	}
	return ProtocolVersion
}

func (s *Server) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Serve accepts connections until the listener closes or the context ends.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	stop := context.AfterFunc(ctx, func() {
		_ = l.Close()
		s.mu.Lock()
		for c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
	})
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.mu.Lock()
		if ctx.Err() != nil {
			// the context ended between Accept returning this connection (already queued by the
			// kernel before Close took effect) and this lock: the closer above already ran and will
			// not run again, so close it here instead of leaving it open and its handler goroutine
			// running forever.
			s.mu.Unlock()
			_ = c.Close()
			return nil
		}
		if s.conns == nil {
			s.conns = map[net.Conn]struct{}{}
		}
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				_ = c.Close()
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
			}()
			s.handle(ctx, c)
		}()
	}
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	log := s.logger()
	// a bug in the decoder or the protocol must not take the executor down: close that connection
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic in a connection handler", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	cred, err := PeerCred(c)
	if err == nil {
		auth := s.Auth
		if auth == nil {
			auth = AllowUIDs()
		}
		err = auth(cred)
	}
	cc := newCodec(c)
	if err != nil {
		log.Warn("connection refused", "error", err)
		_ = cc.write(&Frame{Error: &RemoteError{Code: CodeForbidden, Message: "peer credentials rejected"}})
		return
	}
	if err := cc.write(&Frame{Hello: &Hello{Protocol: s.proto(), Version: version.Version, Role: "executor"}}); err != nil {
		return
	}
	timeout := s.HelloTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	_ = c.SetReadDeadline((&clock.Real{}).Now().Add(timeout))
	f, err := cc.read()
	_ = c.SetReadDeadline(time.Time{})
	if err != nil || f.Hello == nil {
		_ = cc.write(&Frame{Error: &RemoteError{Code: CodeProtocol, Message: "the first message of a client must be a hello"}})
		return
	}
	if f.Hello.Protocol != s.proto() {
		log.Warn("protocol mismatch", "client", f.Hello.Protocol, "server", s.proto(), "peer_pid", cred.PID)
		_ = cc.write(&Frame{Error: &RemoteError{Code: CodeProtocol, Message: fmt.Sprintf("client speaks protocol %d, executor speaks %d", f.Hello.Protocol, s.proto())}})
		return
	}
	f, err = cc.read()
	if err != nil {
		return
	}
	if f.Watch != nil {
		s.serveWatch(ctx, cc, f.Watch)
		return
	}
	for {
		if f.Request == nil {
			_ = cc.write(&Frame{Error: &RemoteError{Code: CodeInvalid, Message: "expected a request"}})
			return
		}
		if err := cc.write(&Frame{Response: s.serve(ctx, f.Request)}); err != nil {
			return
		}
		if f, err = cc.read(); err != nil {
			return
		}
		if f.Watch != nil {
			_ = cc.write(&Frame{Error: &RemoteError{Code: CodeInvalid, Message: "a watch must be the only message on its connection"}})
			return
		}
	}
}

// maxWatches bounds concurrent watch connections (M6a-04): a watch is cheap (one child process,
// one goroutine), but unbounded, uninvited long-lived connections are still a resource a client
// should not be able to exhaust; a handful is far more than this gateway's own clients ever need
// at once (today: one, the engine's conntrack follower).
const maxWatches = 4

// serveWatch runs a connection that asked to watch instead of to make requests (M6a-04): it owns
// the connection for as long as the watch runs, sending one Frame.Event per line until the
// connection closes, the executor stops, or the watch itself fails.
func (s *Server) serveWatch(ctx context.Context, cc *codec, w *WatchRequest) {
	log := s.logger()
	if !s.acquireWatch() {
		_ = cc.write(&Frame{Error: &RemoteError{Code: CodeForbidden, Message: "too many concurrent watches"}})
		return
	}
	defer s.releaseWatch()
	events, stop, err := s.Exec.Watch(ctx, w.What, w.NS)
	if err != nil {
		_ = cc.write(&Frame{Error: &RemoteError{Code: CodeInvalid, Message: err.Error()}})
		return
	}
	defer stop()
	for ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			log.Error("cannot encode a watch event", "error", err)
			continue
		}
		if err := cc.write(&Frame{Event: &WatchEvent{Data: b}}); err != nil {
			return
		}
	}
}

func (s *Server) serve(ctx context.Context, req *Request) *Response {
	resp := &Response{ID: req.ID}
	ops := make([]Operation, 0, len(req.Ops))
	for i, raw := range req.Ops {
		op, err := Decode(raw)
		if err != nil {
			resp.Error = &RemoteError{Code: CodeInvalid, Message: fmt.Sprintf("ops[%d]: %v", i, err)}
			resp.Outcome.Generation = s.Exec.Generation()
			return resp
		}
		ops = append(ops, op)
	}
	// shutdown does not interrupt a running operation: it finishes, and so does a request that was
	// accepted before the shutdown began
	out, err := s.Exec.DoBatch(context.WithoutCancel(ctx), ops)
	resp.Outcome = out
	if err == nil {
		resp.OK = true
		return resp
	}
	code := CodeInternal
	var ce *CommandError
	switch {
	case errors.Is(err, ErrOutOfScope):
		code = CodeScope
	case errors.As(err, &ce):
		code = CodeCommand
	}
	resp.Error = &RemoteError{Code: code, Message: err.Error()}
	return resp
}
