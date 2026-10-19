package ctl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The ways to trust a gateway are one setting. A flag on the command line replaces the trust variables of the
// environment as a group, the variables do not override a flag (CHAOSGW_INSECURE=1 next to --fingerprint sent the
// token to a peer that was never checked), and "no verification" next to a pin or a CA file is a usage error.
func TestTheTrustFlagsWinOverTheTrustVariablesAndInsecureNeverMixesWithAPin(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"removed_overlays":2,"aborted_runs":0,"generation":7}`))
	}))
	defer srv.Close()
	cert := srv.Certificate()
	sum := sha256.Sum256(cert.Raw)
	fp := hex.EncodeToString(sum[:])
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	badFile := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(badFile, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrong := strings.Repeat("00", 32)

	for name, tc := range map[string]struct {
		args     []string
		env      map[string]string
		code     int
		want     string
		requests int
		warns    bool
	}{
		"an insecure environment does not beat a pin":      {[]string{"--fingerprint", wrong}, map[string]string{"CHAOSGW_INSECURE": "1"}, 2, "pinned", 0, false},
		"an insecure environment does not beat a CA file":  {[]string{"--ca-cert", badFile}, map[string]string{"CHAOSGW_INSECURE": "1"}, 2, "PEM", 0, false},
		"--insecure=false beats an insecure environment":   {[]string{"--insecure=false"}, map[string]string{"CHAOSGW_INSECURE": "1"}, 2, "certificate", 0, false},
		"a CA flag beats a wrong pin of the environment":   {[]string{"--ca-cert", caFile}, map[string]string{"CHAOSGW_FINGERPRINT": wrong}, 0, "removed 2 overlays", 1, false},
		"a CA flag beats a wrong CA of the environment":    {[]string{"--ca-cert", caFile}, map[string]string{"CHAOSGW_CA_CERT": badFile}, 0, "removed 2 overlays", 1, false},
		"a right pin flag beats an insecure environment":   {[]string{"--fingerprint", fp}, map[string]string{"CHAOSGW_INSECURE": "1"}, 0, "removed 2 overlays", 1, false},
		"--insecure and --fingerprint":                     {[]string{"--insecure", "--fingerprint", fp}, nil, 2, "cannot be combined", 0, false},
		"--insecure and --ca-cert":                         {[]string{"--insecure", "--ca-cert", caFile}, nil, 2, "cannot be combined", 0, false},
		"an insecure and a pinned environment":             {nil, map[string]string{"CHAOSGW_INSECURE": "1", "CHAOSGW_FINGERPRINT": fp}, 2, "cannot be combined", 0, false},
		"an insecure and a CA file in the environment":     {nil, map[string]string{"CHAOSGW_INSECURE": "true", "CHAOSGW_CA_CERT": caFile}, 2, "cannot be combined", 0, false},
		"--insecure ignores the pin of the environment":    {[]string{"--insecure"}, map[string]string{"CHAOSGW_FINGERPRINT": wrong}, 0, "removed 2 overlays", 1, true},
		"--insecure warns":                                 {[]string{"--insecure"}, nil, 0, "not verified", 1, true},
		"an insecure environment warns":                    {nil, map[string]string{"CHAOSGW_INSECURE": "yes"}, 0, "not verified", 1, true},
		"a pin of the environment does not warn":           {nil, map[string]string{"CHAOSGW_FINGERPRINT": fp}, 0, "removed 2 overlays", 1, false},
		"an environment that is not 1/true/yes is no flag": {nil, map[string]string{"CHAOSGW_INSECURE": "0"}, 2, "certificate", 0, false},
	} {
		mu.Lock()
		requests = 0
		mu.Unlock()
		var out, errb bytes.Buffer
		env := map[string]string{"CHAOSGW_URL": srv.URL, "CHAOSGW_TOKEN": "secret"}
		for k, v := range tc.env {
			env[k] = v
		}
		code := Run(append(append([]string{}, tc.args...), "reset"), Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return env[k] }})
		mu.Lock()
		n := requests
		mu.Unlock()
		if code != tc.code || !strings.Contains(out.String()+errb.String(), tc.want) || n != tc.requests {
			t.Errorf("%s: code %d, %d requests, stdout %q, stderr %q", name, code, n, out.String(), errb.String())
		}
		if warned := strings.Contains(errb.String(), "not verified"); warned != tc.warns {
			t.Errorf("%s: the warning is %v, want %v: %q", name, warned, tc.warns, errb.String())
		}
	}

	// a plain http address has no certificate to verify, so there is nothing to warn about
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"removed_overlays":0,"aborted_runs":0,"generation":1}`))
	}))
	defer plain.Close()
	var out, errb bytes.Buffer
	if code := Run([]string{"--insecure", "reset"}, Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string {
		return map[string]string{"CHAOSGW_URL": plain.URL, "CHAOSGW_TOKEN": "t"}[k]
	}}); code != 0 || strings.Contains(errb.String(), "not verified") {
		t.Errorf("http: %d %q", code, errb.String())
	}
}
