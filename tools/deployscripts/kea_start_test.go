package deployscripts

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// keaStart runs deploy/kea-start.sh with its paths moved into a temporary directory (the script has fixed paths in a container) and stubs for
// the two programs it starts. chaosgw kea-config prints its configuration to the standard output; with failingConfig it prints the
// first half and dies, as a kill during the write does. kea-dhcp4 records what it found: the socket, the configuration.
type keaStart struct {
	t               *testing.T
	root, run, data string
}

func newKeaStart(t *testing.T) *keaStart {
	t.Helper()
	k := &keaStart{t: t, root: t.TempDir()}
	k.run, k.data = filepath.Join(k.root, "run"), filepath.Join(k.root, "data")
	for _, d := range []string{k.run, k.data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return k
}

func (k *keaStart) start(failingConfig bool) (string, error) {
	k.t.Helper()
	script, err := os.ReadFile("../../deploy/kea-start.sh")
	if err != nil {
		k.t.Fatal(err)
	}
	chaosgw := "#!/bin/sh\nprintf '{\"Dhcp4\": {}}'\n"
	if failingConfig {
		chaosgw = "#!/bin/sh\nprintf '{\"Dhcp4\": '\nkill -9 $$\n"
	}
	kea := "#!/bin/sh\n{ if [ -S '" + filepath.Join(k.run, "kea4-ctrl.sock") + "' ]; then echo socket; else echo no-socket; fi; cat \"$2\"; } >'" + filepath.Join(k.root, "kea.out") + "'\n"
	for name, content := range map[string]string{"chaosgw": chaosgw, "kea-dhcp4": kea} {
		if err := os.WriteFile(filepath.Join(k.root, name), []byte(content), 0o755); err != nil {
			k.t.Fatal(err)
		}
	}
	text := string(script)
	for from, to := range map[string]string{
		"/usr/local/bin/chaosgw": filepath.Join(k.root, "chaosgw"),
		"/usr/sbin/kea-dhcp4":    filepath.Join(k.root, "kea-dhcp4"),
		"/run/kea":               k.run,
		"/var/lib/kea":           k.data,
	} {
		text = strings.ReplaceAll(text, from, to)
	}
	file := filepath.Join(k.root, "kea-start.sh")
	if err := os.WriteFile(file, []byte(text), 0o755); err != nil {
		k.t.Fatal(err)
	}
	cmd := exec.Command("sh", file)
	cmd.Env = append(os.Environ(), "CHAOSGW_API_GID="+strconv.Itoa(os.Getgid()))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (k *keaStart) keaSaw() string {
	raw, _ := os.ReadFile(filepath.Join(k.root, "kea.out"))
	return string(raw)
}

// The control socket of an earlier run is in the volume across a recreated container: the health check (test -S) passes on it before
// this Kea has made its own, so it is gone when Kea starts.
func TestKeaStartRemovesTheStaleControlSocketBeforeKeaStarts(t *testing.T) {
	k := newKeaStart(t)
	sock := filepath.Join(k.run, "kea4-ctrl.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(sock); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the stale socket was not made: %v %v", fi, err)
	}
	if out, err := k.start(false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := k.keaSaw(); !strings.HasPrefix(got, "no-socket\n") {
		t.Errorf("Kea started with the socket of the earlier run in place: %q", got)
	}
}

// A kill while the first configuration is written must not leave a partial file that the next start takes for a finished one.
func TestKeaStartWritesTheFirstConfigurationInOneStep(t *testing.T) {
	k := newKeaStart(t)
	conf := filepath.Join(k.data, "kea-dhcp4.conf")
	if _, err := k.start(true); err == nil {
		t.Fatal("the start went on after the configuration could not be made")
	}
	if raw, err := os.ReadFile(conf); err == nil && len(raw) > 0 {
		t.Fatalf("a partial configuration is where the next start takes it for a finished one: %q", raw)
	}
	if k.keaSaw() != "" {
		t.Errorf("Kea started: %q", k.keaSaw())
	}
	// the next start makes it, over the leftover of the killed write
	if out, err := k.start(false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := k.keaSaw(); got != "no-socket\n{\"Dhcp4\": {}}" {
		t.Errorf("Kea saw %q", got)
	}
	// a configuration that is there (the API wrote scopes into it) is not replaced
	if err := os.WriteFile(conf, []byte("{\"Dhcp4\": {\"x\": 1}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := k.start(true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := k.keaSaw(); got != "no-socket\n{\"Dhcp4\": {\"x\": 1}}" {
		t.Errorf("Kea saw %q", got)
	}
}
