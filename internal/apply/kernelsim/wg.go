package kernelsim

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

type wgPeer struct {
	pub       string
	hasPSK    bool
	endpoint  string
	allowed   []string
	keepalive int
	handshake int64
	rx, tx    int64
}

type wgState struct {
	priv  string
	pub   string
	port  int
	peers map[string]*wgPeer
}

// wg simulates `wg syncconf <dev> /dev/stdin` and `wg show <dev> dump`.
func (k *Kernel) wg(c executor.Command) (executor.Result, error) {
	a := c.Args
	switch {
	case len(a) == 3 && a[0] == "syncconf":
		l, ok := k.links[a[1]]
		if !ok || l.wg == nil {
			return executor.Result{Exit: 1, Stderr: "Unable to access interface: No such device\n"}, nil
		}
		return k.syncconf(l, c.Stdin)
	case len(a) == 3 && a[0] == "show" && a[2] == "dump":
		l, ok := k.links[a[1]]
		if !ok || l.wg == nil {
			return executor.Result{Exit: 1, Stderr: "Unable to access interface: No such device\n"}, nil
		}
		return k.wgDump(l)
	}
	return fail("unsupported wg %v", a)
}

func (k *Kernel) syncconf(l *link, conf string) (executor.Result, error) {
	st := l.wg
	var peers []*wgPeer
	var cur *wgPeer
	section := ""
	var priv string
	port := 0
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "":
		case "[Interface]":
			section = "i"
		case "[Peer]":
			section = "p"
			cur = &wgPeer{}
			peers = append(peers, cur)
		default:
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				return fail("Line unrecognized: `%s'", line)
			}
			key, val = strings.TrimSpace(key), strings.TrimSpace(val)
			switch {
			case section == "i" && key == "PrivateKey":
				priv = val
			case section == "i" && key == "ListenPort":
				port, _ = strconv.Atoi(val)
			case section == "p" && key == "PublicKey":
				cur.pub = val
			case section == "p" && key == "PresharedKey":
				cur.hasPSK = true
			case section == "p" && key == "AllowedIPs":
				for _, x := range strings.Split(val, ",") {
					cur.allowed = append(cur.allowed, strings.TrimSpace(x))
				}
			case section == "p" && key == "PersistentKeepalive":
				cur.keepalive, _ = strconv.Atoi(val)
			case section == "p" && key == "Endpoint":
				cur.endpoint = val
			default:
				return fail("Line unrecognized: `%s'", line)
			}
		}
	}
	pub, err := wireguard.PublicKey(priv)
	if err != nil {
		return fail("Key is not the correct length or format: `%s'", "")
	}
	st.priv, st.pub, st.port = priv, pub, port
	next := map[string]*wgPeer{}
	for _, p := range peers {
		if old, ok := st.peers[p.pub]; ok {
			// an unchanged peer keeps its session
			p.handshake, p.rx, p.tx = old.handshake, old.rx, old.tx
			if p.endpoint == "" {
				p.endpoint = old.endpoint // a roaming peer's learned endpoint stays
			}
		}
		next[p.pub] = p
	}
	st.peers = next
	return executor.Result{}, nil
}

func (k *Kernel) wgDump(l *link) (executor.Result, error) {
	st := l.wg
	var b strings.Builder
	// like the real tool, the dump starts with the private key
	fmt.Fprintf(&b, "%s\t%s\t%d\toff\n", st.priv, st.pub, st.port)
	keys := make([]string, 0, len(st.peers))
	for pub := range st.peers {
		keys = append(keys, pub)
	}
	sort.Strings(keys)
	for _, pub := range keys {
		p := st.peers[pub]
		psk := "(none)"
		if p.hasPSK {
			psk = "SIMULATEDPRESHAREDKEYSIMULATEDPRESHAREDKE="
		}
		ep := p.endpoint
		if ep == "" {
			ep = "(none)"
		}
		allowed := "(none)"
		if len(p.allowed) > 0 {
			allowed = strings.Join(p.allowed, ",")
		}
		ka := "off"
		if p.keepalive > 0 {
			ka = strconv.Itoa(p.keepalive)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n", pub, psk, ep, allowed, p.handshake, p.rx, p.tx, ka)
	}
	return okr(b.String())
}

// Handshake records a handshake of a peer, as traffic through the tunnel would.
func (k *Kernel) Handshake(dev, peerPub string, unix int64, rx, tx int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	p := k.links[dev].wg.peers[peerPub]
	p.handshake, p.rx, p.tx = unix, p.rx+rx, p.tx+tx
}

// WireGuardPeers returns the public keys of the peers of an interface.
func (k *Kernel) WireGuardPeers(dev string) []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[dev]
	if !ok || l.wg == nil {
		return nil
	}
	var out []string
	for pub := range l.wg.peers {
		out = append(out, pub)
	}
	sort.Strings(out)
	return out
}

// WireGuardKey returns the private key the kernel has for an interface: tests use it to check
// that the key that was written is the key that was stored.
func (k *Kernel) WireGuardKey(dev string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[dev]; ok && l.wg != nil {
		return l.wg.priv
	}
	return ""
}
