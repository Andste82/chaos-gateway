package dnsproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The DNS faults and the hostname sets of the proxy (plan §2.6, M20). The clients are the loopback address the
// test's own sockets have.

const (
	ovA = "01a16ef6-d146-76e3-acc5-71ab136b5616"
	ovB = "01a16ef6-d182-72dd-84aa-3949ea9474c3"
)

// config is baseConfig with the faults and sets of a JSON fragment: `"faults": [...], "hostname_sets": [...]`.
func config(t *testing.T, fragment string) *model.DnsServiceConfig {
	t.Helper()
	cfg := baseConfig()
	var extra struct {
		Generation   json.RawMessage `json:"generation"`
		Faults       json.RawMessage `json:"faults"`
		HostnameSets json.RawMessage `json:"hostname_sets"`
	}
	if err := json.Unmarshal([]byte("{"+fragment+"}"), &extra); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	var doc map[string]json.RawMessage
	_ = json.Unmarshal(raw, &doc)
	if extra.Generation != nil {
		doc["generation"] = extra.Generation
	}
	if extra.Faults != nil {
		doc["faults"] = extra.Faults
	}
	if extra.HostnameSets != nil {
		doc["hostname_sets"] = extra.HostnameSets
	}
	raw, _ = json.Marshal(doc)
	out := &model.DnsServiceConfig{}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func fault(overlay, body string) string {
	return `{"overlay":"` + overlay + `","sources":["127.0.0.1"],` + body + `}`
}

func faults(fs ...string) string { return `"faults":[` + strings.Join(fs, ",") + `]` }

// upstreamA answers every A query with a fresh record (a fault changes the records of an answer; the upstream's own
// must not change with them).
func upstreamA() *fakeUpstream {
	return &fakeUpstream{answer: func(_, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.Response = true
		m.Answer = []dns.RR{aRecord(q.Name, "203.0.113.10", 300)}
		return m, nil
	}}
}

func lastEntry(t *testing.T, sink *memSink, name string) model.DnsQueryLogEntry {
	t.Helper()
	for i := 0; i < 200; i++ {
		var got []model.DnsQueryLogEntry
		for _, e := range sink.all() {
			if e.Name == name {
				got = append(got, e)
			}
		}
		if len(got) > 0 {
			return got[len(got)-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no log entry for %s", name)
	return model.DnsQueryLogEntry{}
}

func runWithLog(t *testing.T, up Exchanger, cfg *model.DnsServiceConfig, mod func(*Options)) (*Server, string, *memSink) {
	t.Helper()
	sink := &memSink{}
	s, addr := running(t, up, cfg, func(o *Options) {
		o.Sink = sink
		if mod != nil {
			mod(o)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunLog(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, addr, sink
}

func TestNXDOMAINAndSERVFAILAnswerWithoutAskingTheUpstreamAndTheLogNamesTheFault(t *testing.T) {
	up := upstreamA()
	_, addr, sink := runWithLog(t, up, config(t, faults(
		fault(ovA, `"action":"nxdomain","names":["gone.test"]`),
		fault(ovB, `"action":"servfail"`))), nil)
	if r := ask(t, addr, "udp", "gone.test", dns.TypeA); r.Rcode != dns.RcodeNameError || len(r.Answer) != 0 {
		t.Errorf("%v", r)
	}
	if r := ask(t, addr, "tcp", "other.test", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Errorf("%v", r)
	}
	if up.count() != 0 {
		t.Errorf("the upstream was asked: %v", up.calls)
	}
	if e := lastEntry(t, sink, "gone.test"); e.Rcode != "NXDOMAIN" || e.Fault == nil || e.Fault.String() != ovA {
		t.Errorf("%+v", e)
	}
	if e := lastEntry(t, sink, "other.test"); e.Rcode != "SERVFAIL" || e.Fault == nil || e.Fault.String() != ovB {
		t.Errorf("%+v", e)
	}
}

func TestOnlyTheClientsAndNamesOfAFaultAreAffectedAndTheFirstEntryWins(t *testing.T) {
	up := upstreamA()
	cfg := config(t, faults(
		`{"overlay":"`+ovA+`","sources":["192.0.2.0/24"],"action":"servfail"}`, // another client
		fault(ovB, `"action":"nxdomain","names":["*.example.test"]`),           // subdomains, not the apex
		fault(ovA, `"action":"servfail"`)))
	_, addr, _ := runWithLog(t, up, cfg, nil)
	if r := ask(t, addr, "udp", "www.example.test", dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Errorf("a subdomain: %v", r)
	}
	// the apex is not matched by *.example.test: the entry after it takes it, for every name
	if r := ask(t, addr, "udp", "example.test", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Errorf("the apex: %v", r)
	}
	// without an entry for the client nothing is changed
	cfg2 := config(t, faults(`{"overlay":"`+ovA+`","sources":["192.0.2.0/24"],"action":"servfail"}`))
	_, addr2, _ := runWithLog(t, up, cfg2, nil)
	if r := ask(t, addr2, "udp", "example.test", dns.TypeA); r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Errorf("%v", r)
	}
}

func TestATimeoutFaultAnswersNothingOverUDPAndTCP(t *testing.T) {
	up := upstreamA()
	s, addr, sink := runWithLog(t, up, config(t, faults(fault(ovA, `"action":"timeout"`))), nil)
	for _, network := range []string{"udp", "tcp"} {
		m := new(dns.Msg)
		m.SetQuestion("example.test.", dns.TypeA)
		c := &dns.Client{Net: network, Timeout: 300 * time.Millisecond}
		_, _, err := c.Exchange(m, addr)
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Errorf("%s: %v, want a timeout", network, err)
		}
	}
	if up.count() != 0 {
		t.Errorf("the upstream was asked: %v", up.calls)
	}
	if e := lastEntry(t, sink, "example.test"); e.Rcode != "TIMEOUT" || e.Fault == nil {
		t.Errorf("%+v", e)
	}
	if s.Queries() != 2 {
		t.Errorf("%d queries", s.Queries())
	}
}

func TestADelayFaultAnswersLaterAndLeavesTheAnswerAlone(t *testing.T) {
	up := upstreamA()
	_, addr, sink := runWithLog(t, up, config(t, faults(fault(ovA, `"action":"delay","delay":"400ms"`))), nil)
	start := time.Now()
	r := ask(t, addr, "udp", "example.test", dns.TypeA)
	if took := time.Since(start); took < 400*time.Millisecond || took > 2*time.Second {
		t.Errorf("answered after %v", took)
	}
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "203.0.113.10" {
		t.Errorf("%v", r)
	}
	if e := lastEntry(t, sink, "example.test"); e.Fault == nil || e.DurationMs == nil || *e.DurationMs < 400 {
		t.Errorf("%+v", e)
	}
}

func TestAWrongAnswerFaultReplacesTheAddressesOfAnAQuery(t *testing.T) {
	up := upstreamA()
	_, addr, sink := runWithLog(t, up, config(t, faults(fault(ovA, `"action":"wrong_answer","answers":["10.99.0.1","10.99.0.2"],"names":["broker.example.test"]`))), nil)
	r := ask(t, addr, "udp", "broker.example.test", dns.TypeA)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 2 || r.Answer[0].(*dns.A).A.String() != "10.99.0.1" || r.Answer[1].(*dns.A).A.String() != "10.99.0.2" {
		t.Fatalf("%v", r)
	}
	// AAAA: the name exists and has no IPv6 address (the V1 networks are IPv4 only)
	if r := ask(t, addr, "udp", "broker.example.test", dns.TypeAAAA); r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Errorf("%v", r)
	}
	// another type is answered as it would have been, and the log does not claim the fault changed it
	if r := ask(t, addr, "udp", "broker.example.test", dns.TypeTXT); r.Rcode != dns.RcodeSuccess {
		t.Errorf("%v", r)
	}
	if up.count() != 1 {
		t.Errorf("only the TXT query goes upstream: %v", up.calls)
	}
	lastEntry(t, sink, "broker.example.test")
	time.Sleep(700 * time.Millisecond) // the log is posted in batches
	for _, e := range sink.all() {
		if e.Type == "TXT" && e.Fault != nil {
			t.Errorf("a query the fault left alone is logged with it: %+v", e)
		}
	}
	// the name of the fault does not reach other names
	if r := ask(t, addr, "udp", "example.test", dns.TypeA); r.Answer[0].(*dns.A).A.String() != "203.0.113.10" {
		t.Errorf("%v", r)
	}
}

func TestATruncationFaultForcesTheFallbackToTCP(t *testing.T) {
	up := upstreamA()
	_, addr, sink := runWithLog(t, up, config(t, faults(fault(ovA, `"action":"truncate"`))), nil)
	udp := ask(t, addr, "udp", "example.test", dns.TypeA)
	if !udp.Truncated || len(udp.Answer) != 0 {
		t.Fatalf("over UDP: truncated=%v, %d answers", udp.Truncated, len(udp.Answer))
	}
	tcp := ask(t, addr, "tcp", "example.test", dns.TypeA)
	if tcp.Truncated || len(tcp.Answer) != 1 {
		t.Fatalf("over TCP: truncated=%v, %d answers", tcp.Truncated, len(tcp.Answer))
	}
	// a client that falls back as it should gets the answer
	m := new(dns.Msg)
	m.SetQuestion("example.test.", dns.TypeA)
	r, err := dnsExchangeWithFallback(addr, m)
	if err != nil || len(r.Answer) != 1 {
		t.Errorf("%v %v", r, err)
	}
	// the log names the fault for the truncated UDP answer only
	time.Sleep(700 * time.Millisecond)
	var n int
	for _, e := range sink.all() {
		if e.Fault != nil {
			if e.Protocol == nil || *e.Protocol != "udp" {
				t.Errorf("a fault on %+v", e)
			}
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d faulted entries", n)
	}
}

// dnsExchangeWithFallback is what a resolver does: ask over UDP, ask again over TCP when the answer is truncated.
func dnsExchangeWithFallback(addr string, m *dns.Msg) (*dns.Msg, error) {
	r, _, err := (&dns.Client{Net: "udp", Timeout: 2 * time.Second}).Exchange(m, addr)
	if err == nil && r.Truncated {
		r, _, err = (&dns.Client{Net: "tcp", Timeout: 2 * time.Second}).Exchange(m, addr)
	}
	return r, err
}

func TestAShortTTLFaultRewritesTheTTLAndTheCacheKeepsTheRealOne(t *testing.T) {
	up := upstreamA()
	s, addr, _ := runWithLog(t, up, config(t, faults(fault(ovA, `"action":"short_ttl","ttl":"1s"`))), nil)
	for i := 0; i < 3; i++ {
		r := ask(t, addr, "udp", "example.test", dns.TypeA)
		if len(r.Answer) != 1 || r.Answer[0].Header().Ttl != 1 {
			t.Fatalf("%v", r)
		}
	}
	if up.count() != 1 {
		t.Errorf("the cache was not used: %d upstream queries", up.count())
	}
	// a TTL of 0 is a legitimate test: answers that must not be cached
	s.SetConfig(config(t, `"generation":2,`+faults(fault(ovA, `"action":"short_ttl","ttl":"0s"`))))
	if r := ask(t, addr, "udp", "example.test", dns.TypeA); len(r.Answer) != 1 || r.Answer[0].Header().Ttl != 0 {
		t.Errorf("%v", r)
	}
	// without the fault the real TTL is back
	s.SetConfig(baseConfigGen(3))
	if r := ask(t, addr, "udp", "example.test", dns.TypeA); len(r.Answer) != 1 || r.Answer[0].Header().Ttl < 200 {
		t.Errorf("%v", r)
	}
}

func baseConfigGen(g int64) *model.DnsServiceConfig {
	c := baseConfig()
	c.Generation = g
	return c
}

// ---- hostname sets ------------------------------------------------------------------------------------------

type memResolutions struct {
	mu    sync.Mutex
	calls [][]model.DnsResolution
	fail  error
	block chan struct{}
	seen  chan struct{}
}

func (m *memResolutions) PostResolutions(ctx context.Context, rs []model.DnsResolution) error {
	m.mu.Lock()
	m.calls = append(m.calls, rs)
	fail, block, seen := m.fail, m.block, m.seen
	m.mu.Unlock()
	if seen != nil {
		select {
		case seen <- struct{}{}:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fail
}

func (m *memResolutions) all() [][]model.DnsResolution {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]model.DnsResolution(nil), m.calls...)
}

func sets(s ...string) string { return `"hostname_sets":[` + strings.Join(s, ",") + `]` }

func hostSet(id string, sources string, names ...string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = `"` + n + `"`
	}
	src := ""
	if sources != "" {
		src = `,"sources":[` + sources + `]`
	}
	return `{"set":"` + id + `","names":[` + strings.Join(q, ",") + `]` + src + `}`
}

// Plan §2.6: the addresses of an answer are reported before the answer is sent, so the first packet already matches.
func TestTheAddressesAreReportedBeforeTheAnswerIsSent(t *testing.T) {
	up := upstreamA()
	rec := &memResolutions{block: make(chan struct{}), seen: make(chan struct{}, 1)}
	_, addr := running(t, up, config(t, sets(hostSet("dns_aaaaaaaaaa", "", "example.test"))), func(o *Options) { o.Resolutions = rec })
	answered := make(chan *dns.Msg, 1)
	go func() { answered <- ask(t, addr, "udp", "example.test", dns.TypeA) }()
	select {
	case <-rec.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was reported")
	}
	select {
	case <-answered:
		t.Fatal("the answer was sent before the report was through")
	case <-time.After(150 * time.Millisecond):
	}
	close(rec.block)
	r := <-answered
	if len(r.Answer) != 1 {
		t.Errorf("%v", r)
	}
	calls := rec.all()
	if len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("%+v", calls)
	}
	got := calls[0][0]
	if got.Set != "dns_aaaaaaaaaa" || got.Client != "127.0.0.1" || got.Name != "example.test" || len(got.Addresses) != 1 || got.Addresses[0] != "203.0.113.10" || got.Ttl != 300 {
		t.Errorf("%+v", got)
	}
}

func TestACNAMEChainMatchesASelectorOfAnyOfItsNames(t *testing.T) {
	up := &fakeUpstream{answer: func(_, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.Response = true
		cname, _ := dns.NewRR("www.example.test. 60 IN CNAME edge.cdn.example.net.")
		m.Answer = []dns.RR{cname, aRecord("edge.cdn.example.net.", "203.0.113.20", 120), aRecord("edge.cdn.example.net.", "203.0.113.21", 30)}
		return m, nil
	}}
	rec := &memResolutions{}
	_, addr := running(t, up, config(t, sets(
		hostSet("dns_1111111111", "", "*.cdn.example.net"),                // the CNAME's target
		hostSet("dns_2222222222", "", "www.example.test"),                 // the name asked
		hostSet("dns_3333333333", "", "*.example.test"),                   // matches the name asked as a subdomain
		hostSet("dns_4444444444", "", "example.net"),                      // matches neither
		hostSet("dns_5555555555", `"192.0.2.0/24"`, "www.example.test"))), // another client
		func(o *Options) { o.Resolutions = rec })
	ask(t, addr, "udp", "www.example.test", dns.TypeA)
	calls := rec.all()
	if len(calls) != 1 {
		t.Fatalf("%+v", calls)
	}
	byset := map[string]model.DnsResolution{}
	for _, r := range calls[0] {
		byset[r.Set] = r
	}
	if len(byset) != 3 {
		t.Fatalf("%+v", calls[0])
	}
	for _, id := range []string{"dns_1111111111", "dns_2222222222", "dns_3333333333"} {
		r := byset[id]
		if len(r.Addresses) != 2 || r.Ttl != 30 {
			t.Errorf("%s: %+v (both addresses, the smaller TTL)", id, r)
		}
	}
	if byset["dns_1111111111"].Name != "edge.cdn.example.net" || byset["dns_2222222222"].Name != "www.example.test" {
		t.Errorf("%+v", byset)
	}
}

// A wrong answer is what the device connects to: the selector is matched with the addresses it got, and a TTL
// that a fault shortened is the one reported (the API keeps the element for ten minutes anyway).
func TestWhatIsReportedIsTheAnswerThatWasSent(t *testing.T) {
	up := upstreamA()
	rec := &memResolutions{}
	_, addr := running(t, up, config(t, faults(
		fault(ovA, `"action":"wrong_answer","answers":["10.99.0.1"],"names":["broker.example.test"]`),
		fault(ovB, `"action":"short_ttl","ttl":"1s","names":["example.test"]`))+","+sets(hostSet("dns_aaaaaaaaaa", "", "*.example.test", "example.test"))),
		func(o *Options) { o.Resolutions = rec })
	ask(t, addr, "udp", "broker.example.test", dns.TypeA)
	ask(t, addr, "udp", "example.test", dns.TypeA)
	calls := rec.all()
	if len(calls) != 2 {
		t.Fatalf("%+v", calls)
	}
	if r := calls[0][0]; len(r.Addresses) != 1 || r.Addresses[0] != "10.99.0.1" || r.Name != "broker.example.test" {
		t.Errorf("%+v", r)
	}
	if r := calls[1][0]; r.Ttl != 1 || r.Addresses[0] != "203.0.113.10" {
		t.Errorf("%+v", r)
	}
}

// A report that was made a moment ago for the same addresses is not made again; a new configuration forgets it;
// another address is reported.
func TestARepeatedReportIsNotMadeAgainUntilTheConfigurationChanges(t *testing.T) {
	up := upstreamA()
	rec := &memResolutions{}
	s, addr := running(t, up, config(t, `"generation":5,`+sets(hostSet("dns_aaaaaaaaaa", "", "example.test"))), func(o *Options) { o.Resolutions = rec })
	for i := 0; i < 4; i++ {
		ask(t, addr, "udp", "example.test", dns.TypeA)
	}
	if n := len(rec.all()); n != 1 {
		t.Fatalf("%d reports for 4 identical answers", n)
	}
	s.SetConfig(config(t, `"generation":6,`+sets(hostSet("dns_aaaaaaaaaa", "", "example.test"))))
	ask(t, addr, "udp", "example.test", dns.TypeA)
	if n := len(rec.all()); n != 2 {
		t.Errorf("%d reports after a new configuration", n)
	}
}

// Reports of concurrent queries are one call (plan §3.11, the batch window); the answers wait for it.
func TestConcurrentQueriesAreReportedInOneCall(t *testing.T) {
	up := &fakeUpstream{answer: func(_, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.Response = true
		m.Answer = []dns.RR{aRecord(q.Name, "203.0.113.10", 60)}
		return m, nil
	}}
	rec := &memResolutions{}
	_, addr := running(t, up, config(t, sets(hostSet("dns_aaaaaaaaaa", "", "*.example.test"))), func(o *Options) {
		o.Resolutions = rec
		o.BatchWindow = 100 * time.Millisecond
	})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ask(t, addr, "udp", "h"+string(rune('a'+i))+".example.test", dns.TypeA)
		}(i)
	}
	wg.Wait()
	calls := rec.all()
	total := 0
	for _, c := range calls {
		total += len(c)
	}
	if total != 10 || len(calls) > 3 {
		t.Errorf("%d resolutions in %d calls", total, len(calls))
	}
}

// A gateway that does not take the report in time does not take the DNS down with it: the answer goes out, and a
// later answer reports again.
func TestAnAnswerGoesOutWhenTheReportFails(t *testing.T) {
	up := upstreamA()
	rec := &memResolutions{fail: errors.New("the executor does not answer")}
	_, addr := running(t, up, config(t, sets(hostSet("dns_aaaaaaaaaa", "", "example.test"))), func(o *Options) { o.Resolutions = rec })
	if r := ask(t, addr, "udp", "example.test", dns.TypeA); len(r.Answer) != 1 {
		t.Errorf("%v", r)
	}
	ask(t, addr, "udp", "example.test", dns.TypeA)
	if n := len(rec.all()); n != 2 {
		t.Errorf("a failed report was not repeated: %d calls", n)
	}
}

// A name no selector matches, an answer without addresses and a proxy without a sink report nothing.
func TestNothingIsReportedForWhatNoSelectorMatches(t *testing.T) {
	up := &fakeUpstream{answer: func(_, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.Response = true
		if strings.HasPrefix(q.Name, "empty") {
			return m, nil
		}
		m.Answer = []dns.RR{aRecord(q.Name, "203.0.113.10", 60)}
		return m, nil
	}}
	rec := &memResolutions{}
	_, addr := running(t, up, config(t, sets(hostSet("dns_aaaaaaaaaa", "", "example.test", "empty.test"))), func(o *Options) { o.Resolutions = rec })
	ask(t, addr, "udp", "other.test", dns.TypeA)
	ask(t, addr, "udp", "empty.test", dns.TypeA)
	ask(t, addr, "udp", "nothere.invalid", dns.TypeA)
	if calls := rec.all(); len(calls) != 0 {
		t.Errorf("%+v", calls)
	}
	// without a sink the proxy answers as before
	_, addr2 := running(t, up, config(t, sets(hostSet("dns_aaaaaaaaaa", "", "example.test"))), nil)
	if r := ask(t, addr2, "udp", "example.test", dns.TypeA); len(r.Answer) != 1 {
		t.Errorf("%v", r)
	}
}
