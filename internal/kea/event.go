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

// EventFromHook reads the hook point (argv[1]) and the environment (as a lookup function) of a
// run_script call. The variable names are Kea's: KEA_LEASE4_ADDRESS, KEA_LEASE4_HWADDR,
// KEA_LEASE4_CLIENT_ID, KEA_LEASE4_HOSTNAME, KEA_LEASE4_VALID_LIFETIME and KEA_SUBNET_ID.
func EventFromHook(point string, getenv func(string) string) (Event, error) {
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
	if v := getenv("KEA_SUBNET_ID"); v != "" {
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
