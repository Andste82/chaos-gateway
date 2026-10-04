package linux

import "testing"

func TestParseConntrack(t *testing.T) {
	text := `tcp      6 431999 ESTABLISHED src=10.10.0.10 dst=203.0.113.10 sport=45566 dport=80 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=80 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1
udp      17 25 src=10.10.0.10 dst=10.10.0.1 sport=40000 dport=53 [UNREPLIED] src=10.10.0.1 dst=10.10.0.10 sport=53 dport=40000 mark=5 use=1
ipv4     2 icmp     1 29 src=10.10.0.10 dst=203.0.113.10 type=8 code=0 id=7 src=203.0.113.10 dst=10.10.0.10 type=0 code=0 id=7 mark=0 use=1
conntrack v1.4.8 (conntrack-tools): 3 flow entries have been shown.
garbage line
`
	got := ParseConntrack(text)
	if len(got) != 3 {
		t.Fatalf("%d flows: %+v", len(got), got)
	}
	tcp := got[0]
	if tcp.Proto != "tcp" || tcp.State != "ESTABLISHED" || tcp.TimeoutSeconds != 431999 || tcp.Original.Src != "10.10.0.10" || tcp.Original.DPort != 80 ||
		tcp.Original.Packets != 6 || tcp.Original.Bytes != 412 || tcp.Reply.Bytes != 500 || len(tcp.Flags) != 1 || tcp.Flags[0] != "ASSURED" {
		t.Errorf("%+v", tcp)
	}
	// a NATed connection: the reply is addressed to the translated source
	if tcp.Reply.Dst != "203.0.113.1" || tcp.Reply.Src != "203.0.113.10" {
		t.Errorf("%+v", tcp.Reply)
	}
	udp := got[1]
	if udp.Proto != "udp" || udp.State != "" || udp.Original.DPort != 53 || udp.Flags[0] != "UNREPLIED" || udp.Mark != 5 || udp.Reply.Src != "10.10.0.1" {
		t.Errorf("%+v", udp)
	}
	icmp := got[2]
	if icmp.Proto != "icmp" || icmp.Original.Dst != "203.0.113.10" || icmp.TimeoutSeconds != 29 {
		t.Errorf("%+v", icmp)
	}
	// M6a-13: the echo type, code and id are parsed, for both tuples
	if icmp.Original.ICMPType == nil || *icmp.Original.ICMPType != 8 || icmp.Original.ICMPCode == nil || *icmp.Original.ICMPCode != 0 ||
		icmp.Original.ICMPID == nil || *icmp.Original.ICMPID != 7 {
		t.Errorf("original tuple: %+v", icmp.Original)
	}
	if icmp.Reply.ICMPType == nil || *icmp.Reply.ICMPType != 0 || icmp.Reply.ICMPID == nil || *icmp.Reply.ICMPID != 7 {
		t.Errorf("reply tuple: %+v", icmp.Reply)
	}
	if len(ParseConntrack("")) != 0 {
		t.Error("an empty output has flows")
	}
}

func TestParseNeighbors(t *testing.T) {
	n, err := ParseNeighbors([]byte(`[{"dst":"10.10.0.10","dev":"br-iot","lladdr":"02:00:00:00:00:aa","state":["REACHABLE"]},{"dst":"10.10.0.11","dev":"br-iot","state":["FAILED"]},{"dst":"10.10.0.12","dev":"br-iot","lladdr":"02:00:00:00:00:bb","state":["STALE"]}]`))
	if err != nil || len(n) != 3 {
		t.Fatalf("%v %v", n, err)
	}
	if !n[0].Usable() || n[0].Stale() || n[1].Usable() || !n[2].Usable() || !n[2].Stale() {
		t.Errorf("%+v", n)
	}
	if n, err := ParseNeighbors(nil); err != nil || n != nil {
		t.Error("an empty output")
	}
	if _, err := ParseNeighbors([]byte("not json")); err == nil {
		t.Error("garbage is accepted")
	}
}
