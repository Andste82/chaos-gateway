package api

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/ntpserver"
)

// This file is the API of the NTP server (plan §2.18, M34): what it fetches (its configuration by long poll), what it
// reports (the log of the queries it answered) and what the API makes of that: the device of a query, the counter of the
// time fault that shaped it, the events and the state per device that the reads and the checks of the later stages use.

// ntpLogSize is how many query-log entries the API keeps in memory: what the wait and the check of a run look at.
const ntpLogSize = 20000

// ntpUnownedLogSize is how many of those entries may be of clients that no device owns (yet): a client in a test network that
// sends from many source addresses must not push the queries of the devices out of the log. The oldest of them go first.
const ntpUnownedLogSize = 2000

// ntpPoll are the long poll's wait and the interval at which it looks for a new configuration.
var ntpPoll = struct{ wait, tick time.Duration }{30 * time.Second, 100 * time.Millisecond}

// ntpEventEvery is how often ntp_answered and ntp_query_seen are sent for one device (and fault): a device that polls
// once a second, or a test client in a loop, must not flood the stream (plan §2.18: at most one per device and fault
// every 10 s).
const ntpEventEvery = faultEventEvery

// ntpAskedFor is how long the server a device asked is taken from one reading of the flows: a reading is a read of the
// conntrack table, which the log of a busy server must not trigger for every batch.
const ntpAskedFor = 10 * time.Second

// ntpFlowsEvery is the least time between two reads of the conntrack table for the servers devices asked, whatever the
// number of clients that need a lookup: a client that sends from many source addresses must not keep the executor busy with
// dumps of the table.
const ntpFlowsEvery = time.Second

// ntpServerUnknown is what a device is shown to have asked when nothing says which server it was: a redirected query
// whose destination no fault names and the observed flows do not settle (decision D36).
const ntpServerUnknown = "an external server (redirected)"

// ntpRecentPerDevice is how many entries of its own log the API keeps for each device (GET /devices/{id}/ntp): a device that
// polls once an hour keeps its history when another one polls every second and pushes it out of the shared log.
const ntpRecentPerDevice = 50

// ntpMaxDevices bounds the per-device state and the maps of the event limits: each is cleaned of what is old when it is full.
const ntpMaxDevices = 8192

type ntpState struct {
	mu sync.Mutex
	// gen counts configurations; it starts at the boot time in milliseconds, so a server that holds the generation of an
	// earlier start never waits for a smaller number.
	gen  int64
	hash string
	cur  *model.NtpServiceConfig
	// of is the snapshot cur was computed from
	of *engine.Snapshot

	// lastPollAt is when the server last asked for its configuration: the health check uses it to tell whether the
	// service is still polling.
	lastPollAt atomic.Int64 // UnixNano, 0 before the first poll

	logMu   sync.Mutex
	seq     int64
	entries []loggedNTP // oldest first, bounded
	// unowned is set when entries are in the log for clients that no device owns; adoptOf is the snapshot the owners of the
	// log were last looked up in (ntpAdopt)
	unowned bool
	adoptOf *engine.Snapshot
	// hits counts the queries each time fault shaped (by overlay id): the counter of a time overlay and of the time part
	// of a profile. It lives in memory, like the log; epoch changes with every start of the API.
	hits  map[string]int64
	epoch int64
	// hitsOf is the snapshot hits and the answers per fault of the devices were last pruned to
	hitsOf *engine.Snapshot
	// sent is when an event of a kind was last sent for a device (and fault), for the limit of ntpEventEvery; nts is the
	// devices that announced NTS key establishment, which is sent once
	sent map[string]time.Time
	nts  map[string]bool
	// devices is what the API knows of each device's use of the server
	devices map[string]*ntpDevice
	// asked is the server each client was last found to ask, from the observed flows
	asked map[netip.Addr]askedServer

	// flowsMu serializes the reads of the flows and guards flowsAt and flowsRead: the NTP flows of the last read of the
	// conntrack table, shared by every batch for ntpFlowsEvery (the executor dumps the table for each read)
	flowsMu   sync.Mutex
	flowsAt   time.Time
	flowsRead map[netip.Addr]map[string]bool
}

