package domain

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func TestTheFamiliesOfAProfileAreItsPartsInReportingOrder(t *testing.T) {
	p := model.Profile{Name: "all", Parts: model.ProfileParts{
		Tls: &model.TlsCase{Case: "expired"}, Dns: &model.DnsFault{Action: "servfail"},
		Mtu: &model.MtuParams{Size: 1400}, Impairment: &model.ImpairmentParams{},
	}}
	if got := strings.Join(ProfileFamilies(p), ","); got != "impairment,mtu,dns,tls" {
		t.Errorf("%s", got)
	}
	if got := ProfileFamilies(model.Profile{}); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

// Plan §2.9: every built-in profile is usable ("DNS broken" since M20, "TLS broken" since M21, P2-M11-01) and so is a custom
// profile with any part: the case intercept came with M22, the last thing a part waited for. The rule that a profile with a part of
// a family that is not implemented is unavailable is kept (and tested with a family that is named for the test).
func TestOnlyProfilesWithAPartOfALaterFamilyAreUnavailable(t *testing.T) {
	for _, b := range BuiltinProfiles() {
		if parts := UnavailableParts(b.Profile); len(parts) != 0 {
			t.Errorf("%s is unavailable: %+v", b.Profile.Name, parts)
		}
		// the milestone the catalogue records is the one the rule names
		if (b.Milestone != "") != (len(UnavailableParts(b.Profile)) > 0) {
			t.Errorf("%s: catalogue milestone %q, parts %+v", b.Profile.Name, b.Milestone, UnavailableParts(b.Profile))
		}
	}
	cases := model.Profile{Parts: model.ProfileParts{
		Impairment: &model.ImpairmentParams{}, Dns: &model.DnsFault{Action: "servfail"}, Tls: &model.TlsCase{Case: "expired"}}}
	if got := UnavailableParts(cases); len(got) != 0 {
		t.Errorf("a certificate case is available since M21: %+v", got)
	}
	cases.Parts.Tls = &model.TlsCase{Case: "intercept"}
	if got := UnavailableParts(cases); len(got) != 0 {
		t.Errorf("the case intercept is available since M22: %+v", got)
	}
	// a family this build does not implement makes the profile unavailable as a whole, in the order of the families
	defer SetUnavailableFamilies(map[string]string{FamilyTLS: "M99", FamilyDNS: "M98"})()
	if got := UnavailableParts(cases); len(got) != 2 || got[0] != (UnavailablePart{FamilyDNS, "M98"}) || got[1] != (UnavailablePart{FamilyTLS, "M99"}) {
		t.Errorf("%+v", got)
	}
	for _, b := range BuiltinProfiles() {
		if (b.Profile.Parts.Tls != nil || b.Profile.Parts.Dns != nil) != (len(UnavailableParts(b.Profile)) > 0) {
			t.Errorf("%s: %+v", b.Profile.Name, UnavailableParts(b.Profile))
		}
	}
}

func TestAProfileIsFoundByUUIDOrNameAmongTheConfiguredAndTheBuiltInOnes(t *testing.T) {
	const custom = "5c6d7e8f-9a0b-4c1d-8e2f-3a4b5c6d7e8f"
	cfg := &model.Configuration{Profiles: &map[string]model.Profile{
		custom: {Name: "Flaky", Parts: model.ProfileParts{Impairment: &model.ImpairmentParams{Latency: ptr("5ms")}}},
	}}
	for _, ref := range []string{custom, strings.ToUpper(custom), "flaky", "FLAKY"} {
		id, p, builtin, ok := ProfileRef(cfg, ref)
		if !ok || id != custom || builtin || p.Name != "Flaky" {
			t.Errorf("%s: %s %+v %v %v", ref, id, p, builtin, ok)
		}
	}
	lte, _ := BuiltinProfileByName("lte")
	for _, ref := range []string{lte.ID, "LTE", strings.ToUpper(lte.ID)} {
		id, p, builtin, ok := ProfileRef(cfg, ref)
		if !ok || id != lte.ID || !builtin || p.Name != "lte" {
			t.Errorf("%s: %s %+v %v %v", ref, id, p, builtin, ok)
		}
	}
	if _, _, _, ok := ProfileRef(cfg, "nope"); ok {
		t.Error("an unknown profile was found")
	}
	// the lookup hands out a copy
	p, _, _ := ProfileByID(cfg, custom)
	*p.Parts.Impairment.Latency = "1h"
	if got := *(*cfg.Profiles)[custom].Parts.Impairment.Latency; got != "5ms" {
		t.Errorf("the configuration was changed through the copy: %s", got)
	}
}

func TestTheSummaryOfAnEmptyImpairmentSaysSo(t *testing.T) {
	c := Candidate{Family: FamilyImpairment, Impairment: &model.FaultBody{}}
	if got := c.Summary(); got != "no impairment" {
		t.Errorf("%q", got)
	}
}
