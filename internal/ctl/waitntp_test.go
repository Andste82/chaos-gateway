package ctl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// ntpGateway is a gateway that answers GET /ntp/queries as the test says: reply gets the number of the poll (1-based) and the
// request and writes the answer, or returns false to leave the poll hanging until the client gives up.
type ntpGateway struct {
	t     *testing.T
	mu    sync.Mutex
	polls int
	since []string
	reply func(n int, w http.ResponseWriter, r *http.Request) (hang bool)
}

func (g *ntpGateway) run(tweak func(*Env), sigs <-chan os.Signal, args ...string) (code int, out, errb string) {
	g.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.polls++
		n := g.polls
		g.since = append(g.since, r.URL.Query().Get("since"))
		g.mu.Unlock()
		if g.reply(n, w, r) {
			<-r.Context().Done()
		}
	}))
	defer srv.Close()
	return runAgainst(srv.URL, tweak, sigs, args...)
}

func runAgainst(url string, tweak func(*Env), sigs <-chan os.Signal, args ...string) (int, string, string) {
	var out, errb strings.Builder
	env := Env{Stdout: &out, Stderr: &errb, Signals: sigs, Poll: 50 * time.Millisecond,
		Getenv: func(k string) string { return map[string]string{"CHAOSGW_URL": url, "CHAOSGW_TOKEN": "t"}[k] }}
	if tweak != nil {
		tweak(&env)
	}
	code := Run(args, env)
	return code, out.String(), errb.String()
}

func problemBody(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://chaos-gateway.dev/problems/" + code, "title": code, "status": status, "code": code, "detail": code})
}

var ntpBase = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// ntpPage writes a page of the log with the given entries as the gateway lists them (newest arrival first), at the gateway's
// time `date` (empty: no Date header at all).
func ntpPage(w http.ResponseWriter, date string, items ...model.NtpQueryLogEntry) {
	ntpPageNow(w, date, time.Time{}, items...)
}

// ntpPageNow is ntpPage for a gateway that names its time to the nanosecond in the page (`now`; zero: it names none).
func ntpPageNow(w http.ResponseWriter, date string, now time.Time, items ...model.NtpQueryLogEntry) {
	w.Header().Set("Content-Type", "application/json")
	if date == "" {
		w.Header()["Date"] = nil // the server sets none
	} else {
		w.Header().Set("Date", date)
	}
	if items == nil {
		items = []model.NtpQueryLogEntry{}
	}
	_ = json.NewEncoder(w).Encode(model.NtpQueryPage{Items: items, Now: now})
}

func ntpEntryAt(t time.Time, answer model.NtpQueryLogEntryAnswer) model.NtpQueryLogEntry {
	return model.NtpQueryLogEntry{Time: t, Client: "10.10.0.31", Port: 123, Answer: &answer}
}

func httpDate(t time.Time) string { return t.UTC().Format(http.TimeFormat) }

// A wait for an NTP query whose time runs out while the gateway cannot be asked says so with 2, not 1: a script that treats 2
// as an infrastructure error must not read a dead gateway as a device that does not ask.
func TestWaitNtpThatRunsOutOfTimeWithoutAnAnswerFromTheGatewayExits2(t *testing.T) {
	// a gateway that is not there, polled less than ten times before the time is up
	dead := httptest.NewServer(nil)
	url := dead.URL
	dead.Close()
	code, _, errb := runAgainst(url, func(e *Env) { e.Poll = 100 * time.Millisecond }, nil, "wait", "ntp", "esp32-42", "--timeout", "450ms")
	if code != ExitError || !strings.Contains(errb, "timed out after 450ms, and the gateway could not be asked: ") {
		t.Errorf("a dead gateway: %d %q", code, errb)
	}
	if strings.Contains(errb, "lost the gateway") {
		t.Errorf("fewer than ten polls failed: %q", errb)
	}

	// a gateway that accepts the request and never answers: the time is kept, the poll does not hold it
	g := &ntpGateway{t: t, reply: func(int, http.ResponseWriter, *http.Request) bool { return true }}
	within(t, 10*time.Second, "a wait on a gateway that does not answer", func() {
		start := time.Now()
		code, _, errb := g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "300ms")
		if code != ExitError || !strings.Contains(errb, "could not be asked") {
			t.Errorf("a gateway that hangs: %d %q", code, errb)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("--timeout 300ms took %v", d)
		}
	})

	// a gateway that answered and then hangs: the poll that is cut off by the time had a second to be answered
	g = &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		if n > 1 {
			return true
		}
		ntpPage(w, httpDate(time.Now()))
		return false
	}}
	within(t, 10*time.Second, "a wait on a gateway that stops answering", func() {
		code, _, errb := g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "1500ms")
		if code != ExitError || !strings.Contains(errb, "the gateway could not be asked: the poll was not answered") {
			t.Errorf("a gateway that stops answering: %d %q", code, errb)
		}
	})

	// a gateway that answered and then fails: the failing poll is the last word, whatever the polls before said
	g = &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		if n > 1 {
			problemBody(w, 503, "unavailable")
			return false
		}
		ntpPage(w, httpDate(time.Now()))
		return false
	}}
	code, _, errb = g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "400ms")
	if code != ExitError || !strings.Contains(errb, "and the gateway could not be asked: ") || !strings.Contains(errb, "unavailable") {
		t.Errorf("a gateway that fails after the first poll: %d %q", code, errb)
	}

	// a gateway that answers and has no query is a verdict about the device: 1, with what it may mean
	g = &ntpGateway{t: t, reply: func(_ int, w http.ResponseWriter, _ *http.Request) bool {
		ntpPage(w, httpDate(time.Now()), ntpEntryAt(time.Now().Add(-time.Hour), model.NtpQueryLogEntryAnswerNormal))
		return false
	}}
	code, _, errb = g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "300ms")
	if code != ExitFailed || !strings.Contains(errb, "timed out after 300ms: the device did not ask the gateway's NTP server") {
		t.Errorf("an answered poll without a query: %d %q", code, errb)
	}
}