// loggedNTP is a query as the API keeps it: the report and what the API made of it.
type loggedNTP struct {
	seq    int64
	e      model.NtpQueryLogEntry
	client netip.Addr
	device string
}

// askedServer is the server a client was found to ask and when that was read.
type askedServer struct {
	at     time.Time
	server string // empty: the flows did not settle it
}

// ntpDevice is the use a device made of the NTP server since the API started (the per-device counters of plan §2.18).
type ntpDevice struct {
	// Queries counts the client queries of the device; Answers those shaped by each time fault (by overlay id); Silenced
	// those a fault left unanswered, Dropped the entries over the rate limit: the server logs them sampled to one per second per
	// device, so this is not the number of queries it dropped.
	Queries, Silenced, Dropped int64
	Answers                    map[string]int64
	// NTSKE counts the connections to the key-establishment port (TCP 4460) that the server cannot answer.
	NTSKE int64
	// Last* is the last query that was answered or deliberately not: when, with which answer, the time served and how
	// far it was from the correct time, the fault that shaped it and the server the device asked.
	LastQuery    time.Time
	LastAnswer   string
	LastServed   *time.Time
	LastOffsetMs *int64
	LastFault    string
	LastServer   string
	// recent is the device's newest log entries, at most ntpRecentPerDevice (the entries over the rate limit and the NTS
	// connections included: they are what the device did). It is a ring once it is full: next is the place of the oldest
	// entry, which the next one replaces, so a busy device costs no copy per query.
	recent []model.NtpQueryLogEntry
	next   int
}

// addRecent keeps an entry in the device's ring.
func (d *ntpDevice) addRecent(e model.NtpQueryLogEntry) {
	if len(d.recent) < ntpRecentPerDevice {
		d.recent = append(d.recent, e)
		return
	}
	d.recent[d.next] = e
	d.next = (d.next + 1) % ntpRecentPerDevice
}

// recentOldestFirst returns a copy of the ring in the order of arrival.
func (d *ntpDevice) recentOldestFirst() []model.NtpQueryLogEntry {
	out := make([]model.NtpQueryLogEntry, 0, len(d.recent))
	out = append(out, d.recent[d.next:]...)
	return append(out, d.recent[:d.next]...)
}

