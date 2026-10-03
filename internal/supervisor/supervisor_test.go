package supervisor

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestAPanicIsRecoveredAndReportedUnhealthy(t *testing.T) {
	s := New(nil, nil)
	s.Go(context.Background(), "boom", func(context.Context) error { panic("kaput") })
	s.Wait()
	h := s.Health()
	if len(h) != 1 || h[0].State != Panicked || h[0].Err != "kaput" || s.Healthy() {
		t.Fatalf("%+v", h)
	}
}

func TestACriticalGoroutineEndsTheProcessOnPanicAndOnError(t *testing.T) {
	var got []string
	s := New(nil, func(name string, v any) { got = append(got, name) })
	s.Critical(context.Background(), "owner", func(context.Context) error { panic("x") })
	s.Wait()
	s.Critical(context.Background(), "loop", func(context.Context) error { return errors.New("died") })
	s.Wait()
	if len(got) != 2 || got[0] != "owner" || got[1] != "loop" {
		t.Fatalf("fatal calls %v", got)
	}
}

func TestAContextEndIsNotAFailure(t *testing.T) {
	var fatal bool
	s := New(nil, func(string, any) { fatal = true })
	ctx, cancel := context.WithCancel(context.Background())
	s.Critical(ctx, "loop", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	cancel()
	s.Wait()
	if fatal || !s.Healthy() || s.Health()[0].State != Stopped {
		t.Fatalf("%+v fatal=%v", s.Health(), fatal)
	}
}

func TestHealthIsSortedAndAReturnIsStopped(t *testing.T) {
	s := New(nil, nil)
	s.Go(context.Background(), "b", func(context.Context) error { return nil })
	s.Go(context.Background(), "a", func(context.Context) error { return errors.New("no") })
	s.Wait()
	h := s.Health()
	if h[0].Name != "a" || h[0].State != Failed || h[1].State != Stopped {
		t.Fatalf("%+v", h)
	}
}
