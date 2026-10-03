package bird

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Ident turns a name into a BIRD identifier.
func Ident(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "p_" + out
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// Idle is the configuration of an instance with nothing to do: no protocol, and the kernel table the
// instance exports to. The executor writes it when it starts, so BIRD has a file to start from, and
// apply writes it when routing is switched off.
func Idle(kernelTable int) string {
	t, err := Config{RouterID: "127.0.0.1", KernelTable: kernelTable}.Render()
	if err != nil {
		panic(err) // a constant configuration
	}
	return t
}

// Check validates a Config before it is rendered: everything that ends up in the text is an
// identifier, an address, a prefix or a number, and the custom snippets pass the lexical checks.
func (c Config) Check() error {
	if a, err := netip.ParseAddr(c.RouterID); err != nil || !a.Is4() || a.IsUnspecified() {
		return fmt.Errorf("router id %q is not an IPv4 address", c.RouterID)
	}
	if c.KernelTable < 1 || c.KernelTable > 252 {
		return fmt.Errorf("kernel table %d out of range", c.KernelTable)
	}
	for _, p := range c.Protected {
		if _, err := netip.ParsePrefix(p); err != nil {
			return fmt.Errorf("protected prefix %q: %w", p, err)
		}
	}
	seen := map[string]bool{}
	for _, p := range c.Protocols {
		if !identRE.MatchString(p.Name) || seen[p.Name] {
			return fmt.Errorf("protocol name %q is not unique or not an identifier", p.Name)
		}
		seen[p.Name] = true
		if err := p.check(c); err != nil {
			return fmt.Errorf("protocol %s: %w", p.Name, err)
		}
	}
	if c.External != nil {
		if c.External.Table < 1 || c.External.Table > 252 || c.External.Table == c.KernelTable {
			return fmt.Errorf("external table %d is not usable", c.External.Table)
		}
		if err := c.External.Import.check(); err != nil {
			return fmt.Errorf("external import: %w", err)
		}
	}
	return nil
}

var ifaceRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,14}$`)

func (p Protocol) check(c Config) error {
	if !ifaceRE.MatchString(p.Interface) {
		return fmt.Errorf("invalid interface %q", p.Interface)
	}
	for _, a := range []string{p.LocalAddress, p.NeighborAddress} {
		if x, err := netip.ParseAddr(a); err != nil || !x.Is4() {
			return fmt.Errorf("%q is not an IPv4 address", a)
		}
	}
	for _, a := range p.Announce {
		if pf, err := netip.ParsePrefix(a); err != nil || !pf.Addr().Is4() || pf.Masked() != pf {
			return fmt.Errorf("announced prefix %q is not an IPv4 network prefix", a)
		}
	}
	if err := p.Import.check(); err != nil {
		return err
	}
	switch p.Type {
	case "bgp":
		if p.BGP == nil || p.BGP.NeighborASN < 1 || p.BGP.NeighborASN > 4294967295 || c.ASN < 1 || c.ASN > 4294967295 {
			return errors.New("bgp needs the local and the neighbor AS")
		}
		if p.BGP.HoldTime < 3 || p.BGP.KeepaliveTime < 1 || p.BGP.HoldTime < 3*p.BGP.KeepaliveTime {
			return errors.New("the hold time must be at least three keepalive intervals")
		}
	case "ospf":
		if p.OSPF == nil || !areaRE.MatchString(p.OSPF.Area) || p.OSPF.HelloInterval < 1 || p.OSPF.DeadInterval <= p.OSPF.HelloInterval {
			return errors.New("invalid ospf settings")
		}
		if p.OSPF.Cost < 0 || p.OSPF.Cost > 65535 {
			return errors.New("invalid ospf cost")
		}
	case "babel":
		if p.Babel == nil || p.Babel.HelloInterval < 1 || p.Babel.RxCost < 0 || p.Babel.RxCost > 65535 {
			return errors.New("invalid babel settings")
		}
	default:
		return fmt.Errorf("unknown protocol type %q", p.Type)
	}
	if p.Custom != "" {
		if err := CheckSnippet(p.Custom); err != nil {
			return fmt.Errorf("custom snippet: %w", err)
		}
	}
	return nil
}

var areaRE = regexp.MustCompile(`^([0-9]{1,10}|[0-9]{1,3}(\.[0-9]{1,3}){3})$`)

func (i Import) check() error {
	if i.MaxPrefixes < 0 || i.MaxPrefixes > 10000000 {
		return errors.New("invalid maximum prefix count")
	}
	for _, a := range i.Allowed {
		pf, err := netip.ParsePrefix(a.Prefix)
		if err != nil || !pf.Addr().Is4() || pf.Masked() != pf {
			return fmt.Errorf("allowed prefix %q is not an IPv4 network prefix", a.Prefix)
		}
		if a.MaxLength != 0 && (a.MaxLength < pf.Bits() || a.MaxLength > 32) {
			return fmt.Errorf("allowed prefix %q: max length %d out of range", a.Prefix, a.MaxLength)
		}
	}
	return nil
}

// Render generates the BIRD configuration. The text is deterministic: the same Config gives the
// same bytes, so the file's hash tells whether the running configuration is the target.
func (c Config) Render() (string, error) {
	if err := c.Check(); err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("# Generated by Chaos Gateway. Do not edit: the file is rewritten at every apply.\n")
	w("router id %s;\n\n", c.RouterID)
	w("protocol device { scan time 10; }\n\n")

	sources := map[string]bool{}
	for _, p := range c.Protocols {
		switch p.Type {
		case "bgp":
			sources["RTS_BGP"] = true
		case "ospf":
			for _, s := range []string{"RTS_OSPF", "RTS_OSPF_IA", "RTS_OSPF_EXT1", "RTS_OSPF_EXT2"} {
				sources[s] = true
			}
		case "babel":
			sources["RTS_BABEL"] = true
		}
	}
	if c.External != nil {
		sources["RTS_PIPE"] = true
	}
	var src []string
	for s := range sources {
		src = append(src, s)
	}
	sort.Strings(src)
	export := "export none;"
	if len(src) > 0 {
		export = "export where source ~ [ " + strings.Join(src, ", ") + " ];"
	}
	// the only place learned routes go: Chaos Gateway's table, never the main table
	w("# learned routes are exported into table %d only\n", c.KernelTable)
	w("protocol kernel gw_table {\n  kernel table %d;\n  learn off;\n  persist off;\n  ipv4 { import none; %s };\n}\n\n", c.KernelTable, export)

	for _, p := range c.Protocols {
		c.renderProtocol(&b, p)
	}
	if c.External != nil {
		// routes another daemon writes into a kernel table: read into a table of their own, filtered
		// on their way into the main table, from where they are exported like learned routes
		w("# routes another daemon writes into kernel table %d\n", c.External.Table)
		c.renderFilter(&b, "imp_external", c.External.Import)
		w("ipv4 table ext4;\n\n")
		w("protocol kernel ext_import {\n  kernel table %d;\n  learn;\n  ipv4 { table ext4; import all; export none; %s};\n}\n\n", c.External.Table, importLimit(c.External.Import))
		w("protocol pipe ext_pipe {\n  table ext4;\n  peer table master4;\n  import none;\n  export filter imp_external;\n}\n\n")
	}
	return b.String(), nil
}

func importLimit(i Import) string {
	if i.MaxPrefixes <= 0 {
		return ""
	}
	return fmt.Sprintf("import limit %d action disable; ", i.MaxPrefixes)
}

func (c Config) renderFilter(b *strings.Builder, name string, i Import) {
	fmt.Fprintf(b, "filter %s {\n", name)
	if !i.AllowDefault {
		b.WriteString("  if net = 0.0.0.0/0 then reject;\n")
	}
	if len(c.Protected) > 0 {
		set := make([]string, len(c.Protected))
		for k, p := range c.Protected {
			set[k] = p + "+"
		}
		fmt.Fprintf(b, "  if net ~ [ %s ] then reject;\n", strings.Join(set, ", "))
	}
	if len(i.Allowed) > 0 {
		set := make([]string, len(i.Allowed))
		for k, a := range i.Allowed {
			set[k] = a.Prefix
			if a.MaxLength != 0 {
				pf, _ := netip.ParsePrefix(a.Prefix)
				set[k] = fmt.Sprintf("%s{%d,%d}", a.Prefix, pf.Bits(), a.MaxLength)
			}
		}
		fmt.Fprintf(b, "  if net ~ [ %s ] then accept;\n  reject;\n}\n\n", strings.Join(set, ", "))
		return
	}
	b.WriteString("  accept;\n}\n\n")
}

func (c Config) renderProtocol(b *strings.Builder, p Protocol) {
	filter := "imp_" + p.Name
	ann := "ann_" + p.Name
	fmt.Fprintf(b, "# %s over %s\n", strings.ToUpper(p.Type), p.Interface)
	c.renderFilter(b, filter, p.Import)
	exportClause := "export none;"
	if len(p.Announce) > 0 {
		// BGP announces what the static protocol holds as unreachable; OSPF and Babel only originate
		// unicast routes, so there the prefix is a device route onto the link
		fmt.Fprintf(b, "protocol static %s {\n  ipv4;\n", ann)
		for _, a := range p.Announce {
			if p.Type == "bgp" {
				fmt.Fprintf(b, "  route %s unreachable;\n", a)
			} else {
				fmt.Fprintf(b, "  route %s via %q;\n", a, p.Interface)
			}
		}
		b.WriteString("}\n\n")
		exportClause = fmt.Sprintf("export where proto = %q;", ann)
	}
	channel := fmt.Sprintf("ipv4 { import filter %s; %s %s};", filter, exportClause, importLimit(p.Import))
	switch p.Type {
	case "bgp":
		fmt.Fprintf(b, "protocol bgp %s {\n  local %s as %d;\n  neighbor %s as %d;\n", p.Name, p.LocalAddress, c.ASN, p.NeighborAddress, p.BGP.NeighborASN)
		if p.BGP.Passive {
			b.WriteString("  passive on;\n")
		}
		fmt.Fprintf(b, "  hold time %d;\n  keepalive time %d;\n  connect retry time 5;\n  error wait time 1, 30;\n  %s\n", p.BGP.HoldTime, p.BGP.KeepaliveTime, channel)
	case "ospf":
		fmt.Fprintf(b, "protocol ospf v2 %s {\n  %s\n  area %s {\n    interface %q { type ptp; hello %d; dead %d;", p.Name, channel, p.OSPF.Area, p.Interface, p.OSPF.HelloInterval, p.OSPF.DeadInterval)
		if p.OSPF.Cost > 0 {
			fmt.Fprintf(b, " cost %d;", p.OSPF.Cost)
		}
		b.WriteString(" };\n  };\n")
	case "babel":
		fmt.Fprintf(b, "protocol babel %s {\n  %s\n  interface %q { type tunnel; hello interval %d s;", p.Name, channel, p.Interface, p.Babel.HelloInterval)
		if p.Babel.RxCost > 0 {
			fmt.Fprintf(b, " rxcost %d;", p.Babel.RxCost)
		}
		b.WriteString(" };\n")
	}
	if p.Custom != "" {
		b.WriteString("  # --- custom snippet (unmanaged) ---\n")
		for _, line := range strings.Split(strings.TrimRight(p.Custom, "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("}\n\n")
}

// ProtocolName derives the BIRD protocol name of a routing protocol: <type>_<name>.
func ProtocolName(typ, name string) string { return Ident(typ + "_" + name) }
