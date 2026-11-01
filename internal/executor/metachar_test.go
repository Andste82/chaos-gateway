package executor

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The deterministic guard of the validators (P7-M29 audit S5): no string field of any operation takes a shell
// metacharacter, a space, a line break or a leading '-' unless the field is one of the few that carry text by design.
// The fuzz job is the net; it did not find a one-character mutation of devName (a validator that let `;` through,
// `ethtool -K wan0;id`) in 150 seconds, so the seeds below start the fuzzer at those inputs and this test needs no
// fuzzing: it changes every string field of every seed in each of the ways and demands that the decoder refuses.

// metacharMutations are the ways one string field is made to carry a metacharacter.
var metacharMutations = map[string]func(string) string{
	"semicolon":    func(v string) string { return v + ";id" },
	"dollar":       func(v string) string { return v + "$x" },
	"backtick":     func(v string) string { return v + "`id`" },
	"space":        func(v string) string { return v + " id" },
	"newline":      func(v string) string { return v + "\nid" },
	"leading dash": func(v string) string { return "-" + v },
}

// textFields are the fields that carry text by design, as "<operation type> <path>" with the indexes of the lists as []: the text
// is written to a file or to the standard input of a tool, never to a command line, or is checked by a grammar of its own.
var textFields = map[string]string{
	"bird .config": "the BIRD configuration is a file; CheckText reads it (TestDecodeBirdRejects)",
	// the measurements of probe_run run in this process, inside the probe's namespace (internal/probe): the fields below are the request of
	// a diagnostic, checked by domain.ValidateDiagnosticRequest, and become an HTTP request, a DNS question or a datagram, never a command
	"probe_run .roots_pem":          "a PEM text for the measurement, which parses it",
	"probe_run .request.url":        "the URL of an HTTP diagnostic",
	"probe_run .request.from.probe": "the probe the API chose for the diagnostic, a reference the executor does not use",
	"probe_run .user_agent":         "the User-Agent header of an HTTP diagnostic",
	"probe_run .request.payload":    "the payload of a UDP diagnostic",
	"probe_run .request.dns_name":   "the name a DNS diagnostic asks for",
	// the names of the objects of a ruleset are strings of a JSON document on the standard input of nft: data, no command line. What
	// the ruleset may touch is the scope check of CheckNftRuleset (the table), not a character class.
	"nft_apply .ruleset.nftables[].add.chain.name":   "the name of a chain in the JSON ruleset",
	"nft_apply .ruleset.nftables[].add.chain.type":   "the type of a chain in the JSON ruleset, which nft checks",
	"nft_apply .ruleset.nftables[].add.chain.hook":   "the hook of a chain in the JSON ruleset, which nft checks",
	"nft_apply .ruleset.nftables[].add.chain.policy": "the policy of a chain in the JSON ruleset, which nft checks",
	"nft_apply .ruleset.nftables[].add.rule.chain":   "the chain a rule is added to, in the JSON ruleset",
}

// walkStrings calls f for every string below v with its path ("[]" for every index of a list).
func walkStrings(v any, path string, f func(path string, s string, set func(string))) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, ok := e.(string); ok {
				k := k
				f(path+"."+k, s, func(n string) { x[k] = n })
				continue
			}
			walkStrings(e, path+"."+k, f)
		}
	case []any:
		for i, e := range x {
			if s, ok := e.(string); ok {
				i := i
				f(path+"[]", s, func(n string) { x[i] = n })
				continue
			}
			walkStrings(e, path+"[]", f)
		}
	}
}

// metacharCases are the seeds changed in one string field each way: every decodable seed, every string field by its path (the first
// instance of the path), every mutation. The case is a JSON document and what it was made of.
type metacharCase struct{ typ, path, mutation, doc string }

