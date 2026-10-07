package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"
)

// scaleWorld has n overlays on the network IoT; kind chooses what each one selects: "dp" its own
// destination and its own tcp port, "d" its own destination only, "p" its own port only.
func scaleWorld(t *testing.T, n int, kind string) *testWorld {
	t.Helper()
	tw := newTestWorld(t, false)
	for i := 0; i < n; i++ {
		var body string
		switch kind {
		case "dp":
			body = fmt.Sprintf(`{target: {network: IoT}, fault: {destination: {cidr: "%d.%d.%d.0/24"}, protocol: tcp, ports: [%d], latency: 50ms}}`, 11+i/65536, (i/256)%256, i%256, 1000+i)
		case "d":
			body = fmt.Sprintf(`{target: {network: IoT}, fault: {destination: {cidr: "%d.%d.%d.0/24"}, latency: 50ms}}`, 11+i/65536, (i/256)%256, i%256)
		case "p":
			body = fmt.Sprintf(`{target: {network: IoT}, fault: {protocol: tcp, ports: [%d], latency: 50ms}}`, 1000+i)
		case "mixed": // half destination-only, half port-only: the level-1 grid is the product
			if i%2 == 0 {
				body = fmt.Sprintf(`{target: {network: IoT}, fault: {destination: {cidr: "%d.%d.%d.0/24"}, latency: 50ms}}`, 11+i/65536, (i/256)%256, i%256)
			} else {
				body = fmt.Sprintf(`{target: {network: IoT}, fault: {protocol: tcp, ports: [%d], loss: 1%%}}`, 1000+i)
			}
		}
		tw.overlay(body, time.Duration(i)*time.Millisecond)
	}
	return tw
}

func TestTableOfOverlaysThatEachNameTheirOwnDestinationAndPortIsLinear(t *testing.T) {
	// The grid of (destination piece, port piece) cells has n x n cells of which n matter. Looking at
	// all of them, each against all candidates, took 11 s for 160 overlays.
	const n = 1500
	w := scaleWorld(t, n, "dp").world()
	start := time.Now()
	tab, err := w.Table(sourceA, FamilyImpairment)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Entries) != n {
		t.Fatalf("%d entries, want %d", len(tab.Entries), n)
	}
	if took > 60*time.Second {
		t.Fatalf("the table of %d overlays took %v", n, took)
	}
	for _, i := range []int{0, 1, 255, 256, 1000, n - 1} {
		q := Query{Source: subjectA, DestIP: netip.MustParseAddr(fmt.Sprintf("%d.%d.%d.9", 11+i/65536, (i/256)%256, i%256)), Protocol: "tcp", Port: 1000 + i}
		got, _ := tab.Lookup(q)
		if want := Winner(w.Resolve(q), FamilyImpairment); got == nil || want == nil || got.ID != want.ID {
			t.Fatalf("overlay %d: lookup %v, resolve %v", i, got, want)
		}
		q.Port = 1000 + (i+1)%n // the destination of one overlay with the port of another matches neither
		if got, _ := tab.Lookup(q); got != nil {
			t.Fatalf("overlay %d: a mixed-up port finds %v", i, got)
		}
	}
}

func TestTableOfDestinationOnlyAndPortOnlyOverlaysIsTheProductOfBothAndStaysResolvable(t *testing.T) {
	// where a destination-only and a port-only overlay meet, the newer one wins
	const n = 40
	w := scaleWorld(t, n, "mixed").world()
	tab, err := w.Table(sourceA, FamilyImpairment)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Entries) < n*n/10 {
		t.Fatalf("%d entries for %d overlays", len(tab.Entries), n)
	}
	for i := 0; i < n; i += 2 {
		for j := 1; j < n; j += 2 {
			q := Query{Source: subjectA, DestIP: netip.MustParseAddr(fmt.Sprintf("%d.%d.%d.9", 11, 0, i%256)), Protocol: "tcp", Port: 1000 + j}
			got, _ := tab.Lookup(q)
			want := Winner(w.Resolve(q), FamilyImpairment)
			if got == nil || want == nil || got.ID != want.ID {
				t.Fatalf("dest %d, port %d: lookup %v, resolve %v", i, j, got, want)
			}
		}
	}
}

func TestATableThatWouldNeedTooManyCellsIsRefusedQuickly(t *testing.T) {
	// 300 destination-only and 300 port-only overlays that all conflict need 90000 cells
	w := scaleWorld(t, 600, "mixed").world()
	start := time.Now()
	_, err := w.Table(sourceA, FamilyImpairment)
	took := time.Since(start)
	var big *TableTooLargeError
	if !errors.As(err, &big) {
		t.Fatalf("err = %v, want a *TableTooLargeError", err)
	}
	if len(big.Faults) == 0 || len(big.Faults) > 10 {
		t.Fatalf("faults named: %v", big.Faults)
	}
	if took > 60*time.Second {
		t.Fatalf("the refusal took %v", took)
	}
	// and it stays refused (the cached answer is the same)
	if _, err := w.Table(sourceA, FamilyImpairment); !errors.As(err, &big) {
		t.Fatalf("second call: %v", err)
	}
}

func TestSourcesTheSameCandidatesApplyToShareOneTable(t *testing.T) {
	w := scaleWorld(t, 30, "dp").world()
	first, err := w.Table(sourceA, FamilyImpairment)
	if err != nil {
		t.Fatal(err)
	}
	other := Source{Subject: Subject{IP: netip.MustParseAddr("10.10.0.77")}}
	second, err := w.Table(other, FamilyImpairment)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cells == 0 || second.Cells != 0 {
		t.Fatalf("cells: first %d, second %d (the second source must reuse the table)", first.Cells, second.Cells)
	}
	if first.Signature() != second.Signature() || second.Source.Subject.IP != other.Subject.IP {
		t.Fatalf("the shared table lost the source or changed its entries")
	}
}
