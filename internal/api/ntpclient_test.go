package api_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/ntpserver"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The SNTP client of the level 1 tests of M34 (sntp.py) and the stand-in for a real server (fakentp.py) are scripts in testdata/ntp. The
// client shares no code with the NTP server, so that an error the encoder and the parser of the server make alike does not cancel out in
// the testbed. This file holds what both the testbed tests (e2e_ntp*_test.go) and a level 0 test use: the reply the client prints, the
// scripts on disk, and the call; the level 0 test runs the client against the real server on the loopback, so that a mistake of the
// client shows here and not first in the testbed.

//go:embed testdata/ntp/sntp.py
var sntpScript []byte

//go:embed testdata/ntp/fakentp.py
var fakeNTPScript []byte

// sntpReply is one query of sntp.py and what came back.
type sntpReply struct {
	Replied   bool   `json:"replied"`
	Error     string `json:"error"`
	Sent      int    `json:"sent"`
	SPort     int    `json:"sport"`
	Size      int    `json:"size"`
	From      string `json:"from"`
	FromPort  int    `json:"from_port"`
	Leap      int    `json:"leap"`
	Version   int    `json:"version"`
	Mode      int    `json:"mode"`
	Stratum   int    `json:"stratum"`
	Poll      int    `json:"poll"`
	Precision int    `json:"precision"`

	RootDelay      float64 `json:"root_delay"`
	RootDispersion float64 `json:"root_dispersion"`
	RefIDIP        string  `json:"refid_ip"`
	RefIDASCII     string  `json:"refid_ascii"`

	// the timestamps as seconds since 1970 in the era nearest to the client's clock; nil for a zero timestamp
	Reference *float64 `json:"reference"`
	Origin    *float64 `json:"origin"`
	Receive   *float64 `json:"receive"`
	Transmit  *float64 `json:"transmit"`
	// T1 and T4 are the client's own send and receive times
	T1 float64 `json:"t1"`
	T4 float64 `json:"t4"`

	OriginOK   bool `json:"origin_ok"`
	StrictOK   bool `json:"strict_ok"`
	CarelessOK bool `json:"careless_ok"`
	// Offset and Delay are the offset and the round trip delay of RFC 5905, in seconds
	Offset *float64 `json:"offset"`
	Delay  *float64 `json:"delay"`
}

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// chronyResult is what `chronyd -Q` made of a server.
type chronyResult struct {
	// Took says that chrony measured an offset: it accepted the server as a source it can use.
	Took   bool
	Offset time.Duration
	Output string
}

var chronyOffset = regexp.MustCompile(`System clock wrong by (-?\d+(?:\.\d+)?) seconds`)

// parseChronyQ reads the output of `chronyd -Q`: the line it ends with when it found a usable source, none otherwise.
func parseChronyQ(out string) chronyResult {
	res := chronyResult{Output: out}
	if m := chronyOffset.FindStringSubmatch(out); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		res.Took, res.Offset = true, secs(f)
	}
	return res
}

// ntpdigResult is the line `ntpdig` prints for a server.
type ntpdigResult struct {
	Took    bool
	Offset  time.Duration
	Stratum int
	Leap    string
	Output  string
}

// the format of ntpdig: the time, the offset, the delay, the server and its stratum and leap, for example
// `2026-11-03 12:00:00.123456 (+0000) +0.000123 +/- 0.000456 10.10.0.1 s2 no-leap`
var ntpdigLine = regexp.MustCompile(`([+-]\d+(?:\.\d+)?)\s+\+/-\s+\d+(?:\.\d+)?\s+.*\bs(\d+)\s+(\S+)\s*$`)

// parseNtpdig reads the output of `ntpdig`: the line of the server, if there is one.
func parseNtpdig(out string) ntpdigResult {
	res := ntpdigResult{Output: out}
	for _, l := range strings.Split(out, "\n") {
		if m := ntpdigLine.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			f, _ := strconv.ParseFloat(m[1], 64)
			res.Took, res.Offset, res.Leap = true, secs(f), m[3]
			res.Stratum, _ = strconv.Atoi(m[2])
		}
	}
	return res
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// within says that d is in [lo, hi].
func within(d, lo, hi time.Duration) bool { return d >= lo && d <= hi }

