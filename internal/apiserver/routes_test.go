package apiserver_test

import (
	"os"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/apiserver"
)

// serverStub implements ServerInterface by embedding it: only the registration of the routes is
// under test, no handler is ever called.
type serverStub struct{ apiserver.ServerInterface }

// specOperations reads api/openapi.yaml and returns "METHOD /path" for every operation.
func specOperations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{"get": "GET", "put": "PUT", "post": "POST", "delete": "DELETE", "patch": "PATCH"}
	var ops []string
	for path, item := range spec.Paths {
		for key := range item {
			if m, ok := methods[key]; ok {
				ops = append(ops, m+" "+path)
			}
		}
	}
	sort.Strings(ops)
	return ops
}

// ginPath turns an OpenAPI path ("/runs/{runId}") into a Gin path ("/runs/:runId").
func ginPath(p string) string {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '{':
			out = append(out, ':')
		case '}':
		default:
			out = append(out, p[i])
		}
	}
	return string(out)
}

func TestEveryOperationOfTheSpecIsRegisteredWithGin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	apiserver.RegisterHandlersWithOptions(router, serverStub{}, apiserver.GinServerOptions{BaseURL: "/api/v1"})

	registered := map[string]bool{}
	for _, r := range router.Routes() {
		registered[r.Method+" "+r.Path] = true
	}
	ops := specOperations(t)
	if len(ops) < 80 {
		t.Fatalf("only %d operations found in the spec: the test reads it wrongly", len(ops))
	}
	for _, op := range ops {
		method, path, _ := splitOp(op)
		if want := method + " /api/v1" + ginPath(path); !registered[want] {
			t.Errorf("%s is not registered (want %s)", op, want)
		}
	}
	if len(registered) != len(ops) {
		t.Errorf("%d routes registered for %d operations in the spec", len(registered), len(ops))
	}
}

func splitOp(op string) (method, path string, ok bool) {
	for i := 0; i < len(op); i++ {
		if op[i] == ' ' {
			return op[:i], op[i+1:], true
		}
	}
	return op, "", false
}