// ntpLastPoll is when the NTP server last asked for its configuration, zero before the first poll.
func (s *Server) ntpLastPoll() time.Time {
	if ns := s.ntp.lastPollAt.Load(); ns != 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// ntpNetwork is a network of the server's configuration: the gateway address a client of it believes its time comes from
// (the reference id of the answer) and the range of its clients.
type ntpNetwork struct {
	Network string `json:"network"`
	Gateway string `json:"gateway"`
	Clients string `json:"clients"`
}

// ntpNetworks lists the test networks of the applied target, the bridges and the WireGuard interfaces (a WireGuard client
// reaches the server on the tunnel address like a device of a bridge), sorted by network so that the content, and with it the
// generation of the configuration, does not depend on the order the engine lists them in. A network has one entry for its own
// range and one for each network behind it (the downstream routes of a bridge, the routes of the peers of a tunnel), because a
// device behind a router is served like one of the network and its reference id is the gateway it queries.
func ntpNetworks(snap *engine.Snapshot) []ntpNetwork {
	nets := []ntpNetwork{}
	add := func(network string, gateway netip.Prefix, behind []string) {
		covers := []netip.Prefix{gateway.Masked()}
		for _, r := range behind {
			if p, err := netip.ParsePrefix(r); err == nil {
				covers = append(covers, p.Masked())
			} else if a, err := netip.ParseAddr(r); err == nil {
				covers = append(covers, netip.PrefixFrom(a, a.BitLen()))
			}
		}
		for _, c := range domain.CollapsePrefixes(covers) {
			nets = append(nets, ntpNetwork{Network: network, Gateway: gateway.Addr().String(), Clients: c.String()})
		}
	}
	for _, b := range snap.Bridges {
		add(b.NetworkID, b.Address, b.Routes)
	}
	for _, w := range snap.WireGuardInterfaces {
		var behind []string
		for _, p := range w.Peers {
			// the routes and not the allowed addresses: a link may allow 0.0.0.0/0, which is no network behind it
			behind = append(behind, p.Routes...)
		}
		add(w.NetworkID, w.Address, behind)
	}
	sort.SliceStable(nets, func(i, j int) bool {
		if nets[i].Network != nets[j].Network {
			return nets[i].Network < nets[j].Network
		}
		return nets[i].Clients < nets[j].Clients
	})
	return nets
}

// ntpConfig computes what the server needs from the engine's snapshot: the networks with their gateway address and
// client range, the resolved time faults for each group of sources and each port, and the ports to listen on. The
// generation changes with the content.
func (s *Server) ntpConfig() *model.NtpServiceConfig {
	snap := s.cfg.Engine.Snapshot()
	s.ntp.mu.Lock()
	if s.ntp.of == snap && s.ntp.cur != nil {
		cur := s.ntp.cur
		s.ntp.mu.Unlock()
		return cur
	}
	s.ntp.mu.Unlock()

	nets := ntpNetworks(snap)

	faults := make([]any, 0, len(snap.NTPFaults))
	for _, f := range snap.NTPFaults {
		raw, _ := json.Marshal(f.Fault)
		var entry map[string]any
		_ = json.Unmarshal(raw, &entry)
		srcs := make([]string, len(f.Sources))
		for i, p := range f.Sources {
			srcs[i] = p.String()
		}
		entry["overlay"], entry["since"], entry["sources"], entry["port"] = f.Overlay, f.Since, srcs, f.Port
		faults = append(faults, entry)
	}
	// the base port first, then the ports of the destination selectors in use; the server follows this list
	listen := append([]int{compiler.ServiceNTPPort}, snap.NTPRedirectPorts...)
	sort.Ints(listen)
	doc := map[string]any{"networks": nets, "faults": faults, "listen": listen}
	raw, _ := json.Marshal(doc)
	sum := string(raw)

	s.ntp.mu.Lock()
	defer s.ntp.mu.Unlock()
	s.ntp.of = snap
	if s.ntp.cur == nil || s.ntp.hash != sum {
		if s.ntp.gen == 0 {
			s.ntp.gen = s.clk.Now().UnixMilli()
		} else {
			s.ntp.gen++
		}
		doc["generation"] = s.ntp.gen
		full, _ := json.Marshal(doc)
		var cfg model.NtpServiceConfig
		if err := json.Unmarshal(full, &cfg); err != nil {
			s.log.Error("the NTP configuration does not fit its schema", "error", err)
			return s.ntp.cur
		}
		s.ntp.cur, s.ntp.hash = &cfg, sum
	}
	return s.ntp.cur
}

// GetNtpServiceConfig implements GET /internal/ntp/config: the configuration at once when its generation is newer than
// `after`, else a long poll that ends with 204.
func (s *Server) GetNtpServiceConfig(c *gin.Context, params model.GetNtpServiceConfigParams) {
	s.ntp.lastPollAt.Store(s.clk.Now().UnixNano())
	deadline := s.clk.Now().Add(ntpPoll.wait)
	for {
		cfg := s.ntpConfig()
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
		case <-s.clk.After(ntpPoll.tick):
		}
	}
}

