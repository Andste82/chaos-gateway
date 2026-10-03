package appliance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Addresses and names of the topology (plan §4.2 uses the same ones).
const (
	ServerAddr   = "203.0.113.10" // the upstream router and server, a namespace on the host
	GatewayUp    = "203.0.113.1"  // the gateway's address on the uplink, set by netplan like on a real host
	HostMgmt     = "192.168.56.254"
	GatewayMgmt  = "192.168.56.1"
	ClientAddr   = "10.10.0.10"
	GatewayLAN   = "10.10.0.1"
	MgmtNetwork  = "192.168.56.0/24"
	GuestUser    = "ubuntu"
	macUplink    = "52:54:00:aa:00:01"
	macLAN       = "52:54:00:aa:00:02"
	macMgmt      = "52:54:00:aa:00:03"
	macClientNIC = "02:00:00:00:10:10"
)

// MACs of the VM's NICs, in the order of the topology: uplink, test network, management.
var MACs = struct{ Uplink, LAN, Mgmt string }{macUplink, macLAN, macMgmt}

// ClientMAC is the MAC of the client namespace's interface.
const ClientMAC = macClientNIC

// Seed describes what cloud-init configures at the first boot: the user with the harness's SSH key
// and the OS-owned interfaces, as netplan does on a real host (plan §2.1: the uplink and the
// management interface belong to the operating system).
type Seed struct {
	Hostname     string
	SSHPublicKey string
	// TwoPorts is the two-port topology: the management network lives behind the uplink interface,
	// which then has both addresses; there is no management NIC.
	TwoPorts bool
}

// UserData is the cloud-config of the first boot. Nothing is installed here: the host setup does
// that, and it is part of what the tests exercise.
func (s Seed) UserData() string {
	return fmt.Sprintf(`#cloud-config
hostname: %s
users:
  - name: %s
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: true
    ssh_authorized_keys:
      - %s
ssh_pwauth: false
package_update: false
`, s.Hostname, GuestUser, strings.TrimSpace(s.SSHPublicKey))
}

// MetaData is the NoCloud meta-data.
func (s Seed) MetaData() string {
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", s.Hostname, s.Hostname)
}

// NetworkConfig is the netplan configuration (network config version 2): the uplink and the
// management interface with static addresses, the management default route, and the test port
// without an address. Interfaces are matched by MAC and named for readable logs.
func (s Seed) NetworkConfig() string {
	var b strings.Builder
	b.WriteString("version: 2\nethernets:\n")
	eth := func(name, mac string, addrs []string, route bool) {
		fmt.Fprintf(&b, "  %s:\n    match:\n      macaddress: \"%s\"\n    set-name: %s\n", name, mac, name)
		if len(addrs) == 0 {
			b.WriteString("    dhcp4: false\n    dhcp6: false\n    optional: true\n")
			return
		}
		b.WriteString("    dhcp4: false\n    dhcp6: false\n    addresses:\n")
		for _, a := range addrs {
			fmt.Fprintf(&b, "      - %s\n", a)
		}
		if route {
			fmt.Fprintf(&b, "    routes:\n      - to: default\n        via: %s\n    nameservers:\n      addresses: [1.1.1.1, 8.8.8.8]\n", HostMgmt)
		}
	}
	if s.TwoPorts {
		eth("wan0", macUplink, []string{GatewayUp + "/24", GatewayMgmt + "/24"}, true)
		eth("lan0", macLAN, nil, false)
		return b.String()
	}
	eth("wan0", macUplink, []string{GatewayUp + "/24"}, false)
	eth("lan0", macLAN, nil, false)
	eth("mgmt0", macMgmt, []string{GatewayMgmt + "/24"}, true)
	return b.String()
}

// WriteSeed writes the three documents into dir and builds the NoCloud seed image from them with
// cloud-localds (package cloud-image-utils) or, failing that, genisoimage.
func WriteSeed(ctx context.Context, dir string, s Seed) (string, error) {
	files := map[string]string{"user-data": s.UserData(), "meta-data": s.MetaData(), "network-config": s.NetworkConfig()}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	iso := filepath.Join(dir, "seed.iso")
	var cmd *exec.Cmd
	switch {
	case have("cloud-localds"):
		cmd = exec.CommandContext(ctx, "cloud-localds", "--network-config", filepath.Join(dir, "network-config"), iso, filepath.Join(dir, "user-data"), filepath.Join(dir, "meta-data"))
	case have("genisoimage"):
		cmd = exec.CommandContext(ctx, "genisoimage", "-quiet", "-output", iso, "-volid", "cidata", "-joliet", "-rock",
			filepath.Join(dir, "user-data"), filepath.Join(dir, "meta-data"), filepath.Join(dir, "network-config"))
	default:
		return "", fmt.Errorf("appliance: neither cloud-localds (cloud-image-utils) nor genisoimage is installed")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("appliance: build the seed image: %v\n%s", err, out)
	}
	return iso, nil
}

func have(tool string) bool { _, err := exec.LookPath(tool); return err == nil }
