package api

import (
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

type deviceView struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Origin   string         `json:"origin"`
	Network  string         `json:"network,omitempty"`
	Config   *model.Device  `json:"config,omitempty"`
	Groups   []string       `json:"groups,omitempty"`
	Observed deviceObserved `json:"observed"`
}

type deviceObserved struct {
	Addresses   []string         `json:"addresses,omitempty"`
	Lease       *model.DhcpLease `json:"lease,omitempty"`
	LastSeen    *time.Time       `json:"last_seen,omitempty"`
	MACs        []string         `json:"macs,omitempty"`
	Online      bool             `json:"online"`
	Sources     []string         `json:"sources,omitempty"`
	WireGuard   gin.H            `json:"wireguard,omitempty"`
	UploadBps   int64            `json:"upload_bps,omitempty"`
	DownloadBps int64            `json:"download_bps,omitempty"`
	FlowsActive int              `json:"flows_active,omitempty"`
}

func observedOf(st *engine.DeviceState, peer *engine.PeerStatus) deviceObserved {
	o := deviceObserved{}
	if st != nil {
		for _, a := range st.Addresses {
			o.Addresses = append(o.Addresses, a.String())
		}
		o.Lease, o.MACs, o.Online, o.Sources = st.Lease, st.MACs, st.Online, st.Sources
		o.UploadBps, o.DownloadBps, o.FlowsActive = st.UploadBps, st.DownloadBps, st.FlowsActive
		if !st.LastSeen.IsZero() {
			t := st.LastSeen.UTC()
			o.LastSeen = &t
		}
	}
	if peer != nil {
		o.WireGuard = peerStatus(*peer, nil)
		o.Online = o.Online || peer.Online
	}
	return o
}

