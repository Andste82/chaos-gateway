package linux

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RenderNft renders a ruleset read with `nft -j` in nft's own syntax, the way `nft list table`
// prints it: the named counters with their values, the sets and maps with their elements, and the
// chains with their rules, each rule with the counter of its packets and bytes where it has one
// (plan §2.17 "Technical view": the compiled configuration and its live counters). Chains named in
// skip are left out. An expression this renderer does not know is shown as its JSON, so nothing
// the kernel holds is hidden. Sets with more than maxElements elements show the first ones and how
// many more there are.
func RenderNft(rs *Ruleset, maxElements int, skip ...string) string {
	if rs == nil {
		return ""
	}
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	var b strings.Builder
	for _, t := range rs.Tables() {
		fmt.Fprintf(&b, "table %s %s {\n", t.Family, t.Name)
		var counters []*NftCounter
		var sets []*NftSet
		var maps []*NftSet
		var chains []*NftChain
		for _, o := range rs.Objects {
			switch {
			case o.Counter != nil && o.Counter.Table == t.Name && o.Counter.Family == t.Family:
				counters = append(counters, o.Counter)
			case o.Set != nil && o.Set.Table == t.Name && o.Set.Family == t.Family:
				sets = append(sets, o.Set)
			case o.Map != nil && o.Map.Table == t.Name && o.Map.Family == t.Family:
				maps = append(maps, o.Map)
			case o.Chain != nil && o.Chain.Table == t.Name && o.Chain.Family == t.Family && !skipped[o.Chain.Name]:
				chains = append(chains, o.Chain)
			}
		}
		sort.Slice(counters, func(i, j int) bool { return counters[i].Name < counters[j].Name })
		for _, c := range counters {
			fmt.Fprintf(&b, "  counter %s { packets %d bytes %d }\n", c.Name, c.Packets, c.Bytes)
		}
		sort.Slice(sets, func(i, j int) bool { return sets[i].Name < sets[j].Name })
		for _, s := range sets {
			renderSet(&b, "set", s, maxElements)
		}
		sort.Slice(maps, func(i, j int) bool { return maps[i].Name < maps[j].Name })
		for _, m := range maps {
			renderSet(&b, "map", m, maxElements)
		}
		for _, c := range chains {
			fmt.Fprintf(&b, "  chain %s {\n", c.Name)
			if c.Hook != "" {
				prio := 0
				if c.Prio != nil {
					prio = *c.Prio
				}
				fmt.Fprintf(&b, "    type %s hook %s", c.Type, c.Hook)
				if c.Dev != "" {
					fmt.Fprintf(&b, " device %q", c.Dev)
				}
				fmt.Fprintf(&b, " priority %d;", prio)
				if c.Policy != "" {
					fmt.Fprintf(&b, " policy %s;", c.Policy)
				}
				b.WriteString("\n")
			}
			for _, r := range rs.Rules(c.Name) {
				if r.Family != t.Family || r.Table != t.Name {
					continue
				}
				b.WriteString("    " + RenderNftRule(r.Expr) + "\n")
			}
			b.WriteString("  }\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func renderSet(b *strings.Builder, kind string, s *NftSet, maxElements int) {
	fmt.Fprintf(b, "  %s %s {\n", kind, s.Name)
	typ := typeText(s.Type)
	if len(s.Data) > 0 {
		typ += " : " + typeText(s.Data)
	}
	fmt.Fprintf(b, "    type %s\n", typ)
	if len(s.Flags) > 0 {
		fmt.Fprintf(b, "    flags %s\n", strings.Join(s.Flags, ","))
	}
	if len(s.Elem) > 0 {
		var els []string
		quote := typ == "ifname"
		for _, raw := range s.Elem {
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				els = append(els, string(raw))
				continue
			}
			if pair, ok := v.([]any); ok && len(pair) == 2 && kind == "map" {
				els = append(els, renderValue(pair[0], false)+" : "+renderValue(pair[1], false))
				continue
			}
			els = append(els, renderValue(v, quote))
		}
		more := 0
		if maxElements > 0 && len(els) > maxElements {
			more = len(els) - maxElements
			els = els[:maxElements]
		}
		text := strings.Join(els, ", ")
		if more > 0 {
			text += fmt.Sprintf(", … %d more", more)
		}
		fmt.Fprintf(b, "    elements = { %s }\n", text)
	}
	b.WriteString("  }\n")
}

// typeText renders a set's type: a name, or the parts of a concatenated type joined with " . ".
func typeText(raw json.RawMessage) string {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return strings.Join(many, " . ")
	}
	return string(raw)
}

// RenderNftRule renders the expressions of one rule as nft prints them ("iifname "br-iot" oifname
// "wan0" counter packets 3 bytes 180 accept").
func RenderNftRule(expr []json.RawMessage) string {
	parts := make([]string, 0, len(expr))
	for _, raw := range expr {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			parts = append(parts, string(raw))
			continue
		}
		parts = append(parts, renderStatement(v))
	}
	return strings.Join(parts, " ")
}