// Ten failed polls in a row end the wait with 2 before the time is up, and an answered poll resets the count.
func TestWaitNtpGivesUpOnAGatewayThatFailsTenPollsInARow(t *testing.T) {
	g := &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		if n == 1 {
			ntpPage(w, httpDate(time.Now()))
			return false
		}
		problemBody(w, 503, "unavailable")
		return false
	}}
	code, _, errb := g.run(func(e *Env) { e.Poll = time.Millisecond }, nil, "wait", "ntp", "esp32-42", "--timeout", "30s")
	if code != ExitError || !strings.Contains(errb, "lost the gateway while waiting for the NTP query of device esp32-42") || strings.Count(errb, "does not answer") != pollFailures {
		t.Errorf("%d %q", code, errb)
	}

	// nine failures, an answer, nine failures: not lost
	g = &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		switch {
		case n <= 9, n >= 11 && n <= 19:
			problemBody(w, 503, "unavailable")
		case n == 10:
			ntpPage(w, httpDate(time.Now().Add(-time.Hour)))
		default:
			ntpPage(w, httpDate(time.Now().Add(-time.Hour)), ntpEntryAt(time.Now().Add(time.Hour), model.NtpQueryLogEntryAnswerNormal))
		}
		return false
	}}
	code, out, errb := g.run(func(e *Env) { e.Poll = time.Millisecond }, nil, "wait", "ntp", "esp32-42", "--timeout", "30s")
	if code != 0 || !strings.Contains(out, "asked the NTP server") || strings.Contains(errb, "lost the gateway") {
		t.Errorf("an answered poll resets the failures: %d %q %q", code, out, errb)
	}
}

// A poll that hangs is cut off after a few poll intervals and counted as a failed poll; the wait goes on.
func TestANtpPollThatHangsIsCutOffAndTheWaitGoesOn(t *testing.T) {
	g := &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		switch n {
		case 1:
			ntpPage(w, httpDate(time.Now().Add(-time.Hour)))
		case 2:
			return true
		default:
			ntpPage(w, httpDate(time.Now().Add(-time.Hour)), ntpEntryAt(time.Now().Add(time.Hour), model.NtpQueryLogEntryAnswerNormal))
		}
		return false
	}}
	within(t, 20*time.Second, "wait ntp with a poll that hangs", func() {
		code, out, errb := g.run(func(e *Env) { e.PollTimeout = 100 * time.Millisecond }, nil, "wait", "ntp", "esp32-42", "--timeout", "30s")
		if code != 0 || !strings.Contains(out, "asked the NTP server") || !strings.Contains(errb, "the gateway does not answer (1 of 10)") {
			t.Errorf("%d\n%s\n%s", code, out, errb)
		}
	})
}

