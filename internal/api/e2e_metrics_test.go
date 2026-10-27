//go:build testbed

package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M26 on a real kernel: the values of the metrics endpoint against traffic whose size the test knows. Ten echo requests of
// client A to the server, through a fault of a network, are classified into the fault (the requests and their replies, so
// between ten and twenty packets), pass its netem queue (no more than the packets of the faults' directions), cross the
// bridge of the network (at least ten packets received and ten sent more than before the pings) and leave the uplink; the
// numbers are the ones the REST API shows for the same counters.
func TestMetricsCountKnownTrafficOnTheRealKernel(t *testing.T) {
	g, top := newBedGW(t)
	r := g.createOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"2ms","destination":{"cidr":"` + testbed.ServerAddr + `/32"}}}`)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	var bridge, uplink string
	for _, b := range g.e.Snapshot().Bridges {
		if b.NetworkName == "IoT" {
			bridge = b.Name
		}
	}
	if a := g.e.Snapshot().Applied; a != nil {
		uplink = a.Uplink.Name
	}
	if bridge == "" || uplink == "" {
		t.Fatalf("bridge %q uplink %q", bridge, uplink)
	}
	// the interface counters are the kernel's, so what the bed did before is part of them: the test takes the difference
	before := g.scrape()
	rx0 := before.val("chaosgw_interface_receive_packets_total", "interface", bridge)
	tx0 := before.val("chaosgw_interface_transmit_packets_total", "interface", bridge)
	up0 := before.val("chaosgw_interface_transmit_packets_total", "interface", uplink)
	if p := before.val("chaosgw_fault_packets_total", "layer", "overlay", "id", id); p != 0 {
		t.Fatalf("the fault counted %v packets before any traffic", p)
	}

	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 10, 100*time.Millisecond); res.Loss() != 0 {
		t.Fatalf("A cannot reach the server through the fault")
	}
	var m *scrape
	waitFor(t, 20*time.Second, "the fault has counted the pings", func() bool {
		m = g.scrape()
		p, _ := m.find("chaosgw_fault_packets_total", "layer", "overlay", "id", id)
		return p >= 10
	})

	// the fault: the same number as the overlay's view, between the ten requests and the twenty packets of both directions
	p := m.val("chaosgw_fault_packets_total", "layer", "overlay", "id", id)
	oc := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)["counters"].(map[string]any)
	if p < 10 || p > 20 || p > oc["packets"].(float64) || m.val("chaosgw_fault_bytes_total", "layer", "overlay", "id", id) < 10*84 {
		t.Errorf("the fault counted %v packets (the API: %v), want 10 to 20", p, oc)
	}
	// the netem queues of the fault: the requests passed upload, nothing dropped
	var sent, dropped float64
	for _, s := range m.samples {
		if s.name == "chaosgw_queue_sent_packets_total" && s.labels["source"] == id && s.labels["direction"] == "upload" {
			sent += s.value
		}
		if s.name == "chaosgw_queue_dropped_packets_total" && s.labels["source"] == id {
			dropped += s.value
		}
	}
	if sent < 10 || dropped != 0 {
		t.Errorf("the queues of the fault sent %v packets upload and dropped %v", sent, dropped)
	}
	// the interfaces: the bridge received the requests and sent the replies, the uplink sent the requests on
	if d := m.val("chaosgw_interface_receive_packets_total", "interface", bridge) - rx0; d < 10 {
		t.Errorf("%s received %v packets more", bridge, d)
	}
	if d := m.val("chaosgw_interface_transmit_packets_total", "interface", bridge) - tx0; d < 10 {
		t.Errorf("%s sent %v packets more", bridge, d)
	}
	if d := m.val("chaosgw_interface_transmit_packets_total", "interface", uplink) - up0; d < 10 {
		t.Errorf("%s sent %v packets more", uplink, d)
	}
	if m.val("chaosgw_interface_up", "interface", bridge) != 1 || m.val("chaosgw_collector_up", "source", "interfaces") != 1 || m.val("chaosgw_collector_up", "source", "queues") != 1 {
		t.Error("the bridge is up and the sources are read")
	}
	// the load and the memory are those of the machine
	if m.val("chaosgw_system_memory_total_bytes") <= 0 || m.val("chaosgw_system_cpus") < 1 {
		t.Error("the system's numbers")
	}
	// a counter only grows: another round of pings raises it
	testbed.MustPing(t, top.A, testbed.ServerAddr, 5, 100*time.Millisecond)
	waitFor(t, 20*time.Second, "the fault has counted the second round", func() bool {
		return strings.Contains(string(g.do("GET", "/metrics", nil, nil, nil).Body), "chaosgw_fault_packets_total") && g.scrape().val("chaosgw_fault_packets_total", "layer", "overlay", "id", id) >= p+5
	})
}
