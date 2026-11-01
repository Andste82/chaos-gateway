package api_test

import (
	"net/url"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	specpkg "github.com/Andste82/chaos-gateway/api"
)

// The reads the search for secrets covers (P7-M29 audit S13). They are derived from the spec, not listed by hand: every GET of the
// scope read and every GET of the scope full except the downloads that are secret by design, so a read that is added to the spec is
// searched without anybody remembering to add it here. A path parameter or a required query parameter the test cannot fill fails
// the test instead of dropping the operation silently.

// secretReadPathValues are the values of the path parameters: the ids of the fixture where it has the object, and the shape of
// an id for the objects it has not (the answer is then a 404 whose body is searched all the same).
var secretReadPathValues = map[string]string{
	"revisionId": "1", "networkId": hubID, "clientId": rAID,
	"kind": "device", "objectId": espID,
	"tokenId": "00000000-0000-4000-8000-000000000001", "deviceId": espID, "groupId": "00000000-0000-4000-8000-000000000001",
	"ruleId": "00000000-0000-4000-8000-000000000001", "forwardId": "00000000-0000-4000-8000-000000000001",
	"faultId": "00000000-0000-4000-8000-000000000001", "profileId": "00000000-0000-4000-8000-000000000001",
	"scenarioId": "00000000-0000-4000-8000-000000000001", "probeId": "00000000-0000-4000-8000-000000000001",
	"overlayId": "00000000-0000-4000-8000-000000000001", "runId": "00000000-0000-4000-8000-000000000001",
	"captureId": "00000000-0000-4000-8000-000000000001",
}

// secretReadQueryValues are the values of the required query parameters.
var secretReadQueryValues = map[string]string{"base": "1", "src": "10.10.0.5", "dst": "203.0.113.9"}

// secretReadsLeftOut are the reads the search does not make, each with the reason; a stale entry fails the test.
var secretReadsLeftOut = map[string]string{
	"/events":              "a stream that stays open: the test reads the events it collected from the stream it opened first",
	"/runs/{runId}/events": "a stream that stays open",
	// the downloads that are secret by design are the positive control of the test
	"/networks/{networkId}/clients/{clientId}/export": "a secret download by design, searched as the positive control",
	"/networks/{networkId}/export":                    "a secret download by design, searched as the positive control",
}

// secretReads are the paths (with their required query) of the reads for the scope: "read" the operations the scope read may call,
// "full" those and the ones of the scope full, plus the variants of a read that the spec's optional parameters make a different answer
// (the JSON form of an export, a diff, the audit log with its largest page, an explanation of a source) and the health check, which
// needs no scope.
func secretReads(t *testing.T, scope string) []string {
	t.Helper()
	var spec struct {
		Paths      map[string]map[string]yaml.Node `yaml:"paths"`
		Components struct {
			Parameters map[string]struct {
				Name     string `yaml:"name"`
				In       string `yaml:"in"`
				Required bool   `yaml:"required"`
			} `yaml:"parameters"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(specpkg.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	var out []string
	seen := map[string]bool{}
	for path, item := range spec.Paths {
		node, ok := item["get"]
		if !ok {
			continue
		}
		var o struct {
			ID         string `yaml:"operationId"`
			Scope      string `yaml:"x-required-scope"`
			Internal   bool   `yaml:"x-internal"`
			Parameters []struct {
				Ref      string `yaml:"$ref"`
				Name     string `yaml:"name"`
				In       string `yaml:"in"`
				Required bool   `yaml:"required"`
			} `yaml:"parameters"`
		}
		if err := node.Decode(&o); err != nil {
			t.Fatal(err)
		}
		if o.Internal || (o.Scope != "read" && o.Scope != "full") || (scope == "read" && o.Scope != "read") {
			continue
		}
		if _, left := secretReadsLeftOut[path]; left {
			seen[path] = true
			continue
		}
		p := path
		for strings.Contains(p, "{") {
			i, j := strings.IndexByte(p, '{'), strings.IndexByte(p, '}')
			v, known := secretReadPathValues[p[i+1:j]]
			if !known {
				t.Errorf("GET %s (%s): no value for the path parameter %s", path, o.ID, p[i+1:j])
				v = "x"
			}
			p = p[:i] + v + p[j+1:]
		}
		q := url.Values{}
		for _, prm := range o.Parameters {
			if prm.Ref != "" {
				c := spec.Components.Parameters[strings.TrimPrefix(prm.Ref, "#/components/parameters/")]
				prm.Name, prm.In, prm.Required = c.Name, c.In, c.Required
			}
			if prm.In != "query" || !prm.Required {
				continue
			}
			v, known := secretReadQueryValues[prm.Name]
			if !known {
				t.Errorf("GET %s (%s): no value for the required query parameter %s", path, o.ID, prm.Name)
				v = "x"
			}
			q.Set(prm.Name, v)
		}
		if len(q) > 0 {
			p += "?" + q.Encode()
		}
		out = append(out, p)
	}
	for path := range secretReadsLeftOut {
		if !seen[path] && scope == "full" {
			t.Errorf("the read %s is left out of the search and is not in the spec (as a GET of the scope read or full) any more", path)
		}
	}
	out = append(out, "/revisions/1/export?format=json", "/revisions/1/diff?base=1", "/audit?limit=500", "/explain?src=10.10.0.5&dst=203.0.113.9", "/system/health")
	sort.Strings(out)
	if len(out) < 40 {
		t.Errorf("only %d reads derived from the spec", len(out))
	}
	return out
}
