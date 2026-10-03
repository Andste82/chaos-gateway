package wireguard

import "testing"

// Reference pair made with `wg genkey | wg pubkey`.
const (
	refPriv = "cFTvlxJ49hG8zsQ91QN8ATdE5+4QELIulw1yJEvpdGY="
	refPub  = "FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo="
)

func TestPublicKeyMatchesWg(t *testing.T) {
	got, err := PublicKey(refPriv)
	if err != nil || got != refPub {
		t.Fatalf("%q %v, want %q", got, err, refPub)
	}
}

func TestGeneratedKeysAreValidClampedAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		k, err := GeneratePrivateKey()
		if err != nil || !ValidKey(k) {
			t.Fatalf("%q %v", k, err)
		}
		if seen[k] {
			t.Fatal("a key repeated")
		}
		seen[k] = true
		b, _ := decodeKey(k)
		if b[0]&7 != 0 || b[31]&128 != 0 || b[31]&64 == 0 {
			t.Fatalf("the key is not clamped: %x", b)
		}
		if _, err := PublicKey(k); err != nil {
			t.Fatal(err)
		}
	}
	psk, err := GeneratePresharedKey()
	if err != nil || !ValidKey(psk) {
		t.Fatalf("%q %v", psk, err)
	}
}

func TestInvalidKeysAreRejected(t *testing.T) {
	for _, k := range []string{"", "abc", "!!!!", refPub[:40], refPub + "A"} {
		if ValidKey(k) {
			t.Errorf("%q accepted", k)
		}
		if _, err := PublicKey(k); err == nil {
			t.Errorf("PublicKey(%q) accepted", k)
		}
	}
}
