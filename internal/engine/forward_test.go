package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// M35a in the engine: a forward to a device follows the device's address by an element update of the DNAT
// map, like the identity map (no full apply), and the snapshot carries the compiled forwards for their
// status and counters.

const fwdID = "f2000000-0000-4000-8000-000000000001"

func withForwardToDevice(c *model.Configuration) {
	withDHCPAndDevice(c)
	tp := 80
	c.PortForwards = &map[string]model.PortForward{fwdID: {Name: ptr("web"), Protocol: model.PortForwardProtocolTcp,
		Entry: model.PortForwardEntryUplink, ExternalPort: 8080, TargetPort: &tp, Target: model.PortForwardTarget{Device: ptr(devID)}}}
}

// forwardElements reads the elements of the DNAT map from the kernel, as `key : value` lines.
func (h *harness) forwardElements() string {
	h.t.Helper()
	name := compiler.ForwardMapName()
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, o := range st.Nft.Objects {
		if o.Map != nil && o.Map.Name == name {
			var out []string
			for k, v := range o.Map.Pairs() {
				out = append(out, k+" : "+v)
			}
			return strings.Join(out, ",")
		}
	}
	h.t.Fatalf("no DNAT map %s in the kernel", name)
	return ""
}

func TestADeviceForwardFollowsTheAddressByAnElementUpdate(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withForwardToDevice))
	// no address yet: the forward is in the kernel without an element, inactive
	if got := h.forwardElements(); got != "" {
		t.Fatalf("elements before the device has an address: %q", got)
	}
	fs := h.e.Snapshot().Forwards
	if len(fs) != 1 || fs[0].ID != fwdID || fs[0].Active || fs[0].Counter != compiler.ForwardCounterName(fwdID) {
		t.Fatalf("the snapshot's forwards: %+v", fs)
	}
	iface := fs[0].Iface

	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.barrier()
	if got, want := h.forwardElements(), iface+" . tcp . 8080 : 10.10.0.31 . 80"; got != want {
		t.Fatalf("the DNAT map holds %q, want %q", got, want)
	}
	epoch := h.e.Snapshot().ForwardEpochs[fwdID]

	before := len(h.k.Commands())
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.77", macCfg)})
	h.observe()
	h.barrier()
	// a full apply reads and verifies the whole state; an element update only the nft table
	ran := h.k.Commands()[before:]
	for _, c := range ran {
		if strings.HasPrefix(c, "ip -batch") || strings.HasPrefix(c, "ip -j") || strings.HasPrefix(c, "tc ") {
			t.Errorf("the address change took more than an element update: %s\n%s", c, strings.Join(ran, "\n"))
		}
	}
	if got, want := h.forwardElements(), iface+" . tcp . 8080 : 10.10.0.77 . 80"; got != want {
		t.Fatalf("the DNAT map holds %q, want %q", got, want)
	}
	s := h.e.Snapshot()
	if f := s.Forwards[0]; !f.Active || f.Target.String() != "10.10.0.77" {
		t.Errorf("the snapshot's forward: %+v", f)
	}
	if s.ForwardEpochs[fwdID] != epoch {
		t.Errorf("an address change restarted the forward's counter: epoch %d, was %d", s.ForwardEpochs[fwdID], epoch)
	}
}

// A forward that is disabled leaves the kernel: no element, no rule, no counter, and the snapshot forgets
// it.
func TestADisabledForwardLeavesTheKernel(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withForwardToDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.barrier()
	h.mustApply(h.revision(func(c *model.Configuration) {
		withForwardToDevice(c)
		f := (*c.PortForwards)[fwdID]
		f.Enabled = ptr(false)
		(*c.PortForwards)[fwdID] = f
	}))
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Nft.Set(compiler.ForwardMapName()) != nil {
		t.Error("the DNAT map is still there")
	}
	for _, c := range st.Nft.Counters() {
		if c == compiler.ForwardCounterName(fwdID) {
			t.Error("the forward's counter is still there")
		}
	}
	if s := h.e.Snapshot(); len(s.Forwards) != 0 || len(s.ForwardEpochs) != 0 {
		t.Errorf("the snapshot keeps the forward: %+v", s.Forwards)
	}
}

// A full apply (here: a fault overlay on the device) keeps the elements of the DNAT map and of the identity
// map: the apply changes the maps by their difference with what the kernel lists.
func TestAFullApplyKeepsTheForwardsElements(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withForwardToDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.barrier()
	want := h.forwardElements()
	if want == "" {
		t.Fatal("no element")
	}
	if _, err := h.put(model.Owner{Type: "user", Id: "test"}, "target: {device: "+devID+"}\nfault: {latency: 400ms}"); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	if got := h.forwardElements(); got != want {
		t.Errorf("the DNAT map after a full apply: %q, want %q", got, want)
	}
	if got := h.deviceSetElements(devID); got != "10.10.0.31" {
		t.Errorf("the identity map after a full apply: %q", got)
	}
}
