package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/dnsproxy"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// dnsLogSize is how many query-log entries the API keeps in memory.
const dnsLogSize = 20000

// dnsPoll are the long poll's wait and the interval at which it looks for a new configuration.
var dnsPoll = struct{ wait, tick time.Duration }{30 * time.Second, 100 * time.Millisecond}

type dnsState struct {
	mu sync.Mutex
	// gen counts configurations; it starts at the boot time in milliseconds, so a proxy that holds
	// the generation of an earlier start never waits for a smaller number.
	gen  int64
	hash string
	cur  *model.DnsServiceConfig

	resolvers   []string
	resolversAt time.Time

	// lastPollAt is when the DNS proxy last asked for its configuration (M6b-05): the health check
	// uses it to tell whether the service is still polling.
	lastPollAt atomic.Int64 // UnixNano, 0 before the first poll

	logMu   sync.Mutex
	seq     int64
	entries []loggedQuery // oldest first, bounded
	// faultEvent is when a DNS fault last announced itself for a device (dns_fault_triggered): at most once
	// per faultEventEvery for each pair, so a proxy that answers hundreds of queries a second does not flood the
	// stream.
	faultEvent map[string]time.Time
	// answered is what the proxy answered the devices, by client and address: the names of the answers (the name asked
	// and the CNAME targets). It is how a check, a wait or a capture that names a host finds the addresses of the
	// name, whether or not a rule or a fault selects it (the DNS-derived sets know only those names).
	answered map[addrPair]*answeredNames
	// faultHits counts the queries each DNS fault changed (by overlay id): the counter of a DNS overlay and of the dns part of
	// a profile. It lives in memory, like the log; epoch changes with every start of the API.
	faultHits map[string]int64
	epoch     int64
	// hitsOf is the snapshot faultHits was last pruned to (pruneHits)
	hitsOf *engine.Snapshot
}

// addrPair is a client and an address it got in an answer.
type addrPair struct{ client, addr netip.Addr }

type answeredNames struct {
	names []string
	last  time.Time
	// latest is the name that was asked in the newest answer for the address: what a TLS case without a server name
	// certifies (plan §2.8, "the name the DNS proxy last returned for the destination address")
	latest string
}

// maxAnswered bounds the index of answers; answeredFor is how long an answer stays in it.
const (
	maxAnswered = 50000
	answeredFor = time.Hour
)

// faultEventEvery is how often dns_fault_triggered is sent for one overlay and device.
const faultEventEvery = 10 * time.Second

type loggedQuery struct {
	seq int64
	e   model.DnsQueryLogEntry
}

