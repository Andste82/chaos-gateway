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

// Plan §2.9: "DNS broken" and "TLS broken" arrive with M20 and M21, the other built-in profiles are
// usable now; a custom profile is judged by its parts.
func TestOnlyProfilesWithAPartOfALaterFamilyAreUnavailable(t *testing.T) {
	for _, b := range BuiltinProfiles() {
		parts := UnavailableParts(b.Profile)
		switch b.Profile.Name {
		case "dns-broken":
			if len(parts) != 1 || parts[0] != (UnavailablePart{FamilyDNS, "M20"}) {
				t.Errorf("%s: %+v", b.Profile.Name, parts)
			}
		case "tls-broken":
			if len(parts) != 1 || parts[0] != (UnavailablePart{FamilyTLS, "M21"}) {
				t.Errorf("%s: %+v", b.Profile.Name, parts)
			}
		default:
			if len(parts) != 0 {
				t.Errorf("%s is unavailable: %+v", b.Profile.Name, parts)
			}
		}
		// the milestone the catalogue records is the one the rule names
		if (b.Milestone != "") != (len(UnavailableParts(b.Profile)) > 0) {
			t.Errorf("%s: catalogue milestone %q, parts %+v", b.Profile.Name, b.Milestone, UnavailableParts(b.Profile))
		}
	}
	both := model.Profile{Parts: model.ProfileParts{
		Impairment: &model.ImpairmentParams{}, Dns: &model.DnsFault{Action: "servfail"}, Tls: &model.TlsCase{Case: "expired"}}}
	if got := UnavailableParts(both); len(got) != 2 || got[0].Milestone != "M20" || got[1].Milestone != "M21" {
		t.Errorf("%+v", got)
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
