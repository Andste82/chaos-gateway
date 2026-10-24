package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// warnings decodes the document (it must be valid: a warning never stops the apply) and returns its warnings.
func (d doc) warnings(t *testing.T) []Warning {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DecodeConfiguration(raw, FormatJSON)
	if err != nil {
		t.Fatalf("the document must stay valid: %v", err)
	}
	return Warnings(cfg)
}

func TestFixedIPInPool(t *testing.T) {
	inPool := []struct {
		name   string
		change func(t *testing.T, d doc)
		in     string // what the message names the pool
	}{
		{"inside the pool", func(t *testing.T, d doc) { d.set(t, "10.10.0.150", "devices", idESP, "fixed_ip") }, "pool 0 (10.10.0.100-10.10.0.199)"},
		{"the pool's first address", func(t *testing.T, d doc) { d.set(t, "10.10.0.100", "devices", idESP, "fixed_ip") }, "pool 0"},
		{"the pool's last address", func(t *testing.T, d doc) { d.set(t, "10.10.0.199", "devices", idESP, "fixed_ip") }, "pool 0"},
		{"the second pool", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"start":"10.10.0.100","end":"10.10.0.119"}`), obj(t, `{"start":"10.10.0.40","end":"10.10.0.49"}`)}, "networks", idIoT, "dhcp", "pools")
		}, "pool 1 (10.10.0.40-10.10.0.49)"},
		{"a pool that grew over the reservation", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"start":"10.10.0.10","end":"10.10.0.199"}`)}, "networks", idIoT, "dhcp", "pools")
		}, "pool 0 (10.10.0.10-10.10.0.199)"},
		{"the default pool, the second half of the subnet", func(t *testing.T, d doc) {
			d.del(t, "networks", idIoT, "dhcp", "pools")
			d.set(t, "10.10.0.200", "devices", idESP, "fixed_ip")
		}, "the default pool (10.10.0.128-10.10.0.254)"},
		{"the default pool before a gateway inside the second half (#110)", func(t *testing.T, d doc) {
			d.del(t, "networks", idIoT, "dhcp", "pools")
			d.set(t, "10.10.0.200/24", "networks", idIoT, "address")
			d.set(t, "10.10.0.150", "devices", idESP, "fixed_ip")
		}, "the default pool (10.10.0.128-10.10.0.199)"},
		{"the default pool after a gateway inside the second half (#110)", func(t *testing.T, d doc) {
			d.del(t, "networks", idIoT, "dhcp", "pools")
			d.set(t, "10.10.0.200/24", "networks", idIoT, "address")
			d.set(t, "10.10.0.201", "devices", idESP, "fixed_ip")
		}, "the default pool (10.10.0.201-10.10.0.254)"},
		{"the network named by its UUID", func(t *testing.T, d doc) {
			d.set(t, idIoT, "devices", idESP, "network")
			d.set(t, "10.10.0.150", "devices", idESP, "fixed_ip")
		}, "pool 0"},
		{"DHCP switched on explicitly", func(t *testing.T, d doc) {
			d.set(t, true, "networks", idIoT, "dhcp", "enabled")
			d.set(t, "10.10.0.150", "devices", idESP, "fixed_ip")
		}, "pool 0"},
	}
	for _, tt := range inPool {
		t.Run(tt.name, func(t *testing.T) {
			d := baseDoc(t)
			tt.change(t, d)
			ws := d.warnings(t)
			if len(ws) != 1 || ws[0].Path != esp+"/fixed_ip" || ws[0].Code != CodeFixedIPInPool {
				t.Fatalf("want one %s at %s/fixed_ip, got %+v", CodeFixedIPInPool, esp, ws)
			}
			for _, part := range []string{`device "esp32-42"`, tt.in, `network "IoT"`, "until that lease is released"} {
				if !strings.Contains(ws[0].Message, part) {
					t.Errorf("the message %q does not say %q", ws[0].Message, part)
				}
			}
		})
	}

	outside := []struct {
		name   string
		change func(t *testing.T, d doc)
	}{
		{"the example: the reservation is below the pool", func(*testing.T, doc) {}},
		{"just below the pool", func(t *testing.T, d doc) { d.set(t, "10.10.0.99", "devices", idESP, "fixed_ip") }},
		{"just above the pool", func(t *testing.T, d doc) { d.set(t, "10.10.0.200", "devices", idESP, "fixed_ip") }},
		{"the first half of the subnet, with the default pool", func(t *testing.T, d doc) { d.del(t, "networks", idIoT, "dhcp", "pools") }},
		{"DHCP off: no reservation is made", func(t *testing.T, d doc) {
			d.set(t, false, "networks", idIoT, "dhcp", "enabled")
			d.set(t, "10.10.0.150", "devices", idESP, "fixed_ip")
		}},
		{"no DHCP scope at all", func(t *testing.T, d doc) {
			d.del(t, "networks", idIoT, "dhcp")
			d.set(t, "10.10.0.150", "devices", idESP, "fixed_ip")
		}},
		{"no fixed address", func(t *testing.T, d doc) {
			d.del(t, "devices", idESP, "fixed_ip")
			d.set(t, []any{obj(t, `{"start":"10.10.0.2","end":"10.10.0.254"}`)}, "networks", idIoT, "dhcp", "pools")
		}},
	}
	for _, tt := range outside {
		t.Run(tt.name, func(t *testing.T) {
			d := baseDoc(t)
			tt.change(t, d)
			for _, w := range d.warnings(t) {
				if w.Code == CodeFixedIPInPool {
					t.Fatalf("want no %s, got %+v", CodeFixedIPInPool, w)
				}
			}
		})
	}
}

