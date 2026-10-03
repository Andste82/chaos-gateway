// Package wireguard holds what the gateway knows about WireGuard that is not Linux: key
// generation and derivation, provisioning of the keys of a configuration, and the export of client
// and link configurations (plan §2.2.1).
package wireguard

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/crypto/curve25519"
)

// linkPeerNamespace derives the secrets id of a link's remote side.
var linkPeerNamespace = uuid.MustParse("5b0c3a62-6a2e-4f3c-9f7e-2b8d7a1c4e90")

// LinkPeerKeyID is the secrets store id of the remote side of a link. The link's own id holds the
// key of the gateway's interface, so the remote side needs another one.
func LinkPeerKeyID(networkID string) string {
	return uuid.NewSHA1(linkPeerNamespace, []byte(networkID+"/peer")).String()
}

// KeyLen is the length of a WireGuard key in bytes.
const KeyLen = 32

// GeneratePrivateKey returns a new private key in WireGuard's base64 form (`wg genkey`).
func GeneratePrivateKey() (string, error) {
	var k [KeyLen]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", err
	}
	// clamp as Curve25519 requires
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

// GeneratePresharedKey returns a new preshared key (`wg genpsk`).
func GeneratePresharedKey() (string, error) {
	var k [KeyLen]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

// PublicKey derives the public key of a private key (`wg pubkey`).
func PublicKey(private string) (string, error) {
	b, err := decodeKey(private)
	if err != nil {
		return "", err
	}
	pub, err := curve25519.X25519(b, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// ValidKey reports whether s is a WireGuard key: 32 bytes in base64.
func ValidKey(s string) bool { _, err := decodeKey(s); return err == nil }

func decodeKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("not a WireGuard key: %w", err)
	}
	if len(b) != KeyLen {
		return nil, errors.New("not a WireGuard key: wrong length")
	}
	return b, nil
}
