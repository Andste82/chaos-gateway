package wireguard

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
)

// PrivateKeyPlaceholder stands in for a private key the gateway does not have: a client with a
// provided public key keeps its private key, and an exported-once key is gone.
const PrivateKeyPlaceholder = "<the private key of this peer>"

// ExportInput is what an export needs.
type ExportInput struct {
	// Config is the active (stored, UUID-keyed) configuration.
	Config  *model.Configuration
	Secrets *secrets.Store
	// UplinkAddress is the default endpoint host of the gateway: the address of the uplink.
	UplinkAddress string
}

// Export is one exported configuration.
type Export struct {
	// Name is a file name without extension: the client's or link's name.
	Name string
	// Conf is the wg-quick configuration.
	Conf string
	// HasPrivateKey is false when Conf carries a placeholder instead of the private key.
	HasPrivateKey bool
}

// Secret reports whether the export contains a private key: it is shown with a warning and never
// stored, logged or part of a configuration export.
func (e Export) Secret() bool { return e.HasPrivateKey }

func findNetwork(cfg *model.Configuration, id string) (model.WireGuardNetwork, error) {
	if cfg.Networks != nil {
		if n, ok := (*cfg.Networks)[id]; ok {
			if wg, err := n.AsWireGuardNetwork(); err == nil && wg.Type == model.WireGuardNetworkTypeWireguard {
				return wg, nil
			}
		}
	}
	return model.WireGuardNetwork{}, fmt.Errorf("no WireGuard network %s", id)
}

func endpointOf(wg model.WireGuardNetwork, in ExportInput) (string, error) {
	if wg.Endpoint != nil && *wg.Endpoint != "" {
		return *wg.Endpoint, nil
	}
	if in.UplinkAddress == "" {
		return "", errors.New("the network has no public endpoint and the uplink address is unknown")
	}
	return in.UplinkAddress + ":" + strconv.Itoa(wg.ListenPort), nil
}

// networkPrefixes returns the prefixes of a network: a test network's subnet; a WireGuard
// network's tunnel subnet, the networks behind its clients and a link's static routes.
func networkPrefixes(cfg *model.Configuration, id string, add func(string)) {
	if cfg.Networks == nil {
		return
	}
	n, ok := (*cfg.Networks)[id]
	if !ok {
		return
	}
	if disc, _ := n.Discriminator(); disc == "lan" {
		if lan, err := n.AsLanNetwork(); err == nil {
			add(lan.Address)
		}
		return
	}
	wg, err := n.AsWireGuardNetwork()
	if err != nil {
		return
	}
	add(wg.Address)
	if wg.Clients != nil {
		for _, oc := range *wg.Clients {
			if oc.ClientNetworks != nil {
				for _, cn := range *oc.ClientNetworks {
					add(cn)
				}
			}
		}
	}
	if wg.Routes != nil {
		for _, r := range *wg.Routes {
			add(r)
		}
	}
}

func clientPrefixes(cfg *model.Configuration, clientID string, add func(string)) {
	if cfg.Networks == nil {
		return
	}
	for _, n := range *cfg.Networks {
		wg, err := n.AsWireGuardNetwork()
		if err != nil || wg.Clients == nil {
			continue
		}
		if oc, ok := (*wg.Clients)[clientID]; ok {
			if a, err := netip.ParseAddr(oc.Address); err == nil {
				add(netip.PrefixFrom(a, 32).String())
			}
			if oc.ClientNetworks != nil {
				for _, cn := range *oc.ClientNetworks {
					add(cn)
				}
			}
		}
	}
}

