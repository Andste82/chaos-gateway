package ctl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The CLI's own tests: the parts that do not need a gateway (the address, the certificate checks, the flags that
// become a fault, the documents), and the commands against a fake gateway that can fail in ways the real one
// does not on request. The commands against the real API are in internal/api/ctl_test.go.

func TestTheAddressOfTheGatewayIsNormalized(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "https://127.0.0.1:8443/api/v1",
		"gw.lab:8443":                  "https://gw.lab:8443/api/v1",
		"https://gw.lab:8443":          "https://gw.lab:8443/api/v1",
		"https://gw.lab:8443/":         "https://gw.lab:8443/api/v1",
		"http://127.0.0.1:9000/api/v1": "http://127.0.0.1:9000/api/v1",
		"https://gw.lab/api/v1/":       "https://gw.lab/api/v1",
	} {
		got, err := apiBase(in)
		if err != nil || got != want {
			t.Errorf("apiBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://gw", "https://gw/other", "https://", "://x"} {
		if got, err := apiBase(bad); err == nil {
			t.Errorf("apiBase(%q) = %q, want an error", bad, got)
		}
	}
}

func TestAFingerprintIsReadInTheUsualForms(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	h := hex.EncodeToString(sum[:])
	colons := strings.ToUpper(strings.Join(splitEvery(h, 2), ":"))
	for _, in := range []string{h, "sha256:" + h, colons, " " + h + " "} {
		got, err := parseFingerprint(in)
		if err != nil || got != sum {
			t.Errorf("%q: %v", in, err)
		}
	}
	for _, bad := range []string{"", "abc", h[:62], h + "00", strings.Repeat("zz", 32)} {
		if _, err := parseFingerprint(bad); err == nil {
			t.Errorf("%q is not a fingerprint", bad)
		}
	}
}

func splitEvery(s string, n int) []string {
	var out []string
	for len(s) > 0 {
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

// A gateway with a self-signed certificate is trusted by its fingerprint, by the certificate itself or by
// --insecure, and by nothing else.
func TestTheCertificateOfTheGatewayIsCheckedByFingerprintByCAOrNotAtAll(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
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
		args []string
		code int
		want string
	}{
		"nothing":                  {nil, 2, "certificate"},
		"the fingerprint":          {[]string{"--fingerprint", fp}, 0, "removed 2 overlays"},
		"another fingerprint":      {[]string{"--fingerprint", wrong}, 2, "pinned"},
		"a malformed one":          {[]string{"--fingerprint", "xyz"}, 2, "fingerprint"},
		"the certificate":          {[]string{"--ca-cert", caFile}, 0, "removed 2 overlays"},
		"a file without PEM":       {[]string{"--ca-cert", badFile}, 2, "PEM"},
		"a missing file":           {[]string{"--ca-cert", filepath.Join(t.TempDir(), "none")}, 2, "ca-cert"},
		"insecure":                 {[]string{"--insecure"}, 0, "removed 2 overlays"},
		"the wrong token":          {[]string{"--insecure", "--token", "other"}, 2, "401"},
		"the environment":          {[]string{"--url", srv.URL}, 0, "removed 2 overlays"},
		"a fingerprint beats a CA": {[]string{"--fingerprint", wrong, "--ca-cert", caFile}, 2, "pinned"},
	} {
		var out, errb bytes.Buffer
		env := Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string {
			return map[string]string{"CHAOSGW_URL": srv.URL, "CHAOSGW_TOKEN": "secret"}[k]
		}}
		args := append(append([]string{}, tc.args...), "reset")
		if name == "the environment" {
			env.Getenv = func(k string) string {
				return map[string]string{"CHAOSGW_TOKEN": "secret", "CHAOSGW_FINGERPRINT": fp}[k]
			}
		}
		code := Run(args, env)
		if code != tc.code || !strings.Contains(out.String()+errb.String(), tc.want) {
			t.Errorf("%s: code %d, stdout %q, stderr %q", name, code, out.String(), errb.String())
		}
	}

	// the environment can say the same as the flags
	for name, vars := range map[string]map[string]string{
		"CHAOSGW_INSECURE":   {"CHAOSGW_INSECURE": "1"},
		"CHAOSGW_CA_CERT":    {"CHAOSGW_CA_CERT": caFile},
		"CHAOSGW_TOKEN_FILE": {"CHAOSGW_TOKEN_FILE": writeTemp(t, "secret\n"), "CHAOSGW_TOKEN": "", "CHAOSGW_FINGERPRINT": fp},
	} {
		vars["CHAOSGW_URL"] = srv.URL
		if _, ok := vars["CHAOSGW_TOKEN"]; !ok {
			vars["CHAOSGW_TOKEN"] = "secret"
		}
		var out, errb bytes.Buffer
		if code := Run([]string{"reset"}, Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return vars[k] }}); code != 0 {
			t.Errorf("%s: code %d, stderr %q", name, code, errb.String())
		}
	}
}

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTheFlagsOfAFaultBecomeItsBody(t *testing.T) {
	read := func(string) (any, error) { return nil, fmt.Errorf("no files here") }
	body := func(f faultFlags) map[string]any {
		t.Helper()
		b, err := f.faultBody(read)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	got := body(faultFlags{latency: "200ms", jitter: "50ms", loss: "10%", rate: "2Mbit", corrupt: "1%", dup: "2%", reo: "5%", flap: "20s/10s", protocol: "tcp", ports: []int{8883, 1883}, to: "198.51.100.0/24"})
	want := map[string]any{"latency": "200ms", "jitter": "50ms", "loss": "10%", "rate": "2Mbit", "corrupt": "1%", "duplicate": "2%", "reorder": "5%",
		"flapping": map[string]any{"up": "20s", "down": "10s"}, "protocol": "tcp", "ports": []int{8883, 1883}, "destination": map[string]any{"cidr": "198.51.100.0/24"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if got := body(faultFlags{blackout: true}); !reflect.DeepEqual(got, map[string]any{"blackout": true}) {
		t.Errorf("%v", got)
	}
	for in, want := range map[string]map[string]any{
		"uplink":      {"uplink": true},
		"203.0.113.9": {"cidr": "203.0.113.9/32"},
		"10.0.0.0/8":  {"cidr": "10.0.0.0/8"},
		"IoT":         {"network": "IoT"},
		"lab network": {"network": "lab network"},
	} {
		if got := destinationOf(in); !reflect.DeepEqual(got, want) {
			t.Errorf("destinationOf(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []faultFlags{{}, {flap: "20s"}, {flap: "/10s"}} {
		if _, err := bad.faultBody(read); err == nil {
			t.Errorf("%+v is not a fault", bad)
		}
	}
	// --body is the start and the flags change it
	doc := `{"latency": "100ms", "loss": "1%", "protocol": "udp"}`
	b, err := (&faultFlags{body: doc, loss: "5%"}).faultBody(func(s string) (any, error) { return decodeYAML([]byte(s)) })
	if err != nil || b["latency"] != "100ms" || b["loss"] != "5%" || b["protocol"] != "udp" {
		t.Errorf("%v %v", b, err)
	}
}

func TestDocumentsAreReadAsYAMLOrJSON(t *testing.T) {
	v, err := decodeYAML([]byte("name: x\ntarget: {device: d}\nsteps:\n  - {id: a, at: 0s, rule: {ports: [8883, 1883], cut_existing: true}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	if string(raw) != `{"name":"x","steps":[{"at":"0s","id":"a","rule":{"cut_existing":true,"ports":[8883,1883]}}],"target":{"device":"d"}}` {
		t.Errorf("%s", raw)
	}
	if v, err = decodeYAML([]byte(`{"a": [1, {"b": null}]}`)); err != nil {
		t.Fatal(err)
	} else if raw, _ = json.Marshal(v); string(raw) != `{"a":[1,{"b":null}]}` {
		t.Errorf("%s", raw)
	}
	for _, bad := range []string{"", "a: [", "\t- x"} {
		if _, err := decodeYAML([]byte(bad)); err == nil {
			t.Errorf("%q should not decode", bad)
		}
	}
	// a map with keys that are not strings becomes one that JSON can carry
	m := jsonable(map[any]any{1: "a", "b": []any{map[any]any{true: 2}}})
	if raw, err = json.Marshal(m); err != nil || string(raw) != `{"1":"a","b":[{"true":2}]}` {
		t.Errorf("%s %v", raw, err)
	}
}

func TestSetArgumentsAreKeyValuePairs(t *testing.T) {
	got, err := parseSets([]string{"target.device=esp32-42", "steps.slow.fault.latency=5s", "a=b=c", "empty="})
	if err != nil || !reflect.DeepEqual(got, map[string]string{"target.device": "esp32-42", "steps.slow.fault.latency": "5s", "a": "b=c", "empty": ""}) {
		t.Errorf("%v %v", got, err)
	}
	for _, bad := range []string{"novalue", "=x", ""} {
		if _, err := parseSets([]string{bad}); err == nil {
			t.Errorf("%q", bad)
		}
	}
}

func TestAConnectionIsGivenAsProtocolAndPort(t *testing.T) {
	c, err := parseConnSpec("TCP/8883")
	if err != nil || c.proto != "tcp" || c.port != 8883 || c.addr.IsValid() || c.String() != "tcp/8883" {
		t.Errorf("%+v %v", c, err)
	}
	c, err = parseConnSpec("udp/10.0.0.5:5683")
	if err != nil || c.proto != "udp" || c.port != 5683 || c.addr != netip.MustParseAddr("10.0.0.5") || c.String() != "udp/10.0.0.5:5683" {
		t.Errorf("%+v %v", c, err)
	}
	for _, bad := range []string{"", "8883", "tcp", "tcp/", "tcp/0", "tcp/65536", "icmp/1", "tcp/host:8883", "tcp/1.2.3.4:x"} {
		if _, err := parseConnSpec(bad); err == nil {
			t.Errorf("%q", bad)
		}
	}
}

func TestOnlyAnEstablishedConnectionMatches(t *testing.T) {
	flow := func(proto model.FlowProtocol, dst string, dport int, state string, down int64) model.Flow {
		f := model.Flow{Protocol: proto, Dst: dst, Dport: &dport, State: &state}
		f.Download = &struct {
			Bytes   *int64 `json:"bytes,omitempty"`
			Packets *int64 `json:"packets,omitempty"`
		}{Packets: &down}
		return f
	}
	tcp8883 := connSpec{proto: "tcp", port: 8883}
	for state, want := range map[string]bool{"ESTABLISHED": true, "established": true, "TIME_WAIT": false, "CLOSE_WAIT": false, "SYN_SENT": false, "SYN_RECV": false, "": false, "NONE": false} {
		if got := tcp8883.matches(flow(model.FlowProtocolTcp, "203.0.113.10", 8883, state, 5)); got != want {
			t.Errorf("tcp %q: %v", state, got)
		}
	}
	if tcp8883.matches(flow(model.FlowProtocolTcp, "203.0.113.10", 1883, "ESTABLISHED", 5)) || tcp8883.matches(flow(model.FlowProtocolUdp, "203.0.113.10", 8883, "ASSURED", 5)) {
		t.Error("another port or protocol matched")
	}
	udp := connSpec{proto: "udp", port: 5683}
	if !udp.matches(flow(model.FlowProtocolUdp, "10.0.0.5", 5683, "ASSURED", 3)) || udp.matches(flow(model.FlowProtocolUdp, "10.0.0.5", 5683, "UNREPLIED", 0)) || udp.matches(flow(model.FlowProtocolUdp, "10.0.0.5", 5683, "ASSURED", 0)) {
		t.Error("a UDP flow counts when it has been answered")
	}
	peer := connSpec{proto: "tcp", port: 8883, addr: netip.MustParseAddr("203.0.113.10")}
	if !peer.matches(flow(model.FlowProtocolTcp, "203.0.113.10", 8883, "ESTABLISHED", 1)) || peer.matches(flow(model.FlowProtocolTcp, "203.0.113.11", 8883, "ESTABLISHED", 1)) {
		t.Error("the address of the peer")
	}
}

func TestTheExitCodeOfARunIsItsStatus(t *testing.T) {
	for st, want := range map[model.RunStatus]int{model.RunStatusPassed: 0, model.RunStatusFailed: 1, model.RunStatusError: 2, model.RunStatusAborted: 2} {
		if got := runExit(st); got != want {
			t.Errorf("%s: %d", st, got)
		}
		if !ended(st) {
			t.Errorf("%s has ended", st)
		}
	}
	for _, st := range []model.RunStatus{model.RunStatusQueued, model.RunStatusRunning} {
		if ended(st) {
			t.Errorf("%s has not ended", st)
		}
	}
}

// fakeGateway answers like the parts of the API the commands use, and fails on request.
type fakeGateway struct {
	t  *testing.T
	mu sync.Mutex
	// requests, as "METHOD path"
	log []string
	// deleteStatus, when not 0, is the status of DELETE /overlays/{id}
	deleteStatus int
	// createStatus, when not 0, fails the n-th POST /overlays (1-based)
	failCreate int
	creates    int
	// runDown makes GET /runs/{id} fail from the n-th call on (1: from the first)
	runDown int
	gets    int
	renews  int
}

func (f *fakeGateway) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.log = append(f.log, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api/v1"))
		w.Header().Set("Content-Type", "application/json")
		problem := func(status int, code, detail string) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://chaos-gateway.dev/problems/" + code, "title": code, "status": status, "code": code, "detail": detail})
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		switch {
		case r.Method == http.MethodPost && path == "/overlays":
			f.creates++
			if f.failCreate == f.creates {
				problem(422, "validation_failed", "refused on purpose")
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":"00000000-0000-4000-8000-00000000000%d","kind":"fault","owner":{"type":"token","id":"t"},"created_at":"2026-10-18T12:00:00Z","updated_at":"2026-10-18T12:00:00Z","generation":1}`, f.creates)
		case r.Method == http.MethodDelete && strings.HasPrefix(path, "/overlays/"):
			if f.deleteStatus != 0 {
				problem(f.deleteStatus, "internal", "cannot delete")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/renew"):
			f.renews++
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/runs/"):
			f.gets++
			if f.runDown > 0 && f.gets >= f.runDown {
				problem(503, "unavailable", "the executor is down")
				return
			}
			_, _ = w.Write([]byte(`{"id":"00000000-0000-4000-8000-000000000001","scenario":{"name":"x","inline":true},"revision":1,"target":{"device":"d"},"owner":{"type":"token","id":"t"},"status":"running","created_at":"2026-10-18T12:00:00Z"}`))
		default:
			problem(404, "not_found", "no "+path)
		}
	})
}

func (f *fakeGateway) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func (f *fakeGateway) run(t *testing.T, sigs <-chan os.Signal, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	var out, errb bytes.Buffer
	code := Run(args, Env{Stdout: &out, Stderr: &errb, Signals: sigs, Poll: time.Millisecond,
		Getenv: func(k string) string { return map[string]string{"CHAOSGW_URL": srv.URL, "CHAOSGW_TOKEN": "t"}[k] }})
	return code, out.String(), errb.String()
}

// A gateway that cannot be asked while a run is waited for ends the wait with 2 after a number of tries.
func TestWaitingForARunGivesUpOnAGatewayThatDoesNotAnswer(t *testing.T) {
	f := &fakeGateway{t: t, runDown: 1}
	code, _, errb := f.run(t, nil, "wait", "run", "00000000-0000-4000-8000-000000000001")
	if code != 2 || !strings.Contains(errb, "unavailable") {
		t.Errorf("a first look that fails: %d %s", code, errb)
	}
	// the gateway goes away after the first look: ten tries, then the end
	f = &fakeGateway{t: t, runDown: 2}
	code, _, errb = f.run(t, nil, "wait", "run", "00000000-0000-4000-8000-000000000001")
	if code != 2 || !strings.Contains(errb, "lost the gateway") || strings.Count(errb, "does not answer") != pollFailures {
		t.Errorf("%d %s", code, errb)
	}
}

// The overlay of `with` that the gateway cannot remove after the command succeeded is a failure of chaosctl (125);
// after a command that failed, the command's code stays.
func TestWithReportsAnOverlayItCannotRemove(t *testing.T) {
	f := &fakeGateway{t: t, deleteStatus: 500}
	code, _, errb := f.run(t, nil, "with", "--device", "d", "--blackout", "--", "true")
	if code != ExitInternal || !strings.Contains(errb, "could not remove") {
		t.Errorf("%d %s", code, errb)
	}
	f = &fakeGateway{t: t, deleteStatus: 500}
	if code, _, errb = f.run(t, nil, "with", "--device", "d", "--blackout", "--", "sh", "-c", "exit 4"); code != 4 || !strings.Contains(errb, "could not remove") {
		t.Errorf("%d %s", code, errb)
	}
}

// If the second overlay is refused, the first is removed and the command does not run.
func TestWithRemovesTheFirstOverlayWhenTheSecondIsRefused(t *testing.T) {
	f := &fakeGateway{t: t, failCreate: 2}
	marker := filepath.Join(t.TempDir(), "ran")
	code, _, errb := f.run(t, nil, "with", "--device", "d", "--profile", "bad-lte", "--blackout", "--", "touch", marker)
	if code != ExitInternal || !strings.Contains(errb, "refused on purpose") {
		t.Fatalf("%d %s", code, errb)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command ran")
	}
	calls := strings.Join(f.calls(), ",")
	if !strings.Contains(calls, "POST /overlays,POST /overlays,DELETE /overlays/00000000-0000-4000-8000-000000000001") {
		t.Errorf("the calls: %s", calls)
	}
}

// The renewals come while the command runs, at a third of the lease.
func TestWithRenewsAtAThirdOfTheLease(t *testing.T) {
	f := &fakeGateway{t: t}
	code, _, errb := f.run(t, nil, "with", "--device", "d", "--blackout", "--lease", "1500ms", "--", "sleep", "1.2")
	if code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	f.mu.Lock()
	n := f.renews
	f.mu.Unlock()
	if n < 2 || n > 3 {
		t.Errorf("%d renewals in 1.2 s with a lease of 1.5 s (every 0.5 s)", n)
	}
	// no renewals without a lease
	f = &fakeGateway{t: t}
	if code, _, errb = f.run(t, nil, "with", "--device", "d", "--blackout", "--lease", "0", "--", "sleep", "0.3"); code != 0 || f.renews != 0 {
		t.Errorf("%d %d %s", code, f.renews, errb)
	}
}

// A second signal ends a command that ignores the first.
func TestASecondSignalKillsACommandThatIgnoresTheFirst(t *testing.T) {
	f := &fakeGateway{t: t}
	sigs := make(chan os.Signal, 2)
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		sigs <- syscall.SIGTERM
		time.Sleep(200 * time.Millisecond)
		sigs <- syscall.SIGTERM
	}()
	start := time.Now()
	code, _, _ := f.run(t, sigs, "with", "--device", "d", "--blackout", "--", "sh", "-c", `trap '' TERM; touch `+started+`; while true; do sleep 0.05; done`)
	if code != 128+9 || time.Since(start) > 10*time.Second {
		t.Errorf("code %d after %v", code, time.Since(start))
	}
	if last := f.calls()[len(f.calls())-1]; !strings.HasPrefix(last, "DELETE /overlays/") {
		t.Errorf("the overlay is removed last: %v", f.calls())
	}
}

// An interrupt is the terminal's Ctrl-C and reaches the command by itself: chaosctl does not pass it on, and waits for
// the command to end.
func TestAnInterruptIsNotPassedOnToTheCommand(t *testing.T) {
	f := &fakeGateway{t: t}
	sigs := make(chan os.Signal, 2)
	started := filepath.Join(t.TempDir(), "started")
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		sigs <- os.Interrupt
	}()
	code, _, _ := f.run(t, sigs, "with", "--device", "d", "--blackout", "--", "sh", "-c", "trap 'exit 9' INT; touch "+started+"; sleep 0.6; exit 0")
	if code != 0 {
		t.Errorf("the command was interrupted: %d", code)
	}
	if last := f.calls()[len(f.calls())-1]; !strings.HasPrefix(last, "DELETE /overlays/") {
		t.Errorf("the overlay is removed after the command ended: %v", f.calls())
	}
}
