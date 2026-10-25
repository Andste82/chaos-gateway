//go:build testbed

package kea

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This file lets the tests talk DHCP to the real Kea without a network namespace: Kea listens on the loopback
// with UDP sockets, and the test is a relay agent at 127.0.0.2 (giaddr), so Kea selects the subnet by the relay's
// address and answers it on port 67. What Kea does with a message (an offer, an acknowledgement, a NAK, silence)
// and with which lease time and options is then read from the packets themselves.

// DHCP message types.
const (
	dhcpDiscover = 1
	dhcpOffer    = 2
	dhcpRequest  = 3
	dhcpAck      = 5
	dhcpNak      = 6
)

// dhcpMsg is the part of a DHCP message the tests read and write.
type dhcpMsg struct {
	Type    byte
	XID     uint32
	CIAddr  netip.Addr
	YIAddr  netip.Addr
	GIAddr  netip.Addr
	MAC     [6]byte
	Options map[byte][]byte
}

func (m dhcpMsg) bytes() []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 1, 1, 6
	binary.BigEndian.PutUint32(b[4:], m.XID)
	binary.BigEndian.PutUint16(b[10:], 0x8000) // broadcast flag: the relay unicasts anyway
	copy(b[12:16], addr4(m.CIAddr))
	copy(b[24:28], addr4(m.GIAddr))
	copy(b[28:34], m.MAC[:])
	copy(b[236:240], []byte{99, 130, 83, 99})
	b = append(b, 53, 1, m.Type)
	for code, v := range m.Options {
		b = append(b, code, byte(len(v)))
		b = append(b, v...)
	}
	return append(b, 255)
}

func addr4(a netip.Addr) []byte {
	if !a.IsValid() {
		return make([]byte, 4)
	}
	x := a.As4()
	return x[:]
}

func parseDHCP(b []byte) (dhcpMsg, error) {
	if len(b) < 241 || b[236] != 99 || b[237] != 130 || b[238] != 83 || b[239] != 99 {
		return dhcpMsg{}, errors.New("not a DHCP message")
	}
	m := dhcpMsg{XID: binary.BigEndian.Uint32(b[4:]), Options: map[byte][]byte{}}
	m.CIAddr = netip.AddrFrom4([4]byte(b[12:16]))
	m.YIAddr = netip.AddrFrom4([4]byte(b[16:20]))
	copy(m.MAC[:], b[28:34])
	for i := 240; i < len(b); {
		code := b[i]
		if code == 255 {
			break
		}
		if code == 0 {
			i++
			continue
		}
		if i+1 >= len(b) || i+2+int(b[i+1]) > len(b) {
			return m, errors.New("truncated option")
		}
		v := b[i+2 : i+2+int(b[i+1])]
		if code == 53 {
			m.Type = v[0]
		} else {
			m.Options[code] = v
		}
		i += 2 + int(b[i+1])
	}
	return m, nil
}

// leaseTime is option 51 of a reply.
func (m dhcpMsg) leaseTime() (time.Duration, bool) {
	v, ok := m.Options[51]
	if !ok || len(v) != 4 {
		return 0, false
	}
	return time.Duration(binary.BigEndian.Uint32(v)) * time.Second, true
}

// addrs reads an option that is a list of addresses (routers, domain name servers, ntp servers).
func (m dhcpMsg) addrs(code byte) []netip.Addr {
	v := m.Options[code]
	var out []netip.Addr
	for i := 0; i+4 <= len(v); i += 4 {
		out = append(out, netip.AddrFrom4([4]byte(v[i:i+4])))
	}
	return out
}

// relay is the test's relay agent: the socket the replies of Kea come to.
type relay struct {
	t      *testing.T
	conn   *net.UDPConn
	server *net.UDPAddr
	addr   netip.Addr
	xid    uint32
}

// newRelay binds the relay's address on port 67, where a server answers a relay agent.
func newRelay(t *testing.T, addr string) *relay {
	t.Helper()
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var err error
		if cerr := c.Control(func(fd uintptr) { err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1) }); cerr != nil {
			return cerr
		}
		return err
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", addr+":67")
	if err != nil {
		t.Fatalf("the relay cannot listen on %s:67: %v", addr, err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return &relay{t: t, conn: pc.(*net.UDPConn), server: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 67}, addr: netip.MustParseAddr(addr)}
}

