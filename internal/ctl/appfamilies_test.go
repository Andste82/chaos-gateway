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

func TestTheFlagsOfATimeFaultBecomeItsBody(t *testing.T) {
	body := func(kind string, args ...string) (map[string]any, error) {
		var x timeFlags
		fs := pflag.NewFlagSet("time", pflag.ContinueOnError)
		x.add(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return x.body(kind, fs)
	}
	for _, tc := range []struct {
		kind string
		args []string
		want map[string]any
	}{
		{"offset", []string{"--offset", "+400d"}, map[string]any{"offset": "+400d"}},
		{"offset", []string{"--offset", "-90s", "--skew-ppm", "500", "--to", "time.google.com", "--port", "123,5123"},
			map[string]any{"offset": "-90s", "skew_ppm": 500.0, "destination": map[string]any{"hostname": "time.google.com"}, "ports": []int{123, 5123}}},
		// a wildcard hostname (the pool) and a port without a destination (every server, on that port)
		{"offset", []string{"--offset", "+1h", "--to", "*.pool.ntp.org"}, map[string]any{"offset": "+1h", "destination": map[string]any{"hostname": "*.pool.ntp.org"}}},
		{"unsynchronized", []string{"--port", "5123"}, map[string]any{"answer": "unsynchronized", "ports": []int{5123}}},
		{"absolute", []string{"--absolute", "1970-01-01T00:00:00Z"}, map[string]any{"absolute": "1970-01-01T00:00:00Z"}},
		{"skew", []string{"--skew-ppm", "-1000000"}, map[string]any{"skew_ppm": -1000000.0}},
		{"skew", []string{"--skew-ppm", "1000", "--offset", "+1y"}, map[string]any{"skew_ppm": 1000.0, "offset": "+1y"}},
		{"leap", []string{"--leap", "INSERT"}, map[string]any{"leap": "insert"}},
		{"leap", []string{"--leap", "delete", "--absolute", "2016-12-31T23:55:00Z"}, map[string]any{"leap": "delete", "absolute": "2016-12-31T23:55:00Z"}},
		{"unsynchronized", nil, map[string]any{"answer": "unsynchronized"}},
		{"silence", []string{"--to", "203.0.113.7"}, map[string]any{"answer": "silence", "destination": map[string]any{"cidr": "203.0.113.7/32"}}},
		{"kiss_of_death", []string{"--kiss-code", "deny"}, map[string]any{"answer": "kiss_of_death", "kiss_code": "DENY"}},
		{"invalid", []string{"--invalid", "origin-mismatch"}, map[string]any{"answer": "origin_mismatch"}},
		{"invalid", []string{"--invalid", "wrong_mode"}, map[string]any{"answer": "wrong_mode"}},
	} {
		got, err := body(tc.kind, tc.args...)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: %v, %v; want %v", tc.kind, tc.args, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		kind string
		args []string
		want string
	}{
		{"bogus", nil, "is no time fault"},
		{"offset", nil, "needs --offset"},
		{"absolute", []string{"--offset", "+1d"}, "needs --absolute"},
		{"skew", nil, "needs --skew-ppm"},
		{"leap", nil, "needs --leap"},
		{"kiss_of_death", nil, "needs --kiss-code"},
		{"invalid", nil, "needs --invalid"},
		{"offset", []string{"--offset", "+1d", "--absolute", "1970-01-01T00:00:00Z"}, "exclude each other"},
		{"leap", []string{"--leap", "add"}, "is insert or delete"},
		{"kiss_of_death", []string{"--kiss-code", "STOP"}, "is one of RATE, DENY, RSTR"},
		{"invalid", []string{"--invalid", "garbage"}, "is one of origin-mismatch, zero-transmit, wrong-mode"},
		{"silence", []string{"--offset", "+1d"}, "does not use --offset"},
		{"unsynchronized", []string{"--skew-ppm", "5"}, "does not use --skew-ppm"},
		{"kiss_of_death", []string{"--kiss-code", "RATE", "--leap", "insert"}, "does not use --leap"},
		{"offset", []string{"--offset", "+1d", "--kiss-code", "RATE"}, "does not use --kiss-code"},
	} {
		if _, err := body(tc.kind, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %v: %v, want %q", tc.kind, tc.args, err, tc.want)
		}
	}
	// every kind is one the API has, and the names are the ones the command line takes
	for _, k := range timeKinds {
		if k != actionName(k) {
			t.Errorf("%q is not in the normal form of an action name", k)
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

func TestADHCPActionWithoutATargetNamesTheTargetFlagsTheCommandHas(t *testing.T) {
	var tf targetFlags
	fs := pflag.NewFlagSet("dhcp", pflag.ContinueOnError)
	tf.addDeviceOrNetwork(fs)
	for _, args := range [][]string{nil, {"--device", "esp32-42", "--network", "IoT"}} {
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		_, err := tf.scope()
		if err == nil || !strings.Contains(err.Error(), "name exactly one target: --device or --network") || strings.Contains(err.Error(), "--group") || strings.Contains(err.Error(), "--global") {
			t.Errorf("%v: %v, want only --device and --network named", args, err)
		}
	}
	if fs.Lookup("group") != nil || fs.Lookup("global") != nil {
		t.Error("dhcp set has no --group or --global")
	}
	var all targetFlags
	if _, err := all.scope(); err == nil || !strings.Contains(err.Error(), "--device, --group, --network or --global") {
		t.Errorf("the other commands name all four targets: %v", err)
	}
}
