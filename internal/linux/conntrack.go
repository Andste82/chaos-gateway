package linux

import (
	"bufio"
	"strconv"
	"strings"
	"time"
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
	// StartedAt is when the connection was first tracked (M6a-03); zero when `nf_conntrack_timestamp`
	// is off or the kernel has not reported it yet.
	StartedAt time.Time `json:"started_at,omitempty"`
}

// ConntrackEvent is one line of `conntrack -E -o id`: an entry changing state (M6a-04).
type ConntrackEvent struct {
	// Type is "new", "update" or "destroy" (the bracketed word at the start of the line, lowered).
	Type string `json:"type"`
	// ID is the kernel's conntrack entry id (the `-o id` extension), 0 if the line had none.
	ID uint32 `json:"id,omitempty"`
	Conntrack
}

// Tuple is one direction of a connection.
type Tuple struct {
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	SPort int    `json:"sport,omitempty"`
	DPort int    `json:"dport,omitempty"`
	// ICMPType, ICMPCode and ICMPID are set instead of SPort/DPort for icmp: concurrent pings share
	// no port, so the echo id is what tells their flows apart.
	ICMPType *int  `json:"icmp_type,omitempty"`
	ICMPCode *int  `json:"icmp_code,omitempty"`
	ICMPID   *int  `json:"icmp_id,omitempty"`
	Packets  int64 `json:"packets,omitempty"`
	Bytes    int64 `json:"bytes,omitempty"`
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
		c, ok := parseConntrackFields(f)
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// parseConntrackFields parses the fields of one `conntrack -L`/`-E` line after any leading
// `ipv4`/`ipv6` word and bracketed event type have been removed: the protocol, the protocol
// number, an optional timeout, an optional state word, then the two tuples and the trailing
// flags/fields. `-L` output always has the timeout; a `-E` DESTROY event omits it (the entry is
// already gone, so there is nothing left to count down) — the field is read only when present,
// rather than assumed at a fixed position.
func parseConntrackFields(f []string) (Conntrack, bool) {
	if len(f) < 3 {
		return Conntrack{}, false
	}
	c := Conntrack{Proto: f[0]}
	rest := f[2:] // f[1] is the protocol number
	if len(rest) > 0 {
		if to, err := strconv.Atoi(rest[0]); err == nil {
			c.TimeoutSeconds = to
			rest = rest[1:]
		}
	}
	if len(rest) > 0 && !strings.Contains(rest[0], "=") && !strings.HasPrefix(rest[0], "[") {
		c.State, rest = rest[0], rest[1:]
	}
	tuple := 0
	cur := &c.Original
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
		case "type":
			if n, err := strconv.Atoi(v); err == nil {
				cur.ICMPType = &n
			}
		case "code":
			if n, err := strconv.Atoi(v); err == nil {
				cur.ICMPCode = &n
			}
		case "id":
			if n, err := strconv.Atoi(v); err == nil {
				cur.ICMPID = &n
			}
		case "mark":
			m, _ := strconv.ParseUint(v, 10, 32)
			c.Mark = uint32(m)
		case "start":
			// M6a-03: `-o ktimestamp` appends the entry's start time as nanoseconds since the
			// epoch, once `nf_conntrack_timestamp` is on.
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				c.StartedAt = time.Unix(0, n).UTC()
			}
		}
	}
	if c.Original.Src == "" || c.Original.Dst == "" {
		return Conntrack{}, false
	}
	return c, true
}

// ParseConntrackEventLine parses one line of `conntrack -E -o id`: a bracketed event type
// (`[NEW]`, `[UPDATE]`, `[DESTROY]`), then the same fields as `-L`, plus a trailing `id=` that
// names the kernel's conntrack entry (not to be confused with an ICMP echo id inside a tuple).
func ParseConntrackEventLine(line string) (ConntrackEvent, bool) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return ConntrackEvent{}, false
	}
	var ev ConntrackEvent
	if strings.HasPrefix(f[0], "[") && strings.HasSuffix(f[0], "]") {
		ev.Type = strings.ToLower(strings.Trim(f[0], "[]"))
		f = f[1:]
	}
	if len(f) > 2 && (f[0] == "ipv4" || f[0] == "ipv6") {
		f = f[2:]
	}
	// the entry id (`-o id`) is the line's own trailing field, outside both tuples; strip it
	// before the generic parse so it is never mistaken for an ICMP echo id.
	if n := len(f); n > 0 {
		if k, v, found := strings.Cut(f[n-1], "="); found && k == "id" {
			if id, err := strconv.ParseUint(v, 10, 32); err == nil {
				ev.ID = uint32(id)
			}
			f = f[:n-1]
		}
	}
	c, ok := parseConntrackFields(f)
	if !ok {
		return ConntrackEvent{}, false
	}
	ev.Conntrack = c
	return ev, true
}
