package domain

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// Kind says which namespace a reference points into.
type Kind int

const (
	// KindNetwork is a local or WireGuard network.
	KindNetwork Kind = iota
	// KindDevice is the device namespace: configured devices, WireGuard clients and probes share
	// one UUID and one name namespace (api conventions).
	KindDevice
	KindGroup
	// KindClient is a WireGuard client (a device that lives in a hub network).
	KindClient
	// KindLink is a WireGuard network of kind link.
	KindLink
	// KindProfile is a built-in or a configured profile.
	KindProfile

	// namespaces that only need unique names, nobody refers to them by name inside a configuration
	kindScenario
	kindProtocol
	kindFault
	kindAccessRule
	kindCount
)

func (k Kind) String() string {
	return [...]string{"network", "device", "group", "client", "link", "profile", "scenario", "protocol", "fault", "access rule"}[k]
}

// NetInfo describes one network of a configuration.
type NetInfo struct {
	ID, Name string
	Path     string // JSON pointer of the network
	Lan      *model.LanNetwork
	WG       *model.WireGuardNetwork
}

// IsLan reports whether the network is a local test network.
func (n *NetInfo) IsLan() bool { return n.Lan != nil }

// IsHub reports whether the network is a WireGuard hub.
func (n *NetInfo) IsHub() bool { return n.WG != nil && n.WG.Kind == "hub" }

// IsLink reports whether the network is a WireGuard link.
func (n *NetInfo) IsLink() bool { return n.WG != nil && n.WG.Kind == "link" }

// DeviceOrigin says where a member of the device namespace comes from.
type DeviceOrigin string

const (
	OriginConfigured DeviceOrigin = "configured"
	OriginClient     DeviceOrigin = "wireguard_client"
	OriginProbe      DeviceOrigin = "probe"
)

// DeviceInfo is a member of the device namespace.
type DeviceInfo struct {
	ID, Name string
	Origin   DeviceOrigin
	Path     string
	// Network is the ID of the network the device lives in: the hub for a client, the network of
	// a probe, the configured network of a device (empty when unknown).
	Network string
	// Device is set for configured devices, Client for WireGuard clients, Probe for probes.
	Device *model.Device
	Client *model.WireGuardClient
	Probe  *model.Probe
}

// Index answers "what does this name or UUID refer to" for one configuration. It is built by
// BuildIndex and read-only afterwards.
type Index struct {
	Networks  map[string]*NetInfo
	Devices   map[string]*DeviceInfo
	Groups    map[string]string // ID → name
	Profiles  map[string]string // ID → name, including the built-in profiles
	Scenarios map[string]string
	Protocols map[string]string
	// Discovered are the UUIDs of discovered devices that are not configured (WithDiscovered).
	Discovered map[string]bool

	names [kindCount]map[string]string // per Kind: lower-case name → ID
}

// Resolve turns a reference (UUID or name) into the UUID of an existing object of that kind.
func (x *Index) Resolve(kind Kind, ref string) (string, bool) {
	if id, err := uuid.Parse(ref); err == nil && len(ref) == 36 {
		canonical := strings.ToLower(id.String())
		if x.exists(kind, canonical) {
			return canonical, true
		}
		return "", false
	}
	// clients are devices and links are networks: their names live in those namespaces
	ns := kind
	switch kind {
	case KindClient:
		ns = KindDevice
	case KindLink:
		ns = KindNetwork
	}
	id, ok := x.names[ns][strings.ToLower(ref)]
	if !ok || !x.exists(kind, id) {
		return "", false
	}
	return id, true
}

func (x *Index) exists(kind Kind, id string) bool {
	switch kind {
	case KindNetwork:
		_, ok := x.Networks[id]
		return ok
	case KindDevice:
		_, ok := x.Devices[id]
		return ok || x.Discovered[id]
	case KindGroup:
		_, ok := x.Groups[id]
		return ok
	case KindClient:
		d, ok := x.Devices[id]
		return ok && d.Origin == OriginClient
	case KindLink:
		n, ok := x.Networks[id]
		return ok && n.IsLink()
	case KindProfile:
		_, ok := x.Profiles[id]
		return ok
	}
	return false
}

