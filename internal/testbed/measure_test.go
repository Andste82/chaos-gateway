package testbed

import "testing"

func TestASNMPCounterIsReadFromTheTwoLinesOfItsProtocol(t *testing.T) {
	text := "Ip: Forwarding DefaultTTL InReceives InHdrErrors\nIp: 1 64 100 7\nUdp: InDatagrams NoPorts InErrors OutDatagrams RcvbufErrors SndbufErrors InCsumErrors\nUdp: 5 1 3 9 0 0 2\n"
	for _, tc := range []struct {
		proto, name string
		want        int64
	}{{"Ip", "InHdrErrors", 7}, {"Ip", "InReceives", 100}, {"Udp", "InCsumErrors", 2}, {"Udp", "InDatagrams", 5}} {
		got, err := snmpCounter(text, tc.proto, tc.name)
		if err != nil || got != tc.want {
			t.Errorf("%s %s: %d, %v; want %d", tc.proto, tc.name, got, err, tc.want)
		}
	}
	if _, err := snmpCounter(text, "Tcp", "RetransSegs"); err == nil {
		t.Error("a protocol that is not there is an error, not zero")
	}
}
