package api_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// P3-M12-07 (plan §3.4): the preflight checks every interface that is or may become a test interface for
// netplan and NetworkManager configuration, names the file and the entry (or the connection) and says what
// to remove; the setup wizard shows that text (GET /setup), every client gets it from GET
// /system/preflight. Before the setup an OS-configured interface is a warning; after it, a test interface
// that the OS configures fails the preflight. The uplink and the management interface are the operating
// system's and are not checked.
func TestThePreflightNamesTheNetplanEntryOrConnectionOfATestInterface(t *testing.T) {
	host := fstest.MapFS{
		"etc/netplan/50-cloud-init.yaml":                         {Data: []byte("network:\n  version: 2\n  ethernets:\n    lan0:\n      dhcp4: true\n    mgmt0:\n      addresses: [192.168.56.1/24]\n")},
		"etc/NetworkManager/system-connections/lab.nmconnection": {Data: []byte("[connection]\nid=lab port\ntype=ethernet\n\n[ethernet]\nmac-address=02:00:00:00:01:01\n")},
	}
	g := newGW(t, func(o *options) { o.hostFS = host })
	checks := func(report map[string]any) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		for _, c := range report["checks"].([]any) {
			m := c.(map[string]any)
			out[m["id"].(string)] = m
		}
		return out
	}

	setup := g.do("GET", "/setup", nil, nil, nil).json(t)
	managed := map[string]any{}
	for _, i := range setup["interfaces"].([]any) {
		m := i.(map[string]any)
		managed[m["name"].(string)] = m["os_managed"]
	}
	if managed["lan0"] != true || managed["lan1"] != true || managed["mgmt0"] != true || managed["wan0"] != false {
		t.Errorf("os_managed: %v", managed)
	}
	pf := setup["preflight"].(map[string]any)
	cs := checks(pf)
	lan0 := cs["interface.lan0.os_config"]
	if lan0 == nil || lan0["status"] != "warn" || lan0["interface"] != "lan0" ||
		lan0["message"] != `lan0 is configured by netplan (/etc/netplan/50-cloud-init.yaml, "lan0" under "ethernets"); a test interface must not be` ||
		lan0["fix"] != `delete the entry "lan0:" under "ethernets" in /etc/netplan/50-cloud-init.yaml, then run "sudo netplan apply"` {
		t.Errorf("lan0 before the setup: %v", lan0)
	}
	if lan1 := cs["interface.lan1.os_config"]; lan1 == nil || lan1["status"] != "warn" || !strings.Contains(lan1["fix"].(string), `sudo nmcli connection delete "lab port"`) {
		t.Errorf("lan1 before the setup: %v", lan1)
	}
	if wan0 := cs["interface.wan0.os_config"]; wan0 == nil || wan0["status"] != "ok" || wan0["fix"] != nil {
		t.Errorf("wan0 before the setup: %v", wan0)
	}
	// the suggested uplink is a candidate too (the user may make it a test interface) and is checked, but a
	// candidate's warning is not the report's: before the setup the status is that of the host's checks
	if mgmt0 := cs["interface.mgmt0.os_config"]; mgmt0 == nil || mgmt0["status"] != "warn" {
		t.Errorf("mgmt0 before the setup: %v", mgmt0)
	}
	if pf["status"] != "ok" {
		t.Errorf("status before the setup: %v", pf["status"])
	}

	// after the setup lan0 is a test interface (IoT): the check fails; the uplink and the management interface are not checked
	g.finishSetup()
	pf = g.do("GET", "/system/preflight", nil, nil, nil).json(t)
	cs = checks(pf)
	if lan0 := cs["interface.lan0.os_config"]; lan0 == nil || lan0["status"] != "fail" || !strings.HasPrefix(lan0["message"].(string), "lan0 is assigned to a test network but configured by netplan (") {
		t.Errorf("lan0 after the setup: %v", lan0)
	}
	if cs["interface.mgmt0.os_config"] != nil || cs["interface.wan0.os_config"] != nil {
		t.Errorf("the uplink or the management interface is checked: %v", cs)
	}
	if pf["status"] != "fail" || cs["preflight"]["status"] != "warn" {
		t.Errorf("status after the setup: %v (%v)", pf["status"], cs["preflight"])
	}
	for _, i := range g.do("GET", "/system/interfaces", nil, nil, nil).json(t)["items"].([]any) {
		if m := i.(map[string]any); m["name"] == "lan0" && m["os_managed"] != true {
			t.Errorf("lan0: %v", m)
		}
	}
}

