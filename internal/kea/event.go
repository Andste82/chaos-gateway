package kea

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Event is a lease event from the run_script hook (plan §2.7): Kea starts the script with the hook
// point as its first argument and the details in KEA_* environment variables.
type Event struct {
	// Name is the hook point without the prefix: select, renew, rebind, release, expire, decline, recover.
	Name     string
	IP       netip.Addr
	MAC      string
	ClientID string
	Hostname string
	SubnetID int
	// ValidLifetime is in seconds.
	ValidLifetime int
}

var hookPoints = map[string]string{
	"lease4_select": "select", "lease4_renew": "renew", "lease4_rebind": "rebind", "lease4_release": "release",
	"lease4_expire": "expire", "lease4_decline": "decline", "lease4_recover": "recover",
}

// unprefixed makes the variable names work with and without the "KEA_" prefix: Kea 3.0 sets
// LEASES4_AT0_ADDRESS and the like without it (found with a real Kea 3.0.3), its documentation and
// older versions say KEA_.
func unprefixed(getenv func(string) string) func(string) string {
	return func(k string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return getenv(strings.TrimPrefix(k, "KEA_"))
	}
}

func firstOf(getenv func(string) string, keys ...string) string {
	for _, k := range keys {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// EventsFromHook is EventFromHook for every hook point: leases4_committed carries the leases that
// were handed out or renewed (LEASES4_SIZE, KEA_LEASES4_AT<i>_ADDRESS, ...), one event each;
// the other points carry one lease.
func EventsFromHook(point string, getenv func(string) string) ([]Event, error) {
	getenv = unprefixed(getenv)
	if point != "leases4_committed" {
		ev, err := EventFromHook(point, getenv)
		if err != nil {
			return nil, err
		}
		return []Event{ev}, nil
	}
	n, err := strconv.Atoi(getenv("LEASES4_SIZE"))
	if err != nil || n < 0 || n > 64 {
		return nil, fmt.Errorf("kea: %s without a usable LEASES4_SIZE", point)
	}
	var out []Event
	for i := 0; i < n; i++ {
		at := func(k string) string { return getenv(fmt.Sprintf("LEASES4_AT%d_%s", i, k)) }
		ev, err := EventFromHook("lease4_select", func(k string) string {
			if k == "KEA_SUBNET_ID" {
				return at("SUBNET_ID")
			}
			return at(strings.TrimPrefix(k, "KEA_LEASE4_"))
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// EventFromHook reads the hook point (argv[1]) and the environment (as a lookup function) of a
// run_script call. The variable names are Kea's: KEA_LEASE4_ADDRESS, KEA_LEASE4_HWADDR,
// KEA_LEASE4_CLIENT_ID, KEA_LEASE4_HOSTNAME, KEA_LEASE4_VALID_LIFETIME and KEA_SUBNET_ID.
func EventFromHook(point string, getenv func(string) string) (Event, error) {
	getenv = unprefixed(getenv)
	name, ok := hookPoints[point]
	if !ok {
		return Event{}, fmt.Errorf("kea: unknown hook point %q", point)
	}
	ip, err := netip.ParseAddr(getenv("KEA_LEASE4_ADDRESS"))
	if err != nil || !ip.Is4() {
		return Event{}, fmt.Errorf("kea: %s without a usable KEA_LEASE4_ADDRESS", point)
	}
	mac := strings.ToLower(getenv("KEA_LEASE4_HWADDR"))
	if mac == "" {
		return Event{}, fmt.Errorf("kea: %s without KEA_LEASE4_HWADDR", point)
	}
	ev := Event{Name: name, IP: ip, MAC: mac, ClientID: getenv("KEA_LEASE4_CLIENT_ID"), Hostname: getenv("KEA_LEASE4_HOSTNAME")}
	if v := firstOf(getenv, "KEA_SUBNET_ID", "KEA_LEASE4_SUBNET_ID"); v != "" {
		if ev.SubnetID, err = strconv.Atoi(v); err != nil {
			return Event{}, fmt.Errorf("kea: KEA_SUBNET_ID %q: %w", v, err)
		}
	}
	if v := getenv("KEA_LEASE4_VALID_LIFETIME"); v != "" {
		if ev.ValidLifetime, err = strconv.Atoi(v); err != nil {
			return Event{}, fmt.Errorf("kea: KEA_LEASE4_VALID_LIFETIME %q: %w", v, err)
		}
	}
	return ev, nil
}
