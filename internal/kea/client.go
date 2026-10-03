package kea

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Client talks to Kea's control socket: one JSON command per connection, one JSON answer.
type Client struct {
	// Socket is the path of the Unix socket.
	Socket string
	// Timeout bounds one command; default 10 s.
	Timeout time.Duration
}

// Answer is Kea's reply to a command.
type Answer struct {
	Result    int             `json:"result"`
	Text      string          `json:"text"`
	Arguments json.RawMessage `json:"arguments"`
}

// Error is a command Kea refused (result other than 0; 3 means "empty": no such object).
type Error struct {
	Command string
	Result  int
	Text    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("kea: %s: result %d: %s", e.Command, e.Result, e.Text)
}

// Empty reports whether the command found nothing (result 3), which is not a failure for a lookup.
func (e *Error) Empty() bool { return e.Result == 3 }

// Do sends a command and returns the answer; a result other than 0 is an *Error.
func (c *Client) Do(ctx context.Context, command string, args any) (*Answer, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, fmt.Errorf("kea: the control socket %s: %w", c.Socket, err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	msg := map[string]any{"command": command}
	if args != nil {
		msg["arguments"] = args
	}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return nil, err
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		return nil, fmt.Errorf("kea: %s: %w", command, err)
	}
	var a Answer
	// the daemon answers with an object, the control agent with a list of them
	if err := json.Unmarshal(raw, &a); err != nil {
		var list []Answer
		if err2 := json.Unmarshal(raw, &list); err2 != nil || len(list) == 0 {
			return nil, fmt.Errorf("kea: %s: unreadable answer: %w", command, err)
		}
		a = list[0]
	}
	if a.Result != 0 {
		return &a, &Error{Command: command, Result: a.Result, Text: a.Text}
	}
	return &a, nil
}

// SetConfig replaces the running configuration (`config-set`) with the document of cfg. Kea checks
// it before it switches, so a rejected configuration leaves the running one in place.
func (c *Client) SetConfig(ctx context.Context, doc map[string]any) error {
	_, err := c.Do(ctx, "config-set", doc)
	return err
}

// TestConfig lets Kea check a configuration without applying it (`config-test`).
func (c *Client) TestConfig(ctx context.Context, doc map[string]any) error {
	_, err := c.Do(ctx, "config-test", doc)
	return err
}

// Apply checks a configuration with `config-test` and only then replaces the running one: a failed
// `config-set` can leave the server without its lease database, a failed check changes nothing.
func (c *Client) Apply(ctx context.Context, doc map[string]any) error {
	if err := c.TestConfig(ctx, doc); err != nil {
		return err
	}
	return c.SetConfig(ctx, doc)
}

// Hash returns the hash of the running configuration (`config-hash-get`).
func (c *Client) Hash(ctx context.Context) (string, error) {
	a, err := c.Do(ctx, "config-hash-get", nil)
	if err != nil {
		return "", err
	}
	var v struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(a.Arguments, &v); err != nil {
		return "", err
	}
	return v.Hash, nil
}

// RunningSubnet is a subnet of the running configuration.
type RunningSubnet struct {
	ID           int                     `json:"id"`
	Subnet       string                  `json:"subnet"`
	Interface    string                  `json:"interface"`
	Pools        []struct{ Pool string } `json:"pools"`
	Reservations []struct {
		HW string `json:"hw-address"`
		IP string `json:"ip-address"`
	} `json:"reservations"`
}

// Subnets returns the subnets of the running configuration (`config-get`).
func (c *Client) Subnets(ctx context.Context) ([]RunningSubnet, error) {
	a, err := c.Do(ctx, "config-get", nil)
	if err != nil {
		return nil, err
	}
	var v struct {
		Dhcp4 struct {
			Subnet4 []RunningSubnet `json:"subnet4"`
		} `json:"Dhcp4"`
	}
	if err := json.Unmarshal(a.Arguments, &v); err != nil {
		return nil, err
	}
	return v.Dhcp4.Subnet4, nil
}

// Lease is a lease as Kea reports it.
type Lease struct {
	IP       string `json:"ip-address"`
	MAC      string `json:"hw-address"`
	ClientID string `json:"client-id"`
	Hostname string `json:"hostname"`
	SubnetID int    `json:"subnet-id"`
	ValidLft int64  `json:"valid-lft"`
	CLTT     int64  `json:"cltt"`
	State    int    `json:"state"` // 0 default, 1 declined, 2 expired-reclaimed
}

// ExpiresAt is when the lease ends.
func (l Lease) ExpiresAt() time.Time { return time.Unix(l.CLTT+l.ValidLft, 0).UTC() }

// Leases returns all IPv4 leases (`lease4-get-all`).
func (c *Client) Leases(ctx context.Context) ([]Lease, error) {
	a, err := c.Do(ctx, "lease4-get-all", nil)
	var ke *Error
	if errors.As(err, &ke) && ke.Empty() {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v struct {
		Leases []Lease `json:"leases"`
	}
	if err := json.Unmarshal(a.Arguments, &v); err != nil {
		return nil, err
	}
	return v.Leases, nil
}

// DeleteLease deletes the lease of an address (`lease4-del`). Deleting a lease does not force a
// new address: the client that renews gets the same one again (spike S6).
func (c *Client) DeleteLease(ctx context.Context, ip string) error {
	_, err := c.Do(ctx, "lease4-del", map[string]any{"ip-address": ip})
	var ke *Error
	if errors.As(err, &ke) && ke.Empty() {
		return nil
	}
	return err
}

// ErrNoInterface is the error of a configuration that names an interface Kea does not know: one
// that was created after Kea started. Kea learns about it with a configuration that re-detects.
func ErrNoInterface(err error) bool {
	return err != nil && strings.Contains(err.Error(), "is not present in the system")
}