// dnsLastPoll is when the DNS proxy last asked for its configuration, zero before the first poll.
func (s *Server) dnsLastPoll() time.Time {
	if ns := s.dns.lastPollAt.Load(); ns != 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
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
	faults := make([]any, 0, len(snap.DNSFaults))
	for _, f := range snap.DNSFaults {
		raw, _ := json.Marshal(f.Fault)
		var entry map[string]any
		_ = json.Unmarshal(raw, &entry)
		srcs := make([]string, len(f.Sources))
		for i, p := range f.Sources {
			srcs[i] = p.String()
		}
		entry["overlay"], entry["sources"] = f.Overlay, srcs
		faults = append(faults, entry)
	}
	sets := make([]any, 0, len(snap.DNSSets))
	for _, ds := range snap.DNSSets {
		if len(ds.Sources) == 0 {
			continue // no client is recorded for this selector yet: an empty list would mean every client to the proxy
		}
		srcs := make([]string, len(ds.Sources))
		for i, p := range ds.Sources {
			srcs[i] = p.String()
		}
		sets = append(sets, map[string]any{"set": ds.ID, "names": []string{ds.Pattern}, "sources": srcs})
	}
	doc := map[string]any{"upstream": upstream, "strip_aaaa": true, "networks": nets, "faults": faults, "hostname_sets": sets}
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
	if !s.dns.resolversAt.IsZero() && s.clk.Now().Sub(s.dns.resolversAt) < 5*time.Second {
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
	s.dns.resolvers, s.dns.resolversAt = out, s.clk.Now()
	return out
}

// GetDnsServiceConfig implements GET /internal/dns/config: the configuration at once when its
// generation is newer than `after`, else a long poll that ends with 204.
func (s *Server) GetDnsServiceConfig(c *gin.Context, params model.GetDnsServiceConfigParams) {
	s.dns.lastPollAt.Store(s.clk.Now().UnixNano())
	deadline := s.clk.Now().Add(dnsPoll.wait)
	for {
		cfg := s.dnsConfig()
		if cfg != nil && (params.After == nil || cfg.Generation > *params.After) {
			c.JSON(200, cfg)
			return
		}
		if !s.clk.Now().Before(deadline) {
			c.Status(204)
			return
		}
		select {
		case <-contextOf(c).Done():
			return
		case <-s.clk.After(dnsPoll.tick):
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
	var events []map[string]any
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
		s.indexAnswer(e)
		if e.Fault != nil {
			s.countFault(e.Fault.String())
			if ev, ok := s.faultTriggered(e); ok {
				events = append(events, ev)
			}
		}
	}
	if over := len(s.dns.entries) - dnsLogSize; over > 0 {
		s.dns.entries = append([]loggedQuery(nil), s.dns.entries[over:]...)
	}
	s.dns.logMu.Unlock()
	for _, ev := range events {
		s.cfg.Engine.Emit(engine.EventDNSFaultTriggered, ev)
	}
	c.Status(204)
}

// faultTriggered makes the event of a logged query that a DNS fault changed, unless the same overlay announced
// itself for the same device a moment ago. Called with logMu held.
func (s *Server) faultTriggered(e model.DnsQueryLogEntry) (map[string]any, bool) {
	dev := e.Client
	if e.Device != nil {
		dev = e.Device.String()
	}
	key := e.Fault.String() + "|" + dev
	now := s.clk.Now()
	if t, ok := s.dns.faultEvent[key]; ok && now.Sub(t) < faultEventEvery {
		return nil, false
	}
	if s.dns.faultEvent == nil {
		s.dns.faultEvent = map[string]time.Time{}
	}
	if len(s.dns.faultEvent) > 4096 {
		for k, t := range s.dns.faultEvent {
			if now.Sub(t) >= faultEventEvery {
				delete(s.dns.faultEvent, k)
			}
		}
	}
	s.dns.faultEvent[key] = now
	ev := map[string]any{"overlay": e.Fault.String(), "name": e.Name, "type": e.Type, "rcode": e.Rcode, "client": e.Client}
	if e.Device != nil {
		ev["device"] = e.Device.String()
	}
	return ev, true
}

// PostDnsResolutions implements POST /internal/dns/resolutions (plan §2.6): the addresses the proxy is about
// to answer a client with, for the names that hostname selectors match. It returns when the kernel's
// DNS-derived sets hold them, so the device's first packet already matches; the engine deduplicates, batches the
// concurrent calls into one executor request and writes beside a running plan (engine.RecordResolutions).
func (s *Server) PostDnsResolutions(c *gin.Context) {
	var batch []model.DnsResolution
	if p := decodeJSON(c, &batch); p != nil {
		s.write(c, p)
		return
	}
	if len(batch) > 1000 {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "at most 1000 resolutions per request"))
		return
	}
	rs := make([]engine.HostResolution, 0, len(batch))
	for i, r := range batch {
		client, err := netip.ParseAddr(r.Client)
		if err != nil || !client.Is4() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "resolution %d: client %q is not an IPv4 address", i, r.Client))
			return
		}
		if r.Set == "" || len(r.Addresses) == 0 || len(r.Addresses) > 256 || r.Ttl < 0 {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "resolution %d: it needs a set, between 1 and 256 addresses and a TTL that is not negative", i))
			return
		}
		hr := engine.HostResolution{Set: r.Set, Client: client.Unmap(), Name: strings.ToLower(r.Name), TTL: time.Duration(r.Ttl) * time.Second}
		for _, a := range r.Addresses {
			addr, err := netip.ParseAddr(a)
			if err != nil || !addr.Is4() {
				s.write(c, newProblem(model.ErrorCodeValidationFailed, "resolution %d: %q is not an IPv4 address", i, a))
				return
			}
			hr.Addrs = append(hr.Addrs, addr)
		}
		rs = append(rs, hr)
	}
	if _, err := s.cfg.Engine.RecordResolutions(contextOf(c), rs); err != nil {
		switch {
		case errors.Is(err, engine.ErrClosed):
			s.write(c, newProblem(model.ErrorCodeUnavailable, "the gateway is stopping"))
		case contextOf(c).Err() != nil:
			return
		default:
			s.write(c, newProblem(model.ErrorCodeUnavailable, "%s", firstLine(err.Error())))
		}
		return
	}
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