// names of the meta keys nft prints without the word "meta"
var bareMeta = map[string]bool{"iifname": true, "oifname": true, "iif": true, "oif": true}

func renderStatement(v any) string {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return compactJSON(v)
	}
	for key, arg := range m {
		switch key {
		case "accept", "drop", "continue", "return", "masquerade", "notrack":
			if arg == nil {
				return key
			}
		case "jump", "goto":
			if a, ok := arg.(map[string]any); ok {
				if t, ok := a["target"].(string); ok {
					return key + " " + t
				}
			}
		case "match":
			if a, ok := arg.(map[string]any); ok {
				left := renderValue(a["left"], false)
				right := renderValue(a["right"], quotesRight(a["left"]))
				switch op, _ := a["op"].(string); op {
				case "==", "in", "":
					return left + " " + right
				default:
					return left + " " + op + " " + right
				}
			}
		case "counter":
			switch a := arg.(type) {
			case string:
				return "counter name " + strconv.Quote(a)
			case map[string]any:
				return fmt.Sprintf("counter packets %s bytes %s", renderValue(a["packets"], false), renderValue(a["bytes"], false))
			case nil:
				return "counter"
			}
		case "last":
			// nft prints how long ago the rule last matched, or never (P3-M13-01)
			if a, ok := arg.(map[string]any); ok {
				if used, ok := a["used"].(float64); ok && used >= 0 {
					return "last used " + nftTime(int64(used))
				}
			}
			if arg == nil {
				return "last used never"
			}
		case "reject":
			a, _ := arg.(map[string]any)
			if a == nil {
				return "reject"
			}
			typ, _ := a["type"].(string)
			if typ == "tcp reset" {
				return "reject with tcp reset"
			}
			if e, ok := a["expr"].(string); ok {
				if typ == "" {
					typ = "icmp"
				}
				return "reject with " + typ + " " + e
			}
			return "reject"
		case "mangle":
			if a, ok := arg.(map[string]any); ok {
				return renderValue(a["key"], false) + " set " + renderValue(a["value"], false)
			}
		case "vmap":
			if a, ok := arg.(map[string]any); ok {
				return renderValue(a["key"], false) + " vmap " + renderValue(a["data"], false)
			}
		case "limit":
			if a, ok := arg.(map[string]any); ok {
				s := "limit rate "
				if inv, _ := a["inv"].(bool); inv {
					s += "over "
				}
				s += renderValue(a["rate"], false)
				if u, ok := a["rate_unit"].(string); ok && u != "packets" {
					s += " " + u
				}
				s += "/" + renderValue(a["per"], false)
				if burst, ok := a["burst"].(float64); ok && burst > 0 {
					s += " burst " + renderValue(burst, false) + " packets"
				}
				return s
			}
		case "dnat", "snat":
			if a, ok := arg.(map[string]any); ok {
				s := key + " to " + renderValue(a["addr"], false)
				if p, ok := a["port"]; ok {
					s += ":" + renderValue(p, false)
				}
				return s
			}
		case "redirect":
			if a, ok := arg.(map[string]any); ok {
				if p, ok := a["port"]; ok {
					return "redirect to :" + renderValue(p, false)
				}
			}
			if arg == nil {
				return "redirect"
			}
		case "set":
			// the statement that adds to a set ({"op": "add", "elem": ..., "set": "@name"}); a set
			// without "op" is an anonymous set, an expression
			if a, ok := arg.(map[string]any); ok {
				if op, ok := a["op"].(string); ok {
					return op + " " + renderValue(a["set"], false) + " { " + renderValue(a["elem"], false) + " }"
				}
			}
		case "log":
			if arg == nil {
				return "log"
			}
			if a, ok := arg.(map[string]any); ok {
				if p, ok := a["prefix"].(string); ok {
					return "log prefix " + strconv.Quote(p)
				}
				return "log"
			}
		}
	}
	return compactJSON(v)
}

