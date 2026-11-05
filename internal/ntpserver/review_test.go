package ntpserver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// pending is the entries of the query log that the sink has not taken.
func pending(s *Server) []model.NtpQueryLogEntry {
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	return append([]model.NtpQueryLogEntry(nil), s.log.pending...)
}

func probeRequest() []byte {
	return Packet{Version: 4, Mode: ModeClient, Transmit: probeTransmit}.Marshal()
}

// The health check's timestamp is the key to an answer with the correct time before any fault is consulted, so only the
// host the check runs on may use it: a device that sends it is an ordinary client, and is faulted, limited and logged.
func TestTheProbeIsAnsweredOnlyToTheHostItRunsOn(t *testing.T) {
	s, clk := newServer(t, func(o *Options) {
		o.RateLimit = 4
		o.Sink = sinkFunc(func([]model.NtpQueryLogEntry) {})
		o.LocalAddr = netip.MustParseAddr("169.254.100.2")
	})
	s.SetConfig(config(t, []int{Port}, fault(ovA, Port, t0, []string{"10.20.0.0/24", "169.254.100.2"}, `"offset":"+1h"`)))
	for _, from := range []string{"127.0.0.1", "169.254.100.2"} {
		reply, e := s.Process(probeRequest(), netip.MustParseAddr(from), Port, time.Time{})
		p, err := Parse(reply)
		if err != nil || e != nil {
			t.Fatalf("%s: %v %v", from, err, e)
		}
		near(t, "the probe from "+from, served(p, clk.Now()), clk.Now(), time.Microsecond)
	}
	if c := s.Counters(); c.Queries != 0 {
		t.Errorf("the probes of the host are counted as queries: %+v", c)
	}
	// a device gets the time of its fault, the entry of the log, and the limit of 4 per second
	reply, e := s.Process(probeRequest(), dev1, Port, time.Time{})
	p, err := Parse(reply)
	if err != nil || e == nil {
		t.Fatalf("%v %v", err, e)
	}
	near(t, "the probe from a device", served(p, clk.Now().Add(time.Hour)), clk.Now().Add(time.Hour), time.Microsecond)
	if e.Fault == nil || e.OffsetMs == nil || *e.OffsetMs != time.Hour.Milliseconds() {
		t.Errorf("%+v", e)
	}
	for range 10 {
		s.Process(probeRequest(), dev1, Port, time.Time{})
	}
	if c := s.Counters(); c.Dropped == 0 || c.Queries != 4 {
		t.Errorf("a device that sends the probe is not limited: %+v", c)
	}
}

// A device is also in the stretch of its network, so two entries of one port contain its address: the one with the longer
// prefix decides, whatever the order of the list.
func TestTheEntryOfTheLongestPrefixDecides(t *testing.T) {
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port},
		fault(ovA, Port, t0, []string{"10.20.0.0/24"}, `"offset":"+1d"`),
		fault(ovB, Port, t0, []string{"10.20.0.11"}, `"offset":"+5d"`)))
	for dev, want := range map[netip.Addr]time.Duration{dev1: 5 * 24 * time.Hour, dev2: 24 * time.Hour} {
		p, e, ok := ask(t, s, dev, Port)
		if !ok {
			t.Fatalf("%v: no answer", dev)
		}
		near(t, dev.String(), served(p, clk.Now().Add(want)), clk.Now().Add(want), time.Microsecond)
		if e.Fault == nil {
			t.Errorf("%v: no fault in the entry", dev)
		}
	}
}

func TestTheReferenceIdIsTheGatewayOfTheNetworkOfTheClient(t *testing.T) {
	raw := `{"generation":1,"listen":[123],"faults":[],"networks":[
		{"network":"` + ovA + `","gateway":"10.20.0.1","clients":"10.20.0.0/24"},
		{"network":"` + ovB + `","gateway":"10.30.0.1","clients":"10.30.0.0/24"},
		{"network":"33333333-3333-4333-8333-333333333333","gateway":"192.168.50.1","clients":"192.168.50.0/24"}]}`
	var c model.NtpServiceConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	s, _ := newServer(t, nil)
	s.SetConfig(&c)
	for from, want := range map[string][4]byte{"10.20.0.7": {10, 20, 0, 1}, "10.30.0.200": {10, 30, 0, 1}, "192.168.50.9": {192, 168, 50, 1}} {
		p, _, ok := ask(t, s, netip.MustParseAddr(from), Port)
		if !ok || p.RefID != want {
			t.Errorf("%s: reference id %v, want %v (answered %v)", from, p.RefID, want, ok)
		}
	}
	// a client of no network is not served; its reference id would be zero (several networks with different gateways)
	outside := netip.MustParseAddr("172.16.0.5")
	if _, _, ok := ask(t, s, outside, Port); ok || s.parsed.Load().gatewayOf(outside) != [4]byte{} {
		t.Errorf("a client outside every network: answered %v", ok)
	}
}

