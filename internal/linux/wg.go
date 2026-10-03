package linux

import (
	"fmt"
	"strconv"
	"strings"
)

// WGPeerInfo is a peer as `wg show <dev> dump` reports it. The preshared key is a secret and not
// kept: only whether there is one.
type WGPeerInfo struct {
	PublicKey       string
	HasPresharedKey bool
	Endpoint        string
	AllowedIPs      []string
	// LatestHandshake is the time of the last handshake in seconds since the epoch, 0 for never.
	LatestHandshake int64
	RxBytes         int64
	TxBytes         int64
	// Keepalive is the persistent keepalive in seconds, 0 for off.
	Keepalive int
}

// WGInfo is a WireGuard interface. The private key is never kept.
type WGInfo struct {
	PublicKey  string
	ListenPort int
	Peers      []WGPeerInfo
}

// ParseWGDump parses `wg show <dev> dump`. The first line names the interface (its private key
// is dropped at once), every further line a peer.
func ParseWGDump(out string) (*WGInfo, error) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil, fmt.Errorf("parse wg dump: empty output")
	}
	f := strings.Split(lines[0], "\t")
	if len(f) < 3 {
		return nil, fmt.Errorf("parse wg dump: interface line has %d fields", len(f))
	}
	port, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, fmt.Errorf("parse wg dump: listen port %q", f[2])
	}
	info := &WGInfo{PublicKey: f[1], ListenPort: port}
	for _, l := range lines[1:] {
		p := strings.Split(l, "\t")
		if len(p) < 8 {
			return nil, fmt.Errorf("parse wg dump: peer line has %d fields", len(p))
		}
		peer := WGPeerInfo{PublicKey: p[0], HasPresharedKey: p[1] != "(none)"}
		if p[2] != "(none)" {
			peer.Endpoint = p[2]
		}
		if p[3] != "(none)" && p[3] != "" {
			peer.AllowedIPs = strings.Split(p[3], ",")
		}
		var err1, err2, err3 error
		peer.LatestHandshake, err1 = strconv.ParseInt(p[4], 10, 64)
		peer.RxBytes, err2 = strconv.ParseInt(p[5], 10, 64)
		peer.TxBytes, err3 = strconv.ParseInt(p[6], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("parse wg dump: numbers of peer %s", p[0])
		}
		if p[7] != "off" {
			if peer.Keepalive, err = strconv.Atoi(p[7]); err != nil {
				return nil, fmt.Errorf("parse wg dump: keepalive %q", p[7])
			}
		}
		info.Peers = append(info.Peers, peer)
	}
	return info, nil
}
