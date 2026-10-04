package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// view is a read of the active revision or, with ?revision=, of another one; viewed says whether it
// is the active one (a candidate's runtime state is "pending").
type view struct {
	rev    model.Revision
	cfg    *model.Configuration
	active bool
}

func (s *Server) viewOf(c *gin.Context, q *model.RevisionQuery) (view, bool) {
	var rev *int64
	if q != nil {
		v := *q
		rev = &v
	}
	r, cfg, ok := s.configAt(c, rev)
	if !ok {
		return view{}, false
	}
	s.etag(c)
	return view{rev: r, cfg: cfg, active: r.Id == s.cfg.Store.ActiveID()}, true
}

func sortKey(name, id string) string { return strings.ToLower(name) + "\x00" + id }

func listBody[T any](items []T, next *string) gin.H {
	if items == nil {
		items = []T{}
	}
	b := gin.H{"items": items}
	if next != nil {
		b["next_cursor"] = *next
	}
	return b
}

// ---- uplink

// GetUplink implements GET /uplink.
func (s *Server) GetUplink(c *gin.Context, params model.GetUplinkParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	snap := s.cfg.Engine.Snapshot()
	status := gin.H{}
	var name string
	if snap.Applied != nil {
		u := snap.Applied.Uplink
		name = u.Name
		if u.Addr.IsValid() {
			status["address"] = u.Addr.String()
		}
		if u.Gateway.IsValid() {
			status["gateway"] = u.Gateway.String()
		}
	} else if l, ok := resolveLink(snap.Host, v.cfg.Uplink.Interface); ok {
		name = l.Name
		if a, ok := l.FirstV4(); ok {
			status["address"] = a.String()
		}
	}
	if name != "" {
		status["interface"] = name
		hi := hostInterface{Name: name}
		sysfs(&hi)
		if hi.LinkUp != nil {
			status["link_up"] = *hi.LinkUp
		}
		if hi.SpeedMbps > 0 {
			status["speed_mbps"] = hi.SpeedMbps
		}
	}
	if v.cfg.Uplink.DnsUpstream != nil && len(*v.cfg.Uplink.DnsUpstream) > 0 {
		status["dns_servers"] = *v.cfg.Uplink.DnsUpstream
	}
	c.JSON(200, gin.H{"config": v.cfg.Uplink, "status": status})
}

func resolveLink(h compiler.Host, ref model.InterfaceRef) (compiler.HostLink, bool) {
	return h.Resolve(ref)
}

// ---- networks

type networkView struct {
	ID     string        `json:"id"`
	Config model.Network `json:"config"`
	Status gin.H         `json:"status"`
}

func networkName(n model.Network) (name, typ string) {
	typ, _ = n.Discriminator()
	switch typ {
	case "lan":
		if l, err := n.AsLanNetwork(); err == nil {
			return l.Name, typ
		}
	case "wireguard":
		if w, err := n.AsWireGuardNetwork(); err == nil {
			return w.Name, typ
		}
	}
	return "", typ
}