// PostNtpQueries implements POST /internal/ntp/queries: a batch of the server's query log. The device is added here, from
// the observed state: the server only knows addresses. Each query that a time fault shaped is counted for the fault, and
// the events are sent after the lock is released.
func (s *Server) PostNtpQueries(c *gin.Context) {
	var batch []model.NtpQueryLogEntry
	if p := decodeJSON(c, &batch); p != nil {
		s.write(c, p)
		return
	}
	if len(batch) > 10000 {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "at most 10000 entries per request"))
		return
	}
	logged := make([]loggedNTP, 0, len(batch))
	for i, e := range batch {
		client, err := netip.ParseAddr(e.Client)
		if err != nil || !client.Is4() || e.Time.IsZero() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "entry %d needs a time and the client's IPv4 address", i))
			return
		}
		if e.Port < 1 || e.Port > 65535 {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "entry %d: port %d is not a port", i, e.Port))
			return
		}
		if e.Kind != nil && !e.Kind.Valid() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "entry %d: unknown kind %q", i, *e.Kind))
			return
		}
		if e.Answer != nil && !e.Answer.Valid() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "entry %d: unknown answer %q", i, *e.Answer))
			return
		}
		logged = append(logged, loggedNTP{e: e, client: client.Unmap()})
	}
	snap := s.cfg.Engine.Snapshot()
	for i := range logged {
		l := &logged[i]
		l.e.Device = nil
		if dev, ok := snap.Identity.OwnerOf(l.client); ok {
			l.setDevice(dev)
		}
	}
	logged = ntpOurs(snap, logged)
	s.ntpServersAsked(contextOf(c), snap, logged)

	var events []ntpEvent
	s.ntp.logMu.Lock()
	now := s.clk.Now()
	for i := range logged {
		l := &logged[i]
		s.ntp.seq++
		l.seq = s.ntp.seq
		s.ntp.entries = append(s.ntp.entries, *l)
		if l.device == "" {
			s.ntp.unowned = true
		}
		events = append(events, s.noteNTP(snap, l, now)...)
	}
	s.trimNTPLog()
	s.ntp.logMu.Unlock()
	for _, ev := range events {
		s.cfg.Engine.Emit(ev.typ, ev.data)
	}
	c.Status(204)
}

// setDevice makes a device the owner of the query.
func (l *loggedNTP) setDevice(dev string) {
	l.device = strings.ToLower(dev)
	l.e.Device = nil
	if u, err := uuid.Parse(dev); err == nil {
		l.e.Device = &u
	}
}

// ntpOurs keeps the queries of the devices and of the clients in the range of a test network: the server answers no other
// client, and an entry for any other address (the log takes what it is sent) is neither logged nor counted nor announced.
func ntpOurs(snap *engine.Snapshot, batch []loggedNTP) []loggedNTP {
	var nets []netip.Prefix
	for _, n := range ntpNetworks(snap) {
		if p, err := netip.ParsePrefix(n.Clients); err == nil {
			nets = append(nets, p)
		}
	}
	return slices.DeleteFunc(batch, func(l loggedNTP) bool {
		return l.device == "" && !slices.ContainsFunc(nets, func(p netip.Prefix) bool { return p.Contains(l.client) })
	})
}

// trimNTPLog keeps the log to ntpLogSize entries, of which at most ntpUnownedLogSize are of clients that no device owns:
// the oldest go first. Called with logMu held.
func (s *Server) trimNTPLog() {
	e := s.ntp.entries
	skip := -ntpUnownedLogSize // the unowned entries to leave out, the oldest
	for i := range e {
		if e[i].device == "" {
			skip++
		}
	}
	skip = max(skip, 0)
	over := len(e) - skip - ntpLogSize
	if skip == 0 && over <= 0 {
		return
	}
	out := make([]loggedNTP, 0, len(e)-skip)
	for _, l := range e {
		if l.device == "" && skip > 0 {
			skip--
			continue
		}
		out = append(out, l)
	}
	if over > 0 {
		out = out[over:]
	}
	s.ntp.entries = slices.Clone(out)
}

// ntpAdopt gives the owner to the queries of a client that no device owned when they arrived: the gateway may observe a
// device after its first packet, and that query must still be the device's for the wait, the check and the reads. It moves
// the counters kept for the address to the device and puts the entries into its recent ones. It looks again only for a new
// snapshot or new unowned entries. Called with logMu held.
func (s *Server) ntpAdopt(snap *engine.Snapshot) {
	if snap == nil || (s.ntp.adoptOf == snap && !s.ntp.unowned) {
		return
	}
	s.ntp.adoptOf, s.ntp.unowned = snap, false
	owners := map[netip.Addr]string{} // empty: none
	ownerOf := func(a netip.Addr) string {
		dev, ok := owners[a]
		if !ok {
			dev, _ = snap.Identity.OwnerOf(a)
			owners[a] = dev
		}
		return dev
	}
	adopted := map[string][]model.NtpQueryLogEntry{}
	for i := range s.ntp.entries {
		l := &s.ntp.entries[i]
		if l.device != "" {
			continue
		}
		if dev := ownerOf(l.client); dev != "" {
			l.setDevice(dev)
			adopted[l.device] = append(adopted[l.device], l.e)
		} else {
			s.ntp.unowned = true
		}
	}
	now := s.clk.Now()
	var moved []netip.Addr
	for key := range s.ntp.devices {
		if a, err := netip.ParseAddr(key); err == nil && ownerOf(a) != "" {
			moved = append(moved, a)
		}
	}
	for _, a := range moved {
		d := s.ntp.devices[a.String()]
		key := strings.ToLower(ownerOf(a))
		delete(s.ntp.devices, a.String())
		s.ntpDeviceOf(key, now).absorb(d)
		if s.ntp.nts[a.String()] {
			delete(s.ntp.nts, a.String())
			if s.ntp.nts[key] || len(s.ntp.nts) < ntpMaxDevices {
				s.ntp.nts[key] = true
			}
		}
	}
	for key, es := range adopted {
		s.ntpDeviceOf(key, now).addRecentOlder(es)
	}
}

