package wireguard

import (
	"archive/zip"
	"bytes"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	zqr "github.com/makiuchi-d/gozxing/qrcode"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
)

func exportSetup(t *testing.T) (ExportInput, string, string, string, *secrets.Store) {
	t.Helper()
	sec := store(t)
	norm, errs := domain.Normalize(example(t)) // a stored configuration names objects by UUID
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	cfg, err := Provision(norm, sec)
	if err != nil {
		t.Fatal(err)
	}
	hubID, _, linkID, _ := networks(t, cfg)
	cid, _ := firstClient(t, cfg)
	return ExportInput{Config: cfg, Secrets: sec, UplinkAddress: "203.0.113.1"}, hubID, cid, linkID, sec
}

func TestClientConfigHasEverythingTheClientNeeds(t *testing.T) {
	in, hubID, cid, _, sec := exportSetup(t)
	e, err := ClientConfig(in, hubID, cid)
	if err != nil {
		t.Fatal(err)
	}
	ck, _ := sec.WireGuard(cid)
	ik, _ := sec.WireGuard(hubID)
	gwPub, _ := PublicKey(ik.PrivateKey)
	if !e.HasPrivateKey || !e.Secret() || e.Name != "lab-rA" {
		t.Fatalf("%+v", e)
	}
	for _, want := range []string{
		"[Interface]\nPrivateKey = " + ck.PrivateKey + "\n",
		"Address = 10.99.0.2/32\n",
		"DNS = 10.99.0.1\n", // dns mode gateway: the hub address
		"MTU = 1420\n",
		"[Peer]\nPublicKey = " + gwPub + "\n",
		"PresharedKey = " + ck.PresharedKey + "\n",
		"Endpoint = gw.example.net:51820\n", // the example names a public endpoint
		"PersistentKeepalive = 25\n",
	} {
		if !strings.Contains(e.Conf, want) {
			t.Errorf("the configuration lacks %q:\n%s", want, e.Conf)
		}
	}
	// the tunnel subnet and what the client may reach
	if !strings.Contains(e.Conf, "AllowedIPs = 10.10.0.0/24, 10.99.0.0/24\n") {
		t.Errorf("AllowedIPs:\n%s", e.Conf)
	}
}

func TestTheEndpointDefaultsToTheUplinkAddress(t *testing.T) {
	in, hubID, cid, _, _ := exportSetup(t)
	n := (*in.Config.Networks)[hubID]
	wg, _ := n.AsWireGuardNetwork()
	wg.Endpoint = nil
	_ = n.FromWireGuardNetwork(wg)
	(*in.Config.Networks)[hubID] = n
	e, err := ClientConfig(in, hubID, cid)
	if err != nil || !strings.Contains(e.Conf, "Endpoint = 203.0.113.1:51820\n") {
		t.Fatalf("%v\n%s", err, e.Conf)
	}
	in.UplinkAddress = ""
	if _, err := ClientConfig(in, hubID, cid); err == nil {
		t.Error("no endpoint at all must be an error")
	}
}

func TestFullTunnelAndCustomDNS(t *testing.T) {
	in, hubID, cid, _, _ := exportSetup(t)
	n := (*in.Config.Networks)[hubID]
	wg, _ := n.AsWireGuardNetwork()
	c := (*wg.Clients)[cid]
	up := model.MatrixEndpointUplink(true)
	reach := append(*c.Reachable, model.MatrixEndpoint{Uplink: &up})
	c.Reachable = &reach
	mode := model.WireGuardClientDnsMode("custom")
	c.Dns = &model.WireGuardClientDns{Mode: &mode, Servers: &[]string{"9.9.9.9", "1.1.1.1"}}
	(*wg.Clients)[cid] = c
	_ = n.FromWireGuardNetwork(wg)
	(*in.Config.Networks)[hubID] = n
	e, err := ClientConfig(in, hubID, cid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.Conf, "AllowedIPs = 0.0.0.0/0\n") || !strings.Contains(e.Conf, "DNS = 9.9.9.9, 1.1.1.1\n") {
		t.Errorf("%s", e.Conf)
	}
}

func TestAProvidedKeyHasAPlaceholderAndADeletedKeyToo(t *testing.T) {
	in, hubID, cid, _, sec := exportSetup(t)
	k, _ := sec.WireGuard(cid)
	k.PrivateKey = ""
	_ = sec.PutWireGuard(cid, k)
	e, err := ClientConfig(in, hubID, cid)
	if err != nil {
		t.Fatal(err)
	}
	if e.HasPrivateKey || e.Secret() || !strings.Contains(e.Conf, "PrivateKey = "+PrivateKeyPlaceholder) {
		t.Errorf("%+v\n%s", e, e.Conf)
	}
}