// NameOf returns the name of an object, or the ID when it is unknown.
func (x *Index) NameOf(kind Kind, id string) string {
	switch kind {
	case KindNetwork, KindLink:
		if n, ok := x.Networks[id]; ok {
			return n.Name
		}
	case KindDevice, KindClient:
		if d, ok := x.Devices[id]; ok {
			return d.Name
		}
	case KindGroup:
		if n, ok := x.Groups[id]; ok {
			return n
		}
	case KindProfile:
		if n, ok := x.Profiles[id]; ok {
			return n
		}
	}
	return id
}

// Error codes of the structural checks done while the index is built.
const (
	CodeInvalidID        = "invalid_id"
	CodeDuplicateID      = "duplicate_id"
	CodeDuplicateName    = "duplicate_name"
	CodeNameIsUUID       = "name_is_uuid"
	CodeReservedName     = "reserved_name"
	CodeUnknownReference = "unknown_reference"
	CodeWrongReference   = "wrong_reference"
	CodeInvalidNetwork   = "invalid_network"
)

// canonicalID reports whether key is a UUID in the canonical lower-case form.
func canonicalID(key string) bool {
	id, err := uuid.Parse(key)
	return err == nil && len(key) == 36 && strings.ToLower(id.String()) == key
}

type indexBuilder struct {
	x    *Index
	errs []model.ValidationError
}