// A device behind a router of a network is a client of the network: the config has an entry for each network behind it with the
// same gateway, and the longest prefix decides when a route of one network lies inside the range of another.
func TestTheReferenceIdOfAClientBehindARouterIsTheGatewayOfItsNetwork(t *testing.T) {
	raw := `{"generation":1,"listen":[123],"faults":[],"networks":[
		{"network":"` + ovA + `","gateway":"10.20.0.1","clients":"10.20.0.0/24"},
		{"network":"` + ovA + `","gateway":"10.20.0.1","clients":"10.30.0.0/24"},
		{"network":"` + ovB + `","gateway":"10.40.0.1","clients":"10.40.0.0/24"},
		{"network":"` + ovB + `","gateway":"10.40.0.1","clients":"10.30.0.128/25"}]}`
	var c model.NtpServiceConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	s, _ := newServer(t, nil)
	s.SetConfig(&c)
	for from, want := range map[string][4]byte{"10.30.0.7": {10, 20, 0, 1}, "10.30.0.200": {10, 40, 0, 1}, "10.40.0.9": {10, 40, 0, 1}} {
		p, _, ok := ask(t, s, netip.MustParseAddr(from), Port)
		if !ok || p.RefID != want {
			t.Errorf("%s: reference id %v, want %v (answered %v)", from, p.RefID, want, ok)
		}
	}
	// a client of no network is not served; its reference id would be zero (several networks with different gateways)
	outside := netip.MustParseAddr("172.16.0.5")
	if _, _, ok := ask(t, s, outside, Port); ok || s.parsed.Load().gatewayOf(outside) != [4]byte{} {
		t.Errorf("a client outside every network: answered %v", ok)
	}
}

// The destination of a query is told by the port it arrived on (D36): the entry of the log has that port, and the server
// asked is named where the selector names one address and one port.
func TestThePortAndTheServerAskedOfTheEntryOfTheLog(t *testing.T) {
	s, _ := newServer(t, nil)
	s.SetConfig(config(t, []int{Port, 12301, 12302, 12303, 12304},
		fault(ovA, 12301, t0, []string{"10.20.0.0/24"}, `"offset":"+1h","destination":{"cidr":"216.239.35.4"},"ports":[5123]`),
		fault(ovA, 12302, t0, []string{"10.20.0.0/24"}, `"offset":"+1h","destination":{"cidr":"216.239.35.4"},"ports":[123,5123]`),
		fault(ovA, 12303, t0, []string{"10.20.0.0/24"}, `"offset":"+1h","destination":{"cidr":"216.239.35.4"},"port_ranges":[{"from":5000,"to":6000}]`),
		fault(ovA, 12304, t0, []string{"10.20.0.0/24"}, `"offset":"+1h","destination":{"cidr":"216.239.35.0/24"},"ports":[5123]`)))
	for port, want := range map[int]string{12301: "216.239.35.4:5123", 12302: "", 12303: "", 12304: ""} {
		_, e, ok := ask(t, s, dev1, port)
		if !ok || e.Port != port {
			t.Fatalf("port %d: answered %v, entry %+v", port, ok, e)
		}
		switch {
		case want == "" && e.Server != nil:
			t.Errorf("port %d: a server is named: %s", port, *e.Server)
		case want != "" && (e.Server == nil || *e.Server != want):
			t.Errorf("port %d: server %v, want %s", port, e.Server, want)
		}
	}
}

// ntsQuery is a query with the extension fields a client of NTS sends: a unique identifier and a cookie, then the
// authenticator (RFC 8915 §5).
func ntsQuery(types ...uint16) []byte {
	b := request(4, 0x1234_5678_9abc_def0)
	for _, typ := range types {
		f := make([]byte, 32)
		binary.BigEndian.PutUint16(f, typ)
		binary.BigEndian.PutUint16(f[2:], uint16(len(f)))
		b = append(b, f...)
	}
	return b
}