// reachPrefixes returns what the tunnel of a client carries: the tunnel subnet, what its
// `reachable` list names, and what the access matrix lets reach the client or its hub. The last
// part matters as much as the first: WireGuard drops a decrypted packet whose source is not in the
// AllowedIPs of the peer it came from, so a network that may reach the client has to be in the list
// even though the client never sends to it. The uplink means a full tunnel.
func reachPrefixes(cfg *model.Configuration, hubID string, hub model.WireGuardNetwork, clientID string, c model.WireGuardClient) []string {
	set := map[string]bool{}
	add := func(s string) {
		if p, err := netip.ParsePrefix(s); err == nil {
			set[p.Masked().String()] = true
		}
	}
	add(hub.Address)
	endpoint := func(ep model.MatrixEndpoint) {
		switch {
		case ep.Uplink != nil && bool(*ep.Uplink):
			set["0.0.0.0/0"] = true
		case ep.Management != nil && bool(*ep.Management):
			if cfg.Management.AllowedSources != nil {
				for _, s := range *cfg.Management.AllowedSources {
					add(s)
				}
			}
		case ep.Network != nil:
			networkPrefixes(cfg, *ep.Network, add)
		case ep.Client != nil:
			clientPrefixes(cfg, *ep.Client, add)
		}
	}
	if c.Reachable != nil {
		for _, ep := range *c.Reachable {
			endpoint(ep)
		}
	}
	if cfg.AccessMatrix != nil && cfg.AccessMatrix.Entries != nil {
		for _, e := range *cfg.AccessMatrix.Entries {
			if e.Policy != model.MatrixEntryPolicyAllow {
				continue
			}
			toUs := (e.To.Client != nil && *e.To.Client == clientID) || (e.To.Network != nil && *e.To.Network == hubID)
			if toUs && e.From.Uplink == nil {
				endpoint(e.From)
			}
		}
	}
	if set["0.0.0.0/0"] {
		return []string{"0.0.0.0/0"} // a full tunnel needs nothing else
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ClientConfig renders the wg-quick configuration of a hub client (plan §2.2.1). The private key
// comes from the secrets store; for a client with a provided key, or a key that was deleted after
// its export, the configuration carries a placeholder.
func ClientConfig(in ExportInput, networkID, clientID string) (Export, error) {
	hub, err := findNetwork(in.Config, networkID)
	if err != nil {
		return Export{}, err
	}
	if hub.Kind != model.Hub || hub.Clients == nil {
		return Export{}, fmt.Errorf("%q is not a hub with clients", hub.Name)
	}
	c, ok := (*hub.Clients)[clientID]
	if !ok {
		return Export{}, fmt.Errorf("no client %s in %q", clientID, hub.Name)
	}
	ifKey, err := in.Secrets.WireGuard(networkID)
	if err != nil {
		return Export{}, fmt.Errorf("the key of the network %q: %w", hub.Name, err)
	}
	gwPub, err := PublicKey(ifKey.PrivateKey)
	if err != nil {
		return Export{}, err
	}
	endpoint, err := endpointOf(hub, in)
	if err != nil {
		return Export{}, err
	}
	hubPrefix, err := netip.ParsePrefix(hub.Address)
	if err != nil {
		return Export{}, err
	}
	ck, err := in.Secrets.WireGuard(KeyID(clientID, generationOf(c.Key)))
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return Export{}, err
	}
	priv, has := ck.PrivateKey, ck.PrivateKey != ""
	if !has {
		priv = PrivateKeyPlaceholder
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Chaos Gateway: %s on %s\n[Interface]\nPrivateKey = %s\nAddress = %s/32\n", c.Name, hub.Name, priv, c.Address)
	if dns := clientDNS(c, hubPrefix); dns != "" {
		fmt.Fprintf(&b, "DNS = %s\n", dns)
	}
	mtu := 1420
	if hub.Mtu != nil {
		mtu = *hub.Mtu
	}
	fmt.Fprintf(&b, "MTU = %d\n\n[Peer]\nPublicKey = %s\n", mtu, gwPub)
	if wantsPSK(c.Key) && ck.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", ck.PresharedKey)
	}
	fmt.Fprintf(&b, "Endpoint = %s\nAllowedIPs = %s\n", endpoint, strings.Join(reachPrefixes(in.Config, networkID, hub, clientID, c), ", "))
	ka := 25
	if c.Keepalive != nil {
		if d, err := time.ParseDuration(*c.Keepalive); err == nil {
			ka = int(d / time.Second)
		}
	}
	if ka > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", ka)
	}
	return Export{Name: c.Name, Conf: b.String(), HasPrivateKey: has}, nil
}

// wantsPSK reports whether the key settings ask for a preshared key: a key that is stored but no
// longer asked for is not part of any configuration.
func wantsPSK(ks *model.WireGuardKeySettings) bool {
	return ks != nil && ks.PresharedKey != nil && *ks.PresharedKey
}

func clientDNS(c model.WireGuardClient, hub netip.Prefix) string {
	if c.Dns == nil {
		return ""
	}
	if c.Dns.Mode != nil && string(*c.Dns.Mode) == "custom" && c.Dns.Servers != nil {
		return strings.Join(*c.Dns.Servers, ", ")
	}
	if c.Dns.Mode != nil && string(*c.Dns.Mode) == "gateway" {
		return hub.Addr().String() // the gateway's DNS proxy answers on the hub address
	}
	return ""
}

