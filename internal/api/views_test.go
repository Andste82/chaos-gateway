package api_test

import (
	"archive/zip"
	"bytes"
	"image/png"
	"strings"
	"testing"
)

const (
	iotID  = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	hubID  = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	linkID = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	rAID   = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

func TestNetworksAreViewsOfTheActiveRevision(t *testing.T) {
	g := ready(t)
	l := g.do("GET", "/networks", nil, nil, nil).json(t)["items"].([]any)
	if len(l) != 4 {
		t.Fatalf("%d networks", len(l))
	}
	// sorted by name, with status
	var names []string
	for _, n := range l {
		names = append(names, n.(map[string]any)["config"].(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "admin,IoT,lab-hub,site-b" {
		t.Errorf("%v", names)
	}
	iot := g.do("GET", "/networks/"+iotID, nil, nil, nil).json(t)
	if iot["id"] != iotID || iot["status"].(map[string]any)["state"] != "ok" || iot["status"].(map[string]any)["interface"] == nil {
		t.Errorf("%v", iot)
	}
	// by name, case-insensitively
	if r := g.do("GET", "/networks/iot", nil, nil, nil); r.Status != 200 || r.json(t)["id"] != iotID {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/networks/nope", nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("%d", r.Status)
	}
	// the type filter
	if wgs := g.do("GET", "/networks?type=wireguard", nil, nil, nil).json(t)["items"].([]any); len(wgs) != 3 {
		t.Errorf("%d", len(wgs))
	}
	if lan := g.do("GET", "/networks?type=lan", nil, nil, nil).json(t)["items"].([]any); len(lan) != 1 {
		t.Errorf("%d", len(lan))
	}
	// the WireGuard network: interface, key, peers
	hub := g.do("GET", "/networks/lab-hub", nil, nil, nil).json(t)["status"].(map[string]any)
	if hub["interface"] != "wg-lab-hub" || hub["public_key"] == "" || hub["peers_total"] != float64(1) {
		t.Errorf("%v", hub)
	}
	// pagination
	p1 := g.do("GET", "/networks?limit=3", nil, nil, nil).json(t)
	if len(p1["items"].([]any)) != 3 || p1["next_cursor"] == nil {
		t.Fatalf("%v", p1)
	}
	p2 := g.do("GET", "/networks?limit=3&cursor="+p1["next_cursor"].(string), nil, nil, nil).json(t)
	if len(p2["items"].([]any)) != 1 || p2["next_cursor"] != nil {
		t.Errorf("%v", p2)
	}
	g.badRequest = true // limit=0 is below the schema's minimum
	if r := g.do("GET", "/networks?limit=0", nil, nil, nil); r.Status != 400 && r.Status != 422 {
		t.Errorf("limit 0: %d", r.Status)
	}
	g.badRequest = false
	if r := g.do("GET", "/networks?cursor=%25%25", nil, nil, nil); r.Status != 400 {
		t.Errorf("a garbled cursor: %d", r.Status)
	}
}

func TestACandidateIsViewedWithTheRevisionParameter(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"address": "10.20.0.1/24"}}})
	active := g.do("GET", "/networks/"+iotID, nil, nil, nil).json(t)
	cand := g.do("GET", "/networks/"+iotID+"?revision="+itoa(id), nil, nil, nil).json(t)
	if active["config"].(map[string]any)["address"] != "10.10.0.1/24" || cand["config"].(map[string]any)["address"] != "10.20.0.1/24" {
		t.Errorf("%v %v", active["config"], cand["config"])
	}
	if cand["status"].(map[string]any)["state"] != "pending" {
		t.Errorf("a candidate's state is pending: %v", cand["status"])
	}
	if r := g.do("GET", "/networks?revision=999", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
}

func TestUplinkRoutingAndGroups(t *testing.T) {
	g := ready(t)
	up := g.do("GET", "/uplink", nil, nil, nil).json(t)
	if up["config"].(map[string]any)["gateway"] != "203.0.113.10" {
		t.Errorf("%v", up)
	}
	st := up["status"].(map[string]any)
	if st["interface"] != "wan0" || st["address"] != "203.0.113.1/24" {
		t.Errorf("%v", st)
	}
	if r := g.do("GET", "/routing", nil, nil, nil); r.Status != 200 {
		t.Errorf("%d", r.Status)
	}
	rs := g.do("GET", "/routing/status", nil, nil, nil).json(t)
	if rs["bird"].(map[string]any)["running"] != false || len(rs["protocols"].([]any)) != 0 {
		t.Errorf("%v", rs)
	}
	routes := g.do("GET", "/routing/routes", nil, nil, nil).json(t)["items"].([]any)
	var dsts []string
	for _, r := range routes {
		dsts = append(dsts, r.(map[string]any)["destination"].(string))
	}
	if !contains(dsts, "10.10.0.0/24") || !contains(dsts, "10.60.0.0/24") {
		t.Errorf("%v", dsts)
	}
	if st := g.do("GET", "/routing/routes?origin=static", nil, nil, nil).json(t)["items"].([]any); len(st) == 0 {
		t.Error("no static routes")
	}
	for _, r := range g.do("GET", "/routing/routes?origin=learned", nil, nil, nil).json(t)["items"].([]any) {
		t.Errorf("a learned route without BIRD: %v", r)
	}
	// groups: none in the fixture; add one through a revision
	if gs := g.do("GET", "/groups", nil, nil, nil).json(t)["items"].([]any); len(gs) != 0 {
		t.Errorf("%v", gs)
	}
	id := g.mustPatch(map[string]any{"groups": map[string]any{"c0a80001-0000-4000-8000-000000000001": map[string]any{"name": "sensors"}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if gr := g.do("GET", "/groups/sensors", nil, nil, nil); gr.Status != 200 || gr.json(t)["config"].(map[string]any)["name"] != "sensors" {
		t.Errorf("%d %s", gr.Status, gr.Body)
	}
	if r := g.do("GET", "/groups/ghosts", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestWireGuardClientsWithStatus(t *testing.T) {
	g := ready(t)
	l := g.do("GET", "/networks/lab-hub/clients", nil, nil, nil).json(t)["items"].([]any)
	if len(l) != 2 || l[0].(map[string]any)["config"].(map[string]any)["name"] != "rA" {
		t.Fatalf("%v", l)
	}
	c := g.do("GET", "/networks/"+hubID+"/clients/rA", nil, nil, nil).json(t)
	if c["id"] != rAID || c["network"] != hubID || c["private_key_stored"] != false || c["status"].(map[string]any)["online"] != false {
		t.Errorf("%v", c)
	}
	// the handshake makes it online
	pub := g.e.Snapshot().WireGuardInterfaces
	var iface, key string
	for _, w := range pub {
		if w.NetworkID == hubID {
			iface, key = w.Name, w.Peers[0].PublicKey
		}
	}
	g.k.Handshake(iface, key, nowUnix(), 100, 200)
	g.pollWireGuardOnce()
	c = g.do("GET", "/networks/rA/clients/rA", nil, nil, nil).json(t)
	_ = c
	if r := g.do("GET", "/networks/"+iotID+"/clients", nil, nil, nil); r.Status != 404 {
		t.Errorf("clients of a LAN network: %d", r.Status)
	}
	if r := g.do("GET", "/networks/lab-hub/clients/ghost", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
}

// a client with a generated key, for the export tests
func (g *gw) addGeneratedClient(exportOnce bool) {
	g.t.Helper()
	id := g.mustPatch(map[string]any{"networks": map[string]any{hubID: map[string]any{"clients": map[string]any{
		"6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f": map[string]any{"name": "rC", "address": "10.99.0.4", "key": map[string]any{"mode": "generated", "export_once": exportOnce}},
	}}}})
	if r := g.apply(id); r.Status != 200 {
		g.t.Fatalf("%d %s", r.Status, r.Body)
	}
}

func TestExportOfAClientIsASecretDownload(t *testing.T) {
	g := ready(t)
	g.addGeneratedClient(true)
	c := g.do("GET", "/networks/lab-hub/clients/rC", nil, nil, nil).json(t)
	if c["private_key_stored"] != true {
		t.Fatalf("%v", c)
	}
	r := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") || !strings.Contains(r.Header.Get("Content-Disposition"), "rC.conf") {
		t.Fatalf("%d %v", r.Status, r.Header)
	}
	conf := string(r.Body)
	if !strings.Contains(conf, "[Interface]") || !strings.Contains(conf, "PrivateKey = ") || strings.Contains(conf, "PrivateKey = <") {
		t.Errorf("the configuration has no private key:\n%s", conf)
	}
	if !strings.Contains(conf, "Address = 10.99.0.4") {
		t.Errorf("%s", conf)
	}
	// export once: the key is gone after the first download, a second export has a placeholder
	r2 := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if !strings.Contains(string(r2.Body), "PrivateKey = <") {
		t.Errorf("the private key was exported twice:\n%s", r2.Body)
	}
	if c := g.do("GET", "/networks/lab-hub/clients/rC", nil, nil, nil).json(t); c["private_key_stored"] != false {
		t.Errorf("%v", c)
	}
	// every download is in the audit log, without the key
	var found int
	items, _, _ := g.log.List(auditFilter(), "", 50)
	for _, e := range items {
		if e.Action == "wireguard.export" {
			found++
			if strings.Contains(e.Detail, "PrivateKey") {
				t.Errorf("the audit log holds a key: %+v", e)
			}
		}
	}
	if found != 2 {
		t.Errorf("%d export entries", found)
	}
}

// M5-16 test: when the audit log cannot be written, the export fails and no key is sent, instead
// of recording the download only after the key already went out (or not at all).
func TestAFailingAuditLogBlocksTheExport(t *testing.T) {
	g := ready(t)
	g.addGeneratedClient(true)
	if err := g.log.Close(); err != nil {
		t.Fatal(err)
	}
	r := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if r.Status != 503 || strings.Contains(string(r.Body), "PrivateKey") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// the key is still stored: export once did not consume it on a failed export
	c := g.do("GET", "/networks/lab-hub/clients/rC", nil, nil, nil).json(t)
	if c["private_key_stored"] != true {
		t.Errorf("%v", c)
	}
}

func TestQRCodesAndNetworkExports(t *testing.T) {
	g := ready(t)
	g.addGeneratedClient(false)
	png1 := g.do("GET", "/networks/lab-hub/clients/rC/export?format=png", nil, nil, nil)
	if _, err := png.Decode(bytes.NewReader(png1.Body)); err != nil || png1.Header.Get("Content-Type") != "image/png" {
		t.Errorf("png: %v %q", err, png1.Header.Get("Content-Type"))
	}
	g.noContract = true // kin-openapi cannot decode image/svg+xml bodies
	svg := g.do("GET", "/networks/lab-hub/clients/rC/export?format=svg", nil, nil, nil)
	g.noContract = false
	if !strings.Contains(string(svg.Body), "<svg") {
		t.Errorf("%s", truncate(svg.Body))
	}
	g.badRequest = true // format=pdf is not one of the schema's allowed values
	if r := g.do("GET", "/networks/lab-hub/clients/rC/export?format=pdf", nil, nil, nil); r.Status != 400 && r.Status != 422 {
		t.Errorf("an unknown format: %d", r.Status)
	}
	g.badRequest = false
	// the hub as a zip: all clients, or the named ones
	z := g.do("GET", "/networks/lab-hub/export", nil, nil, nil)
	zr, err := zip.NewReader(bytes.NewReader(z.Body), int64(len(z.Body)))
	if err != nil || len(zr.File) != 3 {
		t.Fatalf("zip: %v %d files", err, len(zr.File))
	}
	z = g.do("GET", "/networks/lab-hub/export?clients=rC,rA", nil, nil, nil)
	if zr, err := zip.NewReader(bytes.NewReader(z.Body), int64(len(z.Body))); err != nil || len(zr.File) != 2 {
		t.Errorf("%v", err)
	}
	if r := g.do("GET", "/networks/lab-hub/export?clients=ghost", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
	// the link: the remote side's configuration
	link := g.do("GET", "/networks/site-b/export", nil, nil, nil)
	if link.Status != 200 || !strings.Contains(string(link.Body), "[Peer]") || !strings.Contains(string(link.Body), "AllowedIPs = 0.0.0.0/0") {
		t.Errorf("%d %s", link.Status, link.Body)
	}
	// no routing protocol runs on the link: no BIRD snippet
	if r := g.do("GET", "/networks/site-b/export?format=bird", nil, nil, nil); r.Status != 404 {
		t.Errorf("bird without routing: %d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/networks/"+iotID+"/export", nil, nil, nil); r.Status != 404 {
		t.Errorf("a LAN network: %d", r.Status)
	}
	// delete the private key: later exports have a placeholder
	if r := g.do("DELETE", "/networks/lab-hub/clients/rC/private-key", nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d", r.Status)
	}
	if r := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil); !strings.Contains(string(r.Body), "PrivateKey = <") {
		t.Errorf("%s", r.Body)
	}
}
