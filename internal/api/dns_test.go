package api_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// serviceGateway returns a gateway with the service token in `g.token` and the admin token kept in
// the second result.
func serviceGateway(t *testing.T) (*gw, string) {
	t.Helper()
	g := dhcpGateway(t)
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	admin := g.token
	g.token = strings.TrimSpace(string(raw))
	return g, admin
}

func TestTheDNSConfigurationDescribesTheNetworks(t *testing.T) {
	defer api.SetDNSPoll(300*time.Millisecond, 20*time.Millisecond)()
	g := dhcpGateway(t)
	id := g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"dns": map[string]any{"static_entries": []any{map[string]any{"name": "broker.lab", "addresses": []any{"10.10.0.5"}}}}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	admin := g.token
	// users cannot read it
	if r := g.do("GET", "/internal/dns/config", nil, nil, nil); r.Status != 403 {
		t.Errorf("a full token: %d", r.Status)
	}
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	g.token = strings.TrimSpace(string(raw))
	defer func() { g.token = admin }()

	cfg := g.do("GET", "/internal/dns/config", nil, nil, nil).json(t)
	gen := cfg["generation"].(float64)
	if gen <= 0 || cfg["strip_aaaa"] != true || cfg["faults"] == nil {
		t.Fatalf("%v", cfg)
	}
	if up := cfg["upstream"].([]any); len(up) != 1 || up[0] != "192.0.2.53" {
		t.Errorf("upstream %v", up)
	}
	var iot map[string]any
	for _, n := range cfg["networks"].([]any) {
		if n.(map[string]any)["network"] == iotID {
			iot = n.(map[string]any)
		}
	}
	if iot == nil || iot["gateway"] != "10.10.0.1" || iot["clients"] != "10.10.0.0/24" {
		t.Fatalf("%v", cfg["networks"])
	}
	se := iot["static_entries"].([]any)
	if len(se) != 1 || se[0].(map[string]any)["name"] != "broker.lab" {
		t.Errorf("%v", se)
	}

	// unchanged: a long poll with the current generation ends with 204 after the wait
	start := time.Now()
	r := g.do("GET", fmt.Sprintf("/internal/dns/config?after=%d", int64(gen)), nil, nil, nil)
	if r.Status != 204 || time.Since(start) < 250*time.Millisecond {
		t.Errorf("%d after %v", r.Status, time.Since(start))
	}
	// a change that matters ends the wait with the new configuration
	g.token = admin
	id2 := g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"dns": map[string]any{"static_entries": []any{}}}}})
	if r := g.apply(id2); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.token = strings.TrimSpace(string(raw))
	next := g.do("GET", fmt.Sprintf("/internal/dns/config?after=%d", int64(gen)), nil, nil, nil)
	if next.Status != 200 || next.json(t)["generation"].(float64) <= gen {
		t.Errorf("%d %s", next.Status, next.Body)
	}
	// the same content keeps the generation
	again := g.do("GET", "/internal/dns/config", nil, nil, nil).json(t)["generation"]
	if again != next.json(t)["generation"] {
		t.Errorf("the generation moved without a change: %v", again)
	}
}

func TestTheQueryLogIsPostedByTheProxyAndListedNewestFirst(t *testing.T) {
	g, admin := serviceGateway(t)
	svc := g.token
	now := time.Now().UTC().Truncate(time.Second)
	entry := func(i int, client, name string) map[string]any {
		return map[string]any{"time": now.Add(time.Duration(i) * time.Second).Format(time.RFC3339), "client": client, "name": name, "type": "A", "rcode": "NOERROR", "protocol": "udp",
			"answers": []any{"203.0.113.10"}, "duration_ms": 1.5}
	}
	// the device with the neighbor entry gets its id in the log
	g.token = admin
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	g.token = svc
	var batch []any
	for i := 0; i < 5; i++ {
		batch = append(batch, entry(i, "10.10.0.31", fmt.Sprintf("host%d.example.test", i)))
	}
	batch = append(batch, entry(5, "10.10.0.99", "other.example.test"))
	if r := g.do("POST", "/internal/dns/queries", batch, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// a user cannot post
	g.token = admin
	if r := g.do("POST", "/internal/dns/queries", batch, nil, nil); r.Status != 403 {
		t.Errorf("a full token: %d", r.Status)
	}
	items := g.do("GET", "/dns/queries", nil, nil, nil).json(t)["items"].([]any)
	if len(items) != 6 || items[0].(map[string]any)["name"] != "other.example.test" || items[5].(map[string]any)["name"] != "host0.example.test" {
		t.Fatalf("not newest first: %v", items)
	}
	if items[1].(map[string]any)["device"] != espID {
		t.Errorf("the device is not filled in: %v", items[1])
	}
	if items[0].(map[string]any)["device"] != nil {
		t.Errorf("an unknown client has no device: %v", items[0])
	}
	// filters
	mine := g.do("GET", "/dns/queries?device=esp32-42", nil, nil, nil).json(t)["items"].([]any)
	if len(mine) != 5 {
		t.Errorf("%d queries of the device", len(mine))
	}
	if r := g.do("GET", "/dns/queries?device=ghost", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
	if n := g.do("GET", "/dns/queries?name=host2.example.test", nil, nil, nil).json(t)["items"].([]any); len(n) != 1 {
		t.Errorf("exact name: %d", len(n))
	}
	if n := g.do("GET", "/dns/queries?name=*.example.test", nil, nil, nil).json(t)["items"].([]any); len(n) != 6 {
		t.Errorf("suffix: %d", len(n))
	}
	since := now.Add(4 * time.Second).Format(time.RFC3339)
	if n := g.do("GET", "/dns/queries?since="+since, nil, nil, nil).json(t)["items"].([]any); len(n) != 2 {
		t.Errorf("since: %d", len(n))
	}
	// paging
	p1 := g.do("GET", "/dns/queries?limit=4", nil, nil, nil).json(t)
	if len(p1["items"].([]any)) != 4 || p1["next_cursor"] == nil {
		t.Fatalf("%v", p1)
	}
	p2 := g.do("GET", "/dns/queries?limit=4&cursor="+p1["next_cursor"].(string), nil, nil, nil).json(t)
	if len(p2["items"].([]any)) != 2 || p2["next_cursor"] != nil {
		t.Fatalf("%v", p2)
	}
	// entries that make no sense are refused as a whole
	g.token = svc
	g.badRequest = true // every body below is deliberately invalid
	for name, bad := range map[string]any{
		"no name":    []any{map[string]any{"time": now.Format(time.RFC3339), "client": "10.10.0.5", "type": "A", "rcode": "NOERROR"}},
		"bad client": []any{entry(0, "nope", "x.test")},
		"unknown":    []any{map[string]any{"time": now.Format(time.RFC3339), "client": "10.10.0.5", "name": "x", "type": "A", "rcode": "NOERROR", "extra": 1}},
	} {
		if r := g.do("POST", "/internal/dns/queries", bad, nil, nil); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	g.badRequest = false
	g.token = admin
	if n := g.do("GET", "/dns/queries", nil, nil, nil).json(t)["items"].([]any); len(n) != 6 {
		t.Errorf("a refused batch was partly taken: %d", len(n))
	}
}
