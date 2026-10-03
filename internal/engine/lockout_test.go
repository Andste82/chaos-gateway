package engine

import (
	"encoding/json"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func cfgFrom(t *testing.T, doc string) *model.Configuration {
	t.Helper()
	var c model.Configuration
	if err := json.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

const baseCfg = `{"schema_version":1,"uplink":{"interface":{"name":"wan0"}},"management":{"interface":{"name":"mgmt0"},"allowed_sources":["192.168.56.0/24"]}}`

func TestLockoutRelevance(t *testing.T) {
	for name, c := range map[string]struct {
		cur, next string
		want      bool
	}{
		"first revision":            {"", baseCfg, false},
		"no change":                 {baseCfg, baseCfg, false},
		"ui port":                   {baseCfg, `{"schema_version":1,"uplink":{"interface":{"name":"wan0"}},"management":{"interface":{"name":"mgmt0"},"allowed_sources":["192.168.56.0/24"],"ui_port":8443}}`, true},
		"allowed sources":           {baseCfg, `{"schema_version":1,"uplink":{"interface":{"name":"wan0"}},"management":{"interface":{"name":"mgmt0"},"allowed_sources":["10.0.0.0/8"]}}`, true},
		"management interface":      {baseCfg, `{"schema_version":1,"uplink":{"interface":{"name":"wan0"}},"management":{"interface":{"name":"mgmt1"},"allowed_sources":["192.168.56.0/24"]}}`, true},
		"uplink gateway":            {baseCfg, `{"schema_version":1,"uplink":{"interface":{"name":"wan0"},"gateway":"203.0.113.9"},"management":{"interface":{"name":"mgmt0"},"allowed_sources":["192.168.56.0/24"]}}`, false},
		"uplink with two ports":     {`{"schema_version":1,"uplink":{"interface":{"name":"wan0"}},"management":{"interface":{"name":"wan0"}}}`, `{"schema_version":1,"uplink":{"interface":{"name":"wan0"},"gateway":"203.0.113.9"},"management":{"interface":{"name":"wan0"}}}`, true},
		"other uplink with 2 ports": {`{"schema_version":1,"uplink":{"interface":{"name":"wan0"}},"management":{"interface":{"name":"wan0"}}}`, `{"schema_version":1,"uplink":{"interface":{"name":"wan1"}},"management":{"interface":{"name":"wan0"}}}`, true},
		"dns upstream only":         {baseCfg, `{"schema_version":1,"uplink":{"interface":{"name":"wan0"},"dns_upstream":["9.9.9.9"]},"management":{"interface":{"name":"mgmt0"},"allowed_sources":["192.168.56.0/24"]}}`, false},
	} {
		var cur *model.Configuration
		if c.cur != "" {
			cur = cfgFrom(t, c.cur)
		}
		if got := LockoutRelevant(cur, cfgFrom(t, c.next)); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestAManagementRoleWireGuardNetworkIsLockoutRelevant(t *testing.T) {
	hub := func(role string, port int) string {
		return `{"schema_version":1,"uplink":{"interface":{"name":"wan0"}},"management":{"interface":{"name":"mgmt0"}},"networks":{"4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54":{"type":"wireguard","kind":"hub","name":"hub","role":"` + role + `","address":"10.99.0.1/24","listen_port":` + itoa(port) + `}}}`
	}
	if !LockoutRelevant(cfgFrom(t, baseCfg), cfgFrom(t, hub("management", 51820))) {
		t.Error("adding a management hub")
	}
	if !LockoutRelevant(cfgFrom(t, hub("management", 51820)), cfgFrom(t, hub("management", 51821))) {
		t.Error("changing a management hub")
	}
	if LockoutRelevant(cfgFrom(t, hub("test", 51820)), cfgFrom(t, hub("test", 51821))) {
		t.Error("a test hub cannot lock the administrator out")
	}
	if !LockoutRelevant(cfgFrom(t, hub("management", 51820)), cfgFrom(t, baseCfg)) {
		t.Error("removing a management hub")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
