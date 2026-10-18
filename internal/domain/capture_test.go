package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestByteSizesParseAndFormat(t *testing.T) {
	for in, want := range map[string]int64{"0B": 0, "100B": 100, "64KiB": 64 << 10, "5MiB": 5 << 20, "5GiB": 5 << 30, "2TiB": 2 << 40} {
		got, ok := ParseByteSize(in)
		if !ok || got != want {
			t.Errorf("%s: %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "5", "5GB", "GiB", "-1KiB", "1.5GiB", "99999999999TiB", "5 GiB", "5gib"} {
		if _, ok := ParseByteSize(in); ok {
			t.Errorf("%q is a size", in)
		}
	}
	for in, want := range map[int64]string{100: "100B", 1024: "1KiB", 64 << 10: "64KiB", 1 << 30: "1GiB", 1536: "1536B", 3 << 40: "3TiB"} {
		if got := FormatByteSize(in); got != want {
			t.Errorf("%d: %s, want %s", in, got, want)
		}
	}
}

func TestACaptureRequestIsDecodedAndItsLimitsAreChecked(t *testing.T) {
	ok := `{"name":"mqtt","scope":{"device":"esp32-42"},"format":"pcap","snaplen":128,"duration":"90s","max_size":"10MiB"}`
	req, err := DecodeCaptureRequest([]byte(ok))
	if err != nil || req.Name == nil || *req.Name != "mqtt" || req.Snaplen == nil || *req.Snaplen != 128 {
		t.Fatalf("%+v %v", req, err)
	}
	ring := `{"scope":{"network":"IoT"},"ring_buffer":{"files":3,"file_size":"1MiB"}}`
	if _, err := DecodeCaptureRequest([]byte(ring)); err != nil {
		t.Fatal(err)
	}
	sel := `{"scope":{"selector":{"source":{"group":"sensors"},"protocol":"tcp","ports":[8883],"destination":{"cidr":"203.0.113.0/24"}}}}`
	if _, err := DecodeCaptureRequest([]byte(sel)); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ body, path string }{
		"no scope":                         {`{"name":"x"}`, ""},
		"two scopes":                       {`{"scope":{"device":"a","network":"b"}}`, "/scope"},
		"an unknown field":                 {`{"scope":{"uplink":true},"nope":1}`, "/nope"},
		"a format that is none":            {`{"scope":{"uplink":true},"format":"text"}`, "/format"},
		"a short snap length":              {`{"scope":{"uplink":true},"snaplen":10}`, "/snaplen"},
		"a duration of nothing":            {`{"scope":{"uplink":true},"duration":"0s"}`, "/duration"},
		"a duration that is no duration":   {`{"scope":{"uplink":true},"duration":"soon"}`, "/duration"},
		"a tiny maximum size":              {`{"scope":{"uplink":true},"max_size":"1KiB"}`, "/max_size"},
		"a ring with tiny files":           {`{"scope":{"uplink":true},"ring_buffer":{"files":3,"file_size":"1KiB"}}`, "/ring_buffer/file_size"},
		"a ring of one file":               {`{"scope":{"uplink":true},"ring_buffer":{"files":1,"file_size":"1MiB"}}`, "/ring_buffer/files"},
		"a ring with a maximum size":       {`{"scope":{"uplink":true},"ring_buffer":{"files":3,"file_size":"1MiB"},"max_size":"10MiB"}`, "/max_size"},
		"ports without a protocol":         {`{"scope":{"selector":{"source":{"global":true},"ports":[80]}}}`, "/scope/selector/protocol"},
		"a reversed port range":            {`{"scope":{"selector":{"source":{"global":true},"protocol":"udp","port_ranges":[{"from":90,"to":80}]}}}`, "/scope/selector/port_ranges/0"},
		"a destination that is no address": {`{"scope":{"selector":{"source":{"global":true},"destination":{"cidr":"10.0.0.1/8"}}}}`, "/scope/selector/destination/cidr"},
	} {
		_, err := DecodeCaptureRequest([]byte(c.body))
		var ve ValidationErrors
		isVE := errors.As(err, &ve)
		if !isVE || len(ve) == 0 {
			t.Errorf("%s: %v", name, err)
			continue
		}
		found := false
		for _, e := range ve {
			if strings.HasPrefix(e.Path, c.path) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %v, want a problem at %q", name, ve, c.path)
		}
	}
	if _, err := DecodeCaptureRequest([]byte("{not json")); err == nil {
		t.Error("broken JSON is accepted")
	}
}
