// Package secrets is the protected store for key material (plan §2.16): files with mode 0600 in a
// directory with mode 0700, written atomically. It holds WireGuard keys today. Nothing from here
// ends up in revisions, exports, logs, events or API responses: the compiler and the configuration
// know keys only by reference (the UUID of the network, client or link).
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// WireGuardKeys is the secret material of one WireGuard object: the private key of a network's
// interface, or of a client or link peer that the gateway generated (so it can export a complete
// configuration), and the optional preshared key.
type WireGuardKeys struct {
	PrivateKey   string `json:"private_key,omitempty"`
	PresharedKey string `json:"preshared_key,omitempty"`
	// Generation is the key generation of the configuration this pair belongs to: raising
	// `key.generation` in a candidate rotates the key.
	Generation int `json:"generation,omitempty"`
	// Exported is set after the first export; with `export_once` the private key is gone by then.
	Exported bool `json:"exported,omitempty"`
}

// ErrNotFound means there is no such secret.
var ErrNotFound = errors.New("secrets: not found")

var idRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Store is a directory of secrets.
type Store struct{ dir string }

// Open creates the directory (mode 0700) if it is missing and checks that nobody else can read it.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "wireguard"), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Join(dir, "wireguard"), 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) (string, error) {
	// the id becomes a file name: nothing but a UUID is accepted
	if !idRE.MatchString(id) {
		return "", fmt.Errorf("secrets: %q is not a UUID", id)
	}
	return filepath.Join(s.dir, "wireguard", id+".json"), nil
}

// WireGuard returns the keys of an object.
func (s *Store) WireGuard(id string) (WireGuardKeys, error) {
	p, err := s.path(id)
	if err != nil {
		return WireGuardKeys{}, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return WireGuardKeys{}, ErrNotFound
	}
	if err != nil {
		return WireGuardKeys{}, err
	}
	var k WireGuardKeys
	if err := json.Unmarshal(b, &k); err != nil {
		return WireGuardKeys{}, fmt.Errorf("secrets: %s: %w", filepath.Base(p), err)
	}
	return k, nil
}

// PutWireGuard stores the keys of an object, replacing what was there.
func (s *Store) PutWireGuard(id string, k WireGuardKeys) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	b, err := json.Marshal(k)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	f, err := os.Open(tmp)
	if err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	return os.Rename(tmp, p)
}

// DeleteWireGuard removes the keys of an object; removing what is not there is fine.
func (s *Store) DeleteWireGuard(id string) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// IDs lists the objects that have WireGuard keys.
func (s *Store) IDs() ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(s.dir, "wireguard"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); len(n) == 41 && n[36:] == ".json" {
			out = append(out, n[:36])
		}
	}
	return out, nil
}
