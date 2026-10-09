package testbed

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// One-way probes (plan §4.3). A ping can only tell the sum of the two directions; the faults of
// the product are per direction (upload and download), so the measurement tests need each
// direction by itself. A probe is a numbered UDP datagram that carries the sender's clock; the echo
// at the other end computes the upload delay on arrival and answers with it and its own clock, and
// the sender computes the download delay from that. All namespaces of a bed share one machine and so
// one clock, which is what makes one-way delays meaningful without any synchronization.
//
// The loss of each direction is counted separately and exactly: the echo logs every datagram it
// receives (what the upload delivered), the sender counts the answers (what the download delivered
// of those).

// echoScript answers every probe. The answer is sent before the line is logged, so the log does
// not delay it.
const echoScript = `import socket, sys, time
addr, port = sys.argv[1], int(sys.argv[2])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind((addr, port))
print("ready", flush=True)
while True:
    d, a = s.recvfrom(256)
    now = time.time_ns()
    f = d.split()
    if len(f) != 3:
        continue
    up = now - int(f[2])
    s.sendto(b"%s %s %d %d" % (f[0], f[1], up, now), a)
    print("u", f[0].decode(), f[1].decode(), up, flush=True)
`

// probeScript sends count datagrams interval apart (until the stop file exists when count is 0),
// collects the answers for settle after the last one and prints "d <seq> <download ns>" for each,
// then "sent <n>".
const probeScript = `import os, select, socket, sys, time
run, dst, port, src, count, interval, settle, stop = sys.argv[1:9]
port, count, interval, settle = int(port), int(count), float(interval), float(settle)
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
if src != "-":
    s.bind((src, 0))
else:
    s.bind(("", 0))
got = []
def drain(until):
    while True:
        left = until - time.monotonic()
        if left <= 0:
            return
        r, _, _ = select.select([s], [], [], min(left, 0.05))
        while r:
            try:
                d, a = s.recvfrom(256)
            except BlockingIOError:
                break
            now = time.time_ns()
            f = d.split()
            if len(f) == 4 and f[0].decode() == run:
                got.append((int(f[1]), now - int(f[3])))
            r, _, _ = select.select([s], [], [], 0)
s.setblocking(False)
start = time.monotonic()
i = 0
while (count == 0 and not os.path.exists(stop)) or (count != 0 and i < count):
    s.sendto(("%s %d %d" % (run, i, time.time_ns())).encode(), (dst, port))
    i += 1
    drain(start + i * interval)
drain(time.monotonic() + settle)
for seq, down in got:
    print("d", seq, down)
print("sent", i, flush=True)
`

// Echo is the answering end of one-way probes: a process in a namespace that listens on an address
// and a port.
type Echo struct {
	proc *Process
	addr string
	port int

	mu   sync.Mutex
	runs int
}

// StartEcho starts the echo in ns, listening on addr:port, and waits until it is ready. Probes to
// addr:port, from any namespace that reaches it, are answered from addr. It runs until the bed is
// destroyed.
func StartEcho(t testingTB, ns *Namespace, addr string, port int) *Echo {
	t.Helper()
	p := ns.Start("python3", "-c", echoScript, addr, strconv.Itoa(port))
	deadline := time.Now().Add(time.Minute)
	for !strings.Contains(p.Output(), "ready") {
		select {
		case <-p.Done():
			t.Fatalf("the echo in %s did not start: %s", ns.Short, p.Output())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the echo in %s is not ready: %s", ns.Short, p.Output())
		}
	}
	return &Echo{proc: p, addr: addr, port: port}
}

// ProbeOptions say how a probe run is sent.
type ProbeOptions struct {
	// Src is the source address of the probes; empty lets the kernel choose. A host behind a client
	// (remote network) has an address of its own on the loopback and sends from it.
	Src string
	// Count datagrams are sent, Interval apart. Zero is a run that goes on until Stop.
	Count    int
	Interval time.Duration
	// Settle is how long the sender waits for answers after the last datagram: longer than the
	// longest delay of the faults in the path, both directions together.
	Settle time.Duration
}

// ProbeResult is what a probe run observed. Up are the upload delays of the datagrams that reached
// the echo, Down the download delays of the answers that came back.
type ProbeResult struct {
	Sent int
	// Delivered is the number of datagrams that reached the echo, Replied the number of answers that
	// reached the sender.
	Delivered, Replied int
	Up, Down           []time.Duration
	// UpArrivals and DownArrivals are the sequence numbers in the order the datagrams arrived at
	// the echo (a duplicate is listed again) and the answers at the sender (likewise): what reordering,
	// duplication and the pattern of losses are measured from.
	UpArrivals, DownArrivals []int
}