func (s *Server) networkViews(v view, typ *model.NetworkType) []networkView {
	var out []networkView
	if v.cfg.Networks == nil {
		return out
	}
	for id, n := range *v.cfg.Networks {
		_, t := networkName(n)
		if typ != nil && string(*typ) != t {
			continue
		}
		out = append(out, networkView{ID: id, Config: n, Status: s.networkStatus(v, id, n)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := networkName(out[i].Config)
		b, _ := networkName(out[j].Config)
		return sortKey(a, out[i].ID) < sortKey(b, out[j].ID)
	})
	return out
}

func (s *Server) networkStatus(v view, id string, n model.Network) gin.H {
	snap := s.cfg.Engine.Snapshot()
	st := gin.H{"state": "ok"}
	if !v.active {
		st["state"] = "pending"
	}
	for _, p := range snap.Problems {
		if p.Network == id && p.Code == compiler.CodePortMissing && v.active {
			st["state"] = "degraded"
		}
	}
	if !v.active {
		return st
	}
	_, typ := networkName(n)
	switch typ {
	case "lan":
		for _, b := range snap.Bridges {
			if b.NetworkID != id {
				continue
			}
			st["interface"] = b.Name
			ports := []gin.H{}
			for _, p := range b.Ports {
				hi := hostInterface{Name: p}
				sysfs(&hi)
				port := gin.H{"name": p, "present": true}
				if l, ok := snap.Host.Link(p); ok {
					port["mac"] = l.MAC
				}
				if hi.LinkUp != nil {
					port["link_up"] = *hi.LinkUp
				}
				if hi.SpeedMbps > 0 {
					port["speed_mbps"] = hi.SpeedMbps
				}
				ports = append(ports, port)
			}
			st["ports"] = ports
		}
	case "wireguard":
		for _, w := range snap.WireGuardInterfaces {
			if w.NetworkID != id {
				continue
			}
			st["interface"] = w.Name
			st["public_key"] = w.PublicKey
			total, online := 0, 0
			var rx, tx int64
			for _, p := range w.Peers {
				ps, ok := snap.WireGuard[p.ID]
				total++
				if ok {
					rx, tx = rx+ps.RxBytes, tx+ps.TxBytes
					if ps.Online {
						online++
					}
				}
			}
			if wg, err := n.AsWireGuardNetwork(); err == nil && wg.Kind == model.Link {
				for _, p := range w.Peers {
					if ps, ok := snap.WireGuard[p.ID]; ok {
						st["link_peer"] = peerStatus(ps, nil)
					}
				}
			} else {
				st["peers_total"], st["peers_online"] = total, online
			}
			st["rx_bytes"], st["tx_bytes"] = rx, tx
		}
	}
	return st
}

func peerStatus(ps engine.PeerStatus, learned []string) gin.H {
	b := gin.H{"online": ps.Online, "rx_bytes": ps.RxBytes, "tx_bytes": ps.TxBytes}
	if ps.Endpoint != "" {
		b["endpoint"] = ps.Endpoint
	}
	if !ps.LastHandshake.IsZero() {
		b["last_handshake"] = ps.LastHandshake.UTC()
	}
	if len(learned) > 0 {
		b["learned_routes"] = learned
	}
	return b
}

// ListNetworks implements GET /networks.
func (s *Server) ListNetworks(c *gin.Context, params model.ListNetworksParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	all := s.networkViews(v, params.Type)
	items, next, err := page(all, func(n networkView) string { name, _ := networkName(n.Config); return sortKey(name, n.ID) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// findNetwork finds a network by UUID or name.
func findNetwork(cfg *model.Configuration, ref string) (string, model.Network, bool) {
	return findByRef(cfg.Networks, ref, func(n model.Network) string { name, _ := networkName(n); return name })
}

// GetNetwork implements GET /networks/{networkId}.
func (s *Server) GetNetwork(c *gin.Context, networkId model.NetworkId, params model.GetNetworkParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	id, n, found := findNetwork(v.cfg, networkId)
	if !found {
		s.write(c, notFound("network", networkId))
		return
	}
	c.JSON(200, networkView{ID: id, Config: n, Status: s.networkStatus(v, id, n)})
}

// ---- WireGuard clients

type clientView struct {
	ID               string                `json:"id"`
	Network          string                `json:"network"`
	Config           model.WireGuardClient `json:"config"`
	PrivateKeyStored bool                  `json:"private_key_stored"`
	Status           gin.H                 `json:"status"`
}

func (s *Server) hubOf(c *gin.Context, v view, ref string) (string, model.WireGuardNetwork, bool) {
	id, n, found := findNetwork(v.cfg, ref)
	if !found {
		s.write(c, notFound("network", ref))
		return "", model.WireGuardNetwork{}, false
	}
	wg, err := n.AsWireGuardNetwork()
	if err != nil || wg.Type != model.WireGuardNetworkTypeWireguard {
		s.write(c, newProblem(model.ErrorCodeNotFound, "the network %q is not a WireGuard network", ref))
		return "", model.WireGuardNetwork{}, false
	}
	return id, wg, true
}

func (s *Server) clientView(v view, netID, cid string, cl model.WireGuardClient) clientView {
	snap := s.cfg.Engine.Snapshot()
	cv := clientView{ID: cid, Network: netID, Config: cl, Status: gin.H{"online": false}}
	if k, err := s.cfg.Secrets.WireGuard(wireguard.PeerKeyID(cid, cl.Key)); err == nil && k.PrivateKey != "" {
		cv.PrivateKeyStored = true
	}
	if v.active {
		if ps, ok := snap.WireGuard[cid]; ok {
			cv.Status = peerStatus(ps, nil)
		}
	}
	return cv
}

// ListWireGuardClients implements GET /networks/{networkId}/clients.
func (s *Server) ListWireGuardClients(c *gin.Context, networkId model.NetworkId, params model.ListWireGuardClientsParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	netID, wg, ok := s.hubOf(c, v, networkId)
	if !ok {
		return
	}
	var all []clientView
	if wg.Clients != nil {
		for cid, cl := range *wg.Clients {
			all = append(all, s.clientView(v, netID, cid, cl))
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return sortKey(all[i].Config.Name, all[i].ID) < sortKey(all[j].Config.Name, all[j].ID)
	})
	items, next, err := page(all, func(x clientView) string { return sortKey(x.Config.Name, x.ID) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// GetWireGuardClient implements GET /networks/{networkId}/clients/{clientId}.
func (s *Server) GetWireGuardClient(c *gin.Context, networkId model.NetworkId, clientId model.ClientId, params model.GetWireGuardClientParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	netID, wg, ok := s.hubOf(c, v, networkId)
	if !ok {
		return
	}
	cid, cl, found := findByRef(wg.Clients, clientId, func(x model.WireGuardClient) string { return x.Name })
	if !found {
		s.write(c, notFound("client", clientId))
		return
	}
	c.JSON(200, s.clientView(v, netID, cid, cl))
}

// ---- groups

type groupView struct {
	ID     string      `json:"id"`
	Config model.Group `json:"config"`
}

// ListGroups implements GET /groups.
func (s *Server) ListGroups(c *gin.Context, params model.ListGroupsParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	var all []groupView
	if v.cfg.Groups != nil {
		for id, g := range *v.cfg.Groups {
			all = append(all, groupView{ID: id, Config: g})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return sortKey(all[i].Config.Name, all[i].ID) < sortKey(all[j].Config.Name, all[j].ID)
	})
	items, next, err := page(all, func(g groupView) string { return sortKey(g.Config.Name, g.ID) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// GetGroup implements GET /groups/{groupId}.
func (s *Server) GetGroup(c *gin.Context, groupId model.Ref, params model.GetGroupParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	id, g, found := findByRef(v.cfg.Groups, groupId, func(g model.Group) string { return g.Name })
	if !found {
		s.write(c, notFound("group", groupId))
		return
	}
	c.JSON(200, groupView{ID: id, Config: g})
}

// ---- routing

// GetRouting implements GET /routing.
func (s *Server) GetRouting(c *gin.Context, params model.GetRoutingParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	if v.cfg.Routing == nil {
		c.JSON(200, gin.H{})
		return
	}
	c.JSON(200, v.cfg.Routing)
}

// birdState maps BIRD's state of a protocol to the spec's.
func birdState(p bird.ProtocolStatus) string {
	switch {
	case strings.HasPrefix(p.Info, "Error") || p.LastError != "" && p.State != "up":
		return "error"
	case p.Established():
		return "up"
	case p.State == "up":
		return "starting"
	case p.State == "start":
		return "starting"
	case p.State == "down":
		return "down"
	}
	return "down"
}

// GetRoutingStatus implements GET /routing/status.
func (s *Server) GetRoutingStatus(c *gin.Context) {
	snap := s.cfg.Engine.Snapshot()
	body := gin.H{}
	protocols := []gin.H{}
	if snap.Bird == nil {
		body["bird"] = gin.H{"running": false}
		body["protocols"] = protocols
		c.JSON(200, body)
		return
	}
	body["bird"] = gin.H{"running": len(snap.Routing) > 0 || snap.Bird != nil, "router_id": snap.Bird.Config.RouterID}
	ids := map[string]string{}     // BIRD protocol name → configuration id
	cfgName := map[string]string{} // configuration id → its name
	if snap.Config != nil && snap.Config.Routing != nil && snap.Config.Routing.Protocols != nil {
		for id, p := range *snap.Config.Routing.Protocols {
			ids[bird.ProtocolName(string(p.Type), p.Name)] = id
			cfgName[id] = p.Name
		}
	}
	announced := map[string][]string{}
	for _, p := range snap.Bird.Config.Protocols {
		announced[p.Name] = p.Announce
	}
	names := make([]string, 0, len(snap.Routing))
	for n := range snap.Routing {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := snap.Routing[n]
		id := ids[n]
		if id == "" {
			// a protocol whose name got a suffix (two entries with one name): match by prefix
			for k, v := range ids {
				if strings.HasPrefix(n, k) {
					id = v
				}
			}
		}
		if id == "" {
			continue
		}
		routes := gin.H{"imported": p.Imported, "filtered": p.Filtered, "exported": p.Exported}
		if p.ImportLimitHit {
			routes["limit_hit"] = true
		}
		entry := gin.H{"id": id, "name": cfgName[id], "type": strings.ToLower(p.Proto), "state": birdState(p), "routes": routes}
		if p.Info != "" {
			entry["info"] = p.Info
		}
		if p.LastError != "" {
			entry["last_error"] = p.LastError
		}
		if a := announced[n]; len(a) > 0 {
			entry["announced"] = a
		}
		protocols = append(protocols, entry)
	}
	body["protocols"] = protocols
	c.JSON(200, body)
}

// ListRoutes implements GET /routing/routes: the routes of Chaos Gateway's table 100 with their origin.
func (s *Server) ListRoutes(c *gin.Context, params model.ListRoutesParams) {
	out, err := s.cfg.Exec.Do(contextOf(c), &executor.Read{Target: executor.Target{NS: s.cfg.Namespace}, What: executor.ReadRoutes, Table: strconv.Itoa(compiler.PolicyTable)})
	if err != nil || len(out.Data) == 0 {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "cannot read the routes: %v", err))
		return
	}
	var routes []linux.Route
	if err := jsonUnmarshal(out.Data[0], &routes); err != nil {
		s.fail(c, err)
		return
	}
	snap := s.cfg.Engine.Snapshot()
	ifaceProto := map[string]gin.H{}
	if snap.Bird != nil && snap.Config != nil && snap.Config.Routing != nil && snap.Config.Routing.Protocols != nil {
		byName := map[string]string{}
		for id, p := range *snap.Config.Routing.Protocols {
			byName[bird.ProtocolName(string(p.Type), p.Name)] = id
		}
		for _, p := range snap.Bird.Config.Protocols {
			ifaceProto[p.Interface] = gin.H{"id": byName[p.Name], "name": p.Name}
		}
	}
	networkOf := map[string]string{}
	for _, b := range snap.Bridges {
		networkOf[b.Name] = b.NetworkID
	}
	for _, w := range snap.WireGuardInterfaces {
		networkOf[w.Name] = w.NetworkID
	}
	type entry struct {
		key  string
		body gin.H
	}
	var all []entry
	for _, r := range routes {
		if r.Dst == "" || r.Dst == "default" {
			if r.Dst == "default" {
				r.Dst = "0.0.0.0/0"
			} else {
				continue
			}
		}
		dst := r.Dst
		if !strings.Contains(dst, "/") {
			dst += "/32"
		}
		origin := "static"
		switch {
		case r.Protocol == "bird" || r.Protocol == "12":
			origin = "learned"
		case r.Scope == "link" && r.Gateway == "":
			origin = "connected"
		}
		e := gin.H{"destination": dst, "table": compiler.PolicyTable, "origin": origin}
		if r.Gateway != "" {
			e["gateway"] = r.Gateway
		}
		if r.Dev != "" {
			e["interface"] = r.Dev
			if n := networkOf[r.Dev]; n != "" {
				e["network"] = n
			}
		}
		if r.Metric != nil {
			e["metric"] = *r.Metric
		}
		if origin == "learned" {
			if p, ok := ifaceProto[r.Dev]; ok {
				e["protocol"] = p
			}
		}
		if params.Origin != nil && string(*params.Origin) != origin {
			continue
		}
		if params.Protocol != nil {
			p, ok := e["protocol"].(gin.H)
			if !ok || (p["id"] != *params.Protocol && !strings.EqualFold(fmt.Sprint(p["name"]), *params.Protocol) && !strings.EqualFold(strings.TrimPrefix(fmt.Sprint(p["name"]), "bgp_"), *params.Protocol)) {
				continue
			}
		}
		all = append(all, entry{key: dst + "\x00" + r.Dev, body: e})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].key < all[j].key })
	items, next, perr := page(all, func(e entry) string { return e.key }, params.Cursor, params.Limit)
	if perr != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", perr))
		return
	}
	bodies := make([]gin.H, 0, len(items))
	for _, e := range items {
		bodies = append(bodies, e.body)
	}
	c.JSON(200, listBody(bodies, next))
}
