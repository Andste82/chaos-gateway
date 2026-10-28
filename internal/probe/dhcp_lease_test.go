package probe

import (
	"strings"
	"testing"
	"time"
)

// offerAndAck drives the client to the ACK and answers it with the options given; the offer is a good one.
func (h *harness) offerAndAck(yiaddr string, ack map[byte][]byte) *message {
	h.t.Helper()
	m := request(h.t, h.next())
	if m.typ != msgDiscover {
		h.t.Fatalf("not a DISCOVER: %d", m.typ)
	}
	h.sock.rx <- reply(msgOffer, m.xid, "10.10.0.77", leaseOpts(3600))
	r := request(h.t, h.next())
	if r.typ != msgRequest {
		h.t.Fatalf("not a REQUEST: %d", r.typ)
	}
	h.sock.rx <- reply(msgAck, r.xid, yiaddr, ack)
	return r
}

// A lease that RFC 2131 does not allow is no lease: the client does not bind to it, pauses and starts over (P6-AUDIT-06).
func TestAMalformedAckIsNoLease(t *testing.T) {
	without := func(code byte) map[byte][]byte {
		o := leaseOpts(3600)
		delete(o, code)
		return o
	}
	with := func(code byte, v []byte) map[byte][]byte {
		o := leaseOpts(3600)
		o[code] = v
		return o
	}
	for name, c := range map[string]struct {
		yiaddr string
		opts   map[byte][]byte
	}{
		"an unspecified address":      {"0.0.0.0", leaseOpts(3600)},
		"the broadcast address":       {"255.255.255.255", leaseOpts(3600)},
		"a multicast address":         {"224.0.0.9", leaseOpts(3600)},
		"a loopback address":          {"127.0.0.1", leaseOpts(3600)},
		"no lease time":               {"10.10.0.77", without(optLeaseTime)},
		"a lease time of zero":        {"10.10.0.77", with(optLeaseTime, u32(0))},
		"a lease time of one second":  {"10.10.0.77", with(optLeaseTime, u32(1))},
		"no server identifier":        {"10.10.0.77", without(optServerID)},
		"another server than offered": {"10.10.0.77", with(optServerID, addr4("10.10.0.99"))},
		"a mask with holes":           {"10.10.0.77", with(optSubnetMask, addr4("255.0.255.0"))},
		"a mask of zero":              {"10.10.0.77", with(optSubnetMask, addr4("0.0.0.0"))},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			first := h.offerAndAck(c.yiaddr, c.opts)
			select {
			case l := <-h.bound:
				t.Fatalf("bound to %+v", l)
			case <-time.After(100 * time.Millisecond):
			}
			if h.c.Lease() != nil {
				t.Fatalf("the client holds %+v", h.c.Lease())
			}
			h.clk.BlockUntil(1)
			h.clk.Advance(2 * time.Second)
			if d := request(t, h.next()); d.typ != msgDiscover || d.xid == first.xid {
				t.Errorf("the client does not start over: %+v", d)
			}
		})
	}
}

