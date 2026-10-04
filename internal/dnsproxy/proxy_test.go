package dnsproxy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/goleak"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fakeUpstream answers from a table and records what it was asked.
type fakeUpstream struct {
	mu    sync.Mutex
	calls []string // "network server name type"
	// answer builds the reply; nil answers NXDOMAIN
	answer func(network, server string, q dns.Question) (*dns.Msg, error)
}

func (f *fakeUpstream) Exchange(_ context.Context, m *dns.Msg, network, server string) (*dns.Msg, error) {
	f.mu.Lock()
	f.calls = append(f.calls, network+" "+server+" "+m.Question[0].Name+" "+dns.TypeToString[m.Question[0].Qtype])
	f.mu.Unlock()
	if f.answer == nil {
		r := new(dns.Msg)
		r.SetRcode(m, dns.RcodeNameError)
		return r, nil
	}
	resp, err := f.answer(network, server, m.Question[0])
	if err != nil || resp == nil {
		return resp, err
	}
	resp.Id = m.Id
	resp.Question = m.Question
	return resp, nil
}

func (f *fakeUpstream) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func aRecord(name, ip string, ttl uint32) dns.RR {
	rr, err := dns.NewRR(name + " " + itoa(ttl) + " IN A " + ip)
	if err != nil {
		panic(err)
	}
	return rr
}

func itoa(n uint32) string {
	var b []byte
	if n == 0 {
		return "0"
	}
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func okAnswer(rrs ...dns.RR) func(string, string, dns.Question) (*dns.Msg, error) {
	return func(_, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.Response = true
		m.Answer = rrs
		return m, nil
	}
}

type memSink struct {
	mu      sync.Mutex
	entries []model.DnsQueryLogEntry
	fail    bool
}

func (s *memSink) Post(_ context.Context, e []model.DnsQueryLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("down")
	}
	s.entries = append(s.entries, e...)
	return nil
}
func (s *memSink) all() []model.DnsQueryLogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.DnsQueryLogEntry(nil), s.entries...)
}

func ptr[T any](v T) *T { return &v }

func baseConfig() *model.DnsServiceConfig {
	return &model.DnsServiceConfig{Generation: 1, Upstream: []string{"192.0.2.53"}, Networks: []struct {
		Clients       *model.Ipv4Cidr         `json:"clients,omitempty"`
		Gateway       model.Ipv4              `json:"gateway"`
		Network       model.Uuid              `json:"network"`
		StaticEntries *[]model.DnsStaticEntry `json:"static_entries,omitempty"`
	}{{Gateway: "10.10.0.1", Clients: ptr("10.10.0.0/24"), StaticEntries: &[]model.DnsStaticEntry{{Name: "printer.lab", Addresses: []string{"10.10.0.77"}}}}}}
}

// running starts a server on the loopback and returns its address and a client.
func running(t *testing.T, up Exchanger, cfg *model.DnsServiceConfig, mod func(*Options)) (*Server, string) {
	t.Helper()
	o := Options{Upstream: up, Timeout: time.Second}
	if mod != nil {
		mod(&o)
	}
	s := New(o)
	if cfg != nil {
		s.SetConfig(cfg)
	}
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.LocalAddr().String()
	_ = l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, addr) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	select {
	case <-s.Ready():
	case err := <-done:
		t.Fatalf("the proxy does not listen: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy does not listen")
	}
	return s, addr
}

func ask(t *testing.T, addr, network, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.SetEdns0(4096, false)
	c := &dns.Client{Net: network, Timeout: 3 * time.Second}
	r, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("%s %s: %v", network, name, err)
	}
	return r
}

func TestAnswersOverUDPAndTCPAndCaches(t *testing.T) {
	up := &fakeUpstream{answer: okAnswer(aRecord("example.test.", "203.0.113.10", 300))}
	s, addr := running(t, up, baseConfig(), nil)
	for _, network := range []string{"udp", "tcp"} {
		r := ask(t, addr, network, "example.test", dns.TypeA)
		if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "203.0.113.10" {
			t.Fatalf("%s: %v", network, r)
		}
	}
	if up.count() != 1 {
		t.Errorf("the second query (TCP) was not served from the cache: %v", up.calls)
	}
	if !strings.HasPrefix(up.calls[0], "udp 192.0.2.53:53 example.test. A") {
		t.Errorf("%v", up.calls)
	}
	if s.Queries() != 2 || s.UpstreamQueries() != 1 {
		t.Errorf("%d queries, %d upstream", s.Queries(), s.UpstreamQueries())
	}
}

