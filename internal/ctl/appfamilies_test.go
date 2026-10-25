package ctl

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// The flags of `chaosctl dns set`, `tls set` and `dhcp set` (M23b) and the bodies they become; the commands against the
// real API are in internal/api/ctl_appfamilies_test.go.

func TestTheNameOfAnActionIsTheAPIsWhateverTheSpelling(t *testing.T) {
	for in, want := range map[string]string{"nxdomain": "nxdomain", "wrong-answer": "wrong_answer", "Wrong_Answer": "wrong_answer", " short-TTL ": "short_ttl", "NOT-YET-VALID": "not_yet_valid"} {
		if got := actionName(in); got != want {
			t.Errorf("actionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTheFlagsOfADNSFaultBecomeItsBody(t *testing.T) {
	body := func(action string, args ...string) (map[string]any, error) {
		var d dnsFlags
		fs := pflag.NewFlagSet("dns", pflag.ContinueOnError)
		d.add(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return d.body(action, fs)
	}
	for _, tc := range []struct {
		action string
		args   []string
		want   map[string]any
	}{
		{"nxdomain", nil, map[string]any{"action": "nxdomain"}},
		{"servfail", []string{"--name", "a.example.com", "--name", "*.b.example.com"}, map[string]any{"action": "servfail", "names": []string{"a.example.com", "*.b.example.com"}}},
		{"timeout", []string{"--name", "a.example.com,b.example.com"}, map[string]any{"action": "timeout", "names": []string{"a.example.com", "b.example.com"}}},
		{"delay", []string{"--delay", "2s"}, map[string]any{"action": "delay", "delay": "2s"}},
		{"wrong_answer", []string{"--answer", "10.9.0.1", "--answer", "10.9.0.2"}, map[string]any{"action": "wrong_answer", "answers": []string{"10.9.0.1", "10.9.0.2"}}},
		{"truncate", nil, map[string]any{"action": "truncate"}},
		{"short_ttl", []string{"--answer-ttl", "0s"}, map[string]any{"action": "short_ttl", "ttl": "0s"}},
	} {
		got, err := body(tc.action, tc.args...)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: %v, %v; want %v", tc.action, tc.args, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		action string
		args   []string
		want   string
	}{
		{"bogus", nil, "is no DNS action"},
		{"delay", nil, "needs --delay"},
		{"wrong_answer", nil, "needs --answer"},
		{"short_ttl", nil, "needs --answer-ttl"},
		{"nxdomain", []string{"--delay", "1s"}, "does not use --delay"},
		{"delay", []string{"--delay", "1s", "--answer-ttl", "5s"}, "does not use --answer-ttl"},
	} {
		if _, err := body(tc.action, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %v: %v, want %q", tc.action, tc.args, err, tc.want)
		}
	}
}

func TestTheFlagsOfATLSCaseBecomeItsBody(t *testing.T) {
	var x tlsFlags
	fs := pflag.NewFlagSet("tls", pflag.ContinueOnError)
	x.add(fs)
	if err := fs.Parse([]string{"--port", "8883", "--port", "443", "--sni", "broker.example.com", "--to", "203.0.113.0/24", "--cut-existing"}); err != nil {
		t.Fatal(err)
	}
	got, err := x.body("expired")
	want := map[string]any{"case": "expired", "ports": []int{8883, 443}, "sni": []string{"broker.example.com"}, "destination": map[string]any{"cidr": "203.0.113.0/24"}, "cut_existing": true}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%v, %v; want %v", got, err, want)
	}
	if got, err := (&tlsFlags{}).body("self_signed"); err != nil || !reflect.DeepEqual(got, map[string]any{"case": "self_signed"}) {
		t.Errorf("%v, %v", got, err)
	}
	for _, c := range tlsCases {
		if _, err := (&tlsFlags{}).body(c); err != nil {
			t.Errorf("%s: %v", c, err)
		}
	}
	if _, err := (&tlsFlags{}).body("intercept"); err == nil || !strings.Contains(err.Error(), "overlay create") {
		t.Errorf("the interception case has rules: %v", err)
	}
	if _, err := (&tlsFlags{}).body("bogus"); err == nil || !strings.Contains(err.Error(), "is no TLS case") {
		t.Errorf("%v", err)
	}
}

func TestTheFlagsOfADHCPActionBecomeItsBody(t *testing.T) {
	body := func(action string, args ...string) (map[string]any, error) {
		var d dhcpFlags
		fs := pflag.NewFlagSet("dhcp", pflag.ContinueOnError)
		d.add(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return d.body(action, fs)
	}
	for _, tc := range []struct {
		action string
		args   []string
		want   map[string]any
	}{
		{"short_lease", []string{"--lease-time", "7s"}, map[string]any{"action": "short_lease", "lease_time": "7s"}},
		{"delete_lease", nil, map[string]any{"action": "delete_lease"}},
		{"silence", nil, map[string]any{"action": "silence"}},
		{"force_new_ip", nil, map[string]any{"action": "force_new_ip"}},
		{"force_new_ip", []string{"--ip", "10.10.0.77"}, map[string]any{"action": "force_new_ip", "ip": "10.10.0.77"}},
		{"set_options", []string{"--router", "10.10.0.9"}, map[string]any{"action": "set_options", "options": map[string]any{"router": "10.10.0.9"}}},
		{"set_options", []string{"--no-domain"}, map[string]any{"action": "set_options", "options": map[string]any{"domain": ""}}},
		{"set_options", []string{"--domain", "lab.example", "--dns-server", "192.0.2.53", "--dns-server", "192.0.2.54", "--ntp-server", "192.0.2.123", "--option", "66=tftp.lab", "--option", "43=a=b"},
			map[string]any{"action": "set_options", "options": map[string]any{
				"domain": "lab.example", "dns_servers": []string{"192.0.2.53", "192.0.2.54"}, "ntp_servers": []string{"192.0.2.123"},
				"custom": []map[string]any{{"code": 66, "value": "tftp.lab"}, {"code": 43, "value": "a=b"}}}}},
	} {
		got, err := body(tc.action, tc.args...)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: %v, %v; want %v", tc.action, tc.args, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		action string
		args   []string
		want   string
	}{
		{"bogus", nil, "is no DHCP action"},
		{"short_lease", nil, "needs --lease-time"},
		{"set_options", nil, "needs one of --router"},
		{"set_options", []string{"--domain", "x", "--no-domain"}, "exclude each other"},
		{"set_options", []string{"--option", "66"}, "is not code=value"},
		{"set_options", []string{"--option", "x=y"}, "is not code=value"},
		{"set_options", []string{"--option", "66="}, "is not code=value"},
		{"silence", []string{"--ip", "10.10.0.1"}, "does not use --ip"},
		{"force_new_ip", []string{"--router", "10.10.0.1"}, "does not use --router"},
		{"short_lease", []string{"--lease-time", "7s", "--option", "66=x"}, "does not use --option"},
	} {
		if _, err := body(tc.action, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %v: %v, want %q", tc.action, tc.args, err, tc.want)
		}
	}
}
