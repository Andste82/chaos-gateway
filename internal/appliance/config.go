package appliance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Ports of the port forward test (M35a, plan level 2: a forward in the two-port topology, next to a
// port published by a Docker container).
const (
	// ForwardPort is the external port of the forward on the uplink.
	ForwardPort = 8088
	// ForwardMgmtPort is the external port of the forward on the management network, three ports only.
	ForwardMgmtPort = 8090
	// TargetPort is where the device listens: the client namespace, behind both forwards.
	TargetPort = 9000
	// PublishedPort is the port a container in the VM's Docker publishes on the host.
	PublishedPort = 8089
	// ContainerPort is the port inside that container.
	ContainerPort = 8000
)

// GatewayConfig is the configuration the level 2 test applies with `chaosgw apply --file`: the uplink and
// the management interface by MAC (in the two-port topology both are the uplink NIC), one test network on
// the test NIC, and port forwards to the client namespace (M35a): one on the uplink and, with three
// ports, one on the management network, which the two-port topology refuses.
func GatewayConfig(two bool) string {
	mgmt := MACs.Mgmt
	if two {
		mgmt = MACs.Uplink // the management network lives behind the uplink interface
	}
	cfg := fmt.Sprintf(`schema_version: 1
uplink:
  interface: {mac: '%s'}
  gateway: %s
management:
  interface: {mac: '%s'}
  allowed_sources: [%s]
networks:
  0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21:
    type: lan
    name: IoT
    interfaces: [{mac: '%s'}]
    address: %s/24
port_forwards:
  5e1f0c2a-7b3d-4e8f-9a1b-2c3d4e5f6a71:
    name: device-web
    protocol: tcp
    entry: uplink
    external_port: %d
    target: {address: %s}
    target_port: %d
`, MACs.Uplink, ServerAddr, mgmt, MgmtNetwork, MACs.LAN, GatewayLAN, ForwardPort, ClientAddr, TargetPort)
	if !two {
		cfg += fmt.Sprintf(`  5e1f0c2a-7b3d-4e8f-9a1b-2c3d4e5f6a72:
    name: device-web-mgmt
    protocol: tcp
    entry: management
    external_port: %d
    target: {address: %s}
    target_port: %d
`, ForwardMgmtPort, ClientAddr, TargetPort)
	}
	return cfg
}

// BuildEcho builds the HTTP helper of testdata/echo into dir as a static Linux binary for goarch and
// returns its path: the host's architecture for the client namespace, amd64 for a container in the VM
// (the cloud images are amd64). The working directory must be this package's, as it is for its tests.
func BuildEcho(ctx context.Context, dir, goarch string) (string, error) {
	out := filepath.Join(dir, "echo-"+goarch)
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", out, "./testdata/echo")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("appliance: building the echo helper: %w\n%s", err, b)
	}
	return out, nil
}