// served is the transmit time stamp of the answer.
func (r sntpReply) served() time.Time {
	if r.Transmit == nil {
		return time.Time{}
	}
	return time.Unix(0, 0).Add(secs(*r.Transmit))
}

// shift is how far the served time is from the client's clock, taken at the middle of the round trip: the correct time gives
// about zero, a fault of +400d about 400 days.
func (r sntpReply) shift() time.Duration {
	if r.Transmit == nil {
		return 0
	}
	return secs(*r.Transmit - (r.T1+r.T4)/2)
}

// rtt is the round trip time the client saw.
func (r sntpReply) rtt() time.Duration { return secs(r.T4 - r.T1) }

// measured is the offset of RFC 5905: what a client that takes the answer would correct its clock by.
func (r sntpReply) measured() time.Duration {
	if r.Offset == nil {
		return 0
	}
	return secs(*r.Offset)
}

// correct says that the answer is a server reply that carries the host's time within tol.
func (r sntpReply) correct(tol time.Duration) bool {
	if !r.Replied || r.Mode != 4 || r.Transmit == nil {
		return false
	}
	d := r.shift()
	return d > -tol && d < tol
}

// near says that the served time is want plus or minus tol away from the client's clock.
func (r sntpReply) near(want, tol time.Duration) bool {
	if !r.Replied || r.Mode != 4 || r.Transmit == nil {
		return false
	}
	d := r.shift() - want
	return d > -tol && d < tol
}

// scriptDir writes the scripts to a directory the namespaces can read.
func scriptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sntp.py"), sntpScript, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fakentp.py"), fakeNTPScript, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runSNTP runs sntp.py with run (a command in a namespace, or here on the loopback) against server and returns the reply of each query.
func runSNTP(t *testing.T, run func(ctx context.Context, name string, args ...string) (string, error), dir, server string, args ...string) []sntpReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := run(ctx, "python3", append([]string{"-I", filepath.Join(dir, "sntp.py"), server}, args...)...)
	if err != nil {
		t.Fatalf("sntp.py %s: %v", server, err)
	}
	var replies []sntpReply
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "{") {
			continue
		}
		var r sntpReply
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("sntp.py printed %q: %v", l, err)
		}
		replies = append(replies, r)
	}
	if len(replies) == 0 {
		t.Fatalf("sntp.py printed no reply:\n%s", out)
	}
	return replies
}