func TestCachedTTLsCountDownAndExpire(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	up := &fakeUpstream{answer: okAnswer(aRecord("example.test.", "203.0.113.10", 100))}
	s := New(Options{Upstream: up, Clock: fake})
	s.SetConfig(baseConfig())
	q := new(dns.Msg)
	q.SetQuestion("example.test.", dns.TypeA)
	ask := func() *dns.Msg {
		r, _ := s.answer(context.Background(), q, mustAddr("10.10.0.5"))
		return r
	}
	ask()
	fake.Advance(40 * time.Second)
	if r := ask(); r.Answer[0].Header().Ttl != 60 {
		t.Errorf("ttl %d, want 60", r.Answer[0].Header().Ttl)
	}
	if up.count() != 1 {
		t.Errorf("%d upstream queries", up.count())
	}
	fake.Advance(61 * time.Second)
	ask()
	if up.count() != 2 {
		t.Errorf("an expired answer was served: %d upstream queries", up.count())
	}
}

func TestANewGenerationDropsTheCache(t *testing.T) {
	up := &fakeUpstream{answer: okAnswer(aRecord("example.test.", "203.0.113.10", 300))}
	s := New(Options{Upstream: up})
	s.SetConfig(baseConfig())
	q := new(dns.Msg)
	q.SetQuestion("example.test.", dns.TypeA)
	_, _ = s.answer(context.Background(), q, mustAddr("10.10.0.5"))
	c := baseConfig()
	c.Generation = 2
	s.SetConfig(c)
	_, _ = s.answer(context.Background(), q, mustAddr("10.10.0.5"))
	if up.count() != 2 {
		t.Errorf("%d upstream queries, want 2", up.count())
	}
	// the same generation again keeps it
	s.SetConfig(c)
	_, _ = s.answer(context.Background(), q, mustAddr("10.10.0.5"))
	if up.count() != 2 {
		t.Errorf("%d upstream queries, want 2", up.count())
	}
}

