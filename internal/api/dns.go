package api

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/dnsproxy"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// dnsLogSize is how many query-log entries the API keeps in memory.
const dnsLogSize = 20000

// dnsPoll are the long poll's wait and the interval at which it looks for a new configuration.
var dnsPoll = struct{ wait, tick time.Duration }{30 * time.Second, 250 * time.Millisecond}

type dnsState struct {
	mu sync.Mutex
	// gen counts configurations; it starts at the boot time in milliseconds, so a proxy that holds
	// the generation of an earlier start never waits for a smaller number.
	gen  int64
	hash string
	cur  *model.DnsServiceConfig

	resolvers   []string
	resolversAt time.Time

	logMu   sync.Mutex
	seq     int64
	entries []loggedQuery // oldest first, bounded
}

type loggedQuery struct {
	seq int64
	e   model.DnsQueryLogEntry
}

// dnsConfig computes what the proxy needs from the engine's snapshot: the networks with their
// gateway address and client range, their static entries, the upstream resolvers. The generation
// changes with the content.
func (s *Server) dnsConfig() *model.DnsServiceConfig {
	snap := s.cfg.Engine.Snapshot()
	type network struct {
		Network       string                 `json:"network"`
		Gateway       string                 `json:"gateway"`
		Clients       string                 `json:"clients"`
		StaticEntries []model.DnsStaticEntry `json:"static_entries,omitempty"`
	}
	nets := []network{}
	for _, b := range snap.Bridges {
		n := network{Network: b.NetworkID, Gateway: b.Address.Addr().String(), Clients: b.Address.Masked().String()}
		if snap.Config != nil && snap.Config.Networks != nil {
			if cn, ok := (*snap.Config.Networks)[b.NetworkID]; ok {
				if lan, err := cn.AsLanNetwork(); err == nil && lan.Dns != nil && lan.Dns.StaticEntries != nil {
					n.StaticEntries = *lan.Dns.StaticEntries
				}
			}
		}
		nets = append(nets, n)
	}
	for _, w := range snap.WireGuardInterfaces {
		nets = append(nets, network{Network: w.NetworkID, Gateway: w.Address.Addr().String(), Clients: w.Address.Masked().String()})
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Network < nets[j].Network })

	upstream := []string{}
	if snap.Config != nil && snap.Config.Uplink.DnsUpstream != nil {
		upstream = append(upstream, *snap.Config.Uplink.DnsUpstream...)
	}
	if len(upstream) == 0 {
		upstream = append(upstream, s.hostResolvers()...)
	}
	doc := map[string]any{"upstream": upstream, "strip_aaaa": true, "networks": nets, "faults": []any{}, "hostname_sets": []any{}}
	raw, _ := json.Marshal(doc)
	sum := string(raw)

	s.dns.mu.Lock()
	defer s.dns.mu.Unlock()
	if s.dns.cur == nil || s.dns.hash != sum {
		if s.dns.gen == 0 {
			s.dns.gen = s.clk.Now().UnixMilli()
		} else {
			s.dns.gen++
		}
		doc["generation"] = s.dns.gen
		full, _ := json.Marshal(doc)
		var cfg model.DnsServiceConfig
		if err := json.Unmarshal(full, &cfg); err != nil {
			s.log.Error("the DNS configuration does not fit its schema", "error", err)
			return s.dns.cur
		}
		s.dns.cur, s.dns.hash = &cfg, sum
	}
	return s.dns.cur
}

// hostResolvers are the host's resolvers; the files are read at most every 5 s (a long poll asks
// every few hundred milliseconds).
func (s *Server) hostResolvers() []string {
	s.dns.mu.Lock()
	defer s.dns.mu.Unlock()
	if !s.dns.resolversAt.IsZero() && time.Since(s.dns.resolversAt) < 5*time.Second {
		return s.dns.resolvers
	}
	read := s.cfg.Resolvers
	if read == nil {
		read = func() []netip.Addr { return dnsproxy.HostResolvers() }
	}
	var out []string
	for _, a := range read() {
		out = append(out, a.String())
	}
	s.dns.resolvers, s.dns.resolversAt = out, time.Now()
	return out
}