// A fixed address in a network with DHCP off gets a warning at the field, whether or not another network
// serves DHCP: the compiler makes no reservation for it (#94).
func TestFixedIPDHCPOff(t *testing.T) {
	off := []struct {
		name   string
		change func(t *testing.T, d doc)
	}{
		{"DHCP switched off", func(t *testing.T, d doc) { d.set(t, false, "networks", idIoT, "dhcp", "enabled") }},
		{"no DHCP scope at all", func(t *testing.T, d doc) { d.del(t, "networks", idIoT, "dhcp") }},
		{"the network named by its UUID", func(t *testing.T, d doc) {
			d.del(t, "networks", idIoT, "dhcp")
			d.set(t, idIoT, "devices", idESP, "network")
		}},
	}
	for _, tt := range off {
		t.Run(tt.name, func(t *testing.T) {
			d := baseDoc(t)
			tt.change(t, d)
			ws := d.warnings(t)
			if len(ws) != 1 || ws[0].Path != esp+"/fixed_ip" || ws[0].Code != CodeFixedIPDHCPOff {
				t.Fatalf("want one %s at %s/fixed_ip, got %+v", CodeFixedIPDHCPOff, esp, ws)
			}
			for _, part := range []string{`device "esp32-42"`, "10.10.0.42", `DHCP is off in its network "IoT"`, "no reservation is made"} {
				if !strings.Contains(ws[0].Message, part) {
					t.Errorf("the message %q does not say %q", ws[0].Message, part)
				}
			}
		})
	}

	on := []struct {
		name   string
		change func(t *testing.T, d doc)
	}{
		{"the example: DHCP on", func(*testing.T, doc) {}},
		{"DHCP switched on explicitly", func(t *testing.T, d doc) { d.set(t, true, "networks", idIoT, "dhcp", "enabled") }},
		{"no fixed address", func(t *testing.T, d doc) {
			d.del(t, "networks", idIoT, "dhcp")
			d.del(t, "devices", idESP, "fixed_ip")
		}},
	}
	for _, tt := range on {
		t.Run(tt.name, func(t *testing.T) {
			d := baseDoc(t)
			tt.change(t, d)
			if ws := d.warnings(t); len(ws) != 0 {
				t.Fatalf("want no warning, got %+v", ws)
			}
		})
	}
}

// One warning per device, sorted by path; a reservation the validation refuses gets none.
func TestFixedIPInPoolSeveralDevices(t *testing.T) {
	d := baseDoc(t)
	d.set(t, "10.10.0.150", "devices", idESP, "fixed_ip")
	d.set(t, obj(t, `{"name":"cam-1","identifiers":{"macs":["24:0a:c4:00:00:43"]},"network":"IoT","fixed_ip":"10.10.0.151"}`), "devices", idNew)
	ws := d.warnings(t)
	if len(ws) != 2 || ws[0].Path != esp+"/fixed_ip" || ws[1].Path != "/devices/"+idNew+"/fixed_ip" {
		t.Fatalf("want warnings at %s/fixed_ip and /devices/%s/fixed_ip, got %+v", esp, idNew, ws)
	}

	// what the validation refuses gives no warning (and no panic): an unknown network, an address that is none
	for _, dev := range []string{
		`{"name":"x","identifiers":{"macs":["24:0a:c4:00:00:43"]},"network":"nowhere","fixed_ip":"10.10.0.150"}`,
		`{"name":"x","identifiers":{"macs":["24:0a:c4:00:00:43"]},"network":"IoT","fixed_ip":"10.10.0.300"}`,
		`{"name":"x","identifiers":{"macs":["24:0a:c4:00:00:43"]},"network":"lab-hub","fixed_ip":"10.99.0.150"}`,
	} {
		d := baseDoc(t)
		d.del(t, "devices", idESP, "fixed_ip")
		d.set(t, obj(t, dev), "devices", idNew)
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var cfg model.Configuration
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		if ws := Warnings(&cfg); len(ws) != 0 {
			t.Errorf("%s: want no warning, got %+v", dev, ws)
		}
	}
}
