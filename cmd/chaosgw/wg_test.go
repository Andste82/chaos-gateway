package main

import (
	"archive/zip"
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	zqr "github.com/makiuchi-d/gozxing/qrcode"

	"github.com/Andste82/chaos-gateway/internal/secrets"
)

// wgConfigFile is the WireGuard testbed configuration plus a client rC whose key the gateway
// generates (the fixture's other clients bring their own public keys).
func wgConfigFile(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../internal/compiler/testdata/wireguard.yaml")
	if err != nil {
		t.Fatal(err)
	}
	anchor := []byte("      5d6e7f80-9a1b-4c2d-8e3f-4a5b6c7d8e9f:\n")
	extra := []byte("      6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f:\n        name: rC\n        address: 10.99.0.4\n        reachable: [{network: IoT}]\n        key: {mode: generated, export_once: true}\n")
	raw = bytes.Replace(raw, anchor, append(extra, anchor...), 1)
	p := filepath.Join(t.TempDir(), "wg.yaml")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// applied sets up a gateway with WireGuard through the store and returns the directories.
func applied(t *testing.T) (state, secretsDir string) {
	t.Helper()
	state, secretsDir = t.TempDir(), filepath.Join(t.TempDir(), "secrets")
	_, sock, _ := startExecutorWithKeys(t, secretsDir)
	code, out, errOut := runCmd("apply", "--file", wgConfigFile(t), "--socket", sock, "--executor-uid", uid(), "--state-dir", state, "--secrets-dir", secretsDir)
	if code != 0 || !strings.Contains(out, "is active") {
		t.Fatalf("code %d\n%s\n%s", code, out, errOut)
	}
	return state, secretsDir
}

func TestApplyFileWithWireGuardGeneratesKeysAndKeepsThemOutOfTheStore(t *testing.T) {
	state, secretsDir := applied(t)
	sec, _ := secrets.Open(secretsDir)
	ids, _ := sec.IDs()
	if len(ids) < 3 {
		t.Fatalf("keys %v", ids)
	}
	var secretsText []string
	for _, id := range ids {
		k, _ := sec.WireGuard(id)
		secretsText = append(secretsText, k.PrivateKey, k.PresharedKey)
	}
	// no secret anywhere in the state directory
	err := filepath.Walk(state, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		for _, s := range secretsText {
			if s != "" && bytes.Contains(b, []byte(s)) {
				t.Errorf("a secret is in %s", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyFileWithWireGuardNeedsASecretsDirectory(t *testing.T) {
	_, sock, _ := startExecutor(t)
	code, _, errOut := runCmd("apply", "--file", wgConfigFile(t), "--socket", sock, "--executor-uid", uid())
	if code != 1 || !strings.Contains(errOut, "--secrets-dir") {
		t.Fatalf("code %d: %s", code, errOut)
	}
}

func TestWGExportWritesTheClientConfigurationAndDeletesTheKeyOnce(t *testing.T) {
	state, secretsDir := applied(t)
	out := filepath.Join(t.TempDir(), "rC.conf")
	code, stdout, errOut := runCmd("wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--client", "rC", "--out", out, "--uplink-address", "203.0.113.1")
	if code != 0 || stdout != "" || !strings.Contains(errOut, "private keys") || !strings.Contains(errOut, "export once") {
		t.Fatalf("code %d\n%s\n%s", code, stdout, errOut)
	}
	conf, _ := os.ReadFile(out)
	if !strings.Contains(string(conf), "[Interface]") || !strings.Contains(string(conf), "Address = 10.99.0.4/32") || strings.Contains(string(conf), "private key of this peer") {
		t.Errorf("%s", conf)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
		t.Errorf("an export with a private key is written with mode %v", st.Mode())
	}
	// export once: the second export has a placeholder
	code, stdout, errOut = runCmd("wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--client", "rC", "--uplink-address", "203.0.113.1")
	if code != 0 || !strings.Contains(stdout, "private key of this peer") || strings.Contains(errOut, "private keys") {
		t.Errorf("code %d: %q %q", code, stdout, errOut)
	}
}

func TestWGExportKeepKeyDoesNotConsumeTheKey(t *testing.T) {
	state, secretsDir := applied(t)
	for i := 0; i < 2; i++ {
		code, stdout, _ := runCmd("wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--client", "rC", "--uplink-address", "203.0.113.1", "--keep-key")
		if code != 0 || strings.Contains(stdout, "private key of this peer") {
			t.Fatalf("export %d: %d %q", i, code, stdout)
		}
	}
}

func TestWGExportQRDecodesToTheFile(t *testing.T) {
	state, secretsDir := applied(t)
	args := []string{"wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--client", "rC", "--uplink-address", "203.0.113.1", "--keep-key"}
	_, conf, _ := runCmd(args...)
	out := filepath.Join(t.TempDir(), "rC.png")
	if code, _, errOut := runCmd(append(args, "--format", "png", "--out", out)...); code != 0 {
		t.Fatal(errOut)
	}
	f, _ := os.Open(out)
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	bmp, _ := gozxing.NewBinaryBitmapFromImage(img)
	res, err := zqr.NewQRCodeReader().Decode(bmp, nil)
	if err != nil || res.GetText() != conf {
		t.Fatalf("the QR code does not decode to the file: %v", err)
	}
	code, svg, _ := runCmd(append(args, "--format", "svg")...)
	if code != 0 || !strings.HasPrefix(svg, "<svg") {
		t.Errorf("svg: %d %.40s", code, svg)
	}
}

func TestWGExportAllAsZipAndTheLink(t *testing.T) {
	state, secretsDir := applied(t)
	out := filepath.Join(t.TempDir(), "all.zip")
	code, _, errOut := runCmd("wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--all", "--out", out, "--uplink-address", "203.0.113.1", "--keep-key")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "rA.conf,rB.conf,rC.conf" {
		t.Errorf("%v", names)
	}
	code, remote, errOut := runCmd("wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "site-b", "--link", "--uplink-address", "203.0.113.1")
	if code != 0 || !strings.Contains(remote, "Address = 10.255.0.1/31") || !strings.Contains(remote, "Table = off") {
		t.Errorf("%d\n%s\n%s", code, remote, errOut)
	}
}

func TestWGExportUsageAndErrors(t *testing.T) {
	state, secretsDir := applied(t)
	for name, c := range map[string]struct {
		args []string
		code int
		msg  string
	}{
		"no subcommand":      {[]string{"wg"}, 2, "usage"},
		"missing flags":      {[]string{"wg", "export"}, 2, "required"},
		"unknown network":    {[]string{"wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "nope", "--all"}, 1, "no WireGuard network"},
		"unknown client":     {[]string{"wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--client", "nope"}, 1, "no client"},
		"unknown format":     {[]string{"wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--client", "rA", "--uplink-address", "1.2.3.4", "--format", "pdf"}, 2, "unknown format"},
		"hub as link":        {[]string{"wg", "export", "--state-dir", state, "--secrets-dir", secretsDir, "--network", "lab-hub", "--link"}, 1, "not a link"},
		"no active revision": {[]string{"wg", "export", "--state-dir", t.TempDir(), "--secrets-dir", secretsDir, "--network", "lab-hub", "--all"}, 1, "no active revision"},
	} {
		code, _, errOut := runCmd(c.args...)
		if code != c.code || !strings.Contains(errOut, c.msg) {
			t.Errorf("%s: code %d, %q", name, code, errOut)
		}
	}
}