func metacharCases(t testing.TB) []metacharCase {
	t.Helper()
	var out []metacharCase
	seen := map[string]bool{}
	for _, seed := range fuzzSeeds {
		if _, err := Decode([]byte(seed)); err != nil {
			continue
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(seed), &probe); err != nil {
			continue
		}
		typ, _ := probe["type"].(string)
		var paths []string
		walkStrings(probe, "", func(path, s string, _ func(string)) { paths = append(paths, path) })
		for _, path := range paths {
			if path == ".type" || seen[typ+" "+path] {
				continue
			}
			seen[typ+" "+path] = true
			names := make([]string, 0, len(metacharMutations))
			for name := range metacharMutations {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				var doc map[string]any
				if err := json.Unmarshal([]byte(seed), &doc); err != nil {
					t.Fatal(err)
				}
				done := false
				walkStrings(doc, "", func(p, s string, set func(string)) {
					if p == path && !done {
						set(metacharMutations[name](s))
						done = true
					}
				})
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, metacharCase{typ, path, name, string(raw)})
			}
		}
	}
	return out
}

func TestNoStringFieldOfAnyOperationTakesAMetacharacter(t *testing.T) {
	cases := metacharCases(t)
	fields := map[string]bool{}
	for _, c := range cases {
		fields[c.typ+" "+c.path] = true
	}
	if len(fields) < 80 {
		t.Fatalf("only %d string fields in the seeds", len(fields))
	}
	acceptsText := map[string]bool{}
	for _, c := range cases {
		key := c.typ + " " + c.path
		_, err := Decode([]byte(c.doc))
		if _, text := textFields[key]; text {
			acceptsText[key] = acceptsText[key] || err == nil
		} else if err == nil {
			t.Errorf("%s: the %s variant is accepted: %s", key, c.mutation, c.doc)
		}
	}
	for key, why := range textFields {
		if !fields[key] {
			t.Errorf("%q (%s) is no string field of the seeds any more", key, why)
		} else if !acceptsText[key] {
			t.Errorf("%q (%s) refuses every variant: it is no text field and needs no entry", key, why)
		}
	}
}

// The metacharacter variants of every string field are seeds of FuzzDecode, so that a validator that lets one through is found by
// the seed run of `go test` and the fuzzer starts at the edge of every character class.
func metacharSeeds(t testing.TB) []string {
	var out []string
	for _, c := range metacharCases(t) {
		out = append(out, c.doc)
	}
	return out
}

// jsonStringPaths adds to out the paths (".a.b[].c") of the string fields below a type, by the names of the JSON tags.
func jsonStringPaths(rt reflect.Type, prefix string, out map[string]bool) {
	for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
		if rt.Kind() == reflect.Slice {
			prefix += "[]"
		}
		rt = rt.Elem()
	}
	switch rt.Kind() {
	case reflect.String:
		out[prefix] = true
	case reflect.Struct:
		for i := range rt.NumField() {
			f := rt.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			switch {
			case name == "-" || !f.IsExported():
			case f.Anonymous && name == "":
				jsonStringPaths(f.Type, prefix, out)
			default:
				if name == "" {
					name = f.Name
				}
				jsonStringPaths(f.Type, prefix+"."+name, out)
			}
		}
	}
}

// Every string field of every operation is in a seed, so that the test above and the seeds of the fuzzer reach it: a field added to an
// operation without a seed fails here.
func TestEveryStringFieldOfEveryOperationIsInASeed(t *testing.T) {
	covered := map[string]bool{}
	types := map[string]reflect.Type{}
	for _, c := range metacharCases(t) {
		covered[c.typ+" "+c.path] = true
	}
	for _, seed := range fuzzSeeds {
		var env struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(seed), &env)
		if op, err := Decode([]byte(seed)); err == nil {
			types[env.Type] = reflect.TypeOf(op).Elem()
		}
	}
	for typ, rt := range types {
		paths := map[string]bool{}
		jsonStringPaths(rt, "", paths)
		for path := range paths {
			if path == ".type" || covered[typ+" "+path] || (typ == "bird" && path == ".namespace") { // the BIRD instance is not namespaced
				continue
			}
			t.Errorf("no seed has the string field %s of %s", path, typ)
		}
	}
}
