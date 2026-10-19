package ctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// defaultPollTimeout bounds one request of a wait (a poll of the run or the device, a renewal of a lease). A gateway
// that accepts the connection and does not answer must neither hold the wait past its --timeout nor delay the abort
// of a run after a signal by the 2 minutes a request may take (a create or an export call keeps those).
const defaultPollTimeout = 10 * time.Second

// startGrace is how long the request that creates a run may go on after a signal arrived: the run it makes is then
// known and can be aborted, and a healthy gateway answers in milliseconds.
const startGrace = 5 * time.Second

func (a *app) pollTimeout() time.Duration {
	if a.env.PollTimeout > 0 {
		return a.env.PollTimeout
	}
	return defaultPollTimeout
}

// signalError is the cause of a wait that ended because a signal arrived.
type signalError struct{ sig os.Signal }

func (e *signalError) Error() string { return fmt.Sprintf("%v received", e.sig) }

// deadlineError is the cause of a wait that ended because its --timeout ran out.
type deadlineError struct{ error }

// watch returns a context that ends with ctx and, with the cause, when a signal arrives or the deadline fires: the
// requests of a wait run under it, so none of them outlives what ends the wait. The caller cancels it (with
// context.Canceled) when it is done.
func watch(ctx context.Context, sigs <-chan os.Signal, deadline <-chan time.Time, onDeadline error) (context.Context, context.CancelCauseFunc) {
	wctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case sig := <-sigs:
			cancel(&signalError{sig})
		case <-deadline:
			cancel(&deadlineError{onDeadline})
		case <-wctx.Done():
		}
	}()
	return wctx, cancel
}

// interruption says what ended a watched context ("" while it goes on).
func interruption(wctx context.Context) string {
	if wctx.Err() == nil {
		return ""
	}
	cause := context.Cause(wctx)
	var se *signalError
	var de *deadlineError
	switch {
	case errors.As(cause, &se), errors.As(cause, &de):
		return cause.Error()
	}
	return "interrupted"
}

// guarded runs f, which starts something (the request that creates a run), under a context that ends with ctx and,
// grace after a signal, with the signal. It returns the signal that arrived while f ran, if any.
func (a *app) guarded(ctx context.Context, sigs <-chan os.Signal, grace time.Duration, f func(context.Context) error) (os.Signal, error) {
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()
	got := make(chan os.Signal, 1)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case sig := <-sigs:
			got <- sig
			if grace > 0 {
				t := a.clk().NewTimer(grace)
				defer t.Stop()
				select {
				case <-t.C():
				case <-done:
					return
				}
			}
			cancel()
		case <-done:
		}
	}()
	err := f(gctx)
	close(done)
	wg.Wait()
	select {
	case sig := <-got:
		return sig, err
	default:
		return nil, err
	}
}
