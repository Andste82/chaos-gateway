package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

// P3-M12-07: the executor reads where the host's operating system configures network interfaces (it runs as
// root and can read netplan's 0600 files; the API container cannot) from the host root it is given, runs no
// command for it, and returns the entries only: an unreadable file is named, no content leaves.
func TestTheExecutorReadsTheHostsNetworkConfigurationWithoutItsContent(t *testing.T) {
	host := fstest.MapFS{
		"etc/netplan/50-cloud-init.yaml": {Data: []byte("network:\n  version: 2\n  ethernets:\n    enp2s0:\n      dhcp4: true\n  wifis:\n    wlan0:\n      access-points:\n        home: {password: \"secret-psk\"}\n")},
		"etc/netplan/bad.yaml":           {Data: []byte("network: [\n")},
		// yaml.v3 quotes the scalar of a type mismatch in its error ("cannot unmarshal !!str `...`")
		"etc/netplan/mistyped.yaml":  {Data: []byte("network:\n  ethernets:\n    enp3s0:\n      interfaces: wpa-psk-in-the-wrong-place\n")},
		"etc/netplan/not-a-map.yaml": {Data: []byte("network: top-secret-scalar\n")},
	}
	fr := &fakeRunner{}
	e := newExec(t, fr, WithHostRoot(host))
	op := mustDecode(t, `{"type":"read","what":"host_net"}`)
	res, err := e.Do(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.commands()) != 0 {
		t.Errorf("a command was run: %v", fr.commands())
	}
	if raw := string(res.Data[0]); strings.Contains(raw, "secret-psk") || strings.Contains(raw, "wpa-psk-in-the-wrong-place") || strings.Contains(raw, "top-secret-scalar") {
		t.Fatalf("the content of a file left the executor: %s", res.Data[0])
	}
	var st HostNetState
	if err := json.Unmarshal(res.Data[0], &st); err != nil {
		t.Fatal(err)
	}
	// a device that cannot be read is still named by its id (Phase 3 audit), without its content
	if len(st.Entries) != 3 || st.Entries[2].ID != "enp3s0" || st.Entries[0].ID != "enp2s0" || st.Entries[0].Section != "ethernets" || st.Entries[0].File != "/etc/netplan/50-cloud-init.yaml" {
		t.Errorf("entries: %+v", st.Entries)
	}
	want := []string{
		"/etc/netplan/bad.yaml: not valid netplan YAML (line 1)",
		"/etc/netplan/mistyped.yaml: not valid netplan YAML (network.ethernets.enp3s0, line 4)",
		"/etc/netplan/not-a-map.yaml: not valid netplan YAML (line 1)",
	}
	if strings.Join(st.Errors, "\n") != strings.Join(want, "\n") {
		t.Errorf("errors: %q", st.Errors)
	}
}
