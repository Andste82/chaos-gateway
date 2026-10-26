package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	// DeleteLeases deletes the leases of a subnet, or only those of the clients with these MAC addresses (the DHCP
	// action delete_lease), and returns how many it deleted.
	DeleteLeases(ctx context.Context, subnetID int, macs []string) (int, error)
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

// DeleteLeases deletes leases of one subnet on the running server.
func (k *KeaDHCP) DeleteLeases(ctx context.Context, subnetID int, macs []string) (int, error) {
	return k.Client.DeleteLeases(ctx, subnetID, macs)
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
	var de *leaseDeletionError
	if errors.As(err, &de) {
		return fmt.Sprintf("the DHCP server could not delete leases: %v", de.err)
	}
	return fmt.Sprintf("the DHCP server could not be configured: %v", err)
}

// leaseDeletionError is a failed deletion of leases, which is not a failure of the configuration.
type leaseDeletionError struct{ err error }

func (e *leaseDeletionError) Error() string { return e.err.Error() }
func (e *leaseDeletionError) Unwrap() error { return e.err }

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
	// links is the state of the target's interfaces at the last reading of the host, or at the
	// reading of the apply just before the configuration was sent when that came later; nil before
	// the first one
	links map[string]apply.LinkState
	// came are the interfaces that came up since the server was configured last, waiting for
	// runDHCPRetry; reopen wakes it, scheduled counts the times it was woken for them (tests)
	came      map[string]bool
	reopen    chan struct{}
	scheduled int
	// deleted are the keys of the lease deletions that were done (compiler.LeaseDeletion), with their overlay: each is done once,
	// and the key is forgotten when its overlay is gone (forgetLeaseDeletions)
	deleted map[string]string
	// live are the ids of the overlays of the desired state applied last; nil before the first apply
	live map[string]bool
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
//
// after is the kernel state the apply read back just before: the links of the next reading are compared
// with it, so a bridge the apply created is not taken for one that came up (the server sees it when it
// takes this configuration), while a carrier that changes from here on, during the send included, is.
// Apply does not send a configuration the server runs already, so a served interface that came up or
// was created again since the last reading (the apply created a deleted bridge again) is compared here
// first, and the configuration is sent with Reopen for it (#64).
func (e *Engine) syncDHCP(ctx context.Context, t *compiler.Target, after *apply.State) string {
	if e.cfg.DHCP == nil {
		return ""
	}
	e.dhcp.sending.Lock()
	var came []string
	if after != nil {
		links := after.LinkStates()
		e.dhcp.mu.Lock()
		if prev := e.dhcp.links; e.dhcp.wanted && prev != nil {
			// the interfaces the running configuration serves; a new one is in a changed configuration,
			// which is sent anyway
			came = cameUp(prev, servedLinks(e.dhcp.target, links))
		}
		e.dhcp.links = servedLinks(t.Kea, links)
		e.dhcp.mu.Unlock()
	}
	var err error
	if len(came) > 0 {
		e.cfg.Log.Info("an interface of the DHCP server came up: the server is configured again to listen on it", "interfaces", came)
		err = e.cfg.DHCP.Reopen(ctx, t.Kea)
	} else {
		err = e.cfg.DHCP.Apply(ctx, t.Kea)
	}
	if err == nil {
		err = e.deleteLeases(ctx, t.Kea)
	}
	msg := e.dhcpDescribe(err)
	e.dhcp.set(t.Kea, msg)
	e.dhcp.sending.Unlock()
	if err != nil {
		e.cfg.Log.Warn("the DHCP server does not take the configuration", "error", err)
	}
	return msg
}

// runDHCPRetry sends the configuration again while the server has not taken it, and when the server
// restarted and forgot it: Apply is cheap while the server runs what it was given. It also configures
// the server again for the interfaces followDHCPLinks saw come up.
func (e *Engine) runDHCPRetry(ctx context.Context) error {
	tick := e.cfg.Clock.NewTicker(dhcpRetry)
	defer tick.Stop()
	for {
		reopen := false
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C():
		case <-e.dhcp.reopen:
			reopen = true
		}
		if reopen {
			e.dhcp.mu.Lock()
			came := make([]string, 0, len(e.dhcp.came))
			for n := range e.dhcp.came {
				came = append(came, n)
			}
			e.dhcp.came = nil
			e.dhcp.mu.Unlock()
			sort.Strings(came)
			e.cfg.Log.Info("an interface of the DHCP server came up: the server is configured again to listen on it", "interfaces", came)
		}
		msg, had, sent := e.resendDHCP(ctx, reopen)
		if reopen && msg != "" {
			e.cfg.Log.Warn("the DHCP server does not take the configuration", "error", msg)
		}
		if sent && msg != had {
			_ = e.send(ctx, cmdDHCPStatus{err: msg})
		}
	}
}

// followDHCPLinks has the DHCP server configured again when one of its interfaces is running and was
// not, was created again, or had its carrier go and come (a flap that ended before this reading) since
// the last reading of the host or the send of the configuration (#64). Kea opens its packet socket on
// an interface only while it is configured and skips one that is down or has no carrier: a bridge whose
// test port flapped while a configuration arrived would stay without a socket, and DHCP silent on that
// network. The engine does not count on Kea's own retries (kea.SocketRetries) for that.
//
// It only takes note and wakes runDHCPRetry, which sends the configuration: the reading of the host
// that called it (Refresh) does not wait for the DHCP server.
func (e *Engine) followDHCPLinks(links map[string]apply.LinkState) {
	if e.cfg.DHCP == nil {
		return
	}
	e.dhcp.mu.Lock()
	defer e.dhcp.mu.Unlock()
	t, wanted, prev := e.dhcp.target, e.dhcp.wanted, e.dhcp.links
	now := servedLinks(t, links)
	e.dhcp.links = now
	if !wanted || t == nil || prev == nil {
		return
	}
	came := cameUp(prev, now)
	if len(came) == 0 {
		return
	}
	if e.dhcp.came == nil {
		e.dhcp.came = map[string]bool{}
	}
	for _, name := range came {
		e.dhcp.came[name] = true
	}
	e.dhcp.scheduled++
	select {
	case e.dhcp.reopen <- struct{}{}:
	default: // already woken: it takes these interfaces too
	}
}

// cameUp names the interfaces of now that are running and were not in prev: missing, down, with
// another index (created again) or with another count of carrier changes (a flap in between).
func cameUp(prev, now map[string]apply.LinkState) []string {
	var came []string
	for name, l := range now {
		if p, ok := prev[name]; l.Running && (!ok || !p.Running || p.Index != l.Index || p.CarrierChanges != l.CarrierChanges) {
			came = append(came, name)
		}
	}
	sort.Strings(came)
	return came
}

// servedLinks is the state of the interfaces the DHCP configuration serves.
func servedLinks(t *compiler.KeaTarget, links map[string]apply.LinkState) map[string]apply.LinkState {
	now := map[string]apply.LinkState{}
	if t != nil {
		for _, s := range t.Config.Subnets {
			if l, ok := links[s.Interface]; ok {
				now[s.Interface] = l
			}
		}
	}
	return now
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
	if err == nil {
		err = e.deleteLeases(ctx, t)
	}
	msg = e.dhcpDescribe(err)
	e.dhcp.mu.Lock()
	e.dhcp.err = msg
	e.dhcp.mu.Unlock()
	return msg, had, true
}