// send gives Kea a message of the client with this MAC and waits for the answer; ok is false when none came
// within wait (a server that is silent).
func (r *relay) send(m dhcpMsg, wait time.Duration) (dhcpMsg, bool) {
	r.t.Helper()
	r.xid++
	m.XID = 0x7000 + r.xid
	m.GIAddr = r.addr
	// what is left of an earlier exchange is not the answer to this one
	_ = r.conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	buf := make([]byte, 1500)
	for {
		if _, _, err := r.conn.ReadFromUDP(buf); err != nil {
			break
		}
	}
	if _, err := r.conn.WriteToUDP(m.bytes(), r.server); err != nil {
		r.t.Fatal(err)
	}
	deadline := time.Now().Add(wait)
	for {
		_ = r.conn.SetReadDeadline(deadline)
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return dhcpMsg{}, false
		}
		got, err := parseDHCP(buf[:n])
		if err == nil && got.XID == m.XID {
			return got, true
		}
	}
}

func macBytes(s string) [6]byte {
	hw, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	var m [6]byte
	copy(m[:], hw)
	return m
}

// discover asks for an address: the offer, or ok false for silence.
func (r *relay) discover(mac string, wait time.Duration) (dhcpMsg, bool) {
	return r.send(dhcpMsg{Type: dhcpDiscover, MAC: macBytes(mac), Options: map[byte][]byte{}}, wait)
}

// take is a client that gets a lease: discover, then request what was offered; the acknowledgement.
func (r *relay) take(mac string) dhcpMsg {
	r.t.Helper()
	offer, ok := r.discover(mac, 3*time.Second)
	if !ok || offer.Type != dhcpOffer {
		r.t.Fatalf("no offer for %s: %+v %v", mac, offer, ok)
	}
	req := dhcpMsg{Type: dhcpRequest, MAC: macBytes(mac), Options: map[byte][]byte{
		50: addr4(offer.YIAddr), 54: offer.Options[54]}}
	ack, ok := r.send(req, 3*time.Second)
	if !ok || ack.Type != dhcpAck {
		r.t.Fatalf("no acknowledgement for %s: %+v %v", mac, ack, ok)
	}
	return ack
}

// renew is the client that has an address and asks to keep it: a REQUEST with ciaddr (RENEWING), the answer.
func (r *relay) renew(mac string, ip netip.Addr, wait time.Duration) (dhcpMsg, bool) {
	return r.send(dhcpMsg{Type: dhcpRequest, MAC: macBytes(mac), CIAddr: ip, Options: map[byte][]byte{}}, wait)
}

// reboot is the client that remembers an address after a restart: a REQUEST for it without a server (INIT-REBOOT).
func (r *relay) reboot(mac string, ip netip.Addr, wait time.Duration) (dhcpMsg, bool) {
	return r.send(dhcpMsg{Type: dhcpRequest, MAC: macBytes(mac), Options: map[byte][]byte{50: addr4(ip)}}, wait)
}

// loopbackSubnet is the subnet of the tests below: the loopback network, served on `lo`.
func loopbackSubnet() Subnet {
	return Subnet{
		ID: 7, Network: "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", Subnet: netip.MustParsePrefix("127.0.0.0/8"), Interface: "lo",
		Pools:        []Pool{{netip.MustParseAddr("127.0.0.100"), netip.MustParseAddr("127.0.0.109")}},
		LeaseSeconds: 600, Router: netip.MustParseAddr("127.0.0.1"),
	}
}

