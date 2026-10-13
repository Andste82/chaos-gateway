package apply

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// LinuxText is the kernel state that Chaos Gateway owns, rendered as text for the technical view
// (plan §2.17, the API's `LinuxState`): what is there now with its live counters, not a difference.
type LinuxText struct {
	Nftables, TC, Routes, WireGuard string
}

// maxSetElements is the most elements of one nft set the technical view lists.
const maxSetElements = 256

// Render renders the state read by ReadState: the table inet chaosgw in nft syntax (without the
// generation chain), the tc trees of Chaos Gateway's own with their counters, the bridges, policy
// rules and routes of its tables, and the WireGuard interfaces with their peers. bridges and wg name
// the interfaces of the applied target; the state of an interface that is not there is left out.
func Render(s *State, bridges, wg []string) LinuxText {
	return LinuxText{
		Nftables:  linux.RenderNft(s.Nft, maxSetElements, compiler.GenerationChain),
		TC:        renderTC(s),
		Routes:    renderHost(s, bridges),
		WireGuard: renderWG(s, wg),
	}
}

func renderTC(s *State) string {
	devs := make([]string, 0, len(s.TC))
	for d := range s.TC {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	var b strings.Builder
	for _, d := range devs {
		t := s.TC[d].Sorted()
		for _, q := range t.Qdiscs {
			fmt.Fprintf(&b, "%s: %s\n", d, q.Line())
			if q.Stats != nil {
				fmt.Fprintf(&b, "%s:   %s\n", d, statsText(*q.Stats))
			}
		}
		for _, c := range t.Classes {
			fmt.Fprintf(&b, "%s: %s\n", d, c.Line())
			if c.Stats != nil {
				fmt.Fprintf(&b, "%s:   %s\n", d, statsText(*c.Stats))
			}
		}
		for _, f := range t.Filters {
			fmt.Fprintf(&b, "%s: %s\n", d, f.Line())
			if f.Stats != nil {
				fmt.Fprintf(&b, "%s:   matched %d pkt %d bytes\n", d, f.Stats.Packets, f.Stats.Bytes)
			}
		}
	}
	return b.String()
}

func statsText(st linux.NormStats) string {
	return fmt.Sprintf("sent %d bytes %d pkt (dropped %d, overlimits %d, requeues %d) backlog %db %dp", st.Bytes, st.Packets, st.Drops, st.Overlimits, st.Requeues, st.Backlog, st.Qlen)
}

func renderHost(s *State, bridges []string) string {
	var b strings.Builder
	names := append([]string(nil), bridges...)
	sort.Strings(names)
	for _, n := range names {
		l, ok := s.Links[n]
		if !ok {
			continue
		}
		state := "down"
		if l.Up() {
			state = "up"
		}
		fmt.Fprintf(&b, "bridge %s %s\n", n, state)
		for _, a := range s.Addrs[n] {
			if a.Family == "inet" {
				fmt.Fprintf(&b, "  address %s/%d\n", a.Local, a.PrefixLen)
			}
		}
		var ports []string
		for _, o := range s.Links {
			if o.Master == n {
				ports = append(ports, o.Name)
			}
		}
		sort.Strings(ports)
		for _, p := range ports {
			fmt.Fprintf(&b, "  port %s\n", p)
		}
	}
	var rl, rt []string
	for _, r := range s.Rules {
		if er, ok := stateRule(r); ok && r.Protocol == ownProto {
			rl = append(rl, ruleText(er))
		}
	}
	for _, r := range s.Routes {
		if r.Protocol == ownProto && ownTable(r.Table) {
			er := stateRoute(r)
			rt = append(rt, fmt.Sprintf("route table %d %s%s", er.Table, er.Dst, viaDev(er)))
		}
	}
	sort.Strings(rl)
	sort.Strings(rt)
	for _, l := range append(rl, rt...) {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// ruleText renders a policy rule with the selectors it has ("rule 1000 iif br-iot lookup 100").
func ruleText(r executor.Rule) string {
	s := fmt.Sprintf("rule %d", r.Priority)
	for _, kv := range [][2]string{{"from", r.From}, {"to", r.To}, {"iif", r.Iif}, {"oif", r.Oif}, {"fwmark", r.Fwmark}} {
		if kv[1] != "" {
			s += " " + kv[0] + " " + kv[1]
		}
	}
	return s + fmt.Sprintf(" lookup %d", r.Table)
}

func renderWG(s *State, names []string) string {
	var b strings.Builder
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, n := range sorted {
		l, ok := s.Links[n]
		info := s.WireGuard[n]
		if !ok || info == nil {
			continue
		}
		state := "down"
		if l.Up() {
			state = "up"
		}
		fmt.Fprintf(&b, "wireguard %s %s public key %s\n", n, state, info.PublicKey)
		for _, a := range s.Addrs[n] {
			if a.Family == "inet" {
				fmt.Fprintf(&b, "  address %s/%d mtu %d port %d\n", a.Local, a.PrefixLen, l.MTU, info.ListenPort)
			}
		}
		peers := append([]linux.WGPeerInfo(nil), info.Peers...)
		sort.Slice(peers, func(i, j int) bool { return peers[i].PublicKey < peers[j].PublicKey })
		for _, p := range peers {
			allowed := append([]string(nil), p.AllowedIPs...)
			sort.Strings(allowed)
			line := fmt.Sprintf("  peer %s allowed %s", p.PublicKey, strings.Join(allowed, ","))
			if p.Endpoint != "" {
				line += " endpoint " + p.Endpoint
			}
			if p.Keepalive > 0 {
				line += fmt.Sprintf(" keepalive %d", p.Keepalive)
			}
			if p.HasPresharedKey {
				line += " preshared"
			}
			if p.LatestHandshake > 0 {
				line += " handshake " + time.Unix(p.LatestHandshake, 0).UTC().Format(time.RFC3339)
			} else {
				line += " no handshake"
			}
			line += fmt.Sprintf(" rx %d tx %d", p.RxBytes, p.TxBytes)
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
