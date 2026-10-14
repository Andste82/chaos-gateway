package preflight

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The interfaces assigned to test networks belong to Chaos Gateway alone: the operating system must not
// configure them (plan §3.4: "they simply have no entry there; the preflight checks this, and the setup
// wizard shows the netplan snippet to remove if one exists"). An entry that only turns the addressing off
// still hands the port to systemd-networkd or NetworkManager, so the fix is always to remove the entry
// (docs/open-items.md P3-M12-07). This file finds the entries: netplan's YAML files and NetworkManager's
// keyfile connections, read from the host's file system. It reports where an interface is named and
// nothing else: no address, no key, no Wi-Fi secret of the files leaves it.

// Where netplan and NetworkManager keep their configuration, relative to the host's root. netplan reads
// /etc, /run and /lib (a file in /etc overrides one of the same name in /run and /lib); NetworkManager's
// keyfiles are in /etc and, generated (also by netplan's NetworkManager renderer), in /run. A directory is
// read only when it is there under the root: the shipped deployment (deploy/compose.executor.yaml) mounts
// only the host's /etc, so there /run/netplan, /lib/netplan and /run/NetworkManager are not seen. The
// keyfiles netplan generates under /run come from its YAML in /etc, which is read.
var (
	NetplanDirs = []string{"etc/netplan", "run/netplan", "lib/netplan"}
	NMDirs      = []string{"etc/NetworkManager/system-connections", "run/NetworkManager/system-connections"}
)

// Managers of the OS network configuration.
const (
	ManagerNetplan        = "netplan"
	ManagerNetworkManager = "NetworkManager"
)

// OSEntry is one place where the operating system configures a network interface: a netplan entry or a
// NetworkManager connection.
type OSEntry struct {
	// Manager is ManagerNetplan or ManagerNetworkManager; File the file on the host ("/etc/netplan/50-cloud-init.yaml").
	Manager string `json:"manager"`
	File    string `json:"file"`
	// Section is netplan's device type of the entry (ethernets, bridges, bonds, vlans, wifis, ...); empty for
	// NetworkManager. ID is the entry's name in it, or the NetworkManager connection's id.
	Section string `json:"section,omitempty"`
	ID      string `json:"id"`
	// Name (an interface name, a netplan glob) and MAC select the interface the entry configures; both empty
	// selects none. Member is set when the entry names the interface as a part of another device (a port of
	// a bridge or bond, the link of a VLAN): Name is then the part.
	Name   string `json:"name,omitempty"`
	MAC    string `json:"mac,omitempty"`
	Member bool   `json:"member,omitempty"`
}

