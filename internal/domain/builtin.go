package domain

import (
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// BuiltinProfile is a profile that ships with Chaos Gateway: fixed UUID, reserved name, never
// part of a configuration (plan §2.9). The values are starting points to be calibrated.
type BuiltinProfile struct {
	ID      string
	Profile model.Profile
	// Milestone is the milestone from which the profile is usable; empty when it always is.
	// "dns-broken" and "tls-broken" arrive with the DNS and TLS milestones (plan §2.9).
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
		Milestone: "M20",
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
		Milestone: "M21",
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
