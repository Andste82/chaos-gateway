package domain

import (
	"time"

	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// ---- settings, uplink, management -------------------------------------------------------

func (v *validator) settings() {
	s := v.cfg.Settings
	if s == nil {
		return
	}
	check := func(path string, d *model.Duration, min time.Duration) {
		if d == nil {
			return
		}
		got, ok := parseDuration(*d)
		if !ok || got < min {
			v.add(path, CodeInvalidDuration, "must be a duration of at least %v", min)
		}
	}
	check("/settings/commit_confirm_timeout", s.CommitConfirmTimeout, time.Second)
	check("/settings/counter_poll_interval", s.CounterPollInterval, 100*time.Millisecond)
	if s.Retention != nil {
		check("/settings/retention/runs", s.Retention.Runs, time.Hour)
		check("/settings/retention/events", s.Retention.Events, time.Hour)
		check("/settings/retention/audit", s.Retention.Audit, time.Hour)
	}
}

// interfaceRef is a host interface together with the place it is configured at.
type interfaceRef struct {
	path string
	role string
	ref  model.InterfaceRef
}

// sameInterface reports whether two references can name the same interface: the same MAC, or
// the same name when neither has a MAC to tell them apart.
func sameInterface(a, b model.InterfaceRef) bool {
	if a.Mac != nil && b.Mac != nil {
		return *a.Mac == *b.Mac
	}
	return a.Name != nil && b.Name != nil && *a.Name == *b.Name
}

func (v *validator) uplinkAndManagement() {
	cfg := v.cfg
	if cfg.Uplink.Gateway != nil {
		if a, ok := parseAddr(*cfg.Uplink.Gateway); !ok || a.IsUnspecified() || a.IsMulticast() {
			v.add("/uplink/gateway", CodeInvalidAddress, "must be a unicast IPv4 address")
		}
	}
	for i, s := range deref(cfg.Uplink.DnsUpstream) {
		if a, ok := parseAddr(s); !ok || a.IsUnspecified() || a.IsMulticast() {
			v.add(schema.Pointer("/uplink/dns_upstream", itoa(i)), CodeInvalidAddress, "must be a unicast IPv4 address")
		}
	}
	v.macCheck("/uplink/interface", cfg.Uplink.Interface.Mac)
	v.macCheck("/management/interface", cfg.Management.Interface.Mac)
	for i, s := range deref(cfg.Management.AllowedSources) {
		path := schema.Pointer("/management/allowed_sources", itoa(i))
		p, ok := v.cidrOK(path, s)
		if !ok {
			continue
		}
		for _, n := range sortedKeys(v.idx.Networks) {
			info := v.idx.Networks[n]
			// a WireGuard network with the role management is meant to reach the control plane
			if info.WG != nil && info.WG.Role != nil && *info.WG.Role == "management" {
				continue
			}
			for _, sub := range prefixesOf(info) {
				if overlaps(p, sub) {
					v.add(path, CodeManagementOverlap, "%s overlaps the test network %q (%s): its devices would reach the control plane", p, info.Name, sub)
				}
			}
		}
	}
}

func (v *validator) macCheck(path string, mac *model.MacAddress) {
	if mac == nil {
		return
	}
	hw, err := net.ParseMAC(*mac)
	if err != nil || len(hw) != 6 {
		v.add(path+"/mac", CodeInvalidMAC, "%q is not a MAC address", *mac)
		return
	}
	if hw[0]&1 != 0 {
		v.add(path+"/mac", CodeInvalidMAC, "%s is a multicast address; an interface has a unicast MAC", *mac)
	}
}

// networkSubnet returns the subnet of a network: the interface prefix of a local network, the
// tunnel subnet of a WireGuard network.
func networkSubnet(n *NetInfo) (netip.Prefix, bool) {
	var s string
	switch {
	case n.Lan != nil:
		s = n.Lan.Address
	case n.WG != nil:
		s = n.WG.Address
	}
	p, ok := parsePrefix(s)
	if !ok {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

// ---- networks ---------------------------------------------------------------------------

var hostnameRE = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func (v *validator) networks() {
	var ifaces []interfaceRef
	ifaces = append(ifaces,
		interfaceRef{"/uplink/interface", "uplink", v.cfg.Uplink.Interface},
		interfaceRef{"/management/interface", "management", v.cfg.Management.Interface})

	ports := map[int]string{} // WireGuard listen ports
	keys := map[string]string{}
	for _, id := range sortedKeys(v.idx.Networks) {
		n := v.idx.Networks[id]
		switch {
		case n.Lan != nil:
			v.lan(n, &ifaces)
		case n.WG != nil:
			v.wireguard(n, ports, keys)
		}
	}
	v.checkInterfaces(ifaces)
}

func (v *validator) checkInterfaces(ifaces []interfaceRef) {
	for i, a := range ifaces {
		for _, b := range ifaces[:i] {
			if !sameInterface(a.ref, b.ref) {
				continue
			}
			// the two-port topology: management may be the uplink interface
			if (a.role == "management" && b.role == "uplink") || (a.role == "uplink" && b.role == "management") {
				continue
			}
			v.add(a.path, CodeDuplicateInterface, "the interface is already used as %s at %s", b.role, b.path)
		}
	}
}

func (v *validator) lan(n *NetInfo, ifaces *[]interfaceRef) {
	lan := n.Lan
	addr, ok := parsePrefix(lan.Address)
	if !ok {
		v.add(n.Path+"/address", CodeInvalidAddress, "%q is not an IPv4 address with a prefix length", lan.Address)
		return
	}
	subnet := addr.Masked()
	switch {
	case addr.Bits() > 30:
		v.add(n.Path+"/address", CodeInvalidPrefixLength, "a test network needs a prefix length of at most 30, got /%d", addr.Bits())
	case !usableHost(subnet, addr.Addr()):
		v.add(n.Path+"/address", CodeInvalidAddress, "%s is the network or broadcast address of %s; give the gateway a host address", addr.Addr(), subnet)
	}
	v.register(n.Path+"/address", subnet, "network "+quote(lan.Name))

	for i, ref := range lan.Interfaces {
		path := schema.Pointer(n.Path+"/interfaces", itoa(i))
		v.macCheck(path, ref.Mac)
		*ifaces = append(*ifaces, interfaceRef{path, "a port of network " + quote(lan.Name), ref})
	}

	for i, r := range deref(lan.Routes) {
		path := schema.Pointer(n.Path+"/routes", itoa(i))
		if dst, ok := v.cidrOK(path+"/destination", r.Destination); ok {
			v.register(path+"/destination", dst, "downstream route of network "+quote(lan.Name))
		}
		via, ok := parseAddr(r.Via)
		if !ok || !usableHost(subnet, via) || via == addr.Addr() {
			v.add(path+"/via", CodeOutsideSubnet, "the router %s must be a host address inside %s other than the gateway", r.Via, subnet)
		}
	}
	v.dhcp(n, subnet, addr.Addr())
	v.networkDNS(n)
}

func (v *validator) dhcp(n *NetInfo, subnet netip.Prefix, gateway netip.Addr) {
	d := n.Lan.Dhcp
	if d == nil {
		return
	}
	base := n.Path + "/dhcp"
	if d.LeaseTime != nil {
		if lt, ok := parseDuration(*d.LeaseTime); !ok || lt < time.Second {
			v.add(base+"/lease_time", CodeInvalidDuration, "the lease time must be at least 1s")
		}
	}
	type pool struct{ from, to netip.Addr }
	var pools []pool
	for i, p := range deref(d.Pools) {
		path := schema.Pointer(base+"/pools", itoa(i))
		from, ok1 := parseAddr(p.Start)
		to, ok2 := parseAddr(p.End)
		if !ok1 || !ok2 {
			v.add(path, CodeInvalidAddress, "start and end must be IPv4 addresses")
			continue
		}
		if to.Less(from) {
			v.add(path+"/end", CodePoolOrder, "the end %s is before the start %s", to, from)
			continue
		}
		for _, e := range []struct {
			field string
			a     netip.Addr
		}{{"start", from}, {"end", to}} {
			if !usableHost(subnet, e.a) {
				v.add(path+"/"+e.field, CodeOutsideSubnet, "%s is not a host address of %s", e.a, subnet)
			}
		}
		if !gateway.Less(from) && !to.Less(gateway) {
			v.add(path, CodeInvalidAddress, "the pool contains the gateway address %s", gateway)
		}
		for j, o := range pools {
			if !to.Less(o.from) && !o.to.Less(from) {
				v.add(path, CodePoolOverlap, "the pool overlaps pool %d", j)
			}
		}
		pools = append(pools, pool{from, to})
	}
	v.dhcpOptions(base+"/options", d.Options)
}

func (v *validator) dhcpOptions(path string, o *model.DhcpOptions) {
	if o == nil {
		return
	}
	checkAddr := func(p, s string) {
		if a, ok := parseAddr(s); !ok || a.IsUnspecified() || a.IsMulticast() {
			v.add(p, CodeInvalidAddress, "must be a unicast IPv4 address")
		}
	}
	if o.Router != nil {
		checkAddr(path+"/router", *o.Router)
	}
	for i, s := range deref(o.DnsServers) {
		checkAddr(schema.Pointer(path+"/dns_servers", itoa(i)), s)
	}
	for i, s := range deref(o.NtpServers) {
		checkAddr(schema.Pointer(path+"/ntp_servers", itoa(i)), s)
	}
	seen := map[int]bool{}
	for i, c := range deref(o.Custom) {
		p := schema.Pointer(path+"/custom", itoa(i)) + "/code"
		if seen[c.Code] {
			v.add(p, CodeDuplicateIdentifier, "option %d is set twice", c.Code)
		}
		seen[c.Code] = true
		if reservedDHCPOptions[c.Code] {
			v.add(p, CodeReservedOption, "option %d is managed by the gateway and cannot be set as a custom option", c.Code)
		}
	}
}

func (v *validator) networkDNS(n *NetInfo) {
	d := n.Lan.Dns
	if d == nil {
		return
	}
	seen := map[string]bool{}
	for i, e := range deref(d.StaticEntries) {
		path := schema.Pointer(n.Path+"/dns/static_entries", itoa(i))
		name := lower(e.Name)
		if seen[name] {
			v.add(path+"/name", CodeDuplicateName, "%q has two static entries", e.Name)
		}
		seen[name] = true
		for j, a := range e.Addresses {
			if _, ok := parseAddr(a); !ok {
				v.add(schema.Pointer(path+"/addresses", itoa(j)), CodeInvalidAddress, "must be an IPv4 address")
			}
		}
	}
}

// ---- WireGuard --------------------------------------------------------------------------

func (v *validator) endpoint(path, ep string) {
	host, portStr, ok := strings.Cut(ep, ":")
	port, err := strconv.Atoi(portStr)
	if !ok || err != nil || port < 1 || port > 65535 {
		v.add(path, CodeInvalidEndpoint, "%q must be host:port with a port from 1 to 65535", ep)
		return
	}
	if net.ParseIP(host) != nil {
		if _, ok := parseAddr(host); !ok {
			v.add(path, CodeInvalidEndpoint, "%q: only IPv4 addresses are supported", host)
		}
		return
	}
	// all digits and dots but not an address: a malformed IPv4 address, not a name
	if strings.Trim(host, "0123456789.") == "" || !hostnameRE.MatchString(host) {
		v.add(path, CodeInvalidEndpoint, "%q is neither an IPv4 address nor a host name", host)
	}
}

func (v *validator) wireguard(n *NetInfo, ports map[int]string, pubKeys map[string]string) {
	wg := n.WG
	addr, ok := parsePrefix(wg.Address)
	if !ok {
		v.add(n.Path+"/address", CodeInvalidAddress, "%q is not an IPv4 address with a prefix length", wg.Address)
		return
	}
	subnet := addr.Masked()
	v.register(n.Path+"/address", subnet, "WireGuard network "+quote(wg.Name))

	if other, taken := ports[wg.ListenPort]; taken {
		v.add(n.Path+"/listen_port", CodeDuplicatePort, "UDP port %d is already used by %s", wg.ListenPort, other)
	}
	ports[wg.ListenPort] = quote(wg.Name)
	if wg.Endpoint != nil {
		v.endpoint(n.Path+"/endpoint", *wg.Endpoint)
	}

	switch {
	case n.IsHub():
		if addr.Bits() > 30 {
			v.add(n.Path+"/address", CodeInvalidPrefixLength, "a hub needs a prefix length of at most 30, got /%d", addr.Bits())
		} else if !usableHost(subnet, addr.Addr()) {
			v.add(n.Path+"/address", CodeInvalidAddress, "%s is the network or broadcast address of %s", addr.Addr(), subnet)
		}
		if wg.Peer != nil {
			v.add(n.Path+"/peer", CodeWrongKind, "a hub has clients, not a peer")
		}
		if wg.Routes != nil {
			v.add(n.Path+"/routes", CodeWrongKind, "routes belong to a link; a hub routes the networks of its clients")
		}
		v.hubClients(n, subnet, addr.Addr(), pubKeys)
	case n.IsLink():
		if addr.Bits() != 31 {
			v.add(n.Path+"/address", CodeInvalidPrefixLength, "a link needs a /31 transfer network, got /%d", addr.Bits())
		}
		if wg.Clients != nil {
			v.add(n.Path+"/clients", CodeWrongKind, "a link has one peer, not clients")
		}
		v.linkPeer(n, subnet, addr.Addr(), pubKeys)
		for i, r := range deref(wg.Routes) {
			path := schema.Pointer(n.Path+"/routes", itoa(i))
			if p, ok := v.cidrOK(path, r); ok {
				v.register(path, p, "route via link "+quote(wg.Name))
			}
		}
	}
}

func (v *validator) hubClients(n *NetInfo, subnet netip.Prefix, gateway netip.Addr, pubKeys map[string]string) {
	used := map[netip.Addr]string{gateway: "the hub itself"}
	for _, id := range sortedKeys(deref(n.WG.Clients)) {
		c := deref(n.WG.Clients)[id]
		path := schema.Pointer(n.Path+"/clients", id)
		if a, ok := parseAddr(c.Address); !ok || !usableHost(subnet, a) {
			v.add(path+"/address", CodeOutsideSubnet, "%s must be a host address of %s", c.Address, subnet)
		} else if other, dup := used[a]; dup {
			v.add(path+"/address", CodeDuplicateAddress, "%s is already used by %s", a, other)
		} else {
			used[a] = "client " + quote(c.Name)
		}
		for i, cn := range deref(c.ClientNetworks) {
			cpath := schema.Pointer(path+"/client_networks", itoa(i))
			if p, ok := v.cidrOK(cpath, cn); ok {
				v.register(cpath, p, "client network of "+quote(c.Name))
			}
		}
		if c.Keepalive != nil {
			if d, ok := parseDuration(*c.Keepalive); !ok || d < 0 || d > 65535*time.Second {
				v.add(path+"/keepalive", CodeInvalidDuration, "the keepalive must be between 0s and 65535s")
			}
		}
		v.clientDNS(path, c.Dns)
		v.keySettings(path+"/key", c.Key, pubKeys)
	}
}

func (v *validator) clientDNS(path string, d *model.WireGuardClientDns) {
	if d == nil {
		return
	}
	custom := d.Mode != nil && *d.Mode == "custom"
	servers := deref(d.Servers)
	switch {
	case custom && len(servers) == 0:
		v.add(path+"/dns/servers", CodeMissingField, "mode custom needs at least one server")
	case !custom && len(servers) > 0:
		v.add(path+"/dns/servers", CodeUnexpectedField, "servers are only used with mode custom")
	}
	for i, s := range servers {
		if a, ok := parseAddr(s); !ok || a.IsUnspecified() || a.IsMulticast() {
			v.add(schema.Pointer(path+"/dns/servers", itoa(i)), CodeInvalidAddress, "must be a unicast IPv4 address")
		}
	}
}

func (v *validator) keySettings(path string, k *model.WireGuardKeySettings, pubKeys map[string]string) {
	if k == nil {
		return
	}
	provided := k.Mode != nil && *k.Mode == "provided"
	if provided && k.PublicKey == nil {
		v.add(path+"/public_key", schemaRequired, "the mode provided needs the client's public key")
	}
	if provided && k.ExportOnce != nil && *k.ExportOnce {
		v.add(path+"/export_once", CodeKeySettings, "export_once needs generated keys: with provided keys the gateway never holds a private key")
	}
	if provided && k.Generation != nil && *k.Generation > 1 {
		v.add(path+"/generation", CodeKeySettings, "key rotation is only possible for generated keys")
	}
	if k.PublicKey != nil {
		if other, dup := pubKeys[*k.PublicKey]; dup {
			v.add(path+"/public_key", CodeDuplicatePublicKey, "the public key is already used by %s", other)
		} else {
			pubKeys[*k.PublicKey] = path
		}
	}
}

const schemaRequired = "required"

func (v *validator) linkPeer(n *NetInfo, subnet netip.Prefix, gateway netip.Addr, pubKeys map[string]string) {
	p := n.WG.Peer
	if p == nil {
		v.add(n.Path+"/peer", schemaRequired, "a link needs its peer")
		return
	}
	path := n.Path + "/peer"
	a, ok := parseAddr(p.Address)
	switch {
	case !ok || !subnet.Contains(a):
		v.add(path+"/address", CodeOutsideSubnet, "the peer address %s must be in the transfer network %s", p.Address, subnet)
	case a == gateway:
		v.add(path+"/address", CodeDuplicateAddress, "the peer address is the gateway's own address")
	}
	if p.Endpoint != nil {
		v.endpoint(path+"/endpoint", *p.Endpoint)
	}
	if p.Keepalive != nil {
		if d, ok := parseDuration(*p.Keepalive); !ok || d < 0 || d > 65535*time.Second {
			v.add(path+"/keepalive", CodeInvalidDuration, "the keepalive must be between 0s and 65535s")
		}
	}
	v.keySettings(path+"/key", p.Key, pubKeys)
}