// A signal ends the wait with 2, a gateway that refuses the token does too, and a device the gateway does not know ends it
// with 1 and the hint.
func TestWaitNtpEndsOnASignalARefusalAndAnUnknownDevice(t *testing.T) {
	g := &ntpGateway{t: t, reply: func(_ int, w http.ResponseWriter, _ *http.Request) bool {
		ntpPage(w, httpDate(time.Now()))
		return false
	}}
	sigs := make(chan os.Signal, 1)
	go func() { time.Sleep(200 * time.Millisecond); sigs <- syscall.SIGTERM }()
	within(t, 20*time.Second, "wait ntp and a signal", func() {
		code, _, errb := g.run(nil, sigs, "wait", "ntp", "esp32-42", "--timeout", "30s")
		if code != ExitError || !strings.Contains(errb, "terminated received while waiting for the NTP query of device esp32-42") {
			t.Errorf("a signal: %d %q", code, errb)
		}
	})

	// refused in the middle of the wait: not a verdict on the device, and not retried ten times
	for _, status := range []int{401, 403} {
		g = &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
			if n == 1 {
				ntpPage(w, httpDate(time.Now()))
				return false
			}
			problemBody(w, status, "forbidden")
			return false
		}}
		code, _, errb := g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "30s")
		if code != ExitError || strings.Contains(errb, "does not answer") {
			t.Errorf("%d: %d %q", status, code, errb)
		}
	}

	g = &ntpGateway{t: t, reply: func(_ int, w http.ResponseWriter, _ *http.Request) bool {
		w.Header().Set("Date", httpDate(time.Now()))
		problemBody(w, 404, "not_found")
		return false
	}}
	code, _, errb := g.run(nil, nil, "wait", "ntp", "ghost", "--timeout", "200ms")
	if code != ExitFailed || !strings.Contains(errb, "timed out after 200ms: the gateway does not know the device") {
		t.Errorf("an unknown device: %d %q", code, errb)
	}
}

// The wait starts at the Date of the first answer, which is the gateway's clock, and not at the newest entry of the log: a query
// that arrived between the start of the command and its first poll ends the wait, an older one does not, and a query is not
// told apart by its place in the log, which is the order of arrival and not of time.
func TestWaitNtpStartsAtTheGatewaysClockAndNotAtTheNewestEntry(t *testing.T) {
	now := ntpBase.Add(10 * time.Second)
	normal := model.NtpQueryLogEntryAnswerNormal
	dropped := model.NtpQueryLogEntryAnswerDropped
	// the log, newest arrival first: a dropped one after the start, a late batch whose query is older than the start, and
	// two queries after the start of which the later one arrived first
	offset := func(e model.NtpQueryLogEntry, ms int64) model.NtpQueryLogEntry { e.OffsetMs = &ms; return e }
	late := offset(ntpEntryAt(now.Add(2*time.Second), normal), 2000)
	early := offset(ntpEntryAt(now.Add(500*time.Millisecond), normal), 500)
	g := &ntpGateway{t: t, reply: func(_ int, w http.ResponseWriter, _ *http.Request) bool {
		ntpPage(w, httpDate(now), ntpEntryAt(now.Add(time.Second), dropped), late, early, offset(ntpEntryAt(now.Add(-5*time.Second), normal), -5000))
		return false
	}}
	code, out, errb := g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "5s")
	// the one reported is the oldest by time after the start: not the first of the list, not the newest, not the old one
	if code != 0 || !strings.Contains(out, "asked the NTP server") || !strings.Contains(out, "offset 500ms") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if g.polls != 1 {
		t.Errorf("the first answer already holds the query: %d polls", g.polls)
	}

	// only what is older than the start, or no query at all: the second poll asks from the start of the wait
	g = &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		if n < 3 {
			ntpPage(w, httpDate(now), ntpEntryAt(now.Add(-5*time.Second), normal))
			return false
		}
		ntpPage(w, httpDate(now.Add(time.Minute)), ntpEntryAt(now.Add(30*time.Second), normal))
		return false
	}}
	code, out, errb = g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "5s")
	if code != 0 || !strings.Contains(out, "asked the NTP server") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if g.since[0] != "" || g.since[1] != now.Format(time.RFC3339Nano) {
		t.Errorf("the polls ask from: %q", g.since)
	}
}

