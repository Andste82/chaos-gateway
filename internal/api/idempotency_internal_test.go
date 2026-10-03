package api

import (
	"context"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

func TestAWaiterOnAnInflightKeyHonorsItsContext(t *testing.T) {
	i, err := openIdempotency("", &clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	release, _, _, err := i.begin(context.Background(), "k", "fp")
	if err != nil || release == nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, _, err := i.begin(ctx, "k", "fp"); err == nil {
		t.Fatal("the waiter did not give up")
	}
	// releasing without a result (a panic) frees the key
	release(nil, "fp")
	r2, replay, _, err := i.begin(context.Background(), "k", "fp")
	if err != nil || replay != nil || r2 == nil {
		t.Fatalf("the key is still claimed: %v %v", replay, err)
	}
	r2(nil, "fp")
}
