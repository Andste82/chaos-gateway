package dnsproxy

import (
	"bufio"
	"net/netip"
	"os"
	"strings"
)

// DefaultResolvConfs are looked at in this order: with systemd-resolved the host's own
// /etc/resolv.conf names the stub on 127.0.0.53, which does not exist inside the service namespace;
// the file resolved keeps for its upstream servers does.
var DefaultResolvConfs = []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"}

// HostResolvers returns the IPv4 resolvers of the host: the first file that names any, loopback
// addresses left out (a local stub is not reachable from the service namespace), at most four.
func HostResolvers(paths ...string) []netip.Addr {
	if len(paths) == 0 {
		paths = DefaultResolvConfs
	}
	for _, p := range paths {
		if out := readResolvConf(p); len(out) > 0 {
			return out
		}
	}
	return nil
}

func readResolvConf(path string) []netip.Addr {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		a, err := netip.ParseAddr(strings.SplitN(fields[1], "%", 2)[0])
		if err != nil || !a.Is4() || a.IsLoopback() || a.IsUnspecified() || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
		if len(out) == 4 {
			break
		}
	}
	return out
}
