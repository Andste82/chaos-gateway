package api

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// This file is the API of the TLS responder (plan §2.8, M21): what it fetches (its configuration by long poll, the
// decision of each connection), what it reports (the outcome of each handshake) and the download of the test CA.

// tlsLogSize is how many handshake outcomes the API keeps in memory: what the checks of runs look at.
const tlsLogSize = 20000

// tlsPoll are the long poll's wait and the interval at which it looks for a new configuration.
var tlsPoll = struct{ wait, tick time.Duration }{30 * time.Second, 100 * time.Millisecond}

type tlsState struct {
	mu sync.Mutex
	// gen counts configurations; it starts at the boot time in milliseconds, so a responder that holds the generation of
	// an earlier start never waits for a smaller number.
	gen  int64
	hash string
	cur  *model.TlsServiceConfig
	// of is the snapshot cur was computed from: the decision of every connection asks for the generation, and the cases do not
	// change between two snapshots
	of *engine.Snapshot

	// lastPollAt is when the responder last asked for its configuration: the health check uses it to tell whether
	// the service is still polling.
	lastPollAt atomic.Int64 // UnixNano, 0 before the first poll

	logMu   sync.Mutex
	seq     int64
	entries []loggedHandshake // oldest first, bounded
	// hits counts the handshakes of each overlay (by id): the counter of a TLS overlay and of the tls part of a
	// profile. It lives in memory, like the log; epoch changes with every start of the API.
	hits  map[string]int64
	epoch int64
}

// loggedHandshake is a handshake as the API keeps it: the report and what the API made of it.
type loggedHandshake struct {
	seq    int64
	report model.TlsHandshakeReport
	client netip.Addr
	dst    netip.AddrPort
	device string
}

// tlsLastPoll is when the responder last asked for its configuration, zero before the first poll.
func (s *Server) tlsLastPoll() time.Time {
	if ns := s.tls.lastPollAt.Load(); ns != 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// tlsConfig computes what the responder needs from the engine's snapshot: the cases in the order it should try them for
// each source, with the clients they apply to. The generation changes with the content.
func (s *Server) tlsConfig() *model.TlsServiceConfig {
	snap := s.cfg.Engine.Snapshot()
	s.tls.mu.Lock()
	if s.tls.of == snap && s.tls.cur != nil {
		cur := s.tls.cur
		s.tls.mu.Unlock()
		return cur
	}
	s.tls.mu.Unlock()
	cases := make([]any, 0, len(snap.TLSCases))
	for _, c := range snap.TLSCases {
		raw, _ := json.Marshal(c.Case)
		var entry map[string]any
		_ = json.Unmarshal(raw, &entry)
		srcs := make([]string, len(c.Sources))
		for i, p := range c.Sources {
			srcs[i] = p.String()
		}
		entry["overlay"], entry["sources"], entry["expected"] = c.Overlay, srcs, domain.TLSExpected(c.Case.Case)
		cases = append(cases, entry)
	}
	doc := map[string]any{"cases": cases}
	raw, _ := json.Marshal(doc)
	sum := string(raw)

	s.tls.mu.Lock()
	defer s.tls.mu.Unlock()
	s.tls.of = snap
	if s.tls.cur == nil || s.tls.hash != sum {
		if s.tls.gen == 0 {
			s.tls.gen = s.clk.Now().UnixMilli()
		} else {
			s.tls.gen++
		}
		doc["generation"] = s.tls.gen
		full, _ := json.Marshal(doc)
		var cfg model.TlsServiceConfig
		if err := json.Unmarshal(full, &cfg); err != nil {
			s.log.Error("the TLS configuration does not fit its schema", "error", err)
			return s.tls.cur
		}
		s.tls.cur, s.tls.hash = &cfg, sum
	}
	return s.tls.cur
}

// GetTlsServiceConfig implements GET /internal/tls/config: the configuration at once when its generation is newer than
// `after`, else a long poll that ends with 204.
func (s *Server) GetTlsServiceConfig(c *gin.Context, params model.GetTlsServiceConfigParams) {
	s.tls.lastPollAt.Store(s.clk.Now().UnixNano())
	deadline := s.clk.Now().Add(tlsPoll.wait)
	for {
		cfg := s.tlsConfig()
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
		case <-s.clk.After(tlsPoll.tick):
		}
	}
}

