package api

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const counterOverlay = "0192f0a1-0000-7000-8000-000000000001"

// profileContext is the overlay context of an activated profile with an impairment part (the kernel fault with the key k, made in
// the generation epoch) and a dns part, read with the given counters.
func profileContext(epoch, packets, dnsHits int64) overlayContext {
	snap := &engine.Snapshot{
		Faults:      []compiler.Fault{{Key: "k", Layer: "overlay", Source: counterOverlay, CounterUp: "up", CounterDown: "down"}},
		FaultEpochs: map[string]int64{"k": epoch},
		DNSFaults:   []compiler.DNSFault{{Overlay: counterOverlay}},
	}
	return overlayContext{snap: snap, counters: map[string]engine.CounterValue{"up": {Packets: packets}},
		dnsHits: map[string]int64{counterOverlay: dnsHits}, dnsEpoch: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC).UnixMilli()}
}

// P5-AUDIT-13: the counter of a profile with a kernel part and a dns part changes its epoch when the kernel fault is made anew (its
// counters start over), although the DNS part's epoch, the start of the API, stays: a run takes two readings with the same epoch as
// one counter that went on and subtracts them, so a kernel counter that started over would give the step nothing and a false zero_hit.
func TestTheEpochOfAProfileCounterWithADNSPartChangesWhenItsKernelFaultIsMadeAnew(t *testing.T) {
	read := func(oc overlayContext) model.Counter {
		c := oc.withDNS(oc.counterOf("overlay", counterOverlay), counterOverlay)
		if c == nil {
			t.Fatal("no counter")
		}
		return *c
	}
	first := read(profileContext(3, 50, 2))
	if first.Packets != 52 {
		t.Fatalf("%+v", first)
	}
	// the same parts read again: the same epoch, so the readings may be subtracted
	if again := read(profileContext(3, 70, 4)); again.Epoch != first.Epoch {
		t.Errorf("the epoch changed without a restart: %d, then %d", first.Epoch, again.Epoch)
	}
	// the kernel fault was overridden and came back in generation 7: its counter starts over
	if reborn := read(profileContext(7, 10, 4)); reborn.Epoch == first.Epoch {
		t.Errorf("the epoch %d stayed when the kernel fault was made anew: a run would subtract 52 from 14 and see no packet", reborn.Epoch)
	}
	// the API restarted (the DNS part's epoch): the epoch changes too
	restarted := profileContext(3, 70, 0)
	restarted.dnsEpoch++
	if c := read(restarted); c.Epoch == first.Epoch {
		t.Error("the epoch stayed when the DNS counters started over")
	}
	// a DNS overlay alone keeps the DNS epoch, and an overlay in no DNS list keeps its kernel counter as it is
	only := overlayContext{snap: &engine.Snapshot{DNSFaults: []compiler.DNSFault{{Overlay: counterOverlay}}}, dnsHits: map[string]int64{counterOverlay: 3}, dnsEpoch: 42}
	if c := only.withDNS(nil, counterOverlay); c == nil || c.Epoch != 42 || c.Packets != 3 {
		t.Errorf("%+v", c)
	}
	kernel := profileContext(3, 50, 0)
	kernel.snap.DNSFaults = nil
	if c := kernel.withDNS(kernel.counterOf("overlay", counterOverlay), counterOverlay); c == nil || c.Epoch != 3 {
		t.Errorf("%+v", c)
	}
}

// P5-AUDIT-15: the per-overlay counters of the DNS faults and of the TLS cases forget the overlays that are not in the store any more
// when the snapshot changes, and a read copies only those of the overlays in a DNS or TLS list.
func TestTheDNSAndTLSCountersOfARemovedOverlayAreForgotten(t *testing.T) {
	s := &Server{clk: clock.NewFake(time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC))}
	kept, gone, waiting := counterOverlay, "0192f0a1-0000-7000-8000-000000000002", "0192f0a1-0000-7000-8000-000000000003"
	s.dns.logMu.Lock()
	for _, id := range []string{kept, gone, waiting} {
		s.countFault(id)
	}
	s.countFault(kept)
	s.dns.logMu.Unlock()
	s.tls.logMu.Lock()
	for _, id := range []string{kept, gone, waiting} {
		s.countTLS(id)
	}
	s.tls.logMu.Unlock()
	// gone was deleted; waiting is in the store but overridden, in no list: it keeps its count for when it is back
	snap := &engine.Snapshot{
		Overlays:  []model.Overlay{{Id: uuid.MustParse(kept)}, {Id: uuid.MustParse(waiting)}},
		DNSFaults: []compiler.DNSFault{{Overlay: kept}},
		TLSCases:  []compiler.TLSCase{{Overlay: kept}},
	}
	dns, _ := s.dnsHitsSnapshot(snap)
	tls, _ := s.tlsHitsSnapshot(snap)
	if len(dns) != 1 || dns[kept] != 2 || len(tls) != 1 || tls[kept] != 1 {
		t.Errorf("the copies hold more than the overlays in a list: dns %v, tls %v", dns, tls)
	}
	s.dns.logMu.Lock()
	_, dnsGone := s.dns.faultHits[gone]
	dnsWaiting := s.dns.faultHits[waiting]
	s.dns.logMu.Unlock()
	s.tls.logMu.Lock()
	_, tlsGone := s.tls.hits[gone]
	tlsWaiting := s.tls.hits[waiting]
	s.tls.logMu.Unlock()
	if dnsGone || tlsGone {
		t.Error("the counter of a removed overlay is still kept")
	}
	if dnsWaiting != 1 || tlsWaiting != 1 {
		t.Errorf("the counter of an overlay in the store was forgotten: dns %d, tls %d", dnsWaiting, tlsWaiting)
	}
}
