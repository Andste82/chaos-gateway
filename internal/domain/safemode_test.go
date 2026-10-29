package domain_test

import (
	"os"
	"reflect"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
)

// The safe configuration keeps the management access and nothing that forwards (plan §3.9).
func TestTheSafeModeConfigurationKeepsTheManagementAccessAndNothingElse(t *testing.T) {
	raw, err := os.ReadFile("../compiler/testdata/gateway.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Networks == nil || len(*cfg.Networks) == 0 {
		t.Fatal("the fixture has no network")
	}
	safe := domain.SafeModeConfiguration(cfg)
	if !reflect.DeepEqual(safe.Management, cfg.Management) || !reflect.DeepEqual(safe.Uplink, cfg.Uplink) || safe.SchemaVersion != cfg.SchemaVersion {
		t.Errorf("management %+v, uplink %+v", safe.Management, safe.Uplink)
	}
	if safe.Networks != nil || safe.Devices != nil || safe.Groups != nil || safe.Faults != nil || safe.AccessRules != nil || safe.AccessRuleOrder != nil ||
		safe.AccessMatrix != nil || safe.PortForwards != nil || safe.Probes != nil || safe.Profiles != nil || safe.Scenarios != nil || safe.Routing != nil || safe.Secrets != nil {
		t.Errorf("the safe configuration holds more than the management: %+v", safe)
	}
	if len(*cfg.Networks) == 0 {
		t.Error("the configuration it was made of was modified")
	}
	if errs := domain.Validate(safe); len(errs) != 0 {
		t.Errorf("the safe configuration is not valid: %v", errs)
	}
}
