//go:build testbed

package executor_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// M20 on the real kernel: the executor's persistent netlink connection (google/nftables) writes the elements of a DNS-derived
// set in the gateway's network namespace, with their lifetimes; a refresh gives an element that exists a new lifetime (the kernel keeps the
// lifetime of an element it has: the writer deletes and adds in one transaction); a set that does not exist is an error; the
// connection is the executor's own and works beside the writer's plans (the requests go over a connection of the
// client that is not the plan's).

const dnsSet = "dns_0123456789_abcdef"

func TestTheNetlinkConnectionWritesAndRefreshesDNSElementsInTheGatewayNamespace(t *testing.T) {
	g := startGateway(t)
	g.top.GW.MustStdin("table inet chaosgw {\n set "+dnsSet+" {\n type ipv4_addr . ipv4_addr\n flags timeout\n }\n}\n", "nft", "-f", "-")
	listing := func() string { return g.top.GW.Must("nft", "list", "set", "inet", "chaosgw", dnsSet) }
	el := func(client, addr string, secs int, replace bool) *executor.NftDNSElements {
		return &executor.NftDNSElements{Target: tgt(g.ns), Updates: []executor.DNSSetUpdate{{Set: dnsSet,
			Elements: []executor.DNSElement{{Client: client, Addr: addr, TimeoutSeconds: secs, Replace: replace}}}}}
	}

	// two elements in one request, one with the lifetime of ten minutes
	g.must(&executor.NftDNSElements{Target: tgt(g.ns), Updates: []executor.DNSSetUpdate{{Set: dnsSet, Elements: []executor.DNSElement{
		{Client: "10.10.0.11", Addr: "203.0.113.10", TimeoutSeconds: 600},
		{Client: "10.10.0.12", Addr: "203.0.113.20", TimeoutSeconds: 4},
	}}}})
	out := listing()
	for _, want := range []string{"10.10.0.11 . 203.0.113.10 timeout 10m", "10.10.0.12 . 203.0.113.20 timeout 4s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the set does not hold %q:\n%s", want, out)
		}
	}

	// the element with four seconds is refreshed after two: it is still there when its first lifetime has run out
	time.Sleep(2 * time.Second)
	g.must(el("10.10.0.12", "203.0.113.20", 30, true))
	time.Sleep(3 * time.Second)
	if out := listing(); !strings.Contains(out, "10.10.0.12 . 203.0.113.20 timeout 30s") {
		t.Fatalf("the refreshed element is gone or has the old lifetime:\n%s", out)
	}

	// an element that is not there and is asked to be refreshed is added (the delete finds nothing, the adds go on)
	g.must(el("10.10.0.13", "203.0.113.30", 60, true))
	if out := listing(); !strings.Contains(out, "10.10.0.13 . 203.0.113.30 timeout 1m") {
		t.Fatalf("a refresh of a missing element did not add it:\n%s", out)
	}

	// the same element again, not asked to be refreshed: nothing changes, and no error
	g.must(el("10.10.0.11", "203.0.113.10", 900, false))

	// an element is taken out (P5-AUDIT-01), and one that is not there beside it does not stop it or fail the request
	g.must(&executor.NftDNSElements{Target: tgt(g.ns), Updates: []executor.DNSSetUpdate{{Set: dnsSet, Elements: []executor.DNSElement{
		{Client: "10.10.0.99", Addr: "203.0.113.99", Delete: true},
		{Client: "10.10.0.13", Addr: "203.0.113.30", Delete: true},
	}}}})
	if out := listing(); strings.Contains(out, "203.0.113.30") || !strings.Contains(out, "10.10.0.11 . 203.0.113.10") {
		t.Fatalf("the element was not taken out, or another went with it:\n%s", out)
	}

	// a set that is not there is an error of the request
	if _, err := g.c.Do(tctx(t), &executor.NftDNSElements{Target: tgt(g.ns), Updates: []executor.DNSSetUpdate{{
		Set: "dns_ffffffffff_ffffff", Elements: []executor.DNSElement{{Client: "10.10.0.11", Addr: "203.0.113.10", TimeoutSeconds: 60}}}}}); err == nil {
		t.Error("an element was written into a set that does not exist")
	}
	// and the connection that failed still works, also after the set was replaced by a plan: deleted and made again
	g.top.GW.MustStdin("delete set inet chaosgw "+dnsSet+"\nadd set inet chaosgw "+dnsSet+" { type ipv4_addr . ipv4_addr; flags timeout; }\n", "nft", "-f", "-")
	g.must(el("10.10.0.11", "203.0.113.10", 60, false))
	if out := listing(); !strings.Contains(out, "10.10.0.11 . 203.0.113.10 timeout 1m") || strings.Contains(out, "203.0.113.30") {
		t.Fatalf("the writer does not find the set that was made again:\n%s", out)
	}
}

// The update beside a plan on the real kernel: a plan that takes a while (a large transaction) does not hold the update.
func TestADNSElementIsWrittenWhileTheExecutorAppliesALargeTransactionOnTheRealKernel(t *testing.T) {
	g := startGateway(t)
	g.top.GW.MustStdin("table inet chaosgw {\n set "+dnsSet+" {\n type ipv4_addr . ipv4_addr\n flags timeout\n }\n}\n", "nft", "-f", "-")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 30; i++ {
			g.top.GW.MustStdin("add table inet chaosgw_x"+string(rune('a'+i%26))+"\n", "nft", "-f", "-")
		}
	}()
	for i := 0; i < 20; i++ {
		g.must(&executor.NftDNSElements{Target: tgt(g.ns), Updates: []executor.DNSSetUpdate{{Set: dnsSet, Elements: []executor.DNSElement{
			{Client: "10.10.0.11", Addr: fmt.Sprintf("203.0.113.%d", 1+i), TimeoutSeconds: 60}}}}})
	}
	<-done
	out := g.top.GW.Must("nft", "list", "set", "inet", "chaosgw", dnsSet)
	if n := strings.Count(out, "10.10.0.11 . "); n != 20 {
		t.Errorf("%d elements:\n%s", n, out)
	}
}
