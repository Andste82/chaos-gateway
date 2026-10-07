package kernelsim

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

func (k *Kernel) nftCmd(c executor.Command) (executor.Result, error) {
	a := strings.Join(c.Args, " ")
	switch a {
	case "-j list table inet chaosgw":
		if k.nft == nil {
			return executor.Result{Exit: 1, Stderr: "Error: No such file or directory\nlist table inet chaosgw\n^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^\n"}, nil
		}
		return jsonOut(k.nftList())
	case "-j -f -":
		return k.nftApply(c.Stdin)
	}
	return fail("unsupported nft %v", c.Args)
}

func (k *Kernel) nftList() any {
	t := k.nft
	h := 0
	next := func() int { h++; return h }
	out := []any{
		map[string]any{"metainfo": map[string]any{"version": "1.0.9", "json_schema_version": 1}},
		map[string]any{"table": map[string]any{"family": "inet", "name": "chaosgw", "handle": next()}},
	}
	names := func(m map[string]int64) []string {
		var n []string
		for k := range m {
			n = append(n, k)
		}
		sort.Strings(n)
		return n
	}
	for _, n := range names(t.counters) {
		out = append(out, map[string]any{"counter": map[string]any{"family": "inet", "table": "chaosgw", "name": n, "handle": next(), "packets": t.counters[n], "bytes": t.counters[n] * 100}})
	}
	var setNames []string
	for n := range t.sets {
		setNames = append(setNames, n)
	}
	sort.Strings(setNames)
	for _, n := range setNames {
		s := t.sets[n]
		m := map[string]any{"family": "inet", "table": "chaosgw", "name": n, "handle": next(), "type": s.typ}
		if len(s.flags) > 0 {
			m["flags"] = s.flags
		}
		if len(s.elems) > 0 {
			m["elem"] = printedElems(s)
		}
		out = append(out, map[string]any{"set": m})
	}
	var mapNames []string
	for n := range t.maps {
		mapNames = append(mapNames, n)
	}
	sort.Strings(mapNames)
	for _, n := range mapNames {
		mp := t.maps[n]
		var typ any
		_ = json.Unmarshal(mp.keyType, &typ)
		m := map[string]any{"family": "inet", "table": "chaosgw", "name": n, "handle": next(), "type": typ, "map": mp.valueType}
		if len(mp.flags) > 0 {
			m["flags"] = mp.flags
		}
		if len(mp.elems) > 0 {
			elems := make([]json.RawMessage, len(mp.elems))
			copy(elems, mp.elems)
			m["elem"] = elems
		}
		out = append(out, map[string]any{"map": m})
	}
	var chainNames []string
	for n := range t.chains {
		chainNames = append(chainNames, n)
	}
	sort.Strings(chainNames)
	for _, n := range chainNames {
		c := t.chains[n]
		m := map[string]any{"family": "inet", "table": "chaosgw", "name": n, "handle": next()}
		for k, v := range c.base {
			m[k] = v
		}
		out = append(out, map[string]any{"chain": m})
		for _, r := range c.rules {
			out = append(out, map[string]any{"rule": map[string]any{"family": "inet", "table": "chaosgw", "chain": n, "handle": next(), "comment": r.comment, "expr": r.expr}})
		}
	}
	return map[string]any{"nftables": out}
}

type nftErr string

func (e nftErr) Error() string { return string(e) }

func (k *Kernel) nftApply(stdin string) (executor.Result, error) {
	var doc struct {
		Nftables []map[string]map[string]map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(stdin), &doc); err != nil {
		return executor.Result{Exit: 1, Stderr: "Error: syntax error: " + err.Error() + "\n"}, nil
	}
	work := (*nftTable)(nil)
	if k.nft != nil {
		work = k.nft.clone()
	}
	for i, item := range doc.Nftables {
		for command, objs := range item {
			for kind, fields := range objs {
				var err error
				work, err = applyNft(work, command, kind, fields)
				if err != nil {
					return executor.Result{Exit: 1, Stderr: fmt.Sprintf("Error: %v\n(command %d: %s %s)\n", err, i, command, kind)}, nil
				}
			}
		}
	}
	k.nft = work
	return executor.Result{}, nil
}

func str(f map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(f[key], &s)
	return s
}

