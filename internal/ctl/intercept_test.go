package ctl

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The flags of `chaosctl intercept set` (M23c) and the rules they become; the commands against the real API are in
// internal/api/ctl_intercept_test.go.

func TestARuleOfTheCommandLineBecomesAnHTTPRule(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want map[string]any
	}{
		{"block", map[string]any{"action": "block"}},
		{"abort", map[string]any{"action": "abort"}},
		{"status=503", map[string]any{"action": "status", "status": 503}},
		{"host=api.example.com;path=/v1/;method=GET;status=429", map[string]any{
			"action": "status", "status": 429, "match": map[string]any{"host": "api.example.com", "path_prefix": "/v1/", "method": "GET"}}},
		{" Host = *.example.com ; DELAY=2s ", map[string]any{"action": "delay", "delay": "2s", "match": map[string]any{"host": "*.example.com"}}},
		{"throttle=64kbit;host=cdn.example.net", map[string]any{"action": "throttle", "rate": "64kbit", "match": map[string]any{"host": "cdn.example.net"}}},
		{"request-header=X-Chaos: yes;remove-request-header=Cookie;response-header=Cache-Control:no-store;remove-response-header=Server;replace=ok=>fail;replace=a=>", map[string]any{
			"action": "modify",
			"modify": map[string]any{
				"request_headers":       map[string]string{"X-Chaos": "yes", "Cookie": ""},
				"response_headers":      map[string]string{"Cache-Control": "no-store", "Server": ""},
				"response_body_replace": []map[string]string{{"search": "ok", "replace": "fail"}, {"search": "a", "replace": ""}},
			}}},
		// the search text may hold an equals sign, and the replacement an arrow
		{"replace=a=b=>c=>d", map[string]any{"action": "modify", "modify": map[string]any{"response_body_replace": []map[string]string{{"search": "a=b", "replace": "c=>d"}}}}},
	} {
		got, err := parseHTTPRule(tc.spec)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %v, %v; want %v", tc.spec, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		spec, want string
	}{
		{"", "empty"},
		{" ; ", "empty"},
		{"host=api.example.com", "no action"},
		{"block;abort", "one action"},
		{"status=503;delay=1s", "one action"},
		{"block;request-header=A:b", "one action"},
		{"status=teapot", "not a status code"},
		{"status", "needs a value"},
		{"block=1", "takes no value"},
		{"host", "needs a value"},
		{"host=a.example.com;host=b.example.com;block", "given twice"},
		{"request-header=NoColon", "is not Name:Value"},
		{"request-header=:value", "needs a header name"},
		{"request-header=A:1;request-header=a:2", "named twice"},
		{"request-header=A:1;remove-request-header=A", "named twice"},
		{"replace=nothing", "SEARCH=>REPLACEMENT"},
		{"replace==>x", "SEARCH=>REPLACEMENT"},
		{"explode=now", "is not a field of a rule"},
	} {
		if _, err := parseHTTPRule(tc.spec); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.spec, err, tc.want)
		}
	}
}

func TestTheFlagsOfAnInterceptionCaseBecomeItsSettings(t *testing.T) {
	settings := func(args ...string) (map[string]any, error) {
		var i interceptFlags
		fs := pflag.NewFlagSet("intercept", pflag.ContinueOnError)
		i.add(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return i.settings()
	}
	// nothing asked, nothing sent: the defaults are the gateway's
	if got, err := settings(); got != nil || err != nil {
		t.Errorf("%v, %v", got, err)
	}
	got, err := settings("--no-keylog", "--allow-quic", "--upstream", "Own-CA", "--rule", "block", "--rule", "host=a.example.com;status=503")
	want := map[string]any{
		"keylog": false, "block_quic": false, "upstream": "own_ca",
		"http_rules": []map[string]any{{"action": "block"}, {"action": "status", "status": 503, "match": map[string]any{"host": "a.example.com"}}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%v, %v; want %v", got, err, want)
	}
	// the rules keep the order they were given in
	got, _ = settings("--rule", "delay=1s", "--rule", "block", "--rule", "abort")
	if rules := got["http_rules"].([]map[string]any); rules[0]["action"] != "delay" || rules[1]["action"] != "block" || rules[2]["action"] != "abort" {
		t.Errorf("%v", rules)
	}
	for args, want := range map[string]string{
		"--upstream=sometimes": "is not one of verify, own_ca, insecure",
		"--rule=":              "--rule 1",
		"--rule=block;abort":   "one action",
	} {
		if _, err := settings(args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
	// the number of the rule that is wrong is the one the user counts from 1
	if _, err := settings("--rule", "block", "--rule", "status=x"); err == nil || !strings.Contains(err.Error(), "--rule 2 (\"status=x\")") {
		t.Errorf("%v", err)
	}
}

func TestTheSelectionOfAnInterceptionCaseIsThatOfAnyTLSCase(t *testing.T) {
	var x tlsFlags
	fs := pflag.NewFlagSet("tls", pflag.ContinueOnError)
	x.add(fs)
	if err := fs.Parse([]string{"--port", "443", "--sni", "api.example.com", "--to", "uplink", "--cut-existing"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"ports": []int{443}, "sni": []string{"api.example.com"}, "destination": map[string]any{"uplink": true}, "cut_existing": true}
	if got := x.selection(); !reflect.DeepEqual(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
	if got := (&tlsFlags{}).selection(); len(got) != 0 {
		t.Errorf("%v", got)
	}
	// the TLS command says where the interception is made
	if _, err := (&tlsFlags{}).body("intercept"); err == nil || !strings.Contains(err.Error(), "chaosctl intercept set") {
		t.Errorf("%v", err)
	}
}

func TestAnExchangeIsShownAsWhatItAsked(t *testing.T) {
	s := func(v string) *string { return &v }
	for _, tc := range []struct {
		f    model.InterceptFlow
		want string
	}{
		{model.InterceptFlow{OriginalDst: "203.0.113.10:443", Method: s("GET"), Host: s("api.example.com"), Path: s("/v1/a")}, "GET api.example.com/v1/a"},
		{model.InterceptFlow{OriginalDst: "203.0.113.10:443", Method: s("GET"), Path: s("/v1/a")}, "GET 203.0.113.10:443/v1/a"},
		{model.InterceptFlow{OriginalDst: "203.0.113.10:8883", Host: s("broker.example.com")}, "broker.example.com"},
		{model.InterceptFlow{OriginalDst: "203.0.113.10:8883"}, "203.0.113.10:8883"},
	} {
		if got := flowRequest(tc.f); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.f, got, tc.want)
		}
	}
}

func TestSinceIsATimeOrHowLongAgo(t *testing.T) {
	a := &app{env: Env{Clock: clock.NewFake(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))}}
	for in, want := range map[string]time.Time{
		"2026-10-25T11:00:00Z":        time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC),
		"2026-10-25T11:00:00.5+02:00": time.Date(2026, 10, 25, 9, 0, 0, 500_000_000, time.UTC),
		"5m":                          time.Date(2026, 10, 25, 11, 55, 0, 0, time.UTC),
		"90s":                         time.Date(2026, 10, 25, 11, 58, 30, 0, time.UTC),
	} {
		got, err := a.sinceTime(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q: %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "yesterday", "-5m", "12:00"} {
		if _, err := a.sinceTime(in); err == nil || !strings.Contains(err.Error(), "--since") {
			t.Errorf("%q: %v", in, err)
		}
	}
}
