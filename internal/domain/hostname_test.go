package domain

import "testing"

// The hostname selectors of plan §2.6: an exact name or `*.suffix` for the subdomains of the suffix and not for
// the suffix itself; case and a trailing dot do not matter.
func TestAHostnameSelectorMatchesTheExactNameOrTheSubdomainsOfASuffix(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"broker.example.com", "broker.example.com", true},
		{"Broker.Example.com.", "BROKER.example.COM", true},
		{"broker.example.com", "a.broker.example.com", false},
		{"broker.example.com", "example.com", false},
		{"*.example.com", "broker.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "BROKER.Example.com.", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "badexample.com", false},
		{"*.example.com", "broker.example.com.evil.net", false},
		{"*.example.com", "", false},
		{"broker.example.com", "", false},
	} {
		if got := HostnameMatches(c.pattern, c.name); got != c.want {
			t.Errorf("HostnameMatches(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
	if HostnamePattern(" MQTT.Example.com. ") != "mqtt.example.com" {
		t.Errorf("%q", HostnamePattern(" MQTT.Example.com. "))
	}
}
