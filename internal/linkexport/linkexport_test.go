package linkexport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// example loads the shared example configuration: a link ("site-b") with a BGP protocol that
// announces static routes (plan M4c, the "a link with static routes" scenario).
func example(t *testing.T) *model.Configuration {
	t.Helper()
	raw, err := os.ReadFile("../../api/examples/configuration.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func linkID(t *testing.T, cfg *model.Configuration) string {
	t.Helper()
	for id, n := range *cfg.Networks {
		if wg, err := n.AsWireGuardNetwork(); err == nil && wg.Kind == model.Link {
			return id
		}
	}
	t.Fatal("no link network in the example")
	return ""
}

// CC-03 test: a link's remote side needs its own WireGuard .conf (peer, endpoint, AllowedIPs) and,
// when a routing protocol runs on the link, a BIRD snippet for that remote side too.
func TestALinkWithStaticRoutesCarriesWhatTheRemoteSideNeeds(t *testing.T) {
	sec, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	norm, errs := domain.Normalize(example(t)) // a stored configuration names objects by UUID
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	provisioned, err := wireguard.Provision(norm, sec)
	if err != nil {
		t.Fatal(err)
	}
	id := linkID(t, provisioned)

	conf, err := wireguard.LinkRemoteConfig(wireguard.ExportInput{Config: provisioned, Secrets: sec, UplinkAddress: "203.0.113.1"}, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ListenPort = 51821\n", // the example's peer has a configured endpoint: the gateway dials in
		"AllowedIPs = 0.0.0.0/0\n",
		"[Peer]\nPublicKey = ",
	} {
		if !strings.Contains(conf.Conf, want) {
			t.Errorf("the remote .conf lacks %q:\n%s", want, conf.Conf)
		}
	}

	snippet, err := RemoteBird(provisioned, sec, id, "wg-gw")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"protocol bgp gateway", "protocol static announce", "router id"} {
		if !strings.Contains(snippet, want) {
			t.Errorf("the BIRD snippet lacks %q:\n%s", want, snippet)
		}
	}
	if err := birdParses(t, snippet); err != nil {
		t.Errorf("the remote BIRD snippet does not parse: %v\n%s", err, snippet)
	}
}

func TestRemoteBirdErrorsWithoutRouting(t *testing.T) {
	sec, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := example(t)
	cfg.Routing = nil
	norm, errs := domain.Normalize(cfg)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	provisioned, err := wireguard.Provision(norm, sec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RemoteBird(provisioned, sec, linkID(t, provisioned), "wg-gw"); err != ErrNoRouting {
		t.Errorf("got %v, want ErrNoRouting", err)
	}
}

// birdParses runs `bird -p` on the text: the real parser is the arbiter of the syntax (mirrors
// internal/bird's own helper of the same purpose).
func birdParses(t *testing.T, text string) error {
	t.Helper()
	bin, err := exec.LookPath("bird")
	if err != nil {
		t.Skip("bird is not installed")
	}
	f := filepath.Join(t.TempDir(), "x.conf")
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-p", "-c", f).CombinedOutput()
	if err != nil {
		return &parseError{string(out)}
	}
	return nil
}

type parseError struct{ out string }

func (e *parseError) Error() string { return strings.TrimSpace(e.out) }