// Matches reports whether the entry configures the interface with this name and MAC address.
func (e OSEntry) Matches(name, mac string) bool {
	if e.Name == "" && e.MAC == "" {
		return false
	}
	if e.MAC != "" && !strings.EqualFold(e.MAC, mac) {
		return false
	}
	if e.Name != "" {
		ok, err := path.Match(e.Name, name)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// Where says where the entry is, in the words of its manager: `netplan (/etc/netplan/50-cloud-init.yaml,
// "enp3s0" under "ethernets")`, `NetworkManager (connection "Wired connection 1", /etc/...)`.
func (e OSEntry) Where() string {
	if e.Manager == ManagerNetworkManager {
		return fmt.Sprintf("NetworkManager (connection %q, %s)", e.ID, e.File)
	}
	return fmt.Sprintf("netplan (%s, %q under %q)", e.File, e.ID, e.Section)
}

// Fix says what to remove so that the operating system leaves the interface alone.
func (e OSEntry) Fix(iface string) string {
	if e.Manager == ManagerNetworkManager {
		return fmt.Sprintf("delete the NetworkManager connection %q (sudo nmcli connection delete %q)", e.ID, e.ID)
	}
	apply := `, then run "sudo netplan apply"`
	switch {
	case e.Member && e.Section == "vlans":
		return fmt.Sprintf("delete the entry %q under %q in %s (a VLAN on %s)%s", e.ID+":", e.Section, e.File, iface, apply)
	case e.Member:
		return fmt.Sprintf("remove %s from the interfaces of %q under %q in %s%s", iface, e.ID, e.Section, e.File, apply)
	}
	return fmt.Sprintf("delete the entry %q under %q in %s%s", e.ID+":", e.Section, e.File, apply)
}

// ReadOSConfig reads the netplan and NetworkManager configuration of the host whose root fsys is. A file
// that cannot be read or parsed is reported in errs and the others are still read; a directory that does
// not exist is no error (no netplan, no NetworkManager).
func ReadOSConfig(fsys fs.FS) (entries []OSEntry, errs []error) {
	for _, dir := range NetplanDirs {
		for _, f := range files(fsys, dir, ".yaml", ".yml") {
			raw, err := fs.ReadFile(fsys, f)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			es, err := netplanEntries(raw, "/"+f)
			if err != nil {
				errs = append(errs, fmt.Errorf("/%s: %w", f, err))
				continue
			}
			entries = append(entries, es...)
		}
	}
	for _, dir := range NMDirs {
		for _, f := range files(fsys, dir) {
			raw, err := fs.ReadFile(fsys, f)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if e, ok := nmEntry(raw, "/"+f); ok {
				entries = append(entries, e)
			}
		}
	}
	return entries, errs
}

// files lists the regular files of a directory with one of the extensions (any without), sorted.
func files(fsys fs.FS, dir string, exts ...string) []string {
	ds, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range ds {
		if d.IsDir() {
			continue
		}
		ok := len(exts) == 0
		for _, x := range exts {
			ok = ok || strings.HasSuffix(d.Name(), x)
		}
		if ok {
			out = append(out, path.Join(dir, d.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// netplanEntries reads the entries of one netplan file: every device of every section, the interface it
// selects (its id, or its match by name or MAC), and the interfaces a bridge or a bond takes as ports and a
// VLAN is on.
func netplanEntries(raw []byte, file string) ([]OSEntry, error) {
	var doc struct {
		Network map[string]yaml.Node `yaml:"network"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, notNetplan(err, "")
	}
	var sections []string
	for s := range doc.Network {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	var out []OSEntry
	for _, section := range sections {
		node := doc.Network[section]
		if node.Kind != yaml.MappingNode {
			continue // version, renderer
		}
		var devs map[string]struct {
			Match *struct {
				Name       string `yaml:"name"`
				MACAddress string `yaml:"macaddress"`
			} `yaml:"match"`
			Interfaces []string `yaml:"interfaces"`
			Link       string   `yaml:"link"`
		}
		if err := node.Decode(&devs); err != nil {
			return nil, notNetplan(err, "network."+section)
		}
		var ids []string
		for id := range devs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			d := devs[id]
			e := OSEntry{Manager: ManagerNetplan, File: file, Section: section, ID: id, Name: id}
			if d.Match != nil {
				e.Name, e.MAC = d.Match.Name, d.Match.MACAddress
			}
			out = append(out, e)
			for _, port := range d.Interfaces {
				out = append(out, OSEntry{Manager: ManagerNetplan, File: file, Section: section, ID: id, Name: port, Member: true})
			}
			if d.Link != "" {
				out = append(out, OSEntry{Manager: ManagerNetplan, File: file, Section: section, ID: id, Name: d.Link, Member: true})
			}
		}
	}
	return out, nil
}

// yamlLine is where yaml.v3 says the problem is ("yaml: line 3: ...", "line 3: cannot unmarshal ...").
var yamlLine = regexp.MustCompile(`\bline (\d+)\b`)

// notNetplan is the error of a file that is not valid netplan YAML: where (the section, the line yaml.v3
// names), never yaml.v3's own text, which quotes the offending scalar and so a piece of the file
// ("cannot unmarshal !!str `secret` into ...", P3-M12-07: no content leaves the executor).
func notNetplan(err error, where string) error {
	var at []string
	if where != "" {
		at = append(at, where)
	}
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		at = append(at, "line "+m[1])
	}
	if len(at) == 0 {
		return errors.New("not valid netplan YAML")
	}
	return fmt.Errorf("not valid netplan YAML (%s)", strings.Join(at, ", "))
}

// nmEntry reads a NetworkManager keyfile: the connection's id and the interface it is bound to, by name
// (`interface-name`) or by MAC (`mac-address` of the [ethernet] section). A connection bound to neither,
// or a file that is no keyfile, is not reported.
func nmEntry(raw []byte, file string) (OSEntry, bool) {
	e := OSEntry{Manager: ManagerNetworkManager, File: file}
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
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case section == "connection" && k == "id":
			e.ID = v
		case section == "connection" && k == "interface-name":
			e.Name = v
		case (section == "ethernet" || section == "802-3-ethernet") && k == "mac-address":
			e.MAC = strings.TrimSuffix(v, ";")
		}
	}
	if e.ID == "" {
		e.ID = strings.TrimSuffix(path.Base(file), ".nmconnection")
	}
	return e, e.Name != "" || e.MAC != ""
}

// InterfaceCheck is the preflight's verdict on one host interface that is a test interface (assigned) or
// may become one: "ok" when the operating system does not configure it, otherwise "fail" for an assigned
// one and "warn" for a candidate, with the entries that configure it and what to remove (P3-M12-07).
func InterfaceCheck(name, mac string, assigned bool, entries []OSEntry) (status, message, fix string) {
	var where, fixes []string
	for _, e := range entries {
		if e.Matches(name, mac) {
			where = append(where, e.Where())
			fixes = append(fixes, e.Fix(name))
		}
	}
	if len(where) == 0 {
		return "ok", name + " carries no network configuration of the operating system", ""
	}
	fix = strings.Join(fixes, "; ")
	if assigned {
		return "fail", fmt.Sprintf("%s is assigned to a test network but configured by %s", name, strings.Join(where, " and by ")), fix
	}
	return "warn", fmt.Sprintf("%s is configured by %s; a test interface must not be", name, strings.Join(where, " and by ")), fix
}
