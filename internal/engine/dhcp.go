package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
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
	// Test checks a configuration with Kea's own config-test, without applying it, so the preview can
	// show a rejection (a managed option code, bad option data) before an apply would hit it.
	Test(ctx context.Context, t *compiler.KeaTarget) error
	// Reopen is Apply that sends the configuration even when the server runs it already: Kea detects
	// its interfaces and opens its sockets only while it is configured (#64).
	Reopen(ctx context.Context, t *compiler.KeaTarget) error
}

// KeaDHCP is the DHCP interface on Kea's control socket.
type KeaDHCP struct {
	Client *kea.Client
	// Base is the configuration without scopes (DHCP off for every network): it carries the paths and
	// the hook script the server runs with. Its zero value is Kea's default container paths.
	Base kea.Config

	mu   sync.Mutex
	last string // hash of the document sent last
	// running is the hash Kea reported after it took that document: a Kea that restarted reports
	// another one, and the document is sent again
	running string
}

// Apply sends the configuration with `config-test` and `config-set`; an unchanged one is not sent again.
func (k *KeaDHCP) Apply(ctx context.Context, t *compiler.KeaTarget) error {
	return k.apply(ctx, t, false)
}

// Reopen sends the configuration even when Kea runs it: Kea opens a socket on each of its interfaces
// that is up and running now.
func (k *KeaDHCP) Reopen(ctx context.Context, t *compiler.KeaTarget) error {
	return k.apply(ctx, t, true)
}

func (k *KeaDHCP) apply(ctx context.Context, t *compiler.KeaTarget, force bool) error {
	cfg := k.Base
	if t != nil {
		cfg = t.Config
		// the paths of this installation (Kea restricts them; a test runs Kea elsewhere)
		if k.Base.Socket != "" {
			cfg.Socket = k.Base.Socket
		}
		if k.Base.Leases != "" {
			cfg.Leases = k.Base.Leases
		}
	}
	text, err := cfg.Render()
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(text))
	h := hex.EncodeToString(sum[:])
	k.mu.Lock()
	same, want := k.last == h, k.running
	k.mu.Unlock()
	if same && !force {
		now, err := k.Client.Hash(ctx)
		if err != nil {
			return err
		}
		if now == want {
			return nil
		}
	}
	doc, err := cfg.Document()
	if err != nil {
		return err
	}
	err = k.Client.Apply(ctx, doc)
	if kea.ErrNoInterface(err) {
		// the bridge came after Kea started: a configuration without subnets makes Kea detect the
		// interfaces again, then the real one finds it
		base, berr := k.Base.Document()
		if berr == nil && k.Client.SetConfig(ctx, base) == nil {
			err = k.Client.Apply(ctx, doc)
		}
	}
	if err != nil {
		return err
	}
	running, err := k.Client.Hash(ctx)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.last, k.running = h, running
	k.mu.Unlock()
	return nil
}

// Leases returns the leases of the running server.
func (k *KeaDHCP) Leases(ctx context.Context) ([]kea.Lease, error) { return k.Client.Leases(ctx) }

// Test checks the target's configuration with Kea's config-test, without applying it.
func (k *KeaDHCP) Test(ctx context.Context, t *compiler.KeaTarget) error {
	cfg := k.Base
	if t != nil {
		cfg = t.Config
		if k.Base.Socket != "" {
			cfg.Socket = k.Base.Socket
		}
		if k.Base.Leases != "" {
			cfg.Leases = k.Base.Leases
		}
	}
	doc, err := cfg.Document()
	if err != nil {
		return err
	}
	return k.Client.TestConfig(ctx, doc)
}

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
	// sending is held while a configuration is sent, so the server ends up with the newest target
	// whichever of the apply, the retry and a link change sends last
	sending sync.Mutex
	mu      sync.Mutex
	target  *compiler.KeaTarget
	wanted  bool
	err     string
	// links is the state of the target's interfaces at the last reading of the host, nil before the
	// first one
	links map[string]apply.LinkState
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
	e.dhcp.sending.Lock()
	err := e.cfg.DHCP.Apply(ctx, t.Kea)
	msg := e.dhcpDescribe(err)
	e.dhcp.set(t.Kea, msg)
	e.dhcp.sending.Unlock()
	if err != nil {
		e.cfg.Log.Warn("the DHCP server does not take the configuration", "error", err)
	}
	return msg
}

// runDHCPRetry sends the configuration again while the server has not taken it, and when the server
// restarted and forgot it: Apply is cheap while the server runs what it was given.
func (e *Engine) runDHCPRetry(ctx context.Context) error {
	tick := e.cfg.Clock.NewTicker(dhcpRetry)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C():
		}
		if msg, had, sent := e.resendDHCP(ctx, false); sent && msg != had {
			_ = e.send(ctx, cmdDHCPStatus{err: msg})
		}
	}
}

// followDHCPLinks configures the DHCP server again when one of its interfaces became running or was
// created again since the last reading of the host (#64). Kea opens its packet socket on an interface
// only while it is configured and skips one that is down or has no carrier: a bridge whose test port
// flapped while a configuration arrived, or one that had no port up for longer than Kea tries again
// on its own (kea.SocketRetries), would stay without a socket, and DHCP silent on that network.
func (e *Engine) followDHCPLinks(ctx context.Context, links map[string]apply.LinkState) {
	if e.cfg.DHCP == nil {
		return
	}
	e.dhcp.mu.Lock()
	t, wanted, prev := e.dhcp.target, e.dhcp.wanted, e.dhcp.links
	now := map[string]apply.LinkState{}
	if t != nil {
		for _, s := range t.Config.Subnets {
			if l, ok := links[s.Interface]; ok {
				now[s.Interface] = l
			}
		}
	}
	e.dhcp.links = now
	e.dhcp.mu.Unlock()
	if !wanted || t == nil || prev == nil {
		return
	}
	var came []string
	for name, l := range now {
		if p, ok := prev[name]; l.Running && (!ok || !p.Running || p.Index != l.Index) {
			came = append(came, name)
		}
	}
	if len(came) == 0 {
		return
	}
	sort.Strings(came)
	e.cfg.Log.Info("an interface of the DHCP server came up: the server is configured again to listen on it", "interfaces", came)
	msg, had, _ := e.resendDHCP(ctx, true)
	if msg != "" {
		e.cfg.Log.Warn("the DHCP server does not take the configuration", "error", msg)
	}
	if msg != had {
		_ = e.send(ctx, cmdDHCPStatus{err: msg})
	}
}

// resendDHCP sends the current target to the server again (with Reopen when reopen is set) and
// records the outcome; sent is false while nothing was applied yet.
func (e *Engine) resendDHCP(ctx context.Context, reopen bool) (msg, had string, sent bool) {
	e.dhcp.sending.Lock()
	defer e.dhcp.sending.Unlock()
	e.dhcp.mu.Lock()
	wanted, t, had := e.dhcp.wanted, e.dhcp.target, e.dhcp.err
	e.dhcp.mu.Unlock()
	if !wanted {
		return "", had, false
	}
	var err error
	if reopen {
		err = e.cfg.DHCP.Reopen(ctx, t)
	} else {
		err = e.cfg.DHCP.Apply(ctx, t)
	}
	msg = e.dhcpDescribe(err)
	e.dhcp.mu.Lock()
	e.dhcp.err = msg
	e.dhcp.mu.Unlock()
	return msg, had, true
}
