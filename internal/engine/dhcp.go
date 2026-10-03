package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// DHCP is the DHCP server the engine configures and reads leases from (plan §2.7). Kea implements it;
// tests use a fake.
type DHCP interface {
	// Apply makes the server run the target's configuration; nil means no scope at all (the server
	// answers nobody).
	Apply(ctx context.Context, t *compiler.KeaTarget) error
	// Leases returns the server's current leases.
	Leases(ctx context.Context) ([]kea.Lease, error)
}

// KeaDHCP is the DHCP interface on Kea's control socket.
type KeaDHCP struct {
	Client *kea.Client

	mu   sync.Mutex
	last string // hash of the document sent last
}

// Apply sends the configuration with `config-test` and `config-set`; an unchanged one is not sent again.
func (k *KeaDHCP) Apply(ctx context.Context, t *compiler.KeaTarget) error {
	var cfg kea.Config
	if t != nil {
		cfg = t.Config
	}
	text, err := cfg.Render()
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(text))
	h := hex.EncodeToString(sum[:])
	k.mu.Lock()
	same := k.last == h
	k.mu.Unlock()
	if same {
		return nil
	}
	doc, err := cfg.Document()
	if err != nil {
		return err
	}
	if err := k.Client.Apply(ctx, doc); err != nil {
		return err
	}
	k.mu.Lock()
	k.last = h
	k.mu.Unlock()
	return nil
}

// Leases returns the leases of the running server.
func (k *KeaDHCP) Leases(ctx context.Context) ([]kea.Lease, error) { return k.Client.Leases(ctx) }

// dhcpRetry is how often a configuration that Kea did not take is sent again.
const dhcpRetry = 5 * time.Second

// leasesOf maps Kea's leases to the model, with the network of each lease from its subnet id.
func leasesOf(ls []kea.Lease, networks map[int]string, now time.Time) []model.DhcpLease {
	out := make([]model.DhcpLease, 0, len(ls))
	for _, l := range ls {
		net, ok := networks[l.SubnetID]
		if !ok {
			continue // a lease of a subnet that is not configured any more
		}
		nid, err := parseUUID(net)
		if err != nil {
			continue
		}
		state := model.DhcpLeaseState("active")
		switch {
		case l.State == 1:
			state = "declined"
		case l.State == 2 || !l.ExpiresAt().After(now):
			state = "expired"
		}
		dl := model.DhcpLease{Ip: l.IP, Mac: l.MAC, Network: nid, ExpiresAt: l.ExpiresAt(), State: &state}
		if l.Hostname != "" {
			h := l.Hostname
			dl.Hostname = &h
		}
		out = append(out, dl)
	}
	return out
}

func (e *Engine) dhcpDescribe(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("the DHCP server could not be configured: %v", err)
}

func parseUUID(s string) (model.Uuid, error) { return uuid.Parse(s) }

// dhcpState is what the engine wants the DHCP server to run and whether it does.
type dhcpState struct {
	mu     sync.Mutex
	target *compiler.KeaTarget
	wanted bool
	err    string
}

func (d *dhcpState) set(t *compiler.KeaTarget, err string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.target, d.wanted, d.err = t, true, err
}

func (d *dhcpState) errorNow() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// syncDHCP hands the target's DHCP configuration to the server. A server that is down does not fail
// the apply of the kernel state (a stopped Kea container must not block every change); the problem is
// reported in the snapshot and the configuration is sent again until the server takes it.
func (e *Engine) syncDHCP(ctx context.Context, t *compiler.Target) string {
	if e.cfg.DHCP == nil {
		return ""
	}
	err := e.cfg.DHCP.Apply(ctx, t.Kea)
	msg := e.dhcpDescribe(err)
	e.dhcp.set(t.Kea, msg)
	if err != nil {
		e.cfg.Log.Warn("the DHCP server does not take the configuration", "error", err)
	}
	return msg
}

// runDHCPRetry sends the configuration again while the server has not taken it.
func (e *Engine) runDHCPRetry(ctx context.Context) error {
	tick := e.cfg.Clock.NewTicker(dhcpRetry)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C():
		}
		e.dhcp.mu.Lock()
		wanted, t, had := e.dhcp.wanted, e.dhcp.target, e.dhcp.err
		e.dhcp.mu.Unlock()
		if !wanted || had == "" {
			continue
		}
		err := e.cfg.DHCP.Apply(ctx, t)
		msg := e.dhcpDescribe(err)
		e.dhcp.mu.Lock()
		if e.dhcp.target == t { // nothing newer arrived while the server was asked
			e.dhcp.err = msg
		}
		e.dhcp.mu.Unlock()
		if msg != had {
			_ = e.send(ctx, cmdDHCPStatus{err: msg})
		}
	}
}