// M6b-07 test: an AAAA query of a name that exists (upstream answers NOERROR, with or without an
// AAAA record of its own) is NODATA, not NXDOMAIN; the name is still looked up, only the AAAA record
// of the answer is removed.
func TestAAAAIsStrippedButTheNameIsStillLookedUpUpstream(t *testing.T) {
	aaaa, err := dns.NewRR("dual.test. 60 IN AAAA 2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	up := &fakeUpstream{answer: func(_, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		// a real resolver answers the question it was asked, not every record of the name
		if q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY {
			m.Answer = append(m.Answer, aRecord("dual.test.", "203.0.113.10", 60))
		}
		if q.Qtype == dns.TypeAAAA || q.Qtype == dns.TypeANY {
			m.Answer = append(m.Answer, aaaa)
		}
		return m, nil
	}}
	sink := &memSink{}
	s, addr := running(t, up, baseConfig(), func(o *Options) { o.Sink = sink })
	r := ask(t, addr, "udp", "dual.test", dns.TypeAAAA)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Fatalf("an AAAA query of an existing name is NODATA: %v", r)
	}
	if up.count() != 1 {
		t.Errorf("the name was not looked up upstream: %v", up.calls)
	}
	// a query of another type whose answer holds an AAAA gets it removed
	r = ask(t, addr, "udp", "dual.test", dns.TypeANY)
	for _, rr := range r.Answer {
		if rr.Header().Rrtype == dns.TypeAAAA {
			t.Errorf("AAAA in the answer: %v", rr)
		}
	}
	if len(r.Answer) != 1 {
		t.Errorf("%v", r.Answer)
	}
	// the log says so
	ctx, cancel := context.WithCancel(context.Background())
	go s.RunLog(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for len(sink.all()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	n := 0
	for _, e := range sink.all() {
		if e.StrippedAaaa != nil && *e.StrippedAaaa {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d entries say stripped_aaaa: %+v", n, sink.all())
	}
}

// M6b-07 test: an AAAA query of a name that does not exist at all gets the upstream's own NXDOMAIN,
// not the NOERROR/NODATA of a name that exists without an AAAA record.
func TestAAAAForAMissingNameIsNXDOMAIN(t *testing.T) {
	up := &fakeUpstream{} // answer is nil: every query gets NXDOMAIN
	_, addr := running(t, up, baseConfig(), nil)
	r := ask(t, addr, "udp", "nowhere.test", dns.TypeAAAA)
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("rcode %s, want NXDOMAIN: %v", dns.RcodeToString[r.Rcode], r)
	}
	if up.count() != 1 {
		t.Errorf("the name was not looked up upstream: %v", up.calls)
	}
}

// M6b-07 test: stripping AAAA records never touches an SVCB/HTTPS record's own ipv6hint parameter;
// that is a hint inside a different record type, not an AAAA record.
func TestSVCBIPv6HintSurvivesAAAAStripping(t *testing.T) {
	https, err := dns.NewRR("svc.test. 60 IN HTTPS 1 . ipv6hint=2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	up := &fakeUpstream{answer: okAnswer(https)}
	_, addr := running(t, up, baseConfig(), nil)
	r := ask(t, addr, "udp", "svc.test", dns.TypeHTTPS)
	if len(r.Answer) != 1 || r.Answer[0].Header().Rrtype != dns.TypeHTTPS {
		t.Fatalf("the HTTPS record was removed: %v", r)
	}
	if !strings.Contains(r.Answer[0].String(), "2001:db8::1") {
		t.Errorf("ipv6hint was stripped: %v", r.Answer[0])
	}
}

func TestStaticEntriesAreAnsweredWithoutUpstream(t *testing.T) {
	up := &fakeUpstream{}
	_, addr := running(t, up, baseConfig(), nil)
	r := ask(t, addr, "udp", "Printer.Lab", dns.TypeA)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "10.10.0.77" {
		t.Fatalf("%v", r)
	}
	if !r.Authoritative {
		t.Error("a static entry is authoritative")
	}
	// the name exists, but has no other record type
	r = ask(t, addr, "udp", "printer.lab", dns.TypeTXT)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Errorf("%v", r)
	}
	if up.count() != 0 {
		t.Errorf("%v", up.calls)
	}
}

func TestOnlyTheNetworkOfTheClientAnswersItsStaticEntries(t *testing.T) {
	cfg := baseConfig()
	cfg.Networks = append(cfg.Networks, cfg.Networks[0])
	cfg.Networks[1].Gateway, cfg.Networks[1].Clients = "10.20.0.1", ptr("10.20.0.0/24")
	cfg.Networks[1].StaticEntries = &[]model.DnsStaticEntry{{Name: "printer.lab", Addresses: []string{"10.20.0.77"}}}
	s := New(Options{Upstream: &fakeUpstream{}})
	s.SetConfig(cfg)
	q := new(dns.Msg)
	q.SetQuestion("printer.lab.", dns.TypeA)
	for client, want := range map[string]string{"10.10.0.5": "10.10.0.77", "10.20.0.9": "10.20.0.77"} {
		r, _ := s.answer(context.Background(), q, mustAddr(client))
		if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != want {
			t.Errorf("%s: %v", client, r.Answer)
		}
	}
}

func TestATruncatedUpstreamAnswerIsAskedAgainOverTCP(t *testing.T) {
	big := make([]dns.RR, 0, 60)
	for i := 0; i < 60; i++ {
		big = append(big, aRecord("big.test.", "198.51.100."+itoa(uint32(i+1)), 60))
	}
	up := &fakeUpstream{answer: func(network, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		if network == "udp" {
			m.Truncated = true
			return m, nil
		}
		m.Answer = big
		return m, nil
	}}
	_, addr := running(t, up, baseConfig(), nil)
	// TCP gets everything
	r := ask(t, addr, "tcp", "big.test", dns.TypeA)
	if len(r.Answer) != 60 {
		t.Fatalf("%d answers over TCP", len(r.Answer))
	}
	// a UDP client that takes 512 bytes gets the truncation bit and fewer records
	m := new(dns.Msg)
	m.SetQuestion("big.test.", dns.TypeA)
	c := &dns.Client{Net: "udp", Timeout: 3 * time.Second}
	ur, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatal(err)
	}
	if !ur.Truncated || len(ur.Answer) >= 60 {
		t.Errorf("truncated=%v with %d answers", ur.Truncated, len(ur.Answer))
	}
	var sawTCP bool
	for _, c := range up.calls {
		sawTCP = sawTCP || strings.HasPrefix(c, "tcp ")
	}
	if !sawTCP {
		t.Errorf("no TCP retry upstream: %v", up.calls)
	}
}