// quotesRight says whether the value an expression is compared with is a name nft prints quoted.
func quotesRight(left any) bool {
	if m, ok := left.(map[string]any); ok {
		if meta, ok := m["meta"].(map[string]any); ok {
			k, _ := meta["key"].(string)
			return k == "iifname" || k == "oifname"
		}
	}
	return false
}

func renderValue(v any, quote bool) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if quote && !strings.HasPrefix(x, "@") {
			return strconv.Quote(x)
		}
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = renderValue(p, quote)
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case map[string]any:
		if len(x) != 1 {
			return compactJSON(v)
		}
		for key, arg := range x {
			switch key {
			case "set":
				if l, ok := arg.([]any); ok {
					return renderValue(l, quote)
				}
				return renderValue(arg, quote)
			case "payload":
				if a, ok := arg.(map[string]any); ok {
					if p, ok := a["protocol"].(string); ok {
						return p + " " + renderValue(a["field"], false)
					}
					return fmt.Sprintf("@%s,%s,%s", renderValue(a["base"], false), renderValue(a["offset"], false), renderValue(a["len"], false))
				}
			case "meta":
				if a, ok := arg.(map[string]any); ok {
					k, _ := a["key"].(string)
					if bareMeta[k] {
						return k
					}
					return "meta " + k
				}
			case "ct":
				if a, ok := arg.(map[string]any); ok {
					s := "ct "
					if d, ok := a["dir"].(string); ok {
						s += d + " "
					}
					return s + renderValue(a["key"], false)
				}
			case "rt":
				if a, ok := arg.(map[string]any); ok {
					return "rt " + renderValue(a["key"], false)
				}
			case "tcp option":
				if a, ok := arg.(map[string]any); ok {
					return "tcp option " + renderValue(a["name"], false) + " " + renderValue(a["field"], false)
				}
			case "prefix":
				if a, ok := arg.(map[string]any); ok {
					return renderValue(a["addr"], false) + "/" + renderValue(a["len"], false)
				}
			case "range":
				if r, ok := arg.([]any); ok && len(r) == 2 {
					return renderValue(r[0], quote) + "-" + renderValue(r[1], quote)
				}
			case "concat":
				if l, ok := arg.([]any); ok {
					parts := make([]string, len(l))
					for i, p := range l {
						parts[i] = renderValue(p, false)
					}
					return strings.Join(parts, " . ")
				}
			case "&", "|", "^", "<<", ">>":
				if l, ok := arg.([]any); ok && len(l) == 2 {
					return "(" + renderValue(l[0], false) + " " + key + " " + renderValue(l[1], false) + ")"
				}
			case "numgen":
				if a, ok := arg.(map[string]any); ok {
					s := "numgen " + renderValue(a["mode"], false) + " mod " + renderValue(a["mod"], false)
					if off, ok := a["offset"].(float64); ok && off != 0 {
						s += " offset " + renderValue(off, false)
					}
					return s
				}
			case "map":
				if a, ok := arg.(map[string]any); ok {
					return renderValue(a["key"], false) + " map " + renderValue(a["data"], false)
				}
			case "elem":
				if a, ok := arg.(map[string]any); ok {
					return renderValue(a["val"], quote)
				}
			case "jump", "goto":
				if a, ok := arg.(map[string]any); ok {
					return key + " " + renderValue(a["target"], false)
				}
			case "accept", "drop", "continue", "return":
				if arg == nil {
					return key
				}
			}
		}
	}
	return compactJSON(v)
}

// nftTime renders milliseconds the way nft prints a time: days, hours, minutes, seconds and
// milliseconds, the parts that are not zero ("1h2s", "350ms", "0ms").
func nftTime(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	var b strings.Builder
	for _, u := range []struct {
		n    int64
		name string
	}{{86_400_000, "d"}, {3_600_000, "h"}, {60_000, "m"}, {1000, "s"}, {1, "ms"}} {
		if v := ms / u.n; v > 0 {
			fmt.Fprintf(&b, "%d%s", v, u.name)
			ms -= v * u.n
		}
	}
	return b.String()
}

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
