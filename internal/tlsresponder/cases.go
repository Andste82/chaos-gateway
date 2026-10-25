package tlsresponder

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// serve answers a connection that a case decides. hello is what the client sent (the ClientHello's records) and herr
// why it could not be read in full, nil when it could.
func (s *Server) serve(ctx context.Context, c net.Conn, hello []byte, herr error, info ConnInfo, dec Decision) {
	tc := *dec.Case
	kind := string(tc.Case)
	rep := model.TlsHandshakeReport{Time: s.opt.Clock.Now().UTC(), Client: info.Client.String(), OriginalDst: info.Dst.String(), Case: &kind}
	if info.SNI != "" {
		rep.Sni = &info.SNI
	}
	if id, err := uuid.Parse(dec.Overlay); err == nil {
		rep.Overlay = &id
	}
	finish := func(outcome model.TlsHandshakeReportOutcome, alert string) {
		rep.Outcome = outcome
		if alert != "" {
			rep.Alert = &alert
		}
		s.rep.add(rep)
	}
	if tc.Case == model.TlsCaseCaseIntercept {
		// the proxy takes the connection, also one that is no TLS (plain HTTP on a port the case names)
		s.intercept(ctx, c, hello, herr, info, dec, &rep, finish)
		return
	}
	if herr != nil {
		// it did not get as far as a ClientHello: there is no handshake to break or to show a certificate to
		finish(helloFailure(herr))
		return
	}
	switch tc.Case {
	case model.TlsCaseCaseHandshakeReset:
		reset(c)
		finish(model.TlsHandshakeReportOutcomeResetSent, "")
	case model.TlsCaseCaseHandshakeClose:
		if t, ok := c.(*net.TCPConn); ok {
			_ = t.CloseWrite() // a FIN, and nothing else: the device sees the end of the stream
		}
		finish(model.TlsHandshakeReportOutcomeCloseSent, "")
		s.drain(c, 5*time.Second)
	case model.TlsCaseCaseHandshakeStall:
		finish(model.TlsHandshakeReportOutcomeStalled, "")
		s.stall(ctx, c)
	default:
		if !IsCertificateCase(tc.Case) {
			// a case this build does not serve: never let the device through
			s.opt.Log.Warn("a TLS case the responder does not serve", "case", tc.Case)
			reset(c)
			return
		}
		s.certificate(ctx, c, hello, info, dec, &rep, finish)
	}
}

// certificate shows the certificate of the case and reports how the device took it.
func (s *Server) certificate(ctx context.Context, c net.Conn, hello []byte, info ConnInfo, dec Decision, rep *model.TlsHandshakeReport,
	finish func(model.TlsHandshakeReportOutcome, string)) {
	name := info.SNI
	if name == "" {
		name = dec.Name
	}
	if name == "" {
		name = info.Dst.Addr().String()
	}
	certs, err := s.opt.Issuer.Certificates(dec.Case.Case, name)
	if err != nil {
		s.opt.Log.Error("cannot make the certificate of a case", "case", dec.Case.Case, "name", name, "error", err)
		reset(c)
		return
	}
	var offered []uint16
	cfg := &tls.Config{
		Certificates: certs,
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS13,
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			offered = h.SupportedVersions
			return nil, nil
		},
	}
	conn := tls.Server(&prefixConn{Conn: c, prefix: hello}, cfg)
	hctx, cancel := context.WithTimeout(ctx, s.opt.HandshakeTimeout)
	defer cancel()
	err = conn.HandshakeContext(hctx)
	if err == nil {
		v := versionName(conn.ConnectionState().Version)
		rep.TlsVersion = &v
		finish(model.TlsHandshakeReportOutcomeAccepted, "")
		_ = conn.Close()
		return
	}
	if v := bestOffered(offered); v != "" {
		rep.TlsVersion = &v
	}
	outcome, alert := classify(err)
	finish(outcome, alert)
	reset(c)
}

// helloFailure is the outcome of a connection whose ClientHello could not be read.
func helloFailure(err error) (model.TlsHandshakeReportOutcome, string) {
	var ne net.Error
	switch {
	case errors.Is(err, errNotTLS):
		return model.TlsHandshakeReportOutcomeClientClosed, "not_tls"
	case errors.As(err, &ne) && ne.Timeout():
		return model.TlsHandshakeReportOutcomeTimeout, ""
	}
	return model.TlsHandshakeReportOutcomeClientClosed, ""
}