// ResolveTlsConnection implements POST /internal/tls/resolve: the case that decides a connection the responder holds.
func (s *Server) ResolveTlsConnection(c *gin.Context) {
	var req model.TlsResolveRequest
	if p := decodeJSON(c, &req); p != nil {
		s.write(c, p)
		return
	}
	client, err := netip.ParseAddr(req.Client)
	if err != nil || !client.Is4() {
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "client %q is not an IPv4 address", req.Client))
		return
	}
	dst, err := netip.ParseAddrPort(req.OriginalDst)
	if err != nil || !dst.Addr().Is4() || dst.Port() == 0 {
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "original_dst %q is not an IPv4 address and port", req.OriginalDst))
		return
	}
	sni := ""
	if req.Sni != nil {
		sni = strings.ToLower(strings.TrimSuffix(*req.Sni, "."))
	}
	dec, err := s.cfg.Engine.ResolveTLS(engine.TLSConnection{Client: client, Dst: dst, SNI: sni, Names: s.hostNames(client, dst.Addr())})
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "%s", firstLine(err.Error())))
		return
	}
	out := model.TlsResolveResponse{Generation: s.tlsConfig().Generation}
	if dec.Case == nil || dec.Case.TLS == nil {
		out.Passthrough = ptr(dec.Passthrough)
		c.JSON(200, out)
		return
	}
	tc := *dec.Case.TLS
	out.Case = &tc
	if id, err := uuid.Parse(dec.Case.ID); err == nil {
		out.Overlay = &id
	}
	out.Expected = ptr(model.TlsResolveResponseExpected(domain.TLSExpected(tc.Case)))
	if id, err := uuid.Parse(dec.Device); err == nil {
		out.Device = &id
	}
	out.TrustsTestCa = ptr(dec.TrustsTestCA)
	if sni == "" {
		if name := s.latestName(client, dst.Addr()); name != "" {
			out.Name = &name
		}
	}
	c.JSON(200, out)
}

// PostTlsHandshakes implements POST /internal/tls/handshakes: the outcomes of the responder's handshakes. Each is an
// event, a count for its overlay and an entry of the log that the checks of runs read.
func (s *Server) PostTlsHandshakes(c *gin.Context) {
	var batch []model.TlsHandshakeReport
	if p := decodeJSON(c, &batch); p != nil {
		s.write(c, p)
		return
	}
	if len(batch) > 1000 {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "at most 1000 handshakes per request"))
		return
	}
	logged := make([]loggedHandshake, 0, len(batch))
	for i, r := range batch {
		client, err := netip.ParseAddr(r.Client)
		if err != nil || !client.Is4() || r.Time.IsZero() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "handshake %d needs a time and the client's IPv4 address", i))
			return
		}
		dst, err := netip.ParseAddrPort(r.OriginalDst)
		if err != nil {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "handshake %d: original_dst %q is not an address and port", i, r.OriginalDst))
			return
		}
		if !r.Outcome.Valid() {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "handshake %d: unknown outcome %q", i, r.Outcome))
			return
		}
		logged = append(logged, loggedHandshake{report: r, client: client.Unmap(), dst: dst})
	}
	snap := s.cfg.Engine.Snapshot()
	var events []map[string]any
	for i := range logged {
		h := &logged[i]
		if dev, ok := snap.Identity.OwnerOf(h.client); ok {
			h.device = strings.ToLower(dev)
		}
		events = append(events, s.handshakeEvent(snap, *h))
	}
	s.tls.logMu.Lock()
	for _, h := range logged {
		s.tls.seq++
		h.seq = s.tls.seq
		s.tls.entries = append(s.tls.entries, h)
		if h.report.Overlay != nil {
			s.countTLS(h.report.Overlay.String())
		}
	}
	if over := len(s.tls.entries) - tlsLogSize; over > 0 {
		s.tls.entries = append([]loggedHandshake(nil), s.tls.entries[over:]...)
	}
	s.tls.logMu.Unlock()
	for _, ev := range events {
		s.cfg.Engine.Emit(engine.EventTLSHandshake, ev)
	}
	c.Status(204)
}