func TestTheNextUpstreamIsTriedAndASilentOneIsServfail(t *testing.T) {
	up := &fakeUpstream{answer: func(_, server string, q dns.Question) (*dns.Msg, error) {
		if strings.HasPrefix(server, "192.0.2.1:") {
			return nil, errors.New("timeout")
		}
		return okAnswer(aRecord("example.test.", "203.0.113.10", 60))("", "", q)
	}}
	cfg := baseConfig()
	cfg.Upstream = []string{"192.0.2.1", "192.0.2.2"}
	_, addr := running(t, up, cfg, nil)
	r := ask(t, addr, "udp", "example.test", dns.TypeA)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Fatalf("%v", r)
	}
	// nobody answers
	down := &fakeUpstream{answer: func(_, _ string, _ dns.Question) (*dns.Msg, error) { return nil, errors.New("timeout") }}
	_, addr2 := running(t, down, baseConfig(), nil)
	if r := ask(t, addr2, "udp", "x.test", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Errorf("%v", r)
	}
	// a failure is not cached: the next query asks again
	ask(t, addr2, "udp", "x.test", dns.TypeA)
	if down.count() != 2 {
		t.Errorf("%d upstream queries", down.count())
	}
}

func TestWithoutConfigurationNothingIsAnswered(t *testing.T) {
	up := &fakeUpstream{answer: okAnswer(aRecord("example.test.", "203.0.113.10", 60))}
	s, addr := running(t, up, nil, nil)
	if r := ask(t, addr, "udp", "example.test", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Errorf("%v", r)
	}
	if up.count() != 0 {
		t.Error("asked upstream without a configuration")
	}
	s.SetConfig(baseConfig())
	if r := ask(t, addr, "udp", "example.test", dns.TypeA); r.Rcode != dns.RcodeSuccess {
		t.Errorf("%v", r)
	}
}

func TestNXDOMAINIsCachedBrieflyAndMalformedQueriesRefused(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	up := &fakeUpstream{}
	s := New(Options{Upstream: up, Clock: fake})
	s.SetConfig(baseConfig())
	q := new(dns.Msg)
	q.SetQuestion("nothing.test.", dns.TypeA)
	for i := 0; i < 2; i++ {
		if r, _ := s.answer(context.Background(), q, mustAddr("10.10.0.5")); r.Rcode != dns.RcodeNameError {
			t.Fatalf("%v", r)
		}
	}
	if up.count() != 1 {
		t.Errorf("%d upstream queries", up.count())
	}
	fake.Advance(31 * time.Second)
	_, _ = s.answer(context.Background(), q, mustAddr("10.10.0.5"))
	if up.count() != 2 {
		t.Errorf("%d upstream queries after the negative TTL", up.count())
	}
	two := new(dns.Msg)
	two.Question = []dns.Question{{Name: "a.", Qtype: dns.TypeA, Qclass: dns.ClassINET}, {Name: "b.", Qtype: dns.TypeA, Qclass: dns.ClassINET}}
	if r, _ := s.answer(context.Background(), two, mustAddr("10.10.0.5")); r.Rcode != dns.RcodeFormatError {
		t.Errorf("%v", r)
	}
	chaos := new(dns.Msg)
	chaos.SetQuestion("version.bind.", dns.TypeTXT)
	chaos.Question[0].Qclass = dns.ClassCHAOS
	if r, _ := s.answer(context.Background(), chaos, mustAddr("10.10.0.5")); r.Rcode != dns.RcodeNotImplemented {
		t.Errorf("%v", r)
	}
}