// A configuration file the executor cannot read or parse is a warning of its own; the others are still checked.
func TestThePreflightSaysWhichOSConfigurationFileItCannotRead(t *testing.T) {
	g := newGW(t, func(o *options) {
		o.hostFS = fstest.MapFS{
			"etc/netplan/01-bad.yaml": {Data: []byte("network: [\n")},
			"etc/netplan/02-ok.yaml":  {Data: []byte("network:\n  ethernets:\n    lan1: {}\n")},
		}
	})
	pf := g.do("GET", "/setup", nil, nil, nil).json(t)["preflight"].(map[string]any)
	var unreadable, lan1 map[string]any
	for _, c := range pf["checks"].([]any) {
		m := c.(map[string]any)
		switch m["id"] {
		case "interfaces.os_config":
			unreadable = m
		case "interface.lan1.os_config":
			lan1 = m
		}
	}
	if unreadable == nil || unreadable["status"] != "warn" || !strings.Contains(unreadable["message"].(string), "/etc/netplan/01-bad.yaml") {
		t.Errorf("the unreadable file: %v", unreadable)
	}
	if lan1 == nil || lan1["status"] != "warn" {
		t.Errorf("the readable file is still checked: %v", lan1)
	}
}

// A report of the checks made at start that has no time (the host could not be inspected, buildPreflight's
// "host" warning) is a check that ran: GET /system/preflight does not add "no preflight check has run". It
// adds it only when there is no report at all (review B5 round 1).
func TestThePreflightSaysNoCheckRanOnlyWhenNoneDid(t *testing.T) {
	for _, c := range []struct {
		name   string
		report func() *api.PreflightReport
		noRun  bool
	}{
		{"a report without a time", func() *api.PreflightReport {
			return &api.PreflightReport{Status: "warn", Checks: []api.PreflightCheck{{ID: "host", Status: "warn", Message: "the host cannot be inspected from here: no /proc"}}}
		}, false},
		{"no report", func() *api.PreflightReport { return nil }, true},
		{"no preflight", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newGW(t, func(o *options) { o.preflight = c.report })
			g.finishSetup()
			pf := g.do("GET", "/system/preflight", nil, nil, nil).json(t)
			ids := map[string]bool{}
			for _, ch := range pf["checks"].([]any) {
				ids[ch.(map[string]any)["id"].(string)] = true
			}
			if ids["preflight"] != c.noRun || (c.report != nil && c.report() != nil && !ids["host"]) {
				t.Errorf("checks: %v", pf["checks"])
			}
			if pf["status"] != "warn" && pf["status"] != "fail" {
				t.Errorf("status: %v", pf["status"])
			}
		})
	}
}

// failingHostNet fails the executor's read of the host's network configuration while failing is set.
type failingHostNet struct {
	inner   apply.Exec
	failing *atomic.Pointer[string]
}

func (f failingHostNet) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if msg := f.failing.Load(); msg != nil {
		for _, op := range ops {
			if r, ok := op.(*executor.Read); ok && r.What == executor.ReadHostNet {
				return executor.Outcome{}, errors.New(*msg)
			}
		}
	}
	return f.inner.Do(ctx, ops...)
}

// syncBuffer is a log destination the server's goroutines can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) count(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Count(s.b.String(), sub)
}

