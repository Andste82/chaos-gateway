package apply_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

const (
	hubID   = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	adminID = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"
	linkID  = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	clientA = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

// wgEnv is the harness with WireGuard networks, a secrets store and an executor that can read it.
type wgEnv struct {
	*env
	sec *secrets.Store
}

func newWGEnv(t *testing.T) *wgEnv { return newWGEnvFile(t, "wireguard.yaml") }

func newWGEnvFile(t *testing.T, file string) *wgEnv {
	t.Helper()
	k := newTestbedKernel()
	sec, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex, err := executor.New(k, executor.WithBirdDir(t.TempDir()), executor.WithKeys(func(id string) (string, string, error) {
		kk, err := sec.WireGuard(id)
		return kk.PrivateKey, kk.PresharedKey, err
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	raw, err := os.ReadFile("../compiler/testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg, errs := domain.Normalize(cfg)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	e := &wgEnv{env: &env{t: t, k: k, ex: ex, cfg: cfg}, sec: sec}
	e.provision()
	return e
}

func (e *wgEnv) provision() {
	e.t.Helper()
	cfg, err := wireguard.Provision(e.cfg, e.sec)
	if err != nil {
		e.t.Fatal(err)
	}
	e.cfg = cfg
}

func (e *wgEnv) compile() *compiler.Target {
	e.t.Helper()
	return e.env.compile(func(_ *model.Configuration, in *compiler.Input) {
		keys, err := wireguard.InterfaceKeys(e.cfg, e.sec)
		if err != nil {
			e.t.Fatal(err)
		}
		in.Keys = keys
	})
}

func (e *wgEnv) apply() *apply.Result {
	e.t.Helper()
	tg := e.compile()
	if tg.HasErrors() {
		e.t.Fatalf("%+v", tg.Problems)
	}
	return e.env.apply(tg)
}

func (e *wgEnv) wgOps(res *apply.Result) (n int) {
	for _, op := range res.Plan.Ops {
		if _, ok := op.(*executor.WireGuard); ok {
			n++
		}
	}
	return n
}

func (e *wgEnv) syncs() (n int) {
	for _, c := range e.k.Commands() {
		if strings.HasPrefix(c, "wg syncconf") {
			n++
		}
	}
	return n
}

func TestWireGuardNetworksAreBuiltAndVerified(t *testing.T) {
	e := newWGEnv(t)
	res := e.apply()
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
	s := res.After
	for _, name := range []string{"wg-lab-hub", "wg-admin", "wg-site-b"} {
		l := s.Links[name]
		if l.Kind() != "wireguard" || !l.Up() {
			t.Errorf("%s: %+v", name, l)
		}
	}
	if l := s.Links["wg-site-b"]; l.MTU != 1380 {
		t.Errorf("the link's MTU %d", l.MTU)
	}
	if l := s.Links["wg-lab-hub"]; l.MTU != 1420 {
		t.Errorf("the default MTU %d", l.MTU)
	}
	if a := s.Addrs["wg-lab-hub"]; len(a) != 1 || a[0].Local != "10.99.0.1" || a[0].PrefixLen != 24 {
		t.Errorf("address %+v", a)
	}
	// the interface got the stored private key and the peers their parameters
	hubKeys, _ := e.sec.WireGuard(hubID)
	if got := e.k.WireGuardKey("wg-lab-hub"); got != hubKeys.PrivateKey || got == "" {
		t.Error("the kernel does not have the stored private key")
	}
	hub := s.WireGuard["wg-lab-hub"]
	if hub.ListenPort != 51820 || len(hub.Peers) != 1 || !hub.Peers[0].HasPresharedKey || strings.Join(hub.Peers[0].AllowedIPs, ",") != "10.99.0.2/32,10.50.0.0/24" {
		t.Errorf("%+v", hub)
	}
	site := s.WireGuard["wg-site-b"]
	if len(site.Peers) != 1 || site.Peers[0].Endpoint != "203.0.113.40:51821" || site.Peers[0].Keepalive != 25 || site.Peers[0].AllowedIPs[0] != "0.0.0.0/0" {
		t.Errorf("%+v", site)
	}
	// routes and rules
	var routes int
	for _, r := range s.Routes {
		if r.Table == "100" && (r.Dev == "wg-lab-hub" || r.Dev == "wg-site-b" || r.Dev == "wg-admin") {
			routes++
		}
	}
	if routes != 5 {
		t.Errorf("%d WireGuard routes in table 100", routes)
	}
	if strings.Join(s.Assigned, ",") != "br-iot,lan0,mgmt0,wan0,wg-admin,wg-lab-hub,wg-site-b" {
		t.Errorf("assigned %v", s.Assigned)
	}
}

func TestAReapplyDoesNotTouchTheTunnels(t *testing.T) {
	e := newWGEnv(t)
	e.apply()
	// a handshake happened: the tunnel is established
	tg := e.compile()
	peer := tg.WireGuard[1].Peers[0].PublicKey // wg-lab-hub
	e.k.Handshake("wg-lab-hub", peer, 1760000000, 1000, 2000)
	e.k.ClearLog()
	res := e.apply()
	if e.wgOps(res) != 0 || e.syncs() != 0 {
		t.Errorf("a re-apply synchronized WireGuard: %d ops, %d syncconf, plan %v", e.wgOps(res), e.syncs(), res.Plan.Summary)
	}
	for _, c := range e.k.Commands() {
		if strings.Contains(c, "link add") || strings.Contains(c, "link delete") || strings.Contains(c, " mtu ") {
			t.Errorf("a re-apply ran %q", c)
		}
	}
	if p := res.After.WireGuard["wg-lab-hub"].Peers[0]; p.LatestHandshake != 1760000000 || p.RxBytes != 1000 {
		t.Errorf("the session was lost: %+v", p)
	}
}

func TestChangingAClientSynchronizesWithoutLosingTheOthers(t *testing.T) {
	e := newWGEnv(t)
	e.apply()
	tg := e.compile()
	rA := tg.WireGuard[1].Peers[0].PublicKey
	e.k.Handshake("wg-lab-hub", rA, 1760000000, 5, 6)
	e.k.ClearLog()

	// another network behind the client
	n := (*e.cfg.Networks)[hubID]
	wg, _ := n.AsWireGuardNetwork()
	c := (*wg.Clients)[clientA]
	c.ClientNetworks = &[]string{"10.50.0.0/24", "10.51.0.0/24"}
	(*wg.Clients)[clientA] = c
	_ = n.FromWireGuardNetwork(wg)
	(*e.cfg.Networks)[hubID] = n
	res := e.apply()
	if e.syncs() != 1 || e.wgOps(res) != 1 {
		t.Fatalf("%d syncs, %d ops: %v", e.syncs(), e.wgOps(res), res.Plan.Summary)
	}
	p := res.After.WireGuard["wg-lab-hub"].Peers[0]
	if strings.Join(p.AllowedIPs, ",") != "10.99.0.2/32,10.50.0.0/24,10.51.0.0/24" || p.LatestHandshake != 1760000000 {
		t.Errorf("%+v: the peer has the new network and keeps its session", p)
	}
	// the route to the new network
	var found bool
	for _, r := range res.After.Routes {
		found = found || r.Dst == "10.51.0.0/24" && r.Dev == "wg-lab-hub" && r.Table == "100"
	}
	if !found {
		t.Error("no route to the new client network")
	}
}

func TestDisablingAClientRemovesItsPeerAndItsRoutes(t *testing.T) {
	e := newWGEnv(t)
	e.apply()
	n := (*e.cfg.Networks)[hubID]
	wg, _ := n.AsWireGuardNetwork()
	c := (*wg.Clients)[clientA]
	off := false
	c.Enabled = &off
	(*wg.Clients)[clientA] = c
	_ = n.FromWireGuardNetwork(wg)
	(*e.cfg.Networks)[hubID] = n
	res := e.apply()
	if len(res.After.WireGuard["wg-lab-hub"].Peers) != 0 {
		t.Errorf("the disabled client is still a peer: %+v", res.After.WireGuard["wg-lab-hub"].Peers)
	}
	for _, r := range res.After.Routes {
		if r.Dst == "10.50.0.0/24" && r.Table == "100" {
			t.Error("the route of the disabled client's network is still there")
		}
	}
}

func TestRotatingTheInterfaceKeyIsSynchronizedAndVerified(t *testing.T) {
	e := newWGEnv(t)
	e.apply()
	oldPub := e.compile().WireGuard[1].PublicKey
	priv, _ := wireguard.GeneratePrivateKey()
	if err := e.sec.PutWireGuard(hubID, secrets.WireGuardKeys{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	res := e.apply()
	if got := res.After.WireGuard["wg-lab-hub"].PublicKey; got == oldPub || got == "" {
		t.Errorf("public key %s, was %s", got, oldPub)
	}
	if e.k.WireGuardKey("wg-lab-hub") != priv {
		t.Error("the kernel does not have the new key")
	}
}

func TestARemovedWireGuardNetworkIsDeletedWithItsRoutes(t *testing.T) {
	e := newWGEnv(t)
	e.apply()
	delete(*e.cfg.Networks, linkID)
	res := e.apply()
	if _, ok := res.After.Links["wg-site-b"]; ok {
		t.Error("the interface of the removed network is still there")
	}
	for _, r := range res.After.Routes {
		if r.Dev == "wg-site-b" {
			t.Errorf("route %+v", r)
		}
	}
	if strings.Contains(strings.Join(res.After.Assigned, ","), "wg-site-b") {
		t.Errorf("assigned %v", res.After.Assigned)
	}
	for _, r := range res.After.Rules {
		if r.Iif == "wg-site-b" {
			t.Errorf("rule %+v", r)
		}
	}
	// the tunnel of the others is untouched
	if res.After.Links["wg-lab-hub"].Kind() != "wireguard" {
		t.Error("the hub is gone")
	}
}

func TestMTUChangeIsAppliedAndVerifyNoticesManipulations(t *testing.T) {
	e := newWGEnv(t)
	e.apply()
	n := (*e.cfg.Networks)[hubID]
	wg, _ := n.AsWireGuardNetwork()
	mtu := 1300
	wg.Mtu = &mtu
	_ = n.FromWireGuardNetwork(wg)
	(*e.cfg.Networks)[hubID] = n
	res := e.apply()
	if res.After.Links["wg-lab-hub"].MTU != 1300 {
		t.Errorf("mtu %d", res.After.Links["wg-lab-hub"].MTU)
	}

	for name, c := range map[string]struct {
		do   func(*kernelsim.Kernel, *compiler.Target)
		want string
	}{
		"mtu": {func(k *kernelsim.Kernel, tg *compiler.Target) {
			_, _ = k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"link", "set", "dev", "wg-lab-hub", "mtu", "1500"}})
		}, "MTU 1500"},
		"address": {func(k *kernelsim.Kernel, tg *compiler.Target) { k.SetAddr("wg-lab-hub", "10.77.0.1/24") }, "has the addresses"},
		"down": {func(k *kernelsim.Kernel, tg *compiler.Target) {
			_, _ = k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"link", "set", "dev", "wg-lab-hub", "down"}})
		}, "is down"},
		"peer removed": {func(k *kernelsim.Kernel, tg *compiler.Target) {
			_, _ = k.Run(context.Background(), executor.Command{Tool: executor.ToolWg, Args: []string{"syncconf", "wg-lab-hub", "/dev/stdin"},
				Stdin: "[Interface]\nPrivateKey = " + k.WireGuardKey("wg-lab-hub") + "\nListenPort = 51820\n"})
		}, "is missing"},
		"port": {func(k *kernelsim.Kernel, tg *compiler.Target) {
			_, _ = k.Run(context.Background(), executor.Command{Tool: executor.ToolWg, Args: []string{"syncconf", "wg-lab-hub", "/dev/stdin"},
				Stdin: "[Interface]\nPrivateKey = " + k.WireGuardKey("wg-lab-hub") + "\nListenPort = 4000\n[Peer]\nPublicKey = " + tg.WireGuard[1].Peers[0].PublicKey + "\nAllowedIPs = 10.99.0.2/32, 10.50.0.0/24\n"})
		}, "listen port"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newWGEnv(t)
			tg := e.compile()
			e.env.apply(tg)
			c.do(e.k, tg)
			s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, m := range apply.Verify(tg, s) {
				found = found || strings.Contains(m.String(), c.want)
			}
			if !found {
				t.Fatalf("verify did not report %q: %v", c.want, apply.Verify(tg, s))
			}
			// and the next apply repairs it
			res := e.env.apply(e.compile())
			if len(res.Mismatches) != 0 {
				t.Fatal(res.Mismatches)
			}
		})
	}
}

