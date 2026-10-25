package capturemgr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/capturemgr"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The TLS key log of a capture (plan §2.8, §2.11, M22): which clients a scope covers, which captures take the secrets of a connection, what a
// capture with secrets says of itself and what is left of the log after the capture.

func scopeOf(t *testing.T, doc string) model.CaptureScope {
	t.Helper()
	var s model.CaptureScope
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// A scope covers the clients it records. A selector counts for its source only, the uplink for every client, and the clients of a WireGuard
// network of the role management for none.
func TestAScopeCoversTheClientsItRecords(t *testing.T) {
	const netAdmin = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"
	snap := snapshot(t)
	nets := *snap.Config.Networks
	nets[netAdmin] = network(t, `{"type":"wireguard","name":"admin","kind":"hub","role":"management","address":"10.98.0.1/24","listen_port":51822}`)
	snap.WireGuardInterfaces = append(snap.WireGuardInterfaces, compiler.WGInterface{Name: "wg-admin", NetworkID: netAdmin, NetworkName: "admin", Kind: "hub", Role: "management", Address: netip.MustParsePrefix("10.98.0.1/24")})
	snap.Devices = append(snap.Devices, engine.DeviceState{ID: "d-admin", Name: "admin-laptop", Network: netAdmin, Addresses: []netip.Addr{netip.MustParseAddr("10.98.0.2")}})
	groups := *snap.Config.Groups
	groups["g2"] = model.Group{Name: "mixed", Members: &[]model.Ref{"d-mac", "d-admin"}}
	// the world is the networks of the configuration; the groups and devices come from the snapshot
	w, err := domain.NewWorld(&model.Configuration{Networks: snap.Config.Networks}, nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddr
	mac, ip, wg, admin, stranger, elsewhere := addr("10.10.0.5"), addr("10.10.0.77"), addr("10.99.0.2"), addr("10.98.0.2"), addr("10.10.0.200"), addr("192.0.2.9")

	for _, c := range []struct {
		name   string
		scope  string
		covers []netip.Addr
		not    []netip.Addr
	}{
		{"a device", `{"device":"with-mac"}`, []netip.Addr{mac}, []netip.Addr{ip, wg, stranger, elsewhere}},
		{"a device by its address", `{"device":"behind-a-router"}`, []netip.Addr{ip}, []netip.Addr{mac}},
		{"a network", `{"network":"IoT"}`, []netip.Addr{mac, ip, stranger}, []netip.Addr{wg, elsewhere}},
		{"a WireGuard network", `{"network":"lab-hub"}`, []netip.Addr{wg}, []netip.Addr{mac}},
		{"the uplink", `{"uplink":true}`, []netip.Addr{mac, wg, stranger, elsewhere}, []netip.Addr{admin}},
		{"a selector of a device", `{"selector":{"source":{"device":"with-mac"},"protocol":"tcp","ports":[8883]}}`, []netip.Addr{mac}, []netip.Addr{ip}},
		{"a selector of a group", `{"selector":{"source":{"group":"sensors"}}}`, []netip.Addr{mac, ip, wg}, []netip.Addr{stranger}},
		{"a selector of a network", `{"selector":{"source":{"network":"IoT"},"destination":{"cidr":"203.0.113.0/24"}}}`, []netip.Addr{mac, stranger}, []netip.Addr{wg}},
		{"a selector of every test network", `{"selector":{"source":{"global":true}}}`, []netip.Addr{mac, ip, wg, stranger}, []netip.Addr{elsewhere, admin}},
		{"a group with a management client", `{"selector":{"source":{"group":"mixed"}}}`, []netip.Addr{mac}, []netip.Addr{admin}},
	} {
		for _, a := range c.covers {
			if !capturemgr.Covers(snap, w, scopeOf(t, c.scope), a) {
				t.Errorf("%s does not cover %s", c.name, a)
			}
		}
		for _, a := range c.not {
			if capturemgr.Covers(snap, w, scopeOf(t, c.scope), a) {
				t.Errorf("%s covers %s", c.name, a)
			}
		}
	}
	// the admin's tunnel is never covered, whatever the scope
	if capturemgr.Covers(snap, w, scopeOf(t, `{"uplink":true}`), admin) || capturemgr.Covers(snap, w, scopeOf(t, `{"selector":{"source":{"global":true}}}`), admin) {
		t.Error("a capture holds the secrets of the admin's tunnel")
	}
	// a scope that names something the gateway does not know covers nothing
	if capturemgr.Covers(snap, w, scopeOf(t, `{"device":"nobody"}`), mac) || capturemgr.Covers(snap, w, scopeOf(t, `{"network":"nowhere"}`), mac) {
		t.Error("an unknown name covers a client")
	}
	// without the world of the configuration only the scopes that need none are decided
	if !capturemgr.Covers(snap, nil, scopeOf(t, `{"device":"with-mac"}`), mac) || capturemgr.Covers(snap, nil, scopeOf(t, `{"network":"IoT"}`), mac) {
		t.Error("a missing world")
	}
}

// The key log of a capture holds what was given while it ran and covered the client, one line each, marks the capture, and is the file of the
// download; a capture that never got a line has none, one that ended takes no more, and a delete takes the file.
func TestACaptureKeepsTheKeyLogItWasGivenWhileItRan(t *testing.T) {
	dir := t.TempDir()
	m, op, _ := newManager(t, dir)
	ref := func(s string) *model.Ref { return &s }
	dev, err := m.Start(context.Background(), capturemgr.StartRequest{CaptureRequest: model.CaptureRequest{Name: ptr("esp"), Scope: model.CaptureScope{Device: ref("with-mac")}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.Start(context.Background(), capturemgr.StartRequest{CaptureRequest: model.CaptureRequest{Scope: model.CaptureScope{Device: ref("behind-a-router")}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.Capture{dev, other} {
		if *c.Keylog || *c.Sensitive {
			t.Fatalf("a capture starts without secrets: %+v", c)
		}
	}
	if _, err := m.Open(dev.Id, "keylog"); !errors.Is(err, capturemgr.ErrNoKeylog) {
		t.Errorf("before any line: %v", err)
	}
	m.AppendKeylog(netip.MustParseAddr("10.10.0.5"), nil) // nothing to give
	m.AppendKeylog(netip.MustParseAddr("10.10.0.5"), []string{"CLIENT_RANDOM a b", "EXPORTER_SECRET a c"})
	m.AppendKeylog(netip.MustParseAddr("10.10.0.5"), []string{"CLIENT_TRAFFIC_SECRET_0 a d"})

	got, _ := m.Get(dev.Id)
	if !*got.Keylog || !*got.Sensitive {
		t.Errorf("a capture with secrets says so: %+v", got)
	}
	if o, _ := m.Get(other.Id); *o.Keylog {
		t.Errorf("a capture of another device got the secrets: %+v", o)
	}
	read := func(id string) string {
		d, err := m.Open(dev.Id, "keylog")
		if err != nil {
			t.Fatal(err)
		}
		if d.ContentType != "text/plain" || !strings.HasSuffix(d.Name, "-keylog.txt") || !strings.HasPrefix(d.Name, "esp-") {
			t.Errorf("%+v", d)
		}
		var b bytes.Buffer
		if _, err := d.WriteTo(&b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if got := read(""); got != "CLIENT_RANDOM a b\nEXPORTER_SECRET a c\nCLIENT_TRAFFIC_SECRET_0 a d\n" {
		t.Errorf("%q", got)
	}
	// the flag is in the record on disk, so it survives a restart
	m2, _, _ := newManager(t, dir)
	if err := m2.Recover(); err != nil {
		t.Fatal(err)
	}
	if r, err := m2.Get(dev.Id); err != nil || !*r.Keylog || !*r.Sensitive {
		t.Errorf("after a restart: %+v %v", r, err)
	}

	// a stopped capture takes no more lines and keeps what it has
	op.streams[0].end(executor.CaptureEvent{Kind: executor.CaptureStopped, Reason: "user"})
	eventually(t, "the capture stops", func() bool { c, _ := m.Get(dev.Id); return c.Status == model.CaptureStatusStopped })
	m.AppendKeylog(netip.MustParseAddr("10.10.0.5"), []string{"CLIENT_RANDOM late late"})
	if got := read(""); strings.Contains(got, "late") {
		t.Errorf("a capture that ended took a line: %q", got)
	}
	if r, _ := m.Get(dev.Id); !*r.Keylog || !*r.Sensitive {
		t.Errorf("the flags of a stopped capture: %+v", r)
	}
	if err := m.Delete(dev.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, dev.Id.String(), "keylog.txt")); !os.IsNotExist(err) {
		t.Errorf("the key log stays after the delete: %v", err)
	}
	m.Close(context.Background())
}
