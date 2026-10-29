package capture

import (
	"encoding/json"
	"testing"

	"golang.org/x/net/bpf"
)

// FuzzFilter (M29, the review of the executor interface): the filter of a capture request comes over the executor's socket. Whatever
// JSON it is, it must neither panic the validation, the matcher nor the program builder; a filter that validates builds a program
// the kernel's verifier (here the VM of golang.org/x/net/bpf, which checks the same rules: forward jumps only, bounded length) accepts,
// and the program and the user-space matcher agree on a packet.
func FuzzFilter(f *testing.F) {
	for _, s := range []string{
		`{}`,
		`{"hosts":["10.10.0.5","10.10.0.0/24"],"protocol":"udp","ports":[{"from":53,"to":53}]}`,
		`{"macs":["02:00:00:00:00:01"],"peers":["203.0.113.0/24"],"not_peers":["203.0.113.9"]}`,
		`{"hosts":["::1","fe80::/10"],"protocol":"tcp","ports":[{"from":1,"to":65535}]}`,
		`{"protocol":"icmp","ports":[{"from":80,"to":80}]}`,
		`{"hosts":["10.0.0.1","10.0.0.1"]}`,
		`{"ports":[{"from":9,"to":1}]}`,
	} {
		f.Add([]byte(s), uint8(0), []byte{0x45, 0, 0, 20, 0, 0, 0, 0, 64, 17, 0, 0, 10, 10, 0, 5, 203, 0, 113, 9})
	}
	f.Fuzz(func(t *testing.T, raw []byte, link uint8, pkt []byte) {
		var filter Filter
		if json.Unmarshal(raw, &filter) != nil {
			return
		}
		lt := []LinkType{LinkEthernet, LinkRaw}[link%2]
		if filter.Validate(lt) != nil {
			return
		}
		m, err := filter.Matcher(lt)
		if err != nil {
			t.Fatalf("a filter that validates has no matcher: %v", err)
		}
		prog, err := m.BPF(65535)
		if err != nil {
			return // too large for the kernel is an answer, not a crash
		}
		vm, err := bpf.NewVM(prog)
		if err != nil {
			t.Fatalf("the program of a valid filter is refused: %v\n%s", err, raw)
		}
		n, err := vm.Run(pkt)
		if err != nil {
			t.Fatalf("the program failed on a packet: %v", err)
		}
		if got, want := n > 0, m.Match(pkt); got != want {
			t.Fatalf("the program selects %v, the matcher %v\nfilter %s\npacket %x", got, want, raw, pkt)
		}
	})
}
