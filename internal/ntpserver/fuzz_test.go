package ntpserver

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// FuzzPacket feeds arbitrary datagrams to the parser and to the whole of the server's answer, with every kind of fault
// in force. The properties: nothing panics; a datagram that is no client query gets no reply; a reply is 48 bytes and never
// larger than its request; a reply parses; and the packet of a parse writes back to the same 48 bytes.
func FuzzPacket(f *testing.F) {
	add := func(p Packet, trailer int) {
		f.Add(append(p.Marshal(), make([]byte, trailer)...))
	}
	add(Packet{Version: 4, Mode: ModeClient, Transmit: 0x1234_5678_9abc_def0}, 0)
	add(Packet{Version: 3, Mode: ModeClient}, 68)
	add(Packet{Version: 4, Mode: ModeControl}, 0)
	add(Packet{Version: 2, Mode: ModePrivate}, 40)
	add(Packet{Version: 4, Mode: ModeClient, Transmit: probeTransmit}, 0)
	add(Packet{Version: 7, Mode: ModeClient, Leap: 3, Stratum: 255, Poll: -128, Precision: -128, Transmit: 0xFFFF_FFFF_FFFF_FFFF}, 0)
	f.Add([]byte{})
	f.Add([]byte{0x1b})
	f.Add(make([]byte, 47))

	clk := clock.NewFake(time.Date(2036, 2, 7, 6, 28, 10, 0, time.UTC))
	s := New(Options{Clock: clk, Sync: Synchronized, RateLimit: -1})
	s.SetConfig(config(f, []int{Port, 12301, 12302, 12303, 12304},
		fault(ovA, Port, clk.Now(), []string{"10.20.0.0/24"}, `"offset":"-130y","skew_ppm":-1000000`),
		fault(ovA, 12301, clk.Now(), []string{"10.20.0.0/24"}, `"absolute":"2038-01-19T03:14:07Z","skew_ppm":1000000,"leap":"insert"`),
		fault(ovA, 12302, clk.Now(), []string{"10.20.0.0/24"}, `"answer":"kiss_of_death","kiss_code":"RATE"`),
		fault(ovA, 12303, clk.Now(), []string{"10.20.0.0/24"}, `"answer":"origin_mismatch"`),
		fault(ovA, 12304, clk.Now(), []string{"10.20.0.0/24"}, `"offset":"+20y"`)))
	client := netip.MustParseAddr("10.20.0.11")
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data)
		if err != nil {
			if len(data) >= PacketSize {
				t.Fatalf("%d bytes do not parse: %v", len(data), err)
			}
		} else if b := p.Marshal(); string(b) != string(data[:PacketSize]) {
			t.Fatalf("the packet does not write back to what was read:\n% x\n% x", data[:PacketSize], b)
		}
		for _, port := range []int{Port, 12301, 12302, 12303, 12304} {
			reply, e := s.Process(data, client, port, time.Time{})
			isQuery := err == nil && p.Mode == ModeClient && p.Version != 0
			if reply != nil && !isQuery {
				t.Fatalf("a datagram that is no client query got a reply: % x", data)
			}
			if (e != nil) != isQuery && (!isQuery || p.Transmit != probeTransmit) {
				t.Fatalf("log entry %v for a datagram with query=%v", e, isQuery)
			}
			if reply == nil {
				continue
			}
			if len(reply) != PacketSize || len(reply) > len(data) {
				t.Fatalf("a reply of %d bytes to a request of %d", len(reply), len(data))
			}
			if _, err := Parse(reply); err != nil {
				t.Fatal(err)
			}
			if e != nil && e.Answer != nil && *e.Answer == model.NtpQueryLogEntryAnswerSilence {
				t.Fatal("a silenced query got a reply")
			}
		}
	})
}
