package domain

import (
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// BuiltinProfile is a profile that ships with Chaos Gateway: fixed UUID, reserved name, never
// part of a configuration (plan §2.9). The values are starting points to be calibrated.
type BuiltinProfile struct {
	ID      string
	Profile model.Profile
	// Milestone is the milestone from which the profile is usable; empty when it always is.
	// "dns-broken" came with M20 and "tls-broken" with M21 (plan §2.9), the presets of the time family with M34 (plan §2.18): none waits for a milestone now.
	Milestone string
}

func ptr[T any](v T) *T { return &v }

// builtinProfiles are the profiles of plan §2.9. All values are one-way, per direction.
// The UUIDs are version 5 UUIDs of the name below https://chaos-gateway.dev/builtin-profiles/;
// they must never change.
var builtinProfiles = []BuiltinProfile{
	{
		ID: "5b455ed0-e2bf-5910-8dc6-7a0af428025c",
		Profile: model.Profile{
			Name:        "normal",
			Description: ptr("No impairment"),
			Parts:       model.ProfileParts{Impairment: &model.ImpairmentParams{}},
		},
	},
	{
		ID: "ad24af2d-f184-5d04-b8c4-01f24c86ddcf",
		Profile: model.Profile{
			Name:        "lte",
			Description: ptr("50 ms ± 10 ms, 0.1 % loss"),
			Parts: model.ProfileParts{Impairment: &model.ImpairmentParams{
				Latency: ptr("50ms"), Jitter: ptr("10ms"), Loss: ptr("0.1%"),
			}},
		},
	},
	{
		ID: "b9b6e3e5-b89a-5b25-b7af-b1ac017bda16",
		Profile: model.Profile{
			Name:        "bad-lte",
			Description: ptr("150 ms ± 50 ms, 3 % loss, 2 Mbit/s"),
			Parts: model.ProfileParts{Impairment: &model.ImpairmentParams{
				Latency: ptr("150ms"), Jitter: ptr("50ms"), Loss: ptr("3%"), Rate: ptr("2Mbit"),
			}},
		},
	},
	{
		ID: "39c3e0cb-b84a-5104-8954-c629612acf8e",
		Profile: model.Profile{
			Name:        "satellite",
			Description: ptr("600 ms ± 30 ms, 1 % loss"),
			Parts: model.ProfileParts{Impairment: &model.ImpairmentParams{
				Latency: ptr("600ms"), Jitter: ptr("30ms"), Loss: ptr("1%"),
			}},
		},
	},
	{
		ID: "2edb0767-5e4b-5ae3-a8be-e066ce5f5afe",
		Profile: model.Profile{
			Name:        "congested-wifi",
			Description: ptr("30 ms ± 20 ms, 2 % loss in bursts"),
			Parts: model.ProfileParts{Impairment: &model.ImpairmentParams{
				// Gilbert-Elliott: average loss p/(p+r) = 2 %, bursts of about 4 packets
				Latency: ptr("30ms"), Jitter: ptr("20ms"),
				BurstLoss: &model.GilbertElliott{P: "0.5%", R: "24.5%"},
			}},
		},
	},
	{
		ID: "63bd31e5-b657-50bf-b7cf-843916104518",
		Profile: model.Profile{
			Name:        "offline",
			Description: ptr("Blackout: everything is dropped"),
			Parts:       model.ProfileParts{Impairment: &model.ImpairmentParams{Blackout: ptr(true)}},
		},
	},
	{
		ID: "0aba41c3-7349-5244-8c84-b7490b805960",
		Profile: model.Profile{
			Name:        "intermittent",
			Description: ptr("Flapping: 20 s up, 10 s down"),
			Parts: model.ProfileParts{Impairment: &model.ImpairmentParams{
				Flapping: &model.Flapping{Up: "20s", Down: "10s"},
			}},
		},
	},
	{
		ID: "06f8da2c-f8a8-5d89-ad2b-e5b795d7a48a",
		Profile: model.Profile{
			Name:        "dns-broken",
			Description: ptr("DNS answers SERVFAIL"),
			Parts:       model.ProfileParts{Dns: &model.DnsFault{Action: "servfail"}},
		},
	},
	{
		ID: "16ee6339-08ca-5c47-a34b-f89f32070b14",
		Profile: model.Profile{
			Name:        "tls-broken",
			Description: ptr("TLS handshake reset on the TLS ports"),
			Parts: model.ProfileParts{Tls: &model.TlsCase{
				Case:     "handshake_reset",
				Protocol: ptr(model.Protocol("tcp")),
				Ports:    &[]int{443, 8883},
			}},
		},
	},
	{
		ID: "c1dab135-4a46-5b42-8f31-ac40cff25a7b",
		Profile: model.Profile{
			Name:        "time-plus-1y",
			Description: ptr("The served time is one year ahead (a certificate that is not valid yet, a token from the future)"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Offset: ptr(model.TimeOffset("+1y"))}},
		},
	},
	{
		ID: "0d6f4221-e214-5920-87b5-8b61736318ce",
		Profile: model.Profile{
			Name:        "time-minus-1d",
			Description: ptr("The served time is one day behind (a certificate that has expired, a token that has not been issued yet)"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Offset: ptr(model.TimeOffset("-1d"))}},
		},
	},
	{
		ID: "1a1ebde7-54eb-5d9f-b768-12fa8fc1df67",
		Profile: model.Profile{
			Name:        "time-1970",
			Description: ptr("The served time is 1970-01-01 00:00:00 UTC (a device that lost its clock)"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Absolute: ptr(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC))}},
		},
	},
	{
		ID: "35e4fa1e-2dba-5356-8169-2ce5b7c65c62",
		Profile: model.Profile{
			Name:        "time-2038",
			Description: ptr("The served time is 2038-01-19 03:09:07 UTC, five minutes before a signed 32-bit time_t overflows"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Absolute: ptr(time.Date(2038, 1, 19, 3, 9, 7, 0, time.UTC))}},
		},
	},
	{
		ID: "13ccac50-8353-53f0-9c89-bb44be9021ca",
		Profile: model.Profile{
			Name:        "time-ntp-era-1",
			Description: ptr("The served time is 2036-02-07 06:23:16 UTC, five minutes before the 32-bit seconds of NTP roll over into era 1"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Absolute: ptr(time.Date(2036, 2, 7, 6, 23, 16, 0, time.UTC))}},
		},
	},
	{
		ID: "f564dfd6-68af-5ad9-ade8-acc59e527c90",
		Profile: model.Profile{
			Name:        "time-leap-second",
			Description: ptr("The served time is 2016-12-31 23:55:00 UTC with the leap indicator set to insert a second at the end of the day"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Absolute: ptr(time.Date(2016, 12, 31, 23, 55, 0, 0, time.UTC)), Leap: ptr(model.TimeFaultLeapInsert)}},
		},
	},
	{
		ID: "866d37d3-4e54-56eb-87b0-834f697eb522",
		Profile: model.Profile{
			Name:        "time-unsynchronized",
			Description: ptr("The NTP server answers unsynchronized (leap indicator 3, stratum 16)"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Answer: ptr(model.TimeFaultAnswerUnsynchronized)}},
		},
	},
	{
		ID: "68a521c8-2dea-5d0f-b6b2-3a83d601537c",
		Profile: model.Profile{
			Name:        "time-kiss-of-death",
			Description: ptr("The NTP server answers a kiss-of-death DENY (stratum 0): the client must stop using the server"),
			Parts:       model.ProfileParts{Time: &model.TimeFault{Answer: ptr(model.TimeFaultAnswerKissOfDeath), KissCode: ptr(model.TimeFaultKissCodeDENY)}},
		},
	},
}

// BuiltinProfiles returns the built-in profiles (copies; callers may change them).
func BuiltinProfiles() []BuiltinProfile {
	out := make([]BuiltinProfile, len(builtinProfiles))
	for i, p := range builtinProfiles {
		out[i] = BuiltinProfile{ID: p.ID, Profile: clone(p.Profile), Milestone: p.Milestone}
	}
	return out
}

// IsBuiltinProfileID reports whether id is the UUID of a built-in profile.
func IsBuiltinProfileID(id string) bool {
	for _, p := range builtinProfiles {
		if p.ID == id {
			return true
		}
	}
	return false
}

// BuiltinProfileByName returns the built-in profile with that name (ignoring case).
func BuiltinProfileByName(name string) (BuiltinProfile, bool) {
	for _, p := range BuiltinProfiles() {
		if strings.EqualFold(p.Profile.Name, name) {
			return p, true
		}
	}
	return BuiltinProfile{}, false
}
