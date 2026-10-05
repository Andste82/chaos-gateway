package executor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Redialing is a client for a long-running process (the API server): it connects when the first
// request comes and connects again when the connection broke, so an executor that restarts is
// found again without restarting its clients. An error the executor itself reports
// (*RemoteError: an operation failed) keeps the connection; any other error — the connection broke,
// the request was cancelled half-way — drops it, because it may be out of step with the executor.
type Redialing struct {
	path string
	opt  DialOptions

	mu sync.Mutex
	c  *Client
}

// NewRedialing creates the client; nothing is connected yet.
func NewRedialing(path string, opt DialOptions) *Redialing { return &Redialing{path: path, opt: opt} }

func (r *Redialing) client(ctx context.Context) (*Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c != nil {
		return r.c, nil
	}
	c, err := Dial(ctx, r.path, r.opt)
	if err != nil {
		return nil, err
	}
	r.c = c
	return c, nil
}

func (r *Redialing) drop(c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == c {
		r.c = nil
	}
	_ = c.Close()
}

// Do runs the operations as one request.
func (r *Redialing) Do(ctx context.Context, ops ...Operation) (Outcome, error) {
	c, err := r.client(ctx)
	if err != nil {
		return Outcome{}, err
	}
	out, err := c.Do(ctx, ops...)
	var re *RemoteError
	if err != nil && !errors.As(err, &re) {
		r.drop(c)
	}
	return out, err
}

// Watch starts a watch on a connection of its own; see Client.Watch. It does not affect, and is
// not affected by, this Redialing's own request connection: a watch that ends (the executor
// restarted, the connection broke) is simply not reconnected here — the caller (the engine's
// FollowConntrack) calls Watch again, the same way it already handles a dropped netlink watch.
func (r *Redialing) Watch(ctx context.Context, what, ns string) (<-chan json.RawMessage, func(), error) {
	c, err := r.client(ctx)
	if err != nil {
		return nil, nil, err
	}
	return c.Watch(ctx, what, ns)
}

// Close closes the connection.
func (r *Redialing) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return nil
	}
	err := r.c.Close()
	r.c = nil
	return err
}
