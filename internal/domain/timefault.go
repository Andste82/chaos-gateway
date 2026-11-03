package domain

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// This file is the time family as the compiler and the NTP server need it (plan §2.4, §2.18, M34): the offset
// arithmetic, the rules of a time fault, where one can be aimed, and which faults apply to a source of traffic.

// NTPPort is the UDP port a time fault applies to when it names none.
const NTPPort = 123

// MaxTimeSkewPPM is the largest skew a time fault may have in either direction: 1,000,000 ppm is a clock that
// stands still (or, the other way, runs at twice the rate), which is as far as a drifting clock can still be
// told from a broken one.
const MaxTimeSkewPPM = 1_000_000

// MaxTimeOffsetLen is the longest text a TimeOffset may have. The longest valid offset (a few numbers with units that
// still fit about 292 years) is far shorter, and the exact arithmetic of ParseTimeOffset costs seconds on a megabyte of
// digits, which any caller with the overlays scope could send.
const MaxTimeOffsetLen = 64

// timeReservedPorts are the UDP ports of the services the gateway runs for the devices: DNS and DHCP. A time fault
// that selected one of them would redirect that service's traffic into the NTP server, which answers nothing but NTP,
// and take the device's name resolution or its lease away; that is a DNS or DHCP fault, not a time fault.
var timeReservedPorts = []int{53, 67, 68}

// ntpEpoch is the beginning of NTP era 0 (1900-01-01 00:00:00 UTC). An absolute time before it has no
// representation in a timestamp of the protocol.
var ntpEpoch = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

// ---- the offset ---------------------------------------------------------------------------

// timeOffsetRE is the form of a TimeOffset: one sign for the whole span and one or more numbers with a unit.
var timeOffsetRE = regexp.MustCompile(`^([+-]?)((?:[0-9]+(?:\.[0-9]+)?(?:ms|s|m|h|d|w|y))+)$`)