func TestTheCacheIsBounded(t *testing.T) {
	up := &fakeUpstream{answer: okAnswer(aRecord("n.test.", "203.0.113.10", 600))}
	s := New(Options{Upstream: up, CacheEntries: 3})
	s.SetConfig(baseConfig())
	for i := 0; i < 10; i++ {
		q := new(dns.Msg)
		q.SetQuestion("n"+itoa(uint32(i))+".test.", dns.TypeA)
		_, _ = s.answer(context.Background(), q, mustAddr("10.10.0.5"))
	}
	s.cache.mu.Lock()
	n := len(s.cache.m)
	s.cache.mu.Unlock()
	if n != 3 {
		t.Errorf("%d cached entries", n)
	}
}

func TestTheQueryLogIsBatchedAndSurvivesASinkThatIsDown(t *testing.T) {
	sink := &memSink{fail: true}
	up := &fakeUpstream{answer: okAnswer(aRecord("example.test.", "203.0.113.10", 60))}
	s := New(Options{Upstream: up, Sink: sink})
	s.SetConfig(baseConfig())
	for i := 0; i < 5; i++ {
		q := new(dns.Msg)
		q.SetQuestion("example.test.", dns.TypeA)
		w := &fakeWriter{remote: &net.UDPAddr{IP: net.ParseIP("10.10.0.5"), Port: 4000}, local: &net.UDPAddr{IP: net.ParseIP("169.254.100.2"), Port: 53}}
		s.ServeDNS(w, q)
	}
	s.log.flush(context.Background())
	if len(sink.all()) != 0 {
		t.Fatal("entries were taken by a sink that is down")
	}
	sink.mu.Lock()
	sink.fail = false
	sink.mu.Unlock()
	s.log.flush(context.Background())
	got := sink.all()
	if len(got) != 5 {
		t.Fatalf("%d entries after the sink came back", len(got))
	}
	e := got[0]
	if e.Client != "10.10.0.5" || e.Name != "example.test" || e.Type != "A" || e.Rcode != "NOERROR" || e.Protocol == nil || *e.Protocol != "udp" || e.Answers == nil || (*e.Answers)[0] != "203.0.113.10" {
		t.Errorf("%+v", e)
	}
	if got[0].Cached != nil && *got[0].Cached || got[1].Cached == nil || !*got[1].Cached {
		t.Errorf("the second query is a cache hit: %+v %+v", got[0], got[1])
	}
	// the buffer is bounded while the sink is away
	sink.mu.Lock()
	sink.fail = true
	sink.mu.Unlock()
	for i := 0; i < logBuffer+50; i++ {
		s.log.add(model.DnsQueryLogEntry{Name: "x"})
	}
	if d := s.log.Dropped(); d != 50 {
		t.Errorf("%d dropped, want 50", d)
	}
}

type fakeWriter struct {
	dns.ResponseWriter
	remote, local net.Addr
	msg           *dns.Msg
}

func (w *fakeWriter) RemoteAddr() net.Addr      { return w.remote }
func (w *fakeWriter) LocalAddr() net.Addr       { return w.local }
func (w *fakeWriter) WriteMsg(m *dns.Msg) error { w.msg = m; return nil }
func (w *fakeWriter) Write([]byte) (int, error) { return 0, nil }
func (w *fakeWriter) Close() error              { return nil }
func (w *fakeWriter) TsigStatus() error         { return nil }
func (w *fakeWriter) TsigTimersOnly(bool)       {}
func (w *fakeWriter) Hijack()                   {}

