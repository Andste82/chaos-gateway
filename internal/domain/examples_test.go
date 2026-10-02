package domain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func exampleConfiguration(t *testing.T) *model.Configuration {
	t.Helper()
	cfg, err := DecodeConfiguration(readExample(t, "configuration.yaml"), FormatYAML)
	if err != nil {
		t.Fatalf("configuration.yaml: %v", err)
	}
	return cfg
}

func TestTheExampleConfigurationDecodesAndValidates(t *testing.T) {
	cfg := exampleConfiguration(t)
	if len(deref(cfg.Networks)) != 3 || len(deref(cfg.Faults)) != 5 || len(deref(cfg.Scenarios)) != 1 {
		t.Fatalf("networks %d, faults %d, scenarios %d", len(deref(cfg.Networks)), len(deref(cfg.Faults)), len(deref(cfg.Scenarios)))
	}
}