func TestNoSecretReachesAnArgumentASummaryOrALog(t *testing.T) {
	e := newWGEnv(t)
	res := e.apply()
	var secretsSeen []string
	for _, id := range []string{hubID, adminID, linkID, clientA} {
		if k, err := e.sec.WireGuard(id); err == nil {
			secretsSeen = append(secretsSeen, k.PrivateKey, k.PresharedKey)
		}
	}
	var texts []string
	texts = append(texts, e.k.Commands()...)
	texts = append(texts, res.Plan.Summary...)
	for _, op := range res.Plan.Ops {
		b, _ := executor.Encode(op)
		texts = append(texts, string(b))
	}
	d := apply.Diff(e.compile(), res.After)
	texts = append(texts, d.Nftables, d.Routes)
	for _, text := range texts {
		for _, s := range secretsSeen {
			if s != "" && strings.Contains(text, s) {
				t.Fatalf("a secret is in: %s", text)
			}
		}
	}
	if len(secretsSeen) == 0 {
		t.Fatal("no secrets were generated")
	}
}

func TestAWireGuardInterfaceThatIsNotOursIsNotTakenOver(t *testing.T) {
	e := newWGEnv(t)
	// an operating system interface with the name, which is a WireGuard interface
	_, _ = e.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"link", "add", "dev", "wg-lab-hub", "type", "wireguard"}})
	_, err := apply.Preview(context.Background(), e.exec(), "", e.compile())
	var ae *apply.Error
	if !asError(err, &ae) || !strings.Contains(err.Error(), "does not belong to Chaos Gateway") {
		t.Fatalf("got %v", err)
	}
	// and a device of another kind with that name
	e2 := newWGEnv(t)
	e2.k.AddLink("wg-lab-hub", "02:aa:00:00:00:09", "veth", true)
	_, err = apply.Preview(context.Background(), e2.exec(), "", e2.compile())
	if !asError(err, &ae) || !strings.Contains(err.Error(), "is not a WireGuard interface") {
		t.Fatalf("got %v", err)
	}
}

