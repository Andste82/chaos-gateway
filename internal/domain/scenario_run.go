package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// This file is what the engine of runs (M15, internal/run) needs from the domain: the overlay a
// step writes, the parameters that change a scenario before it is checked, the run request, and the
// objects a revision would take from a run.

// CodeInvalidParameter is the code of a parameter (`POST /runs`, `parameters`) that cannot be applied
// to the scenario.
const CodeInvalidParameter = "invalid_parameter"

// StepCreatesOverlay reports whether a step writes an overlay of the run (the others wait, remove or restore).
func StepCreatesOverlay(st *model.Step) bool {
	k := stepKind(*st)
	return len(k) == 1 && createsOverlay(k[0])
}

// StepKind returns the kind of a step ("fault", "wait", "restore", ...), "" for a step that does
// not have exactly one.
func StepKind(st *model.Step) string { return firstKind(*st) }

// CheckKind returns the type of a check ("reconnected", "dns_query_seen", ...), "" for a check that
// does not have exactly one.
func CheckKind(c *model.Check) string {
	kind := ""
	n := 0
	for name, set := range map[string]bool{
		"reconnected": c.Reconnected != nil, "no_connection": c.NoConnection != nil, "traffic_seen": c.TrafficSeen != nil,
		"traffic_not_seen": c.TrafficNotSeen != nil, "dns_query_seen": c.DnsQuerySeen != nil,
		"tls_rejected": c.TlsRejected != nil, "tls_accepted": c.TlsAccepted != nil,
	} {
		if set {
			kind = name
			n++
		}
	}
	if n != 1 {
		return ""
	}
	return kind
}

// TimelineOrder returns the indexes of the steps in the order a run executes them: by `at`, and in
// list order for steps with the same `at` (plan §2.10).
func TimelineOrder(steps []model.Step) []int { return timelineOrder(steps) }

// StepRequest returns the overlay write a step makes: the step's body with its target (the
// step's own, else the scenario's). A tunnel fault and a WireGuard action have no target; they name
// the tunnel. The scenario must have passed ResolveScenario. The error is for a step that creates
// no overlay.
func StepRequest(sc *model.Scenario, st *model.Step) (model.OverlayRequest, error) {
	if !StepCreatesOverlay(st) {
		return model.OverlayRequest{}, fmt.Errorf("the step %q (%s) writes no overlay", st.Id, firstKind(*st))
	}
	req := model.OverlayRequest{Profile: st.Profile, Fault: st.Fault, Rule: st.Rule, Dns: st.Dns, Tls: st.Tls, Dhcp: st.Dhcp, Wireguard: st.Wireguard}
	target := sc.Target
	if st.Target != nil {
		target = *st.Target
	}
	if st.Wireguard == nil && !isTunnelFault(st.Fault) {
		req.Target = &target
	}
	return clone(req), nil
}

// ScenarioUsesDNS reports whether a scenario depends on the device resolving names through the
// gateway: it has a DNS step, waits for a DNS query, or names a host name as the destination of a
// step or a check (plan §2.10, precondition `uses_gateway_dns`).
func ScenarioUsesDNS(sc *model.Scenario) bool {
	named := func(d *model.Destination) bool { return d != nil && d.Hostname != nil }
	for i := range sc.Steps {
		st := &sc.Steps[i]
		switch {
		case st.Dns != nil:
			return true
		case st.Fault != nil && named(st.Fault.Destination):
			return true
		case st.Rule != nil && named(st.Rule.Destination):
			return true
		case st.Tls != nil && named(st.Tls.Destination):
			return true
		case st.Wait != nil && (st.Wait.For == model.WaitConditionForDnsQuery || named(st.Wait.Destination)):
			return true
		}
	}
	for _, c := range deref(sc.Checks) {
		if c.DnsQuerySeen != nil {
			return true
		}
		for _, m := range []*model.TrafficMatch{c.Reconnected, c.NoConnection, c.TrafficSeen, c.TlsRejected, c.TlsAccepted} {
			if m != nil && named(m.Destination) {
				return true
			}
		}
		if c.TrafficNotSeen != nil {
			if named(c.TrafficNotSeen.Destination) {
				return true
			}
			for i := range deref(c.TrafficNotSeen.Except) {
				if named(&(*c.TrafficNotSeen.Except)[i]) {
					return true
				}
			}
		}
	}
	return false
}

