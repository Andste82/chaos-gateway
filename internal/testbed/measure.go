package testbed

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PingResult is what a ping run observed: one RTT and one TTL per reply.
type PingResult struct {
	Sent, Received int
	RTTs           []time.Duration
	TTLs           []int
}

// Loss returns the fraction of lost packets, 0 to 1.
func (r PingResult) Loss() float64 {
	if r.Sent == 0 {
		return 0
	}
	return float64(r.Sent-r.Received) / float64(r.Sent)
}

// Median returns the median RTT, or 0 without replies.
func (r PingResult) Median() time.Duration { return Median(r.RTTs) }

var (
	replyRE   = regexp.MustCompile(`ttl=(\d+) time=([0-9.]+) ms`)
	summaryRE = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received`)
)

// ParsePing reads the output of iputils ping. It fails when the summary line is missing, which
// means ping itself failed (unknown host, no permission) rather than lost packets.
func ParsePing(out string) (PingResult, error) {
	m := summaryRE.FindStringSubmatch(out)
	if m == nil {
		return PingResult{}, fmt.Errorf("testbed: no ping summary in %q", out)
	}
	var r PingResult
	r.Sent, _ = strconv.Atoi(m[1])
	r.Received, _ = strconv.Atoi(m[2])
	for _, rm := range replyRE.FindAllStringSubmatch(out, -1) {
		ttl, _ := strconv.Atoi(rm[1])
		ms, _ := strconv.ParseFloat(rm[2], 64)
		r.TTLs = append(r.TTLs, ttl)
		r.RTTs = append(r.RTTs, time.Duration(ms*float64(time.Millisecond)))
	}
	return r, nil
}

// Ping sends count echo requests from the namespace, interval apart, and returns what came
// back. Lost packets are not an error: assert on Loss.
func Ping(ctx context.Context, from *Namespace, dst string, count int, interval time.Duration) (PingResult, error) {
	out, _ := from.Run(ctx, "ping", "-n", "-c", strconv.Itoa(count),
		"-i", fmt.Sprintf("%.3f", interval.Seconds()), "-W", "1", dst)
	return ParsePing(out)
}

// MustPing is Ping that fails the test on an error of ping itself.
func MustPing(t testingTB, from *Namespace, dst string, count int, interval time.Duration) PingResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(count+10)*time.Second+time.Duration(count)*interval)
	defer cancel()
	r, err := Ping(ctx, from, dst, count, interval)
	if err != nil {
		t.Fatalf("ping %s from %s: %v", dst, from.Short, err)
	}
	return r
}

// testingTB is the part of testing.TB the helpers need; it keeps measure.go testable.
type testingTB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// Median returns the median of ds (the mean of the two middle values for an even count), or 0
// for an empty slice. It does not modify ds.
func Median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Percentile returns the p-th percentile (0–100, nearest rank) of ds, or 0 for an empty slice.
func Percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(p/100*float64(len(s)) + 0.999999999)
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1]
}

// Within reports whether got is within tolerance of want: abs plus frac of want. The plan's
// latency tolerance is ±2 ms + 5 % (§4.3); under emulation use a wider one.
func Within(got, want, abs time.Duration, frac float64) bool {
	tol := abs + time.Duration(frac*float64(want))
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// SNMPCounter reads one counter of /proc/net/snmp inside a namespace: proto is the line's prefix
// ("Ip", "Udp") and name the field ("InHdrErrors", "InCsumErrors"). The kernel counts there the
// packets it discards because their checksums do not hold, which is where corrupted packets end.
func SNMPCounter(ns *Namespace, proto, name string) (int64, error) {
	return snmpCounter(ns.Must("cat", "/proc/net/snmp"), proto, name)
}

func snmpCounter(text, proto, name string) (int64, error) {
	var header []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != proto+":" {
			continue
		}
		if header == nil {
			header = f // the first line of a protocol names the fields, the second holds the values
			continue
		}
		for i, h := range header {
			if h == name && i < len(f) {
				return strconv.ParseInt(f[i], 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("testbed: no counter %s %s in /proc/net/snmp", proto, name)
}
