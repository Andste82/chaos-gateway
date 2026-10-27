package domain

import (
	"bytes"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The limits of a diagnostic request (plan §2.12, M24).
const (
	// MaxDiagnosticDuration is how long a diagnostic may take: the request is synchronous.
	MaxDiagnosticDuration = 60 * time.Second
	// DefaultDiagnosticTimeout is the timeout of a request that names none.
	DefaultDiagnosticTimeout = 5 * time.Second
	// DefaultPingCount is the number of echoes of a ping that names none.
	DefaultPingCount = 5
	// PingInterval is the time between two echoes (the smallest an unprivileged ping may have).
	PingInterval = 200 * time.Millisecond
)

// CodeDiagnosticRequest is the code of a diagnostic request that cannot be run as it is.
const CodeDiagnosticRequest = "invalid_diagnostic"

// DiagnosticTypes are the types of the diagnostics of this build, as `GET /capabilities` lists them.
var DiagnosticTypes = []string{"ping", "tcp", "udp", "dns", "http", "tls", "path"}

var diagnosticHostPattern = regexp.MustCompile(`^([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?\.)*[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$`)

// ValidDiagnosticHost reports whether s is an IPv4 address or a host name, without a port, a path or a wildcard. A name
// whose last label is only digits is no name (it is a malformed address).
func ValidDiagnosticHost(s string) bool {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Is4()
	}
	if len(s) == 0 || len(s) > 253 || !diagnosticHostPattern.MatchString(s) {
		return false
	}
	last := s[strings.LastIndexByte(s, '.')+1:]
	return strings.Trim(last, "0123456789") != ""
}

// DecodeDiagnosticRequest reads the body of `POST /diagnostics`: syntax, schema and the rules the schema cannot say.
func DecodeDiagnosticRequest(raw []byte) (*model.DiagnosticRequest, error) {
	doc, err := ParseDocument(bytes.TrimSpace(raw), FormatJSON)
	if err != nil {
		return nil, err
	}
	s, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := s.Validate("DiagnosticRequest", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var req model.DiagnosticRequest
	if err := decodeInto(doc, &req); err != nil {
		return nil, fmt.Errorf("domain: decode validated diagnostic request: %w", err)
	}
	if errs := ValidateDiagnosticRequest(&req); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	return &req, nil
}

// DiagnosticTimeout is the timeout of a request, with the default.
func DiagnosticTimeout(req *model.DiagnosticRequest) time.Duration {
	if req.Timeout != nil {
		if d, ok := parseDuration(*req.Timeout); ok {
			return d
		}
	}
	return DefaultDiagnosticTimeout
}

// DiagnosticCount is the number of echoes of a ping request, with the default.
func DiagnosticCount(req *model.DiagnosticRequest) int {
	if req.Count != nil {
		return *req.Count
	}
	return DefaultPingCount
}

// ValidateDiagnosticRequest checks what the schema cannot: the host, the fields each type needs and the ones it does
// not use, the timeout and the length of the whole run.
func ValidateDiagnosticRequest(req *model.DiagnosticRequest) []model.ValidationError {
	v := &validator{}
	typ := string(req.Type)
	if !ValidDiagnosticHost(req.Host) {
		v.add("/host", CodeDiagnosticRequest, "the host must be an IPv4 address or a host name, without a port or a path")
	}
	unused := func(field string, present bool, types ...string) {
		if !present {
			return
		}
		for _, t := range types {
			if typ == t {
				return
			}
		}
		v.add("/"+field, CodeUnexpectedField, "%s is not used by the type %s", field, typ)
	}
	unused("count", req.Count != nil, "ping")
	unused("dns_name", req.DnsName != nil, "dns")
	unused("url", req.Url != nil, "http")
	unused("sni", req.Sni != nil, "tls", "path")
	unused("payload", req.Payload != nil, "udp")
	unused("tls", req.Tls != nil, "path")
	unused("port", req.Port != nil, "tcp", "udp", "dns", "http", "tls", "path")
	if req.Port == nil && (typ == "tcp" || typ == "udp" || typ == "path") {
		v.add("/port", CodeMissingField, "the type %s needs a port", typ)
	}
	if req.DnsName != nil && strings.HasPrefix(*req.DnsName, "*") {
		v.add("/dns_name", CodeDiagnosticRequest, "a wildcard cannot be resolved")
	}
	if req.Sni != nil && *req.Sni != "" && !ValidDiagnosticHost(*req.Sni) {
		v.add("/sni", CodeDiagnosticRequest, "the SNI must be a host name")
	}
	if typ == "http" && req.Url != nil {
		u, err := url.Parse(*req.Url)
		switch {
		case err != nil:
			v.add("/url", CodeDiagnosticRequest, "the URL is not valid")
		case u.Scheme != "http" && u.Scheme != "https":
			v.add("/url", CodeDiagnosticRequest, "the URL must start with http:// or https://")
		case u.User != nil:
			v.add("/url", CodeDiagnosticRequest, "the URL must not carry credentials")
		case !ValidDiagnosticHost(u.Hostname()):
			v.add("/url", CodeDiagnosticRequest, "the host of the URL must be an IPv4 address or a host name")
		default:
			if p := u.Port(); p != "" {
				if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
					v.add("/url", CodeDiagnosticRequest, "the port of the URL is not valid")
				}
			}
		}
	}
	timeout := DefaultDiagnosticTimeout
	if req.Timeout != nil {
		d, ok := parseDuration(*req.Timeout)
		switch {
		case !ok || d < 10*time.Millisecond:
			v.add("/timeout", CodeInvalidDuration, "the timeout must be at least 10ms")
		case d > MaxDiagnosticDuration:
			v.add("/timeout", CodeInvalidDuration, "the timeout must be at most %s", MaxDiagnosticDuration)
		default:
			timeout = d
		}
	}
	if typ == "ping" && len(v.errs) == 0 {
		// the echoes and the wait for the last reply have to fit the synchronous request
		if total := time.Duration(DiagnosticCount(req)-1)*PingInterval + timeout; total > MaxDiagnosticDuration {
			v.add("/count", CodeDiagnosticRequest, "%d echoes %s apart and a timeout of %s take longer than %s", DiagnosticCount(req), PingInterval, timeout, MaxDiagnosticDuration)
		}
	}
	return v.errs
}
