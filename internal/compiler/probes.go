package compiler

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// CodeProbeNameClash is the problem of two probes whose ids start with the same eight hex digits: their namespaces would be
// one.
const CodeProbeNameClash = "probe_name_clash"

// ProbeTarget is a probe as it is built on the host (plan §2.12, M25): a namespace with a veth pair whose gateway end is a
// port of the bridge of the probe's network, and a DHCP client that gives it an address.
type ProbeTarget struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NetworkID string `json:"network_id"`
	// Bridge is the bridge of the probe's network. Namespace is the probe's namespace (executor.ProbeNSPrefix and the first
	// eight hex digits of its id), HostIf the gateway's end of the pair.
	Bridge    string `json:"bridge"`
	Namespace string `json:"namespace"`
	HostIf    string `json:"host_if"`
	// MAC is derived from the id, so a probe keeps its hardware address (and the leases and reservations by it) when the
	// gateway restarts or the probe is made again.
	MAC string `json:"mac"`
	// Hostname is what the DHCP client sends: the probe's name as a DNS label.
	Hostname string `json:"hostname,omitempty"`
}

// ProbeShort is the part of a probe's id its namespace and interface are named after: the first eight hex digits.
func ProbeShort(id string) string { return strings.ReplaceAll(strings.ToLower(id), "-", "")[:8] }

// ProbeMAC is the hardware address of a probe: locally administered and unicast (the first octet 02), the rest from the
// id.
func ProbeMAC(id string) string {
	h := sha256.Sum256([]byte("chaosgw-probe-mac:" + strings.ToLower(id)))
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", h[0], h[1], h[2], h[3], h[4])
}

// probeHostname is the probe's name as a DNS label: lower case letters, digits and hyphens, at most 63 characters.
func probeHostname(name string) string {
	n := nameClean.ReplaceAllString(strings.ToLower(name), "-")
	n = strings.Trim(strings.ReplaceAll(n, "_", "-"), "-")
	if len(n) > 63 {
		n = strings.Trim(n[:63], "-")
	}
	return n
}

// compileProbes lists the probes whose network has a bridge in this target; a probe of a network the host cannot build
// (a WireGuard network, which validation refuses, or a network with an error) is left out.
func (t *Target) compileProbes(idx *domain.Index) {
	bridges := map[string]string{}
	for _, b := range t.Bridges {
		bridges[b.NetworkID] = b.Name
	}
	seen := map[string]string{}
	var ids []string
	for id, d := range idx.Devices {
		if d.Origin == domain.OriginProbe {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := idx.Devices[id]
		br, ok := bridges[d.Network]
		if !ok {
			continue
		}
		short := ProbeShort(id)
		if other, clash := seen[short]; clash {
			t.errorf(CodeProbeNameClash, d.Name, "the probes %q and %q have ids that start with the same eight digits %s: make one of them again", other, d.Name, short)
			continue
		}
		seen[short] = d.Name
		t.Probes = append(t.Probes, ProbeTarget{ID: id, Name: d.Name, NetworkID: d.Network, Bridge: br,
			Namespace: executor.ProbeNSPrefix + short, HostIf: executor.ProbeHostPrefix + short, MAC: ProbeMAC(id), Hostname: probeHostname(d.Name)})
	}
	sort.Slice(t.Probes, func(i, j int) bool { return t.Probes[i].Namespace < t.Probes[j].Namespace })
}
