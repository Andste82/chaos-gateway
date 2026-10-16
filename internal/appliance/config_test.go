package appliance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
)

// The configuration of the level 2 test is only applied in the nightly run: here it is checked with the
// gateway's own validation, so a forward the gateway would refuse (or the compiler leave out) fails at
// level 0 already. 443 is the API's default port, which a forward must not take.
func TestTheLevel2ConfigurationIsValidWithItsPortForwards(t *testing.T) {
	for _, two := range []bool{false, true} {
		cfg, err := domain.DecodeConfiguration([]byte(GatewayConfig(two)), domain.FormatYAML)
		if err != nil {
			t.Fatalf("two ports %v: %v", two, err)
		}
		if errs := domain.Validate(cfg, domain.WithUIPort(443)); len(errs) > 0 {
			t.Errorf("two ports %v: %v", two, errs)
		}
		want := map[bool]int{false: 2, true: 1}[two] // the management forward only with three ports
		if cfg.PortForwards == nil || len(*cfg.PortForwards) != want {
			t.Errorf("two ports %v: port forwards %v, want %d", two, cfg.PortForwards, want)
		}
	}
}

// The reason the two-port configuration has no management forward (plan M35a): it would be refused.
func TestTheTwoPortConfigurationWouldRefuseAManagementForward(t *testing.T) {
	three := GatewayConfig(false)
	mgmt := three[strings.Index(three, "  5e1f0c2a-7b3d-4e8f-9a1b-2c3d4e5f6a72:"):]
	// the decoder already validates; what is refused there is the same as Validate's
	var errs domain.ValidationErrors
	cfg, err := domain.DecodeConfiguration([]byte(GatewayConfig(true)+mgmt), domain.FormatYAML)
	if err == nil {
		errs = domain.Validate(cfg, domain.WithUIPort(443))
	} else if !errors.As(err, &errs) {
		t.Fatal(err)
	}
	if len(errs) != 1 || errs[0].Code != "management_is_uplink" || !strings.HasSuffix(errs[0].Path, "/entry") {
		t.Fatalf("got %v, want management_is_uplink at the forward's entry", errs)
	}
}

func TestTheEchoHelperAnswersWithItsTagAndThePeer(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal("the go tool is not in PATH: the level 2 test builds its helper with it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	bin, err := BuildEcho(ctx, t.TempDir(), runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	cmd := exec.CommandContext(ctx, bin, "-tag", "device", "-listen", addr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var body string
	for i := 0; ; i++ {
		resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			body = string(b)
			break
		}
		if i == 50 {
			t.Fatalf("the helper does not answer: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if body != "device client=127.0.0.1\n" {
		t.Errorf("the helper answered %q", body)
	}
}
