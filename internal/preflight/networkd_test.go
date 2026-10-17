package preflight

import (
	"strings"
	"testing"
	"testing/fstest"
)

// dracutDefault is dracut's default of systemd-networkd: DHCP on every interface that is no virtual one and
// no loopback (issue #64 item 2, a Raspberry Pi with Ubuntu 26.04).
const dracutDefault = "[Match]\nKind=!*\nType=!loopback\n\n[Network]\nDHCP=yes\n"

// Issue #64 item 2: systemd-networkd configures an interface by the first .network file that selects it; the
// preflight names that file and the fix (the host setup's rule, which comes first in networkd's order), and a
// rule with [Link] Unmanaged=yes that comes first leaves the interface alone.
func TestThePreflightFindsTheNetworkdFileThatConfiguresAnInterface(t *testing.T) {
	host := fstest.MapFS{
		"run/systemd/network/zzzz-dracut-default.network": {Data: []byte(dracutDefault)},
		// netplan's generated file: the netplan entry is reported, not this
		"run/systemd/network/10-netplan-eth2.network": {Data: []byte("[Match]\nName=eth2\n\n[Network]\nDHCP=ipv4\n")},
		// systemd's own files select other interfaces
		"usr/lib/systemd/network/80-container-ve.network": {Data: []byte("[Match]\nName=ve-*\nDriver=veth\n\n[Network]\nAddress=0.0.0.0/28\n")},
		"usr/lib/systemd/network/80-wifi-adhoc.network":   {Data: []byte("[Match]\nType=wlan\nWLANInterfaceType=ad-hoc\n")},
	}
	entries, errs := ReadOSConfig(host)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	status, msg, fix := InterfaceCheck("eth0", "02:00:00:00:00:01", "macb", true, entries)
	if status != "fail" || msg != "eth0 is assigned to a test network but configured by systemd-networkd (/run/systemd/network/zzzz-dracut-default.network)" {
		t.Errorf("eth0: %s %q", status, msg)
	}
	if want := `keep systemd-networkd off eth0: write /etc/systemd/network/10-chaosgw-eth0.network with "[Match]" "Name=eth0" and "[Link]" "Unmanaged=yes" (sudo sh host-setup.sh --test-interface eth0 does), then run "sudo networkctl reload"`; fix != want {
		t.Errorf("eth0 fix %q", fix)
	}
	for _, e := range entries {
		if strings.Contains(e.File, "10-netplan-") {
			t.Errorf("netplan's generated file is an entry: %+v", e)
		}
	}

	// the host setup's rule comes first and leaves eth0 alone; eth1 is still configured by the default
	host["etc/systemd/network/10-chaosgw-eth0.network"] = &fstest.MapFile{Data: []byte("[Match]\nName=eth0\n\n[Link]\nUnmanaged=yes\n")}
	entries, _ = ReadOSConfig(host)
	if st, m, _ := InterfaceCheck("eth0", "", "", true, entries); st != "ok" {
		t.Errorf("eth0 with the rule: %s %q", st, m)
	}
	if st, _, _ := InterfaceCheck("eth1", "", "", true, entries); st != "fail" {
		t.Errorf("eth1: %s", st)
	}
	// a file of the same name in /etc hides the one in /run; a symlink to /dev/null (empty) masks it
	host["etc/systemd/network/zzzz-dracut-default.network"] = &fstest.MapFile{Data: nil}
	entries, _ = ReadOSConfig(host)
	if st, m, _ := InterfaceCheck("eth1", "", "", true, entries); st != "ok" {
		t.Errorf("eth1 with the default masked: %s %q", st, m)
	}

	// a file that comes before the rule is the one to change
	host["etc/systemd/network/05-lab.network"] = &fstest.MapFile{Data: []byte("[Match]\nName=eth0 eth3\n\n[Network]\nDHCP=yes\n")}
	entries, _ = ReadOSConfig(host)
	st, m, f := InterfaceCheck("eth0", "", "", false, entries)
	if st != "warn" || !strings.Contains(m, "systemd-networkd (/etc/systemd/network/05-lab.network)") ||
		f != `change /etc/systemd/network/05-lab.network so that its [Match] section does not select eth0, or delete it, then run "sudo networkctl reload"` {
		t.Errorf("eth0 with an earlier file: %s %q %q", st, m, f)
	}
}