// classify turns the error of a handshake that did not complete into what the report says. The device aborted it
// after it was shown the certificate with an alert (the alert is the report's) or by closing or resetting the
// connection; it timed out; or the handshake failed on something other than the certificate (a protocol or cipher the
// two sides do not share, bytes that are not TLS), which says nothing about the certificate and is reported as a
// connection that never got as far.
func classify(err error) (model.TlsHandshakeReportOutcome, string) {
	var ne net.Error
	var op *net.OpError
	text := err.Error()
	switch {
	case errors.As(err, &op) && op.Op == "remote error":
		return model.TlsHandshakeReportOutcomeRejected, alertName(op.Err.Error())
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return model.TlsHandshakeReportOutcomeTimeout, ""
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE),
		strings.Contains(text, "connection reset"), strings.Contains(text, "broken pipe"):
		return model.TlsHandshakeReportOutcomeRejected, ""
	case strings.Contains(text, "first record does not look like a TLS handshake"):
		return model.TlsHandshakeReportOutcomeClientClosed, "not_tls"
	case strings.Contains(text, "unsupported versions"), strings.Contains(text, "protocol version not supported"):
		return model.TlsHandshakeReportOutcomeClientClosed, "unsupported_version"
	case strings.Contains(text, "no cipher suite supported"):
		return model.TlsHandshakeReportOutcomeClientClosed, "no_shared_cipher"
	}
	return model.TlsHandshakeReportOutcomeClientClosed, "handshake_error"
}

// alertName is the name of an alert as RFC 8446 writes it, from the text crypto/tls gives it ("tls: bad certificate").
func alertName(s string) string {
	s = strings.TrimPrefix(s, "tls: ")
	if n, ok := alertNames[s]; ok {
		return n
	}
	return strings.ReplaceAll(strings.ToLower(s), " ", "_")
}

var alertNames = map[string]string{
	"close notify":                    "close_notify",
	"unexpected message":              "unexpected_message",
	"bad record MAC":                  "bad_record_mac",
	"record overflow":                 "record_overflow",
	"decompression failure":           "decompression_failure",
	"handshake failure":               "handshake_failure",
	"bad certificate":                 "bad_certificate",
	"unsupported certificate":         "unsupported_certificate",
	"revoked certificate":             "certificate_revoked",
	"expired certificate":             "certificate_expired",
	"unknown certificate":             "certificate_unknown",
	"illegal parameter":               "illegal_parameter",
	"unknown certificate authority":   "unknown_ca",
	"access denied":                   "access_denied",
	"error decoding message":          "decode_error",
	"error decrypting message":        "decrypt_error",
	"export restriction":              "export_restriction",
	"protocol version not supported":  "protocol_version",
	"insufficient security level":     "insufficient_security",
	"internal error":                  "internal_error",
	"inappropriate fallback":          "inappropriate_fallback",
	"user canceled":                   "user_canceled",
	"no renegotiation":                "no_renegotiation",
	"missing extension":               "missing_extension",
	"unsupported extension":           "unsupported_extension",
	"certificate unobtainable":        "certificate_unobtainable",
	"unrecognized name":               "unrecognized_name",
	"bad certificate status response": "bad_certificate_status_response",
	"bad certificate hash value":      "bad_certificate_hash_value",
	"unknown PSK identity":            "unknown_psk_identity",
	"certificate required":            "certificate_required",
	"no application protocol":         "no_application_protocol",
}

// versionName is the name of a TLS version in a report.
func versionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	}
	return ""
}

// bestOffered is the highest version the client offered that the responder speaks: the one the handshake would have
// had, which a rejected one never reports.
func bestOffered(versions []uint16) string {
	best := uint16(0)
	for _, v := range versions {
		if (v == tls.VersionTLS13 || v == tls.VersionTLS12) && v > best {
			best = v
		}
	}
	return versionName(best)
}

// drain reads and discards what the client sends until it closes the connection, for at most d.
func (s *Server) drain(c net.Conn, d time.Duration) {
	_ = c.SetReadDeadline(s.opt.Clock.Now().Add(d))
	_, _ = io.Copy(io.Discard, c)
}

// stall holds the connection and answers nothing, until the client gives up, the stall time is over or the responder
// stops.
func (s *Server) stall(ctx context.Context, c net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.drain(c, s.opt.StallFor)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	reset(c)
}