func TestAQueryWithNTSExtensionFieldsIsAnsweredPlainAndReportedOncePerDevice(t *testing.T) {
	s, clk := newServer(t, func(o *Options) { o.Sink = sinkFunc(func([]model.NtpQueryLogEntry) {}) })
	s.SetConfig(config(t, []int{Port, 12301}, fault(ovA, 12301, t0, []string{"10.20.0.0/24"}, `"offset":"+1h","destination":{"cidr":"216.239.35.4"}`)))
	query := ntsQuery(extNTSUniqueIdentifier, extNTSCookie)
	// the answer is the 48 bytes of a plain one: never larger than the request, and without the fields
	reply, e := s.Process(query, dev1, 12301, time.Time{})
	if len(reply) != PacketSize || e == nil || e.Answer == nil {
		t.Fatalf("%d bytes, %+v", len(reply), e)
	}
	ntsEntries := func() (n int) {
		for _, e := range pending(s) {
			if e.Kind != nil && *e.Kind == model.NtsKe {
				if (e.Client != "10.20.0.11" && e.Client != "10.20.0.12") || (e.Client == "10.20.0.11") != (e.Port == 12301) || e.Answer != nil {
					t.Errorf("%+v", e)
				}
				n++
			}
		}
		return n
	}
	if n := ntsEntries(); n != 1 {
		t.Fatalf("%d NTS entries after the first query, want 1", n)
	}
	s.Process(query, dev1, 12301, time.Time{})
	if n := ntsEntries(); n != 1 {
		t.Errorf("%d NTS entries after the second query, want still 1", n)
	}
	s.Process(query, dev2, Port, time.Time{})
	if n := ntsEntries(); n != 2 {
		t.Errorf("%d NTS entries after another device, want 2", n)
	}
	clk.Advance(time.Minute + time.Second)
	s.Process(query, dev1, 12301, time.Time{})
	if n := ntsEntries(); n != 3 {
		t.Errorf("%d NTS entries a minute later, want 3", n)
	}
	// a query that carries some other extension field is no NTS
	s.Process(ntsQuery(0x0002), dev3, Port, time.Time{})
	if n := ntsEntries(); n != 3 {
		t.Errorf("a query with another extension field was reported as NTS (%d)", n)
	}
}