// startLoopbackKea runs the real daemon on the loopback with UDP sockets (a relay's messages come to it there) and the
// configuration of cfg, whose subnets are served on `lo`. It returns the client of its control socket.
func startLoopbackKea(t *testing.T, cfg Config, mutate ...func(doc map[string]any)) *Client {
	t.Helper()
	bin, err := exec.LookPath("kea-dhcp4")
	if err != nil {
		t.Skip("kea-dhcp4 is not installed")
	}
	if os.Geteuid() != 0 {
		t.Skip("Kea opens sockets on port 67: it needs root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg.Socket, cfg.Leases = filepath.Join(dir, "ctrl.sock"), filepath.Join(dir, "leases.csv")
	cfg.Script = ""
	doc, err := cfg.Document()
	if err != nil {
		t.Fatal(err)
	}
	loopbackUDP(doc)
	for _, m := range mutate {
		m(doc)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "kea.json")
	if err := os.WriteFile(f, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", f)
	cmd.Env = append(os.Environ(), "KEA_CONTROL_SOCKET_DIR="+dir, "KEA_DHCP_DATA_DIR="+dir, "KEA_PIDFILE_DIR="+dir, "KEA_LOCKFILE_DIR="+dir, "KEA_LOG_FILE_DIR="+dir)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
		if t.Failed() {
			t.Logf("kea:\n%s", out.String())
		}
	})
	c := &Client{Socket: cfg.Socket, Timeout: 5 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Hash(context.Background()); err == nil {
			return c
		}
		select {
		case <-done:
			t.Fatalf("kea exited:\n%s", out.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("kea does not answer:\n%s", out.String())
	return nil
}

// loopbackUDP makes a document use UDP sockets, which can be opened on the loopback, and no retries.
func loopbackUDP(doc map[string]any) {
	ic := doc["Dhcp4"].(jsonMap)["interfaces-config"].(jsonMap)
	ic["dhcp-socket-type"] = "udp"
}

// applyLoopback sends a configuration to the running Kea the way the engine does, with the loopback's socket type.
func applyLoopback(t *testing.T, c *Client, cfg Config) {
	t.Helper()
	cfg.Socket, cfg.Leases = c.Socket, strings.TrimSuffix(c.Socket, "ctrl.sock")+"leases.csv"
	cfg.Script = ""
	doc, err := cfg.Document()
	if err != nil {
		t.Fatal(err)
	}
	loopbackUDP(doc)
	if err := c.Apply(context.Background(), doc); err != nil {
		t.Fatalf("Kea refuses the configuration: %v", err)
	}
}

const (
	macA = "02:00:00:00:00:aa"
	macB = "02:00:00:00:00:bb"
	macC = "02:00:00:00:00:cc"
)

// asksFor is a DISCOVER that asks for a lease time (option 51).
func (r *relay) asksFor(mac string, lease time.Duration) (dhcpMsg, bool) {
	v := make([]byte, 4)
	binary.BigEndian.PutUint32(v, uint32(lease/time.Second))
	return r.send(dhcpMsg{Type: dhcpDiscover, MAC: macBytes(mac), Options: map[byte][]byte{51: v}}, 3*time.Second)
}

func leaseOf(t *testing.T, m dhcpMsg) time.Duration {
	t.Helper()
	d, ok := m.leaseTime()
	if !ok {
		t.Fatalf("the message has no lease time: %+v", m)
	}
	return d
}

// A short lease of a network (M23): the subnet's lease time, and the bounds a client may ask within, are the short
// time, so a client that asks for an hour (most clients ask for something) still gets the short lease. Without the
// bounds the subnet would give it the hour, which is the maximum of an ordinary subnet.
func TestAShortLeaseOfASubnetIsWhatEveryClientGetsEvenOneThatAsksForAnHour(t *testing.T) {
	cfg := Config{Subnets: []Subnet{loopbackSubnet()}}
	cfg.Subnets[0].LeaseSeconds = 600
	c := startLoopbackKea(t, cfg)
	r := newRelay(t, "127.0.0.2")
	if offer, _ := r.asksFor(macA, time.Hour); leaseOf(t, offer) != time.Hour {
		t.Fatalf("an ordinary subnet gives a client its hour: %v", leaseOf(t, offer))
	}
	cfg.Subnets[0].LeaseSeconds, cfg.Subnets[0].LeaseMin, cfg.Subnets[0].LeaseMax = 7, 7, 7
	applyLoopback(t, c, cfg)
	offer, ok := r.asksFor(macB, time.Hour)
	if !ok || leaseOf(t, offer) != 7*time.Second {
		t.Fatalf("a client that asks for an hour: %+v", offer)
	}
	if ack := r.take(macC); leaseOf(t, ack) != 7*time.Second {
		t.Errorf("a client that asks for nothing: %v", leaseOf(t, ack))
	}
}

// A lease class gives the clients of a device a lease time of their own while the subnet keeps its time for the others.
func TestALeaseClassGivesItsClientsTheirOwnLeaseTimeAndNoOtherClient(t *testing.T) {
	cfg := Config{Subnets: []Subnet{loopbackSubnet()}, LeaseClasses: []LeaseClass{{Name: "cgw-test", MACs: []string{macA, "02:00:00:00:00:A1"}, Seconds: 9, Min: 9, Max: 9}}}
	startLoopbackKea(t, cfg)
	r := newRelay(t, "127.0.0.2")
	for _, mac := range []string{macA, "02:00:00:00:00:a1"} {
		if ack := r.take(mac); leaseOf(t, ack) != 9*time.Second {
			t.Errorf("%s: %v, want its class's 9s", mac, leaseOf(t, ack))
		}
		if offer, _ := r.asksFor(mac, time.Hour); leaseOf(t, offer) != 9*time.Second {
			t.Errorf("%s asking for an hour: %v", mac, leaseOf(t, offer))
		}
	}
	if ack := r.take(macB); leaseOf(t, ack) != 10*time.Minute {
		t.Errorf("another client: %v, want the subnet's 10m", leaseOf(t, ack))
	}
}

// Silence: the clients of the list are not answered, whichever message they send, and the others are. A silenced
// interface is silent for every client but the ones it excepts.
func TestSilencedClientsAreNotAnsweredAndTheOthersAre(t *testing.T) {
	cfg := Config{Subnets: []Subnet{loopbackSubnet()}, Silenced: []Silence{{MACs: []string{macA}}}}
	c := startLoopbackKea(t, cfg)
	r := newRelay(t, "127.0.0.2")
	if m, ok := r.discover(macA, time.Second); ok {
		t.Fatalf("a silenced client got %+v", m)
	}
	if m, ok := r.renew(macA, netip.MustParseAddr("127.0.0.100"), time.Second); ok {
		t.Fatalf("a silenced client that renews got %+v", m)
	}
	if m, ok := r.reboot(macA, netip.MustParseAddr("127.0.0.100"), time.Second); ok {
		t.Fatalf("a silenced client that restarts got %+v", m)
	}
	r.take(macB)

	// the whole interface, with an exception
	cfg.Silenced = []Silence{{Interface: "lo", Except: []string{macC}}}
	applyLoopback(t, c, cfg)
	if m, ok := r.discover(macB, time.Second); ok {
		t.Fatalf("a client of a silent interface got %+v", m)
	}
	if m, ok := r.renew(macB, netip.MustParseAddr("127.0.0.100"), time.Second); ok {
		t.Fatalf("a client of a silent interface that renews got %+v", m)
	}
	if ack := r.take(macC); !ack.YIAddr.IsValid() {
		t.Error("the excepted client was not answered")
	}
	// an interface that is not the client's is no reason for silence
	cfg.Silenced = []Silence{{Interface: "eth9"}}
	applyLoopback(t, c, cfg)
	r.take(macA)
	// no silence at all: everybody
	cfg.Silenced = nil
	applyLoopback(t, c, cfg)
	r.take(macB)
}

// Force a new IP (the plan's "change the reservation → the renewing client gets a NAK and requests a new address"):
// the client that renews, and the one that restarts and asks for its old address, get a NAK, and the discover that follows
// offers the reserved address. When the reservation goes the client keeps what it has.
func TestAReservationOfAnotherAddressNaksTheRenewalAndTheClientGetsTheReservedAddress(t *testing.T) {
	cfg := Config{Subnets: []Subnet{loopbackSubnet()}}
	c := startLoopbackKea(t, cfg)
	r := newRelay(t, "127.0.0.2")
	old := r.take(macA).YIAddr
	if m, ok := r.renew(macA, old, 2*time.Second); !ok || m.Type != dhcpAck {
		t.Fatalf("before the reservation: %+v", m)
	}
	moved := netip.MustParseAddr("127.0.0.108")
	cfg.Subnets[0].Reservations = []Reservation{{MAC: macA, IP: moved}}
	applyLoopback(t, c, cfg)
	if m, ok := r.renew(macA, old, 2*time.Second); !ok || m.Type != dhcpNak {
		t.Fatalf("the renewal after the reservation: %+v %v, want a NAK", m, ok)
	}
	if m, ok := r.reboot(macA, old, 2*time.Second); !ok || m.Type != dhcpNak {
		t.Fatalf("the restart after the reservation: %+v %v, want a NAK", m, ok)
	}
	if got := r.take(macA).YIAddr; got != moved {
		t.Fatalf("the client got %s, want its reservation %s", got, moved)
	}
	// the reservation goes: the lease it made stays
	cfg.Subnets[0].Reservations = nil
	applyLoopback(t, c, cfg)
	if m, ok := r.renew(macA, moved, 2*time.Second); !ok || m.Type != dhcpAck || m.YIAddr != moved {
		t.Errorf("after the reservation went: %+v %v", m, ok)
	}
	// another client is never given the reserved address of a client that has none yet
	cfg.Subnets[0].Reservations = []Reservation{{MAC: macB, IP: netip.MustParseAddr("127.0.0.101")}}
	applyLoopback(t, c, cfg)
	if got := r.take(macC).YIAddr; got == netip.MustParseAddr("127.0.0.101") {
		t.Error("an address reserved for another client was handed out")
	}
}

// A reservation without an address changes the options of its client only.
func TestAReservationWithOptionsOnlyChangesTheOptionsOfItsClient(t *testing.T) {
	cfg := Config{Subnets: []Subnet{loopbackSubnet()}}
	cfg.Subnets[0].DNS = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	cfg.Subnets[0].Reservations = []Reservation{{MAC: macA, Options: Options{Router: netip.MustParseAddr("127.0.0.9"),
		DNS: []netip.Addr{netip.MustParseAddr("192.0.2.53")}, Domain: "x.test", Custom: []CustomOption{{Code: 224, Value: "0a0b0c"}}}}}
	startLoopbackKea(t, cfg)
	r := newRelay(t, "127.0.0.2")
	a, b := r.take(macA), r.take(macB)
	if got := a.addrs(3); len(got) != 1 || got[0].String() != "127.0.0.9" {
		t.Errorf("router of the client with options: %v", got)
	}
	if got := a.addrs(6); len(got) != 1 || got[0].String() != "192.0.2.53" {
		t.Errorf("DNS of the client with options: %v", got)
	}
	if string(a.Options[15]) != "x.test" || string(a.Options[224]) != "\n\x0b\x0c" {
		t.Errorf("domain and custom option: %q %q", a.Options[15], a.Options[224])
	}
	if got := b.addrs(3); len(got) != 1 || got[0].String() != "127.0.0.1" {
		t.Errorf("router of the other client: %v", got)
	}
	if got := b.addrs(6); len(got) != 1 || got[0].String() != "127.0.0.1" || b.Options[15] != nil {
		t.Errorf("the other client's options: %v %q", got, b.Options[15])
	}
}

// DeleteLeases: the leases of some clients, or of the whole subnet; a client that renews gets the same address again.
func TestDeletingLeasesRemovesThoseOfTheClientsOrTheSubnetAndTheClientGetsTheSameAddressBack(t *testing.T) {
	cfg := Config{Subnets: []Subnet{loopbackSubnet()}}
	c := startLoopbackKea(t, cfg)
	ctx := context.Background()
	r := newRelay(t, "127.0.0.2")
	a, b, cc := r.take(macA).YIAddr, r.take(macB).YIAddr, r.take(macC).YIAddr
	if n, err := c.DeleteLeases(ctx, 7, []string{"02:00:00:00:00:AA"}); err != nil || n != 1 {
		t.Fatalf("deleting a client's lease: %d %v", n, err)
	}
	got, _ := c.Leases(ctx)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if n, err := c.DeleteLeases(ctx, 7, []string{macA}); err != nil || n != 0 {
		t.Errorf("a client without a lease: %d %v", n, err)
	}
	if m, ok := r.renew(macA, a, 2*time.Second); !ok || m.Type != dhcpAck || m.YIAddr != a {
		t.Errorf("the renewal after the deletion: %+v %v, want the same address %s", m, ok, a)
	}
	if n, err := c.DeleteLeases(ctx, 7, nil); err != nil || n != 3 {
		t.Fatalf("deleting the subnet's leases: %d %v", n, err)
	}
	if got, _ := c.Leases(ctx); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if n, err := c.DeleteLeases(ctx, 7, nil); err != nil || n != 0 {
		t.Errorf("an empty subnet: %d %v", n, err)
	}
	for mac, ip := range map[string]netip.Addr{macB: b, macC: cc} {
		if m, ok := r.renew(mac, ip, 2*time.Second); !ok || m.Type != dhcpAck {
			t.Errorf("%s after the wipe: %+v %v", mac, m, ok)
		}
	}
	// a subnet that is not there
	if n, err := c.DeleteLeases(ctx, 99, nil); err != nil || n != 0 {
		t.Errorf("another subnet: %d %v", n, err)
	}
}
