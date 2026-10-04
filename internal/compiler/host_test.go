package compiler

import "testing"

// M4-06 test: FirstV4 must not depend on the kernel's own address order. It prefers a
// non-secondary address over a secondary one, and among addresses of the same kind the lowest,
// so a reordering of the kernel's own address list (a DHCP renewal, a reboot) never changes which
// address the compiler treats as the interface's own.
func TestFirstV4PrefersThePrimaryAddress(t *testing.T) {
	cases := []struct {
		name string
		l    HostLink
		want string
	}{
		{
			"secondary listed first",
			HostLink{Addrs: []HostAddr{
				{Prefix: pfx("198.51.100.5/24"), Secondary: true},
				{Prefix: pfx("203.0.113.1/24")},
			}},
			"203.0.113.1/24",
		},
		{
			"two non-secondary: the lowest wins",
			HostLink{Addrs: []HostAddr{
				{Prefix: pfx("203.0.113.9/24")},
				{Prefix: pfx("203.0.113.1/24")},
			}},
			"203.0.113.1/24",
		},
		{
			"two secondary: the lowest wins",
			HostLink{Addrs: []HostAddr{
				{Prefix: pfx("198.51.100.9/24"), Secondary: true},
				{Prefix: pfx("198.51.100.1/24"), Secondary: true},
			}},
			"198.51.100.1/24",
		},
		{
			"IPv6 addresses are not candidates",
			HostLink{Addrs: []HostAddr{
				{Prefix: pfx("2001:db8::1/64")},
				{Prefix: pfx("203.0.113.1/24")},
			}},
			"203.0.113.1/24",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.l.FirstV4()
			if !ok || got.String() != c.want {
				t.Fatalf("got %v, %v, want %s", got, ok, c.want)
			}
		})
	}
	if _, ok := (HostLink{}).FirstV4(); ok {
		t.Error("an interface without addresses must report false")
	}
}
