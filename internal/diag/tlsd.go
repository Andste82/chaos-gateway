package diag

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// tlsInfo is what a handshake showed.
type tlsInfo struct {
	Version, Cipher, ALPN string
	Certs                 []*x509.Certificate
	Verified              bool
	VerifyError           string
}

// handshake makes the TLS handshake on conn and verifies what the server presented. The handshake does not stop at a
// certificate that does not verify: what the server showed is the answer. Only a handshake that fails is an error.
// serverName is the SNI (empty: none), verifyName the name the certificate has to be valid for.
func (r *Runner) handshake(ctx context.Context, conn net.Conn, serverName, verifyName string, timeout time.Duration) (*tlsInfo, error) {
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cfg := &tls.Config{
		// the verification below is the same one, made after the handshake so that its failure is a result
		InsecureSkipVerify: true, //nolint:gosec // see above
		ServerName:         serverName,
		// a diagnostic has to be able to show an old server, whatever the gateway would accept itself
		MinVersion: tls.VersionTLS10, //nolint:gosec // see above
		NextProtos: []string{"h2", "http/1.1"},
		Time:       r.cfg.Now,
	}
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(hctx); err != nil {
		return nil, err
	}
	st := tc.ConnectionState()
	info := &tlsInfo{
		Version: tls.VersionName(st.Version),
		Cipher:  tls.CipherSuiteName(st.CipherSuite),
		ALPN:    st.NegotiatedProtocol,
		Certs:   st.PeerCertificates,
	}
	info.Verified, info.VerifyError = r.verify(st.PeerCertificates, verifyName)
	return info, nil
}

// verify checks the chain a server presented against the roots, for name, at the time of the gateway.
func (r *Runner) verify(chain []*x509.Certificate, name string) (bool, string) {
	if len(chain) == 0 {
		return false, "the server presented no certificate"
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	var roots *x509.CertPool
	if r.cfg.Roots != nil {
		roots = r.cfg.Roots()
	}
	now := r.cfg.Now()
	_, err := chain[0].Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, CurrentTime: now, DNSName: name,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil {
		return true, ""
	}
	return false, verifyMessage(err, chain[0], now)
}

// verifyMessage is the reason a certificate does not verify, in a sentence.
func verifyMessage(err error, leaf *x509.Certificate, now time.Time) string {
	var inv x509.CertificateInvalidError
	if errors.As(err, &inv) && inv.Reason == x509.Expired {
		if now.Before(leaf.NotBefore) {
			return "certificate is not yet valid"
		}
		return "certificate has expired"
	}
	var ua x509.UnknownAuthorityError
	if errors.As(err, &ua) {
		return "certificate signed by unknown authority"
	}
	return strings.TrimPrefix(err.Error(), "x509: ")
}

// fillTLS puts what a handshake showed into the result.
func fillTLS(res *model.DiagnosticResult, info *tlsInfo) {
	t := newOf(res.Tls)
	res.Tls = t
	t.Version, t.Cipher, t.Verified = ptr(info.Version), ptr(info.Cipher), ptr(info.Verified)
	if info.ALPN != "" {
		t.Alpn = ptr(info.ALPN)
	}
	if info.VerifyError != "" {
		t.VerifyError = ptr(info.VerifyError)
	}
	certs := newOf(t.Certificates)
	t.Certificates = certs
	*certs = emptyOf(certs)
	for _, c := range info.Certs {
		e := elemOf(certs)
		e.Subject, e.Issuer = ptr(c.Subject.String()), ptr(c.Issuer.String())
		nb, na := c.NotBefore.UTC(), c.NotAfter.UTC()
		e.NotBefore, e.NotAfter = &nb, &na
		names := append([]string{}, c.DNSNames...)
		e.DnsNames = &names
		*certs = append(*certs, e)
	}
}

// serverNames returns the SNI the handshake sends and the name the certificate is verified for: the request's SNI,
// else the host; an address is no SNI, but a certificate may be valid for it.
func serverNames(req *model.DiagnosticRequest) (sni, verify string) {
	verify = req.Host
	if req.Sni != nil && *req.Sni != "" {
		verify = *req.Sni
	}
	if _, ok := literal(verify); !ok {
		sni = verify
	}
	return sni, verify
}

// tlsDetails is the diagnostic `tls`: it connects, makes the handshake and reports the version, the cipher, the ALPN
// protocol and the chain the server presented. It is ok when the certificate verifies for the name.
func (r *Runner) tlsDetails(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) {
	addr, ok := r.target(ctx, req, timeout, res)
	if !ok {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	conn, err := r.dial(dctx, "tcp", addr, port(req, 443))
	cancel()
	if err != nil {
		res.Error = ptr("cannot connect: " + failure(err, timeout))
		return
	}
	defer func() { _ = conn.Close() }()
	sni, verify := serverNames(req)
	info, err := r.handshake(ctx, conn, sni, verify, timeout)
	if err != nil {
		res.Error = ptr("TLS handshake failed: " + failure(err, timeout))
		return
	}
	fillTLS(res, info)
	if res.Ok = info.Verified; !res.Ok {
		res.Error = ptr(info.VerifyError)
	}
}

// elemOf returns a zero element of the slice p points at, emptyOf an empty slice of its type (the parts of a result
// are anonymous structs).
func elemOf[T any](*[]T) T { var z T; return z }

func emptyOf[T any](*[]T) []T { return []T{} }