// duplicates counts the arrivals of a sequence that were already there.
func duplicates(arrivals []int) int {
	seen := map[int]bool{}
	n := 0
	for _, s := range arrivals {
		if seen[s] {
			n++
		}
		seen[s] = true
	}
	return n
}

// reordered counts the arrivals (of a sequence that was not seen before) that came after a datagram
// with a higher number: the datagrams that were overtaken.
func reordered(arrivals []int) int {
	seen := map[int]bool{}
	hi, n := -1, 0
	for _, s := range arrivals {
		if seen[s] {
			continue
		}
		seen[s] = true
		if s < hi {
			n++
		}
		hi = max(hi, s)
	}
	return n
}

// UpDuplicates is the number of datagrams that reached the echo more than once (counting each extra
// copy); DownDuplicates the same for the answers at the sender. Note that a duplicated upload makes
// the echo answer twice, so the answers carry duplicates of an upload fault too.
func (r ProbeResult) UpDuplicates() int   { return duplicates(r.UpArrivals) }
func (r ProbeResult) DownDuplicates() int { return duplicates(r.DownArrivals) }

// UpReordered is the number of datagrams that arrived at the echo after one that was sent later;
// DownReordered the same for the answers.
func (r ProbeResult) UpReordered() int   { return reordered(r.UpArrivals) }
func (r ProbeResult) DownReordered() int { return reordered(r.DownArrivals) }

// UpLost lists the sequence numbers (0 to Sent-1) that never reached the echo; DownLost those of the
// delivered ones whose answer never came back.
func (r ProbeResult) UpLost() []int { return missing(r.UpArrivals, r.Sent) }
func (r ProbeResult) DownLost() []int {
	var out []int
	got := map[int]bool{}
	for _, s := range r.DownArrivals {
		got[s] = true
	}
	for _, s := range r.UpArrivals {
		if !got[s] {
			out = append(out, s)
			got[s] = true
		}
	}
	sort.Ints(out)
	return out
}

func missing(arrivals []int, sent int) []int {
	got := make([]bool, sent)
	for _, s := range arrivals {
		if s >= 0 && s < sent {
			got[s] = true
		}
	}
	var out []int
	for i, g := range got {
		if !g {
			out = append(out, i)
		}
	}
	return out
}

// UpLoss is the fraction of the datagrams the upload lost.
func (r ProbeResult) UpLoss() float64 {
	if r.Sent == 0 {
		return 0
	}
	return float64(r.Sent-r.Delivered) / float64(r.Sent)
}

// DownLoss is the fraction of the answers the download lost, of those that were sent (the ones that
// reached the echo).
func (r ProbeResult) DownLoss() float64 {
	if r.Delivered == 0 {
		return 0
	}
	return float64(r.Delivered-r.Replied) / float64(r.Delivered)
}

// UpMedian and DownMedian are the median delays of the two directions.
func (r ProbeResult) UpMedian() time.Duration   { return Median(r.Up) }
func (r ProbeResult) DownMedian() time.Duration { return Median(r.Down) }

// String is the measured distribution of the run, for the log of a test that asserts on it.
func (r ProbeResult) String() string {
	return fmt.Sprintf("sent %d, delivered %d (loss %.2f%%), replied %d (loss %.2f%%); up median %v p5 %v p95 %v; down median %v p5 %v p95 %v",
		r.Sent, r.Delivered, r.UpLoss()*100, r.Replied, r.DownLoss()*100,
		r.UpMedian(), Percentile(r.Up, 5), Percentile(r.Up, 95),
		r.DownMedian(), Percentile(r.Down, 5), Percentile(r.Down, 95))
}

// Probe sends o.Count probes from the namespace to the echo and returns the result. An error means
// the run itself failed (the sender did not run), not that packets were lost.
func (e *Echo) Probe(from *Namespace, o ProbeOptions) (ProbeResult, error) {
	if o.Count <= 0 {
		return ProbeResult{}, fmt.Errorf("testbed: a probe run needs a count; use Begin for one that goes on")
	}
	r := e.Begin(from, o)
	return r.wait(time.Duration(o.Count)*o.Interval + o.Settle + 3*time.Minute)
}

// MustProbe is Probe that fails the test when the run itself fails.
func (e *Echo) MustProbe(t testingTB, from *Namespace, o ProbeOptions) ProbeResult {
	t.Helper()
	res, err := e.Probe(from, o)
	if err != nil {
		t.Fatalf("probe from %s to %s:%d: %v", from.Short, e.addr, e.port, err)
	}
	return res
}