// absorb adds the counters of another state (the one kept for the address of the device before it was known) to the device's.
func (d *ntpDevice) absorb(o *ntpDevice) {
	d.Queries, d.Silenced, d.Dropped, d.NTSKE = d.Queries+o.Queries, d.Silenced+o.Silenced, d.Dropped+o.Dropped, d.NTSKE+o.NTSKE
	for k, v := range o.Answers {
		if d.Answers == nil {
			d.Answers = map[string]int64{}
		}
		d.Answers[k] += v
	}
	if o.LastQuery.After(d.LastQuery) {
		d.LastQuery, d.LastAnswer, d.LastServed, d.LastOffsetMs = o.LastQuery, o.LastAnswer, o.LastServed, o.LastOffsetMs
		d.LastFault, d.LastServer = o.LastFault, o.LastServer
	}
}

// addRecentOlder puts entries that arrived before the ones in the ring among them, by time, and keeps the newest.
func (d *ntpDevice) addRecentOlder(es []model.NtpQueryLogEntry) {
	all := append(d.recentOldestFirst(), es...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })
	if len(all) > ntpRecentPerDevice {
		all = all[len(all)-ntpRecentPerDevice:]
	}
	d.recent, d.next = all, 0
}

// ntpEvent is an event to send once the lock is released.
type ntpEvent struct {
	typ  string
	data map[string]any
}

// isNTSKE says whether an entry reports a connection to the key-establishment port instead of a query.
func isNTSKE(e model.NtpQueryLogEntry) bool {
	return e.Kind != nil && *e.Kind == model.NtsKe
}

// noteNTP adds one logged entry to the counters and the state of its device, and returns the events it gives rise to.
// Called with logMu held.
func (s *Server) noteNTP(snap *engine.Snapshot, l *loggedNTP, now time.Time) []ntpEvent {
	e := l.e
	key := l.device
	if key == "" {
		key = l.client.String()
	}
	d := s.ntpDeviceOf(key, now)
	if l.device != "" { // a client no device owns has counters but no history: a flood of source addresses must not cost memory
		d.addRecent(e)
	}
	if isNTSKE(e) {
		d.NTSKE++
		if s.ntp.nts[key] {
			return nil
		}
		if s.ntp.nts == nil {
			s.ntp.nts = map[string]bool{}
		}
		if len(s.ntp.nts) < ntpMaxDevices {
			s.ntp.nts[key] = true
		}
		return []ntpEvent{{engine.EventNTSSeen, s.ntpEventData(snap, l, nil)}}
	}
	answer := ""
	if e.Answer != nil {
		answer = string(*e.Answer)
	}
	if e.Answer != nil && *e.Answer == model.NtpQueryLogEntryAnswerDropped {
		d.Dropped++
		return nil
	}
	d.Queries++
	d.LastQuery, d.LastAnswer, d.LastServed, d.LastOffsetMs = e.Time, answer, e.ServedTime, e.OffsetMs
	d.LastFault, d.LastServer = "", ntpServerUnknown
	if e.Server != nil && *e.Server != "" {
		d.LastServer = *e.Server
	}
	if e.Answer != nil && *e.Answer == model.NtpQueryLogEntryAnswerSilence {
		d.Silenced++
	}
	var out []ntpEvent
	if e.Fault != nil {
		id := strings.ToLower(e.Fault.String())
		s.countNTP(id)
		if d.Answers == nil {
			d.Answers = map[string]int64{}
		}
		d.Answers[id]++
		d.LastFault = id
		if s.ntpMayEmit("answered|"+id+"|"+key, now) {
			data := s.ntpEventData(snap, l, e.Answer)
			data["overlay"] = id
			if e.OffsetMs != nil {
				data["offset_ms"] = *e.OffsetMs
			}
			out = append(out, ntpEvent{engine.EventNTPAnswered, data})
		}
	}
	if s.ntpMayEmit("seen|"+key, now) {
		out = append(out, ntpEvent{engine.EventNTPQuerySeen, s.ntpEventData(snap, l, nil)})
	}
	return out
}