// LinkRemoteConfig renders the configuration of the remote side of a link: what the other gateway,
// router or lab network needs to bring the tunnel up. `Table = off` keeps wg-quick from installing
// a default route for the 0.0.0.0/0 of the link: routing is the remote side's business.
func LinkRemoteConfig(in ExportInput, networkID string) (Export, error) {
	wg, err := findNetwork(in.Config, networkID)
	if err != nil {
		return Export{}, err
	}
	if wg.Kind != model.Link || wg.Peer == nil {
		return Export{}, fmt.Errorf("%q is not a link", wg.Name)
	}
	ifKey, err := in.Secrets.WireGuard(networkID)
	if err != nil {
		return Export{}, fmt.Errorf("the key of the link %q: %w", wg.Name, err)
	}
	gwPub, err := PublicKey(ifKey.PrivateKey)
	if err != nil {
		return Export{}, err
	}
	pk, err := in.Secrets.WireGuard(KeyID(LinkPeerKeyID(networkID), generationOf(wg.Peer.Key)))
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return Export{}, err
	}
	priv, has := pk.PrivateKey, pk.PrivateKey != ""
	if !has {
		priv = PrivateKeyPlaceholder
	}
	prefix, err := netip.ParsePrefix(wg.Address)
	if err != nil {
		return Export{}, err
	}
	mtu := 1420
	if wg.Mtu != nil {
		mtu = *wg.Mtu
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Chaos Gateway: remote side of the link %s\n[Interface]\nPrivateKey = %s\nAddress = %s/%d\nMTU = %d\nTable = off\n",
		wg.Name, priv, wg.Peer.Address, prefix.Bits(), mtu)
	gatewayInitiates := wg.Peer.Endpoint != nil && *wg.Peer.Endpoint != ""
	if gatewayInitiates {
		// the gateway connects to the remote side: it has to listen where the gateway expects it
		_, port, _ := strings.Cut(*wg.Peer.Endpoint, ":")
		fmt.Fprintf(&b, "ListenPort = %s\n", port)
	}
	fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\n", gwPub)
	if wantsPSK(wg.Peer.Key) && pk.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", pk.PresharedKey)
	}
	if !gatewayInitiates {
		endpoint, err := endpointOf(wg, in)
		if err != nil {
			return Export{}, err
		}
		fmt.Fprintf(&b, "Endpoint = %s\nPersistentKeepalive = 25\n", endpoint)
	}
	b.WriteString("AllowedIPs = 0.0.0.0/0\n")
	return Export{Name: wg.Name, Conf: b.String(), HasPrivateKey: has}, nil
}

// ConsumePrivateKey implements "export once": after the first download of a client whose key
// settings say `export_once`, the private key is deleted from the store; the public key stays in
// the configuration. It does nothing for other clients.
func ConsumePrivateKey(in ExportInput, networkID, clientID string) (bool, error) {
	hub, err := findNetwork(in.Config, networkID)
	if err != nil || hub.Clients == nil {
		return false, err
	}
	c, ok := (*hub.Clients)[clientID]
	if !ok || c.Key == nil || c.Key.ExportOnce == nil || !*c.Key.ExportOnce {
		return false, nil
	}
	rid := KeyID(clientID, generationOf(c.Key))
	k, err := in.Secrets.WireGuard(rid)
	if err != nil {
		return false, err
	}
	if k.PrivateKey == "" {
		return false, nil
	}
	k.PrivateKey, k.Exported = "", true
	return true, in.Secrets.PutWireGuard(rid, k)
}

// MaxQRBytes is what fits into a QR code at the error correction level used.
const MaxQRBytes = 2331

// QRPNG renders a configuration as a QR code, for phones and tablets.
func QRPNG(conf string, size int) ([]byte, error) {
	if len(conf) > MaxQRBytes {
		return nil, fmt.Errorf("the configuration (%d bytes) is too large for a QR code (%d): reduce the networks the client reaches", len(conf), MaxQRBytes)
	}
	return qrcode.Encode(conf, qrcode.Low, size)
}

// QRSVG renders a configuration as an SVG QR code.
func QRSVG(conf string) (string, error) {
	if len(conf) > MaxQRBytes {
		return "", fmt.Errorf("the configuration (%d bytes) is too large for a QR code (%d)", len(conf), MaxQRBytes)
	}
	q, err := qrcode.New(conf, qrcode.Low)
	if err != nil {
		return "", err
	}
	bm := q.Bitmap()
	n := len(bm)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y, row := range bm {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

// Zip bundles configurations, one `<name>.conf` per export, in name order.
func Zip(exports []Export) ([]byte, error) {
	sorted := append([]Export(nil), exports...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]bool{}
	for _, e := range sorted {
		name := safeFileName(e.Name) + ".conf"
		for seen[name] {
			name = "_" + name
		}
		seen[name] = true
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Unix(0, 0).UTC()})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(e.Conf)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func safeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.TrimLeft(strings.ReplaceAll(b.String(), "..", "_"), ".")
	if name == "" {
		return "wireguard"
	}
	return name
}