// Running is a probe run that goes on until it is stopped.
type Running struct {
	e        *Echo
	from     *Namespace
	run      string
	proc     *Process
	stopFile string
	started  time.Time
}

// Begin starts a run in the background. With Count 0 it sends until Stop; with a count it ends by
// itself, and Stop returns its result too.
func (e *Echo) Begin(from *Namespace, o ProbeOptions) *Running {
	e.mu.Lock()
	e.runs++
	run := strconv.Itoa(e.runs)
	e.mu.Unlock()
	dir, err := os.MkdirTemp("", "probe")
	if err != nil {
		panic(err)
	}
	stop := filepath.Join(dir, "stop")
	src := o.Src
	if src == "" {
		src = "-"
	}
	p := from.Start("python3", "-c", probeScript, run, e.addr, strconv.Itoa(e.port), src,
		strconv.Itoa(o.Count), fmt.Sprintf("%.4f", o.Interval.Seconds()), fmt.Sprintf("%.3f", o.Settle.Seconds()), stop)
	return &Running{e: e, from: from, run: run, proc: p, stopFile: stop, started: time.Now()}
}

// Elapsed is how long the run has been going.
func (r *Running) Elapsed() time.Duration { return time.Since(r.started) }

// Delivered is the number of datagrams of the run that have reached the echo so far.
func (r *Running) Delivered() int { return r.e.delivered(r.run) }

// Stop ends a run that goes on, waits for the sender to collect the answers, and returns the result.
func (r *Running) Stop() (ProbeResult, error) {
	if err := os.WriteFile(r.stopFile, nil, 0o644); err != nil {
		return ProbeResult{}, err
	}
	return r.wait(3 * time.Minute)
}

func (r *Running) wait(d time.Duration) (ProbeResult, error) {
	defer func() { _ = os.RemoveAll(filepath.Dir(r.stopFile)) }()
	select {
	case <-r.proc.Done():
	case <-time.After(d):
		r.proc.Stop()
		return ProbeResult{}, fmt.Errorf("the sender in %s did not finish in %v: %s", r.from.Short, d, r.proc.Output())
	}
	res, err := parseProbe(r.proc.Output(), r.e.proc.Output(), r.run)
	if err != nil {
		return ProbeResult{}, err
	}
	return res, nil
}

func (e *Echo) delivered(run string) int {
	n := 0
	for _, l := range strings.Split(e.proc.Output(), "\n") {
		if f := strings.Fields(l); len(f) == 4 && f[0] == "u" && f[1] == run {
			n++
		}
	}
	return n
}

// parseProbe reads the output of the sender and of the echo for one run.
func parseProbe(sender, echo, run string) (ProbeResult, error) {
	var r ProbeResult
	sent := false
	downSeen := map[int]bool{}
	for _, l := range strings.Split(sender, "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) == 3 && f[0] == "d":
			ns, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil {
				return r, fmt.Errorf("testbed: probe line %q: %w", l, err)
			}
			seq, err := strconv.Atoi(f[1])
			if err != nil {
				return r, fmt.Errorf("testbed: probe line %q: %w", l, err)
			}
			r.DownArrivals = append(r.DownArrivals, seq)
			if downSeen[seq] {
				continue // a copy of an answer: counted in DownDuplicates, not another reply
			}
			downSeen[seq] = true
			r.Down = append(r.Down, time.Duration(ns))
			r.Replied++
		case len(f) == 2 && f[0] == "sent":
			n, err := strconv.Atoi(f[1])
			if err != nil {
				return r, fmt.Errorf("testbed: probe line %q: %w", l, err)
			}
			r.Sent, sent = n, true
		}
	}
	if !sent {
		return r, fmt.Errorf("testbed: the probe sender did not report: %q", sender)
	}
	seen := map[string]bool{}
	for _, l := range strings.Split(echo, "\n") {
		f := strings.Fields(l)
		if len(f) != 4 || f[0] != "u" || f[1] != run {
			continue
		}
		if seq, err := strconv.Atoi(f[2]); err == nil {
			r.UpArrivals = append(r.UpArrivals, seq)
		}
		if seen[f[2]] {
			continue
		}
		ns, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil {
			return r, fmt.Errorf("testbed: echo line %q: %w", l, err)
		}
		seen[f[2]] = true
		r.Up = append(r.Up, time.Duration(ns))
		r.Delivered++
	}
	return r, nil
}
