//go:build testbed

package capture

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// injectFrames sends Ethernet frames out of the loopback interface through a raw socket, which gives the
// tests frames with the addresses of devices that do not exist.
func injectFrames(t *testing.T, frames ...[]byte) {
	t.Helper()
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	ifi, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	to := &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ALL), Ifindex: ifi.Index, Halen: 6}
	for _, f := range frames {
		if err := unix.Sendto(fd, f, 0, to); err != nil {
			t.Fatal(err)
		}
	}
}

// copies counts how often each frame is in the capture, as sent (outgoing) and as received. The loopback interface
// shows a frame twice: when it leaves and when it comes back in.
type copies struct{ out, in int }

func countFrames(pkts []Packet) map[string]copies {
	m := map[string]copies{}
	for _, p := range pkts {
		c := m[string(p.Data)]
		if p.Outgoing {
			c.out++
		} else {
			c.in++
		}
		m[string(p.Data)] = c
	}
	return m
}

// expectExactly fails unless the capture holds each wanted frame once outgoing and once incoming, and nothing else.
func expectExactly(t *testing.T, pkts []Packet, want ...[]byte) {
	t.Helper()
	got := countFrames(pkts)
	if len(got) != len(want) {
		t.Errorf("%d different frames in the capture, want %d (%d packets)", len(got), len(want), len(pkts))
	}
	for i, w := range want {
		if c := got[string(w)]; c != (copies{1, 1}) {
			t.Errorf("frame %d is in the capture %d times outgoing and %d times incoming, want once each", i, c.out, c.in)
		}
	}
}

func TestADeviceCaptureHoldsTheFramesOfItsAddressInBothDirectionsAndNoOthers(t *testing.T) {
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.Interfaces[0].Filter = Filter{MACs: []string{macDev}}
	})
	arp := make([]byte, 60)
	copy(arp[0:], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(arp[6:], []byte{2, 0, 0, 0, 0, 0x0b})
	arp[12], arp[13] = 0x08, 0x06
	otherDev := tcpUp(8883)
	otherDev.src, otherDev.sa = macOther, "10.10.0.12"
	otherDevDown := tcpDown(8883)
	otherDevDown.dst, otherDevDown.da = macOther, "10.10.0.12"
	selected := [][]byte{tcpUp(8883).build(), tcpDown(443).build(), arp}
	injectFrames(t, selected[0], otherDev.build(), selected[1], otherDevDown.build(), selected[2])
	waitFor(t, "the selected frames", func() bool { return s.Stats().Packets >= 6 })
	time.Sleep(200 * time.Millisecond)
	s.Stop(ReasonUser)
	expectExactly(t, readCapture(t, dir, s), selected...)
}

func TestASelectorCaptureHoldsOnlyTheMatchingTrafficOfTheDevice(t *testing.T) {
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.Interfaces[0].Filter = Filter{MACs: []string{macDev}, Protocol: TCP, Ports: []PortRange{{8883, 8883}}, Peers: []netip.Prefix{pfx("203.0.113.0/24")}}
	})
	udp := tcpUp(8883)
	udp.proto = protoUDP
	otherDev := tcpUp(8883)
	otherDev.src = macOther
	injectFrames(t,
		tcpUp(8883).build(), tcpUp(443).build(), udp.build(), otherDev.build(), tcpDown(8883).build(), tcpDown(443).build())
	waitFor(t, "the selected frames", func() bool { return s.Stats().Packets >= 4 })
	time.Sleep(200 * time.Millisecond)
	s.Stop(ReasonUser)
	expectExactly(t, readCapture(t, dir, s), tcpUp(8883).build(), tcpDown(8883).build())
}

func TestAFilterTooLargeForTheKernelIsAppliedInUserSpace(t *testing.T) {
	var macs []string
	for i := 0; i < 600; i++ {
		macs = append(macs, "06:00:00:00:"+hex2(i>>8)+":"+hex2(i&0xff))
	}
	macs = append(macs, macDev)
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.Snaplen = 100
		c.Interfaces[0].Filter = Filter{MACs: macs}
	})
	big := tcpUp(8883)
	big.payload = 400
	otherDev := tcpUp(8883)
	otherDev.src = "06:00:00:00:ff:ff"
	injectFrames(t, big.build(), otherDev.build(), tcpDown(8883).build())
	waitFor(t, "the selected frames", func() bool { return s.Stats().Packets >= 4 })
	time.Sleep(200 * time.Millisecond)
	s.Stop(ReasonUser)
	pkts := readCapture(t, dir, s)
	if len(pkts) != 4 {
		t.Fatalf("%d packets, want the request and the answer, each as sent and as received", len(pkts))
	}
	for _, p := range pkts[:2] {
		if len(p.Data) != 100 || p.OrigLen != len(big.build()) {
			t.Errorf("a packet of the big frame has %d bytes of %d: the snap length is not applied", len(p.Data), p.OrigLen)
		}
	}
}