// Issue #64 item 2: a [Match] section selects an interface when every condition holds: lists of globs (a list
// that starts with "!" is negated, an empty setting clears the list), MAC addresses, the driver, the type and
// kind of a physical ethernet port; a WLAN condition never selects one, a condition the preflight cannot
// evaluate is taken to hold, and a file without a condition is ignored.
func TestThePreflightEvaluatesANetworkdMatch(t *testing.T) {
	for _, c := range networkdMatchCases {
		e, ok := networkdEntry([]byte("[Match]\n"+c.match+"\n[Network]\nDHCP=yes\n"), "/etc/systemd/network/a.network")
		if !ok {
			t.Errorf("%q: no entry", c.match)
			continue
		}
		if got := len(Configuring([]OSEntry{e}, c.name, c.mac, c.driver)) == 1; got != c.want {
			t.Errorf("%q selects %s (%s, %s): %v, want %v", c.match, c.name, c.mac, c.driver, got, c.want)
		}
	}
	for _, raw := range networkdIgnored {
		if _, ok := networkdEntry([]byte(raw), "/x.network"); ok {
			t.Errorf("%q is an entry", raw)
		}
	}
	// the values of a condition that is not evaluated do not leave the preflight
	e, _ := networkdEntry([]byte("[Match]\nSSID=my-home-network\nName=wlan0\n"), "/x.network")
	if v, ok := e.Match["SSID"]; !ok || v != nil {
		t.Errorf("SSID: %v", e.Match)
	}
	if !parseBool("yes") || !parseBool("TRUE") || parseBool("no") || parseBool("") {
		t.Error("parseBool")
	}
	if e, _ := networkdEntry([]byte("[Match]\nName=eth0\n[Link]\nUnmanaged=1\n"), "/x.network"); !e.Unmanaged {
		t.Error("Unmanaged=1")
	}
}

// networkdMatchCases are [Match] sections and whether they select a physical ethernet port with the name, MAC
// address and driver; the preflight and the host setup's check (hostsetup_test.go) evaluate them alike.
var networkdMatchCases = []struct {
	match, name, mac, driver string
	want                     bool
}{
	{"Name=eth0", "eth0", "", "", true},
	{"Name=eth?", "eth0", "", "", true},
	{"Name=eth[01]", "eth1", "", "", true},
	{"Name=eth[!01]", "eth1", "", "", false},
	{"Name=eth[!01]", "eth2", "", "", true},
	{"Name=eth0.10", "eth0x10", "", "", false},
	{"Name=en* eth*", "eth1", "", "", true},
	{"Name=!eth0", "eth0", "", "", false},
	{"Name=!eth0", "eth1", "", "", true},
	{"Name=!eth0 eth1", "eth1", "", "", false},
	{"Name=eth0\nName=\nName=eth1", "eth0", "", "", false},
	{"Name=eth0\nName=eth1", "eth0", "", "", true},
	{"MACAddress=02:00:00:00:00:01 02:00:00:00:00:02", "x", "02:00:00:00:00:02", "", true},
	{"MACAddress=02:00:00:00:00:01", "x", "02:00:00:00:00:02", "", false},
	{"PermanentMACAddress=02:00:00:00:00:01", "x", "02:00:00:00:00:01", "", true},
	{"Driver=igb e1000*", "x", "", "e1000e", true},
	{"Driver=igb", "x", "", "", false},
	{"Type=ether", "x", "", "", true},
	{"Type=wlan", "x", "", "", false},
	{"Type=!loopback", "x", "", "", true},
	{"Kind=!*", "x", "", "", true},
	{"Kind=bridge", "x", "", "", false},
	{"SSID=lab", "x", "", "", false},
	{"Path=platform-*", "x", "", "", true},
	{"Path=platform-*\nName=eth9", "x", "", "", false},
	{"Virtualization=container", "x", "", "", true},
}

// networkdIgnored are .network files networkd ignores: no [Match] condition.
var networkdIgnored = []string{"", "[Network]\nDHCP=yes\n", "[Match]\n\n[Network]\nDHCP=yes\n", "[Match]\nName=\n"}
