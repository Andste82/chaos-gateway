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
	if strings.Contains(string(res.Data[0]), "secret-psk") {
		t.Fatalf("the content of a file left the executor: %s", res.Data[0])
	}
	var st HostNetState
	if err := json.Unmarshal(res.Data[0], &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 2 || st.Entries[0].ID != "enp2s0" || st.Entries[0].Section != "ethernets" || st.Entries[0].File != "/etc/netplan/50-cloud-init.yaml" {
		t.Errorf("entries: %+v", st.Entries)
	}
	if len(st.Errors) != 1 || !strings.Contains(st.Errors[0], "/etc/netplan/bad.yaml") {
		t.Errorf("errors: %v", st.Errors)
	}
}
