package engine

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ErrUnknownDevice is returned by Explain for a device that is neither configured nor discovered.
var ErrUnknownDevice = errors.New("engine: no such device")

// ExplainQuery is one traffic tuple to explain: the device (or, without one, the source address)
// that opens the connection, the destination, and optionally the protocol and port.
type ExplainQuery struct {
	// Device is a device's name or UUID; empty when Src is given.
	Device string
	// Src is the source address; with a Device it overrides the address the device has now.
	Src netip.Addr
	// Dst is an IPv4 address or a hostname.
	Dst string
	// Protocol is tcp, udp, icmp, or empty.
	Protocol string
	// Port is the destination port, 0 when unspecified.
	Port int
}

// Explanation is the answer: the API's Explanation of the spec, field for field.
type Explanation struct {
	Generation  int64               `json:"generation"`
	Source      ExplainSource       `json:"source"`
	Destination *ExplainDestination `json:"destination,omitempty"`
	Access      ExplainAccess       `json:"access"`
	Faults      []ExplainFamily     `json:"faults"`
	Service     string              `json:"service,omitempty"`
	Kernel      *ExplainKernel      `json:"kernel,omitempty"`
	Route       *ExplainRoute       `json:"route,omitempty"`
}

// ExplainSource is who opens the connection.
type ExplainSource struct {
	IP      string        `json:"ip,omitempty"`
	Device  *ExplainNamed `json:"device,omitempty"`
	Network *ExplainNamed `json:"network,omitempty"`
	Groups  []string      `json:"groups,omitempty"`
}

