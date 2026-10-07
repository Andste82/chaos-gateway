package compiler

import (
	"fmt"
	"runtime"
	"sort"
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
}

// ClassID is the class's id in tc notation.
func (c TCClass) ClassID() string { return fmt.Sprintf("1:%x", c.Minor) }

// LeafHandle is the handle of the netem qdisc below the class. It is the class's minor, so it is
// unique on the interface and never the root's "1:".
func (c TCClass) LeafHandle() string { return fmt.Sprintf("%x:", c.Minor) }

// FilterHandle is the fw filter's handle: the mark with its mask, as in plan §3.3
// (`0x000a0/0x1fff0` for upload of id 10, `0x100a0/0x1fff0` for its download).
func (c TCClass) FilterHandle() string { return fmt.Sprintf("0x%05x/0x%05x", c.Mark, MarkMask) }

// classMinor numbers the classes of a fault id: two minors per id, upload then download.
func classMinor(id int, dir Direction) int { return tcFirstMinor + 2*id + int(dir) }

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
				Args: c.Netem.Args()},
			executor.TCEntry{Object: "filter", Action: "replace", Dev: dev, Parent: TCRootHandle, Handle: c.FilterHandle(),
				Args: []string{"protocol", "ip", "prio", "1", "fw", "flowid", c.ClassID()}},
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
