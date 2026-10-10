package api_test

import (
	"fmt"
	"strings"
	"testing"
)

// Profiles over the API (plan M11): the catalogue, custom profiles through revisions, activation as an
// overlay, the state of an activation, the preview and the deletion rules.

const (
	pLTEMTU = "5c6d7e8f-9a0b-4c1d-8e2f-3a4b5c6d7e8f"
	pFlaky  = "6d7e8f9a-0b1c-4d2e-8f3a-4b5c6d7e8f9a"
	pSlowNS = "7e8f9a0b-1c2d-4e3f-8a4b-5c6d7e8f9a0b"
)

func profileItems(t *testing.T, g *gw) map[string]map[string]any {
	t.Helper()
	r := g.do("GET", "/profiles?limit=100", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	out := map[string]map[string]any{}
	for _, it := range r.json(t)["items"].([]any) {
		m := it.(map[string]any)
		out[m["config"].(map[string]any)["name"].(string)] = m
	}
	return out
}

func TestTheProfileCatalogueListsTheBuiltInProfilesAndMarksTheUnavailableOnes(t *testing.T) {
	g := ready(t)
	items := profileItems(t, g)
	for _, name := range []string{"normal", "lte", "bad-lte", "satellite", "congested-wifi", "offline", "intermittent", "dns-broken", "tls-broken"} {
		it, ok := items[name]
		if !ok {
			t.Fatalf("%s is not listed: %v", name, items)
		}
		later := name == "dns-broken" || name == "tls-broken"
		if it["builtin"] != true || it["available"] == later {
			t.Errorf("%s: %v", name, it)
		}
		if reason, _ := it["unavailable_reason"].(string); later != (reason != "") || (later && !strings.Contains(reason, map[string]string{"dns-broken": "M20", "tls-broken": "M21"}[name])) {
			t.Errorf("%s: unavailable_reason %q", name, reason)
		}
	}
	if len(items) != 9 {
		t.Errorf("%d profiles on a fresh gateway", len(items))
	}
	// by name and by UUID, ignoring case
	byName := g.do("GET", "/profiles/Bad-LTE", nil, nil, nil)
	if byName.Status != 200 || byName.json(t)["id"] != "b9b6e3e5-b89a-5b25-b7af-b1ac017bda16" {
		t.Fatalf("%d %s", byName.Status, byName.Body)
	}
	byID := g.do("GET", "/profiles/B9B6E3E5-B89A-5B25-B7AF-B1AC017BDA16", nil, nil, nil)
	if byID.Status != 200 || byID.json(t)["config"].(map[string]any)["name"] != "bad-lte" {
		t.Fatalf("%d %s", byID.Status, byID.Body)
	}
	cfg := byID.json(t)["config"].(map[string]any)["parts"].(map[string]any)["impairment"].(map[string]any)
	if cfg["latency"] != "150ms" || cfg["jitter"] != "50ms" || cfg["loss"] != "3%" || cfg["rate"] != "2Mbit" {
		t.Errorf("%v", cfg)
	}
	if r := g.do("GET", "/profiles/no-such-profile", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// paging
	r := g.do("GET", "/profiles?limit=4", nil, nil, nil).json(t)
	if len(r["items"].([]any)) != 4 || r["next_cursor"] == nil {
		t.Errorf("%v", r)
	}
}

// Custom profiles are created, changed and deleted with revisions; the built-in ones are fixed.
func TestACustomProfileIsCreatedChangedAndDeletedWithRevisions(t *testing.T) {
	g := ready(t)
	rev := g.mustPatch(map[string]any{"profiles": map[string]any{
		pLTEMTU: map[string]any{"name": "lte-small-mtu", "description": "LTE with a small MTU", "parts": map[string]any{
			"impairment": map[string]any{"latency": "50ms", "jitter": "10ms", "loss": "0.1%"},
			"mtu":        map[string]any{"size": 1400, "mode": "mss_clamp"},
		}},
	}})
	// a candidate shows its profiles by revision, the active revision does not have them yet
	if r := g.do("GET", "/profiles/lte-small-mtu", nil, nil, nil); r.Status != 404 {
		t.Errorf("a candidate's profile is active: %d", r.Status)
	}
	if r := g.do("GET", fmt.Sprintf("/profiles/lte-small-mtu?revision=%d", rev), nil, nil, nil); r.Status != 200 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	pv := g.do("POST", fmt.Sprintf("/revisions/%d/preview", rev), nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	found := false
	for _, c := range pv.json(t)["domain"].([]any) {
		m := c.(map[string]any)
		found = found || (m["kind"] == "profile" && m["op"] == "added")
	}
	if !found {
		t.Errorf("the preview does not show the new profile: %s", pv.Body)
	}
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	it := profileItems(t, g)["lte-small-mtu"]
	if it == nil || it["builtin"] == true || it["available"] != true || it["id"] != pLTEMTU {
		t.Fatalf("%v", it)
	}
	if r := g.do("GET", "/profiles/"+pLTEMTU, nil, nil, nil); r.Status != 200 {
		t.Errorf("%d", r.Status)
	}

	// change: a merge patch on one parameter
	rev = g.mustPatch(map[string]any{"profiles": map[string]any{pLTEMTU: map[string]any{"parts": map[string]any{"impairment": map[string]any{"latency": "80ms"}}}}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	imp := g.do("GET", "/profiles/lte-small-mtu", nil, nil, nil).json(t)["config"].(map[string]any)["parts"].(map[string]any)["impairment"].(map[string]any)
	if imp["latency"] != "80ms" || imp["jitter"] != "10ms" {
		t.Errorf("%v", imp)
	}

	// refused: a built-in name (in any case), a built-in UUID as the key, a jitter above the latency,
	// a profile without a part, a part the schema does not know
	for name, doc := range map[string]map[string]any{
		"a built-in name":         {pFlaky: map[string]any{"name": "Bad-LTE", "parts": map[string]any{"impairment": map[string]any{"latency": "1ms"}}}},
		"a built-in id":           {"b9b6e3e5-b89a-5b25-b7af-b1ac017bda16": map[string]any{"name": "mine", "parts": map[string]any{"impairment": map[string]any{"latency": "1ms"}}}},
		"jitter above latency":    {pFlaky: map[string]any{"name": "flaky", "parts": map[string]any{"impairment": map[string]any{"latency": "10ms", "jitter": "50ms"}}}},
		"the same name":           {pFlaky: map[string]any{"name": "LTE-small-MTU", "parts": map[string]any{"impairment": map[string]any{"latency": "1ms"}}}},
		"no part":                 {pFlaky: map[string]any{"name": "flaky", "parts": map[string]any{}}},
		"a mtu above the maximum": {pFlaky: map[string]any{"name": "flaky", "parts": map[string]any{"mtu": map[string]any{"size": 70000}}}},
	} {
		g.badRequest = true
		r := g.patch(map[string]any{"profiles": doc})
		g.badRequest = false
		if r.Status != 422 && r.Status != 400 && r.Status != 409 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}

	// delete
	rev = g.mustPatch(map[string]any{"profiles": map[string]any{pLTEMTU: nil}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if _, ok := profileItems(t, g)["lte-small-mtu"]; ok {
		t.Error("the deleted profile is still listed")
	}
	if len(profileItems(t, g)) != 9 {
		t.Errorf("the built-in profiles changed: %d", len(profileItems(t, g)))
	}
}

// A profile with a part of a later milestone can be defined and is listed as unavailable; activating it is
// refused with the milestone.
func TestACustomProfileWithADNSPartIsStoredButCannotBeActivated(t *testing.T) {
	g := ready(t)
	rev := g.mustPatch(map[string]any{"profiles": map[string]any{
		pSlowNS: map[string]any{"name": "slow-dns-lte", "parts": map[string]any{
			"impairment": map[string]any{"latency": "50ms"},
			"dns":        map[string]any{"action": "delay", "delay": "2s"},
		}},
	}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	it := profileItems(t, g)["slow-dns-lte"]
	if it["available"] != false || !strings.Contains(it["unavailable_reason"].(string), "M20") {
		t.Errorf("%v", it)
	}
	r := g.createOverlay(`{"target":{"network":"IoT"},"profile":"slow-dns-lte"}`)
	if r.Status != 422 || r.code(t) != "unsupported_feature" || !strings.Contains(r.json(t)["detail"].(string), "M20") {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("%d overlays", n)
	}
}

// Activation over the API: 201, then 200 for a switch with the same id; the state, counters and queues
// of the activation; the profile card lists where it is activated.
func TestAProfileIsActivatedSwitchedAndListedOverTheAPI(t *testing.T) {
	g := ready(t)
	g.mintToken("overlays")
	r := g.createOverlay(`{"target":{"network":"IoT"},"profile":"lte","ttl":"10m"}`)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ov := r.json(t)
	if ov["kind"] != "profile" || ov["profile"] != "ad24af2d-f184-5d04-b8c4-01f24c86ddcf" && ov["profile"] != "lte" || ov["expires_at"] == nil {
		t.Fatalf("%v", ov)
	}
	id := ov["id"].(string)
	got := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)
	if got["state"] != "effective" || got["counters"] == nil {
		t.Errorf("%v", got)
	}

	card := g.do("GET", "/profiles/lte", nil, nil, nil).json(t)
	acts, _ := card["activations"].([]any)
	if len(acts) != 1 || acts[0].(map[string]any)["overlay"] != id || acts[0].(map[string]any)["target"].(map[string]any)["network"] == nil || acts[0].(map[string]any)["state"] != "effective" {
		t.Fatalf("%v", card["activations"])
	}
	if other := g.do("GET", "/profiles/satellite", nil, nil, nil).json(t); other["activations"] != nil {
		t.Errorf("%v", other["activations"])
	}

	// switching is a replacement
	r = g.createOverlay(`{"target":{"network":"IoT"},"profile":"satellite"}`)
	if r.Status != 200 || r.json(t)["id"] != id {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if acts := g.do("GET", "/profiles/lte", nil, nil, nil).json(t)["activations"]; acts != nil {
		t.Errorf("lte is still activated: %v", acts)
	}
	if acts, _ := g.do("GET", "/profiles/satellite", nil, nil, nil).json(t)["activations"].([]any); len(acts) != 1 {
		t.Errorf("satellite is not activated: %v", acts)
	}
	// an unknown profile is a validation error with a pointer; deleting the activation ends it
	r = g.createOverlay(`{"target":{"network":"IoT"},"profile":"nope"}`)
	if r.Status != 422 || r.code(t) != "validation_failed" || r.json(t)["errors"].([]any)[0].(map[string]any)["path"] != "/profile" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if d := g.do("DELETE", "/overlays/"+id, nil, nil, nil); d.Status != 204 {
		t.Fatalf("%d %s", d.Status, d.Body)
	}
	if acts := g.do("GET", "/profiles/satellite", nil, nil, nil).json(t)["activations"]; acts != nil {
		t.Errorf("%v", acts)
	}
}

// Plan M11 "Tests" over the API: a fault on the same scope replaces only the profile part of its family. The
// activation shows `partially_overridden` while its MTU part stays, `overridden` once nothing wins, and
// explain and the preview name the profile.
func TestAFaultOnTheSameScopeOverridesOnlyOnePartOfTheActivationOverTheAPI(t *testing.T) {
	g := ready(t)
	rev := g.mustPatch(map[string]any{"profiles": map[string]any{
		pLTEMTU: map[string]any{"name": "lte-small-mtu", "parts": map[string]any{
			"impairment": map[string]any{"latency": "50ms"},
			"mtu":        map[string]any{"size": 1400, "mode": "mss_clamp"},
		}},
	}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	prof := g.mustCreateOverlay(`{"target":{"network":"IoT"},"profile":"lte-small-mtu"}`)
	if st := g.do("GET", "/overlays/"+prof["id"].(string), nil, nil, nil).json(t)["state"]; st != "effective" {
		t.Fatalf("state %v", st)
	}
	fault := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"300ms"}}`)
	got := g.do("GET", "/overlays/"+prof["id"].(string), nil, nil, nil).json(t)
	if got["state"] != "partially_overridden" {
		t.Errorf("state %v", got["state"])
	}
	if got := g.do("GET", "/overlays/"+fault["id"].(string), nil, nil, nil).json(t)["state"]; got != "effective" {
		t.Errorf("the fault is %v", got)
	}
	card := g.do("GET", "/profiles/lte-small-mtu", nil, nil, nil).json(t)
	if acts := card["activations"].([]any); len(acts) != 1 || acts[0].(map[string]any)["state"] != "partially_overridden" {
		t.Errorf("%v", card["activations"])
	}
	// and a fault of the MTU family on the same scope takes the other part: nothing of the profile wins
	g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"family":"mtu","mtu":{"size":1200,"mode":"blackhole"}}}`)
	if st := g.do("GET", "/overlays/"+prof["id"].(string), nil, nil, nil).json(t)["state"]; st != "overridden" {
		t.Errorf("state %v", st)
	}
	// the configuration part is untouched by the overlays: the candidate's preview lists what wins now
	rev = g.mustPatch(map[string]any{"settings": map[string]any{}})
	pv := g.do("POST", fmt.Sprintf("/revisions/%d/preview", rev), nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	if faults, _ := pv.json(t)["faults"].([]any); len(faults) != 2 {
		t.Errorf("preview faults %s", pv.Body)
	}
}

// The preview of a revision that changes a profile shows what its activations will produce, with the
// profile as the origin of each part.
func TestThePreviewNamesTheProfileOfEachEffectivePart(t *testing.T) {
	g := ready(t)
	rev := g.mustPatch(map[string]any{"profiles": map[string]any{
		pFlaky: map[string]any{"name": "flaky", "parts": map[string]any{"impairment": map[string]any{"latency": "100ms"}}},
	}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.mustCreateOverlay(`{"target":{"network":"IoT"},"profile":"flaky"}`)
	g.mustCreateOverlay(`{"target":{"network":"lab-hub"},"profile":"offline"}`)
	rev = g.mustPatch(map[string]any{"profiles": map[string]any{pFlaky: map[string]any{"parts": map[string]any{"impairment": map[string]any{"latency": "250ms"}}}}})
	pv := g.do("POST", fmt.Sprintf("/revisions/%d/preview", rev), nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	byProfile := map[string]map[string]any{}
	for _, f := range pv.json(t)["faults"].([]any) {
		m := f.(map[string]any)
		byProfile[m["profile"].(string)] = m
	}
	if f := byProfile["flaky"]; f == nil || f["summary"] != "latency 250ms" || f["scope"] != "network IoT" || f["family"] != "impairment" || f["layer"] != "overlay" {
		t.Errorf("%v", byProfile)
	}
	if f := byProfile["offline"]; f == nil || f["summary"] != "blackout" || f["scope"] != "network lab-hub" {
		t.Errorf("%v", byProfile)
	}
}

// Plan §2.1.1: deleting an activated profile needs force and lists the references; the built-in profiles
// cannot be deleted.
func TestDeletingAnActivatedProfileNeedsForceAndListsTheReferences(t *testing.T) {
	g := ready(t)
	rev := g.mustPatch(map[string]any{"profiles": map[string]any{
		pFlaky: map[string]any{"name": "flaky", "parts": map[string]any{"impairment": map[string]any{"latency": "100ms"}}},
	}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	admin := g.token
	g.mintToken("overlays")
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"profile":"flaky"}`)
	keep := g.mustCreateOverlay(`{"target":{"network":"lab-hub"},"profile":"lte"}`)
	g.token = admin

	del := g.mustPatch(map[string]any{"profiles": map[string]any{pFlaky: nil}})
	r := g.apply(del)
	if r.Status != 422 || r.code(t) != "validation_failed" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	refs, _ := r.json(t)["references"].([]any)
	if len(refs) != 1 {
		t.Fatalf("references %s", r.Body)
	}
	if ref := refs[0].(map[string]any); ref["kind"] != "overlay" || ref["id"] != ov["id"] || ref["object"] != "/profiles/"+pFlaky {
		t.Errorf("reference %v", ref)
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 2 {
		t.Errorf("a refused apply left %d overlays", n)
	}
	r = g.do("POST", "/revisions/"+itoa(del)+"/apply?force=true", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if removed, _ := r.json(t)["removed_overlays"].([]any); len(removed) != 1 || removed[0] != ov["id"] {
		t.Errorf("removed_overlays %s", r.Body)
	}
	items := g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != keep["id"] {
		t.Errorf("overlays %v", items)
	}
}
