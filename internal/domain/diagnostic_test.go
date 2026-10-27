package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestADiagnosticRequestIsDecoded(t *testing.T) {
	for name, body := range map[string]string{
		"ping":                 `{"type":"ping","host":"203.0.113.10","count":10,"timeout":"2s"}`,
		"ping at the limit":    `{"type":"ping","host":"203.0.113.10","count":100,"timeout":"40s"}`,
		"tcp to a name":        `{"type":"tcp","host":"broker.example.com","port":8883}`,
		"tcp to a name with _": `{"type":"tcp","host":"_mqtt._tcp.lab","port":8883}`,
		"udp with a payload":   `{"type":"udp","host":"10.10.0.20","port":5683,"payload":"hello"}`,
		"dns for a server":     `{"type":"dns","host":"10.10.0.1","dns_name":"broker.example.com"}`,
		"dns for a name":       `{"type":"dns","host":"broker.example.com"}`,
		"http with a URL":      `{"type":"http","host":"x","url":"https://api.example.com:8443/health?x=1"}`,
		"http without a URL":   `{"type":"http","host":"10.10.0.20","port":8080}`,
		"tls":                  `{"type":"tls","host":"10.10.0.20","sni":"broker.example.com"}`,
		"path":                 `{"type":"path","host":"broker.example.com","port":8883}`,
		"path without tls":     `{"type":"path","host":"10.10.0.20","port":1883,"tls":false,"from":{"gateway":true}}`,
	} {
		if _, err := DecodeDiagnosticRequest([]byte(body)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	req, err := DecodeDiagnosticRequest([]byte(`{"type":"ping","host":"10.0.0.1"}`))
	if err != nil || DiagnosticCount(req) != DefaultPingCount || DiagnosticTimeout(req) != DefaultDiagnosticTimeout {
		t.Errorf("defaults: %v %d %v", err, DiagnosticCount(req), DiagnosticTimeout(req))
	}
	req, _ = DecodeDiagnosticRequest([]byte(`{"type":"ping","host":"10.0.0.1","count":3,"timeout":"250ms"}`))
	if DiagnosticCount(req) != 3 || DiagnosticTimeout(req) != 250*time.Millisecond {
		t.Errorf("%d %v", DiagnosticCount(req), DiagnosticTimeout(req))
	}
}

func TestADiagnosticRequestThatCannotRunIsRefused(t *testing.T) {
	for name, c := range map[string]struct{ body, path, code string }{
		"no type":                      {`{"host":"10.0.0.1"}`, "", ""},
		"an unknown type":              {`{"type":"traceroute","host":"10.0.0.1"}`, "/type", ""},
		"an unknown field":             {`{"type":"ping","host":"10.0.0.1","nope":1}`, "/nope", ""},
		"no host":                      {`{"type":"ping"}`, "", ""},
		"a host with a port":           {`{"type":"tcp","host":"10.0.0.1:80","port":80}`, "/host", CodeDiagnosticRequest},
		"a URL as the host":            {`{"type":"tcp","host":"http://broker.test","port":80}`, "/host", CodeDiagnosticRequest},
		"an IPv6 host":                 {`{"type":"tcp","host":"2001:db8::1","port":80}`, "/host", CodeDiagnosticRequest},
		"a malformed address":          {`{"type":"tcp","host":"10.0.0.999","port":80}`, "/host", CodeDiagnosticRequest},
		"a wildcard host":              {`{"type":"tcp","host":"*.example.com","port":80}`, "/host", CodeDiagnosticRequest},
		"a host with a space":          {`{"type":"tcp","host":"a b","port":80}`, "/host", CodeDiagnosticRequest},
		"an empty host":                {`{"type":"tcp","host":"","port":80}`, "/host", CodeDiagnosticRequest},
		"tcp without a port":           {`{"type":"tcp","host":"10.0.0.1"}`, "/port", CodeMissingField},
		"udp without a port":           {`{"type":"udp","host":"10.0.0.1"}`, "/port", CodeMissingField},
		"path without a port":          {`{"type":"path","host":"10.0.0.1"}`, "/port", CodeMissingField},
		"a port out of range":          {`{"type":"tcp","host":"10.0.0.1","port":70000}`, "/port", ""},
		"a port for a ping":            {`{"type":"ping","host":"10.0.0.1","port":80}`, "/port", CodeUnexpectedField},
		"a count for a tcp check":      {`{"type":"tcp","host":"10.0.0.1","port":80,"count":3}`, "/count", CodeUnexpectedField},
		"a payload for a ping":         {`{"type":"ping","host":"10.0.0.1","payload":"x"}`, "/payload", CodeUnexpectedField},
		"a url for a tls check":        {`{"type":"tls","host":"10.0.0.1","url":"https://x/"}`, "/url", CodeUnexpectedField},
		"a dns name for a tcp":         {`{"type":"tcp","host":"10.0.0.1","port":80,"dns_name":"x.test"}`, "/dns_name", CodeUnexpectedField},
		"tls for a tls check":          {`{"type":"tls","host":"10.0.0.1","tls":false}`, "/tls", CodeUnexpectedField},
		"an sni for a ping":            {`{"type":"ping","host":"10.0.0.1","sni":"x.test"}`, "/sni", CodeUnexpectedField},
		"a wildcard dns name":          {`{"type":"dns","host":"10.0.0.1","dns_name":"*.example.com"}`, "/dns_name", CodeDiagnosticRequest},
		"a count of zero":              {`{"type":"ping","host":"10.0.0.1","count":0}`, "/count", ""},
		"a count of 101":               {`{"type":"ping","host":"10.0.0.1","count":101}`, "/count", ""},
		"a timeout with no unit":       {`{"type":"ping","host":"10.0.0.1","timeout":"5"}`, "/timeout", ""},
		"a timeout of nothing":         {`{"type":"ping","host":"10.0.0.1","timeout":"0s"}`, "/timeout", CodeInvalidDuration},
		"a timeout over a minute":      {`{"type":"tcp","host":"10.0.0.1","port":80,"timeout":"61s"}`, "/timeout", CodeInvalidDuration},
		"a ping that takes too long":   {`{"type":"ping","host":"10.0.0.1","count":100,"timeout":"41s"}`, "/count", CodeDiagnosticRequest},
		"an ftp URL":                   {`{"type":"http","host":"x","url":"ftp://x.test/"}`, "/url", CodeDiagnosticRequest},
		"a URL with credentials":       {`{"type":"http","host":"x","url":"http://user:pw@x.test/"}`, "/url", CodeDiagnosticRequest},
		"a URL without a host":         {`{"type":"http","host":"x","url":"http:///path"}`, "/url", CodeDiagnosticRequest},
		"a URL with an IPv6 host":      {`{"type":"http","host":"x","url":"http://[::1]/"}`, "/url", CodeDiagnosticRequest},
		"a URL with a bad port":        {`{"type":"http","host":"x","url":"http://x.test:99999/"}`, "/url", CodeDiagnosticRequest},
		"a URL that is none":           {`{"type":"http","host":"x","url":"http://x.test:ab/"}`, "/url", CodeDiagnosticRequest},
		"an SNI that is no name":       {`{"type":"tls","host":"10.0.0.1","sni":"a b"}`, "/sni", CodeDiagnosticRequest},
		"from the gateway and a probe": {`{"type":"ping","host":"10.0.0.1","from":{"gateway":true,"probe":"p"}}`, "/from", ""},
		"from nothing":                 {`{"type":"ping","host":"10.0.0.1","from":{}}`, "/from", ""},
		"a payload that is too long":   {`{"type":"udp","host":"10.0.0.1","port":1,"payload":"` + strings.Repeat("x", 1025) + `"}`, "/payload", ""},
	} {
		_, err := DecodeDiagnosticRequest([]byte(c.body))
		var ve ValidationErrors
		if !errors.As(err, &ve) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		found := false
		for _, e := range ve {
			if (c.path == "" || strings.HasPrefix(e.Path, c.path)) && (c.code == "" || e.Code == c.code) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %v", name, ve)
		}
	}
}

func TestDiagnosticHosts(t *testing.T) {
	for _, h := range []string{"10.0.0.1", "203.0.113.10", "broker", "broker.example.com", "a-b.c_d.example", "xn--bcher-kva.example", "host1.lab", "1abc.example"} {
		if !ValidDiagnosticHost(h) {
			t.Errorf("%q is a host", h)
		}
	}
	for _, h := range []string{"", "10.0.0.256", "1.2.3", "::1", "[::1]", "a.b.", "-a.example", "a..b", "a b", "a/b", "*.a.b", "a:80", strings.Repeat("a", 254), strings.Repeat("a", 64) + ".example"} {
		if ValidDiagnosticHost(h) {
			t.Errorf("%q is no host", h)
		}
	}
}