func applyNft(t *nftTable, command, kind string, f map[string]json.RawMessage) (*nftTable, error) {
	if kind == "table" {
		if command != "add" {
			return t, nftErr("table: only add is simulated")
		}
		if t == nil {
			t = &nftTable{sets: map[string]*nftSet{}, maps: map[string]*nftMap{}, counters: map[string]int64{}, chains: map[string]*nftChain{}}
		}
		return t, nil
	}
	if t == nil {
		return t, nftErr("No such file or directory")
	}
	name := str(f, "name")
	switch kind {
	case "counter":
		switch command {
		case "add":
			if _, ok := t.counters[name]; !ok {
				t.counters[name] = 0
			}
		case "delete":
			if _, ok := t.counters[name]; !ok {
				return t, nftErr("No such file or directory")
			}
			if usedBy(t, func(e any) bool { m, ok := e.(map[string]any); return ok && m["counter"] == name }) {
				return t, nftErr("Device or resource busy")
			}
			delete(t.counters, name)
		default:
			return t, nftErr(command + " counter is not simulated")
		}
	case "set":
		switch command {
		case "add":
			var flags []string
			_ = json.Unmarshal(f["flags"], &flags)
			typ := str(f, "type")
			if old, ok := t.sets[name]; ok {
				if old.typ != typ || !reflect.DeepEqual(old.flags, flags) {
					return t, nftErr("File exists")
				}
				return t, nil
			}
			t.sets[name] = &nftSet{typ: typ, flags: flags}
		case "flush":
			s, ok := t.sets[name]
			if !ok {
				return t, nftErr("No such file or directory")
			}
			s.elems = nil
		case "delete":
			if _, ok := t.sets[name]; !ok {
				return t, nftErr("No such file or directory")
			}
			if usedBy(t, func(e any) bool { return refersTo(e, "@"+name) }) {
				return t, nftErr("Device or resource busy")
			}
			delete(t.sets, name)
		default:
			return t, nftErr(command + " set is not simulated")
		}
	case "map":
		switch command {
		case "add":
			typ := f["type"]
			val := str(f, "map")
			var flags []string
			_ = json.Unmarshal(f["flags"], &flags)
			if old, ok := t.maps[name]; ok {
				if string(old.keyType) != string(typ) || old.valueType != val || !reflect.DeepEqual(old.flags, flags) {
					return t, nftErr("File exists")
				}
				return t, nil
			}
			t.maps[name] = &nftMap{keyType: typ, valueType: val, flags: flags}
		case "flush":
			m, ok := t.maps[name]
			if !ok {
				return t, nftErr("No such file or directory")
			}
			m.elems = nil
		case "delete":
			if _, ok := t.maps[name]; !ok {
				return t, nftErr("No such file or directory")
			}
			if usedBy(t, func(e any) bool { return refersTo(e, "@"+name) }) {
				return t, nftErr("Device or resource busy")
			}
			delete(t.maps, name)
		default:
			return t, nftErr(command + " map is not simulated")
		}
	case "element":
		if s, ok := t.sets[name]; ok {
			if command != "add" && command != "delete" {
				return t, nftErr("No such file or directory")
			}
			var elems []json.RawMessage
			_ = json.Unmarshal(f["elem"], &elems)
			if command == "delete" {
				for _, e := range elems {
					found := false
					for i, x := range s.elems {
						if string(x) == string(e) {
							s.elems = append(s.elems[:i:i], s.elems[i+1:]...)
							found = true
							break
						}
					}
					if !found {
						return t, nftErr("No such file or directory") // nft refuses to delete an element that is not there
					}
				}
				return t, nil
			}
			for _, e := range elems {
				dup := false
				for _, x := range s.elems {
					dup = dup || string(x) == string(e)
				}
				if !dup {
					s.elems = append(s.elems, e)
				}
			}
			return t, nil
		}
		m, ok := t.maps[name]
		if !ok || (command != "add" && command != "delete") {
			return t, nftErr("No such file or directory")
		}
		var raw []json.RawMessage
		_ = json.Unmarshal(f["elem"], &raw)
		// the elements' keys are worked out once: a burst of thousands of elements must not parse
		// every existing element for every new one
		index := make(map[string]int, len(m.elems))
		for i, x := range m.elems {
			index[mapElemKey(x)] = i
		}
		if command == "delete" {
			for _, e := range raw {
				k := mapElemKey(e)
				if _, found := index[k]; !found {
					return t, nftErr("No such file or directory")
				}
				// delete in place by key: rebuild once below
				m.elems[index[k]] = nil
				delete(index, k)
			}
			kept := make([]json.RawMessage, 0, len(index))
			for _, x := range m.elems {
				if x != nil {
					kept = append(kept, x)
				}
			}
			m.elems = kept
			return t, nil
		}
		for _, e := range raw {
			var v any
			if err := json.Unmarshal(e, &v); err == nil {
				if err := checkRefs(t, v); err != nil {
					return t, err
				}
			}
			k := mapElemKey(e)
			if i, replaced := index[k]; replaced {
				m.elems[i] = e
				continue
			}
			index[k] = len(m.elems)
			m.elems = append(m.elems, e)
		}
	case "chain":
		switch command {
		case "add":
			base := map[string]any{}
			for _, key := range []string{"type", "hook", "prio", "policy"} {
				if raw, ok := f[key]; ok {
					var v any
					_ = json.Unmarshal(raw, &v)
					base[key] = v
				}
			}
			if old, ok := t.chains[name]; ok {
				if !reflect.DeepEqual(old.base, base) && len(base) > 0 {
					return t, nftErr("File exists")
				}
				return t, nil
			}
			t.chains[name] = &nftChain{base: base}
		case "flush":
			c, ok := t.chains[name]
			if !ok {
				return t, nftErr("No such file or directory")
			}
			c.rules = nil
		case "delete":
			c, ok := t.chains[name]
			if !ok {
				return t, nftErr("No such file or directory")
			}
			if len(c.rules) > 0 {
				return t, nftErr("Device or resource busy")
			}
			delete(t.chains, name)
		default:
			return t, nftErr(command + " chain is not simulated")
		}
	case "rule":
		if command != "add" {
			return t, nftErr(command + " rule is not simulated")
		}
		c, ok := t.chains[str(f, "chain")]
		if !ok {
			return t, nftErr("No such file or directory")
		}
		var expr []any
		_ = json.Unmarshal(f["expr"], &expr)
		for _, e := range expr {
			if err := checkRefs(t, e); err != nil {
				return t, err
			}
		}
		c.rules = append(c.rules, nftRule{expr: expr, comment: str(f, "comment")})
	default:
		return t, nftErr(kind + " is not simulated")
	}
	return t, nil
}