// ntpEventData is what the events of the NTP server have in common: the client, the device when it is known and the port
// the query arrived on.
func (s *Server) ntpEventData(snap *engine.Snapshot, l *loggedNTP, answer *model.NtpQueryLogEntryAnswer) map[string]any {
	data := map[string]any{"client": l.client.String(), "port": l.e.Port}
	if answer != nil {
		data["answer"] = string(*answer)
	}
	if l.device != "" {
		data["device"] = l.device
		name := s.deviceName(snap, l.device)
		data["device_name"] = name
		data["subject"] = engine.Subject{Kind: "device", ID: l.device, Name: name}
	}
	return data
}

// deviceName is the name of a device as the configuration and the observed state have it, else its id.
func (s *Server) deviceName(snap *engine.Snapshot, device string) string {
	name := device
	if snap.Config != nil {
		if d, ok := deref(snap.Config.Devices)[device]; ok {
			name = d.Name
		}
	}
	for _, d := range snap.Devices {
		if strings.EqualFold(d.ID, device) {
			name = d.Name
		}
	}
	return name
}

// ntpMayEmit says whether an event of the key may be sent now, and notes that it was. Called with logMu held.
func (s *Server) ntpMayEmit(key string, now time.Time) bool {
	if t, ok := s.ntp.sent[key]; ok && now.Sub(t) < ntpEventEvery {
		return false
	}
	if s.ntp.sent == nil {
		s.ntp.sent = map[string]time.Time{}
	}
	if len(s.ntp.sent) >= ntpMaxDevices {
		for k, t := range s.ntp.sent {
			if now.Sub(t) >= ntpEventEvery {
				delete(s.ntp.sent, k)
			}
		}
		if len(s.ntp.sent) >= ntpMaxDevices {
			return false // everything is fresh: a flood of different devices is not announced
		}
	}
	s.ntp.sent[key] = now
	return true
}

