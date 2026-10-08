package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	ruleA = "a1000000-0000-4000-8000-000000000001"
	ruleB = "a1000000-0000-4000-8000-000000000002"
	ruleC = "a1000000-0000-4000-8000-000000000003"
	ruleD = "a1000000-0000-4000-8000-000000000004"
)

// addConfigRule adds a configured access rule (a YAML body) and appends it to the order.
func (w *faultWorld) addConfigRule(id, body string) {
	w.t.Helper()
	doc, err := domain.ParseDocument([]byte(body), domain.FormatYAML)
	if err != nil {
		w.t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	var r model.AccessRule
	if err := json.Unmarshal(raw, &r); err != nil {
		w.t.Fatal(err)
	}
	cfg := *w.cfg
	rules := map[string]model.AccessRule{}
	if cfg.AccessRules != nil {
		for k, v := range *cfg.AccessRules {
			rules[k] = v
		}
	}
	rules[id] = r
	cfg.AccessRules = &rules
	order := []uuid.UUID{}
	if cfg.AccessRuleOrder != nil {
		order = append(order, *cfg.AccessRuleOrder...)
	}
	order = append(order, uuid.MustParse(id))
	cfg.AccessRuleOrder = &order
	norm, errs := domain.Normalize(&cfg)
	if len(errs) != 0 {
		w.t.Fatalf("normalize: %v", errs)
	}
	if errs := domain.Validate(norm); len(errs) != 0 {
		w.t.Fatalf("validate: %v", errs)
	}
	w.cfg = norm
}

// ruleOverlay creates an overlay rule at tFault0+age and returns it.
func (w *faultWorld) ruleOverlay(body string, age time.Duration) model.Overlay {
	return w.overlay(body, age)
}

// accessWorld is the fixture of the access rule tests: the fault fixture with the two networks, three
// devices and the group sensors.
func accessWorld(t *testing.T) *faultWorld { return newFaultWorld(t) }

func hasChain(tg *Target, name string) bool {
	for _, c := range tg.Nft.Chains {
		if c.Name == name {
			return true
		}
	}
	return false
}

func chainNames(tg *Target) []string {
	var out []string
	for _, c := range tg.Nft.Chains {
		out = append(out, c.Name)
	}
	return out
}

// indexOfRule returns the index of the first rule of the chain whose text contains all the parts,
// -1 if there is none.
func indexOfRule(c Chain, parts ...string) int {
	for i, r := range c.Rules {
		s := renderRule(r)
		ok := true
		for _, p := range parts {
			if !strings.Contains(s, p) {
				ok = false
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// ---- a readable form of the rules -------------------------------------------------------------

func renderRule(r Rule) string {
	parts := make([]string, len(r.Expr))
	for i, e := range r.Expr {
		parts[i] = renderExpr(e)
	}
	return strings.Join(parts, " ")
}

func renderExpr(e any) string {
	switch x := e.(type) {
	case nil:
		return "null"
	case string:
		return x
	case int:
		return fmt.Sprint(x)
	case int64:
		return fmt.Sprint(x)
	case float64:
		return fmt.Sprint(x)
	case []any:
		parts := make([]string, len(x))
		for i, v := range x {
			parts[i] = renderExpr(v)
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		if len(x) != 1 {
			return fmt.Sprint(x)
		}
		for k, v := range x {
			switch k {
			case "match":
				m := v.(map[string]any)
				op := m["op"].(string)
				if op == "==" || op == "in" {
					op = ""
				} else {
					op += " "
				}
				right := renderExpr(m["right"])
				if l, ok := m["right"].([]any); ok {
					right = "{ " + renderExpr(l) + " }"
				}
				return renderExpr(m["left"]) + " " + op + right
			case "ct":
				m := v.(map[string]any)
				if d, ok := m["dir"].(string); ok {
					return "ct " + d + " " + m["key"].(string)
				}
				return "ct " + m["key"].(string)
			case "meta":
				return "meta " + v.(map[string]any)["key"].(string)
			case "payload":
				m := v.(map[string]any)
				return m["protocol"].(string) + " " + m["field"].(string)
			case "set":
				return "{ " + renderExpr(v) + " }"
			case "prefix":
				m := v.(map[string]any)
				return fmt.Sprintf("%v/%v", m["addr"], m["len"])
			case "range":
				l := v.([]any)
				return fmt.Sprintf("%v-%v", l[0], l[1])
			case "counter":
				return fmt.Sprintf("counter %q", v)
			case "jump", "goto":
				return k + " " + v.(map[string]any)["target"].(string)
			case "reject":
				if v == nil {
					return "reject"
				}
				m := v.(map[string]any)
				s := "reject with " + m["type"].(string)
				if ex, ok := m["expr"]; ok {
					s += " " + fmt.Sprint(ex)
				}
				return s
			default:
				if v == nil {
					return k
				}
			}
		}
	}
	return fmt.Sprint(e)
}

// describeAccess is the readable form of the access side of a target for the golden files: the
// effective order, the sets of the sources and the rules of the chains.
func describeAccess(tg *Target) string {
	var b strings.Builder
	if tg.Access == nil {
		return "# no access rules\n"
	}
	fmt.Fprintf(&b, "# rules\n")
	for _, r := range tg.Access.Rules {
		cut := ""
		if r.CutExisting {
			cut = "  cut"
		}
		fmt.Fprintf(&b, "%d  %s  %s  %s%s  scope %q  dest %q  proto %s  counter %s\n", r.Position, r.Key, r.Name, r.Action, cut, r.Scope, r.Destination, r.Protocol, r.Counter)
	}
	fmt.Fprintf(&b, "# sets\n")
	var sets []SetDef
	for _, s := range tg.Nft.Sets {
		if strings.HasPrefix(s.Name, "asrc_") || strings.HasPrefix(s.Name, "anon_uplink") {
			sets = append(sets, s)
		}
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].Name < sets[j].Name })
	for _, s := range sets {
		fmt.Fprintf(&b, "set %s { %s }\n", s.Name, strings.Join(s.Elements, ", "))
	}
	for _, name := range []string{"forward", "input", AccessForwardChain, AccessInputChain, CutForwardChain, CutInputChain} {
		if !hasChain(tg, name) {
			continue
		}
		var c Chain
		for _, x := range tg.Nft.Chains {
			if x.Name == name {
				c = x
			}
		}
		fmt.Fprintf(&b, "# chain %s\n", name)
		for _, r := range c.Rules {
			fmt.Fprintf(&b, "  %s\n", renderRule(r))
		}
	}
	return b.String()
}
