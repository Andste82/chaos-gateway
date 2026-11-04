package ntpserver

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// t0 is the instant the fake clock starts at.
var t0 = time.Date(2026, 11, 3, 12, 0, 0, 500_000_000, time.UTC)

const (
	ovA = "11111111-1111-4111-8111-111111111111"
	ovB = "22222222-2222-4222-8222-222222222222"
)

var (
	dev1 = netip.MustParseAddr("10.20.0.11")
	dev2 = netip.MustParseAddr("10.20.0.12")
	dev3 = netip.MustParseAddr("10.20.0.13")
)

// fault is the JSON of one entry of the configuration's faults: body is the members of the TimeFault, sources a list of
// addresses or prefixes.
func fault(overlay string, port int, since time.Time, sources []string, body string) string {
	src, _ := json.Marshal(sources)
	if body != "" {
		body = "," + body
	}
	return fmt.Sprintf(`{"overlay":%q,"port":%d,"since":%q,"sources":%s%s}`, overlay, port, since.Format(time.RFC3339Nano), src, body)
}

// config makes a configuration of generation 1 with one network (gateway 10.20.0.1) and the given faults.
func config(t testing.TB, listen []int, faults ...string) *model.NtpServiceConfig {
	t.Helper()
	l, _ := json.Marshal(listen)
	raw := fmt.Sprintf(`{"generation":1,"listen":%s,"networks":[{"network":%q,"gateway":"10.20.0.1","clients":"10.20.0.0/24"}],"faults":[%s]}`,
		l, ovB, strings.Join(faults, ","))
	var c model.NtpServiceConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	return &c
}

// newServer makes a server on a fake clock that starts at t0, whose host clock is synchronized, and without a rate
// limit unless a test asks for it.
func newServer(t testing.TB, mod func(*Options)) (*Server, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0)
	o := Options{Clock: clk, Sync: Synchronized, RateLimit: -1}
	if mod != nil {
		mod(&o)
	}
	return New(o), clk
}

// request is a client query (mode 3) of the given version with the given transmit timestamp.
func request(version uint8, transmit Timestamp) []byte {
	return Packet{Version: version, Mode: ModeClient, Poll: 6, Transmit: transmit}.Marshal()
}

// ask sends a query of device from to the port and parses the reply (ok false: no answer).
func ask(t testing.TB, s *Server, from netip.Addr, port int) (Packet, *model.NtpQueryLogEntry, bool) {
	t.Helper()
	reply, e := s.Process(request(4, 0x1234_5678_9abc_def0), from, port, time.Time{})
	if reply == nil {
		return Packet{}, e, false
	}
	p, err := Parse(reply)
	if err != nil {
		t.Fatal(err)
	}
	return p, e, true
}

// served is the transmit timestamp of a reply as an instant, resolved against the correct time.
func served(p Packet, pivot time.Time) time.Time { return p.Transmit.Time(pivot) }

func near(t testing.TB, what string, got, want time.Time, tol time.Duration) {
	t.Helper()
	if d := got.Sub(want); d > tol || d < -tol {
		t.Errorf("%s: %v, want %v (off by %v, tolerance %v)", what, got.UTC(), want.UTC(), d, tol)
	}
}
