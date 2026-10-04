package api_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/supervisor"
)

// healthComponents reads /system/health as the admin token and returns each component's status by
// name.
func healthComponents(t *testing.T, g *gw) (map[string]string, string) {
	t.Helper()
	body := g.do("GET", "/system/health", nil, nil, nil).json(t)
	out := map[string]string{}
	comps, _ := body["components"].([]any)
	for _, c := range comps {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		out[m["name"].(string)] = m["status"].(string)
	}
	status, _ := body["status"].(string)
	return out, status
}

// M6b-05 test: /system/health reports kea, svcns and dns, which §2.14 promises but the handler never
// filled in.
func TestHealthReportsKeaSvcnsAndDns(t *testing.T) {
	g := dhcpGateway(t)
	admin := g.token

	comps, overall := healthComponents(t, g)
	if comps["kea"] != "healthy" {
		t.Fatalf("kea with DHCP configured and no error: %v", comps)
	}
	if comps["svcns"] != "disabled" {
		t.Fatalf("svcns without a service namespace: %v", comps)
	}
	if comps["dns"] != "degraded" {
		t.Fatalf("dns before the proxy has ever polled: %v", comps)
	}
	if overall != "degraded" {
		t.Fatalf("overall status: %q", overall)
	}

	// the DNS proxy polls once: the component turns healthy
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	g.token = strings.TrimSpace(string(raw))
	if r := g.do("GET", "/internal/dns/config", nil, nil, nil); r.Status != 200 {
		t.Fatalf("dns config: %d %s", r.Status, r.Body)
	}
	g.token = admin
	comps, _ = healthComponents(t, g)
	if comps["dns"] != "healthy" {
		t.Fatalf("dns right after a poll: %v", comps)
	}

	// kea turns degraded when the DHCP server refuses the configuration, and so does the overall status
	g.dhcp.setErr(errors.New("config-set refused"))
	id := g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"dhcp": map[string]any{"lease_time": "20m"}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("apply: %d %s", r.Status, r.Body)
	}
	comps, overall = healthComponents(t, g)
	if comps["kea"] != "degraded" {
		t.Fatalf("kea once the DHCP server refuses the configuration: %v", comps)
	}
	if overall != "degraded" {
		t.Fatalf("overall status: %q", overall)
	}
}

// panicOnServiceNSRead panics inside a read of the service namespace once armed, standing in for a
// goroutine (WatchService) that panics while supervised but not critical.
type panicOnServiceNSRead struct {
	inner apply.Exec
	armed *atomic.Bool
}

func (p panicOnServiceNSRead) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if p.armed.Load() {
		for _, op := range ops {
			if r, ok := op.(*executor.Read); ok && r.What == executor.ReadServiceNS {
				panic("kaboom")
			}
		}
	}
	return p.inner.Do(ctx, ops...)
}

// M4-01 test: §3.11 says a recovered panic marks the component unhealthy; before this, Engine.Health()
// was never read by /system/health, so a panicked (but non-critical) supervised goroutine stayed
// invisible to anyone watching the API.
func TestHealthReportsAPanickedEngineGoroutine(t *testing.T) {
	var armed atomic.Bool
	g := newGW(t, func(o *options) {
		o.serviceNS = "cgsvc"
		o.execWrap = func(e apply.Exec) apply.Exec { return panicOnServiceNSRead{inner: e, armed: &armed} }
	})
	g.k.ServiceNamespace("cgsvc")
	g.finishSetup()
	if comps, _ := healthComponents(t, g); comps["api"] != "healthy" {
		t.Fatalf("before the panic: %v", comps)
	}

	armed.Store(true)
	g.e.WatchService(context.Background(), 10*time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for {
		panicked := false
		for _, h := range g.e.Health() {
			panicked = panicked || h.State == supervisor.Panicked
		}
		if panicked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watcher never panicked: %+v", g.e.Health())
		}
		time.Sleep(10 * time.Millisecond)
	}

	comps, overall := healthComponents(t, g)
	if comps["api"] != "unhealthy" {
		t.Fatalf("api component after the panic: %v", comps)
	}
	if overall != "unhealthy" {
		t.Fatalf("overall status after the panic: %q", overall)
	}
}
