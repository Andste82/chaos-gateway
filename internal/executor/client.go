package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/version"
)

// Client talks to the executor over its Unix socket. It is safe for concurrent use; requests on
// one connection are answered in order.
type Client struct {
	mu     sync.Mutex
	conn   net.Conn
	codec  *codec
	nextID uint64
}

// DialOptions tune Dial.
type DialOptions struct {
	// Auth checks the executor's peer credentials; nil requires root.
	Auth func(Cred) error
	// Protocol overrides ProtocolVersion; for tests of the mismatch handling only.
	Protocol int
}

// Dial connects, checks the executor's credentials and runs the version handshake.
func Dial(ctx context.Context, path string, opt DialOptions) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	c, err := handshake(ctx, conn, opt)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func handshake(ctx context.Context, conn net.Conn, opt DialOptions) (*Client, error) {
	auth := opt.Auth
	if auth == nil {
		auth = AllowUIDs()
	}
	cred, err := PeerCred(conn)
	if err != nil {
		return nil, err
	}
	if err := auth(cred); err != nil {
		return nil, fmt.Errorf("the executor socket is served by an untrusted process: %w", err)
	}
	proto := opt.Protocol
	if proto == 0 {
		proto = ProtocolVersion
	}
	deadline := (&clock.Real{}).Now().Add(10 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	cc := newCodec(conn)
	f, err := cc.read()
	if err != nil {
		return nil, fmt.Errorf("executor handshake: %w", err)
	}
	if f.Error != nil {
		return nil, remoteErr(f.Error)
	}
	if f.Hello == nil {
		return nil, errors.New("executor handshake: the first message is not a hello")
	}
	if f.Hello.Protocol != proto {
		return nil, fmt.Errorf("%w: executor speaks %d (version %s), this side speaks %d", ErrProtocolMismatch, f.Hello.Protocol, f.Hello.Version, proto)
	}
	if err := cc.write(&Frame{Hello: &Hello{Protocol: proto, Version: version.Version, Role: "client"}}); err != nil {
		return nil, err
	}
	return &Client{conn: conn, codec: cc}, nil
}

func remoteErr(e *RemoteError) error {
	if e.Code == CodeProtocol {
		return fmt.Errorf("%w: %s", ErrProtocolMismatch, e.Message)
	}
	return e
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Do runs the operations as one request. After a context error the client must be closed. On a failed request the outcome still carries the
// generation and the number of completed operations.
func (c *Client) Do(ctx context.Context, ops ...Operation) (Outcome, error) {
	raw := make([]json.RawMessage, len(ops))
	for i, op := range ops {
		b, err := Encode(op)
		if err != nil {
			return Outcome{}, err
		}
		raw[i] = b
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(dl)
		defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	}
	stop := context.AfterFunc(ctx, func() { _ = c.conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if err := c.codec.write(&Frame{Request: &Request{ID: id, Ops: raw}}); err != nil {
		return Outcome{}, ctxOr(ctx, err)
	}
	f, err := c.codec.read()
	if err != nil {
		return Outcome{}, ctxOr(ctx, err)
	}
	if f.Error != nil {
		return Outcome{}, remoteErr(f.Error)
	}
	if f.Response == nil || f.Response.ID != id {
		return Outcome{}, errors.New("executor sent an unexpected message")
	}
	if !f.Response.OK {
		return f.Response.Outcome, f.Response.Error
	}
	return f.Response.Outcome, nil
}

// ctxOr reports the context's error when the I/O error is a consequence of it. The connection
// deadline can fire a moment before the context notices its own; after either, the connection is
// out of step with the executor and the caller must close the client.
func ctxOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, ok := ctx.Deadline(); ok && errors.Is(err, os.ErrDeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return err
}

// Generation returns the executor's current generation (an empty request).
func (c *Client) Generation(ctx context.Context) (uint64, error) {
	out, err := c.Do(ctx)
	return out.Generation, err
}

// Read runs one Read operation and decodes its result into v.
func (c *Client) Read(ctx context.Context, r Read, v any) (Outcome, error) {
	out, err := c.Do(ctx, &r)
	if err != nil {
		return out, err
	}
	if len(out.Data) != 1 {
		return out, errors.New("executor returned no data")
	}
	return out, json.Unmarshal(out.Data[0], v)
}
