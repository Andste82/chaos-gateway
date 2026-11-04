package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The redirect of a time fault is NAT, and the NAT chain sees only the first packet of a flow: a device that keeps its source
// port (ntpd, systemd-timesyncd, a chronyd that keeps its socket) and polls more often than the UDP timeouts of conntrack
// (30 s unreplied, 120 s replied) goes on to the server its flow was first sent to. A fault that arrives would miss it, and
// a fault that ends would go on shaping its answers, which is the silent miss that decision Q3 and risk 35 are about. So
// when the redirects of one apply differ from those of the one before, the engine deletes the tracked NTP flows, and the
// next packet of each is a new flow that meets the new rules (plan §2.18, D36). Like the cut of access rules (cut.go) this
// is a consequence of the apply, not part of it: a failure is logged and the rules hold either way, and the first apply of a
// process, which does not know what the kernel ran before, deletes nothing.

// ntpStaleFlows lists the tracked UDP flows a change of the NTP redirects from old to next may have decided wrongly: the
// flows to an NTP port (123 and the ports a time fault names) and the flows that were redirected into the server (the reply
// comes from the service address and one of the ports it listens on).
func ntpStaleFlows(old, next *compiler.Target, raw []linux.Conntrack) []executor.ConntrackFlow {
	ports := map[int]bool{compiler.ServiceNTPPort: true}
	var ranges []model.PortRange
	listen := map[int]bool{}
	peers := map[netip.Addr]bool{}
	for _, t := range []*compiler.Target{old, next} {
		if t == nil {
			continue
		}
		for _, p := range t.NTPListenPorts() {
			listen[p] = true
		}
		if t.Service != nil {
			peers[t.Service.PeerCIDR.Addr()] = true
		}
		for _, f := range t.NTPFaults {
			ps, rs := domain.TimePorts(f.Fault)
			for _, p := range ps {
				ports[p] = true
			}
			ranges = append(ranges, rs...)
		}
	}
	named := func(p int) bool {
		if ports[p] {
			return true
		}
		for _, r := range ranges {
			if p >= r.From && p <= r.To {
				return true
			}
		}
		return false
	}
	var out []executor.ConntrackFlow
	for _, c := range raw {
		if c.Proto != "udp" || c.Original.SPort == 0 || c.Original.DPort == 0 {
			continue
		}
		src, err1 := netip.ParseAddr(c.Original.Src)
		dst, err2 := netip.ParseAddr(c.Original.Dst)
		if err1 != nil || err2 != nil || !src.Is4() || !dst.Is4() {
			continue
		}
		rs, rerr := netip.ParseAddr(c.Reply.Src)
		redirected := rerr == nil && peers[rs] && listen[c.Reply.SPort]
		if !named(c.Original.DPort) && !redirected {
			continue
		}
		out = append(out, executor.ConntrackFlow{Proto: "udp", Src: c.Original.Src, Dst: c.Original.Dst, SPort: c.Original.SPort, DPort: c.Original.DPort})
	}
	return out
}

// dropNTPFlows deletes the tracked NTP flows when the redirects changed (see above) and reports how many it deleted.
func (e *Engine) dropNTPFlows(ctx context.Context, old, next *compiler.Target) (int, error) {
	if old == nil || next == nil || !compiler.NTPRedirectChanged(old, next) {
		return 0, nil
	}
	ns := executor.Target{NS: e.cfg.Namespace}
	out, err := e.cfg.Exec.Do(ctx, &executor.Read{Target: ns, What: executor.ReadConntrack})
	if err != nil {
		return 0, fmt.Errorf("read the tracked connections: %w", err)
	}
	var raw []linux.Conntrack
	if len(out.Data) > 0 {
		if err := json.Unmarshal(out.Data[0], &raw); err != nil {
			return 0, fmt.Errorf("read the tracked connections: %w", err)
		}
	}
	flows := ntpStaleFlows(old, next, raw)
	deleted := 0
	var first error
	for start := 0; start < len(flows); start += executor.MaxConntrackFlows {
		end := min(start+executor.MaxConntrackFlows, len(flows))
		if _, err := e.cfg.Exec.Do(ctx, &executor.ConntrackDelete{Target: ns, Flows: flows[start:end]}); err != nil {
			if first == nil {
				first = fmt.Errorf("delete the tracked NTP flows: %w", err)
			}
			continue
		}
		deleted += end - start
	}
	return deleted, first
}