// M34 level 0: the Python client of the level 1 tests against the real NTP server on the loopback. The client decodes the packets on its own
// (struct, RFC 5905), so each answer kind, the eras of 2036 and 2038 and the sizes are checked by a decoder that shares nothing with the
// server's, and the assertions of the testbed tests that rely on the client (the strict and the careless reading of an answer, the
// offset, the delay) are known to be right before they run on a kernel.
func TestThePythonSNTPClientReadsTheAnswersOfTheServerAsTheTestbedTestsExpect(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	dir := scriptDir(t)
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	_ = pc.Close()

	sync := ntpserver.Synchronized
	srv := ntpserver.New(ntpserver.Options{Sync: sync, RateLimit: -1, BasePort: port})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ntpserver.ListenUDP("127.0.0.1")) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("Serve: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the server does not listen")
	}

	const ov = "11111111-1111-4111-8111-111111111111"
	configure := func(fault string) {
		t.Helper()
		since := time.Now().UTC().Format(time.RFC3339Nano)
		faults := ""
		if fault != "" {
			faults = fmt.Sprintf(`{"overlay":%q,"port":%d,"since":%q,"sources":["127.0.0.0/8"],%s}`, ov, port, since, fault)
		}
		raw := fmt.Sprintf(`{"generation":1,"listen":[%d],"networks":[{"network":"22222222-2222-4222-8222-222222222222","gateway":"127.0.0.1","clients":"127.0.0.0/8"}],"faults":[%s]}`, port, faults)
		var c model.NtpServiceConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		srv.SetConfig(&c)
	}
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	server := fmt.Sprintf("127.0.0.1:%d", port)
	ask := func(args ...string) sntpReply { return runSNTP(t, run, dir, server, args...)[0] }

	// the correct time, as a correct server answers it
	configure("")
	r := ask()
	if !r.StrictOK || r.Mode != 4 || r.Version != 4 || r.Leap != 0 || r.Stratum != 2 || r.Size != 48 || r.RefIDIP != "127.0.0.1" || !r.OriginOK || !r.correct(200*time.Millisecond) {
		t.Errorf("the correct answer: %+v", r)
	}
	if r.Offset == nil || abs(r.measured()) > 200*time.Millisecond || r.Delay == nil || *r.Delay < 0 {
		t.Errorf("the offset and the delay of the answer: %+v", r)
	}
	if v3 := ask("--version", "3"); v3.Version != 3 || !v3.StrictOK {
		t.Errorf("the answer to a version 3 query: %+v", v3)
	}

	// a time in era 1 (2038), at the end of era 0 and just after it, and before the NTP epoch plus seventy years
	for _, p := range []struct{ name, instant string }{
		{"2038", "2038-01-19T03:09:07Z"},
		{"NTP era 1", "2036-02-07T06:23:16Z"},
		{"after the rollover", "2036-02-07T06:28:30Z"},
		{"1970", "1970-01-01T00:00:00Z"},
	} {
		want, _ := time.Parse(time.RFC3339, p.instant)
		configure(fmt.Sprintf(`"absolute":%q`, p.instant))
		r := ask()
		if !r.StrictOK || abs(r.served().Sub(want)) > 5*time.Second {
			t.Errorf("%s: the client read %v, want a moment after %v: %+v", p.name, r.served().UTC(), want, r)
		}
	}

	// an offset, a skew and a leap indicator
	configure(`"offset":"+400d"`)
	if r := ask(); !r.near(400*24*time.Hour, time.Second) || !r.StrictOK {
		t.Errorf("+400d: %+v", r)
	}
	configure(`"leap":"insert"`)
	if r := ask(); r.Leap != 1 || !r.StrictOK {
		t.Errorf("leap insert: %+v", r)
	}
	configure(`"leap":"delete"`)
	if r := ask(); r.Leap != 2 {
		t.Errorf("leap delete: %+v", r)
	}

	// a device that keeps its socket asks every query from one source port, which the level 1 test of the flows relies on; the
	// default is a new port for each query
	configure("")
	same := runSNTP(t, run, dir, server, "--count", "3", "--keep", "1")
	fresh := runSNTP(t, run, dir, server, "--count", "3")
	if len(same) != 3 || same[0].SPort == 0 || same[1].SPort != same[0].SPort || same[2].SPort != same[0].SPort || !same[2].StrictOK {
		t.Errorf("--keep asks from one port: %+v", same)
	}
	if len(fresh) != 3 || fresh[0].SPort == fresh[1].SPort || fresh[1].SPort == fresh[2].SPort {
		t.Errorf("without --keep every query has a port of its own: %+v", fresh)
	}

	// the answers instead of the time: a correct client takes none, a careless one takes what comes
	for _, c := range []struct {
		fault string
		check func(sntpReply) bool
	}{
		{`"answer":"unsynchronized"`, func(r sntpReply) bool { return r.Leap == 3 && r.Stratum == 16 && r.Mode == 4 }},
		{`"answer":"kiss_of_death","kiss_code":"DENY"`, func(r sntpReply) bool { return r.Stratum == 0 && r.RefIDASCII == "DENY" && r.Leap == 3 }},
		{`"answer":"kiss_of_death","kiss_code":"RATE"`, func(r sntpReply) bool { return r.Stratum == 0 && r.RefIDASCII == "RATE" }},
		{`"answer":"origin_mismatch"`, func(r sntpReply) bool { return !r.OriginOK && r.Mode == 4 && r.Stratum == 2 }},
		{`"answer":"zero_transmit"`, func(r sntpReply) bool { return r.Transmit == nil && r.OriginOK }},
		{`"answer":"wrong_mode"`, func(r sntpReply) bool { return r.Mode != 4 }},
	} {
		configure(c.fault)
		r := ask()
		if !r.Replied || r.StrictOK || !r.CarelessOK || !c.check(r) {
			t.Errorf("%s: %+v", c.fault, r)
		}
	}
	configure(`"answer":"silence"`)
	if r := ask("--timeout", "0.5"); r.Replied {
		t.Errorf("silence was answered: %+v", r)
	}

	// the sizes: a reply is 48 bytes, whatever the request, and a datagram that is no client query is not answered
	configure("")
	for _, pad := range []string{"0", "52", "1000"} {
		if r := ask("--pad", pad); !r.Replied || r.Size != 48 || r.Size > r.Sent {
			t.Errorf("a query of %d bytes was answered with %d: %+v", r.Sent, r.Size, r)
		}
	}
	zeros := func(n int) string { return strings.Repeat("00", n) }
	for _, raw := range []string{"160200010000000000000000", "1602" + zeros(46), "1700032a00000000", "1700032a" + zeros(464), "25" + zeros(47), "23" + zeros(19), "03" + zeros(47)} {
		if r := ask("--raw", raw, "--timeout", "0.4"); r.Replied {
			t.Errorf("the datagram %s was answered: %+v", raw[:8], r)
		}
	}
	// the client reports a server that is not there as no answer and does not crash
	if r := runSNTP(t, run, dir, "127.0.0.1:9", "--timeout", "0.3")[0]; r.Replied {
		t.Errorf("an answer from a port nobody listens on: %+v", r)
	}
}