func TestHostResolversSkipTheLocalStub(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "stub")
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(stub, []byte("nameserver 127.0.0.53\noptions edns0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("# resolved\nnameserver 192.0.2.1\nnameserver 2001:db8::1\nnameserver 192.0.2.1\nnameserver 192.0.2.2%eth0\nnameserver 0.0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := HostResolvers(stub, real)
	if len(got) != 2 || got[0].String() != "192.0.2.1" || got[1].String() != "192.0.2.2" {
		t.Errorf("%v", got)
	}
	if got := HostResolvers(stub, filepath.Join(dir, "missing")); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestTheCacheKeepsNothingOfTheFirstClientsEDNS(t *testing.T) {
	up := &fakeUpstream{answer: func(_, _ string, q dns.Question) (*dns.Msg, error) {
		m := new(dns.Msg)
		m.Answer = []dns.RR{aRecord("sec.test.", "203.0.113.10", 300)}
		sig, _ := dns.NewRR("sec.test. 300 IN RRSIG A 13 2 300 20300101000000 20200101000000 12345 test. AAAA")
		m.Answer = append(m.Answer, sig)
		m.SetEdns0(4096, true)
		return m, nil
	}}
	_, addr := running(t, up, baseConfig(), nil)
	// a client with EDNS and DO gets the signature and an OPT record
	m := new(dns.Msg)
	m.SetQuestion("sec.test.", dns.TypeA)
	m.SetEdns0(4096, true)
	r, _, err := (&dns.Client{Net: "udp", Timeout: 3 * time.Second}).Exchange(m, addr)
	if err != nil {
		t.Fatal(err)
	}
	sawSig := false
	for _, rr := range r.Answer {
		sawSig = sawSig || rr.Header().Rrtype == dns.TypeRRSIG
	}
	if !sawSig || r.IsEdns0() == nil {
		t.Errorf("a DO client gets the signature and an OPT record: %v", r)
	}
	if o := r.IsEdns0(); o != nil && o.UDPSize() > maxUDP {
		t.Errorf("advertised size %d", o.UDPSize())
	}
	// a client without EDNS gets neither, from the cache or not
	plain := new(dns.Msg)
	plain.SetQuestion("sec.test.", dns.TypeA)
	r, _, err = (&dns.Client{Net: "udp", Timeout: 3 * time.Second}).Exchange(plain, addr)
	if err != nil {
		t.Fatal(err)
	}
	for _, rr := range append(r.Answer, r.Extra...) {
		if rr.Header().Rrtype == dns.TypeRRSIG || rr.Header().Rrtype == dns.TypeOPT {
			t.Errorf("a client without EDNS got %v", rr)
		}
	}
	if len(r.Answer) != 1 {
		t.Errorf("%v", r.Answer)
	}
}

func TestTheQueryLogDoesNotLoseTheWrongEntriesWhenItOverflowsDuringAPost(t *testing.T) {
	block := make(chan struct{})
	release := make(chan struct{})
	sink := &blockingSink{started: block, release: release}
	q := newQueryLog(sink, clock.NewFake(time.Now()), nil)
	for i := 0; i < 10; i++ {
		q.add(model.DnsQueryLogEntry{Name: "old" + itoa(uint32(i))})
	}
	done := make(chan struct{})
	go func() { q.flush(context.Background()); close(done) }()
	<-block // the batch is on its way
	// the buffer overflows meanwhile: the oldest entries (part of the batch) are dropped
	for i := 0; i < logBuffer; i++ {
		q.add(model.DnsQueryLogEntry{Name: "new"})
	}
	close(release)
	<-done
	// the entries of the first batch were sent once and are not sent again
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.batches) < 2 {
		t.Fatalf("%d batches", len(sink.batches))
	}
	for _, batch := range sink.batches[1:] {
		for _, e := range batch {
			if strings.HasPrefix(e.Name, "old") {
				t.Fatalf("an entry of the sent batch is sent again: %s", e.Name)
			}
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) != 0 {
		t.Errorf("%d entries stay", len(q.pending))
	}
}

type blockingSink struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	batches [][]model.DnsQueryLogEntry
}

func (b *blockingSink) Post(_ context.Context, e []model.DnsQueryLogEntry) error {
	b.once.Do(func() { close(b.started) })
	<-b.release
	b.mu.Lock()
	b.batches = append(b.batches, e)
	b.mu.Unlock()
	return nil
}