func (b *indexBuilder) add(path, code, format string, args ...any) {
	b.errs = append(b.errs, model.ValidationError{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

// claim registers a name in a namespace and reports duplicates and names that look like UUIDs.
func (b *indexBuilder) claim(kind Kind, path, id, name string) {
	if name == "" {
		return
	}
	if _, err := uuid.Parse(name); err == nil {
		b.add(path+"/name", CodeNameIsUUID, "a name must not have the form of a UUID")
		return
	}
	lower := strings.ToLower(name)
	if other, taken := b.x.names[kind][lower]; taken && other != id {
		b.add(path+"/name", CodeDuplicateName, "the name %q is already used (names are unique per kind, ignoring case)", name)
		return
	}
	b.x.names[kind][lower] = id
}

// BuildIndex indexes the named objects of a configuration. It reports malformed keys, duplicate
// IDs in the device namespace, duplicate names and names that look like UUIDs; the index is
// usable even when errors are returned.
func BuildIndex(cfg *model.Configuration) (*Index, []model.ValidationError) {
	x := &Index{
		Networks: map[string]*NetInfo{}, Devices: map[string]*DeviceInfo{}, Groups: map[string]string{},
		Profiles: map[string]string{}, Scenarios: map[string]string{}, Protocols: map[string]string{},
	}
	for i := range x.names {
		x.names[i] = map[string]string{}
	}
	b := &indexBuilder{x: x}

	// built-in profiles come first: their names are reserved
	for _, p := range BuiltinProfiles() {
		x.Profiles[p.ID] = p.Profile.Name
		x.names[KindProfile][strings.ToLower(p.Profile.Name)] = p.ID
	}

	checkKey := func(path, key string) bool {
		if canonicalID(key) {
			return true
		}
		b.add(path, CodeInvalidID, "the key must be a UUID in lower-case canonical form")
		return false
	}

	for _, key := range sortedKeys(deref(cfg.Networks)) {
		path := schema.Pointer("/networks", key)
		if !checkKey(path, key) {
			continue
		}
		n := deref(cfg.Networks)[key]
		info := &NetInfo{ID: key, Path: path}
		disc, _ := n.Discriminator()
		switch disc {
		case "lan":
			lan, err := n.AsLanNetwork()
			if err != nil {
				b.add(path, CodeInvalidNetwork, "cannot read the network: %v", err)
				continue
			}
			info.Name, info.Lan = lan.Name, &lan
		case "wireguard":
			wg, err := n.AsWireGuardNetwork()
			if err != nil {
				b.add(path, CodeInvalidNetwork, "cannot read the network: %v", err)
				continue
			}
			info.Name, info.WG = wg.Name, &wg
		default:
			b.add(path+"/type", CodeInvalidNetwork, "the network type must be lan or wireguard")
			continue
		}
		x.Networks[key] = info
		b.claim(KindNetwork, path, key, info.Name)
	}

	claimDevice := func(d *DeviceInfo) {
		if other, dup := x.Devices[d.ID]; dup {
			b.add(d.Path, CodeDuplicateID, "the UUID is already used by %s (devices, WireGuard clients and probes share one namespace)", other.Path)
			return
		}
		x.Devices[d.ID] = d
		b.claim(KindDevice, d.Path, d.ID, d.Name)
	}
	for _, key := range sortedKeys(deref(cfg.Devices)) {
		path := schema.Pointer("/devices", key)
		if !checkKey(path, key) {
			continue
		}
		d := deref(cfg.Devices)[key]
		dev := d
		info := &DeviceInfo{ID: key, Name: d.Name, Origin: OriginConfigured, Path: path, Device: &dev}
		if d.Network != nil {
			info.Network = *d.Network
		}
		claimDevice(info)
	}
	for _, key := range sortedKeys(x.Networks) {
		n := x.Networks[key]
		if !n.IsHub() {
			continue
		}
		for _, ck := range sortedKeys(deref(n.WG.Clients)) {
			path := schema.Pointer(schema.Pointer(n.Path, "clients"), ck)
			if !checkKey(path, ck) {
				continue
			}
			c := deref(n.WG.Clients)[ck]
			client := c
			claimDevice(&DeviceInfo{ID: ck, Name: c.Name, Origin: OriginClient, Path: path, Network: key, Client: &client})
		}
	}
	for _, key := range sortedKeys(deref(cfg.Probes)) {
		path := schema.Pointer("/probes", key)
		if !checkKey(path, key) {
			continue
		}
		p := deref(cfg.Probes)[key]
		probe := p
		claimDevice(&DeviceInfo{ID: key, Name: p.Name, Origin: OriginProbe, Path: path, Network: p.Network, Probe: &probe})
	}

	for _, key := range sortedKeys(deref(cfg.Groups)) {
		path := schema.Pointer("/groups", key)
		if checkKey(path, key) {
			g := deref(cfg.Groups)[key]
			x.Groups[key] = g.Name
			b.claim(KindGroup, path, key, g.Name)
		}
	}
	for _, key := range sortedKeys(deref(cfg.Profiles)) {
		path := schema.Pointer("/profiles", key)
		if !checkKey(path, key) {
			continue
		}
		p := deref(cfg.Profiles)[key]
		if IsBuiltinProfileID(key) {
			// a configured profile with a built-in UUID would shadow it (the lookup tries configured profiles first)
			b.add(path, CodeReservedName, "%s is the UUID of a built-in profile", key)
			continue
		}
		if reserved, ok := x.names[KindProfile][strings.ToLower(p.Name)]; ok && IsBuiltinProfileID(reserved) {
			b.add(path+"/name", CodeReservedName, "%q is the name of a built-in profile", p.Name)
			continue
		}
		x.Profiles[key] = p.Name
		b.claim(KindProfile, path, key, p.Name)
	}
	for _, key := range sortedKeys(deref(cfg.Scenarios)) {
		path := schema.Pointer("/scenarios", key)
		if checkKey(path, key) {
			s := deref(cfg.Scenarios)[key]
			x.Scenarios[key] = s.Name
			b.claim(kindScenario, path, key, s.Name)
		}
	}
	if cfg.Routing != nil {
		for _, key := range sortedKeys(deref(cfg.Routing.Protocols)) {
			path := schema.Pointer("/routing/protocols", key)
			if checkKey(path, key) {
				p := deref(cfg.Routing.Protocols)[key]
				x.Protocols[key] = p.Name
				b.claim(kindProtocol, path, key, p.Name)
			}
		}
	}
	for _, key := range sortedKeys(deref(cfg.Faults)) {
		path := schema.Pointer("/faults", key)
		if checkKey(path, key) {
			if f := deref(cfg.Faults)[key]; f.Name != nil {
				b.claim(kindFault, path, key, *f.Name)
			}
		}
	}
	for _, key := range sortedKeys(deref(cfg.AccessRules)) {
		path := schema.Pointer("/access_rules", key)
		if checkKey(path, key) {
			if r := deref(cfg.AccessRules)[key]; r.Name != nil {
				b.claim(kindAccessRule, path, key, *r.Name)
			}
		}
	}
	return x, b.errs
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