// GetDnsServiceConfig implements GET /internal/dns/config: the configuration at once when its
// generation is newer than `after`, else a long poll that ends with 204.
func (s *Server) GetDnsServiceConfig(c *gin.Context, params model.GetDnsServiceConfigParams) {
	deadline := time.Now().Add(dnsPoll.wait)
	for {
		cfg := s.dnsConfig()
		if cfg != nil && (params.After == nil || cfg.Generation > *params.After) {
			c.JSON(200, cfg)
			return
		}
		if !time.Now().Before(deadline) {
			c.Status(204)
			return
		}
		select {
		case <-contextOf(c).Done():
			return
		case <-time.After(dnsPoll.tick):
		}
	}
}

// PostDnsQueries implements POST /internal/dns/queries: a batch of the proxy's query log. The device
// is added here, from the observed state: the proxy only knows addresses.
func (s *Server) PostDnsQueries(c *gin.Context) {
	var batch []model.DnsQueryLogEntry
	if p := decodeJSON(c, &batch); p != nil {
		s.write(c, p)
		return
	}
	if len(batch) > 10000 {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "at most 10000 entries per request"))
		return
	}
	for _, e := range batch {
		if e.Name == "" || e.Type == "" || e.Rcode == "" || e.Time.IsZero() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "an entry needs time, client, name, type and rcode"))
			return
		}
		if _, err := netip.ParseAddr(e.Client); err != nil {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "client %q is not an address", e.Client))
			return
		}
	}
	id := s.cfg.Engine.Snapshot().Identity
	s.dns.logMu.Lock()
	for _, e := range batch {
		if a, err := netip.ParseAddr(e.Client); err == nil {
			if dev, ok := id.OwnerOf(a); ok {
				if u, err := uuid.Parse(dev); err == nil {
					e.Device = &u
				}
			}
		}
		s.dns.seq++
		s.dns.entries = append(s.dns.entries, loggedQuery{seq: s.dns.seq, e: e})
	}
	if over := len(s.dns.entries) - dnsLogSize; over > 0 {
		s.dns.entries = append([]loggedQuery(nil), s.dns.entries[over:]...)
	}
	s.dns.logMu.Unlock()
	c.Status(204)
}

// ListDnsQueries implements GET /dns/queries: newest first.
func (s *Server) ListDnsQueries(c *gin.Context, params model.ListDnsQueriesParams) {
	wantDevice := ""
	if params.Device != nil {
		_, cfg, ok := s.configAt(c, nil)
		if !ok {
			return
		}
		d, found := findDevice(s.deviceViews(cfg, true), *params.Device)
		if !found {
			s.write(c, notFound("device", *params.Device))
			return
		}
		wantDevice = strings.ToLower(d.ID)
	}
	s.dns.logMu.Lock()
	all := make([]loggedQuery, 0, len(s.dns.entries))
	for i := len(s.dns.entries) - 1; i >= 0; i-- {
		q := s.dns.entries[i]
		if wantDevice != "" && (q.e.Device == nil || strings.ToLower(q.e.Device.String()) != wantDevice) {
			continue
		}
		if params.Name != nil && !nameMatches(*params.Name, q.e.Name) {
			continue
		}
		if params.Since != nil && q.e.Time.Before(*params.Since) {
			continue
		}
		all = append(all, q)
	}
	s.dns.logMu.Unlock()
	items, next, err := page(all, func(q loggedQuery) string { return fmt.Sprintf("%020d", int64(1<<62)-q.seq) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	out := make([]model.DnsQueryLogEntry, 0, len(items))
	for _, q := range items {
		out = append(out, q.e)
	}
	c.JSON(200, listBody(out, next))
}

// nameMatches is an exact name or `*.suffix` (the suffix itself and everything below it).
func nameMatches(pattern, name string) bool {
	pattern, name = strings.ToLower(strings.TrimSuffix(pattern, ".")), strings.ToLower(strings.TrimSuffix(name, "."))
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		return name == rest || strings.HasSuffix(name, "."+rest)
	}
	return name == pattern
}
