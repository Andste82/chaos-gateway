package preflight

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// systemd-networkd configures an interface by the first .network file, in the order of the files' names, whose
// [Match] section selects it (issue #64: a distribution's default such as dracut's
// /run/systemd/network/zzzz-dracut-default.network runs DHCP on every ethernet port no netplan entry names).
// A file whose [Link] section says `Unmanaged=yes` makes networkd leave the interfaces it selects alone; the
// host setup writes one per test interface (deploy/host-setup.sh --test-interface, /etc/systemd/network/
// 10-chaosgw-<interface>.network), which sorts before netplan's generated files (10-netplan-*) and every
// default. A file of the same name in an earlier directory of NetworkdDirs hides the others (/etc hides
// /run, /run hides /usr/lib), and a file without a [Match] condition is ignored by networkd (an empty file, or
// a symlink to /dev/null that masks a file of the same name, too). Drop-ins (<file>.network.d/*.conf) are not
// read. As for netplan, a directory is read only when it is there under the root: the shipped deployment
// mounts the host's /etc and /usr/lib/systemd/network, not /run/systemd/network, which exists only while
// networkd or netplan has written to it (docs/open-items.md I64-02, decided 2026-10-18).
var NetworkdDirs = []string{"etc/systemd/network", "run/systemd/network", "usr/local/lib/systemd/network", "usr/lib/systemd/network", "lib/systemd/network"}

// ManagerNetworkd is systemd-networkd's entries' Manager.
const ManagerNetworkd = "systemd-networkd"

// netplanGenerated is the prefix of the .network files netplan writes to /run/systemd/network from its YAML:
// their entries are netplan's, which the preflight reports with netplan's file and fix.
const netplanGenerated = "10-netplan-"

// networkdKeys are the [Match] keys the preflight evaluates for an interface; the WLAN keys select only
// wireless interfaces, which a test interface is not. Any other key (Path, Property, Virtualization, Host,
// KernelCommandLine, ...) is a condition the preflight cannot evaluate: it is kept without its values and taken
// to hold.
var (
	networkdKeys     = map[string]bool{"Name": true, "MACAddress": true, "PermanentMACAddress": true, "Driver": true, "Type": true, "Kind": true}
	networkdWLANKeys = map[string]bool{"WLANInterfaceType": true, "SSID": true, "BSSID": true}
)

// TestInterfaceType and TestInterfaceKind are the networkd type and kind the preflight takes an interface to
// have: a physical ethernet port.
const (
	TestInterfaceType = "ether"
	TestInterfaceKind = ""
)

// readNetworkd reads the .network files of systemd-networkd, in networkd's order: one entry per file with a
// [Match] condition (Unmanaged set when its [Link] section says so), but netplan's generated ones.
func readNetworkd(fsys fs.FS) (entries []OSEntry, errs []error) {
	winners := map[string]string{}
	for _, dir := range NetworkdDirs {
		for _, f := range files(fsys, dir, ".network") {
			if _, shadowed := winners[path.Base(f)]; !shadowed {
				winners[path.Base(f)] = f
			}
		}
	}
	for _, name := range sortedKeys(winners) {
		f := winners[name]
		if strings.HasPrefix(name, netplanGenerated) && path.Dir(f) == "run/systemd/network" {
			continue
		}
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if e, ok := networkdEntry(raw, "/"+f); ok {
			entries = append(entries, e)
		}
	}
	return entries, errs
}

// networkdEntry reads one .network file: its [Match] conditions (a list setting adds to the ones before it,
// an empty one clears them; a list that starts with "!" is negated, item by item) and [Link] Unmanaged. ok is
// false for a file without a condition, which networkd ignores.
func networkdEntry(raw []byte, file string) (e OSEntry, ok bool) {
	e = OSEntry{Manager: ManagerNetworkd, File: file, ID: path.Base(file), Match: map[string][]string{}}
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, ";") {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = l[1 : len(l)-1]
			continue
		}
		k, v, found := strings.Cut(l, "=")
		if !found {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case section == "Link" && k == "Unmanaged":
			e.Unmanaged = parseBool(v)
		case section != "Match":
		case !networkdKeys[k]:
			e.Match[k] = nil // a condition that is not evaluated, without its value
		case v == "":
			delete(e.Match, k)
		default:
			neg := strings.HasPrefix(v, "!")
			for _, item := range strings.Fields(strings.TrimPrefix(v, "!")) {
				if neg {
					item = "!" + item
				}
				e.Match[k] = append(e.Match[k], item)
			}
		}
	}
	return e, len(e.Match) > 0
}

// parseBool reads a systemd boolean.
func parseBool(v string) bool {
	switch strings.ToLower(v) {
	case "1", "yes", "y", "true", "t", "on":
		return true
	}
	return false
}

// networkdSelects reports whether a [Match] section selects the interface (a physical ethernet port with this
// name, MAC address and driver): every condition must hold.
func networkdSelects(match map[string][]string, name, mac, driver string) bool {
	for k, items := range match {
		var holds bool
		switch {
		case k == "MACAddress" || k == "PermanentMACAddress":
			holds = false
			for _, m := range items {
				holds = holds || (mac != "" && strings.EqualFold(m, mac))
			}
		case k == "Name":
			holds = globList(items, name)
		case k == "Driver":
			holds = globList(items, driver)
		case k == "Type":
			holds = globList(items, TestInterfaceType)
		case k == "Kind":
			holds = globList(items, TestInterfaceKind)
		case networkdWLANKeys[k]:
			holds = false
		default:
			holds = true
		}
		if !holds {
			return false
		}
	}
	return true
}

// globList is networkd's test of a list of globs: no item that starts with "!" may match s (an empty s matches
// none), and when the list has items without "!", one of them must match.
func globList(items []string, s string) bool {
	positive, match := false, false
	for _, g := range items {
		neg := strings.HasPrefix(g, "!")
		g = strings.TrimPrefix(g, "!")
		positive = positive || !neg
		// fnmatch's "[!...]" is path.Match's "[^...]"
		if ok, err := path.Match(strings.ReplaceAll(g, "[!", "[^"), s); s != "" && err == nil && ok {
			if neg {
				return false
			}
			match = true
		}
	}
	return match || !positive
}

// networkdRule is the file the host setup writes so that systemd-networkd leaves the interface alone.
func networkdRule(iface string) string {
	return "10-chaosgw-" + iface + ".network"
}

// networkdFix says how to keep systemd-networkd off the interface the file configures: the host setup's rule,
// or, when the file comes before the rule in networkd's order, the file itself.
func networkdFix(e OSEntry, iface string) string {
	reload := `, then run "sudo networkctl reload"`
	if e.ID < networkdRule(iface) {
		return fmt.Sprintf("change %s so that its [Match] section does not select %s, or delete it%s", e.File, iface, reload)
	}
	return fmt.Sprintf(`keep systemd-networkd off %s: write /etc/systemd/network/%s with "[Match]" "Name=%s" and "[Link]" "Unmanaged=yes" (sudo sh host-setup.sh --test-interface %s does)%s`, iface, networkdRule(iface), iface, iface, reload)
}
