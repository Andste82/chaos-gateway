package api_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/audit"
)

// auditEntries returns the entries of an action, newest first.
func (g *gw) auditEntries(action string) []audit.Entry {
	g.t.Helper()
	items, _, err := g.log.List(audit.Filter{Action: action}, "", 100)
	if err != nil {
		g.t.Fatal(err)
	}
	return items
}

// waitAudit waits for an entry of an action (the server records the engine's events from a goroutine of its own).
func (g *gw) waitAudit(action string) []audit.Entry {
	g.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if items := g.auditEntries(action); len(items) > 0 {
			return items
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("no audit entry %s", action)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// R2 and R4 of the audit of Phase 7: the engine publishes the rollback of a revision that waited for confirmation when the
// process ended while it starts, which is before the server exists, and a server that subscribed only afterwards missed it:
// no audit entry, and the setup stayed closed with an admin password and no configuration (docs/recovery.md, "A change locked
// me out").
func TestAStartAfterAnUnconfirmedSetupAuditsTheRollbackAndReopensTheSetup(t *testing.T) {
	root := t.TempDir()
	g1 := newGW(t, func(o *options) { o.root = root })
	r := g1.do("POST", "/setup", fixtureJSON(t), map[string]string{"X-Setup-Token": g1.setup}, nil)
	if r.Status != 200 || r.json(t)["status"] != "pending_confirm" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if !g1.au.SetupCompleted() {
		t.Fatal("the setup is not completed")
	}
	g1.close() // the process ends inside the confirmation window

	g2 := newGW(t, func(o *options) { o.root = root })
	items := g2.waitAudit("revision.rolled_back")
	if len(items) != 1 || items[0].Revision != 1 || items[0].Detail != "restart" || items[0].Actor.Type != "system" {
		t.Fatalf("%+v", items)
	}
	if g2.au.SetupCompleted() {
		t.Error("the setup is still completed: an admin password without a configuration")
	}
	if g2.activeID() != 0 {
		t.Errorf("active %d", g2.activeID())
	}
}

// R4 of the audit of Phase 7: safe mode is audited, the entering with the reason and the end of it.
func TestSafeModeAndItsEndAreInTheAuditLog(t *testing.T) {
	g, broken, _ := inSafeMode(t, 0)
	items := g.waitAudit("service.safe_mode")
	if len(items) != 1 || items[0].Actor.Type != "system" || !strings.Contains(items[0].Detail, "revision 2 could not be applied") {
		t.Fatalf("%+v", items)
	}
	broken.fixed.Store(true)
	cl := g.do("POST", "/revisions/1/clone", nil, ifMatch(g.activeID()), nil)
	if cl.Status != 201 {
		t.Fatalf("clone: %d %s", cl.Status, cl.Body)
	}
	id := int64(cl.json(t)["id"].(float64))
	if a := g.apply(id); a.Status != 200 {
		t.Fatalf("apply: %d %s", a.Status, a.Body)
	}
	if cf := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); cf.Status != 200 {
		t.Fatalf("confirm: %d %s", cf.Status, cf.Body)
	}
	if items := g.waitAudit("service.safe_mode_ended"); len(items) != 1 || items[0].Actor.Type != "system" {
		t.Fatalf("%+v", items)
	}
}

// R4: safe mode at the start is published before the server exists, so the server has to replay it.
func TestSafeModeAtStartIsInTheAuditLog(t *testing.T) {
	root := t.TempDir()
	g1 := newGW(t, func(o *options) { o.root = root })
	g1.finishSetup()
	g1.close()

	// the only revision is damaged: the checksum notices, there is nothing earlier to fall back to
	p := filepath.Join(root, "revisions", "revisions", fmt.Sprintf("%06d.json", 1))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "IoT") {
		t.Fatal("the fixture has no network IoT to damage")
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(raw), "IoT", "IoX", 1)), 0o640); err != nil {
		t.Fatal(err)
	}

	g2 := newGW(t, func(o *options) { o.root = root })
	if !g2.e.Snapshot().SafeMode {
		t.Fatal("the gateway is not in safe mode")
	}
	items := g2.waitAudit("service.safe_mode")
	if len(items) != 1 || items[0].Actor.Type != "system" || !strings.Contains(items[0].Detail, "cannot be read") {
		t.Fatalf("%+v", items)
	}
}
