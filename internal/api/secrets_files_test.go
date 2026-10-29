package api_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/api"
)

// M29, secret storage (plan §2.16: "Secrets live in a protected directory (0600, own volume)"): after the operations that make
// secrets, nothing in the secrets directory or in the directory of the test CAs' keys is readable by anybody but its owner, and the
// one file that holds a key of the HTTPS certificate is not the one that is published to the services.
func TestEverySecretOnDiskIsPrivateToItsOwner(t *testing.T) {
	g := ready(t)
	if r := put(g, newCert(t, certSpec{})); r.Status != 200 { // the installed certificate's key
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.do("POST", "/auth/tokens", map[string]any{"name": "ci", "scope": "read"}, nil, nil)
	g.do("POST", "/tls/ca/regenerate", nil, nil, nil)

	publicFiles := map[string]bool{ // certificates: what the services and the browsers get anyway
		filepath.Join(g.root, "secrets", "tls", "cert.pem"): true,
	}
	var seenKeys int
	walk := func(root string, keyFile func(path string, content string) bool) {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			switch {
			case d.IsDir() && root == filepath.Join(g.root, "secrets"):
				if info.Mode().Perm()&0o077 != 0 {
					t.Errorf("the directory %s is %o: group or others can look into the secrets", path, info.Mode().Perm())
				}
			case d.Type().IsRegular() && !publicFiles[path]:
				b, _ := os.ReadFile(path)
				if keyFile(path, string(b)) {
					seenKeys++
					if info.Mode().Perm()&0o077 != 0 {
						t.Errorf("%s holds a key or a credential and is %o", path, info.Mode().Perm())
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// everything below the secrets directory is a secret except the public certificate
	walk(filepath.Join(g.root, "secrets"), func(string, string) bool { return true })
	// the CAs of the TLS responder: the keys are private, the certificates are public by design
	walk(filepath.Join(g.root, "tlsca"), func(path, content string) bool {
		return strings.HasSuffix(path, ".key") || strings.Contains(content, "PRIVATE KEY")
	})
	if seenKeys < 6 { // auth.json, the WireGuard keys of the fixture, key.pem, ui.pem, the two CA keys
		t.Errorf("only %d secret files were looked at", seenKeys)
	}

	// the service token is the one secret outside both directories: the services mount it read-only and read it as the
	// group of the API (Kea is 0:65532, the DNS proxy 65532), so it is 0640 in a 0750 directory and nothing for others
	serviceFile := filepath.Join(g.root, "service", "token")
	if err := g.au.EnsureServiceToken(serviceFile); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(serviceFile); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("the service token file: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Dir(serviceFile)); err != nil || st.Mode().Perm() != 0o750 {
		t.Errorf("the directory of the service token: %v %v", st, err)
	}

	// the certificate published to the services is the gateway's own, without its key
	pub := filepath.Join(t.TempDir(), "api.pem")
	if err := api.PublishCertificate(filepath.Join(g.root, "secrets", "tls"), pub); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "PRIVATE KEY") || !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		t.Errorf("the published certificate: %q", b)
	}
	if st, _ := os.Stat(pub); st.Mode().Perm() != 0o644 {
		t.Errorf("the published certificate is %o", st.Mode().Perm())
	}
}
