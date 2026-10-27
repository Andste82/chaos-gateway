package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestACalibrationRequestIsDecodedWithItsDefaults(t *testing.T) {
	req, err := DecodeCalibrationRequest([]byte(`{"host":"203.0.113.10"}`))
	if err != nil {
		t.Fatal(err)
	}
	if CalibrationDuration(req) != 30*time.Second || CalibrationPort(req) != 7 || req.Profile != nil || req.Fault != nil {
		t.Errorf("%+v", req)
	}
	req, err = DecodeCalibrationRequest([]byte(`{"host":"203.0.113.10","port":9001,"duration":"90s","profile":"bad-lte"}`))
	if err != nil || CalibrationDuration(req) != 90*time.Second || CalibrationPort(req) != 9001 || *req.Profile != "bad-lte" {
		t.Errorf("%+v %v", req, err)
	}
	req, err = DecodeCalibrationRequest([]byte(`{"host":"203.0.113.10","fault":{"upload":{"latency":"50ms","jitter":"5ms"},"download":{"loss":"1%"}}}`))
	if err != nil || req.Fault == nil || req.Fault.Upload == nil {
		t.Errorf("%+v %v", req, err)
	}
}

func TestACalibrationRequestThatCannotBeMeasuredIsRefused(t *testing.T) {
	for name, c := range map[string]struct{ in, path, code string }{
		"no host":            {`{}`, "", ""},
		"a host name":        {`{"host":"broker.example.com"}`, "/host", CodeCalibrationRequest},
		"an IPv6 address":    {`{"host":"2001:db8::1"}`, "/host", CodeCalibrationRequest},
		"a profile and more": {`{"host":"203.0.113.10","profile":"bad-lte","fault":{"latency":"5ms"}}`, "/fault", CodeExclusive},
		"too short":          {`{"host":"203.0.113.10","duration":"4s"}`, "/duration", CodeInvalidDuration},
		"too long":           {`{"host":"203.0.113.10","duration":"301s"}`, "/duration", CodeInvalidDuration},
		"not a duration":     {`{"host":"203.0.113.10","duration":"soon"}`, "", ""},
		"a port of zero":     {`{"host":"203.0.113.10","port":0}`, "", ""},
		"a jitter beyond":    {`{"host":"203.0.113.10","fault":{"latency":"5ms","jitter":"10ms"}}`, "/fault/jitter", CodeJitterExceedsLatency},
		"mixed directions":   {`{"host":"203.0.113.10","fault":{"latency":"5ms","upload":{"latency":"5ms"}}}`, "/fault", CodeMixedDirections},
		"an unknown field":   {`{"host":"203.0.113.10","mystery":1}`, "", ""},
	} {
		_, err := DecodeCalibrationRequest([]byte(c.in))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if c.code == "" {
			continue
		}
		var ve ValidationErrors
		ok := errors.As(err, &ve)
		if !ok || len(ve) == 0 || ve[0].Code != c.code || !strings.HasPrefix(ve[0].Path, c.path) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