func TestOnlyTheExtensionFieldsOfNTSAreRecognized(t *testing.T) {
	long := func(typ uint16, size int) []byte {
		f := make([]byte, size)
		binary.BigEndian.PutUint16(f, typ)
		binary.BigEndian.PutUint16(f[2:], uint16(size))
		return f
	}
	header := request(4, 1)
	for name, tc := range map[string]struct {
		tail []byte
		want bool
	}{
		"none":                        {nil, false},
		"the cookie":                  {long(extNTSCookie, 32), true},
		"the unique identifier":       {long(extNTSUniqueIdentifier, 36), true},
		"the cookie placeholder":      {long(extNTSCookiePlaceholder, 16), true},
		"the authenticator":           {long(extNTSAuthenticator, 28), true},
		"another field, then the NTS": {append(long(0x0002, 16), long(extNTSCookie, 16)...), true},
		"another field":               {long(0x0002, 16), false},
		"a field of length 0":         {[]byte{0x00, 0x02, 0x00, 0x00}, false},
		"a truncated header":          {[]byte{0x02, 0x04, 0x00}, false},
		"a length that overruns":      {append(long(0x0002, 16), 0x00, 0x09, 0x01, 0x00), false},
		"a length not of four bytes":  {append(long(0x0002, 18), long(extNTSCookie, 16)...), false},
	} {
		if got := hasNTSExtension(append(append([]byte(nil), header...), tc.tail...)); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// A connection that arrives before the first configuration is closed and not reported: the server cannot tell yet whether
// the address is a device of a test network.
func TestNTSKeyEstablishmentBeforeTheConfigurationIsNotReported(t *testing.T) {
	s, _ := newServer(t, func(o *Options) { o.Sink = sinkFunc(func([]model.NtpQueryLogEntry) {}) })
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ServeNTSKE(ctx, ln) }()
	connect := func() {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = c.Read(make([]byte, 1)) // closed by the server
		_ = c.Close()
	}
	connect()
	s.SetConfig(config(t, []int{Port}))
	connect()
	waitFor(t, "the connection after the configuration is counted", func() bool { return s.Counters().NTSKE >= 1 })
	cancel()
	if err := <-done; err != nil {
		t.Error(err)
	}
	if got := s.Counters().NTSKE; got != 1 {
		t.Errorf("%d connections counted, want only the one after the configuration", got)
	}
}

// A device that stays quiet for the time its bucket needs to fill again is forgotten when the table is full, and a busy one
// is kept: the sweep only gives a device a fresh burst, it never takes a token that is owed.
func TestTheLimiterSweepKeepsTheBusyAndForgetsTheIdle(t *testing.T) {
	l := newLimiter(16)
	idle, busy := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2")
	now := time.Duration(0)
	l.allow(idle, now)
	for range 16 {
		l.allow(busy, now) // the bucket of the busy one is empty
	}
	// one second later (burst / rate) the idle bucket is full again, and the busy one has been used just now
	now += time.Second
	l.allow(busy, now)
	l.mu.Lock()
	l.sweep(now)
	_, idleKept := l.m[idle]
	_, busyKept := l.m[busy]
	l.mu.Unlock()
	if idleKept || !busyKept {
		t.Errorf("after the sweep the idle device is kept: %v, the busy one: %v", idleKept, busyKept)
	}
	// less than the time a bucket needs to fill: nothing is forgotten
	l.allow(idle, now)
	l.mu.Lock()
	l.sweep(now + time.Second/2)
	n := len(l.m)
	l.mu.Unlock()
	if n != 2 {
		t.Errorf("%d devices after a sweep within the filling time, want 2", n)
	}
}

func TestTheNTPEpochItselfIsNeverTheUnsetTimestamp(t *testing.T) {
	got := TimestampOf(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	if got.Seconds() != 1 || got == 0 {
		t.Errorf("%#x", uint64(got))
	}
	if got := TimestampOf(time.Date(1900, 1, 1, 0, 0, 1, 0, time.UTC)); got.Seconds() != 1 {
		t.Errorf("%#x", uint64(got))
	}
}

// Of two entries for the same port whose sources are equally specific, the first decides (the compiler lists the one it ranks
// higher first): the walk keeps the first match and only a longer prefix replaces it.
func TestOfTwoEquallySpecificFaultsTheFirstDecides(t *testing.T) {
	s, clk := newServer(t, nil)
	s.SetConfig(config(t, []int{Port},
		fault(ovA, Port, t0, []string{"10.20.0.0/24"}, `"offset":"+1h"`),
		fault(ovB, Port, t0, []string{"10.20.0.0/24"}, `"offset":"+2h"`)))
	p, e, ok := ask(t, s, dev1, Port)
	if !ok {
		t.Fatal("no answer")
	}
	near(t, "transmit", served(p, clk.Now().Add(time.Hour)), clk.Now().Add(time.Hour), time.Microsecond)
	if e.Fault == nil || e.Fault.String() != ovA {
		t.Errorf("fault %v, want %s", e.Fault, ovA)
	}
}

// A port outside 1..65535 in the list to listen on is not opened and not retried: the API validates the configuration, and a
// server that meets such a port asks the system for nothing it cannot have.
func TestAListenPortOutsideTheRangeIsNotOpened(t *testing.T) {
	var mu sync.Mutex
	asked := map[int]int{}
	ports := freePorts(t, 2)
	listen := func(port int) (net.PacketConn, error) {
		mu.Lock()
		asked[port]++
		mu.Unlock()
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("port %d", port)
		}
		return ListenUDP("127.0.0.1")(port)
	}
	s, _ := newServer(t, func(o *Options) { o.BasePort = ports[0]; o.Clock = nil; o.OpenRetry = 20 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listen) }()
	defer func() {
		cancel()
		<-done
	}()
	<-s.Ready()
	s.SetConfig(config(t, []int{ports[0], 0, 70000, -5, ports[1]}))
	waitFor(t, "the valid port opens", func() bool { return exchange(t, "127.0.0.1", ports[1], 100*time.Millisecond, request(4, 1)) != nil })
	time.Sleep(100 * time.Millisecond) // several retries would show
	mu.Lock()
	defer mu.Unlock()
	for _, p := range []int{0, 70000, -5} {
		if asked[p] != 0 {
			t.Errorf("port %d was asked for %d times", p, asked[p])
		}
	}
}