// ntpDeviceOf returns the state of a device (or of a client no device owns), made when it is not there. Called with logMu
// held.
func (s *Server) ntpDeviceOf(key string, now time.Time) *ntpDevice {
	if d := s.ntp.devices[key]; d != nil {
		return d
	}
	if s.ntp.devices == nil {
		s.ntp.devices = map[string]*ntpDevice{}
	}
	if len(s.ntp.devices) >= ntpMaxDevices {
		// forget the devices that asked least recently, a quarter of them
		type aged struct {
			key string
			at  time.Time
		}
		all := make([]aged, 0, len(s.ntp.devices))
		for k, d := range s.ntp.devices {
			all = append(all, aged{k, d.LastQuery})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
		for _, a := range all[:len(all)/4] {
			delete(s.ntp.devices, a.key)
		}
	}
	d := &ntpDevice{}
	s.ntp.devices[key] = d
	return d
}

// countNTP counts a query that a time fault shaped. Called with logMu held.
func (s *Server) countNTP(overlay string) {
	if s.ntp.hits == nil {
		s.ntp.hits = map[string]int64{}
		s.ntp.epoch = s.clk.Now().UnixMilli()
	}
	s.ntp.hits[strings.ToLower(overlay)]++
}

// ntpHitsSnapshot copies the counters of the overlays in the snapshot's list of time faults and their epoch; the counters
// of overlays that are not in the store any more are forgotten when the snapshot is a new one (P5-AUDIT-15).
func (s *Server) ntpHitsSnapshot(snap *engine.Snapshot) (map[string]int64, int64) {
	s.ntp.logMu.Lock()
	defer s.ntp.logMu.Unlock()
	if s.ntp.epoch == 0 {
		s.ntp.epoch = s.clk.Now().UnixMilli()
	}
	if snap == nil {
		return nil, s.ntp.epoch
	}
	s.pruneToSnapshot(snap)
	out := make(map[string]int64, len(snap.NTPFaults))
	for _, f := range snap.NTPFaults {
		id := strings.ToLower(f.Overlay)
		if v, ok := s.ntp.hits[id]; ok {
			out[id] = v
		}
	}
	return out, s.ntp.epoch
}

// pruneToSnapshot forgets the counters of the overlays that are not in the snapshot any more, and the answers per fault of the
// devices for them (P5-AUDIT-15), once for each snapshot. Called with logMu held.
func (s *Server) pruneToSnapshot(snap *engine.Snapshot) {
	if s.ntp.hitsOf == snap {
		return
	}
	s.ntp.hitsOf = snap
	pruneHits(s.ntp.hits, snap.Overlays)
	in := make(map[string]bool, len(snap.Overlays))
	for _, o := range snap.Overlays {
		in[strings.ToLower(o.Id.String())] = true
	}
	for _, d := range s.ntp.devices {
		for id := range d.Answers {
			if !in[id] {
				delete(d.Answers, id)
			}
		}
	}
}

// ntpQueries returns the queries of the devices (UUIDs) at or after the time, oldest first: what the wait and the check
// of a run look at. Queries over the rate limit and connections to the key-establishment port are not queries.
func (s *Server) ntpQueries(devices []string, since time.Time) []loggedNTP {
	want := map[string]bool{}
	for _, d := range devices {
		want[strings.ToLower(d)] = true
	}
	var out []loggedNTP
	s.ntp.logMu.Lock()
	defer s.ntp.logMu.Unlock()
	s.ntpAdopt(s.cfg.Engine.Snapshot())
	for _, l := range s.ntp.entries {
		if l.device == "" || !want[l.device] || l.e.Time.Before(since) || isNTSKE(l.e) {
			continue
		}
		if l.e.Answer != nil && *l.e.Answer == model.NtpQueryLogEntryAnswerDropped {
			continue
		}
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].e.Time.Before(out[j].e.Time) })
	return out
}

// ntpDeviceState returns a copy of what the API knows of a device's use of the NTP server (the UUID), false for a device
// that never asked.
func (s *Server) ntpDeviceState(device string) (ntpDevice, bool) {
	s.ntp.logMu.Lock()
	defer s.ntp.logMu.Unlock()
	snap := s.cfg.Engine.Snapshot()
	s.ntpAdopt(snap)
	if snap != nil {
		s.pruneToSnapshot(snap)
	}
	d, ok := s.ntp.devices[strings.ToLower(device)]
	if !ok {
		return ntpDevice{}, false
	}
	out := *d
	out.recent, out.next = d.recentOldestFirst(), 0
	out.Answers = make(map[string]int64, len(d.Answers))
	for k, v := range d.Answers {
		out.Answers[k] = v
	}
	return out, true
}

