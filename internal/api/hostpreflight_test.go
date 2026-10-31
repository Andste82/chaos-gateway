package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/preflight"
)

// scriptedHostPreflight answers the executor's read of its checks of the host (M28) with a state, or with an error while
// failing is set. The executor on the simulated kernel of these tests has no tools to check.
type scriptedHostPreflight struct {
	inner   apply.Exec
	state   executor.HostPreflightState
	failing *atomic.Pointer[string]
}

func (s scriptedHostPreflight) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	for _, op := range ops {
		if r, ok := op.(*executor.Read); ok && r.What == executor.ReadHostPreflight {
			if msg := s.failing.Load(); msg != nil {
				return executor.Outcome{}, errors.New(*msg)
			}
			raw, err := json.Marshal(s.state)
			return executor.Outcome{Data: []json.RawMessage{raw}}, err
		}
	}
	return s.inner.Do(ctx, ops...)
}

// M28 (plan §3.4, "preflight in the executor"): the checks the privileged executor makes of the host (tools, forwarding,
// namespaces, other firewalls, the data directories) are part of GET /system/preflight and of the preflight in GET /setup, with
// their fixes, and they decide the report's status like the API's own checks: a failed one fails the report, a warning warns.
func TestThePreflightCarriesTheChecksTheExecutorMadeOfTheHost(t *testing.T) {
	var failing atomic.Pointer[string]
	g := newGW(t, func(o *options) {
		o.execWrap = func(e apply.Exec) apply.Exec {
			return scriptedHostPreflight{inner: e, failing: &failing, state: executor.HostPreflightState{
				At: time.Now(),
				Checks: []preflight.HostCheck{
					{ID: "host.tools", Status: "ok", Message: "ip utility, iproute2-6.1.0"},
					{ID: "host.firewall", Status: "warn", Message: "another firewall is active: inet firewalld", Fix: "stop firewalld"},
					{ID: "host.data.captures", Status: "fail", Message: "/var/lib/chaosgw/captures belongs to user 0", Fix: "run the host setup (deploy/host-setup.sh)"},
				},
			}}
		}
	})
	byID := func(report map[string]any) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		for _, c := range report["checks"].([]any) {
			m := c.(map[string]any)
			out[m["id"].(string)] = m
		}
		return out
	}
	check := func(what string, report map[string]any, public bool) {
		t.Helper()
		cs := byID(report)
		if c := cs["host.tools"]; c == nil || c["status"] != "ok" || strings.Contains(c["message"].(string), "iproute2") == public {
			t.Errorf("%s: host.tools = %v", what, c)
		}
		if public {
			// the unauthenticated endpoint carries no tool versions, paths or errors of the host, only the status and the fix
			for id, c := range cs {
				if m := c["message"].(string); strings.HasPrefix(id, "host.") && (strings.Contains(m, "firewalld") || strings.Contains(m, "/var/lib")) {
					t.Errorf("%s: %s quotes the host: %s", what, id, m)
				}
			}
		}
		if c := cs["host.firewall"]; c == nil || c["status"] != "warn" || c["fix"] != "stop firewalld" {
			t.Errorf("%s: host.firewall = %v", what, c)
		}
		if c := cs["host.data.captures"]; c == nil || c["status"] != "fail" || !strings.Contains(c["fix"].(string), "host-setup") {
			t.Errorf("%s: host.data.captures = %v", what, c)
		}
		if report["status"] != "fail" {
			t.Errorf("%s: a failed check of the executor leaves the status %v", what, report["status"])
		}
	}
	// the wizard shows the same checks before the setup, to the unauthenticated caller
	check("GET /setup", g.do("GET", "/setup", nil, nil, nil).json(t)["preflight"].(map[string]any), true)
	g.finishSetup()
	check("GET /system/preflight", g.do("GET", "/system/preflight", nil, nil, nil).json(t), false)

	// an executor that cannot be asked is a warning that says so; the API's own checks stay
	gone := "the executor is gone"
	failing.Store(&gone)
	pf := g.do("GET", "/system/preflight", nil, nil, nil).json(t)
	cs := byID(pf)
	if c := cs["host"]; c == nil || c["status"] != "warn" || !strings.Contains(c["message"].(string), "cannot be read: the executor is gone") {
		t.Errorf("a failing read: %v", cs["host"])
	}
	if cs["host.tools"] != nil || cs["interface.lan0.os_config"] == nil {
		t.Errorf("without the executor's answer there are no executor checks and the interface checks of the API remain: %v", pf["checks"])
	}
}

// The unauthenticated GET /setup does not quote why the executor could not be asked (the reason is in the log).
func TestTheSetupDoesNotQuoteWhyTheExecutorsChecksAreMissing(t *testing.T) {
	var failing atomic.Pointer[string]
	gone := "dial unix /run/chaosgw/exec.sock: connection refused"
	failing.Store(&gone)
	g := newGW(t, func(o *options) {
		o.execWrap = func(e apply.Exec) apply.Exec { return scriptedHostPreflight{inner: e, failing: &failing} }
	})
	pf := g.do("GET", "/setup", nil, nil, nil).json(t)["preflight"].(map[string]any)
	var found bool
	for _, c := range pf["checks"].([]any) {
		m := c.(map[string]any)
		if m["id"] == "host" {
			found = true
			if strings.Contains(m["message"].(string), "exec.sock") || m["status"] != "warn" {
				t.Errorf("the check quotes the reason: %v", m)
			}
		}
	}
	if !found {
		t.Errorf("no check says the executor's checks are missing: %v", pf["checks"])
	}
}
