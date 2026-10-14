package preflight

import (
	"strings"
	"testing"
	"testing/fstest"
)

// P3-M12-07: the preflight finds where the operating system configures an interface — a netplan entry by
// its id or its match (name glob, MAC), a port of a netplan bridge or bond, the link of a VLAN, a
// NetworkManager connection by interface name or MAC — and says what to remove. Nothing else of the files
// (addresses, keys, Wi-Fi secrets) is reported.
func TestThePreflightFindsTheOSConfigurationOfAnInterface(t *testing.T) {
	host := fstest.MapFS{
		"etc/netplan/50-cloud-init.yaml": {Data: []byte(`network:
  version: 2
  ethernets:
    enp1s0:
      dhcp4: true
    lan:
      match:
        macaddress: "52:54:00:00:00:03"
      set-name: lan
      addresses: [192.168.7.2/24]
    usb:
      match:
        name: "enx*"
  bridges:
    br0:
      interfaces: [enp4s0]
  vlans:
    vlan10:
      id: 10
      link: enp5s0
  wifis:
    wlan0:
      access-points:
        home:
          password: "secret-psk"
`)},
		"run/netplan/90-extra.yaml": {Data: []byte("network:\n  version: 2\n  renderer: networkd\n")},
		"lib/netplan/broken.yaml":   {Data: []byte("network: [\n")},
		"etc/netplan/README":        {Data: []byte("not yaml")},
		"etc/NetworkManager/system-connections/Wired connection 1.nmconnection": {Data: []byte(`[connection]
id=Wired connection 1
type=ethernet
interface-name=enp6s0

[ipv4]
method=auto
`)},
		"run/NetworkManager/system-connections/by-mac.nmconnection": {Data: []byte(`[connection]
type=ethernet

[ethernet]
mac-address=52:54:00:00:00:07

[wifi-security]
psk=another-secret
`)},
		"etc/NetworkManager/system-connections/unbound.nmconnection": {Data: []byte("[connection]\nid=vpn\ntype=vpn\n")},
	}
	entries, errs := ReadOSConfig(host)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "/lib/netplan/broken.yaml") {
		t.Errorf("errors: %v", errs)
	}
	for _, e := range entries {
		for _, secret := range []string{"secret-psk", "another-secret", "192.168.7.2"} {
			if strings.Contains(e.Where()+e.Fix("x")+e.Name+e.MAC+e.ID, secret) {
				t.Errorf("%+v carries %q", e, secret)
			}
		}
	}

	for _, c := range []struct {
		name, mac string
		want      []string // the words of the fix, nothing for an interface the OS leaves alone
	}{
		{"enp1s0", "52:54:00:00:00:01", []string{`delete the entry "enp1s0:" under "ethernets" in /etc/netplan/50-cloud-init.yaml, then run "sudo netplan apply"`}},
		{"enp3s0", "52:54:00:00:00:03", []string{`delete the entry "lan:" under "ethernets"`}},
		{"enx0011", "02:11:22:33:44:55", []string{`delete the entry "usb:" under "ethernets"`}},
		{"enp4s0", "52:54:00:00:00:04", []string{`remove enp4s0 from the interfaces of "br0" under "bridges" in /etc/netplan/50-cloud-init.yaml`}},
		{"enp5s0", "52:54:00:00:00:05", []string{`delete the entry "vlan10:" under "vlans" in /etc/netplan/50-cloud-init.yaml (a VLAN on enp5s0)`}},
		{"enp6s0", "52:54:00:00:00:06", []string{`delete the NetworkManager connection "Wired connection 1" (sudo nmcli connection delete "Wired connection 1")`}},
		{"enp7s0", "52:54:00:00:00:07", []string{`delete the NetworkManager connection "by-mac"`}},
		{"enp8s0", "52:54:00:00:00:08", nil},
	} {
		status, msg, fix := InterfaceCheck(c.name, c.mac, true, entries)
		if c.want == nil {
			if status != "ok" || fix != "" || msg != c.name+" carries no network configuration of the operating system" {
				t.Errorf("%s: %s %q %q", c.name, status, msg, fix)
			}
			continue
		}
		if status != "fail" || !strings.HasPrefix(msg, c.name+" is assigned to a test network but configured by ") {
			t.Errorf("%s: %s %q", c.name, status, msg)
		}
		for _, w := range c.want {
			if !strings.Contains(fix, w) {
				t.Errorf("%s: fix %q, want %q", c.name, fix, w)
			}
		}
		// a candidate is a warning, with the same fix
		if st, m, f := InterfaceCheck(c.name, c.mac, false, entries); st != "warn" || f != fix || !strings.HasSuffix(m, "; a test interface must not be") {
			t.Errorf("%s as a candidate: %s %q %q", c.name, st, m, f)
		}
	}
	// the message names the file and the entry
	if _, msg, _ := InterfaceCheck("enp1s0", "", true, entries); !strings.Contains(msg, `netplan (/etc/netplan/50-cloud-init.yaml, "enp1s0" under "ethernets")`) {
		t.Errorf("message: %q", msg)
	}
	if _, msg, _ := InterfaceCheck("enp6s0", "", false, entries); !strings.Contains(msg, `NetworkManager (connection "Wired connection 1", /etc/NetworkManager/system-connections/Wired connection 1.nmconnection)`) {
		t.Errorf("message: %q", msg)
	}
	// a host without netplan and NetworkManager configures nothing
	if es, errs := ReadOSConfig(fstest.MapFS{}); len(es) != 0 || len(errs) != 0 {
		t.Errorf("an empty host: %v %v", es, errs)
	}
}

