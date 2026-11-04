package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The built-in profiles of the time family (plan §2.18, the presets of M34): each has the time part and no other, and the
// values are the ones the plan names.
func TestTheTimePresetsAreBuiltInProfilesWithATimePartOnly(t *testing.T) {
	byName := map[string]BuiltinProfile{}
	for _, p := range BuiltinProfiles() {
		byName[p.Profile.Name] = p
	}
	at := func(y int, mo time.Month, d, h, mi, s int) *time.Time {
		v := time.Date(y, mo, d, h, mi, s, 0, time.UTC)
		return &v
	}
	for name, want := range map[string]model.TimeFault{
		"time-plus-1y":  {Offset: ptr(model.TimeOffset("+1y"))},
		"time-minus-1d": {Offset: ptr(model.TimeOffset("-1d"))},
		"time-1970":     {Absolute: at(1970, 1, 1, 0, 0, 0)},
		// 2038-01-19 03:14:07 UTC is the last second a signed 32-bit time_t holds, 2036-02-07 06:28:16 UTC the end of NTP era 0:
		// the presets start five minutes before
		"time-2038":           {Absolute: at(2038, 1, 19, 3, 9, 7)},
		"time-ntp-era-1":      {Absolute: at(2036, 2, 7, 6, 23, 16)},
		"time-leap-second":    {Absolute: at(2016, 12, 31, 23, 55, 0), Leap: ptr(model.TimeFaultLeapInsert)},
		"time-unsynchronized": {Answer: ptr(model.TimeFaultAnswerUnsynchronized)},
		"time-kiss-of-death":  {Answer: ptr(model.TimeFaultAnswerKissOfDeath), KissCode: ptr(model.TimeFaultKissCodeDENY)},
	} {
		b, ok := byName[name]
		if !ok {
			t.Errorf("%s is not a built-in profile", name)
			continue
		}
		parts := b.Profile.Parts
		if parts.Time == nil || parts.Impairment != nil || parts.Mtu != nil || parts.Dns != nil || parts.Tls != nil {
			t.Errorf("%s: parts %+v, want the time part alone", name, parts)
			continue
		}
		if fams := ProfileFamilies(b.Profile); len(fams) != 1 || fams[0] != FamilyTime {
			t.Errorf("%s: families %v", name, fams)
		}
		if got := *parts.Time; !equalJSON(t, got, want) {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
		if b.Profile.Description == nil || *b.Profile.Description == "" {
			t.Errorf("%s has no description", name)
		}
		if UnavailableParts(b.Profile) != nil {
			t.Errorf("%s: unavailable parts %v", name, UnavailableParts(b.Profile))
		}
	}
}

// The identity of a built-in profile never changes: the UUID is the version 5 UUID of its name below the address of the catalogue,
// and the time presets are no exception.
func TestEveryBuiltinProfileHasTheVersion5UUIDOfItsName(t *testing.T) {
	for _, b := range BuiltinProfiles() {
		want := uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://chaos-gateway.dev/builtin-profiles/"+b.Profile.Name)).String()
		if b.ID != want {
			t.Errorf("%s: id %s, want %s", b.Profile.Name, b.ID, want)
		}
	}
}