// OrphanedScenario returns the JSON pointers of the objects of current that a scenario refers to
// and next does not have, sorted: what applying next in place of current takes from a queued or
// running run (plan §2.1.1). The scenario is a run's snapshot (ResolveScenario), so its references
// are UUIDs. Discovered devices pass as for OrphanedOverlays.
func OrphanedScenario(current, next *model.Configuration, sc *model.Scenario, opts ...Option) []string {
	oldIdx, _ := BuildIndex(current)
	newIdx, _ := newIndex(next, collectOptions(opts))
	seen := map[string]bool{}
	visitScenario("", sc, func(_ string, kind Kind, ref *string) {
		id := lower(*ref)
		if !oldIdx.exists(kind, id) || newIdx.exists(kind, id) {
			return
		}
		seen[objectPointer(oldIdx, kind, id)] = true
	})
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ---- parameters

var wholeNumber = regexp.MustCompile(`^-?[0-9]+$`)

// ApplyParameters changes a scenario document (the generic values of ParseDocument) by dotted
// paths (RunOptions.parameters). A segment names a field, or an element of a list by its position
// or by its `id`; an object that does not exist yet is made on the way. A value is a string, except
// that the value of a field that is a boolean or a number there is read as such, and so are `true`,
// `false` and whole numbers where the field does not exist yet, and a value that starts with `[` or
// `{` is JSON. A member of a `target` replaces the whole target. The problems are reported at
// /parameters/<path>; the parameters are applied in the order of their paths.
func ApplyParameters(doc any, params map[string]string) []model.ValidationError {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []model.ValidationError
	for _, k := range keys {
		if err := applyParameter(doc, k, params[k]); err != nil {
			errs = append(errs, model.ValidationError{Path: schema.Pointer("/parameters", k), Code: CodeInvalidParameter, Message: err.Error()})
		}
	}
	return errs
}

func applyParameter(doc any, path, value string) error {
	segs := strings.Split(path, ".")
	for _, s := range segs {
		if s == "" {
			return fmt.Errorf("%q is not a path of dotted field names", path)
		}
	}
	cur := doc
	for i, seg := range segs {
		last := i == len(segs)-1
		switch node := cur.(type) {
		case map[string]any:
			if last {
				if i > 0 && segs[i-1] == "target" {
					for k := range node {
						delete(node, k)
					}
				}
				old, exists := node[seg]
				v, err := parameterValue(old, exists, value)
				if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				node[seg] = v
				return nil
			}
			next, ok := node[seg]
			if !ok || next == nil {
				next = map[string]any{}
				node[seg] = next
			}
			cur = next
		case []any:
			el, err := listElement(node, seg)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if last {
				v, err := parameterValue(node[el], true, value)
				if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				node[el] = v
				return nil
			}
			cur = node[el]
		default:
			return fmt.Errorf("%s: %q is not an object or a list, it has no field %q", path, strings.Join(segs[:i], "."), seg)
		}
	}
	return nil
}

// listElement finds the element a segment names: its position, or the id of an element that is an
// object.
func listElement(list []any, seg string) (int, error) {
	if n, err := strconv.Atoi(seg); err == nil {
		if n < 0 || n >= len(list) {
			return 0, fmt.Errorf("the list has no element %d (it has %d)", n, len(list))
		}
		return n, nil
	}
	for i, e := range list {
		if m, ok := e.(map[string]any); ok && m["id"] == seg {
			return i, nil
		}
	}
	return 0, fmt.Errorf("the list has no element with the id %q", seg)
}

func parameterValue(old any, exists bool, v string) (any, error) {
	if t := strings.TrimSpace(v); strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") {
		dec := json.NewDecoder(strings.NewReader(t))
		dec.UseNumber()
		var out any
		if err := dec.Decode(&out); err != nil {
			return nil, fmt.Errorf("the value is not JSON: %w", err)
		}
		return out, nil
	}
	switch old.(type) {
	case bool:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%q is not true or false", v)
		}
		return b, nil
	case json.Number, float64:
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return nil, fmt.Errorf("%q is not a number", v)
		}
		return json.Number(v), nil
	case string:
		return v, nil
	}
	if !exists || old == nil {
		switch {
		case v == "true" || v == "false":
			return v == "true", nil
		case wholeNumber.MatchString(v):
			return json.Number(v), nil
		}
	}
	return v, nil
}