// P3-M12-07 (review): NetworkManager may take an interface no entry names — netplan's NetworkManager
// renderer for all devices, an ethernet connection bound to no interface — and the check says so, with
// how to keep NetworkManager off the interface; `unmanaged-devices` in NetworkManager's configuration
// turns that (and NetworkManager's connections) off for the devices it names, not netplan's entries.
func TestThePreflightSeesNetworkManagerTakeEveryInterface(t *testing.T) {
	host := fstest.MapFS{
		"etc/netplan/01-network-manager-all.yaml": {Data: []byte("network:\n  version: 2\n  renderer: NetworkManager\n")},
		"etc/netplan/50-lab.yaml":                 {Data: []byte("network:\n  ethernets:\n    eth4:\n      dhcp4: true\n")},
		"etc/NetworkManager/system-connections/any.nmconnection": {Data: []byte(
			"[connection]\nid=Wired\ntype=ethernet\n\n[ipv4]\nmethod=auto\n")},
		"etc/NetworkManager/system-connections/manual.nmconnection": {Data: []byte(
			"[connection]\nid=Manual\ntype=ethernet\nautoconnect=false\n")},
		"etc/NetworkManager/NetworkManager.conf": {Data: []byte("[main]\nplugins=keyfile\n")},
		"etc/NetworkManager/conf.d/99-chaos-gateway.conf": {Data: []byte(
			"[keyfile]\nunmanaged-devices=interface-name:eth3;mac:52:54:00:00:00:09\nunmanaged-devices+=interface-name:eth4\n")},
	}
	entries, errs := ReadOSConfig(host)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// eth2: the renderer and the unbound connection, one fix
	status, msg, fix := InterfaceCheck("eth2", "52:54:00:00:00:02", false, entries)
	if status != "warn" ||
		!strings.Contains(msg, `netplan (/etc/netplan/01-network-manager-all.yaml, renderer "NetworkManager" for all devices)`) ||
		!strings.Contains(msg, `NetworkManager (connection "Wired", /etc/NetworkManager/system-connections/any.nmconnection, bound to no interface)`) ||
		strings.Contains(msg, "Manual") {
		t.Errorf("eth2: %s %q", status, msg)
	}
	want := `keep NetworkManager off eth2: add "interface-name:eth2" to "unmanaged-devices=" under "[keyfile]" in /etc/NetworkManager/conf.d/99-chaos-gateway.conf (entries separated by ";"), then run "sudo systemctl reload NetworkManager"`
	if fix != want {
		t.Errorf("eth2 fix %q", fix)
	}
	// eth3 by name and eth5 by MAC are unmanaged: nothing configures them
	for _, c := range [][2]string{{"eth3", "52:54:00:00:00:03"}, {"eth5", "52:54:00:00:00:09"}} {
		if st, m, _ := InterfaceCheck(c[0], c[1], true, entries); st != "ok" {
			t.Errorf("%s: %s %q", c[0], st, m)
		}
	}
	// eth4 is unmanaged, but netplan's entry for it stays
	if st, m, f := InterfaceCheck("eth4", "", true, entries); st != "fail" || strings.Contains(m, "NetworkManager (") ||
		f != `delete the entry "eth4:" under "ethernets" in /etc/netplan/50-lab.yaml, then run "sudo netplan apply"` {
		t.Errorf("eth4: %s %q %q", st, m, f)
	}

	// a later file that names another renderer wins; /etc hides a file of the same name in /lib
	host["lib/netplan/90-networkd.yaml"] = &fstest.MapFile{Data: []byte("network:\n  renderer: networkd\n")}
	delete(host, "etc/NetworkManager/system-connections/any.nmconnection")
	entries, _ = ReadOSConfig(host)
	if st, m, _ := InterfaceCheck("eth2", "", false, entries); st != "ok" {
		t.Errorf("eth2 under networkd: %s %q", st, m)
	}
	host["etc/netplan/90-networkd.yaml"] = &fstest.MapFile{Data: []byte("network:\n  version: 2\n")}
	entries, _ = ReadOSConfig(host)
	if st, _, _ := InterfaceCheck("eth2", "", false, entries); st != "warn" {
		t.Errorf("eth2 with the networkd file hidden: %s", st)
	}
}
