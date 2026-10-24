package domain

import (
	"slices"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// Plan §2.8: a correct device rejects every certificate case, whatever its firmware trusts, and does as it likes with the
// handshake cases, which show it no certificate.
func TestACertificateCaseIsExpectedToBeRejectedAndAHandshakeCaseToBeAnything(t *testing.T) {
	for _, c := range []model.TlsCaseCase{model.TlsCaseCaseUntrustedCa, model.TlsCaseCaseExpired, model.TlsCaseCaseNotYetValid,
		model.TlsCaseCaseHostnameMismatch, model.TlsCaseCaseSelfSigned} {
		if got := TLSExpected(c); got != "reject" {
			t.Errorf("%s: %s", c, got)
		}
	}
	for _, c := range []model.TlsCaseCase{model.TlsCaseCaseHandshakeReset, model.TlsCaseCaseHandshakeClose, model.TlsCaseCaseHandshakeStall} {
		if got := TLSExpected(c); got != "any" {
			t.Errorf("%s: %s", c, got)
		}
	}
}

// Plan §2.8: a device that does not trust the test CA fails expiry, date and name cases at the first check as an unknown
// issuer, so its rejection says nothing about its dates and names; the unknown CA and the self-signed certificate are
// rejected by every firmware for what they are, and a handshake case shows no certificate to test.
func TestARejectionTestsADateOrANameOnlyOnADeviceThatTrustsTheTestCA(t *testing.T) {
	for _, tc := range []struct {
		c                model.TlsCaseCase
		trusts, distrust bool
	}{
		{model.TlsCaseCaseUntrustedCa, true, true},
		{model.TlsCaseCaseSelfSigned, true, true},
		{model.TlsCaseCaseExpired, true, false},
		{model.TlsCaseCaseNotYetValid, true, false},
		{model.TlsCaseCaseHostnameMismatch, true, false},
		{model.TlsCaseCaseHandshakeReset, false, false},
		{model.TlsCaseCaseHandshakeClose, false, false},
		{model.TlsCaseCaseHandshakeStall, false, false},
	} {
		if got := TLSTested(tc.c, true); got != tc.trusts {
			t.Errorf("%s on a device that trusts the test CA: %v", tc.c, got)
		}
		if got := TLSTested(tc.c, false); got != tc.distrust {
			t.Errorf("%s on a device that does not: %v", tc.c, got)
		}
	}
}

// Plan §2.8: a case that names no port applies to the ports of TLS and MQTT over TLS, one that names some applies to those
// only (a range as well).
func TestACaseWithoutPortsSelectsTheDefaultTLSPortsAndOneWithPortsSelectsThose(t *testing.T) {
	ports, ranges := TLSPorts(model.TlsCase{Case: model.TlsCaseCaseExpired})
	if !slices.Equal(ports, []int{443, 8883}) || len(ranges) != 0 {
		t.Errorf("%v %v", ports, ranges)
	}
	// the caller may change what it gets without changing the default
	ports[0] = 1
	if again, _ := TLSPorts(model.TlsCase{}); again[0] != 443 {
		t.Errorf("the default was changed: %v", again)
	}
	ports, ranges = TLSPorts(model.TlsCase{Ports: &[]int{993}})
	if !slices.Equal(ports, []int{993}) || len(ranges) != 0 {
		t.Errorf("%v %v", ports, ranges)
	}
	rs := []model.PortRange{{From: 8000, To: 8100}}
	ports, ranges = TLSPorts(model.TlsCase{PortRanges: &rs})
	if len(ports) != 0 || len(ranges) != 1 || ranges[0].From != 8000 {
		t.Errorf("%v %v", ports, ranges)
	}
}
