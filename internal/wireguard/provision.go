package wireguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
)

// Provision makes sure every WireGuard object of a configuration has the keys it needs and returns
// a copy of the configuration with the public keys filled in (plan §2.2.1): the interface key of
// each network, and for every client or link peer in mode `generated` a key pair (and a preshared
// key when asked for). Private keys go to the secrets store, never into the configuration. Raising
// `key.generation` rotates the key pair. The call is idempotent: a second call changes nothing.
//
// A candidate is provisioned before it is stored, so the revision carries the public keys and no
// secret.
func Provision(cfg *model.Configuration, sec *secrets.Store) (*model.Configuration, error) {
	out, err := clone(cfg)
	if err != nil {
		return nil, err
	}
	if out.Networks == nil {
		return out, nil
	}
	ids := make([]string, 0, len(*out.Networks))
	for id := range *out.Networks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := (*out.Networks)[id]
		wg, err := n.AsWireGuardNetwork()
		if err != nil || wg.Type != model.WireGuardNetworkTypeWireguard {
			continue
		}
		if sec == nil {
			return nil, errors.New("wireguard: the configuration has WireGuard networks but no secrets store was given")
		}
		if err := ensureInterfaceKey(sec, id); err != nil {
			return nil, err
		}
		switch wg.Kind {
		case model.Hub:
			if wg.Clients != nil {
				clients := *wg.Clients
				for cid, c := range clients {
					key, err := ensurePeerKey(sec, cid, c.Key)
					if err != nil {
						return nil, fmt.Errorf("client %q: %w", c.Name, err)
					}
					c.Key = key
					clients[cid] = c
				}
			}
		case model.Link:
			if wg.Peer != nil {
				key, err := ensurePeerKey(sec, LinkPeerKeyID(id), wg.Peer.Key)
				if err != nil {
					return nil, fmt.Errorf("link %q: %w", wg.Name, err)
				}
				wg.Peer.Key = key
			}
		}
		if err := n.FromWireGuardNetwork(wg); err != nil {
			return nil, err
		}
		(*out.Networks)[id] = n
	}
	return out, nil
}

func clone(cfg *model.Configuration) (*model.Configuration, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out model.Configuration
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ensureInterfaceKey creates the private key of a network's interface when it has none.
func ensureInterfaceKey(sec *secrets.Store, id string) error {
	k, err := sec.WireGuard(id)
	if err == nil && k.PrivateKey != "" {
		return nil
	}
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	priv, err := GeneratePrivateKey()
	if err != nil {
		return err
	}
	return sec.PutWireGuard(id, secrets.WireGuardKeys{PrivateKey: priv})
}

// ensurePeerKey applies the key settings of a peer: generated keys are created, rotated and
// recorded; provided keys are left alone. It returns the settings with the public key filled in.
func ensurePeerKey(sec *secrets.Store, id string, ks *model.WireGuardKeySettings) (*model.WireGuardKeySettings, error) {
	if ks == nil {
		ks = &model.WireGuardKeySettings{}
	}
	mode := model.Generated
	if ks.Mode != nil {
		mode = *ks.Mode
	}
	ks.Mode = &mode
	wantPSK := ks.PresharedKey != nil && *ks.PresharedKey
	if mode == model.Provided {
		if ks.PublicKey == nil || !ValidKey(*ks.PublicKey) {
			return nil, errors.New("a provided key needs a valid public key")
		}
		// the peer keeps its own key pair, but a preshared key is shared: the gateway generates it
		// and the export tells the administrator
		rec, err := sec.WireGuard(id)
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return nil, err
		}
		switch {
		case wantPSK && rec.PresharedKey == "":
			if rec.PresharedKey, err = GeneratePresharedKey(); err != nil {
				return nil, err
			}
			if err := sec.PutWireGuard(id, rec); err != nil {
				return nil, err
			}
		case !wantPSK && (rec.PresharedKey != "" || rec.PrivateKey != ""):
			if err := sec.DeleteWireGuard(id); err != nil {
				return nil, err
			}
		}
		return ks, nil
	}
	gen := 0
	if ks.Generation != nil {
		gen = *ks.Generation
	}
	rec, err := sec.WireGuard(id)
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return nil, err
	}
	exists := err == nil
	needPair := !exists || rec.Generation != gen
	if needPair {
		priv, err := GeneratePrivateKey()
		if err != nil {
			return nil, err
		}
		rec = secrets.WireGuardKeys{PrivateKey: priv, Generation: gen}
	}
	switch {
	case wantPSK && rec.PresharedKey == "":
		if rec.PresharedKey, err = GeneratePresharedKey(); err != nil {
			return nil, err
		}
		needPair = true
	case !wantPSK && rec.PresharedKey != "":
		rec.PresharedKey = ""
		needPair = true
	}
	if needPair {
		if err := sec.PutWireGuard(id, rec); err != nil {
			return nil, err
		}
	}
	// the public key of an exported-once key outlives its private key: it stays as the revision has it
	if rec.PrivateKey != "" {
		pub, err := PublicKey(rec.PrivateKey)
		if err != nil {
			return nil, err
		}
		ks.PublicKey = &pub
	} else if ks.PublicKey == nil {
		return nil, errors.New("the private key was deleted after its export and the configuration has no public key: rotate the key")
	}
	return ks, nil
}

// InterfaceKeys returns the public key of every WireGuard network's interface, derived from the
// private keys in the store; networks without a usable key are left out and the first problem is
// returned with the keys that exist. The compiler needs them for verify and for the exports; they are not
// secret.
func InterfaceKeys(cfg *model.Configuration, sec *secrets.Store) (map[string]string, error) {
	out := map[string]string{}
	if cfg.Networks == nil {
		return out, nil
	}
	var first error
	for id, n := range *cfg.Networks {
		wg, err := n.AsWireGuardNetwork()
		if err != nil || wg.Type != model.WireGuardNetworkTypeWireguard {
			continue
		}
		if sec == nil {
			return out, errors.New("wireguard: no secrets store")
		}
		k, err := sec.WireGuard(id)
		if err == nil {
			var pub string
			if pub, err = PublicKey(k.PrivateKey); err == nil {
				out[id] = pub
				continue
			}
		}
		if first == nil {
			first = fmt.Errorf("the key of network %q: %w", wg.Name, err)
		}
	}
	// the keys that could be derived are returned in any case: the compiler reports the networks
	// without one, each by name
	return out, first
}

// Prune deletes the secrets of objects that are not in the configuration any more. Call it after a
// revision without them has been committed.
func Prune(cfg *model.Configuration, sec *secrets.Store) error {
	keep := map[string]bool{}
	if cfg.Networks != nil {
		for id, n := range *cfg.Networks {
			wg, err := n.AsWireGuardNetwork()
			if err != nil || wg.Type != model.WireGuardNetworkTypeWireguard {
				continue
			}
			keep[id] = true
			keep[LinkPeerKeyID(id)] = true
			if wg.Clients != nil {
				for cid := range *wg.Clients {
					keep[cid] = true
				}
			}
		}
	}
	ids, err := sec.IDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !keep[id] {
			if err := sec.DeleteWireGuard(id); err != nil {
				return err
			}
		}
	}
	return nil
}
