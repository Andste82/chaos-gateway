package compiler

import (
	"fmt"
	"net/netip"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// The tc tree of plan §3.3, one per interface a classified packet can leave through: an HTB root
// with a default class and one class per active (fault id, direction), a netem leaf below each
// class, and one `fw` filter per class that selects it by the mark bits the classification chain
// writes (id in bits 4-15, direction in bit 16). Because the direction is part of the mark, every
// interface uses the same (id, direction) -> class mapping, so the tree is the same on all of them.
//
// The compiler produces the desired tree. Applying it (in place, make-before-break for a fault id
// that moves) is the fault engine's job (M8b).

// Direction of a packet in its connection: upload is the original direction of the initiator,
// download the reply (plan §2.4).
type Direction int

// The two directions; the value is the direction bit of the mark.
const (
	Upload   Direction = 0
	Download Direction = 1
)

func (d Direction) String() string {
	if d == Download {
		return "download"
	}
	return "upload"
}

const (
	// TCRootHandle is the handle of the HTB root qdisc ("1:").
	TCRootHandle = "1:"
	// TCDefaultMinor is the minor of the default class: traffic of no fault, or of a direction
	// the fault does not impair, goes through it unimpaired.
	TCDefaultMinor = 1
	// tcFirstMinor is the minor of the first fault class; the classes of fault id n are
	// tcFirstMinor + 2n (upload) and + 1 (download).
	tcFirstMinor = 0x10

	// TCClassRate is the rate of every HTB class: far above any link, so HTB only classifies and
	// never limits; rate limits are the netem leaf's `rate`. 10 Gbit/s fits the 64-bit rate field.
	TCClassRate = "10gbit"
	// TCClassQuantum is HTB's quantum for those classes. At the class rate the kernel's own
	// default is above its limit of 200000 bytes and tc warns ("quantum of class ... is big"); an
	// explicit value is silent and does not matter for classes that never compete.
	TCClassQuantum = "60000"

	// MarkMask are the mark bits a fw filter looks at: the fault id and the direction (plan §3.3).
	MarkMask = markIDMaskBits | markDirMaskBits // 0x1fff0
)

// DefaultClassLimitX86 and DefaultClassLimitARM64 are the default limits of classes per interface
// (plan §3.3); Input.ClassLimit overrides them (H1 adjusts them on real hardware).
const (
	DefaultClassLimitX86   = 1000
	DefaultClassLimitARM64 = 200
)

// TCClass is one active (fault id, direction): the class, its filter and its netem leaf.
type TCClass struct {
	ID  int       `json:"id"`
	Dir Direction `json:"dir"`
	// Minor is the minor number of the class, ClassID "1:<minor in hex>".
	Minor int `json:"minor"`
	// Mark is the value the fw filter matches under MarkMask.
	Mark  uint32 `json:"mark"`
	Netem Netem  `json:"netem"`
	// Endpoint is "ip:port" of the WireGuard peer whose encrypted UDP this class holds, set for the classes
	// of the IFB tree (tunnel faults, plan §2.2.1): a flower filter on the outer source address and port
	// selects the class, where the classes of the other trees are selected by the packet mark.
	Endpoint string `json:"endpoint,omitempty"`
	// FlapKey names the flapping this class belongs to (set when Netem.Flapping is): all classes of
	// one fault and direction share it, so a fault that gets one class per device flaps in step.
	FlapKey string `json:"flap_key,omitempty"`
	// Down says the class is in the down phase of its flapping now (Input.FlapPhase): the leaf holds
	// Netem.Down(), not Netem. Phase is the fault engine's, the compiler only writes it down.
	Down bool `json:"down,omitempty"`
}

// Config is the netem configuration the leaf holds now: the fault's, or its blackout in the down
// phase of a flapping.
func (c TCClass) Config() Netem {
	if c.Down && c.Netem.Flapping != nil {
		return c.Netem.Down()
	}
	return c.Netem
}

// ClassID is the class's id in tc notation.
func (c TCClass) ClassID() string { return fmt.Sprintf("1:%x", c.Minor) }

// LeafHandle is the handle of the netem qdisc below the class. It is the class's minor, so it is
// unique on the interface and never the root's "1:".
func (c TCClass) LeafHandle() string { return fmt.Sprintf("%x:", c.Minor) }

// FilterHandle is the fw filter's handle: the mark with its mask, as in plan §3.3
// (`0x000a0/0x1fff0` for upload of id 10, `0x100a0/0x1fff0` for its download).
func (c TCClass) FilterHandle() string { return fmt.Sprintf("0x%05x/0x%05x", c.Mark, MarkMask) }

// FilterEntry is the tc command that makes the filter selecting the class: the `fw` filter on the mark
// bits for the classes of the interfaces' trees, the flower filter on the peer's outer UDP for the
// classes of the IFB tree. The handle of a flower filter is the fault id.
func (c TCClass) FilterEntry(dev string) executor.TCEntry {
	if c.Endpoint != "" {
		ep, _ := netip.ParseAddrPort(c.Endpoint)
		return executor.TCEntry{Object: "filter", Action: "replace", Dev: dev, Parent: TCRootHandle, Handle: strconv.Itoa(c.ID),
			Args: []string{"protocol", "ip", "prio", strconv.Itoa(IFBFlowerPref), "flower", "ip_proto", "udp",
				"src_ip", ep.Addr().String(), "src_port", strconv.Itoa(int(ep.Port())), "flowid", c.ClassID()}}
	}
	return executor.TCEntry{Object: "filter", Action: "replace", Dev: dev, Parent: TCRootHandle, Handle: c.FilterHandle(),
		Args: []string{"protocol", "ip", "prio", "1", "fw", "flowid", c.ClassID()}}
}

// classMinor numbers the classes of a fault id: two minors per id, upload then download.
func classMinor(id int, dir Direction) int { return tcFirstMinor + 2*id + int(dir) }

// ClassIDOf is the class id in tc notation ("1:24") of a fault id and a direction: where the
// fault's packets of that direction queue.
func ClassIDOf(id int, dir Direction) string {
	return TCClass{ID: id, Dir: dir, Minor: classMinor(id, dir)}.ClassID()
}

// ClassIDToFault is the inverse of the class numbering: the fault id and direction of a class id in tc
// notation ("1:24"); false for the default class and for anything that is not a class of a fault.
func ClassIDToFault(classID string) (id int, dir Direction, ok bool) {
	maj, min, found := strings.Cut(classID, ":")
	if !found || maj != "1" {
		return 0, 0, false
	}
	m, err := strconv.ParseUint(min, 16, 32)
	if err != nil || m < tcFirstMinor+2 || m > tcFirstMinor+2*MarkIDMax+1 {
		return 0, 0, false
	}
	n := int(m) - tcFirstMinor
	return n / 2, Direction(n % 2), true
}

// MarkOf is the mark value of a fault id and direction (what the classification chain writes).
func MarkOf(id int, dir Direction) uint32 {
	return uint32(id)<<MarkIDShift | uint32(dir)<<MarkDirectionBit
}

// TCTarget is the tc tree every interface in Devs gets. A target without classes has no tree at
// all: nothing is impaired, and the root qdisc stays the interface's own.
type TCTarget struct {
	// Devs are the interfaces classified traffic leaves through: the bridges of the test
	// networks (traffic to a device), the WireGuard interfaces, the uplink (traffic through NAT to the
	// Internet) and the host side of the service namespace.
	Devs []string `json:"devs"`
	// Classes are sorted by id, then direction.
	Classes []TCClass `json:"classes"`
}

// HasDup reports whether some class duplicates packets: the interfaces then carry the duplication
// hook (Target.DupDevs, an nftables table of its own: dup.go).
func (tc *TCTarget) HasDup() bool {
	if tc == nil {
		return false
	}
	for _, c := range tc.Classes {
		if c.Netem.Duplicate > 0 {
			return true
		}
	}
	return false
}

// Entries returns the tc commands of the tree of one interface in the order they must run: the root,
// then each class with its leaf and filter. Everything but the root is `replace`, which creates
// what is missing and changes what exists in place (classes, netem leaves and fw filters all accept
// it, proven on kernel 6.8; replacing a netem leaf keeps its queue, plan §3.2).
//
// The root is `add`, and only when withRoot says the interface has no HTB root yet: the kernel
// refuses every change of an existing HTB qdisc ("Change operation not supported by specified
// qdisc"), and its two parameters (handle 1:, default class 1) are constants of the compiler, so
// there is nothing to change. A caller that finds the root in place leaves it alone.
func (tc *TCTarget) Entries(dev string, withRoot bool) []executor.TCEntry {
	if tc == nil || len(tc.Classes) == 0 {
		return nil
	}
	var es []executor.TCEntry
	if withRoot {
		es = append(es, executor.TCEntry{Object: "qdisc", Action: "add", Dev: dev, Parent: "root", Handle: TCRootHandle,
			Args: []string{"htb", "default", fmt.Sprintf("%x", TCDefaultMinor)}})
	}
	es = append(es, executor.TCEntry{Object: "class", Action: "replace", Dev: dev, Parent: TCRootHandle, ClassID: fmt.Sprintf("1:%x", TCDefaultMinor),
		Args: []string{"htb", "rate", TCClassRate, "quantum", TCClassQuantum}})
	for _, c := range tc.Classes {
		es = append(es,
			executor.TCEntry{Object: "class", Action: "replace", Dev: dev, Parent: TCRootHandle, ClassID: c.ClassID(),
				Args: []string{"htb", "rate", TCClassRate, "quantum", TCClassQuantum}},
			executor.TCEntry{Object: "qdisc", Action: "replace", Dev: dev, Parent: c.ClassID(), Handle: c.LeafHandle(),
				Args: c.Config().Args()},
			c.FilterEntry(dev),
		)
	}
	return es
}

// Lines renders Entries (with the root) as `tc` command lines (without the leading "tc"), for
// golden files.
func (tc *TCTarget) Lines(dev string) []string {
	var out []string
	for _, e := range tc.Entries(dev, true) {
		l := []string{e.Object, e.Action, "dev", e.Dev}
		switch e.Parent {
		case "":
		case "root", "ingress", "clsact":
			l = append(l, e.Parent)
		default:
			l = append(l, "parent", e.Parent)
		}
		if e.Handle != "" {
			l = append(l, "handle", e.Handle)
		}
		if e.ClassID != "" {
			l = append(l, "classid", e.ClassID)
		}
		out = append(out, strings.Join(append(l, e.Args...), " "))
	}
	return out
}

// ClassesPerDevice is the number of classes every interface carries: the fault classes and the
// default one.
func (tc *TCTarget) ClassesPerDevice() int {
	if tc == nil || len(tc.Classes) == 0 {
		return 0
	}
	return len(tc.Classes) + 1
}

// TCCandidates lists the interfaces the tc tree is (or, with no active fault, would be) installed on:
// the apply reads them, to put the tree there or to take a tree that is no longer wanted away. The IFB
// device of the tunnel faults is always among them: its tree is its own (tunnel.go).
func (t *Target) TCCandidates() []string {
	return append(t.tcDevs(), IFBDev)
}

// TCTrees returns the trees the target wants: the one the interfaces carry and the one of the IFB
// device, as far as they have classes.
func (t *Target) TCTrees() []*TCTarget {
	var out []*TCTarget
	if t.TC != nil && len(t.TC.Classes) > 0 {
		out = append(out, t.TC)
	}
	if t.IFB != nil && t.IFB.TC != nil && len(t.IFB.TC.Classes) > 0 {
		out = append(out, t.IFB.TC)
	}
	return out
}

// TreeOf returns the tree the target wants on the interface, nil when it wants none there.
func (t *Target) TreeOf(dev string) *TCTarget {
	for _, tr := range t.TCTrees() {
		for _, d := range tr.Devs {
			if d == dev {
				return tr
			}
		}
	}
	return nil
}

// tcDevs lists the interfaces a classified packet can leave through.
func (t *Target) tcDevs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, b := range t.Bridges {
		add(b.Name)
	}
	for _, w := range t.WireGuard {
		add(w.Name)
	}
	add(t.Uplink.Name)
	if t.Service != nil {
		add(t.Service.HostIf)
	}
	sort.Strings(out)
	return out
}

// DefaultClassLimit is the class limit of an interface when the input names none: 1000 on x86 and
// the conservative 200 on ARM64 (plan §3.3), until the hardware measurements (H1) adjust them.
func DefaultClassLimit() int {
	if runtime.GOARCH == "arm64" {
		return DefaultClassLimitARM64
	}
	return DefaultClassLimitX86
}
