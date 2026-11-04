//go:build testbed

package api_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The clients of the level 1 tests of the NTP server (plan M34, notes for the stages). None of them sets the clock: the system
// clock is not namespaced, so a client that stepped it would move the clock of the whole machine under every other test
// (`busybox ntpd -q` does, which is why it is not used).
//
//   - sntp.py is a Python SNTP client for exact assertions. It shares no code with the server, so an error that the encoder and the
//     parser of the server make alike (a swapped field, a wrong era) shows. It reports every field of the answer, the offset and
//     delay of RFC 5905 and whether a correct client would take the answer.
//   - `chronyd -Q` is a real client that measures once and prints the offset without touching the clock: what a correct client
//     does with an answer that is unsynchronized, a kiss-of-death or an invalid one.
//   - `ntpdig` (ntpsec) asks once and prints the offset.
//   - fakentp.py is "the real server" behind the uplink or in the device's own network.

// sntp sends count queries from a namespace to server (host:port) with sntp.py and returns what came back, one reply each.
func (b *ntpBed) sntp(t *testing.T, from *testbed.Namespace, server string, args ...string) []sntpReply {
	t.Helper()
	return runSNTP(t, func(ctx context.Context, name string, args ...string) (string, error) {
		return from.Run(ctx, name, args...)
	}, b.scripts, server, args...)
}

// ask is one query; the timeout is 2 s unless the arguments say otherwise.
func (b *ntpBed) ask(t *testing.T, from *testbed.Namespace, server string, args ...string) sntpReply {
	t.Helper()
	return b.sntp(t, from, server, args...)[0]
}

// best sends n queries a little apart and returns the answer with the shortest round trip: the one whose offset the delay of
// the path has disturbed least (the choice of chrony and of ntpd, too).
func (b *ntpBed) best(t *testing.T, from *testbed.Namespace, server string, n int) sntpReply {
	t.Helper()
	var best sntpReply
	for _, r := range b.sntp(t, from, server, "--count", strconv.Itoa(n), "--interval", "0.1") {
		if !r.Replied || r.Transmit == nil {
			continue
		}
		if best.Transmit == nil || r.rtt() < best.rtt() {
			best = r
		}
	}
	if best.Transmit == nil {
		t.Fatalf("no answer from %s in %d queries", server, n)
	}
	return best
}

// until asks from a namespace until the answer satisfies ok (the server needs a moment to have the new configuration) and
// returns it.
func (b *ntpBed) until(t *testing.T, what string, from *testbed.Namespace, server string, ok func(sntpReply) bool) sntpReply {
	t.Helper()
	var last sntpReply
	waitFor(t, 40*time.Second, what, func() bool {
		last = b.ask(t, from, server, "--timeout", "1.5")
		return ok(last)
	})
	return last
}

// chronyConf writes a configuration for a client that does not listen on anything: no command port, a private pid file and
// command socket (two runs must not meet in /run), and the server asked at port 123.
func (b *ntpBed) chronyConf(t *testing.T, server string, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	lines := []string{
		"server " + server + " port 123 " + strings.Join(extra, " "),
		"cmdport 0",
		"pidfile " + filepath.Join(dir, "chronyd.pid"),
		"bindcmdaddress " + filepath.Join(dir, "chronyd.sock"),
		"driftfile " + filepath.Join(dir, "drift"),
		"dumpdir " + dir,
		"logdir " + dir,
	}
	path := filepath.Join(dir, "chrony.conf")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// chronydBinary returns a copy of the chronyd executable in a private directory. Where the host confines chronyd by the path of its
// executable (the AppArmor profile of the Ubuntu package is loaded on the host and applies to a privileged container, too), the
// original cannot open a configuration file in a temporary directory ("Could not open ...chrony.conf: Permission denied", level 1
// in the container, while the VMs, which load no profile, run it); a copy has another path and runs unconfined.
func chronydBinary(t *testing.T) string {
	t.Helper()
	orig, err := exec.LookPath("chronyd")
	if err != nil {
		t.Skipf("chronyd is not installed: %v", err)
	}
	data, err := os.ReadFile(orig)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "chronyd")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// chronyQ runs `chronyd -Q` from a namespace against a server: it measures the offset (with four samples, as iburst does) and
// prints it without setting the clock, or, when no source is usable, runs until the timeout and prints nothing of the kind.
func (b *ntpBed) chronyQ(t *testing.T, from *testbed.Namespace, server string) chronyResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, _ := from.Run(ctx, chronydBinary(t), "-Q", "-u", "root", "-t", "25", "-f", b.chronyConf(t, server, "iburst"))
	return parseChronyQ(out)
}

// startChronyd runs chronyd as a client that polls every second and never touches the clock (-x), until the bed ends. The
// queries it sends are what the counters of the server show.
func (b *ntpBed) startChronyd(t *testing.T, from *testbed.Namespace, server string) *testbed.Process {
	t.Helper()
	return from.Start(chronydBinary(t), "-d", "-x", "-u", "root", "-f", b.chronyConf(t, server, "minpoll 0 maxpoll 6"))
}

// ntpdig asks a server once with the ntpsec client, which prints the offset and sets nothing without -s or -S.
func (b *ntpBed) ntpdig(t *testing.T, from *testbed.Namespace, server string) ntpdigResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, _ := from.Run(ctx, "ntpdig", "-t", "4", server)
	return parseNtpdig(out)
}

// startFakeNTP starts "a real server" in a namespace: it answers with the host's time at stratum 3 and the label as the
// reference id, and writes a line to the returned log file for every query.
func (b *ntpBed) startFakeNTP(t *testing.T, ns *testbed.Namespace, addr, label string) (logFile string) {
	t.Helper()
	logFile = filepath.Join(b.scripts, "fake-"+label+".log")
	p := ns.Start("python3", "-I", filepath.Join(b.scripts, "fakentp.py"), addr, "123", label, logFile)
	waitFor(t, 20*time.Second, "the stand-in for the real NTP server "+label+" listens", func() bool {
		return strings.Contains(p.Output(), "ready")
	})
	return logFile
}

// queriesIn counts the lines of a fake server's log.
func queriesIn(logFile string) int {
	raw, err := os.ReadFile(logFile)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(raw))) / 2
}