func TestAnOfferWithoutServerOrAddressIsNotAnswered(t *testing.T) {
	for name, c := range map[string]struct {
		yiaddr string
		opts   map[byte][]byte
	}{
		"no server identifier": {"10.10.0.77", map[byte][]byte{optLeaseTime: u32(3600)}},
		"no address":           {"0.0.0.0", leaseOpts(3600)},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			m := request(t, h.next())
			h.sock.rx <- reply(msgOffer, m.xid, c.yiaddr, c.opts)
			select {
			case d := <-h.sock.sent:
				t.Fatalf("the client answers the offer: %+v", request(t, d))
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

// A server that sets T1 to 0 must not have the probe renew as fast as the server answers (P6-AUDIT-07).
func TestARenewalTimeOfZeroDoesNotMakeATightLoop(t *testing.T) {
	h := newHarness(t)
	opts := leaseOpts(100)
	opts[optRenewalT1] = u32(0)
	opts[optRebindingT2] = u32(0)
	m := request(t, h.next())
	h.sock.rx <- reply(msgOffer, m.xid, "10.10.0.77", opts)
	r := request(t, h.next())
	h.sock.rx <- reply(msgAck, r.xid, "10.10.0.77", opts)
	if l := h.nextBound(); l.T1 != time.Second || l.T2 != 2*time.Second {
		t.Fatalf("the timers have no floor: %+v", l)
	}
	renewals := 0
	for i := 0; i < 60; i++ {
		// The clock moves in steps of 100 ms until the renewal comes. A timer that the client registers late (the fake clock
		// also holds the timer of the request that was just answered, so BlockUntil cannot tell) only delays it, and the
		// renewal must not come before the floor of a second has passed: the answer to a renewal does not start the next one.
		var rm *message
		for waited := time.Duration(0); ; waited += 100 * time.Millisecond {
			if waited > 5*time.Second {
				t.Fatalf("no renewal after %v", waited)
			}
			select {
			case d := <-h.sock.sent:
				if waited < 900*time.Millisecond {
					t.Fatalf("a message after %v: %+v", waited, request(t, d))
				}
				rm = request(t, d)
			case <-time.After(20 * time.Millisecond):
				h.clk.Advance(100 * time.Millisecond)
				continue
			}
			break
		}
		if rm.typ != msgRequest {
			t.Fatalf("%+v", rm)
		}
		renewals++
		h.sock.rx <- reply(msgAck, rm.xid, "10.10.0.77", opts)
	}
	if renewals != 60 {
		t.Errorf("%d renewals in a minute", renewals)
	}
}

// A server that refuses every request is asked less and less often, and never by an immediate DISCOVER.
func TestANakIsFollowedByAGrowingPause(t *testing.T) {
	h := newHarness(t)
	var xids []uint32
	for _, pause := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 16 * time.Second} {
		m := request(t, h.next())
		xids = append(xids, m.xid)
		h.sock.rx <- reply(msgOffer, m.xid, "10.10.0.77", leaseOpts(3600))
		r := request(t, h.next())
		h.sock.rx <- reply(msgNak, r.xid, "", nil)
		time.Sleep(50 * time.Millisecond) // the client takes the NAK and sets its pause
		h.clk.BlockUntil(1)
		h.clk.Advance(pause - time.Millisecond)
		select {
		case d := <-h.sock.sent:
			t.Fatalf("a message %v into a pause of %v: %+v", pause-time.Millisecond, pause, request(t, d))
		case <-time.After(50 * time.Millisecond):
		}
		h.clk.Advance(time.Millisecond)
	}
	if len(xids) != 5 {
		t.Errorf("%v", xids)
	}
}

func TestTheRepetitionsOfARenewalKeepTheirTransactionId(t *testing.T) {
	h := newHarness(t)
	h.acquire(100)
	h.clk.BlockUntil(1)
	h.clk.Advance(50 * time.Second)
	first := request(t, h.next())
	h.clk.BlockUntil(1)
	h.clk.Advance(19 * time.Second) // the wait for the answer is half of what is left until T2
	second := request(t, h.next())
	if second.typ != msgRequest || second.xid != first.xid {
		t.Fatalf("a repeated renewal is a new transaction: %d then %d", first.xid, second.xid)
	}
	// the answer to the first try, arriving late, is taken
	h.sock.rx <- reply(msgAck, first.xid, "10.10.0.77", leaseOpts(200))
	deadline := time.Now().Add(5 * time.Second)
	for h.c.Lease() == nil || h.c.Lease().Duration != 200*time.Second {
		if time.Now().After(deadline) {
			t.Fatalf("the late answer was not taken: %+v", h.c.Lease())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnAckWithoutALeaseTimeIsNotARenewal(t *testing.T) {
	h := newHarness(t)
	h.acquire(100)
	h.clk.BlockUntil(1)
	h.clk.Advance(50 * time.Second)
	m := request(t, h.next())
	bad := leaseOpts(100)
	delete(bad, optLeaseTime)
	h.sock.rx <- reply(msgAck, m.xid, "10.10.0.77", bad)
	time.Sleep(50 * time.Millisecond)
	if l := h.c.Lease(); l == nil || l.Duration != 100*time.Second {
		t.Errorf("the lease changed: %+v", l)
	}
	if !strings.Contains(h.c.Lease().Addr.String(), "10.10.0.77") {
		t.Errorf("%+v", h.c.Lease())
	}
}
