//go:build testbed

package executor

import (
	"context"
	"testing"
)

// The API container's client reconnects; a capture goes over a connection of its own and ends like any other.
func TestARedialingClientOpensCapturesToo(t *testing.T) {
	path, _, root := startCaptureServer(t)
	mkCapDir(t, root, "c1")
	r := NewRedialing(path, selfOpts())
	defer func() { _ = r.Close() }()
	s, err := r.OpenCapture(context.Background(), loOpen("c1"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ev := next(t, s); ev.Kind != CaptureStarted {
		t.Fatalf("%+v", ev)
	}
	s.Stop("user")
	if end := untilStopped(t, s); end.Reason != "user" {
		t.Fatalf("%+v", end)
	}
}
