package testbed

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// End is one end of a veth pair.
type End struct {
	NS *Namespace
	// If is the interface name inside NS.
	If string
	// MAC is optional; testbed MACs are fixed so tests can name devices by MAC.
	MAC string
	// Addr is an optional address in CIDR notation.
	Addr string
	// Master is an optional bridge of NS that the interface becomes a port of.
	Master string
}

// Link joins two namespaces with a veth pair, switches offloads off so tc and netem see real
// packets (spike S1), and brings both ends up.
func (b *Bed) Link(x, y End) {
	b.t.Helper()
	args := []string{"link", "add", x.If, "netns", x.NS.Name}
	if x.MAC != "" {
		args = append(args, "address", x.MAC)
	}
	args = append(args, "type", "veth", "peer", "name", y.If, "netns", y.NS.Name)
	if y.MAC != "" {
		args = append(args, "address", y.MAC)
	}
	if out, err := runHost("ip", args...); err != nil {
		b.t.Fatalf("testbed: ip %s: %v: %s", strings.Join(args, " "), err, out)
	}
	for _, e := range []End{x, y} {
		if e.Master != "" {
			e.NS.Must("ip", "link", "set", e.If, "master", e.Master)
		}
		if e.Addr != "" {
			e.NS.Must("ip", "addr", "add", e.Addr, "dev", e.If)
		}
		e.NS.Must("ip", "link", "set", e.If, "up")
		// offloads are best effort: a virtual device may not support every feature
		_, _ = e.NS.Run(ctxBackground(), "ethtool", "-K", e.If, "tso", "off", "gso", "off", "gro", "off", "tx", "off", "rx", "off")
	}
	b.mu.Lock()
	b.ends = append(b.ends, x, y)
	b.mu.Unlock()
}

// DiagNoLinkWait is TEMPORARY (P2-M8b-08 diagnostic): NewDefault does not wait for the links.
var DiagNoLinkWait bool

// WaitLinks waits until every veth end that Link created is operationally up and every one that is
// a port of a bridge forwards. NewDefault calls it; a test that builds its own topology with Link
// can call it before it sends.
//
// "Up" is not enough (P2-M8b-08): a bridge enables a port only once the port's operational state is
// up, and until then it drops every frame the port brings and sends nothing out of it. The kernel
// derives the operational state from the carrier in its link-watch work, which handles the events it
// does not count as urgent at most once a second. A veth's carrier event is urgent when its queueing
// discipline waits for the carrier to be activated (the end that is set up first, before its peer) or when its
// peer has another interface index than itself; but indexes are counted per namespace, so the end
// that is set up second and happens to have its peer's index waits up to a second. In the default
// topology that is q1, Switch1's port to the gateway's lan1 (both the third interface of their
// namespace): the Lab network was cut off for up to a second after the topology was built, the first
// ARP request of device C was lost, and C's first packets waited for its retry a second later.
func (b *Bed) WaitLinks() {
	b.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		pending := b.pendingLinks()
		if len(pending) == 0 {
			return
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("testbed: links not ready after a minute: %s", strings.Join(pending, "; "))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pendingLinks lists the ends of the bed that are not ready yet, each namespace read once.
func (b *Bed) pendingLinks() []string {
	b.mu.Lock()
	byNS := map[*Namespace][]End{}
	for _, e := range b.ends {
		byNS[e.NS] = append(byNS[e.NS], e)
	}
	b.mu.Unlock()
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		pending []string
	)
	for ns, ends := range byNS {
		wg.Add(1)
		go func() {
			defer wg.Done()
			not := ns.notReady(ends)
			mu.Lock()
			for _, why := range not {
				pending = append(pending, ns.Short+": "+why)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Strings(pending)
	return pending
}

// notReady reads the links of the namespace and says what of the ends is not ready.
func (n *Namespace) notReady(ends []End) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := n.Run(ctx, "ip", "-d", "-j", "link", "show")
	if err != nil {
		return []string{err.Error()}
	}
	var links []struct {
		Name      string `json:"ifname"`
		OperState string `json:"operstate"`
		Info      *struct {
			SlaveKind string `json:"info_slave_kind"`
			SlaveData *struct {
				State string `json:"state"`
			} `json:"info_slave_data"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal([]byte(out), &links); err != nil {
		return []string{fmt.Sprintf("ip -d -j link show: %v", err)}
	}
	var not []string
	for _, e := range ends {
		why := e.If + " is missing"
		for _, l := range links {
			if l.Name != e.If {
				continue
			}
			why = ""
			state := ""
			if l.Info != nil && l.Info.SlaveKind == "bridge" && l.Info.SlaveData != nil {
				state = l.Info.SlaveData.State
			}
			switch {
			case l.OperState != "UP":
				why = fmt.Sprintf("%s is %s", e.If, l.OperState)
			case e.Master != "" && state != "forwarding":
				why = fmt.Sprintf("%s is a %q port of %s", e.If, state, e.Master)
			}
			break
		}
		if why != "" {
			not = append(not, why)
		}
	}
	return not
}

// Bridge adds a bridge to the namespace and brings it up; addr is an optional address on it.
func (n *Namespace) Bridge(name, addr string) {
	n.bed.t.Helper()
	n.Must("ip", "link", "add", name, "type", "bridge")
	if addr != "" {
		n.Must("ip", "addr", "add", addr, "dev", name)
	}
	n.Must("ip", "link", "set", name, "up")
}

// Addr adds an address in CIDR notation to an interface.
func (n *Namespace) Addr(dev, cidr string) {
	n.bed.t.Helper()
	n.Must("ip", "addr", "add", cidr, "dev", dev)
}

// Route adds a route, e.g. Route("default", "via", "10.10.0.1").
func (n *Namespace) Route(args ...string) {
	n.bed.t.Helper()
	n.Must("ip", append([]string{"route", "add"}, args...)...)
}

// MAC returns the hardware address of an interface.
func (n *Namespace) MAC(dev string) string {
	n.bed.t.Helper()
	return n.Must("cat", fmt.Sprintf("/sys/class/net/%s/address", dev))
}