// ParameterizeScenario returns the scenario with the parameters applied, decoded and checked
// against the schema of a scenario (references are checked by ResolveScenario). The error is
// ValidationErrors; the paths of the scenario's own problems are relative to the scenario. A scenario
// without parameters is checked only.
func ParameterizeScenario(sc *model.Scenario, params map[string]string) (*model.Scenario, error) {
	raw, err := json.Marshal(sc)
	if err != nil {
		return nil, err
	}
	doc, err := ParseDocument(raw, FormatJSON)
	if err != nil {
		return nil, err
	}
	if errs := ApplyParameters(doc, params); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	return decodeScenarioDocument(doc)
}

func decodeScenarioDocument(doc any) (*model.Scenario, error) {
	v, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := v.Validate("Scenario", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var out model.Scenario
	if err := decodeInto(doc, &out); err != nil {
		return nil, fmt.Errorf("domain: decode a validated scenario: %w", err)
	}
	return &out, nil
}

// DecodeRunRequest reads the body of `POST /runs`: strictly (unknown fields are rejected), with
// exactly one of `scenario` and `inline`, and the parameters must fit the inline scenario (which may
// leave out what they give, such as its target). The request is returned as it was sent: the
// parameters are applied when the run is made (ParameterizeScenario).
func DecodeRunRequest(raw []byte) (*model.RunRequest, error) {
	doc, err := ParseDocument(raw, FormatJSON)
	if err != nil {
		return nil, err
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, ValidationErrors{{Path: "", Code: "invalid_type", Message: "the request is a JSON object"}}
	}
	v, err := Schemas()
	if err != nil {
		return nil, err
	}
	check := obj
	if inline, ok := obj["inline"].(map[string]any); ok {
		if params, ok := obj["parameters"].(map[string]any); ok {
			strs := map[string]string{}
			for k, pv := range params {
				if s, ok := pv.(string); ok {
					strs[k] = s
				}
			}
			// the schema looks at the inline scenario the parameters make
			copied, err := ParseDocument(mustMarshal(inline), FormatJSON)
			if err != nil {
				return nil, err
			}
			errs := ApplyParameters(copied, strs)
			if len(errs) > 0 {
				return nil, ValidationErrors(errs)
			}
			check = map[string]any{}
			for k, e := range obj {
				check[k] = e
			}
			check["inline"] = copied
		}
	}
	if errs := v.Validate("RunRequest", check); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var req model.RunRequest
	if err := decodeInto(obj, &req); err != nil {
		return nil, fmt.Errorf("domain: decode a validated run request: %w", err)
	}
	if (req.Scenario == nil) == (req.Inline == nil) {
		return nil, ValidationErrors{{Path: "", Code: CodeExclusive, Message: "give exactly one of scenario (a stored scenario) and inline (a scenario sent with the request)"}}
	}
	if req.Lease != nil {
		if d, ok := parseDuration(*req.Lease); !ok || d < 1_000_000_000 {
			return nil, ValidationErrors{{Path: "/lease", Code: CodeInvalidDuration, Message: "the lease must be at least 1s"}}
		}
	}
	return &req, nil
}

// DecodeRunOptions reads the optional body of `POST /scenarios/{id}/runs`: the parameters and the lease of a run
// of a stored scenario. An empty body is no options.
func DecodeRunOptions(raw []byte) (*model.RunOptions, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return &model.RunOptions{}, nil
	}
	doc, err := ParseDocument(raw, FormatJSON)
	if err != nil {
		return nil, err
	}
	v, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := v.Validate("RunOptions", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var opts model.RunOptions
	if err := decodeInto(doc, &opts); err != nil {
		return nil, fmt.Errorf("domain: decode validated run options: %w", err)
	}
	if opts.Lease != nil {
		if d, ok := parseDuration(*opts.Lease); !ok || d < 1_000_000_000 {
			return nil, ValidationErrors{{Path: "/lease", Code: CodeInvalidDuration, Message: "the lease must be at least 1s"}}
		}
	}
	return &opts, nil
}

func mustMarshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(v); err != nil {
		panic(fmt.Sprintf("domain: marshal: %v", err))
	}
	return b.Bytes()
}
