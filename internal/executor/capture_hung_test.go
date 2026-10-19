package executor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/version"
)

// hungExecutor listens on a socket, greets like an executor and reads the request of a capture, but never
// answers it. The connections stay open until the test ends; got says that a request has arrived.
func hungExecutor(t *testing.T) (path string, got <-chan struct{}) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cgh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path = filepath.Join(dir, "x.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	arrived := make(chan struct{}, 8)
	release := make(chan struct{})
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				cc := newCodec(conn)
				if cc.write(&Frame{Hello: &Hello{Protocol: ProtocolVersion, Version: version.Version, Role: "executor"}}) != nil {
					return
				}
				if f, err := cc.read(); err != nil || f.Hello == nil {
					return
				}
				if f, err := cc.read(); err != nil || f.Capture == nil {
					return
				}
				arrived <- struct{}{}
				<-release
			}()
		}
	}()
	t.Cleanup(func() {
		close(release)
		_ = l.Close()
		<-accepted
	})
	return path, arrived
}

// An open that waits for an executor that does not answer ends with the context of the request, whether it runs out of
// time or is cancelled: no goroutine and no socket is left behind (the test binary checks the goroutines).
func TestOpeningACaptureEndsWithTheContextOfAnExecutorThatDoesNotAnswer(t *testing.T) {
	path, arrived := hungExecutor(t)
	c, err := Dial(context.Background(), path, selfOpts())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.OpenCapture(ctx, loOpen("c1"))
		errc <- err
	}()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the request did not arrive")
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled open: %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled open still waits for the executor")
	}

	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.OpenCapture(ctx, loOpen("c1")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("an open that runs out of time: %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the open took %v for a deadline of 200 ms", time.Since(start))
	}
}
