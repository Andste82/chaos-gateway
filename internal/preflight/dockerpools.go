package preflight

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// dockerBridge matches the bridges Docker makes: docker0 and one br-<12 hex digits> per user-defined network. The gateway's own
// bridges are named after their network and never have this form.
var dockerBridge = regexp.MustCompile(`^(docker0|br-[0-9a-f]{12})$`)

// ipAddrLink is one link of `ip -j -4 addr show`.
type ipAddrLink struct {
	Name  string `json:"ifname"`
	Addrs []struct {
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

// dockerDaemonConfig is the part of /etc/docker/daemon.json that sets the addresses Docker hands out.
type dockerDaemonConfig struct {
	BIP   string `json:"bip"`
	Pools []struct {
		Base string `json:"base"`
	} `json:"default-address-pools"`
}

// dockerPoolCheck is the check of plan §3.8 (Chaos Gateway's networks must not overlap Docker's own address pools): the address
// ranges of Docker's bridges that exist, and the pools the administrator set for new Docker networks (daemon.json, read from the
// host's /etc), must not overlap an address of any other interface of the host, which are the uplink, the management interface
// and the gateway's own test networks. Docker's built-in pools are not counted: it skips a range that is in use when it makes a
// network, so only a pool set by hand can run into one.
func dockerPoolCheck(ctx context.Context, p HostProbe) HostCheck {
	const id = "host.docker_pools"
	fix := "give Docker other address ranges (default-address-pools and bip in /etc/docker/daemon.json, then restart Docker) or use other addresses for the networks of the gateway"
	out, err := p.Run(ctx, "ip", "-j", "-4", "addr", "show")
	var links []ipAddrLink
	if err != nil || (strings.TrimSpace(out) != "" && json.Unmarshal([]byte(out), &links) != nil) {
		return HostCheck{ID: id, Status: "warn", Message: "the addresses of the host's interfaces cannot be listed, so the overlap with Docker's address ranges is not checked"}
	}

	type owner struct {
		prefix netip.Prefix
		name   string // the interface, or "default-address-pools"
	}
	var docker, others []owner
	for _, l := range links {
		for _, a := range l.Addrs {
			ip, perr := netip.ParseAddr(a.Local)
			if perr != nil || l.Name == "lo" {
				continue
			}
			pfx := netip.PrefixFrom(ip, a.PrefixLen).Masked()
			if dockerBridge.MatchString(l.Name) {
				docker = append(docker, owner{pfx, l.Name})
			} else {
				others = append(others, owner{pfx, l.Name})
			}
		}
	}
	if raw, ok := p.hostRootFile("etc/docker/daemon.json"); ok {
		var cfg dockerDaemonConfig
		if json.Unmarshal([]byte(raw), &cfg) == nil {
			ranges := []string{cfg.BIP}
			for _, pool := range cfg.Pools {
				ranges = append(ranges, pool.Base)
			}
			for _, s := range ranges {
				if pfx, perr := netip.ParsePrefix(s); perr == nil && pfx.Addr().Is4() {
					docker = append(docker, owner{pfx.Masked(), "daemon.json"})
				}
			}
		}
	}

	var clashes []string
	for _, d := range docker {
		for _, o := range others {
			if d.prefix.Overlaps(o.prefix) {
				clashes = append(clashes, o.name+" "+o.prefix.String()+" overlaps "+d.name+" "+d.prefix.String())
			}
		}
	}
	if len(clashes) > 0 {
		sort.Strings(clashes)
		return HostCheck{ID: id, Status: "warn", Message: "an address range of Docker overlaps an interface of the host, so traffic to it cannot be routed to both: " + strings.Join(clashes, "; "), Fix: fix}
	}
	return HostCheck{ID: id, Status: "ok", Message: "no address of an interface overlaps the address ranges of Docker's bridges and pools"}
}

// hostRootFile reads a file of the host's file system (the executor's /host); ok is false when there is none or it is not mounted.
func (p HostProbe) hostRootFile(name string) (string, bool) {
	if p.HostFS == nil {
		return "", false
	}
	raw, err := fs.ReadFile(p.HostFS, name)
	return string(raw), err == nil
}