func TestExportOnceDeletesThePrivateKeyAfterTheFirstExport(t *testing.T) {
	in, hubID, cid, _, sec := exportSetup(t)
	// the example client does not ask for export once: nothing happens
	if done, err := ConsumePrivateKey(in, hubID, cid); err != nil || done {
		t.Fatalf("%v %v", done, err)
	}
	n := (*in.Config.Networks)[hubID]
	wg, _ := n.AsWireGuardNetwork()
	c := (*wg.Clients)[cid]
	once := true
	c.Key.ExportOnce = &once
	(*wg.Clients)[cid] = c
	_ = n.FromWireGuardNetwork(wg)
	(*in.Config.Networks)[hubID] = n

	first, err := ClientConfig(in, hubID, cid)
	if err != nil || !first.HasPrivateKey {
		t.Fatal(err)
	}
	if done, err := ConsumePrivateKey(in, hubID, cid); err != nil || !done {
		t.Fatalf("%v %v", done, err)
	}
	k, _ := sec.WireGuard(cid)
	if k.PrivateKey != "" || !k.Exported || k.PresharedKey == "" {
		t.Errorf("%+v: the private key goes, the rest stays", k)
	}
	second, _ := ClientConfig(in, hubID, cid)
	if second.HasPrivateKey || !strings.Contains(second.Conf, PrivateKeyPlaceholder) {
		t.Errorf("a second export must not have the key:\n%s", second.Conf)
	}
	if done, _ := ConsumePrivateKey(in, hubID, cid); done {
		t.Error("consumed twice")
	}
}

func TestLinkRemoteConfig(t *testing.T) {
	in, _, _, linkID, sec := exportSetup(t)
	// the example's link peer has a provided key: placeholder
	e, err := LinkRemoteConfig(in, linkID)
	if err != nil {
		t.Fatal(err)
	}
	ik, _ := sec.WireGuard(linkID)
	gwPub, _ := PublicKey(ik.PrivateKey)
	for _, want := range []string{"Address = 10.255.0.1/31\n", "Table = off\n", "[Peer]\nPublicKey = " + gwPub + "\n", "AllowedIPs = 0.0.0.0/0\n", "PrivateKey = " + PrivateKeyPlaceholder} {
		if !strings.Contains(e.Conf, want) {
			t.Errorf("lacks %q:\n%s", want, e.Conf)
		}
	}
	// the gateway initiates (the example gives the remote endpoint): the remote side listens and needs no endpoint
	if !strings.Contains(e.Conf, "ListenPort = 51821\n") || strings.Contains(e.Conf, "Endpoint =") {
		t.Errorf("the remote side listens where the gateway connects:\n%s", e.Conf)
	}
	// the remote side initiates: it needs the gateway's endpoint and a keepalive
	n := (*in.Config.Networks)[linkID]
	wg, _ := n.AsWireGuardNetwork()
	wg.Peer.Endpoint = nil
	_ = n.FromWireGuardNetwork(wg)
	(*in.Config.Networks)[linkID] = n
	e, err = LinkRemoteConfig(in, linkID)
	if err != nil || !strings.Contains(e.Conf, "Endpoint = 203.0.113.1:51821\n") || !strings.Contains(e.Conf, "PersistentKeepalive = 25\n") || strings.Contains(e.Conf, "ListenPort") {
		t.Fatalf("%v\n%s", err, e.Conf)
	}
}

func TestExportErrors(t *testing.T) {
	in, hubID, cid, linkID, _ := exportSetup(t)
	if _, err := ClientConfig(in, "00000000-0000-4000-8000-000000000000", cid); err == nil {
		t.Error("unknown network")
	}
	if _, err := ClientConfig(in, hubID, "00000000-0000-4000-8000-000000000000"); err == nil {
		t.Error("unknown client")
	}
	if _, err := ClientConfig(in, linkID, cid); err == nil {
		t.Error("a link has no clients")
	}
	if _, err := LinkRemoteConfig(in, hubID); err == nil {
		t.Error("a hub is no link")
	}
}

func decodeQR(t *testing.T, pngBytes []byte) string {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	res, err := zqr.NewQRCodeReader().Decode(bmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.GetText()
}

func TestTheQRCodeDecodesToTheFile(t *testing.T) {
	in, hubID, cid, _, _ := exportSetup(t)
	e, err := ClientConfig(in, hubID, cid)
	if err != nil {
		t.Fatal(err)
	}
	pngBytes, err := QRPNG(e.Conf, 512)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeQR(t, pngBytes); got != e.Conf {
		t.Fatalf("the QR code does not decode to the file:\n%q\n%q", got, e.Conf)
	}
	svg, err := QRSVG(e.Conf)
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "<path") || strings.Contains(svg, e.Conf[:20]) {
		t.Errorf("svg: %v %.80s", err, svg)
	}
	if _, err := QRPNG(strings.Repeat("x", MaxQRBytes+1), 256); err == nil {
		t.Error("a configuration that does not fit must be refused")
	}
	if _, err := QRSVG(strings.Repeat("x", MaxQRBytes+1)); err == nil {
		t.Error("svg: too large")
	}
}

func TestZipHoldsOneFilePerExportInNameOrder(t *testing.T) {
	data, err := Zip([]Export{{Name: "b client", Conf: "B"}, {Name: "a", Conf: "A"}, {Name: "../evil", Conf: "E"}, {Name: "a", Conf: "A2"}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	content := map[string]string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		_ = r.Close()
		content[f.Name] = string(b)
		if strings.Contains(f.Name, "/") || strings.Contains(f.Name, "..") {
			t.Errorf("unsafe name %q", f.Name)
		}
	}
	if len(names) != 4 {
		t.Errorf("names %v", names)
	}
	if content["a.conf"] != "A" || content["_a.conf"] != "A2" || content["b_client.conf"] != "B" {
		t.Errorf("content %v", content)
	}
}

func TestTheClientConfigCarriesWhatTheMatrixLetsReachTheClient(t *testing.T) {
	in, hubID, cid, _, _ := exportSetup(t)
	// without a reachable list and without matrix entries the client carries its tunnel subnet only
	n := (*in.Config.Networks)[hubID]
	wg, _ := n.AsWireGuardNetwork()
	c := (*wg.Clients)[cid]
	c.Reachable = nil
	(*wg.Clients)[cid] = c
	_ = n.FromWireGuardNetwork(wg)
	(*in.Config.Networks)[hubID] = n
	e, _ := ClientConfig(in, hubID, cid)
	if !strings.Contains(e.Conf, "AllowedIPs = 10.99.0.0/24\n") {
		t.Fatalf("%s", e.Conf)
	}
	// an entry that allows a test network to reach the hub: the decrypted packets of that network
	// are only accepted by the client when their source is in AllowedIPs
	iot := ""
	for id, nn := range *in.Config.Networks {
		if disc, _ := nn.Discriminator(); disc == "lan" {
			iot = id
		}
	}
	hub := hubID
	in.Config.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{
		{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Network: &hub}, Policy: model.MatrixEntryPolicyAllow},
	}}
	e, _ = ClientConfig(in, hubID, cid)
	if !strings.Contains(e.Conf, "AllowedIPs = 10.10.0.0/24, 10.99.0.0/24\n") {
		t.Errorf("a network that may reach the hub must be in the client's AllowedIPs:\n%s", e.Conf)
	}
	// a deny entry, and an entry from the uplink, add nothing
	up := model.MatrixEndpointUplink(true)
	in.Config.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{
		{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Network: &hub}, Policy: model.MatrixEntryPolicyDeny},
		{From: model.MatrixEndpoint{Uplink: &up}, To: model.MatrixEndpoint{Network: &hub}, Policy: model.MatrixEntryPolicyAllow},
	}}
	e, _ = ClientConfig(in, hubID, cid)
	if !strings.Contains(e.Conf, "AllowedIPs = 10.99.0.0/24\n") {
		t.Errorf("%s", e.Conf)
	}
	// reachable management: the allowed sources
	m := model.MatrixEndpointManagement(true)
	c.Reachable = &[]model.MatrixEndpoint{{Management: &m}}
	(*wg.Clients)[cid] = c
	_ = n.FromWireGuardNetwork(wg)
	(*in.Config.Networks)[hubID] = n
	e, _ = ClientConfig(in, hubID, cid)
	if !strings.Contains(e.Conf, "192.168.88.0/24") {
		t.Errorf("the management network is in the list of a client that may reach it:\n%s", e.Conf)
	}
}
