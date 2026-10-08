package domain

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func prefixList(ps []netip.Prefix) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return strings.Join(out, " ")
}

func prefixes(ss ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func TestCollapsePrefixesGivesDisjointSortedPrefixes(t *testing.T) {
	got := CollapsePrefixes(prefixes("10.1.0.0/16", "10.0.0.0/8", "10.1.2.3/32", "192.168.1.7/24", "10.0.0.0/8", "172.16.0.0/12"))
	if want := "10.0.0.0/8 172.16.0.0/12 192.168.1.0/24"; prefixList(got) != want {
		t.Errorf("got %s, want %s", prefixList(got), want)
	}
	if len(CollapsePrefixes(nil)) != 0 || len(CollapsePrefixes([]netip.Prefix{{}, netip.MustParsePrefix("::/0")})) != 0 {
		t.Error("invalid and IPv6 prefixes are dropped")
	}
}

func TestScopePrefixesExpandEveryKindOfScopeFromTheIdentity(t *testing.T) {
	tw := newTestWorld(t, false)
	w := tw.world()
	esp := netip.MustParseAddr("10.10.0.42")
	// a discovered device of IoT that has an address outside the network's prefixes
	other := netip.MustParseAddr("192.0.2.9")
	id := Identity{
		Addresses:  map[string][]netip.Addr{idESP: {esp}, idLab: {netip.MustParseAddr("10.50.0.10")}},
		Owner:      map[netip.Addr]string{esp: idESP},
		Ranges:     []IdentityRange{{Prefix: netip.MustParsePrefix("10.50.0.0/28"), Device: idLab}},
		Discovered: []DiscoveredDevice{{ID: "7e8f9a0b-1c2d-4e3f-8a4b-5c6d7e8f9a0b", Network: idIoT, IPs: []netip.Addr{other}}},
	}
	id.Addresses["7e8f9a0b-1c2d-4e3f-8a4b-5c6d7e8f9a0b"] = []netip.Addr{other}
	scope := func(s string) model.Scope {
		var sc model.Scope
		doc, err := ParseDocument([]byte(s), FormatYAML)
		if err != nil {
			t.Fatal(err)
		}
		if err := decodeInto(doc, &sc); err != nil {
			t.Fatal(err)
		}
		n, errs := Normalize(withRule(w.Config, sc))
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		return deref(n.AccessRules)[idNew].Source
	}
	for _, c := range []struct {
		scope, want string
	}{
		{`{device: esp32-42}`, "10.10.0.42/32"},
		// a device's address inside the range that identifies it is the range
		{`{device: lab-host}`, "10.50.0.0/28"},
		{`{group: sensors}`, "10.10.0.42/32"},
		// the subnet, plus the devices that belong to the network
		{`{network: IoT}`, "10.10.0.0/24 192.0.2.9/32"},
		{`{network: lab-hub}`, "10.50.0.0/24 10.99.0.0/24"},
		{`{remote_network: {client: lab-rA}}`, "10.50.0.0/24"},
	} {
		got, all := w.ScopePrefixes(scope(c.scope), id)
		if all || prefixList(got) != c.want {
			t.Errorf("%s: %s (all=%v), want %s", c.scope, prefixList(got), all, c.want)
		}
	}
	if got, all := w.ScopePrefixes(scope(`{global: true}`), id); !all || got != nil {
		t.Errorf("global: %v %v", got, all)
	}
	// a device that has no address covers nothing
	if got, _ := w.ScopePrefixes(scope(`{device: esp32-42}`), Identity{}); len(got) != 0 {
		t.Errorf("no address: %v", got)
	}
}

func TestDestinationSetsFollowTheNetworksAndTheManagementNetwork(t *testing.T) {
	w := newTestWorld(t, false).world()
	if got := prefixList(w.NetworkPrefixes(idIoT)); got != "10.10.0.0/24" {
		t.Errorf("IoT: %s", got)
	}
	// uplink is everything else: every network, the hub's client network, the link's transfer net
	// and the management network are excluded
	got := prefixList(w.NonUplinkPrefixes())
	for _, want := range []string{"10.10.0.0/24", "10.50.0.0/24", "10.99.0.0/24", "10.255.0.0/31", "192.168.88.0/24"} {
		if !strings.Contains(got, want) {
			t.Errorf("non-uplink set %s lacks %s", got, want)
		}
	}
	for _, out := range []string{"203.0.113.9", "8.8.8.8"} {
		if !w.viaUplink(netip.MustParseAddr(out)) || containsAny(w.NonUplinkPrefixes(), netip.MustParseAddr(out)) {
			t.Errorf("%s is routed via the uplink", out)
		}
	}
	for _, in := range []string{"10.10.0.9", "10.50.0.3", "192.168.88.7"} {
		a := netip.MustParseAddr(in)
		if w.viaUplink(a) || !containsAny(w.NonUplinkPrefixes(), a) {
			t.Errorf("%s must be outside the uplink", in)
		}
	}
}

func containsAny(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// withRule returns a copy of the configuration with one more access rule (id idNew) of the scope.
func withRule(cfg *model.Configuration, sc model.Scope) *model.Configuration {
	c := *cfg
	rules := map[string]model.AccessRule{}
	for k, v := range deref(cfg.AccessRules) {
		rules[k] = v
	}
	rules[idNew] = model.AccessRule{Source: sc, Action: model.AccessActionDrop}
	c.AccessRules = &rules
	return &c
}