// A screen polls GET /system/interfaces every few seconds, and each request reads the host's network
// configuration through the executor: a failing read is logged as a warning once, again only when its
// message changes, and its end once (review B5 round 1); every response still says it.
func TestAFailingReadOfTheHostsNetworkConfigurationIsLoggedOnce(t *testing.T) {
	var failing atomic.Pointer[string]
	var logs syncBuffer
	g := newGW(t, func(o *options) {
		o.execWrap = func(e apply.Exec) apply.Exec { return failingHostNet{inner: e, failing: &failing} }
		o.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	g.finishSetup()
	warned := func() int { return logs.count(`level=WARN msg="cannot read the host's network configuration"`) }
	poll := func() {
		t.Helper()
		for range 5 {
			if r := g.do("GET", "/system/interfaces", nil, nil, nil); r.Status != 200 {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
		}
	}
	gone := "the executor is gone"
	failing.Store(&gone)
	poll()
	if n := warned(); n != 1 {
		t.Errorf("one failure, %d warnings", n)
	}
	if pf := g.do("GET", "/system/preflight", nil, nil, nil).json(t); !strings.Contains(fmt.Sprint(pf["checks"]), "cannot be read: the executor is gone") {
		t.Errorf("the preflight does not say it: %v", pf["checks"])
	}
	other := "permission denied"
	failing.Store(&other)
	poll()
	if n := warned(); n != 2 {
		t.Errorf("another failure, %d warnings in all", n)
	}
	failing.Store(nil)
	poll()
	if n := logs.count("can be read again"); n != 1 {
		t.Errorf("the end of the failure: logged %d times", n)
	}
	failing.Store(&gone)
	poll()
	if n := warned(); n != 3 {
		t.Errorf("a new failure, %d warnings in all", n)
	}
}

// countingHostNet counts the executor's reads of the host's network configuration and fails them while
// failing is set.
type countingHostNet struct {
	inner   apply.Exec
	reads   *atomic.Int64
	failing *atomic.Pointer[string]
}

func (f countingHostNet) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	for _, op := range ops {
		if r, ok := op.(*executor.Read); ok && r.What == executor.ReadHostNet {
			f.reads.Add(1)
			if msg := f.failing.Load(); msg != nil {
				return executor.Outcome{}, errors.New(*msg)
			}
		}
	}
	return f.inner.Do(ctx, ops...)
}

// Phase 3 audit: GET /setup needs no authentication, so, like the health check (M5-13), it must not give
// every caller its own read through the executor: requests in a row share one read. Its report does not
// quote the executor's error either (that is in the log), only that the configuration cannot be read.
// The server's clock stands still while the 20 requests run: on a slow machine (level 1b under emulation)
// they took longer than the cache's 2 s, and a second read was the cache working, not a failure.
func TestTheUnauthenticatedSetupSharesOneReadOfTheHostsNetworkConfiguration(t *testing.T) {
	var reads atomic.Int64
	var failing atomic.Pointer[string]
	clk := clock.NewFake(time.Now())
	g := newGW(t, func(o *options) {
		o.execWrap = func(e apply.Exec) apply.Exec {
			return countingHostNet{inner: e, reads: &reads, failing: &failing}
		}
		o.hostFS = fstest.MapFS{"etc/netplan/50-lab.yaml": {Data: []byte("network:\n  ethernets:\n    lan0: {dhcp4: true}\n")}}
		o.apiClock = clk
	})
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := g.do("GET", "/setup", nil, nil, nil); r.Status != 200 {
				t.Errorf("%d %s", r.Status, r.Body)
			}
		}()
	}
	wg.Wait()
	for range 10 {
		if r := g.do("GET", "/setup", nil, nil, nil); !strings.Contains(string(r.Body), `\"lan0\" under \"ethernets\"`) {
			t.Fatalf("the entry is not named: %s", r.Body)
		}
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("20 requests made %d reads", n)
	}
	// after the cache's 2 s the next request reads again, once
	clk.Advance(2 * time.Second)
	for range 3 {
		if r := g.do("GET", "/setup", nil, nil, nil); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}
	if n := reads.Load(); n != 2 {
		t.Errorf("3 requests after the cache's lifetime: %d reads in all, want 2", n)
	}

	// a failing read (another gateway: the cache is per server) is named, its error is not
	secret := "open /host/etc/netplan: permission denied (some detail)"
	failing.Store(&secret)
	g2 := newGW(t, func(o *options) {
		o.execWrap = func(e apply.Exec) apply.Exec {
			return countingHostNet{inner: e, reads: &reads, failing: &failing}
		}
	})
	body := string(g2.do("GET", "/setup", nil, nil, nil).Body)
	if strings.Contains(body, "permission denied") || !strings.Contains(body, "the network configuration of the operating system cannot be read") {
		t.Errorf("GET /setup with a failing read: %s", body)
	}
}
