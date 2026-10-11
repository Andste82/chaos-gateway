//go:build testbed

package compiler

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The kernel test of docs/open-items.md P2-M10-09: a packet of a flow whose fault does not change is
// classified while other faults are written. Two dense UDP streams (esp32-42 through the `dev` map,
// lab-host through the `devdest` map, see classifyChurn) reach a gateway namespace while the table
// is switched between the two targets of the churn again and again, each switch one transaction
// like an apply's. A counter of a table of its own, on prerouting before conntrack, counts what
// arrived; the fault's own counter (in its mark chain) what was classified. The difference is the
// number of packets classification missed.
//
// First with every set and map flushed and filled (what every apply did before): the kernel's interval
// sets can miss lookups during such a commit (see setUpdate; the native CI runner's 6.17 kernel does), and
// the number is logged, not asserted (it depends on the kernel and on the timing). Then with the transaction of the product, which changes the
// elements by their difference: the elements of the two flows are never touched, so not one packet
// may be missed.
func TestAFlowKeepsItsClassificationWhileOtherFaultsAreWritten(t *testing.T) {
	c := newClassifyChurn(t)
	bed := testbed.New(t)
	gw := bed.Add("clsgw")
	h42 := bed.Add("cls42")
	hLab := bed.Add("clslab")
	bed.Link(testbed.End{NS: gw, If: "lan0", Addr: "10.10.0.1/24"}, testbed.End{NS: h42, If: "eth0", Addr: "10.10.0.42/24"})
	bed.Link(testbed.End{NS: gw, If: "lan1", Addr: churnDest + "/24"}, testbed.End{NS: hLab, If: "eth0", Addr: "10.20.0.50/24"})

	apply := func(tx []byte) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := gw.Command(ctx, "nft", "-j", "-f", "-")
		cmd.Stdin = bytes.NewReader(tx)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the transaction is refused: %v: %s", err, out)
		}
	}
	listing := func() *linux.Ruleset {
		t.Helper()
		rs, err := linux.ParseNft([]byte(gw.Must("nft", "-j", "list", "table", "inet", "chaosgw")))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	first, err := c.a.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	apply(first)
	// what arrives, counted before conntrack (-200) and so before classification (-150)
	gw.MustStdin(`table ip clsprobe {
	counter arrived42 {}
	counter arrivedlab {}
	chain pre {
		type filter hook prerouting priority -300; policy accept;
		ip saddr 10.10.0.42 udp dport 9 counter name "arrived42"
		ip saddr 10.20.0.50 udp dport 9 counter name "arrivedlab"
	}
}`, "nft", "-f", "-")

	type counts struct{ arrived42, arrivedLab, classified42, classifiedLab int64 }
	read := func() counts {
		t.Helper()
		rs, err := linux.ParseNft([]byte(gw.Must("nft", "-j", "list", "counters")))
		if err != nil {
			t.Fatal(err)
		}
		var n counts
		for _, o := range rs.Objects {
			if o.Counter == nil {
				continue
			}
			switch o.Counter.Table + "/" + o.Counter.Name {
			case "clsprobe/arrived42":
				n.arrived42 = o.Counter.Packets
			case "clsprobe/arrivedlab":
				n.arrivedLab = o.Counter.Packets
			case "chaosgw/" + c.dev42.CounterUp:
				n.classified42 = o.Counter.Packets
			case "chaosgw/" + c.devdest.CounterUp:
				n.classifiedLab = o.Counter.Packets
			}
		}
		return n
	}

	// the streams: bursts every millisecond (an emulated machine sends what it can)
	burst := 20
	if !testbed.Accurate() {
		burst = 4
	}
	stream := func(ns *testbed.Namespace, src, dst string) (stop func()) {
		pc, err := testbed.ListenPacketIn(ns.Name, "udp4", src+":0")
		if err != nil {
			t.Fatal(err)
		}
		to := &net.UDPAddr{IP: net.ParseIP(dst), Port: 9}
		var quit atomic.Bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := make([]byte, 64)
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for !quit.Load() {
				for range burst {
					_, _ = pc.WriteTo(payload, to)
				}
				<-tick.C
			}
		}()
		return func() { quit.Store(true); wg.Wait(); _ = pc.Close() }
	}
	// phase runs the streams for d, and at least for minTx transactions, while churn (if not nil)
	// switches the table, and returns what the counters counted in the phase and how many
	// transactions ran. The streams stop before the counters are read, so no packet is between the two
	// hooks then.
	phase := func(d time.Duration, minTx int, churn func(to *Target)) (counts, int) {
		t.Helper()
		before := read()
		stop42 := stream(h42, "10.10.0.42", "10.10.0.1")
		stopLab := stream(hLab, "10.20.0.50", churnDest)
		n := 0
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) || (churn != nil && n < minTx) {
			if churn == nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			to := c.b
			if n%2 == 1 {
				to = c.a
			}
			churn(to)
			n++
		}
		stop42()
		stopLab()
		time.Sleep(200 * time.Millisecond)
		after := read()
		return counts{after.arrived42 - before.arrived42, after.arrivedLab - before.arrivedLab,
			after.classified42 - before.classified42, after.classifiedLab - before.classifiedLab}, n
	}
	missed := func(n counts) (int64, int64) { return n.arrived42 - n.classified42, n.arrivedLab - n.classifiedLab }

	quiet, _ := phase(time.Second, 0, nil)
	t.Logf("without changes: esp32-42 %d arrived, %d classified; lab-host %d arrived, %d classified", quiet.arrived42, quiet.classified42, quiet.arrivedLab, quiet.classifiedLab)
	if m42, mLab := missed(quiet); quiet.arrived42 == 0 || quiet.arrivedLab == 0 || m42 != 0 || mLab != 0 {
		t.Fatalf("the streams are not classified even without changes: %+v", quiet)
	}

	// natively a phase is a few hundred transactions; an emulated machine needs seconds for one
	d, minTx := 8*time.Second, 20
	if !testbed.Accurate() {
		d, minTx = 10*time.Second, 6
	}
	flushed, nf := phase(d, minTx, func(to *Target) {
		tx, err := to.Nft.transaction(listing(), false)
		if err != nil {
			t.Fatal(err)
		}
		apply(tx)
	})
	f42, fLab := missed(flushed)
	t.Logf("flush and fill, %d transactions: esp32-42 (dev map) %d of %d not classified, lab-host (devdest map) %d of %d not classified",
		nf, f42, flushed.arrived42, fLab, flushed.arrivedLab)
	if f42 > 0 || fLab > 0 {
		t.Logf("this kernel (%s) misses lookups of interval sets whose elements a transaction flushes and adds again", gw.Must("uname", "-r"))
	}

	// back to a: the transaction of the product from here on
	apply(mustTx(t, c.a.Nft, listing()))
	diffed, nd := phase(d, minTx, func(to *Target) { apply(mustTx(t, to.Nft, listing())) })
	d42, dLab := missed(diffed)
	t.Logf("by difference, %d transactions: esp32-42 (dev map) %d of %d not classified, lab-host (devdest map) %d of %d not classified",
		nd, d42, diffed.arrived42, dLab, diffed.arrivedLab)
	if d42 != 0 || dLab != 0 {
		t.Errorf("packets of flows whose fault did not change were not classified while other faults were written: %d of esp32-42's, %d of lab-host's", d42, dLab)
	}
	if diffed.arrived42 < 1000 || diffed.arrivedLab < 1000 {
		t.Errorf("the churn was too short to say anything: %d transactions, %d and %d packets", nd, diffed.arrived42, diffed.arrivedLab)
	}

	// and the kernel holds the elements of the last target exactly
	last := c.b
	if nd%2 == 0 {
		last = c.a
	}
	if diffs := sameModel(kernelModel(t, listing(), last), modelOf(last)); diffs != "" {
		t.Errorf("after the churn the kernel holds other elements than the target:\n%s", diffs)
	}
}

func mustTx(t *testing.T, n Nft, current *linux.Ruleset) []byte {
	t.Helper()
	tx, err := n.Transaction(current)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// kernelModel reads the elements of the target's static sets and maps from a listing.
func kernelModel(t *testing.T, rs *linux.Ruleset, tg *Target) elementModel {
	t.Helper()
	m := elementModel{}
	for _, s := range tg.Nft.Sets {
		if s.Dynamic {
			continue
		}
		m[s.Name] = map[string]string{}
		if got := rs.Set(s.Name); got != nil {
			for _, e := range got.Elements() {
				m[s.Name][e] = ""
			}
		}
	}
	for _, d := range tg.Nft.Maps {
		m[d.Name] = map[string]string{}
		if got := rs.Set(d.Name); got != nil {
			for k, v := range got.Pairs() {
				m[d.Name][k] = v
			}
		}
	}
	if len(m) == 0 {
		t.Fatal(fmt.Errorf("the target has no sets"))
	}
	return m
}
