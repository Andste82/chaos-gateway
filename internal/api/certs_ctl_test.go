package api_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChaosctlShowsInstallsAndResetsTheCertificate(t *testing.T) {
	g := ready(t)
	r := g.ctl("certificate", "show")
	if r.code != 0 || !strings.Contains(r.out, "source:      own") || !strings.Contains(r.out, "169.254.100.1") {
		t.Fatalf("show: %+v", r)
	}
	// the values of the lines start in the same column
	for _, label := range []string{"source:", "subject:", "issuer:", "valid:", "DNS names:", "IPs:", "SHA-256:"} {
		i := strings.Index(r.out, label)
		if i < 0 {
			t.Fatalf("show has no %s line:\n%s", label, r.out)
		}
		if line := r.out[i:]; len(line) < 14 || line[len(label):13] != strings.Repeat(" ", 13-len(label)) || line[13] == ' ' {
			t.Errorf("the value of %s does not start in column 14:\n%s", label, r.out)
		}
	}
	in := newCert(t, certSpec{dns: []string{"gw.lab"}})
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "chain.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, []byte(in.chainPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte(in.keyPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := g.ctl("certificate", "install", "--cert", certFile); r.code != 2 || !strings.Contains(r.err, "--key") {
		t.Errorf("without the key: %+v", r)
	}
	r = g.ctl("certificate", "install", "--cert", certFile, "--key", keyFile)
	if r.code != 0 || !strings.Contains(r.out, "source:      installed") || !strings.Contains(r.out, fingerprint(in.der)) || strings.Contains(r.out+r.err, "PRIVATE") {
		t.Fatalf("install: %+v", r)
	}
	// a bad pair is refused with the reason, and the token needs the scope full
	if err := os.WriteFile(keyFile, []byte(newCert(t, certSpec{}).keyPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := g.ctl("certificate", "install", "--cert", certFile, "--key", keyFile); r.code != 2 || !strings.Contains(r.err, "private key does not match") {
		t.Errorf("a key of another certificate: %+v", r)
	}
	admin := g.token
	g.token = g.mustTokenOf("overlays")
	if r := g.ctl("certificate", "reset"); r.code == 0 || !strings.Contains(r.err, "scope") {
		t.Errorf("reset with the scope overlays: %+v", r)
	}
	g.token = admin
	if r := g.ctl("certificate", "reset", "--json"); r.code != 0 || !strings.Contains(r.out, `"source": "own"`) {
		t.Errorf("reset: %+v", r)
	}
	if r := g.ctl("certificate", "show", "--json"); r.code != 0 || !strings.Contains(r.out, `"self_signed": true`) {
		t.Errorf("show after the reset: %+v", r)
	}
}