// ntpServersAsked fills in the server a query was meant for where the server did not know it (a destination that no fault
// names, or names with a prefix or a host name): from the observed flows, which hold every connection redirected to the
// NTP server with the address it was sent to. It is best effort (decision D36): a client with one such flow asked that
// server, with several or none the server stays unknown. The flows are one read of the conntrack table for the clients of a
// batch, and a client's answer is kept for ntpAskedFor. The batch holds only the clients of the test networks (ntpOurs): the
// flows of any other address say nothing the log needs.
func (s *Server) ntpServersAsked(ctx context.Context, snap *engine.Snapshot, batch []loggedNTP) {
	now := s.clk.Now()
	need := map[netip.Addr]bool{}
	s.ntp.logMu.Lock()
	for i := range batch {
		l := &batch[i]
		if isNTSKE(l.e) || (l.e.Server != nil && *l.e.Server != "") || (l.e.Answer != nil && *l.e.Answer == model.NtpQueryLogEntryAnswerDropped) {
			continue
		}
		if a, ok := s.ntp.asked[l.client]; ok && now.Sub(a.at) < ntpAskedFor {
			continue
		}
		need[l.client] = true
	}
	s.ntp.logMu.Unlock()

	found := map[netip.Addr]map[string]bool{}
	fresh := true // false: the flows are from a read that may be older than the flow of a client
	if len(need) > 0 {
		var read map[netip.Addr]map[string]bool
		read, fresh = s.ntpFlows(ctx, now)
		for c := range need {
			found[c] = read[c]
		}
	}

	s.ntp.logMu.Lock()
	defer s.ntp.logMu.Unlock()
	if s.ntp.asked == nil {
		s.ntp.asked = map[netip.Addr]askedServer{}
	}
	if len(s.ntp.asked) >= ntpMaxDevices {
		for k, a := range s.ntp.asked {
			if now.Sub(a.at) >= ntpAskedFor {
				delete(s.ntp.asked, k)
			}
		}
	}
	for c := range need {
		a := askedServer{at: now}
		if set := found[c]; len(set) == 1 {
			for srv := range set {
				a.server = srv
			}
		} else if !fresh {
			// a read that was made before this batch may not have the flow yet: do not settle it for ntpAskedFor
			continue
		}
		if len(s.ntp.asked) < ntpMaxDevices {
			s.ntp.asked[c] = a
		}
	}
	for i := range batch {
		l := &batch[i]
		if isNTSKE(l.e) || (l.e.Server != nil && *l.e.Server != "") {
			continue
		}
		if a, ok := s.ntp.asked[l.client]; ok && a.server != "" {
			srv := a.server
			l.e.Server = &srv
		}
	}
}

// ntpFlows returns the destinations of the NTP flows of each client from a read of the conntrack table, and whether the
// read was made now. A read younger than ntpFlowsEvery is shared (false), a failed read gives nothing.
func (s *Server) ntpFlows(ctx context.Context, now time.Time) (map[netip.Addr]map[string]bool, bool) {
	s.ntp.flowsMu.Lock()
	defer s.ntp.flowsMu.Unlock()
	if !s.ntp.flowsAt.IsZero() && now.Sub(s.ntp.flowsAt) >= 0 && now.Sub(s.ntp.flowsAt) < ntpFlowsEvery {
		return s.ntp.flowsRead, false
	}
	flows, err := s.cfg.Engine.Flows(ctx)
	if err != nil {
		return nil, true
	}
	read := map[netip.Addr]map[string]bool{}
	for _, f := range flows {
		if f.Service != "ntp_server" || f.Protocol != "udp" {
			continue
		}
		if read[f.Src] == nil {
			read[f.Src] = map[string]bool{}
		}
		read[f.Src][netip.AddrPortFrom(f.Dst, uint16(f.DPort)).String()] = true
	}
	s.ntp.flowsAt, s.ntp.flowsRead = now, read
	return read, true
}

// ntpHealth is the component `ntp` of GET /system/health: disabled without a service namespace, degraded while the
// server has not polled for its configuration in the last 60 s. A host clock that is not synchronized does not make the
// component unhealthy (the server is honest about it: stratum 16 and leap indicator 3), but the detail says it.
func (s *Server) ntpHealth(snap *engine.Snapshot) (healthComponent, bool) {
	if snap.Service == nil {
		return healthComponent{Name: "ntp", Status: "disabled"}, false
	}
	c := healthComponent{Name: "ntp", Status: "healthy"}
	degraded := false
	if last := s.ntpLastPoll(); last.IsZero() || s.clk.Now().Sub(last) > 60*time.Second {
		c.Status, c.Detail = "degraded", "the NTP server has not polled for its configuration in the last 60s"
		degraded = true
	}
	if st := s.hostClock().State(); !st.Synchronized {
		note := "the host clock is not synchronized, so the NTP server answers with stratum 16 and leap indicator 3 (start a time service on the host: chrony or systemd-timesyncd)"
		if c.Detail != "" {
			c.Detail += "; " + note
		} else {
			c.Detail = note
		}
	}
	return c, degraded
}

// hostClock is what the API reads of the host's clock: the kernel's own answer unless a test says otherwise.
func (s *Server) hostClock() ntpserver.SyncState {
	if s.cfg.HostClock != nil {
		return s.cfg.HostClock
	}
	return ntpserver.HostSync{}
}