// handshakeEvent is the event of a handshake (`tls_handshake`): who, where to, what happened and what a correct
// device would have done. `violation` is set when the device completed the handshake with a certificate it must
// reject, which is the failed security check of plan §2.8; `tested` says whether a rejection tests what the case is for
// (a device that does not trust the test CA fails expiry and name cases at the first check, as an unknown issuer).
func (s *Server) handshakeEvent(snap *engine.Snapshot, h loggedHandshake) map[string]any {
	r := h.report
	ev := map[string]any{"client": h.client.String(), "original_dst": h.dst.String(), "outcome": string(r.Outcome)}
	if r.Sni != nil {
		ev["sni"] = *r.Sni
	}
	if r.Overlay != nil {
		ev["overlay"] = r.Overlay.String()
	}
	if r.Alert != nil {
		ev["alert"] = *r.Alert
	}
	if r.TlsVersion != nil {
		ev["tls_version"] = *r.TlsVersion
	}
	trusts := false
	if h.device != "" {
		ev["device"] = h.device
		name := h.device
		if snap.Config != nil {
			if d, ok := deref(snap.Config.Devices)[h.device]; ok {
				name = d.Name
				trusts = d.TrustsTestCa != nil && *d.TrustsTestCa
			}
		}
		for _, d := range snap.Devices {
			if strings.EqualFold(d.ID, h.device) {
				name = d.Name
			}
		}
		ev["device_name"] = name
		ev["subject"] = engine.Subject{Kind: "device", ID: h.device, Name: name}
	}
	if r.Case != nil {
		kind := model.TlsCaseCase(*r.Case)
		ev["case"] = *r.Case
		expected := domain.TLSExpected(kind)
		ev["expected"] = expected
		ev["violation"] = expected == "reject" && r.Outcome == model.TlsHandshakeReportOutcomeAccepted
		ev["tested"] = tlsresponder.IsCertificateCase(kind) && r.Outcome == model.TlsHandshakeReportOutcomeRejected && domain.TLSTested(kind, trusts)
	}
	return ev
}

// countTLS counts a handshake of an overlay. Called with logMu held.
func (s *Server) countTLS(overlay string) {
	if s.tls.hits == nil {
		s.tls.hits = map[string]int64{}
		s.tls.epoch = s.clk.Now().UnixMilli()
	}
	s.tls.hits[strings.ToLower(overlay)]++
}

// tlsHitsSnapshot copies the counters of the TLS overlays and their epoch.
func (s *Server) tlsHitsSnapshot() (map[string]int64, int64) {
	s.tls.logMu.Lock()
	defer s.tls.logMu.Unlock()
	if s.tls.epoch == 0 {
		s.tls.epoch = s.clk.Now().UnixMilli()
	}
	out := make(map[string]int64, len(s.tls.hits))
	for k, v := range s.tls.hits {
		out[k] = v
	}
	return out, s.tls.epoch
}

// tlsHandshakes returns the handshakes of the devices (UUIDs) at or after the time, oldest first.
func (s *Server) tlsHandshakes(devices []string, since time.Time) []loggedHandshake {
	want := map[string]bool{}
	for _, d := range devices {
		want[strings.ToLower(d)] = true
	}
	var out []loggedHandshake
	s.tls.logMu.Lock()
	defer s.tls.logMu.Unlock()
	for _, h := range s.tls.entries {
		if h.device != "" && want[h.device] && !h.report.Time.Before(since) {
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].report.Time.Before(out[j].report.Time) })
	return out
}

// GetTestCa implements GET /tls/ca: the test CA's certificate as a download.
func (s *Server) GetTestCa(c *gin.Context) {
	if s.cfg.TLSCADir == "" {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "the gateway has no directory for its test CA"))
		return
	}
	pem, err := tlsresponder.TestCAPEM(s.cfg.TLSCADir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.write(c, newProblem(model.ErrorCodeUnavailable, "the gateway has not made its test CA yet"))
			return
		}
		s.log.Error("cannot read the test CA", "error", err)
		s.write(c, newProblem(model.ErrorCodeInternal, "the test CA cannot be read"))
		return
	}
	c.Header("Content-Disposition", `attachment; filename="chaos-gateway-test-ca.pem"`)
	c.Data(200, "application/x-pem-file", pem)
}