// indexAnswer notes the names of an answer for the addresses it holds. Called with logMu held.
func (s *Server) indexAnswer(e model.DnsQueryLogEntry) {
	if e.Rcode != "NOERROR" || e.Answers == nil {
		return
	}
	client, err := netip.ParseAddr(e.Client)
	if err != nil {
		return
	}
	names := []string{strings.ToLower(strings.TrimSuffix(e.Name, "."))}
	var addrs []netip.Addr
	for _, a := range *e.Answers {
		if ip, err := netip.ParseAddr(a); err == nil {
			addrs = append(addrs, ip.Unmap())
		} else if a != "" && !strings.ContainsAny(a, " \t") {
			names = append(names, strings.ToLower(strings.TrimSuffix(a, ".")))
		}
	}
	if len(addrs) == 0 {
		return
	}
	if s.dns.answered == nil {
		s.dns.answered = map[addrPair]*answeredNames{}
	}
	if len(s.dns.answered) >= maxAnswered {
		// drop what is old; if everything is fresh, half of it
		cut := e.Time.Add(-answeredFor)
		for k, v := range s.dns.answered {
			if v.last.Before(cut) {
				delete(s.dns.answered, k)
			}
		}
		for k := range s.dns.answered {
			if len(s.dns.answered) < maxAnswered/2 {
				break
			}
			delete(s.dns.answered, k)
		}
	}
	for _, a := range addrs {
		k := addrPair{client, a}
		v := s.dns.answered[k]
		if v == nil {
			v = &answeredNames{}
			s.dns.answered[k] = v
		}
		for _, n := range names {
			if !slices.Contains(v.names, n) && len(v.names) < 16 {
				v.names = append(v.names, n)
			}
		}
		if !e.Time.Before(v.last) {
			v.last, v.latest = e.Time, names[0]
		}
	}
}

// latestName is the name the gateway's DNS answered the client last for the address, "" for an address it never
// answered the client with. Names that only a DNS-derived set knows (the answers of an earlier start) are not kept: the
// certificate is then for the address.
func (s *Server) latestName(client, addr netip.Addr) string {
	s.dns.logMu.Lock()
	defer s.dns.logMu.Unlock()
	if v := s.dns.answered[addrPair{client, addr}]; v != nil {
		return v.latest
	}
	return ""
}

// hostNames returns the names a client was answered for an address: those of the proxy's answers (the whole
// log of the proxy) and those the DNS-derived sets hold.
func (s *Server) hostNames(client, addr netip.Addr) []string {
	var out []string
	s.dns.logMu.Lock()
	if v := s.dns.answered[addrPair{client, addr}]; v != nil {
		out = append(out, v.names...)
	}
	s.dns.logMu.Unlock()
	for _, n := range s.cfg.Engine.HostNames(client, addr) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// hostAddrs returns the addresses the proxy answered any client for names that the selector matches, sorted: where a
// capture of "traffic to this host" looks.
func (s *Server) hostAddrs(pattern string) []netip.Addr {
	seen := map[netip.Addr]bool{}
	s.dns.logMu.Lock()
	for k, v := range s.dns.answered {
		for _, n := range v.names {
			if domain.HostnameMatches(pattern, n) {
				seen[k.addr] = true
				break
			}
		}
	}
	s.dns.logMu.Unlock()
	for _, a := range s.cfg.Engine.HostAddrs(pattern) {
		seen[a] = true
	}
	out := make([]netip.Addr, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

// countFault counts a query that a DNS fault changed. Called with logMu held.
func (s *Server) countFault(overlay string) {
	if s.dns.faultHits == nil {
		s.dns.faultHits = map[string]int64{}
		s.dns.epoch = s.clk.Now().UnixMilli()
	}
	s.dns.faultHits[strings.ToLower(overlay)]++
}

// dnsHitsSnapshot copies the counters of the overlays in the snapshot's DNS lists and their epoch; the counters of
// overlays that are not in the store any more are forgotten when the snapshot is a new one (P5-AUDIT-15).
func (s *Server) dnsHitsSnapshot(snap *engine.Snapshot) (map[string]int64, int64) {
	s.dns.logMu.Lock()
	defer s.dns.logMu.Unlock()
	if s.dns.epoch == 0 {
		s.dns.epoch = s.clk.Now().UnixMilli()
	}
	if snap == nil {
		return nil, s.dns.epoch
	}
	if s.dns.hitsOf != snap {
		pruneHits(s.dns.faultHits, snap.Overlays)
		s.dns.hitsOf = snap
	}
	out := make(map[string]int64, len(snap.DNSFaults))
	for _, f := range snap.DNSFaults {
		id := strings.ToLower(f.Overlay)
		if v, ok := s.dns.faultHits[id]; ok {
			out[id] = v
		}
	}
	return out, s.dns.epoch
}

// pruneHits forgets the counters of the overlays that are not in the store: an overlay that is there keeps its
// count while it is overridden and in no list, so its counter goes on when it is back (same epoch).
func pruneHits(hits map[string]int64, overlays []model.Overlay) {
	if len(hits) == 0 {
		return
	}
	in := make(map[string]bool, len(overlays))
	for _, o := range overlays {
		in[strings.ToLower(o.Id.String())] = true
	}
	for id := range hits {
		if !in[id] {
			delete(hits, id)
		}
	}
}
