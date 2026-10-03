package linux

import (
	"bufio"
	"strconv"
	"strings"
)

// Conntrack is one connection of `conntrack -L`: the original direction, the reply, the state and,
// when the counters are on, the traffic of both directions.
type Conntrack struct {
	Proto string `json:"proto"` // tcp, udp, icmp, ...
	// TimeoutSeconds is what is left of the entry's lifetime.
	TimeoutSeconds int    `json:"timeout"`
	State          string `json:"state,omitempty"` // TCP: ESTABLISHED, TIME_WAIT, ...
	// Original and Reply are the two tuples; the reply of a NATed connection shows the translated addresses.
	Original Tuple `json:"original"`
	Reply    Tuple `json:"reply"`
	// Flags are the bracketed words: ASSURED, UNREPLIED, ...
	Flags []string `json:"flags,omitempty"`
	Mark  uint32   `json:"mark,omitempty"`
}

// Tuple is one direction of a connection.
type Tuple struct {
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	SPort   int    `json:"sport,omitempty"`
	DPort   int    `json:"dport,omitempty"`
	Packets int64  `json:"packets,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
}

// ParseConntrack parses `conntrack -L` text: lines like
//
//	tcp 6 431999 ESTABLISHED src=10.10.0.10 dst=203.0.113.10 sport=45566 dport=80 packets=6 bytes=412 src=203.0.113.10 dst=10.10.0.10 sport=80 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1
//
// A leading `ipv4 2` (the extended format) is skipped; lines it does not understand are skipped.
func ParseConntrack(text string) []Conntrack {
	var out []Conntrack
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > 2 && (f[0] == "ipv4" || f[0] == "ipv6") {
			f = f[2:]
		}
		if len(f) < 4 {
			continue
		}
		c := Conntrack{Proto: f[0]}
		// f[1] is the protocol number, f[2] the timeout; a TCP line then has the state
		to, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		c.TimeoutSeconds = to
		rest := f[3:]
		if len(rest) > 0 && !strings.Contains(rest[0], "=") && !strings.HasPrefix(rest[0], "[") {
			c.State, rest = rest[0], rest[1:]
		}
		tuple := 0
		cur := &c.Original
		ok := true
		for _, w := range rest {
			if strings.HasPrefix(w, "[") && strings.HasSuffix(w, "]") {
				c.Flags = append(c.Flags, strings.Trim(w, "[]"))
				continue
			}
			k, v, found := strings.Cut(w, "=")
			if !found {
				continue
			}
			switch k {
			case "src":
				if tuple == 1 || cur.Src != "" {
					// the second src starts the reply tuple
					if cur == &c.Original {
						cur = &c.Reply
						tuple = 1
					}
				}
				cur.Src = v
			case "dst":
				cur.Dst = v
			case "sport":
				cur.SPort, _ = strconv.Atoi(v)
			case "dport":
				cur.DPort, _ = strconv.Atoi(v)
			case "packets":
				cur.Packets, _ = strconv.ParseInt(v, 10, 64)
			case "bytes":
				cur.Bytes, _ = strconv.ParseInt(v, 10, 64)
			case "mark":
				m, _ := strconv.ParseUint(v, 10, 32)
				c.Mark = uint32(m)
			}
		}
		if c.Original.Src == "" || c.Original.Dst == "" {
			ok = false
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}
