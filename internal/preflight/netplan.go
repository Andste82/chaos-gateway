package preflight

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
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
//
// NetworkManager's own configuration (NetworkManager.conf and its conf.d directories) says which devices it
// leaves alone (`unmanaged-devices` under [keyfile]; the values of all files are taken together). What
// NetworkManager does by its own defaults, outside these files, is not seen: a NetworkManager that manages
// every ethernet port without netplan's NetworkManager renderer and without a keyfile (an auto-default
// "Wired connection N" has no file) is not reported.
var (
	NetplanDirs = []string{"etc/netplan", "run/netplan", "lib/netplan"}
	NMDirs      = []string{"etc/NetworkManager/system-connections", "run/NetworkManager/system-connections"}
	NMConfFiles = []string{"etc/NetworkManager/NetworkManager.conf"}
	NMConfDirs  = []string{"usr/lib/NetworkManager/conf.d", "run/NetworkManager/conf.d", "etc/NetworkManager/conf.d"}
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
	// All is set when the entry hands every interface to NetworkManager: an ethernet connection of
	// NetworkManager bound to no interface (it may come up on any ethernet port), or netplan's NetworkManager
	// renderer for all devices (Section "renderer", ID the renderer; NetworkManager then manages every device
	// and brings up a port without a connection of its own with DHCP).
	All bool `json:"all,omitempty"`
	// Unmanaged is set for a NetworkManager setting (`unmanaged-devices` under [keyfile], File the
	// configuration file) that leaves the interfaces Name or MAC selects alone: it configures nothing, and
	// for those interfaces Configuring leaves out NetworkManager's entries and the All entries.
	Unmanaged bool `json:"unmanaged,omitempty"`
}

// Matches reports whether the entry, on its own, configures the interface with this name and MAC address
// (an Unmanaged entry configures nothing; Configuring takes it into account).
func (e OSEntry) Matches(name, mac string) bool {
	return !e.Unmanaged && e.selects(name, mac)
}

// selects reports whether the entry names the interface with this name and MAC address: by its name
// glob and MAC, or All.
func (e OSEntry) selects(name, mac string) bool {
	if e.All {
		return true
	}
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
	switch {
	case e.All && e.Manager == ManagerNetworkManager:
		return fmt.Sprintf("NetworkManager (connection %q, %s, bound to no interface)", e.ID, e.File)
	case e.All:
		return fmt.Sprintf("netplan (%s, renderer %q for all devices)", e.File, e.ID)
	case e.Manager == ManagerNetworkManager:
		return fmt.Sprintf("NetworkManager (connection %q, %s)", e.ID, e.File)
	}
	return fmt.Sprintf("netplan (%s, %q under %q)", e.File, e.ID, e.Section)
}

