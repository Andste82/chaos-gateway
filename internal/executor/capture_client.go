package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// OpenCapture starts a capture on a connection of its own (M17), like Watch: the capture lives as long as the
// connection. It returns when the executor has answered, with the error that refused the capture or with the
// stream, whose first event is `started`. A connection that breaks ends the stream with an event `stopped`
// whose reason is `error`.
func (c *Client) OpenCapture(ctx context.Context, o CaptureOpen) (CaptureStream, error) {
	if c.path == "" {
		return nil, errors.New("this client was not created with Dial")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.path)
	if err != nil {
		return nil, err
	}
	// The open ends with the context: a request that is cancelled, or runs out of time, closes the connection and so
	// ends the read of an executor that does not answer. The hook is removed once the capture is open; the stream
	// lives on without the context.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	cc, err := handshake(ctx, conn, c.opt)
	if err != nil {
		stop()
		_ = conn.Close()
		return nil, ctxOr(ctx, err)
	}
	if err := cc.codec.write(&Frame{Capture: &o}); err != nil {
		stop()
		_ = cc.Close()
		return nil, ctxOr(ctx, err)
	}
	f, err := cc.codec.read()
	if err != nil {
		stop()
		_ = cc.Close()
		return nil, ctxOr(ctx, err)
	}
	if f.Error != nil {
		stop()
		_ = cc.Close()
		return nil, captureErr(f.Error)
	}
	if f.CaptureEvent == nil || f.CaptureEvent.Kind != CaptureStarted {
		stop()
		_ = cc.Close()
		return nil, errors.New("executor sent an unexpected message")
	}
	if !stop() {
		// the context ended while the answer arrived: the connection is closed, which stops the capture
		_ = cc.Close()
		return nil, ctx.Err()
	}
	s := &remoteStream{c: cc, events: make(chan CaptureEvent, 16), done: make(chan struct{})}
	s.events <- *f.CaptureEvent
	go s.read()
	return s, nil
}

// captureErr turns the refusal of a capture back into the error the executor in this process would have returned,
// so that errors.Is names the reason on both sides of the socket.
func captureErr(e *RemoteError) error {
	var sentinel error
	switch e.Code {
	case CodeCaptureLimit:
		sentinel = ErrTooManyCaptures
	case CodeDiskLow:
		sentinel = ErrDiskLow
	case CodeNoCaptures:
		sentinel = ErrNoCaptureDir
	case CodeScope:
		sentinel = ErrOutOfScope
	default:
		return remoteErr(e)
	}
	return fmt.Errorf("%w (%s)", sentinel, e.Message)
}

// remoteStream is a capture of an executor in another process.
type remoteStream struct {
	c      *Client
	events chan CaptureEvent
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func (s *remoteStream) Events() <-chan CaptureEvent { return s.events }

func (s *remoteStream) Stop(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	_ = s.c.codec.write(&Frame{CaptureCtl: &CaptureCtl{Stop: true, Reason: reason}})
}

// Close ends the connection, which stops a capture that still runs, and waits for the reader. The reader
// delivers what it has read to whoever reads Events; one that does not, loses it.
func (s *remoteStream) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.c.conn.Close()
	<-s.done
}

func (s *remoteStream) send(ev CaptureEvent) {
	select {
	case s.events <- ev:
	default:
		// the reader of the events is behind: counters are replaced by the next ones, the end is not
		if ev.Kind != CaptureStats {
			select {
			case s.events <- ev:
			case <-(&clock.Real{}).After(5 * time.Second):
			}
		}
	}
}

func (s *remoteStream) read() {
	defer close(s.done)
	defer close(s.events)
	for {
		f, err := s.c.codec.read()
		if err != nil || f.Error != nil || f.CaptureEvent == nil {
			msg := "the connection to the executor broke"
			if err == nil && f.Error != nil {
				msg = f.Error.Message
			}
			s.send(CaptureEvent{Kind: CaptureStopped, Reason: "error", Error: msg})
			return
		}
		s.send(*f.CaptureEvent)
		if f.CaptureEvent.Kind == CaptureStopped {
			return
		}
	}
}

// OpenCapture starts a capture; see Client.OpenCapture. It uses a connection of its own, so it neither affects nor
// is affected by this Redialing's request connection.
func (r *Redialing) OpenCapture(ctx context.Context, o CaptureOpen) (CaptureStream, error) {
	c, err := r.client(ctx)
	if err != nil {
		return nil, err
	}
	return c.OpenCapture(ctx, o)
}
