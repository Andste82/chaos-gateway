package wireguard

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
)

func example(t *testing.T) *model.Configuration {
	t.Helper()
	raw, err := os.ReadFile("../../api/examples/configuration.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func store(t *testing.T) *secrets.Store {
	t.Helper()
	s, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// hub and link of the example, by their kind
func networks(t *testing.T, cfg *model.Configuration) (hubID string, hub model.WireGuardNetwork, linkID string, link model.WireGuardNetwork) {
	t.Helper()
	for id, n := range *cfg.Networks {
		wg, err := n.AsWireGuardNetwork()
		if err != nil || wg.Type != model.WireGuardNetworkTypeWireguard {
			continue
		}
		if wg.Kind == model.Hub {
			hubID, hub = id, wg
		} else {
			linkID, link = id, wg
		}
	}
	return
}

func TestProvisionGeneratesKeysAndKeepsPrivateKeysOutOfTheConfiguration(t *testing.T) {
	sec := store(t)
	cfg := example(t)
	out, err := Provision(cfg, sec)
	if err != nil {
		t.Fatal(err)
	}
	hubID, hub, linkID, link := networks(t, out)
	// interface keys
	for _, id := range []string{hubID, linkID} {
		k, err := sec.WireGuard(id)
		if err != nil || !ValidKey(k.PrivateKey) {
			t.Fatalf("interface key of %s: %+v %v", id, k, err)
		}
	}
	// the client got a key pair: public key in the configuration, private key in the store
	var cid string
	var client model.WireGuardClient
	for id, c := range *hub.Clients {
		cid, client = id, c
	}
	k, err := sec.WireGuard(cid)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := PublicKey(k.PrivateKey)
	if client.Key == nil || client.Key.PublicKey == nil || *client.Key.PublicKey != pub || *client.Key.Mode != model.Generated {
		t.Errorf("client key %+v, want public key %s", client.Key, pub)
	}
	if k.PresharedKey == "" {
		t.Error("the example asks for a preshared key")
	}
	// the link peer of the example brings its own public key: nothing is generated for it
	if link.Peer.Key == nil || *link.Peer.Key.Mode != model.Provided {
		t.Errorf("link peer key %+v", link.Peer.Key)
	}
	// nothing secret in the configuration
	b, _ := json.Marshal(out)
	for _, id := range []string{hubID, cid} {
		sk, _ := sec.WireGuard(id)
		for _, secret := range []string{sk.PrivateKey, sk.PresharedKey} {
			if secret != "" && strings.Contains(string(b), secret) {
				t.Fatal("a private or preshared key is in the configuration")
			}
		}
	}
	// the input was not modified
	if _, c := firstClient(t, cfg); c.Key != nil && c.Key.PublicKey != nil && *c.Key.PublicKey == pub {
		t.Error("Provision changed its input")
	}
}

func firstClient(t *testing.T, cfg *model.Configuration) (string, model.WireGuardClient) {
	t.Helper()
	_, hub, _, _ := networks(t, cfg)
	for id, c := range *hub.Clients {
		return id, c
	}
	t.Fatal("no client")
	return "", model.WireGuardClient{}
}

func TestProvisionIsIdempotent(t *testing.T) {
	sec := store(t)
	a, err := Provision(example(t), sec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Provision(a, sec)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("a second provisioning changed the configuration")
	}
	cid, _ := firstClient(t, a)
	k1, _ := sec.WireGuard(cid)
	if _, err := Provision(b, sec); err != nil {
		t.Fatal(err)
	}
	if k2, _ := sec.WireGuard(cid); k1 != k2 {
		t.Error("the keys changed")
	}
}

func TestRaisingTheGenerationRotatesTheKeyPair(t *testing.T) {
	sec := store(t)
	cfg, _ := Provision(example(t), sec)
	cid, c := firstClient(t, cfg)
	before, _ := sec.WireGuard(cid)
	oldPub := *c.Key.PublicKey

	hubID, hub, _, _ := networks(t, cfg)
	cl := (*hub.Clients)[cid]
	gen := 1
	cl.Key.Generation = &gen
	(*hub.Clients)[cid] = cl
	n := (*cfg.Networks)[hubID]
	if err := n.FromWireGuardNetwork(hub); err != nil {
		t.Fatal(err)
	}
	(*cfg.Networks)[hubID] = n

	out, err := Provision(cfg, sec)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := sec.WireGuard(cid)
	_, c2 := firstClient(t, out)
	if after.PrivateKey == before.PrivateKey || *c2.Key.PublicKey == oldPub || after.Generation != 1 {
		t.Errorf("not rotated: %+v", after)
	}
	// the interface key stays
	ik, _ := sec.WireGuard(hubID)
	if ik.PrivateKey == "" {
		t.Error("interface key lost")
	}
}

func TestPresharedKeyFollowsTheSetting(t *testing.T) {
	sec := store(t)
	cfg, _ := Provision(example(t), sec)
	cid, _ := firstClient(t, cfg)
	if k, _ := sec.WireGuard(cid); k.PresharedKey == "" {
		t.Fatal("no preshared key")
	}
	hubID, hub, _, _ := networks(t, cfg)
	cl := (*hub.Clients)[cid]
	off := false
	cl.Key.PresharedKey = &off
	(*hub.Clients)[cid] = cl
	n := (*cfg.Networks)[hubID]
	_ = n.FromWireGuardNetwork(hub)
	(*cfg.Networks)[hubID] = n
	before, _ := sec.WireGuard(cid)
	if _, err := Provision(cfg, sec); err != nil {
		t.Fatal(err)
	}
	after, _ := sec.WireGuard(cid)
	if after.PresharedKey != "" || after.PrivateKey != before.PrivateKey {
		t.Errorf("%+v: the preshared key goes, the key pair stays", after)
	}
}

func TestAKeyThatWasExportedOnceIsNotRegenerated(t *testing.T) {
	sec := store(t)
	cfg, _ := Provision(example(t), sec)
	cid, c := firstClient(t, cfg)
	k, _ := sec.WireGuard(cid)
	k.PrivateKey, k.Exported = "", true // export once: the private key is deleted after the download
	if err := sec.PutWireGuard(cid, k); err != nil {
		t.Fatal(err)
	}
	out, err := Provision(cfg, sec)
	if err != nil {
		t.Fatal(err)
	}
	_, c2 := firstClient(t, out)
	if *c2.Key.PublicKey != *c.Key.PublicKey {
		t.Error("the public key changed although nothing asked for it")
	}
	if k2, _ := sec.WireGuard(cid); k2.PrivateKey != "" {
		t.Error("a private key reappeared")
	}
}

func TestProvideModeNeedsAValidPublicKey(t *testing.T) {
	sec := store(t)
	cfg := example(t)
	cid, _ := firstClient(t, cfg)
	hubID, hub, _, _ := networks(t, cfg)
	cl := (*hub.Clients)[cid]
	m := model.Provided
	cl.Key = &model.WireGuardKeySettings{Mode: &m}
	(*hub.Clients)[cid] = cl
	n := (*cfg.Networks)[hubID]
	_ = n.FromWireGuardNetwork(hub)
	(*cfg.Networks)[hubID] = n
	if _, err := Provision(cfg, sec); err == nil || !strings.Contains(err.Error(), "public key") {
		t.Fatalf("got %v", err)
	}
	pub, _ := PublicKey(refPriv)
	cl.Key.PublicKey = &pub
	(*hub.Clients)[cid] = cl
	n = (*cfg.Networks)[hubID]
	_ = n.FromWireGuardNetwork(hub)
	(*cfg.Networks)[hubID] = n
	out, err := Provision(cfg, sec)
	if err != nil {
		t.Fatal(err)
	}
	if _, c := firstClient(t, out); *c.Key.PublicKey != pub {
		t.Error("the provided key changed")
	}
	if k, err := sec.WireGuard(cid); err == nil && k.PrivateKey != "" {
		t.Error("a private key was stored for a provided key")
	}
}

func TestProvisionNeedsASecretsStoreForWireGuardNetworks(t *testing.T) {
	if _, err := Provision(example(t), nil); err == nil {
		t.Fatal("no store")
	}
	// a configuration without WireGuard needs none
	cfg := example(t)
	hubID, _, linkID, _ := networks(t, cfg)
	delete(*cfg.Networks, hubID)
	delete(*cfg.Networks, linkID)
	if _, err := Provision(cfg, nil); err != nil {
		t.Fatal(err)
	}
}

func TestInterfaceKeysAndPrune(t *testing.T) {
	sec := store(t)
	cfg, _ := Provision(example(t), sec)
	keys, err := InterfaceKeys(cfg, sec)
	if err != nil || len(keys) != 2 {
		t.Fatalf("%v %v", keys, err)
	}
	hubID, _, linkID, _ := networks(t, cfg)
	for _, id := range []string{hubID, linkID} {
		k, _ := sec.WireGuard(id)
		if want, _ := PublicKey(k.PrivateKey); keys[id] != want {
			t.Errorf("%s: %s, want %s", id, keys[id], want)
		}
	}
	// a client that is removed takes its secrets along when pruned
	cid, _ := firstClient(t, cfg)
	delete(*cfg.Networks, linkID)
	if err := Prune(cfg, sec); err != nil {
		t.Fatal(err)
	}
	if _, err := sec.WireGuard(linkID); err == nil {
		t.Error("the secrets of the removed link are still there")
	}
	if _, err := sec.WireGuard(cid); err != nil {
		t.Errorf("the client's secrets are gone: %v", err)
	}
	// a key that is missing is an error for the compiler's input
	_ = sec.DeleteWireGuard(hubID)
	if partial, err := InterfaceKeys(cfg, sec); err == nil || len(partial) != 0 {
		t.Errorf("a missing interface key must be reported: %v %v", partial, err)
	}
}