// ExplainNamed is an object by id and name.
type ExplainNamed struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ExplainDestination is where the connection goes.
type ExplainDestination struct {
	IP       string `json:"ip,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// ExplainAccess is whether the gateway forwards the traffic.
type ExplainAccess struct {
	Verdict string `json:"verdict"`
	Layer   string `json:"layer"`
	// Reason says which entry or default decided (not part of the spec's schema: kept out of the
	// JSON, the API may add it to a later version).
	Reason string `json:"-"`
}

// ExplainFamily is the resolution of one family.
type ExplainFamily struct {
	Family     string           `json:"family"`
	Winner     *model.FaultRef  `json:"winner,omitempty"`
	Overridden []model.FaultRef `json:"overridden,omitempty"`
}

// ExplainKernel is the classification as compiled: the fault id and the marks of its directions.
type ExplainKernel struct {
	FaultID      int    `json:"fault_id"`
	MarkUpload   string `json:"mark_upload"`
	MarkDownload string `json:"mark_download"`
}

// ExplainRoute is the route the kernel gives the packet.
type ExplainRoute struct {
	Table     int    `json:"table,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
	Interface string `json:"interface,omitempty"`
	// Unreachable and Error are set when the kernel has no route.
	Unreachable bool   `json:"unreachable,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Explain resolves a traffic tuple over the snapshot of this moment (plan §2.4): the access verdict,
// the winning fault of each family with the faults it overrode, the fault id and marks the compiler
// gave the impairment winner, whether the connection is redirected into the service namespace and
// the route the kernel takes for it (an executor read: `ip route get`). It never changes anything.
func (e *Engine) Explain(ctx context.Context, q ExplainQuery) (*Explanation, error) {
	snap := e.Snapshot()
	if snap.Config == nil {
		return nil, ErrNoConfiguration
	}
	world, err := domain.NewWorld(snap.Config, snap.Overlays)
	if err != nil {
		return nil, err
	}
	out := &Explanation{Generation: int64(snap.Generation), Faults: []ExplainFamily{}}

	// who opens the connection
	sub := domain.Subject{}
	var state *DeviceState
	switch {
	case q.Device != "":
		id, ok := world.Index.Resolve(domain.KindDevice, q.Device)
		if !ok {
			for i := range snap.Devices {
				d := &snap.Devices[i]
				if strings.EqualFold(d.ID, q.Device) || strings.EqualFold(d.Name, q.Device) {
					id, ok = strings.ToLower(d.ID), true
					break
				}
			}
		}
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownDevice, q.Device)
		}
		sub.Device = id
	case q.Src.IsValid():
		if id, ok := snap.Identity.OwnerOf(q.Src); ok {
			sub.Device = strings.ToLower(id)
		}
	default:
		return nil, errors.New("engine: explain needs a device or a source address")
	}
	for i := range snap.Devices {
		if strings.EqualFold(snap.Devices[i].ID, sub.Device) && sub.Device != "" {
			state = &snap.Devices[i]
			break
		}
	}
	switch {
	case q.Src.IsValid():
		sub.IP = q.Src
	case state != nil && len(state.Addresses) > 0:
		sub.IP = state.Addresses[0]
	case sub.Device != "":
		if as := snap.Identity.Addresses[sub.Device]; len(as) > 0 {
			sub.IP = as[0]
		}
	}
	if state != nil {
		sub.Network = strings.ToLower(state.Network)
	}
	if sub.Network == "" && sub.IP.IsValid() {
		if n, ok := world.NetworkOf(sub.IP); ok {
			sub.Network = n
		}
	}
	if sub.IP.IsValid() {
		out.Source.IP = sub.IP.String()
	}
	if sub.Device != "" {
		name := sub.Device
		if d, ok := world.Index.Devices[sub.Device]; ok {
			name = d.Name
		} else if state != nil {
			name = state.Name
		}
		out.Source.Device = &ExplainNamed{ID: sub.Device, Name: name}
		for gid, g := range deref(snap.Config.Groups) {
			for _, m := range deref(g.Members) {
				if strings.EqualFold(m, sub.Device) {
					out.Source.Groups = append(out.Source.Groups, strings.ToLower(gid))
				}
			}
		}
		sort.Strings(out.Source.Groups)
	}
	if n, ok := world.Index.Networks[sub.Network]; ok && sub.Network != "" {
		out.Source.Network = &ExplainNamed{ID: sub.Network, Name: n.Name}
	}

	// where it goes
	query := domain.Query{Source: sub, Protocol: strings.ToLower(q.Protocol), Port: q.Port}
	var dstIP netip.Addr
	if a, err := netip.ParseAddr(q.Dst); err == nil && a.Is4() {
		dstIP = a
		query.DestIP = a
		out.Destination = &ExplainDestination{IP: a.String()}
	} else if q.Dst != "" {
		query.DestNames = []string{strings.ToLower(q.Dst)}
		out.Destination = &ExplainDestination{Hostname: q.Dst}
	}

	// access: the gateway's protection and the matrix (access rules join with M9)
	if sub.IP.IsValid() && dstIP.IsValid() {
		a := world.AccessVerdict(sub.IP, dstIP, query.Protocol, q.Port, e.accessFacts(snap))
		out.Access = ExplainAccess{Verdict: a.Verdict, Layer: a.Layer, Reason: a.Reason}
	} else {
		// without a source address or an address to go to there is nothing to judge: say what holds
		// for traffic that is not addressed to the gateway
		out.Access = ExplainAccess{Verdict: "allow", Layer: domain.AccessMatrix, Reason: "the traffic cannot be placed without a source and a destination address"}
	}

	// faults: one entry per family that has candidates
	results := world.Resolve(query)
	for _, r := range results {
		fam := ExplainFamily{Family: r.Family}
		if r.Winner != nil {
			ref := r.Winner.Ref("")
			fam.Winner = &ref
		}
		for _, o := range r.Overridden {
			fam.Overridden = append(fam.Overridden, o.Ref(o.Reason))
		}
		out.Faults = append(out.Faults, fam)
	}
	if w := domain.Winner(results, domain.FamilyImpairment); w != nil {
		if f, ok := faultOf(snap, *w, sub.Device); ok {
			out.Kernel = &ExplainKernel{FaultID: f.ID,
				MarkUpload:   fmt.Sprintf("0x%08x", compiler.MarkOf(f.ID, compiler.Upload)),
				MarkDownload: fmt.Sprintf("0x%08x", compiler.MarkOf(f.ID, compiler.Download))}
		}
	}

	// the service namespace: DNS queries to the gateway's own address
	if snap.Service != nil && q.Port == 53 && (query.Protocol == "" || query.Protocol == "udp" || query.Protocol == "tcp") && dstIP.IsValid() {
		for _, g := range gatewayAddrs(snap) {
			if g == dstIP {
				out.Service = "dns_proxy"
			}
		}
	}
	if out.Service == "" {
		out.Service = "none"
	}

	// the route: the kernel decides, through the policy rules and table 100 (plan §2.2)
	if dstIP.IsValid() && sub.IP.IsValid() {
		iif := ""
		if sub.Network != "" {
			iif = interfaceOfNetwork(snap, sub.Network)
		}
		ans, err := e.RouteFor(ctx, dstIP.String(), sub.IP.String(), iif)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			e.cfg.Log.Warn("cannot read the route for explain", "dst", dstIP, "error", err)
		} else {
			r := &ExplainRoute{Gateway: ans.Gateway, Interface: ans.Interface, Unreachable: ans.Unreachable, Error: ans.Error}
			switch ans.Table {
			case "", "main":
				r.Table = 254
			default:
				if n, err := strconv.Atoi(ans.Table); err == nil {
					r.Table = n
				}
			}
			out.Route = r
		}
	}
	return out, nil
}

func deref[T any](p *T) T {
	var z T
	if p != nil {
		z = *p
	}
	return z
}

// faultOf finds the fault id the compiler gave a winning candidate: the id of the candidate itself,
// or the one of its device when it has an id per device (a rate, a queue limit or keep order).
func faultOf(snap *Snapshot, c domain.Candidate, device string) (compiler.Fault, bool) {
	base := string(c.Layer) + ":" + c.ID + ":" + c.Family
	for _, f := range snap.Faults {
		if f.Key == base || (device != "" && f.Key == base+"@"+device) || f.Key == base+"@shared" {
			return f, true
		}
	}
	return compiler.Fault{}, false
}

func gatewayAddrs(snap *Snapshot) []netip.Addr {
	var out []netip.Addr
	for _, b := range snap.Bridges {
		out = append(out, b.Address.Addr())
	}
	for _, w := range snap.WireGuardInterfaces {
		out = append(out, w.Address.Addr())
	}
	return out
}

// interfaceOfNetwork is the interface traffic from a network arrives on.
func interfaceOfNetwork(snap *Snapshot, network string) string {
	for _, b := range snap.Bridges {
		if strings.EqualFold(b.NetworkID, network) {
			return b.Name
		}
	}
	for _, w := range snap.WireGuardInterfaces {
		if strings.EqualFold(w.NetworkID, network) {
			return w.Name
		}
	}
	return ""
}

func (e *Engine) accessFacts(snap *Snapshot) domain.AccessFacts {
	f := domain.AccessFacts{Gateway: gatewayAddrs(snap), Management: append([]netip.Prefix(nil), snap.Management.Sources...)}
	if snap.Management.Subnet.IsValid() {
		f.Management = append(f.Management, snap.Management.Subnet)
	}
	// two-port: the management network lies behind the uplink interface
	f.TwoPort = snap.Applied != nil && snap.Management.Name != "" && snap.Management.Name == snap.Applied.Uplink.Name
	return f
}