// A query stamped exactly at the start of the wait is after the command began (a query at the very moment of the gateway's
// answer cannot be told from one a nanosecond later), and one a nanosecond before it is not.
func TestWaitNtpCountsAQueryStampedExactlyAtTheStart(t *testing.T) {
	normal := model.NtpQueryLogEntryAnswerNormal
	at := ntpBase.Add(10 * time.Second)
	for _, tc := range []struct {
		name string
		e    time.Time
		code int
	}{
		{"at the start", at, 0},
		{"a nanosecond before", at.Add(-time.Nanosecond), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &ntpGateway{t: t, reply: func(_ int, w http.ResponseWriter, _ *http.Request) bool {
				ntpPageNow(w, httpDate(at), at, ntpEntryAt(tc.e, normal))
				return false
			}}
			code, out, errb := g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "300ms")
			if code != tc.code {
				t.Errorf("%d, want %d: %q %q", code, tc.code, out, errb)
			}
		})
	}
}

// The start is the gateway's time to the nanosecond when it names it, not its Date, which is rounded down to the second: a
// query answered 700 ms before the command, with the fault maybe not yet in force, must not end the wait that began after it.
func TestWaitNtpStartsAtTheNanosecondTimeOfTheGatewayAndNotAtItsDate(t *testing.T) {
	normal := model.NtpQueryLogEntryAnswerNormal
	date := ntpBase.Add(10 * time.Second)
	now := date.Add(700 * time.Millisecond)
	before := ntpEntryAt(date.Add(200*time.Millisecond), normal) // after the Date, before the command
	g := &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		if n == 1 {
			ntpPageNow(w, httpDate(date), now, before)
			return false
		}
		ntpPageNow(w, httpDate(date), now.Add(time.Second), before, ntpEntryAt(now.Add(300*time.Millisecond), normal))
		return false
	}}
	code, out, errb := g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "5s")
	if code != 0 || g.polls != 2 || !strings.Contains(out, "asked the NTP server") {
		t.Fatalf("%d, %d polls: %q %q", code, g.polls, out, errb)
	}
	if g.since[1] != now.Format(time.RFC3339Nano) {
		t.Errorf("the second poll asks from: %q", g.since)
	}
}

// An answer without a Date: the newest time of the log is the start and the entry that gives it does not count.
func TestWaitNtpWithoutADateStartsAtTheNewestTimeOfTheLog(t *testing.T) {
	normal := model.NtpQueryLogEntryAnswerNormal
	g := &ntpGateway{t: t, reply: func(n int, w http.ResponseWriter, _ *http.Request) bool {
		if n == 1 { // the newest arrival is not the newest time
			ntpPage(w, "", ntpEntryAt(ntpBase.Add(-time.Hour), normal), ntpEntryAt(ntpBase, normal))
			return false
		}
		ntpPage(w, "", ntpEntryAt(ntpBase, normal), ntpEntryAt(ntpBase.Add(time.Second), normal))
		return false
	}}
	code, out, errb := g.run(nil, nil, "wait", "ntp", "esp32-42", "--timeout", "5s")
	if code != 0 || !strings.Contains(out, "asked the NTP server") || g.polls != 2 {
		t.Errorf("%d %d polls %q %q", code, g.polls, out, errb)
	}
	if g.since[1] != ntpBase.Add(time.Nanosecond).Format(time.RFC3339Nano) {
		t.Errorf("the second poll asks from: %q", g.since)
	}
}

// What the wait says about the query: the answer, the time served, how far it is from the correct time, the fault and the
// server asked.
func TestDescribeNTPQuery(t *testing.T) {
	served := time.Date(2038, 1, 19, 3, 14, 8, 0, time.UTC)
	off := int64(-1500)
	fault := uuid.MustParse("00000000-0000-4000-8000-0000000000aa")
	server := "203.0.113.7:123"
	kiss := model.NtpQueryLogEntryAnswerKissOfDeath
	for _, tc := range []struct {
		name string
		e    model.NtpQueryLogEntry
		want string
	}{
		{"nothing known", model.NtpQueryLogEntry{}, "answer unknown"},
		{"silence", model.NtpQueryLogEntry{Answer: ptr(model.NtpQueryLogEntryAnswerSilence), Server: &server}, "answer silence, server 203.0.113.7:123"},
		{"everything", model.NtpQueryLogEntry{Answer: &kiss, ServedTime: &served, OffsetMs: &off, Fault: &fault, Server: &server},
			"answer kiss-of-death, served 2038-01-19T03:14:08Z, offset -1500ms, fault 00000000-0000-4000-8000-0000000000aa, server 203.0.113.7:123"},
		{"an empty server is not said", model.NtpQueryLogEntry{Answer: ptr(model.NtpQueryLogEntryAnswerNormal), Server: ptr("")}, "answer normal"},
	} {
		if got := describeNTPQuery(&tc.e); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }
