package capture

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var ethIface = InterfaceInfo{Name: "br-lan", Description: "network lan", Link: LinkEthernet, Snaplen: 262144}
var rawIface = InterfaceInfo{Name: "wg-site", Link: LinkRaw, Snaplen: 262144}

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.UTC)

func writeAll(t testing.TB, format Format, ifaces []InterfaceInfo, pkts []Packet) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, format, ifaces, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkts {
		if err := w.WritePacket(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if int(w.Size()) != buf.Len() {
		t.Fatalf("the writer counts %d bytes, the file has %d", w.Size(), buf.Len())
	}
	return buf.Bytes()
}

func readAll(t testing.TB, b []byte) (*Reader, []Packet) {
	t.Helper()
	r, err := NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var out []Packet
	for {
		p, err := r.Next()
		if errors.Is(err, io.EOF) {
			return r, out
		}
		if err != nil {
			t.Fatalf("after %d packets: %v", len(out), err)
		}
		out = append(out, p)
	}
}

func samplePackets() []Packet {
	return []Packet{
		{Iface: 0, Time: t0, Data: tcpUp(8883).build(), OrigLen: 90},
		{Iface: 0, Time: t0.Add(time.Millisecond), Data: tcpDown(8883).build()[:57], OrigLen: 100, Outgoing: true},
		{Iface: 0, Time: t0.Add(2 * time.Millisecond), Data: []byte{1}, OrigLen: 1},
	}
}

func TestPcapngKeepsPacketsInterfacesTimesAndDirection(t *testing.T) {
	pkts := samplePackets()
	b := writeAll(t, PCAPNG, []InterfaceInfo{ethIface}, pkts)
	r, got := readAll(t, b)
	if r.Format() != PCAPNG || len(got) != 3 {
		t.Fatalf("format %s, %d packets", r.Format(), len(got))
	}
	for i, p := range got {
		if !bytes.Equal(p.Data, pkts[i].Data) || p.OrigLen != pkts[i].OrigLen || !p.Time.Equal(pkts[i].Time) || p.Outgoing != pkts[i].Outgoing {
			t.Errorf("packet %d: %+v, want %+v", i, p, pkts[i])
		}
	}
	if ifs := r.Interfaces(); len(ifs) != 1 || ifs[0] != ethIface {
		t.Errorf("interfaces %+v", ifs)
	}
}

func TestPcapngHoldsInterfacesOfDifferentLinkTypes(t *testing.T) {
	pkts := []Packet{{Iface: 0, Time: t0, Data: tcpUp(1).build(), OrigLen: 60}, {Iface: 1, Time: t0, Data: pk{sa: "10.0.0.1", da: "10.0.0.2", proto: protoUDP, sp: 1, dp: 2}.build(), OrigLen: 30}}
	r, got := readAll(t, writeAll(t, PCAPNG, []InterfaceInfo{ethIface, rawIface}, pkts))
	if len(got) != 2 || got[1].Iface != 1 || r.Interfaces()[1].Link != LinkRaw || r.Interfaces()[1].Name != "wg-site" {
		t.Fatalf("%+v %+v", got, r.Interfaces())
	}
}

func TestPcapKeepsPacketsToTheMicrosecondAndHasOneLinkType(t *testing.T) {
	pkts := samplePackets()
	b := writeAll(t, PCAP, []InterfaceInfo{ethIface}, pkts)
	r, got := readAll(t, b)
	if r.Format() != PCAP || len(got) != 3 || r.Interfaces()[0].Link != LinkEthernet {
		t.Fatalf("format %s, %d packets, %+v", r.Format(), len(got), r.Interfaces())
	}
	for i, p := range got {
		if !bytes.Equal(p.Data, pkts[i].Data) || p.OrigLen != pkts[i].OrigLen || !p.Time.Equal(pkts[i].Time.Truncate(time.Microsecond)) {
			t.Errorf("packet %d: %+v, want %+v", i, p, pkts[i])
		}
	}
	if _, err := NewWriter(io.Discard, PCAP, []InterfaceInfo{ethIface, rawIface}, ""); err == nil || !strings.Contains(err.Error(), "pcapng") {
		t.Errorf("pcap with two link types: %v", err)
	}
	if _, err := NewWriter(io.Discard, "pcapx", []InterfaceInfo{ethIface}, ""); err == nil {
		t.Error("an unknown format is accepted")
	}
	if _, err := NewWriter(io.Discard, PCAP, nil, ""); err == nil {
		t.Error("a file without an interface is accepted")
	}
}

func TestSizesAreWhatTheWritersWrite(t *testing.T) {
	for _, format := range []Format{PCAP, PCAPNG} {
		ifaces := []InterfaceInfo{ethIface}
		pkts := samplePackets()
		b := writeAll(t, format, ifaces, pkts[:0])
		if HeaderSize(format, ifaces, "test") != len(b) {
			t.Errorf("%s: the header is %d bytes, HeaderSize says %d", format, len(b), HeaderSize(format, ifaces, "test"))
		}
		b = writeAll(t, format, ifaces, pkts)
		want := HeaderSize(format, ifaces, "test")
		for _, p := range pkts {
			want += PacketSize(format, len(p.Data), true)
		}
		if len(b) != want {
			t.Errorf("%s: the file has %d bytes, the sizes add up to %d", format, len(b), want)
		}
	}
}

func TestWritePacketRefusesAnUnknownInterface(t *testing.T) {
	w, err := NewWriter(io.Discard, PCAPNG, []InterfaceInfo{ethIface}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WritePacket(Packet{Iface: 3, Time: t0}); err == nil {
		t.Error("a packet of an interface that is not in the file is written")
	}
}

func TestCopyCompleteCutsARecordThatIsBeingWritten(t *testing.T) {
	for _, format := range []Format{PCAP, PCAPNG} {
		pkts := samplePackets()
		b := writeAll(t, format, []InterfaceInfo{ethIface}, pkts)
		full := len(b)
		// a file cut at every length gives only whole records, and never fails
		for cut := 0; cut <= full; cut++ {
			var out bytes.Buffer
			n, err := CopyComplete(&out, bytes.NewReader(b[:cut]), format, false)
			if err != nil || int(n) != out.Len() {
				t.Fatalf("%s cut at %d: %d bytes, %v", format, cut, n, err)
			}
			if out.Len() == 0 {
				continue
			}
			if r, err := NewReader(&out); err != nil {
				t.Fatalf("%s cut at %d: the copy is not a capture file: %v", format, cut, err)
			} else {
				for {
					if _, err := r.Next(); err != nil {
						if !errors.Is(err, io.EOF) {
							t.Fatalf("%s cut at %d: the copy has a broken record: %v", format, cut, err)
						}
						break
					}
				}
			}
		}
		var out bytes.Buffer
		if n, _ := CopyComplete(&out, bytes.NewReader(b), format, false); int(n) != full {
			t.Errorf("%s: a whole file is copied as %d of %d bytes", format, n, full)
		}
	}
}

func TestACopyOfRingFilesIsOneFile(t *testing.T) {
	a := writeAll(t, PCAP, []InterfaceInfo{ethIface}, samplePackets()[:2])
	b := writeAll(t, PCAP, []InterfaceInfo{ethIface}, samplePackets()[1:])
	var out bytes.Buffer
	if _, err := CopyComplete(&out, bytes.NewReader(a), PCAP, false); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyComplete(&out, bytes.NewReader(b), PCAP, true); err != nil {
		t.Fatal(err)
	}
	if _, got := readAll(t, out.Bytes()); len(got) != 4 {
		t.Errorf("%d packets in the joined pcap file, want 4", len(got))
	}
	// pcapng files join by concatenation: each starts a section
	na := writeAll(t, PCAPNG, []InterfaceInfo{ethIface}, samplePackets()[:2])
	nb := writeAll(t, PCAPNG, []InterfaceInfo{ethIface, rawIface}, samplePackets()[1:])
	out.Reset()
	for _, f := range [][]byte{na, nb} {
		if _, err := CopyComplete(&out, bytes.NewReader(f), PCAPNG, true); err != nil {
			t.Fatal(err)
		}
	}
	if r, got := readAll(t, out.Bytes()); len(got) != 4 || len(r.Interfaces()) != 2 {
		t.Errorf("%d packets, %d interfaces in the joined pcapng file", len(got), len(r.Interfaces()))
	}
}

func TestReaderRejectsWhatIsNoCaptureFile(t *testing.T) {
	if _, err := NewReader(strings.NewReader("hello, this is not a capture")); err == nil {
		t.Error("text is read as a capture file")
	}
	if _, err := NewReader(strings.NewReader("ab")); err == nil {
		t.Error("two bytes are read as a capture file")
	}
	b := writeAll(t, PCAPNG, []InterfaceInfo{ethIface}, samplePackets())
	r, _ := NewReader(bytes.NewReader(b[:len(b)-5]))
	var err error
	for err == nil {
		_, err = r.Next()
	}
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("a cut file ends with %v, want ErrTruncated", err)
	}
}

// tshark reads the file with Wireshark's own dissector: the proof that other tools read what is written.
func tsharkPackets(t *testing.T, path string, display string) []string {
	t.Helper()
	exe, err := exec.LookPath("tshark")
	if err != nil {
		t.Skip("tshark is not installed")
	}
	cmd := exec.Command(exe, "-r", path, "-Y", display, "-T", "fields", "-e", "frame.number")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("tshark: %v\n%s", err, stderr.String())
	}
	return strings.Fields(string(out))
}

func TestWiresharkReadsBothFormats(t *testing.T) {
	for _, format := range []Format{PCAP, PCAPNG} {
		path := filepath.Join(t.TempDir(), "x"+format.Ext())
		pkts := []Packet{{Iface: 0, Time: t0, Data: tcpUp(8883).build(), OrigLen: 90}, {Iface: 0, Time: t0.Add(time.Second), Data: tcpDown(8883).build(), OrigLen: 90}}
		if err := os.WriteFile(path, writeAll(t, format, []InterfaceInfo{ethIface}, pkts), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := tsharkPackets(t, path, "tcp.port == 8883"); len(got) != 2 {
			t.Errorf("%s: tshark finds %v packets of port 8883, want 2", format, got)
		}
	}
	// a raw IP interface next to an Ethernet one in one pcapng file
	path := filepath.Join(t.TempDir(), "x.pcapng")
	pkts := []Packet{{Iface: 0, Time: t0, Data: tcpUp(8883).build(), OrigLen: 90}, {Iface: 1, Time: t0, Data: pk{sa: "10.20.0.2", da: "203.0.113.10", proto: protoUDP, sp: 5000, dp: 53}.build(), OrigLen: 40}}
	if err := os.WriteFile(path, writeAll(t, PCAPNG, []InterfaceInfo{ethIface, rawIface}, pkts), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tsharkPackets(t, path, "udp.port == 53"); len(got) != 1 {
		t.Errorf("tshark does not dissect the raw IP packet: %v", got)
	}
}