// checkRefs fails when an expression names a set, map, chain or counter that does not exist.
func checkRefs(t *nftTable, e any) error {
	switch x := e.(type) {
	case string:
		if strings.HasPrefix(x, "@") {
			n := x[1:]
			_, isSet := t.sets[n]
			_, isMap := t.maps[n]
			if !isSet && !isMap {
				return nftErr("No such file or directory: set " + n)
			}
		}
	case map[string]any:
		if n, ok := x["counter"].(string); ok {
			if _, ok := t.counters[n]; !ok {
				return nftErr("No such file or directory: counter " + n)
			}
		}
		for _, verb := range []string{"jump", "goto"} {
			if j, ok := x[verb].(map[string]any); ok {
				if n, ok := j["target"].(string); ok {
					if _, ok := t.chains[n]; !ok {
						return nftErr("No such file or directory: chain " + n)
					}
				}
			}
		}
		for _, v := range x {
			if err := checkRefs(t, v); err != nil {
				return err
			}
		}
	case []any:
		for _, v := range x {
			if err := checkRefs(t, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func usedBy(t *nftTable, pred func(any) bool) bool {
	for _, c := range t.chains {
		for _, r := range c.rules {
			for _, e := range r.expr {
				if pred(e) || walk(e, pred) {
					return true
				}
			}
		}
	}
	return false
}

func walk(e any, pred func(any) bool) bool {
	switch x := e.(type) {
	case map[string]any:
		for _, v := range x {
			if pred(v) || walk(v, pred) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if pred(v) || walk(v, pred) {
				return true
			}
		}
	}
	return false
}

func refersTo(e any, ref string) bool { s, ok := e.(string); return ok && s == ref }

// mapElemKey returns the normalized key part of a map element (an add's `[key, value]` pair, or,
// for a delete, the bare key), so two elements can be compared regardless of their value.
func mapElemKey(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	if pair, ok := v.([]any); ok && len(pair) == 2 {
		b, _ := json.Marshal(pair[0])
		return string(b)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// printedElems renders the elements as nft prints them: a single-address interval element (a /32
// prefix) comes out as the bare address.
func printedElems(s *nftSet) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(s.elems))
	for _, e := range s.elems {
		var v struct {
			Prefix *struct {
				Addr string `json:"addr"`
				Len  int    `json:"len"`
			} `json:"prefix"`
		}
		if json.Unmarshal(e, &v) == nil && v.Prefix != nil && v.Prefix.Len == 32 {
			b, _ := json.Marshal(v.Prefix.Addr)
			out = append(out, b)
			continue
		}
		out = append(out, e)
	}
	return out
}
