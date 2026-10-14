package api_test

import (
	"strings"
	"testing"
	"testing/fstest"
)

// P3-M12-07 (plan §3.4): the preflight checks every interface that is or may become a test interface for
// netplan and NetworkManager configuration, names the file and the entry (or the connection) and says what
// to remove; the setup wizard shows that text (GET /setup), every client gets it from GET
// /system/preflight. Before the setup an OS-configured interface is a warning; after it, a test interface
// that the OS configures fails the preflight. The uplink and the management interface are the operating
// system's and are not checked.
func TestThePreflightNamesTheNetplanEntryOrConnectionOfATestInterface(t *testing.T) {
	host := fstest.MapFS{
		"etc/netplan/50-cloud-init.yaml":                         {Data: []byte("network:\n  version: 2\n  ethernets:\n    lan0:\n      dhcp4: true\n    mgmt0:\n      addresses: [192.168.56.1/24]\n")},
		"etc/NetworkManager/system-connections/lab.nmconnection": {Data: []byte("[connection]\nid=lab port\ntype=ethernet\n\n[ethernet]\nmac-address=02:00:00:00:01:01\n")},
	}
	g := newGW(t, func(o *options) { o.hostFS = host })
	checks := func(report map[string]any) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		for _, c := range report["checks"].([]any) {
			m := c.(map[string]any)
			out[m["id"].(string)] = m
		}
		return out
	}

	setup := g.do("GET", "/setup", nil, nil, nil).json(t)
	managed := map[string]any{}
	for _, i := range setup["interfaces"].([]any) {
		m := i.(map[string]any)
		managed[m["name"].(string)] = m["os_managed"]
	}
	if managed["lan0"] != true || managed["lan1"] != true || managed["mgmt0"] != true || managed["wan0"] != false {
		t.Errorf("os_managed: %v", managed)
	}
	pf := setup["preflight"].(map[string]any)
	cs := checks(pf)
	lan0 := cs["interface.lan0.os_config"]
	if lan0 == nil || lan0["status"] != "warn" || lan0["interface"] != "lan0" ||
		lan0["message"] != `lan0 is configured by netplan (/etc/netplan/50-cloud-init.yaml, "lan0" under "ethernets"); a test interface must not be` ||
		lan0["fix"] != `delete the entry "lan0:" under "ethernets" in /etc/netplan/50-cloud-init.yaml, then run "sudo netplan apply"` {
		t.Errorf("lan0 before the setup: %v", lan0)
	}
	if lan1 := cs["interface.lan1.os_config"]; lan1 == nil || lan1["status"] != "warn" || !strings.Contains(lan1["fix"].(string), `sudo nmcli connection delete "lab port"`) {
		t.Errorf("lan1 before the setup: %v", lan1)
	}
	if wan0 := cs["interface.wan0.os_config"]; wan0 == nil || wan0["status"] != "ok" || wan0["fix"] != nil {
		t.Errorf("wan0 before the setup: %v", wan0)
	}
	if pf["status"] != "warn" {
		t.Errorf("status before the setup: %v", pf["status"])
	}

	// after the setup lan0 is a test interface (IoT): the check fails; the uplink and the management interface are not checked
	g.finishSetup()
	pf = g.do("GET", "/system/preflight", nil, nil, nil).json(t)
	cs = checks(pf)
	if lan0 := cs["interface.lan0.os_config"]; lan0 == nil || lan0["status"] != "fail" || !strings.HasPrefix(lan0["message"].(string), "lan0 is assigned to a test network but configured by netplan (") {
		t.Errorf("lan0 after the setup: %v", lan0)
	}
	if cs["interface.mgmt0.os_config"] != nil || cs["interface.wan0.os_config"] != nil {
		t.Errorf("the uplink or the management interface is checked: %v", cs)
	}
	if pf["status"] != "fail" || cs["preflight"]["status"] != "warn" {
		t.Errorf("status after the setup: %v (%v)", pf["status"], cs["preflight"])
	}
	for _, i := range g.do("GET", "/system/interfaces", nil, nil, nil).json(t)["items"].([]any) {
		if m := i.(map[string]any); m["name"] == "lan0" && m["os_managed"] != true {
			t.Errorf("lan0: %v", m)
		}
	}
}

// A configuration file the executor cannot read or parse is a warning of its own; the others are still checked.
func TestThePreflightSaysWhichOSConfigurationFileItCannotRead(t *testing.T) {
	g := newGW(t, func(o *options) {
		o.hostFS = fstest.MapFS{
			"etc/netplan/01-bad.yaml": {Data: []byte("network: [\n")},
			"etc/netplan/02-ok.yaml":  {Data: []byte("network:\n  ethernets:\n    lan1: {}\n")},
		}
	})
	pf := g.do("GET", "/setup", nil, nil, nil).json(t)["preflight"].(map[string]any)
	var unreadable, lan1 map[string]any
	for _, c := range pf["checks"].([]any) {
		m := c.(map[string]any)
		switch m["id"] {
		case "interfaces.os_config":
			unreadable = m
		case "interface.lan1.os_config":
			lan1 = m
		}
	}
	if unreadable == nil || unreadable["status"] != "warn" || !strings.Contains(unreadable["message"].(string), "/etc/netplan/01-bad.yaml") {
		t.Errorf("the unreadable file: %v", unreadable)
	}
	if lan1 == nil || lan1["status"] != "warn" {
		t.Errorf("the readable file is still checked: %v", lan1)
	}
}