// The level 1 tests read what the real clients print, and cannot run without them: this pins the readers to samples of the
// output of chrony 4.x (`chronyd -Q`) and of ntpsec (`ntpdig`), so that a wrong expression shows here and not in the first
// run of the testbed.
func TestTheOutputOfChronyAndNtpdigIsRead(t *testing.T) {
	chrony := map[string]struct {
		took   bool
		offset time.Duration
	}{
		"2026-11-03T12:00:00Z chronyd version 4.5 starting (+CMDMON +NTP +REFCLOCK +RTC +PRIVDROP +SCFILTER +SIGND +ASYNCDNS +NTS +SECHASH +IPV6 -DEBUG)\n" +
			"2026-11-03T12:00:00Z Disabled control of system clock\n" +
			"2026-11-03T12:00:04Z System clock wrong by -0.000123 seconds (ignored)\n" +
			"2026-11-03T12:00:04Z chronyd exiting\n": {true, -123 * time.Microsecond},
		"2026-11-03T12:00:04Z System clock wrong by 34560000.500000 seconds\n":    {true, secs(34560000.5)},
		"2026-11-03T12:00:04Z System clock wrong by 0.000000 seconds (ignored)\n": {true, 0},
		// no usable source: chrony ran into the timeout without an offset, or refused every sample
		"2026-11-03T12:00:00Z chronyd version 4.5 starting\n2026-11-03T12:00:25Z No suitable source for synchronisation\n2026-11-03T12:00:25Z chronyd exiting\n": {false, 0},
		"": {false, 0},
	}
	for out, want := range chrony {
		got := parseChronyQ(out)
		if got.Took != want.took || got.Offset != want.offset || got.Output != out {
			t.Errorf("%q: took %v offset %v, want %v %v", out, got.Took, got.Offset, want.took, want.offset)
		}
	}
	dig := []struct {
		out     string
		took    bool
		offset  time.Duration
		stratum int
		leap    string
	}{
		{"2026-11-03 12:00:00.123456 (+0000) +0.000123 +/- 0.000456 10.10.0.1 s2 no-leap\n", true, 123 * time.Microsecond, 2, "no-leap"},
		{"2026-11-03 12:00:00.123456 (+0000) -34560000.250000 +/- 0.000456 10.10.0.1 s2 no-leap\n", true, secs(-34560000.25), 2, "no-leap"},
		{"2026-11-03 12:00:00.123456 (+0000) +0.000010 +/- 0.001000 10.10.0.1 s16 unsync\n", true, 10 * time.Microsecond, 16, "unsync"},
		{"2026-11-03 12:00:00.123456 (+0000) +3600.000000 +/- 0.000300 10.10.0.1 s2 leap-add\n", true, time.Hour, 2, "leap-add"},
		{"ntpdig: no eligible servers\n", false, 0, 0, ""},
		{"", false, 0, 0, ""},
	}
	for _, c := range dig {
		got := parseNtpdig(c.out)
		if got.Took != c.took || got.Offset != c.offset || got.Stratum != c.stratum || got.Leap != c.leap {
			t.Errorf("%q: %+v, want took %v offset %v s%d %s", c.out, got, c.took, c.offset, c.stratum, c.leap)
		}
	}
}