// deviceViews joins the configuration's devices (configured, WireGuard clients, probes) with what the
// gateway observes, and adds the discovered devices.
func (s *Server) deviceViews(cfg *model.Configuration, withDiscovered bool) []deviceView {
	snap := s.cfg.Engine.Snapshot()
	states := map[string]*engine.DeviceState{}
	for i := range snap.Devices {
		states[snap.Devices[i].ID] = &snap.Devices[i]
	}
	idx, _ := domain.BuildIndex(cfg)
	groups := map[string][]string{}
	if cfg.Groups != nil {
		for gid, g := range *cfg.Groups {
			if g.Members == nil {
				continue
			}
			for _, ref := range *g.Members {
				if did, ok := idx.Resolve(domain.KindDevice, ref); ok {
					groups[did] = append(groups[did], gid)
				}
			}
		}
	}
	var out []deviceView
	for did, d := range idx.Devices {
		v := deviceView{ID: did, Name: d.Name, Origin: string(d.Origin), Network: d.Network, Config: d.Device, Groups: groups[did]}
		var peer *engine.PeerStatus
		if p, ok := snap.WireGuard[did]; ok {
			peer = &p
		}
		v.Observed = observedOf(states[did], peer)
		sort.Strings(v.Groups)
		out = append(out, v)
	}
	if withDiscovered {
		for _, st := range snap.Devices {
			if st.Origin != model.DeviceOriginDiscovered {
				continue
			}
			if _, configured := idx.Devices[st.ID]; configured {
				continue
			}
			cp := st
			out = append(out, deviceView{ID: st.ID, Name: st.Name, Origin: "discovered", Network: st.Network, Observed: observedOf(&cp, nil)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return sortKey(out[i].Name, out[i].ID) < sortKey(out[j].Name, out[j].ID) })
	return out
}

// ListDevices implements GET /devices.
func (s *Server) ListDevices(c *gin.Context, params model.ListDevicesParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	all := s.deviceViews(v.cfg, v.active)
	netID := ""
	if params.Network != nil {
		id, _, found := findNetwork(v.cfg, *params.Network)
		if !found {
			s.write(c, notFound("network", *params.Network))
			return
		}
		netID = id
	}
	var filtered []deviceView
	for _, d := range all {
		if netID != "" && d.Network != netID {
			continue
		}
		if params.Origin != nil && string(*params.Origin) != d.Origin {
			continue
		}
		if params.Online != nil && *params.Online != d.Observed.Online {
			continue
		}
		filtered = append(filtered, d)
	}
	items, next, err := page(filtered, func(d deviceView) string { return sortKey(d.Name, d.ID) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

func findDevice(views []deviceView, ref string) (deviceView, bool) {
	lower := strings.ToLower(ref)
	for _, d := range views {
		if strings.ToLower(d.ID) == lower {
			return d, true
		}
	}
	for _, d := range views {
		if strings.EqualFold(d.Name, ref) {
			return d, true
		}
	}
	return deviceView{}, false
}

// GetDevice implements GET /devices/{deviceId}.
func (s *Server) GetDevice(c *gin.Context, deviceId model.DeviceId, params model.GetDeviceParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	d, found := findDevice(s.deviceViews(v.cfg, v.active), deviceId)
	if !found {
		s.write(c, notFound("device", deviceId))
		return
	}
	c.JSON(200, d)
}

type leaseView struct {
	Device    string    `json:"device,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	Hostname  string    `json:"hostname,omitempty"`
	IP        string    `json:"ip"`
	MAC       string    `json:"mac"`
	Network   string    `json:"network"`
	State     string    `json:"state,omitempty"`
}

// ListLeases implements GET /networks/{networkId}/leases.
func (s *Server) ListLeases(c *gin.Context, networkId model.NetworkId, params model.ListLeasesParams) {
	_, cfg, ok := s.configAt(c, nil)
	if !ok {
		return
	}
	id, n, found := findNetwork(cfg, networkId)
	if !found {
		s.write(c, notFound("network", networkId))
		return
	}
	if _, typ := networkName(n); typ != "lan" {
		s.write(c, newProblem(model.ErrorCodeNotFound, "WireGuard networks have no DHCP"))
		return
	}
	snap := s.cfg.Engine.Snapshot()
	deviceOf := map[string]string{} // lease address → device
	for _, st := range snap.Devices {
		if st.Lease != nil {
			deviceOf[st.Lease.Ip] = st.ID
		}
	}
	var all []leaseView
	for _, l := range snap.Leases {
		if l.Network.String() != id {
			continue
		}
		lv := leaseView{ExpiresAt: l.ExpiresAt.UTC(), IP: l.Ip, MAC: l.Mac, Network: id, Device: deviceOf[l.Ip]}
		if l.Hostname != nil {
			lv.Hostname = *l.Hostname
		}
		if l.State != nil {
			lv.State = string(*l.State)
		}
		all = append(all, lv)
	}
	sort.Slice(all, func(i, j int) bool {
		a, _ := netip.ParseAddr(all[i].IP)
		b, _ := netip.ParseAddr(all[j].IP)
		return a.Less(b)
	})
	items, next, err := page(all, func(l leaseView) string {
		a, _ := netip.ParseAddr(l.IP)
		b := a.As4()
		return string(b[:])
	}, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

type flowView struct {
	Device    string     `json:"device,omitempty"`
	Download  *traffic   `json:"download,omitempty"`
	DPort     int        `json:"dport,omitempty"`
	Dst       string     `json:"dst"`
	ID        string     `json:"id"`
	NatSrc    string     `json:"nat_src,omitempty"`
	Network   string     `json:"network,omitempty"`
	Protocol  string     `json:"protocol"`
	Service   string     `json:"service,omitempty"`
	SPort     int        `json:"sport,omitempty"`
	Src       string     `json:"src"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	State     string     `json:"state,omitempty"`
	Upload    *traffic   `json:"upload,omitempty"`
}

type traffic struct {
	Bytes   int64 `json:"bytes"`
	Packets int64 `json:"packets"`
}

func trafficOf(t engine.Traffic) *traffic {
	if t.Bytes == 0 && t.Packets == 0 {
		return nil
	}
	return &traffic{Bytes: t.Bytes, Packets: t.Packets}
}

// ListFlows implements GET /flows: the tracked connections of the test networks, with their devices.
func (s *Server) ListFlows(c *gin.Context, params model.ListFlowsParams) {
	_, cfg, ok := s.configAt(c, nil)
	if !ok {
		return
	}
	wantDevice, wantNet := "", ""
	if params.Device != nil {
		d, found := findDevice(s.deviceViews(cfg, true), *params.Device)
		if !found {
			s.write(c, notFound("device", *params.Device))
			return
		}
		wantDevice = d.ID
	}
	if params.Network != nil {
		id, _, found := findNetwork(cfg, *params.Network)
		if !found {
			s.write(c, notFound("network", *params.Network))
			return
		}
		wantNet = id
	}
	flows, err := s.cfg.Engine.Flows(contextOf(c))
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "cannot read the connections: %v", firstLine(err.Error())))
		return
	}
	var all []flowView
	for _, f := range flows {
		if (wantDevice != "" && f.Device != wantDevice) || (wantNet != "" && f.Network != wantNet) {
			continue
		}
		fv := flowView{Device: f.Device, DPort: f.DPort, Dst: f.Dst.String(), ID: f.ID, Network: f.Network, Protocol: f.Protocol, Service: f.Service, SPort: f.SPort,
			Src: f.Src.String(), State: f.State, Upload: trafficOf(f.Upload), Download: trafficOf(f.Download)}
		if f.NatSrc.IsValid() {
			fv.NatSrc = f.NatSrc.String()
		}
		if !f.StartedAt.IsZero() {
			fv.StartedAt = &f.StartedAt
		}
		all = append(all, fv)
	}
	items, next, perr := page(all, func(f flowView) string { return f.ID }, params.Cursor, params.Limit)
	if perr != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", perr))
		return
	}
	c.JSON(200, listBody(items, next))
}

// PostLeaseEvent implements POST /internal/dhcp/lease-events: Kea's run_script hook reports a lease.
func (s *Server) PostLeaseEvent(c *gin.Context) {
	var body struct {
		ClientID      string `json:"client_id"`
		Event         string `json:"event"`
		Hostname      string `json:"hostname"`
		IP            string `json:"ip"`
		MAC           string `json:"mac"`
		SubnetID      int    `json:"subnet_id"`
		ValidLifetime int    `json:"valid_lifetime"`
	}
	if p := decodeJSON(c, &body); p != nil {
		s.write(c, p)
		return
	}
	if p := requireFields(map[string]bool{"event": body.Event != "", "ip": body.IP != "", "mac": body.MAC != "", "subnet_id": body.SubnetID > 0}); p != nil {
		s.write(c, p)
		return
	}
	ip, err := netip.ParseAddr(body.IP)
	if _, merr := net.ParseMAC(body.MAC); merr != nil || len(body.Hostname) > 253 || err != nil || !ip.Is4() || !model.KeaLeaseEventEvent(body.Event).Valid() {
		errs := []model.ValidationError{{Path: "/ip", Code: "invalid", Message: "event and ip must be a Kea hook event and an IPv4 address"}}
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the lease event is not valid").with(func(b *model.Problem) { b.Errors = &errs }))
		return
	}
	s.cfg.Engine.LeaseEvent(kea.Event{Name: body.Event, IP: ip, MAC: strings.ToLower(body.MAC), ClientID: body.ClientID, Hostname: body.Hostname,
		SubnetID: body.SubnetID, ValidLifetime: body.ValidLifetime})
	c.Status(http.StatusNoContent)
}