// Fix says what to remove so that the operating system leaves the interface alone.
func (e OSEntry) Fix(iface string) string {
	switch {
	case e.All:
		return fmt.Sprintf(`keep NetworkManager off %s: add "interface-name:%s" to "unmanaged-devices=" under "[keyfile]" in /etc/NetworkManager/conf.d/99-chaos-gateway.conf (entries separated by ";"), then run "sudo systemctl reload NetworkManager"`, iface, iface)
	case e.Manager == ManagerNetworkManager:
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
	// netplan's global renderer: the files are merged in the order of their names (a file in /etc hides
	// one of the same name in /run and /lib), and a later "renderer" wins.
	type rendered struct{ file, renderer string }
	renderers := map[string]rendered{}
	for _, dir := range NetplanDirs {
		for _, f := range files(fsys, dir, ".yaml", ".yml") {
			raw, err := fs.ReadFile(fsys, f)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			es, renderer, err := netplanEntries(raw, "/"+f)
			if err != nil {
				errs = append(errs, fmt.Errorf("/%s: %w", f, err))
				continue
			}
			entries = append(entries, es...)
			if _, hidden := renderers[path.Base(f)]; !hidden {
				renderers[path.Base(f)] = rendered{"/" + f, renderer}
			}
		}
	}
	var global rendered
	for _, name := range sortedKeys(renderers) {
		if r := renderers[name]; r.renderer != "" {
			global = r
		}
	}
	if strings.EqualFold(global.renderer, ManagerNetworkManager) {
		entries = append(entries, OSEntry{Manager: ManagerNetplan, File: global.file, Section: "renderer", ID: global.renderer, All: true})
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
	conf := append([]string(nil), NMConfFiles...)
	for _, dir := range NMConfDirs {
		conf = append(conf, files(fsys, dir, ".conf")...)
	}
	for _, f := range conf {
		raw, err := fs.ReadFile(fsys, f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		entries = append(entries, nmUnmanaged(raw, "/"+f)...)
	}
	return entries, errs
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Configuring returns the entries that configure the interface with this name and MAC address: those
// that select it, without NetworkManager's entries and the All entries when a NetworkManager setting
// leaves the interface alone (`unmanaged-devices`).
func Configuring(entries []OSEntry, name, mac string) []OSEntry {
	unmanaged := false
	for _, e := range entries {
		unmanaged = unmanaged || (e.Unmanaged && e.selects(name, mac))
	}
	var out []OSEntry
	for _, e := range entries {
		if !e.Matches(name, mac) || (unmanaged && (e.All || e.Manager == ManagerNetworkManager)) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// nmUnmanaged reads the devices a NetworkManager configuration file leaves alone: `unmanaged-devices`
// under [keyfile], its `interface-name:` and `mac:` specs (other kinds of spec, "except:" and "none" are
// not taken).
func nmUnmanaged(raw []byte, file string) []OSEntry {
	var out []OSEntry
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
		k = strings.TrimSuffix(strings.TrimSpace(k), "+") // unmanaged-devices+= adds to the list
		if !ok || section != "keyfile" || k != "unmanaged-devices" {
			continue
		}
		for _, spec := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == ',' }) {
			spec = strings.TrimSpace(spec)
			e := OSEntry{Manager: ManagerNetworkManager, File: file, Section: "unmanaged-devices", ID: spec, Unmanaged: true}
			switch {
			case strings.HasPrefix(spec, "interface-name:"):
				e.Name = strings.TrimPrefix(strings.TrimPrefix(spec, "interface-name:"), "=")
			case strings.HasPrefix(spec, "mac:"):
				e.MAC = strings.TrimPrefix(spec, "mac:")
			default:
				continue
			}
			if e.Name != "" || e.MAC != "" {
				out = append(out, e)
			}
		}
	}
	return out
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
// VLAN is on; renderer is the file's global renderer (`network.renderer`), empty without one.
func netplanEntries(raw []byte, file string) (out []OSEntry, renderer string, err error) {
	var doc struct {
		Network map[string]yaml.Node `yaml:"network"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, "", notNetplan(err, "")
	}
	var sections []string
	for s := range doc.Network {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	for _, section := range sections {
		node := doc.Network[section]
		if section == "renderer" && node.Kind == yaml.ScalarNode {
			renderer = node.Value
		}
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
			return nil, "", notNetplan(err, "network."+section)
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
	return out, renderer, nil
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
// (`interface-name`) or by MAC (`mac-address` of the [ethernet] section). An ethernet connection bound to
// neither that NetworkManager brings up by itself (autoconnect not false) may come up on any ethernet port:
// it is an All entry. Another connection bound to neither, or a file that is no keyfile, is not reported.
func nmEntry(raw []byte, file string) (OSEntry, bool) {
	e := OSEntry{Manager: ManagerNetworkManager, File: file}
	section, typ, autoconnect := "", "", true
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
		case section == "connection" && k == "type":
			typ = v
		case section == "connection" && k == "autoconnect":
			autoconnect = !strings.EqualFold(v, "false") && v != "0" && !strings.EqualFold(v, "no")
		case section == "connection" && k == "interface-name":
			e.Name = v
		case (section == "ethernet" || section == "802-3-ethernet") && k == "mac-address":
			e.MAC = strings.TrimSuffix(v, ";")
		}
	}
	if e.ID == "" {
		e.ID = strings.TrimSuffix(path.Base(file), ".nmconnection")
	}
	if e.Name == "" && e.MAC == "" && (typ == "ethernet" || typ == "802-3-ethernet") && autoconnect {
		e.All = true
	}
	return e, e.Name != "" || e.MAC != "" || e.All
}

// InterfaceCheck is the preflight's verdict on one host interface that is a test interface (assigned) or
// may become one: "ok" when the operating system does not configure it, otherwise "fail" for an assigned
// one and "warn" for a candidate, with the entries that configure it and what to remove (P3-M12-07).
func InterfaceCheck(name, mac string, assigned bool, entries []OSEntry) (status, message, fix string) {
	var where, fixes []string
	for _, e := range Configuring(entries, name, mac) {
		where = append(where, e.Where())
		if f := e.Fix(name); !slices.Contains(fixes, f) {
			fixes = append(fixes, f)
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
