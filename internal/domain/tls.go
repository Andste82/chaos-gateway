package domain

import (
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the TLS family as the compiler and the TLS responder need it (plan §2.4, §2.8, M21): which
// cases apply to a source of traffic, and what the responder is expected to see of a device that handles
// each of them correctly.

// TLSCandidates returns the TLS cases (overlays, and the tls parts of activated profiles) that apply to a source of
// traffic, in the order the precedence rules of §2.4 try them (RankOrder). The kernel redirects the traffic that
// any of them selects to the responder; which of them decides a connection depends on what only the connection
// knows (the server name in its ClientHello, the names the device resolved), so the responder asks for the
// decision with Resolve once it has read the ClientHello.
func (w *World) TLSCandidates(src Source) []Candidate {
	var out []Candidate
	for _, c := range w.candidates() {
		if c.Family == FamilyTLS && w.scopeMatches(c.Scope, src.Subject) {
			out = append(out, c)
		}
	}
	RankOrder(out)
	return out
}

// TLSPorts are the TCP ports a TLS case selects: the ones it names, else the default TLS ports (443 and 8883).
func TLSPorts(c model.TlsCase) (ports []int, ranges []model.PortRange) {
	ports, ranges = deref(c.Ports), deref(c.PortRanges)
	if len(ports) == 0 && len(ranges) == 0 {
		return append([]int(nil), defaultTLSPorts...), nil
	}
	return ports, ranges
}

// TLSExpected is what a correct device does with the case (plan §2.8): `reject` for every certificate case, whatever
// the firmware trusts (a device without the test CA fails them all as "unknown issuer", which is still a rejection:
// only the reason differs, see TLSTested), `any` for the handshake cases, where no certificate is shown and the
// device may reset, close or time out as it likes.
func TLSExpected(c model.TlsCaseCase) string {
	switch c {
	case model.TlsCaseCaseHandshakeReset, model.TlsCaseCaseHandshakeClose, model.TlsCaseCaseHandshakeStall:
		return "any"
	}
	return "reject"
}

// TLSTested says whether a rejection of the case tests what the case is for. untrusted_ca and self_signed are
// rejected by every firmware for what they are. Expiry, a date that has not come and a name that does not match are only
// checked by a device that trusts the test CA: one that does not fails every one of them at the first check, as an unknown
// issuer, so its rejection says nothing of its dates or its names (plan §2.8).
func TLSTested(c model.TlsCaseCase, trustsTestCA bool) bool {
	switch c {
	case model.TlsCaseCaseUntrustedCa, model.TlsCaseCaseSelfSigned:
		return true
	case model.TlsCaseCaseExpired, model.TlsCaseCaseNotYetValid, model.TlsCaseCaseHostnameMismatch:
		return trustsTestCA
	}
	return false
}