func TestThePreviewShowsWireGuardChanges(t *testing.T) {
	e := newWGEnv(t)
	d := diffOf(t, e.env, e.compile())
	for _, want := range []string{"+wireguard wg-lab-hub up", "+  address 10.99.0.1/24 mtu 1420 port 51820", "+  peer ", "keepalive 25 endpoint 203.0.113.40:51821", " psk"} {
		if !strings.Contains(d.WireGuard, want) {
			t.Errorf("the diff lacks %q:\n%s", want, d.WireGuard)
		}
	}
	if strings.Contains(d.Routes, "wireguard ") {
		t.Errorf("WireGuard lines leaked into the routes diff:\n%s", d.Routes)
	}
	e.apply()
	if d := diffOf(t, e.env, e.compile()); d.Routes != "" || d.Nftables != "" || d.WireGuard != "" {
		t.Errorf("a diff after the apply:\n%s\n%s\n%s", d.Routes, d.Nftables, d.WireGuard)
	}
}

func TestWireGuardApplyNeedsTheKeys(t *testing.T) {
	e := newWGEnv(t)
	if err := e.sec.DeleteWireGuard(hubID); err != nil {
		t.Fatal(err)
	}
	// the compiler refuses before anything happens: the interface key is missing
	if _, err := wireguard.InterfaceKeys(e.cfg, e.sec); err == nil {
		t.Fatal("a missing interface key must be an error")
	}
	tg := e.env.compile()
	if !tg.HasErrors() {
		t.Fatal("a target without keys has errors")
	}
}

func TestAHostNameEndpointDoesNotMakeEveryApplyFail(t *testing.T) {
	e := newWGEnv(t)
	n := (*e.cfg.Networks)[linkID]
	wg, _ := n.AsWireGuardNetwork()
	host := "site.example.net:51821"
	wg.Peer.Endpoint = &host
	_ = n.FromWireGuardNetwork(wg)
	(*e.cfg.Networks)[linkID] = n
	e.apply() // the kernel reports the resolved address: the name cannot be compared
	e.k.ClearLog()
	res := e.apply()
	if e.syncs() != 0 || e.wgOps(res) != 0 {
		t.Errorf("a re-apply synchronizes the link again: %v", res.Plan.Summary)
	}
}

func TestRepliesToANetworkBehindATunnelAreRoutedByTable100(t *testing.T) {
	e := newWGEnv(t)
	res := e.apply()
	var to []string
	for _, r := range res.After.Rules {
		if r.Protocol == "201" && r.Dst != "" && r.Dst != "all" {
			to = append(to, r.Dst)
		}
	}
	if len(to) != 2 {
		t.Errorf("destination rules in the kernel: %v", to)
	}
}
