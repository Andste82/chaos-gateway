package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// M29: secrets stay out of what the gateway sends and writes (plan §2.16): "excluded from logs, events, API responses and
// exports by default". The test collects every secret value the gateway holds (the WireGuard keys, the key of an installed
// HTTPS certificate, the keys of the test CAs, the admin password and its hash, the values of the tokens and the setup token),
// drives the gateway through the operations that handle them, and looks for the values in every read of the API, in the
// events, in the audit log and in the log of the server. The downloads that are secret by design are the positive control:
// the same search finds the key in them.

// pemBodyLine is a line from the middle of the base64 body of a PEM block: enough to recognize the key wherever it is
// repeated (a JSON string holds the line breaks as \n, so the whole block is not a substring of the response).
func pemBodyLine(t *testing.T, pemText string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(pemText), "\n")
	if len(lines) < 3 {
		t.Fatalf("no body in %q", pemText)
	}
	return strings.TrimSpace(lines[1])
}

// theSecretsOf lists what must not leave the gateway, by what it is.
func theSecretsOf(t *testing.T, g *gw, extra map[string]string) map[string]string {
	t.Helper()
	out := map[string]string{"the admin password": adminPassword, "the setup token": g.setup}
	for k, v := range extra {
		out[k] = v
	}
	ids, err := g.sec.IDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Fatal("the fixture has no WireGuard keys")
	}
	for _, id := range ids {
		k, err := g.sec.WireGuard(id)
		if err != nil {
			t.Fatal(err)
		}
		if k.PrivateKey != "" {
			out["the WireGuard private key of "+id] = k.PrivateKey
		}
		if k.PresharedKey != "" {
			out["the WireGuard preshared key of "+id] = k.PresharedKey
		}
	}
	raw, err := os.ReadFile(filepath.Join(g.authPath, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		PasswordHash string `json:"password_hash"`
		Tokens       []struct {
			Hash string `json:"hash"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.PasswordHash == "" {
		t.Fatalf("auth.json: %v", err)
	}
	out["the hash of the admin password"] = st.PasswordHash
	for i, tk := range st.Tokens {
		out["the hash of token "+string(rune('a'+i))] = tk.Hash
	}
	keys, _ := filepath.Glob(filepath.Join(g.root, "tlsca", "*.key"))
	if len(keys) == 0 {
		t.Fatal("no key of a test CA")
	}
	for _, f := range keys {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out["the key "+filepath.Base(f)] = pemBodyLine(t, string(b))
	}
	b, err := os.ReadFile(filepath.Join(g.root, "secrets", "tls", "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	out["the key of the gateway's own certificate"] = pemBodyLine(t, string(b))
	return out
}

var keyValue = regexp.MustCompile(`preshared_key"?:\s*"?[A-Za-z0-9+/]{40,}`)

// unzipped is the text of every file of a zip, for the downloads that are one.
func unzipped(t *testing.T, raw []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		all.Write(b)
	}
	return all.String()
}

func assertNone(t *testing.T, what, text string, secretValues map[string]string) {
	t.Helper()
	for name, v := range secretValues {
		if v != "" && strings.Contains(text, v) {
			t.Errorf("%s holds %s", what, name)
		}
	}
}

func TestNoSecretIsInAResponseAnEventAnAuditEntryOrALog(t *testing.T) {
	logs := &ctlBuf{}
	g := newGW(t, func(o *options) {
		o.log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	g.finishSetup()
	full := g.token
	s := g.openStream("", "")
	in := newCert(t, certSpec{})
	if r := put(g, in); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	readTok, ovTok := g.mustTokenOf("read"), g.mustTokenOf("overlays")
	g.token = full
	// a token created over the API: its value is in the answer to the creation and nowhere else
	created := g.do("POST", "/auth/tokens", map[string]any{"name": "ci", "scope": "overlays"}, nil, nil)
	if created.Status != 201 {
		t.Fatalf("%d %s", created.Status, created.Body)
	}
	apiTok := created.json(t)["token"].(string)

	// the service token of the DNS proxy, the responder and Kea's hook: a file outside the secrets directory by design
	// (the services mount it and read it as a group), so its value is searched everywhere else
	serviceFile := filepath.Join(g.root, "service", "token")
	if err := g.au.EnsureServiceToken(serviceFile); err != nil {
		t.Fatal(err)
	}
	rawService, err := os.ReadFile(serviceFile)
	if err != nil {
		t.Fatal(err)
	}
	serviceToken := strings.TrimSpace(string(rawService))

	secretValues := theSecretsOf(t, g, map[string]string{
		"the key of the installed certificate": pemBodyLine(t, in.keyPEM),
		"a full token":                         full, "a read token": readTok, "an overlays token": ovTok, "the created token": apiTok,
		"the service token": serviceToken})

	// wrong and right logins, a password change that fails, a token that is wrong: the paths where a secret could be echoed
	g.token = ""
	g.do("POST", "/auth/login", map[string]any{"password": "a wrong password"}, nil, nil)
	g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil)
	g.token = "cgw_" + strings.Repeat("x", 40)
	g.do("GET", "/state", nil, nil, nil)
	g.token = full
	g.do("POST", "/auth/password", map[string]any{"current": "a wrong password", "new": "another long password"}, nil, nil)
	// an apply and an overlay put events on the stream
	if r := g.apply(g.mustPatch(map[string]any{"management": map[string]any{"ui_port": 9443}})); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r := g.createOverlay(`{"target":{"network":"IoT"},"profile":"lte","ttl":"5m"}`); r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// the downloads that are secret on purpose, each audited
	for _, p := range []string{"/revisions/1/export?include_secrets=true&format=json", "/networks/" + hubID + "/clients/" + rAID + "/export", "/networks/" + hubID + "/export"} {
		r := g.do("GET", p, nil, nil, nil)
		if r.Status != 200 {
			t.Fatalf("%s: %d %s", p, r.Status, r.Body)
		}
		body := string(r.Body)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/zip") {
			body = unzipped(t, r.Body)
		}
		hits := 0
		for name, v := range secretValues {
			if strings.Contains(name, "WireGuard") && strings.Contains(body, v) {
				hits++
			}
		}
		if hits == 0 {
			t.Errorf("the positive control failed: %s holds no WireGuard key, the search below would find nothing anywhere", p)
		}
		assertNone(t, p, string(r.Body), map[string]string{"the admin password": adminPassword, "the hash": secretValues["the hash of the admin password"], "a full token": full})
	}

	// every read the full scope may make, with the ids of the fixture
	paths := []string{"/state", "/state/linux", "/capabilities", "/system/info", "/system/preflight", "/system/interfaces", "/system/busy", "/system/health",
		"/system/certificate", "/auth/session", "/auth/tokens", "/revisions", "/revisions/active", "/revisions/1", "/revisions/1/diff?base=1", "/revisions/1/export",
		"/revisions/1/export?format=json", "/uplink", "/uplink/traffic", "/networks", "/networks/" + hubID, "/networks/" + hubID + "/clients",
		"/networks/" + hubID + "/clients/" + rAID, "/networks/" + hubID + "/leases", "/routing", "/routing/status", "/routing/routes", "/devices", "/flows", "/groups",
		"/rules", "/port-forwards", "/faults", "/profiles", "/scenarios", "/probes", "/overlays", "/runs", "/captures", "/captures/storage", "/tls/ca",
		"/dns/queries", "/intercept/flows", "/audit?limit=500", "/metrics", "/explain?src=10.10.0.5&dst=203.0.113.9"}
	g.badRequest = true // a few of the reads answer a 4xx for the fixture; the body is searched all the same
	for _, scope := range []string{"full", "read"} {
		if scope == "read" {
			g.token = readTok
		}
		for _, p := range paths {
			if scope == "read" && p == "/auth/tokens" {
				continue
			}
			r := g.do("GET", p, nil, nil, nil)
			if r.Status >= 500 {
				t.Errorf("GET %s: %d", p, r.Status)
			}
			assertNone(t, "GET "+p+" ("+scope+")", string(r.Body), secretValues)
		}
	}
	g.badRequest = false
	g.token = full

	// the events of the whole test (the stream was opened before the first operation)
	var seen strings.Builder
	for {
		ev, ok := s.next(300 * time.Millisecond)
		if !ok {
			break
		}
		raw, _ := json.Marshal(ev.Data)
		seen.Write(raw)
		seen.WriteString("\n")
	}
	if seen.Len() == 0 {
		t.Error("no event was replayed: the search of the events looked at nothing")
	}
	assertNone(t, "the events", seen.String(), secretValues)

	// the audit log as the API lists it, the files of the state on disk, and the log of the server
	entries, _, err := g.log.List(auditFilter(), "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 5 {
		t.Errorf("only %d audit entries", len(entries))
	}
	b, _ := json.Marshal(entries)
	assertNone(t, "the audit log", string(b), secretValues)
	assertNone(t, "the log of the server", logs.String(), secretValues)
	var failedLogins int
	for _, e := range entries {
		if e.Action == "auth.login_failed" {
			failedLogins++
		}
	}
	if failedLogins != 1 {
		t.Errorf("%d audit entries of the failed login, want 1: %s", failedLogins, b)
	}
	assertNoneOnDisk(t, g.root, secretValues)
}

// assertNoneOnDisk searches every file below root for the secret values, except the two directories that are meant to hold
// them (the secrets directory and the test CAs' keys, whose modes TestEverySecretOnDiskIsPrivateToItsOwner checks) and the
// service token's own file (service/token, the one place the services read it from): the
// revisions, the audit log and every other file of the state hold none of them. A search that reads too few files fails.
func assertNoneOnDisk(t *testing.T, root string, secretValues map[string]string) {
	t.Helper()
	var files int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (path == filepath.Join(root, "secrets") || path == filepath.Join(root, "tlsca")) {
			return fs.SkipDir
		}
		if !d.Type().IsRegular() || path == filepath.Join(root, "service", "token") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		rel, _ := filepath.Rel(root, path)
		assertNone(t, "the file "+rel, string(b), secretValues)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 2 {
		t.Errorf("only %d files outside the secrets directories: the search of the disk looked at nothing", files)
	}
}

// A revision made by the API never stores a secret: the export without include_secrets and the revision itself hold no key,
// and the candidate made from an export with them keeps them out of the revision.
func TestAnExportWithoutSecretsHoldsNoKeyInAnyFormat(t *testing.T) {
	g := ready(t)
	secretValues := theSecretsOf(t, g, nil)
	for _, p := range []string{"/revisions/1/export", "/revisions/1/export?format=json", "/revisions/1/export?include_secrets=false", "/revisions/1"} {
		r := g.do("GET", p, nil, nil, nil)
		if r.Status != 200 {
			t.Fatalf("%s: %d", p, r.Status)
		}
		assertNone(t, p, string(r.Body), secretValues)
		for _, w := range []string{"private_key", `"secrets"`, "secrets:"} {
			if strings.Contains(string(r.Body), w) {
				t.Errorf("%s mentions %s", p, w)
			}
		}
		// `preshared_key: true` is a setting of a client; a key would be a long base64 string
		if keyValue.Match(r.Body) {
			t.Errorf("%s holds a preshared key", p)
		}
	}
	// the answers of the keys' own operations tell that a key exists, never what it is
	r := g.do("GET", "/networks/"+hubID+"/clients/"+rAID, nil, nil, nil)
	assertNone(t, "the client", string(r.Body), secretValues)
}