var timeOffsetPart = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)(ms|s|m|h|d|w|y)`)

// timeOffsetUnits are the units of an offset in nanoseconds. A day is 24 hours and a year 365 days: a calendar
// year has no fixed length, and a test that wants a leap year says 366d.
var timeOffsetUnits = map[string]int64{
	"ms": int64(time.Millisecond),
	"s":  int64(time.Second),
	"m":  int64(time.Minute),
	"h":  int64(time.Hour),
	"d":  24 * int64(time.Hour),
	"w":  7 * 24 * int64(time.Hour),
	"y":  365 * 24 * int64(time.Hour),
}

// ParseTimeOffset reads the span of a TimeOffset (`+400d`, `-90s`, `1y2d`, `-1.5h`): an optional sign, which
// applies to the whole span, and one or more numbers with a unit. A missing sign is a plus. The span is exact to the
// nanosecond and must fit a time.Duration without its most negative value, about 292 years either way, so that it can be negated; a time further away than that is an
// `absolute` time.
func ParseTimeOffset(s string) (time.Duration, error) {
	if len(s) > MaxTimeOffsetLen {
		return 0, fmt.Errorf("a time offset has at most %d characters", MaxTimeOffsetLen)
	}
	m := timeOffsetRE.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a time offset: a sign and numbers with the units ms, s, m, h, d, w or y, such as +400d or -90s", s)
	}
	total := new(big.Rat)
	for _, p := range timeOffsetPart.FindAllStringSubmatch(m[2], -1) {
		n, ok := new(big.Rat).SetString(p[1])
		if !ok {
			return 0, fmt.Errorf("%q is not a time offset", s)
		}
		total.Add(total, n.Mul(n, new(big.Rat).SetInt64(timeOffsetUnits[p[2]])))
	}
	if m[1] == "-" {
		total.Neg(total)
	}
	// a fraction of a nanosecond is cut off (big.Int.Quo truncates towards zero)
	ns := new(big.Int).Quo(total.Num(), total.Denom())
	// the range is symmetric: math.MinInt64 is a Duration that cannot be negated (-d == d), and a server that undoes or
	// mirrors an offset must not meet one
	if !ns.IsInt64() || ns.Int64() == math.MinInt64 {
		return 0, fmt.Errorf("%q is further than about 292 years away: use an absolute time", s)
	}
	return time.Duration(ns.Int64()), nil
}

// ---- validation ---------------------------------------------------------------------------

// timeTargetKinds are the scopes a time fault can be aimed at: the server tells the client's scope from the client's
// address, a device, a group of devices or a network. The global scope has no address to tell it by, and a remote
// network is no group of clients of the NTP server.
var timeTargetKinds = []string{"device", "group", "network"}

// timeTarget checks where a time fault can be aimed.
func (v *validator) timeTarget(path string, target *model.Scope) {
	k := scopeKind(target)
	if k == "" || timeScopeAllowed(*target) {
		return // the schema reports a scope without exactly one property
	}
	v.add(path, CodeTargetKind, "a time fault targets a device, a group or a network, not a %s", k)
}

// profileTimeTarget checks the target of the activation of a profile that has a time part: the part competes on the
// target like a time fault of its own, so the target is one a time fault can have. A profile without a time part, or
// that does not exist (reported elsewhere), has no restriction.
func (v *validator) profileTimeTarget(path string, target *model.Scope, profile string) {
	if p, _, ok := ProfileByID(v.cfg, profile); ok && p.Parts.Time != nil {
		v.timeTarget(path, target)
	}
}

// timeFault checks the fields of a time fault (plan §2.18).
func (v *validator) timeFault(path string, t model.TimeFault) {
	add := func(field, format string, args ...any) { v.add(path+field, CodeInvalidTimeFault, format, args...) }

	// the destination selector: the server asked, an address or a prefix or a name; the protocol is UDP
	match := timeMatch(t)
	v.match(path, match)
	if p := protocolOf(convert[model.TrafficMatch](t)); p != "udp" && p != "any" {
		add("/protocol", "NTP runs over UDP: the protocol must be udp or omitted")
	}
	for _, port := range timeReservedPorts {
		if slices.Contains(deref(match.Ports), port) {
			add("/ports", "a time fault cannot select port %d: the gateway's own DNS and DHCP services use it, and their traffic would end in the NTP server", port)
		}
		for _, r := range deref(match.PortRanges) {
			if r.From <= port && port <= r.To {
				add("/port_ranges", "the range %d-%d covers port %d, which the gateway's own DNS and DHCP services use: their traffic would end in the NTP server", r.From, r.To, port)
			}
		}
	}
	if d := t.Destination; d != nil && (d.Network != nil || d.Uplink != nil) {
		add("/destination", "the destination of a time fault is the server the device asked: an address, a prefix or a host name, not a network or the uplink")
	}

	answer := model.TimeFaultAnswerNormal
	if t.Answer != nil {
		answer = *t.Answer
	}
	if !answer.Valid() {
		add("/answer", "%q is not an answer", string(answer))
	}
	if t.Leap != nil && !t.Leap.Valid() {
		add("/leap", "%q is not a leap indicator: insert or delete", string(*t.Leap))
	}

	// an answer other than normal is the whole fault: it replaces the time, so there is nothing for the fields of the
	// time to shape
	if answer != model.TimeFaultAnswerNormal {
		for _, f := range []struct {
			name    string
			present bool
		}{{"offset", t.Offset != nil}, {"absolute", t.Absolute != nil}, {"skew_ppm", t.SkewPpm != nil}, {"leap", t.Leap != nil}} {
			if f.present {
				add("/"+f.name, "the answer %s is the whole fault: it excludes %s", answer, f.name)
			}
		}
	}

	if (answer == model.TimeFaultAnswerKissOfDeath) != (t.KissCode != nil) {
		if answer == model.TimeFaultAnswerKissOfDeath {
			add("/kiss_code", "the answer kiss_of_death needs a kiss_code: RATE, DENY or RSTR")
		} else {
			add("/kiss_code", "kiss_code is only used by the answer kiss_of_death")
		}
	}
	if t.KissCode != nil && !t.KissCode.Valid() {
		add("/kiss_code", "%q is not a kiss code: RATE, DENY or RSTR", string(*t.KissCode))
	}

	if t.Offset != nil {
		if _, err := ParseTimeOffset(*t.Offset); err != nil {
			add("/offset", "%s", err)
		}
	}
	if t.Offset != nil && t.Absolute != nil {
		add("/absolute", "offset and absolute exclude each other: the offset moves the correct time, absolute replaces it")
	}
	if t.Absolute != nil && t.Absolute.Before(ntpEpoch) {
		add("/absolute", "an NTP timestamp starts at 1900-01-01T00:00:00Z: an earlier time cannot be served")
	}
	if t.SkewPpm != nil {
		if math.Abs(float64(*t.SkewPpm)) > MaxTimeSkewPPM {
			add("/skew_ppm", "the skew must be within ±%d ppm", MaxTimeSkewPPM)
		}
	}

	// a time fault must change something; a field that is set counts, even with the value zero: "+0s" is the way to
	// say "the correct time" for a device or a server that a wider fault of its group or network would otherwise hit
	if answer == model.TimeFaultAnswerNormal && t.Offset == nil && t.Absolute == nil && t.SkewPpm == nil && t.Leap == nil {
		add("", "a time fault changes something: give an offset, an absolute time, a skew, a leap or an answer other than normal")
	}
}

// ---- the match ----------------------------------------------------------------------------

// timeMatch is the selector of a time fault as it applies: the protocol is UDP and, without ports, the port is the
// NTP port. Two faults that select the same traffic have the same match, written out or not.
func timeMatch(t model.TimeFault) model.TrafficMatch {
	m := convert[model.TrafficMatch](t)
	if protocolOf(m) == "any" {
		udp := model.Protocol("udp")
		m.Protocol = &udp
	}
	if len(deref(m.Ports)) == 0 && len(deref(m.PortRanges)) == 0 {
		m.Ports = &[]int{NTPPort}
	}
	return m
}

// TimePorts are the UDP ports a time fault selects: the ones it names, else the NTP port.
func TimePorts(t model.TimeFault) (ports []int, ranges []model.PortRange) {
	m := timeMatch(t)
	return deref(m.Ports), deref(m.PortRanges)
}

// ---- candidates ---------------------------------------------------------------------------

// timeScopeAllowed reports whether a scope can hold a time fault. Validation refuses the others, but a profile
// with a time part can be activated on a target that was fine for its old parts (the configuration changes
// under an activation), and a part that no client can be told by must not turn into a candidate.
func timeScopeAllowed(s model.Scope) bool {
	k := scopeKind(&s)
	for _, ok := range timeTargetKinds {
		if k == ok {
			return true
		}
	}
	return false
}

func timeCandidate(layer Layer, id, name string, since time.Time, scope model.Scope, t model.TimeFault) Candidate {
	m := timeMatch(t)
	// the ports are always there (the NTP port by default), so a time fault has a port part like a TLS case, and
	// the destination narrows it. Only a device scope tells the two apart by level (1 against 3); on a group or a
	// network both have the level 5 or 7 of "destination and/or port/protocol" in the table of §2.4, and the newer wins
	c := newCandidate(FamilyTime, layer, id, name, since, scope, m.Destination != nil, true)
	c.Match, c.Time = m, &t
	return c
}

// TimeCandidates returns the time faults (overlays, and the time parts of activated profiles) that apply to a source
// of traffic, in the order the precedence rules of §2.4 try them (RankOrder): the NTP server answers a query with the
// first of them whose destination is the server the query was meant for, so a fault for one server does not hide a
// less specific one for the others. On a group or a network a fault for one server and one for every server have the
// same level, and the newer of the two comes first. Which server a query was meant for is known to the server only (the port it
// received the query on, decision D36).
func (w *World) TimeCandidates(src Source) []Candidate {
	var out []Candidate
	for _, c := range w.candidates() {
		if c.Family == FamilyTime && w.scopeMatches(c.Scope, src.Subject) {
			out = append(out, c)
		}
	}
	RankOrder(out)
	return out
}

// TimeSelectorKey names the destination selector of a time fault as the redirect of the NTP server keys on it (decision
// D36): the destination together with the ports and port ranges, whatever order they are written in. A fault without a
// destination and with no port other than 123 has no selector: the key is empty, and it applies to every server asked.
func TimeSelectorKey(t model.TimeFault) string {
	m := timeMatch(t)
	if m.Destination == nil && matchKey(m) == matchKey(timeMatch(model.TimeFault{})) {
		return ""
	}
	return matchKey(m)
}

// TimeWinner picks the fault that decides a query that arrived on one redirect port, from the ranked candidates of a
// source (TimeCandidates): the first one without a selector or whose selector is redirected to the port. Which
// selectors those are is for the caller to say, since the compiler assigns the ports. A fault without a selector
// applies to every server, so a newer one hides an older fault for one server on that server's port too, and an older
// one is hidden by a newer fault for one server only on that server's port. The compiler writes the winner of each
// port of a group of sources into the configuration of the NTP server, which looks the fault up by the port alone.
func TimeWinner(ranked []Candidate, redirected func(selector string) bool) (Candidate, bool) {
	for _, c := range ranked {
		if c.Time == nil {
			continue
		}
		if k := TimeSelectorKey(*c.Time); k == "" || redirected(k) {
			return c, true
		}
	}
	return Candidate{}, false
}

// timeSummary says what a time fault does, in a few words.
func timeSummary(t model.TimeFault) string {
	answer := model.TimeFaultAnswerNormal
	if t.Answer != nil {
		answer = *t.Answer
	}
	switch answer {
	case model.TimeFaultAnswerNormal:
	case model.TimeFaultAnswerKissOfDeath:
		if t.KissCode != nil {
			return "time kiss_of_death " + string(*t.KissCode)
		}
		return "time kiss_of_death"
	default:
		return "time " + string(answer)
	}
	var parts []string
	if t.Offset != nil {
		parts = append(parts, "offset "+*t.Offset)
	}
	if t.Absolute != nil {
		parts = append(parts, "absolute "+t.Absolute.UTC().Format(time.RFC3339))
	}
	if t.SkewPpm != nil {
		parts = append(parts, "skew "+strconv.FormatFloat(float64(*t.SkewPpm), 'f', -1, 32)+" ppm")
	}
	if t.Leap != nil {
		parts = append(parts, "leap "+string(*t.Leap))
	}
	return "time " + strings.Join(parts, ", ")
}

// ---- the scenario's wait and check ----------------------------------------------------------

// ntpWait checks a wait for the NTP server's query: it has no selector, the query is the device's own.
func (v *validator) ntpWait(path string, w model.WaitCondition) {
	for _, f := range []struct {
		name    string
		present bool
	}{{"destination", w.Destination != nil}, {"protocol", w.Protocol != nil}, {"ports", w.Ports != nil}, {"port_ranges", w.PortRanges != nil}} {
		if f.present {
			v.add(path+"/"+f.name, CodeUnexpectedField, "%s is only used when waiting for a connection: a wait for ntp_query_seen has no selector", f.name)
		}
	}
}

// ntpQueried checks the check of the NTP query: a block without fields.
func (v *validator) ntpQueried(path string, c model.NtpQueriedCheck) {
	for _, k := range sortedKeys(c) {
		v.add(schema.Pointer(path, k), CodeUnexpectedField, "ntp_queried has no fields: it is {}")
	}
}
