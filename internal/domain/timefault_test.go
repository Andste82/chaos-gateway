package domain

import (
	"math"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// rawRequest reads an overlay request without the schema, so that a test can hand the domain a value the schema would
// already refuse: the domain checks everything it is given (a request can be built in code, and a stored profile was
// written by an older schema).
func rawRequest(t *testing.T, body string) *model.OverlayRequest {
	t.Helper()
	doc, err := ParseDocument([]byte(body), FormatYAML)
	if err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	var req model.OverlayRequest
	if err := decodeInto(doc, &req); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return &req
}

func validateRaw(t *testing.T, body string) ValidationErrors {
	t.Helper()
	_, errs := ValidateOverlay(exampleConfiguration(t), rawRequest(t, body))
	return ValidationErrors(errs)
}

func TestParseTimeOffset(t *testing.T) {
	const day = 24 * time.Hour
	valid := []struct {
		in   string
		want time.Duration
	}{
		{"+400d", 400 * day},
		{"400d", 400 * day},
		{"-90s", -90 * time.Second},
		{"+0s", 0},
		{"-0d", 0},
		{"250ms", 250 * time.Millisecond},
		{"1m", time.Minute}, // m is minutes, ms milliseconds
		{"5m30s", 5*time.Minute + 30*time.Second},
		{"2h", 2 * time.Hour},
		{"3w", 21 * day},
		{"1y", 365 * day},
		{"+1y", 365 * day},
		{"-1y2d", -(367 * day)}, // the sign is the whole span's
		{"1d12h", 36 * time.Hour},
		{"1.5h", 90 * time.Minute},
		{"0.5s", 500 * time.Millisecond},
		{"0.0000000005s", 0}, // half a nanosecond is cut off
		{"-0.0000000015s", -time.Nanosecond},
		{"1s1ms1s", 2*time.Second + time.Millisecond}, // a unit may come twice
		{"292y", 292 * 365 * day},                     // just inside the range of a Duration
		{"-292y", -292 * 365 * day},
		{"9223372036854.775807ms", time.Duration(math.MaxInt64)}, // the limits are symmetric
		{"-9223372036854.775807ms", -time.Duration(math.MaxInt64)},
	}
	for _, tt := range valid {
		got, err := ParseTimeOffset(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("%q: %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	invalid := []string{
		"", "+", "-", "400", "d", "+d", "400 d", " 400d", "400d ", "400D", "1.d", ".5s", "1,5s", "+-1s", "--1s",
		"400days", "1ns", "1us", "1mo", "1s+1s", "1d-2h", "1e3s", "0x10s", "Inf", "NaN",
		"293y", "-293y", "99999999999999999999s", // beyond the range of a Duration, about 292 years
		"9223372036854.775808ms", "-9223372036854.775808ms", // math.MinInt64 cannot be negated
	}
	// the exact arithmetic costs seconds on a megabyte: the length is refused before anything is parsed
	invalid = append(invalid, "+"+strings.Repeat("1", 100_000)+"s", strings.Repeat("1s", MaxTimeOffsetLen/2+1))
	for _, in := range invalid {
		if got, err := ParseTimeOffset(in); err == nil {
			t.Errorf("%q must be refused, got %v", in, got)
		}
	}
}

// The offset of the examples and of the plan (§2.18: "+400d", "-90s", "s ... years").
func TestTheOffsetsOfThePlanParse(t *testing.T) {
	for in, want := range map[string]time.Duration{"+400d": 400 * 24 * time.Hour, "-90s": -90 * time.Second, "+1y": 365 * 24 * time.Hour, "-1d": -24 * time.Hour} {
		if got, err := ParseTimeOffset(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
}

func TestATimeFaultOfEveryKindIsValid(t *testing.T) {
	for name, body := range map[string]string{
		"an offset":                                   `{target: {device: esp32-42}, time: {offset: +400d}}`,
		"a negative offset":                           `{target: {device: esp32-42}, time: {offset: -90s}}`,
		"an offset of zero":                           `{target: {device: esp32-42}, time: {offset: +0s}}`,
		"an absolute time":                            `{target: {device: esp32-42}, time: {absolute: '1970-01-01T00:00:00Z'}}`,
		"an absolute time in 2038":                    `{target: {device: esp32-42}, time: {absolute: '2038-01-19T03:09:07Z'}}`,
		"the first NTP second":                        `{target: {device: esp32-42}, time: {absolute: '1900-01-01T00:00:00Z'}}`,
		"an absolute time with an offset of the zone": `{target: {device: esp32-42}, time: {absolute: '2036-02-07T07:28:16+01:00'}}`,
		"a skew alone":                                `{target: {device: esp32-42}, time: {skew_ppm: 500}}`,
		"a skew of zero":                              `{target: {device: esp32-42}, time: {skew_ppm: 0}}`,
		"a negative skew":                             `{target: {device: esp32-42}, time: {skew_ppm: -250.5}}`,
		"a clock that stands still":                   `{target: {device: esp32-42}, time: {skew_ppm: -1000000}}`,
		"the fastest clock":                           `{target: {device: esp32-42}, time: {skew_ppm: 1000000}}`,
		"an offset with a skew":                       `{target: {device: esp32-42}, time: {offset: -1d, skew_ppm: 100}}`,
		"an absolute time with a skew":                `{target: {device: esp32-42}, time: {absolute: '2026-01-01T00:00:00Z', skew_ppm: 100}}`,
		"a leap insert alone":                         `{target: {device: esp32-42}, time: {leap: insert}}`,
		"a leap delete":                               `{target: {device: esp32-42}, time: {leap: delete}}`,
		"a leap before midnight":                      `{target: {device: esp32-42}, time: {absolute: '2026-12-31T23:55:00Z', leap: insert}}`,
		"everything of the time":                      `{target: {device: esp32-42}, time: {offset: +1h, skew_ppm: 5, leap: delete, answer: normal}}`,
		"unsynchronized":                              `{target: {device: esp32-42}, time: {answer: unsynchronized}}`,
		"kiss of death RATE":                          `{target: {device: esp32-42}, time: {answer: kiss_of_death, kiss_code: RATE}}`,
		"kiss of death DENY":                          `{target: {device: esp32-42}, time: {answer: kiss_of_death, kiss_code: DENY}}`,
		"kiss of death RSTR":                          `{target: {device: esp32-42}, time: {answer: kiss_of_death, kiss_code: RSTR}}`,
		"silence":                                     `{target: {device: esp32-42}, time: {answer: silence}}`,
		"an origin mismatch":                          `{target: {device: esp32-42}, time: {answer: origin_mismatch}}`,
		"a zero transmit timestamp":                   `{target: {device: esp32-42}, time: {answer: zero_transmit}}`,
		"a wrong mode":                                `{target: {device: esp32-42}, time: {answer: wrong_mode}}`,
		"an address":                                  `{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.7}, offset: +1d}}`,
		"an address as /32":                           `{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.7/32}, offset: +1d}}`,
		"a prefix":                                    `{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.0/24}, offset: +1d}}`,
		"a host name":                                 `{target: {device: esp32-42}, time: {destination: {hostname: time.google.com}, offset: +1d}}`,
		"a wildcard host name":                        `{target: {device: esp32-42}, time: {destination: {hostname: '*.pool.ntp.org'}, offset: +1d}}`,
		"udp":                                         `{target: {device: esp32-42}, time: {protocol: udp, offset: +1d}}`,
		"the protocol any":                            `{target: {device: esp32-42}, time: {protocol: any, offset: +1d}}`,
		"another port":                                `{target: {device: esp32-42}, time: {ports: [12345], offset: +1d}}`,
		"a port range":                                `{target: {device: esp32-42}, time: {port_ranges: [{from: 120, to: 130}], offset: +1d}}`,
		"udp and the port":                            `{target: {device: esp32-42}, time: {protocol: udp, ports: [123], offset: +1d}}`,
		"a group":                                     `{target: {group: sensors}, time: {offset: +1d}}`,
		"a network":                                   `{target: {network: IoT}, time: {offset: +1d}}`,
		"a silent server for a name":                  `{target: {network: IoT}, time: {destination: {hostname: pool.ntp.org}, answer: silence}}`,
		"a ttl and a lease":                           `{target: {device: esp32-42}, time: {offset: +1d}, ttl: 10m, lease: 1m}`,
	} {
		t.Run(name, func(t *testing.T) {
			if errs := validateRaw(t, body); len(errs) != 0 {
				t.Fatalf("%s:\n%s", body, dump(errs))
			}
		})
	}
}

func TestATimeFaultThatIsNotValid(t *testing.T) {
	const (
		dev = `target: {device: esp32-42}, `
	)
	tests := []struct {
		name, body, path, code string
	}{
		// it must change something
		{"no effect at all", `{` + dev + `time: {}}`, "/time", ""},
		{"only a destination", `{` + dev + `time: {destination: {hostname: time.google.com}}}`, "/time", ""},
		{"only the answer normal", `{` + dev + `time: {answer: normal}}`, "/time", ""},
		{"only ports", `{` + dev + `time: {ports: [123]}}`, "/time", ""},
		// offset against absolute
		{"offset with absolute", `{` + dev + `time: {offset: +1d, absolute: '1970-01-01T00:00:00Z'}}`, "/time/absolute", ""},
		// an answer is the whole fault
		{"silence with an offset", `{` + dev + `time: {answer: silence, offset: +1d}}`, "/time/offset", ""},
		{"unsynchronized with an absolute time", `{` + dev + `time: {answer: unsynchronized, absolute: '1970-01-01T00:00:00Z'}}`, "/time/absolute", ""},
		{"kiss of death with a skew", `{` + dev + `time: {answer: kiss_of_death, kiss_code: RATE, skew_ppm: 10}}`, "/time/skew_ppm", ""},
		{"a wrong mode with a leap", `{` + dev + `time: {answer: wrong_mode, leap: insert}}`, "/time/leap", ""},
		{"an origin mismatch with an offset", `{` + dev + `time: {answer: origin_mismatch, offset: -1d}}`, "/time/offset", ""},
		{"a zero transmit with an offset", `{` + dev + `time: {answer: zero_transmit, offset: -1d}}`, "/time/offset", ""},
		{"an answer that does not exist", `{` + dev + `time: {answer: sleepy}}`, "/time/answer", ""},
		// the kiss code
		{"kiss of death without a code", `{` + dev + `time: {answer: kiss_of_death}}`, "/time/kiss_code", ""},
		{"a code with another answer", `{` + dev + `time: {answer: silence, kiss_code: RATE}}`, "/time/kiss_code", ""},
		{"a code without an answer", `{` + dev + `time: {offset: +1d, kiss_code: RATE}}`, "/time/kiss_code", ""},
		{"a code with the answer normal", `{` + dev + `time: {answer: normal, offset: +1d, kiss_code: DENY}}`, "/time/kiss_code", ""},
		{"a kiss code that does not exist", `{` + dev + `time: {answer: kiss_of_death, kiss_code: STEP}}`, "/time/kiss_code", ""},
		{"a kiss code in lower case", `{` + dev + `time: {answer: kiss_of_death, kiss_code: rate}}`, "/time/kiss_code", ""},
		// the offset
		{"an offset that is no span", `{` + dev + `time: {offset: soon}}`, "/time/offset", ""},
		{"an offset without a unit", `{` + dev + `time: {offset: '400'}}`, "/time/offset", ""},
		{"an offset with an unknown unit", `{` + dev + `time: {offset: +4 fortnights}}`, "/time/offset", ""},
		{"an offset in nanoseconds", `{` + dev + `time: {offset: +4ns}}`, "/time/offset", ""},
		{"an offset of 300 years", `{` + dev + `time: {offset: +300y}}`, "/time/offset", ""},
		{"an offset of minus 300 years", `{` + dev + `time: {offset: -300y}}`, "/time/offset", ""},
		{"an empty offset", `{` + dev + `time: {offset: ''}}`, "/time/offset", ""},
		// the absolute time
		{"an absolute time before the first NTP second", `{` + dev + `time: {absolute: '1899-12-31T23:59:59Z'}}`, "/time/absolute", ""},
		{"an absolute time in the year 1", `{` + dev + `time: {absolute: '0001-01-01T00:00:00Z'}}`, "/time/absolute", ""},
		{"an absolute time before 1900 in another zone", `{` + dev + `time: {absolute: '1900-01-01T00:30:00+01:00'}}`, "/time/absolute", ""},
		// the skew and the leap
		{"a skew beyond a million", `{` + dev + `time: {skew_ppm: 1000001}}`, "/time/skew_ppm", ""},
		{"a skew below minus a million", `{` + dev + `time: {skew_ppm: -1000001}}`, "/time/skew_ppm", ""},
		{"a leap that does not exist", `{` + dev + `time: {leap: smear}}`, "/time/leap", ""},
		// the destination selector
		{"tcp", `{` + dev + `time: {protocol: tcp, offset: +1d}}`, "/time/protocol", ""},
		{"icmp", `{` + dev + `time: {protocol: icmp, offset: +1d}}`, "/time/protocol", ""},
		{"a network as the server asked", `{` + dev + `time: {destination: {network: IoT}, offset: +1d}}`, "/time/destination", ""},
		{"the uplink as the server asked", `{` + dev + `time: {destination: {uplink: true}, offset: +1d}}`, "/time/destination", ""},
		{"a destination that is no address", `{` + dev + `time: {destination: {cidr: not-an-ip}, offset: +1d}}`, "/time/destination/cidr", CodeInvalidAddress},
		{"a prefix with host bits", `{` + dev + `time: {destination: {cidr: 203.0.113.7/24}, offset: +1d}}`, "/time/destination/cidr", CodeHostBitsSet},
		// the ports of the services the gateway runs for the device are not the NTP server's to take
		{"the DNS port", `{` + dev + `time: {ports: [53], offset: +1d}}`, "/time/ports", ""},
		{"the DHCP port", `{` + dev + `time: {ports: [123, 67], offset: +1d}}`, "/time/ports", ""},
		{"the DHCP client port", `{` + dev + `time: {ports: [68], offset: +1d}}`, "/time/ports", ""},
		{"a range of the DHCP client port", `{` + dev + `time: {port_ranges: [{from: 68, to: 68}], offset: +1d}}`, "/time/port_ranges", ""},
		{"a range over the DHCP ports", `{` + dev + `time: {port_ranges: [{from: 60, to: 70}], offset: +1d}}`, "/time/port_ranges", ""},
		{"every port", `{` + dev + `time: {port_ranges: [{from: 1, to: 65535}], offset: +1d}}`, "/time/port_ranges", ""},
		{"an offset of 66 characters", `{` + dev + `time: {offset: '` + strings.Repeat("1s", 33) + `'}}`, "/time/offset", ""},
		{"a port range that ends before it starts", `{` + dev + `time: {port_ranges: [{from: 130, to: 120}], offset: +1d}}`, "/time/port_ranges/0", CodeInvalidPortRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// the rules of every selector (an address, a port range) keep their own codes
			code := tt.code
			if code == "" {
				code = CodeInvalidTimeFault
			}
			wantError(t, validateRaw(t, tt.body), tt.path, code)
		})
	}
}

// Every rule of a time fault is reported together, not one at a time.
func TestTheProblemsOfATimeFaultAreReportedTogether(t *testing.T) {
	errs := validateRaw(t, `{target: {device: esp32-42}, time: {answer: silence, offset: soon, absolute: '1800-01-01T00:00:00Z', skew_ppm: 2000000, leap: insert, protocol: tcp, destination: {uplink: true}}}`)
	for _, path := range []string{"/time/offset", "/time/absolute", "/time/skew_ppm", "/time/leap", "/time/protocol", "/time/destination"} {
		wantError(t, errs, path, CodeInvalidTimeFault)
	}
}

func TestATimeFaultIsAimedAtADeviceAGroupOrANetwork(t *testing.T) {
	for _, tt := range []struct {
		target string
		ok     bool
	}{
		{`{device: esp32-42}`, true},
		{`{group: sensors}`, true},
		{`{network: IoT}`, true},
		{`{global: true}`, false},
		{`{remote_network: {client: lab-rA}}`, false},
		{`{remote_network: {link: site-b}}`, false},
	} {
		errs := validateRaw(t, `{target: `+tt.target+`, time: {offset: +1d}}`)
		if tt.ok && len(errs) != 0 {
			t.Errorf("%s: %s", tt.target, dump(errs))
		}
		if !tt.ok {
			if !errs.Has("/target", CodeTargetKind) {
				t.Errorf("%s: want %s at /target, got:\n%s", tt.target, CodeTargetKind, dump(errs))
			}
			if msg := errs[0].Message; !strings.Contains(msg, "time fault") {
				t.Errorf("%s: the message does not say what is wrong: %s", tt.target, msg)
			}
		}
	}
	// and without a target there is nothing to aim at
	wantError(t, validateRaw(t, `{time: {offset: +1d}}`), "/target", CodeMissingField)
	// a time fault is not one of the kinds that have no target
	wantError(t, validateRaw(t, `{target: {device: ghost}, time: {offset: +1d}}`), "/target/device", CodeUnknownReference)
}

func TestTheKindOfAnOverlayIsTimeWhenItHasATimeFault(t *testing.T) {
	got, err := OverlayKindOf(requestOf(t, `{target: {device: esp32-42}, time: {offset: +1d}}`))
	if err != nil || got != "time" {
		t.Errorf("kind %q, err %v", got, err)
	}
	// exactly one kind: a time fault next to another body is refused
	for _, body := range []string{
		`{target: {device: esp32-42}, time: {offset: +1d}, fault: {blackout: true}}`,
		`{target: {device: esp32-42}, time: {offset: +1d}, dns: {action: servfail}}`,
		`{target: {device: esp32-42}, time: {offset: +1d}, profile: lte}`,
	} {
		if _, err := OverlayKindOf(rawRequest(t, body)); err == nil {
			t.Errorf("%s: two kinds", body)
		}
		wantError(t, validateRaw(t, body), "", CodeInvalidOverlay)
	}
	if _, err := OverlayKindOf(&model.OverlayRequest{}); err == nil || !strings.Contains(err.Error(), "time") {
		t.Errorf("the message names the kinds: %v", err)
	}
}

func TestTheKeyOfATimeFaultIsItsDestinationSelector(t *testing.T) {
	key := func(body string) string {
		t.Helper()
		out, errs := validateOverlay(t, body)
		if len(errs) != 0 {
			t.Fatalf("%s: %v", body, errs)
		}
		k, err := OverlayKey(admin, out)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	const dev = `target: {device: esp32-42}, `
	same := [][2]string{
		// the effect is not part of the key: writing another effect replaces the fault
		{`{` + dev + `time: {offset: +1d}}`, `{` + dev + `time: {answer: silence}}`},
		// no selector is the NTP port over UDP, written out or not
		{`{` + dev + `time: {offset: +1d}}`, `{` + dev + `time: {protocol: udp, ports: [123], offset: -1d}}`},
		{`{` + dev + `time: {offset: +1d}}`, `{` + dev + `time: {ports: [123], skew_ppm: 5}}`},
		// a server by its address or by its address as /32
		{`{` + dev + `time: {destination: {cidr: 203.0.113.7}, offset: +1d}}`, `{` + dev + `time: {destination: {cidr: 203.0.113.7/32}, leap: insert}}`},
		// a name in any case
		{`{` + dev + `time: {destination: {hostname: Time.Google.com}, offset: +1d}}`, `{` + dev + `time: {destination: {hostname: time.google.com}, offset: -1d}}`},
		// ports in another order, with a duplicate
		{`{` + dev + `time: {ports: [123, 12300], offset: +1d}}`, `{` + dev + `time: {ports: [12300, 123, 12300], offset: +2d}}`},
		// the device by name or by UUID
		{`{` + dev + `time: {offset: +1d}}`, `{target: {device: ` + idESP + `}, time: {offset: +1d}}`},
	}
	for i, pair := range same {
		if a, b := key(pair[0]), key(pair[1]); a != b {
			t.Errorf("pair %d: the keys must be equal\n  %s\n  %s", i, a, b)
		}
	}
	different := [][2]string{
		{`{` + dev + `time: {offset: +1d}}`, `{target: {group: sensors}, time: {offset: +1d}}`},
		{`{` + dev + `time: {offset: +1d}}`, `{target: {network: IoT}, time: {offset: +1d}}`},
		{`{` + dev + `time: {offset: +1d}}`, `{target: {device: lab-host}, time: {offset: +1d}}`},
		{`{` + dev + `time: {destination: {cidr: 203.0.113.7}, offset: +1d}}`, `{` + dev + `time: {destination: {cidr: 203.0.113.8}, offset: +1d}}`},
		{`{` + dev + `time: {destination: {hostname: a.test}, offset: +1d}}`, `{` + dev + `time: {destination: {hostname: b.test}, offset: +1d}}`},
		{`{` + dev + `time: {offset: +1d}}`, `{` + dev + `time: {destination: {hostname: a.test}, offset: +1d}}`},
		{`{` + dev + `time: {offset: +1d}}`, `{` + dev + `time: {ports: [12345], offset: +1d}}`},
		{`{` + dev + `time: {offset: +1d}}`, `{` + dev + `time: {port_ranges: [{from: 100, to: 200}], offset: +1d}}`},
		// a time fault is not another fault of the same scope
		{`{` + dev + `time: {offset: +1d}}`, `{` + dev + `dns: {action: servfail}}`},
		{`{` + dev + `time: {answer: silence}}`, `{` + dev + `fault: {protocol: udp, ports: [123], blackout: true}}`},
		{`{` + dev + `time: {answer: silence}}`, `{` + dev + `rule: {action: drop, protocol: udp, ports: [123]}}`},
	}
	for i, pair := range different {
		if a, b := key(pair[0]), key(pair[1]); a == b {
			t.Errorf("pair %d: the keys must differ: %s", i, a)
		}
	}
	// and it depends on the owner like the key of every kind
	out, _ := validateOverlay(t, `{`+dev+`time: {offset: +1d}}`)
	a, _ := OverlayKey(admin, out)
	b, _ := OverlayKey(model.Owner{Type: "run", Id: "7"}, out)
	if a == b || !strings.Contains(a, "|time|") {
		t.Errorf("owner and kind are in the key: %s %s", a, b)
	}
}

func TestATimeOverlayKeepsItsBodyThroughNewOverlayAndRequestOf(t *testing.T) {
	out, errs := validateOverlay(t, `{target: {device: esp32-42}, time: {destination: {hostname: time.google.com}, offset: +400d}, ttl: 10m}`)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	o, err := NewOverlay(*out, admin, mustUUID("00000000-0000-4000-8000-000000000001"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != model.OverlayKindTime || o.Time == nil || *o.Time.Offset != "+400d" || *o.Time.Destination.Hostname != "time.google.com" {
		t.Fatalf("overlay %+v", o)
	}
	if got := RequestOf(&o); got.Time == nil || *got.Time.Offset != "+400d" || *got.Target.Device != idESP || got.Fault != nil {
		t.Errorf("request %+v", got)
	}
	// the overlay of the request is a copy
	if c := CloneOverlayRequest(out); c.Time == out.Time || *c.Time.Offset != "+400d" {
		t.Error("the clone shares the body")
	}
}

// The schema is the first wall: the domain gets what it lets through. These are the rules of the schema a client sees.
func TestTheSchemaRefusesATimeFaultThatIsNotWellFormed(t *testing.T) {
	for name, body := range map[string]string{
		"an offset that is no span":       `{target: {device: esp32-42}, time: {offset: soon}}`,
		"an offset in a number":           `{target: {device: esp32-42}, time: {offset: 400}}`,
		"an absolute time without a zone": `{target: {device: esp32-42}, time: {absolute: '2026-01-01T00:00:00'}}`,
		"an absolute time that is a date": `{target: {device: esp32-42}, time: {absolute: '2026-01-01'}}`,
		"a skew beyond the range":         `{target: {device: esp32-42}, time: {skew_ppm: 2000000}}`,
		"a skew as text":                  `{target: {device: esp32-42}, time: {skew_ppm: '500'}}`,
		"an answer that does not exist":   `{target: {device: esp32-42}, time: {answer: sleepy}}`,
		"a leap that does not exist":      `{target: {device: esp32-42}, time: {leap: smear}}`,
		"a kiss code that does not exist": `{target: {device: esp32-42}, time: {answer: kiss_of_death, kiss_code: STEP}}`,
		"a field that does not exist":     `{target: {device: esp32-42}, time: {offset: +1d, jitter: 5ms}}`,
		"a time that is no object":        `{target: {device: esp32-42}, time: +1d}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeOverlayRequest([]byte(body), FormatYAML); err == nil {
				t.Fatalf("%s must be refused by the schema", body)
			}
		})
	}
	// and what is well formed passes it, in the forms a client sends
	for _, body := range []string{
		`{target: {device: esp32-42}, time: {offset: +400d}}`,
		`{target: {device: esp32-42}, time: {offset: -1y2d}}`,
		`{target: {device: esp32-42}, time: {absolute: '2038-01-19T03:09:07+00:00', skew_ppm: -1000000}}`,
		`{target: {device: esp32-42}, time: {answer: kiss_of_death, kiss_code: DENY}}`,
	} {
		if _, err := DecodeOverlayRequest([]byte(body), FormatYAML); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
}

// ---- resolution -----------------------------------------------------------------------------

// toNTP is the query of the NTP server for a device asking a server: UDP to the NTP port.
func toNTP(dest string, names ...string) Query {
	return Query{Source: subjectA, DestIP: netip.MustParseAddr(dest), DestNames: names, Protocol: "udp", Port: 123}
}

func ntpWinner(t *testing.T, tw *testWorld, q Query) Candidate {
	t.Helper()
	return mustWinner(t, tw.world().Resolve(q), FamilyTime)
}

func TestTimeFaultsOfADeviceBeatThoseOfItsGroupAndOfItsNetwork(t *testing.T) {
	tw := newTestWorld(t, false)
	net := tw.overlay(`{target: {network: IoT}, time: {offset: +1d}}`, 3*time.Second)
	grp := tw.overlay(`{target: {group: sensors}, time: {offset: +2d}}`, 2*time.Second)
	dev := tw.overlay(`{target: {device: esp32-42}, time: {offset: +3d}}`, time.Second) // the oldest of the three
	res := tw.world().Resolve(toNTP("203.0.113.7"))
	win := mustWinner(t, res, FamilyTime)
	if win.ID != dev.Id.String() || win.Level != 3 || *win.Time.Offset != "+3d" || win.Family != FamilyTime {
		t.Fatalf("winner = %+v", win)
	}
	r := familyResult(t, res, FamilyTime)
	if len(r.Overridden) != 2 || r.Overridden[0].ID != grp.Id.String() || r.Overridden[1].ID != net.Id.String() {
		t.Fatalf("overridden = %+v", r.Overridden)
	}
	if got := overriddenReason(t, r, grp.Id.String()); got != "level 3 beats level 5" {
		t.Errorf("group: %s", got)
	}
	if got := overriddenReason(t, r, net.Id.String()); got != "level 3 beats level 7" {
		t.Errorf("network: %s", got)
	}
	// a device of the group that has no fault of its own gets the group's, then the network's
	other := toNTP("203.0.113.7")
	other.Source = Subject{IP: netip.MustParseAddr("10.10.0.77")}
	if w := ntpWinner(t, tw, other); w.ID != net.Id.String() {
		t.Fatalf("another device of the network: winner = %s", w.ID)
	}
}

func TestTheGroupBeatsTheNetworkAndTheNewerOfTwoWins(t *testing.T) {
	tw := newTestWorld(t, false)
	net := tw.overlay(`{target: {network: IoT}, time: {offset: +1d}}`, 9*time.Second) // the newest, but the widest scope
	grp := tw.overlay(`{target: {group: sensors}, time: {offset: +2d}}`, time.Second)
	if w := ntpWinner(t, tw, toNTP("203.0.113.7")); w.ID != grp.Id.String() || w.Level != 5 {
		t.Fatalf("the group must win over the newer network: %+v", w)
	}
	// two groups with the same device: the newer wins at the same level
	g2 := tw.overlay(`{target: {group: g2}, time: {offset: +4d}}`, 2*time.Second)
	res := tw.world().Resolve(toNTP("203.0.113.7"))
	if w := mustWinner(t, res, FamilyTime); w.ID != g2.Id.String() {
		t.Fatalf("the newer group must win: %+v", w)
	}
	if got := overriddenReason(t, familyResult(t, res, FamilyTime), grp.Id.String()); got != "newer at the same level" {
		t.Errorf("reason = %s", got)
	}
	_ = net
}

func TestOfTwoTimeFaultsOfTheSameScopeTheNewerWins(t *testing.T) {
	tw := newTestWorld(t, false)
	old := tw.overlay(`{target: {device: esp32-42}, time: {offset: +1d}}`, time.Second)
	newer := tw.overlay(`{target: {device: esp32-42}, time: {offset: +2d, skew_ppm: 5}}`, time.Minute)
	// writing a fault with the same key replaces it in the store, so the resolution sees different selectors here
	tw.overlays[0].Time.Ports = &[]int{123}
	res := tw.world().Resolve(toNTP("203.0.113.7"))
	win := mustWinner(t, res, FamilyTime)
	if win.ID != newer.Id.String() || *win.Time.Offset != "+2d" {
		t.Fatalf("winner = %+v", win)
	}
	if got := overriddenReason(t, familyResult(t, res, FamilyTime), old.Id.String()); got != "newer at the same level" {
		t.Errorf("reason = %s", got)
	}
	// at the very same time the higher id wins, so the winner does not depend on the order of the overlays
	tw.overlays[0].UpdatedAt, tw.overlays[1].UpdatedAt = t0, t0
	if w := ntpWinner(t, tw, toNTP("203.0.113.7")); w.ID != newer.Id.String() {
		t.Fatalf("a tie goes to the higher id: %+v", w)
	}
	tw.overlays[0], tw.overlays[1] = tw.overlays[1], tw.overlays[0]
	if w := ntpWinner(t, tw, toNTP("203.0.113.7")); w.ID != newer.Id.String() {
		t.Fatalf("the winner depends on the order: %+v", w)
	}
}

func TestATimeFaultForOneServerBeatsAFaultOfTheSameScopeForEveryServer(t *testing.T) {
	tw := newTestWorld(t, false)
	for _, tt := range []struct{ name, dest, host string }{{"an address", `{cidr: 203.0.113.7}`, ""}, {"a prefix", `{cidr: 203.0.113.0/24}`, ""}, {"a name", `{hostname: time.google.com}`, "time.google.com"}} {
		t.Run(tt.name, func(t *testing.T) {
			tw := newTestWorld(t, false)
			narrow := tw.overlay(`{target: {device: esp32-42}, time: {destination: `+tt.dest+`, offset: +1d}}`, time.Second) // the older one
			wide := tw.overlay(`{target: {device: esp32-42}, time: {offset: -1d}}`, time.Minute)
			var names []string
			if tt.host != "" {
				names = []string{tt.host}
			}
			res := tw.world().Resolve(toNTP("203.0.113.7", names...))
			win := mustWinner(t, res, FamilyTime)
			if win.ID != narrow.Id.String() || win.Level != 1 {
				t.Fatalf("the fault for the server must win: %+v", win)
			}
			if got := overriddenReason(t, familyResult(t, res, FamilyTime), wide.Id.String()); got != "level 1 beats level 3" {
				t.Errorf("reason = %s", got)
			}
			// another server only gets the fault for every server
			elsewhere := toNTP("198.51.100.9", "pool.ntp.org")
			if w := ntpWinner(t, tw, elsewhere); w.ID != wide.Id.String() {
				t.Fatalf("another server: %+v", w)
			}
		})
	}
	// a fault for one server of a network does not hide a device fault for every server from the device's other servers
	netNarrow := tw.overlay(`{target: {network: IoT}, time: {destination: {cidr: 203.0.113.7}, offset: +1d}}`, time.Second)
	devWide := tw.overlay(`{target: {device: esp32-42}, time: {offset: -1d}}`, time.Minute)
	if w := ntpWinner(t, tw, toNTP("203.0.113.7")); w.ID != devWide.Id.String() {
		t.Fatalf("the device's scope comes before the network's server: %+v (network fault %s)", w, netNarrow.Id)
	}
}

// The destination ranks a fault only on a device scope (level 1 against 3). On a group and on a network a fault for one
// server and a fault for every server both have the level "destination and/or port/protocol" of the table of §2.4 (5 and
// 7), and the newer one wins, whichever of the two it is, as for every other family: the table is normative and the
// OpenAPI text says so.
func TestOnAGroupAndOnANetworkTheNewerTimeFaultWinsWhetherOrNotItNamesAServer(t *testing.T) {
	for _, scope := range []struct {
		name, target string
		level        int
	}{{"group", `{group: sensors}`, 5}, {"network", `{network: IoT}`, 7}} {
		for _, narrowIsNewer := range []bool{false, true} {
			name := scope.name + " narrow older"
			narrowAge, wideAge := time.Second, time.Minute // the larger age is the newer
			if narrowIsNewer {
				name, narrowAge, wideAge = scope.name+" narrow newer", time.Minute, time.Second
			}
			t.Run(name, func(t *testing.T) {
				tw := newTestWorld(t, false)
				narrow := tw.overlay(`{target: `+scope.target+`, time: {destination: {cidr: 203.0.113.7}, offset: +1d}}`, narrowAge)
				wide := tw.overlay(`{target: `+scope.target+`, time: {offset: -1d}}`, wideAge)
				newerID, olderID := wide.Id.String(), narrow.Id.String()
				if narrowIsNewer {
					newerID, olderID = olderID, newerID
				}
				win := ntpWinner(t, tw, toNTP("203.0.113.7"))
				if win.ID != newerID || win.Level != scope.level {
					t.Fatalf("the newer fault must win at level %d: %+v", scope.level, win)
				}
				var order []string
				for _, c := range tw.world().TimeCandidates(Source{Subject: subjectA}) {
					order = append(order, c.ID)
				}
				if len(order) != 2 || order[0] != newerID || order[1] != olderID {
					t.Errorf("the candidates are the newer first: %v", order)
				}
			})
		}
	}
}

// A fault that names a server does not apply to a query that does not say which server it was meant for: the question
// "what does this device get in general" has the answer of the fault for every server.
func TestAFaultThatNamesAServerDoesNotApplyToAQueryWithoutOne(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.7}, offset: +1d}}`, time.Second)
	general := Query{Source: subjectA, Protocol: "udp", Port: 123}
	if w := Winner(tw.world().Resolve(general), FamilyTime); w != nil {
		t.Fatalf("winner = %+v", w)
	}
	every := tw.overlay(`{target: {device: esp32-42}, time: {answer: silence}}`, 0)
	if w := ntpWinner(t, tw, general); w.ID != every.Id.String() {
		t.Fatalf("winner = %+v", w)
	}
}

func TestATimeFaultOnlyAppliesToNTPTraffic(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, time: {offset: +1d}}`, time.Second)
	explicit := tw.overlay(`{target: {device: esp32-42}, time: {ports: [12345], destination: {cidr: 203.0.113.7}, offset: +1d}}`, 2*time.Second)
	w := tw.world()
	for name, q := range map[string]Query{
		"tcp 123":         toServer("tcp", 123),
		"udp 53":          toServer("udp", 53),
		"udp 443":         toServer("udp", 443),
		"icmp":            toServer("icmp", 0),
		"no protocol":     {Source: subjectA, DestIP: netip.MustParseAddr("203.0.113.10")},
		"udp, no port":    {Source: subjectA, DestIP: netip.MustParseAddr("203.0.113.10"), Protocol: "udp"},
		"123 over tcp":    {Source: subjectA, DestIP: netip.MustParseAddr("203.0.113.10"), Protocol: "tcp", Port: 123},
		"another port":    {Source: subjectA, DestIP: netip.MustParseAddr("203.0.113.10"), Protocol: "udp", Port: 124},
		"a dns lookup":    {Source: subjectA, DNSName: "time.google.com"},
		"a tls handshake": {Source: subjectA, SNI: "time.google.com", Protocol: "tcp", Port: 443},
	} {
		if win := Winner(w.Resolve(q), FamilyTime); win != nil {
			t.Errorf("%s: winner = %+v", name, win)
		}
	}
	if win := Winner(w.Resolve(toServer("udp", 123)), FamilyTime); win == nil {
		t.Error("udp 123 is the traffic of a time fault")
	}
	// the ports a fault names replace the default one
	q := Query{Source: subjectA, DestIP: netip.MustParseAddr("203.0.113.7"), Protocol: "udp", Port: 12345}
	if win := Winner(w.Resolve(q), FamilyTime); win == nil || win.ID != explicit.Id.String() {
		t.Errorf("udp 12345: winner = %+v", win)
	}
}

func TestATimeFaultCompetesOnlyWithTimeFaults(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultAny, `{source: {device: esp32-42}, latency: 50ms}`, 0)
	timeNet := tw.overlay(`{target: {network: IoT}, time: {offset: +1d}}`, time.Second)
	// faults of every other family, on a more specific scope and newer
	tw.overlay(`{target: {device: esp32-42}, fault: {protocol: udp, ports: [123], latency: 100ms}}`, time.Minute)
	tw.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280}}}`, time.Minute)
	tw.overlay(`{target: {device: esp32-42}, dns: {action: servfail}}`, time.Minute)
	tw.overlay(`{target: {device: esp32-42}, tls: {case: expired}}`, time.Minute)
	tw.overlay(`{target: {device: esp32-42}, dhcp: {action: silence}}`, time.Minute)
	tw.overlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: udp, ports: [123]}}`, time.Minute)
	res := tw.world().Resolve(toNTP("203.0.113.7"))
	if w := mustWinner(t, res, FamilyTime); w.ID != timeNet.Id.String() {
		t.Fatalf("time winner = %+v", w)
	}
	if got := familyResult(t, res, FamilyTime).Overridden; len(got) != 0 {
		t.Errorf("nothing else is overridden by a time fault: %+v", got)
	}
	if latencyOf(mustWinner(t, res, FamilyImpairment)) != "100ms" {
		t.Error("the impairment must be unaffected by the time fault")
	}
	for _, f := range []string{FamilyMTU, FamilyDHCP} {
		if Winner(res, f) == nil {
			t.Errorf("no winner of %s", f)
		}
	}
}

func TestATimeFaultOfAProfilePartCompetesLikeAFault(t *testing.T) {
	tw := newTestWorld(t, false)
	// the example's profile clock-in-1970 has a time part only
	prof := tw.overlay(`{target: {network: IoT}, profile: clock-in-1970}`, time.Second)
	win := ntpWinnerOf(t, tw, "203.0.113.7")
	if win.ID != prof.Id.String() || !win.IsProfilePart() || win.ProfileName != "clock-in-1970" || win.Level != 7 || win.Time.Absolute == nil {
		t.Fatalf("winner = %+v", win)
	}
	if got := win.Summary(); got != "time absolute 1970-01-01T00:00:00Z" {
		t.Errorf("summary = %q", got)
	}
	// on the same scope a fault beats the part of a profile, whatever is newer
	fault := tw.overlay(`{target: {network: IoT}, time: {offset: +1d}}`, -time.Hour)
	res := tw.world().Resolve(toNTP("203.0.113.7"))
	if w := mustWinner(t, res, FamilyTime); w.ID != fault.Id.String() {
		t.Fatalf("a fault beats a profile part of its scope: %+v", w)
	}
	if got := overriddenReason(t, familyResult(t, res, FamilyTime), prof.Id.String()); got != "a fault beats a profile part at the same scope" {
		t.Errorf("reason = %s", got)
	}
	// a part on a more specific scope beats the fault of a wider one
	dev := tw.overlay(`{target: {device: esp32-42}, profile: clock-in-1970}`, -2*time.Hour)
	if w := ntpWinner(t, tw, toNTP("203.0.113.7")); w.ID != dev.Id.String() || w.Level != 3 {
		t.Fatalf("the part on the device must win: %+v", w)
	}
}

func ntpWinnerOf(t *testing.T, tw *testWorld, dest string) Candidate {
	t.Helper()
	return ntpWinner(t, tw, toNTP(dest))
}

// A profile that gets a time part after it was activated on a target a time fault cannot have must not make the
// resolution invent a candidate for traffic that no client address tells apart.
func TestATimeFaultOnTheGlobalScopeOrARemoteNetworkIsIgnoredByTheResolution(t *testing.T) {
	tw := newTestWorld(t, false)
	for _, target := range []model.Scope{{Global: ptr(model.ScopeGlobal(true))}, {RemoteNetwork: &model.RemoteNetworkRef{Cidr: ptr("10.10.0.0/24")}}} {
		tw.overlays = append(tw.overlays,
			model.Overlay{Id: mustUUID("00000000-0000-4000-8000-0000000000a1"), Kind: model.OverlayKindTime, Target: &target, Time: &model.TimeFault{Offset: ptr("+1d")}, UpdatedAt: t0},
			model.Overlay{Id: mustUUID("00000000-0000-4000-8000-0000000000a2"), Kind: model.OverlayKindProfile, Target: &target, Profile: ptr(idClockProfile), UpdatedAt: t0})
	}
	w := tw.world()
	if win := Winner(w.Resolve(toNTP("203.0.113.7")), FamilyTime); win != nil {
		t.Fatalf("winner = %+v", win)
	}
	if got := w.TimeCandidates(Source{Subject: subjectA, Device: idESP}); len(got) != 0 {
		t.Fatalf("candidates = %+v", got)
	}
}

const idClockProfile = "4f5a6b7c-8d9e-4f0a-9b1c-2d3e4f5a6b72"

func TestTimeCandidatesAreTheFaultsOfASourceInTheOrderOfThePrecedence(t *testing.T) {
	tw := newTestWorld(t, false)
	net := tw.overlay(`{target: {network: IoT}, time: {offset: +1d}}`, 5*time.Second)
	grp := tw.overlay(`{target: {group: sensors}, time: {offset: +2d}}`, 4*time.Second)
	devAll := tw.overlay(`{target: {device: esp32-42}, time: {offset: +3d}}`, 2*time.Second)
	devOne := tw.overlay(`{target: {device: esp32-42}, time: {destination: {hostname: time.google.com}, offset: +4d}}`, time.Second)
	part := tw.overlay(`{target: {device: esp32-42}, profile: clock-in-1970}`, 3*time.Second)
	// another device's fault and faults of other families are no candidates
	tw.overlay(`{target: {device: lab-host}, time: {offset: +5d}}`, time.Second)
	tw.overlay(`{target: {device: esp32-42}, dns: {action: servfail}}`, time.Second)
	got := tw.world().TimeCandidates(Source{Subject: subjectA, Device: idESP})
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	// device and server (1), the device's faults (3: the fault before the profile part), the group (5), the network (7)
	want := []string{devOne.Id.String(), devAll.Id.String(), part.Id.String(), grp.Id.String(), net.Id.String()}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("order\n got %v\nwant %v", ids, want)
	}
	if len(tw.world().TimeCandidates(Source{Subject: Subject{IP: netip.MustParseAddr("10.77.0.1")}})) != 0 {
		t.Error("a source outside every scope has no candidates")
	}
	// the match of a candidate is complete: the NTP server reads the ports and the protocol from it
	if m := got[1].Match; protocolOf(m) != "udp" || len(deref(m.Ports)) != 1 || deref(m.Ports)[0] != NTPPort {
		t.Errorf("match = %+v", m)
	}
	if m := got[0].Match; m.Destination == nil || *m.Destination.Hostname != "time.google.com" {
		t.Errorf("match = %+v", m)
	}
}

func TestTheSummaryOfATimeFault(t *testing.T) {
	abs := time.Date(2038, 1, 19, 3, 9, 7, 0, time.UTC)
	skew, neg := float32(500), float32(-12.5)
	for want, f := range map[string]model.TimeFault{
		"time offset +400d":                               {Offset: ptr("+400d")},
		"time absolute 2038-01-19T03:09:07Z":              {Absolute: &abs},
		"time skew 500 ppm":                               {SkewPpm: &skew},
		"time skew -12.5 ppm":                             {SkewPpm: &neg},
		"time leap insert":                                {Leap: ptr(model.TimeFaultLeapInsert)},
		"time offset -1d, skew 500 ppm, leap delete":      {Offset: ptr("-1d"), SkewPpm: &skew, Leap: ptr(model.TimeFaultLeapDelete)},
		"time absolute 2038-01-19T03:09:07Z, leap insert": {Absolute: &abs, Leap: ptr(model.TimeFaultLeapInsert)},
		"time silence":                                    {Answer: ptr(model.TimeFaultAnswerSilence)},
		"time unsynchronized":                             {Answer: ptr(model.TimeFaultAnswerUnsynchronized)},
		"time kiss_of_death RATE":                         {Answer: ptr(model.TimeFaultAnswerKissOfDeath), KissCode: ptr(model.TimeFaultKissCodeRATE)},
		"time origin_mismatch":                            {Answer: ptr(model.TimeFaultAnswerOriginMismatch)},
		"time zero_transmit":                              {Answer: ptr(model.TimeFaultAnswerZeroTransmit)},
		"time wrong_mode":                                 {Answer: ptr(model.TimeFaultAnswerWrongMode)},
	} {
		if got := timeSummary(f); got != want {
			t.Errorf("%q: %q", want, got)
		}
	}
	// the API's view of a result carries it
	c := timeCandidate(LayerOverlay, "00000000-0000-4000-8000-000000000001", "", t0, model.Scope{Device: ptr(idESP)}, model.TimeFault{Offset: ptr("+400d")})
	if ref := c.Ref(""); ref.Family != model.FaultFamilyTime || ref.Summary == nil || *ref.Summary != "time offset +400d" || *ref.Level != 3 {
		t.Errorf("ref = %+v", ref)
	}
}

func TestTimePortsAreTheNamedOnesOrTheNTPPort(t *testing.T) {
	ports, ranges := TimePorts(model.TimeFault{Offset: ptr("+1d")})
	if len(ports) != 1 || ports[0] != 123 || len(ranges) != 0 {
		t.Errorf("default: %v %v", ports, ranges)
	}
	ports, ranges = TimePorts(model.TimeFault{Ports: &[]int{12300, 123}})
	if len(ports) != 2 || ports[0] != 12300 || len(ranges) != 0 {
		t.Errorf("named: %v %v", ports, ranges)
	}
	ports, ranges = TimePorts(model.TimeFault{PortRanges: &[]model.PortRange{{From: 100, To: 200}}})
	if len(ports) != 0 || len(ranges) != 1 {
		t.Errorf("a range replaces the default: %v %v", ports, ranges)
	}
}
