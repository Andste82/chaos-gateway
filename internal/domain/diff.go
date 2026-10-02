package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// Diff returns the domain diff of two configurations: what changed, in the user's terms (plan
// §3.2). It is what the preview shows first; the Linux diff follows from the compiler. base may
// be nil (everything is added). Changes are sorted by kind and path.
func Diff(base, next *model.Configuration) []model.DomainChange {
	// a missing configuration is an empty document, not a configuration with empty required parts
	a, b := map[string]any{}, map[string]any{}
	d := &differ{names: map[string]string{}}
	if base != nil {
		a, _ = jsonValue(*base).(map[string]any)
		d.collectNames(base)
	}
	if next != nil {
		b, _ = jsonValue(*next).(map[string]any)
		d.collectNames(next)
	}

	for _, c := range []struct{ key, kind string }{
		{"networks", "network"}, {"devices", "device"}, {"groups", "group"}, {"probes", "probe"},
		{"access_rules", "access_rule"}, {"faults", "fault"}, {"profiles", "profile"}, {"scenarios", "scenario"},
	} {
		d.collection("/"+c.key, c.kind, asMap(a[c.key]), asMap(b[c.key]))
	}
	for _, s := range []struct{ key, kind string }{
		{"uplink", "uplink"}, {"management", "management"}, {"settings", "settings"}, {"access_matrix", "access_matrix"},
	} {
		d.singleton("/"+s.key, s.kind, a[s.key], b[s.key])
	}
	if oa, ob := a["access_rule_order"], b["access_rule_order"]; !reflect.DeepEqual(oa, ob) {
		op, what := "changed", "changed"
		switch {
		case oa == nil:
			op, what = "added", "set"
		case ob == nil:
			op, what = "removed", "removed"
		}
		d.add(op, "access_rule", "", "", "/access_rule_order", "the order of the access rules "+what,
			[]field{{path: "/access_rule_order", old: oa, new: ob}})
	}
	// routing: its own fields, and the protocols as objects of their own
	ra, rb := asMap(a["routing"]), asMap(b["routing"])
	d.collection("/routing/protocols", "routing_protocol", asMap(ra["protocols"]), asMap(rb["protocols"]))
	d.singleton("/routing", "routing", without(ra, "protocols"), without(rb, "protocols"))

	sort.SliceStable(d.out, func(i, j int) bool {
		ki, kj := kindOrder[d.out[i].Kind], kindOrder[d.out[j].Kind]
		if ki != kj {
			return ki < kj
		}
		return d.out[i].Path < d.out[j].Path
	})
	return d.out
}

var kindOrder = map[model.DomainChangeKind]int{
	"uplink": 0, "management": 1, "network": 2, "client": 3, "link_peer": 4, "routing": 5, "routing_protocol": 6,
	"access_matrix": 7, "device": 8, "group": 9, "probe": 10, "access_rule": 11, "fault": 12, "profile": 13,
	"scenario": 14, "settings": 15,
}

type field struct {
	path     string
	old, new any
}

type differ struct {
	names map[string]string // UUID → name
	out   []model.DomainChange
}

func (d *differ) collectNames(cfg *model.Configuration) {
	idx, _ := BuildIndex(cfg)
	for id, n := range idx.Networks {
		d.names[id] = n.Name
	}
	for id, dev := range idx.Devices {
		d.names[id] = dev.Name
	}
	for id, n := range idx.Groups {
		d.names[id] = n
	}
	for id, n := range idx.Profiles {
		d.names[id] = n
	}
	for id, n := range idx.Scenarios {
		d.names[id] = n
	}
	for id, n := range idx.Protocols {
		d.names[id] = n
	}
	for id, r := range deref(cfg.AccessRules) {
		if r.Name != nil {
			d.names[id] = *r.Name
		}
	}
	for id, f := range deref(cfg.Faults) {
		if f.Name != nil {
			d.names[id] = *f.Name
		}
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func without(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		skip := false
		for _, s := range keys {
			skip = skip || k == s
		}
		if !skip {
			out[k] = v
		}
	}
	return out
}

// nameField returns the name inside an object, or "".
func nameField(v any) string {
	if m, ok := v.(map[string]any); ok {
		if n, ok := m["name"].(string); ok {
			return n
		}
	}
	return ""
}

func (d *differ) label(id string, old, new any) string {
	if n := nameField(new); n != "" {
		return n
	}
	if n := nameField(old); n != "" {
		return n
	}
	if n, ok := d.names[id]; ok {
		return n
	}
	return id
}

// collection compares the objects of a map keyed by UUID.
func (d *differ) collection(base, kind string, a, b map[string]any) {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for _, k := range sortedKeys(keys) {
		path := schema.Pointer(base, k)
		old, hadOld := a[k]
		cur, hasNew := b[k]
		name := d.label(k, old, cur)
		switch {
		case !hadOld:
			d.add("added", kind, k, name, path, d.describe(kind, name, cur)+" added", nil)
			d.nested(path, kind, nil, cur)
		case !hasNew:
			d.add("removed", kind, k, name, path, d.describe(kind, name, old)+" removed", nil)
			d.nested(path, kind, old, nil)
		default:
			// WireGuard clients and the link peer are objects of their own
			o, n := old, cur
			if kind == "network" {
				o, n = without(asMap(old), "clients", "peer"), without(asMap(cur), "clients", "peer")
			}
			if fs := leafDiff(o, n, ""); len(fs) > 0 {
				d.add("changed", kind, k, name, path, d.describe(kind, name, cur)+" changed: "+d.fields(fs), fs)
			}
			d.nested(path, kind, old, cur)
		}
	}
}

// nested diffs the objects that live inside a network: WireGuard clients and the link peer.
func (d *differ) nested(path, kind string, old, cur any) {
	if kind != "network" {
		return
	}
	o, c := asMap(old), asMap(cur)
	d.collection(path+"/clients", "client", asMap(o["clients"]), asMap(c["clients"]))
	d.singleton(path+"/peer", "link_peer", o["peer"], c["peer"])
}

// singleton compares one object that exists once.
func (d *differ) singleton(path, kind string, old, cur any) {
	empty := func(v any) bool { m, ok := v.(map[string]any); return v == nil || (ok && len(m) == 0) }
	switch {
	case empty(old) && empty(cur):
		return
	case empty(old):
		d.add("added", kind, "", "", path, strings.ReplaceAll(kind, "_", " ")+" added", nil)
	case empty(cur):
		d.add("removed", kind, "", "", path, strings.ReplaceAll(kind, "_", " ")+" removed", nil)
	default:
		if fs := leafDiff(old, cur, ""); len(fs) > 0 {
			d.add("changed", kind, "", "", path, strings.ReplaceAll(kind, "_", " ")+" changed: "+d.fields(fs), fs)
		}
	}
}

func (d *differ) add(op, kind, id, name, path, summary string, fs []field) {
	c := model.DomainChange{
		Op: model.DomainChangeOp(op), Kind: model.DomainChangeKind(kind), Path: path, Summary: summary,
	}
	if id != "" {
		c.Id = &id
	}
	if name != "" {
		c.Name = &name
	}
	if len(fs) > 0 {
		out := make([]struct {
			New  interface{} `json:"new,omitempty"`
			Old  interface{} `json:"old,omitempty"`
			Path string      `json:"path"`
		}, len(fs))
		for i, f := range fs {
			out[i].Path, out[i].Old, out[i].New = f.path, f.old, f.new
		}
		c.Fields = &out
	}
	d.out = append(d.out, c)
}

// describe names an object in a summary: `fault "x" (device esp32-42)`.
func (d *differ) describe(kind, name string, obj any) string {
	label := strings.ReplaceAll(kind, "_", " ") + " " + fmt.Sprintf("%q", name)
	if kind == "fault" || kind == "access_rule" {
		if src := asMap(asMap(obj)["source"]); len(src) == 1 {
			for k, v := range src {
				label += " (" + k + " " + d.render(v) + ")"
			}
		}
	}
	return label
}

// fields lists up to three changed fields as "path old → new".
func (d *differ) fields(fs []field) string {
	var parts []string
	for i, f := range fs {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("… and %d more", len(fs)-3))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s → %s", strings.TrimPrefix(f.path, "/"), d.renderOr(f.old), d.renderOr(f.new)))
	}
	return strings.Join(parts, "; ")
}

func (d *differ) renderOr(v any) string {
	if v == nil {
		return "(unset)"
	}
	return d.render(v)
}

// render shows a value for a person: UUIDs become names, long structures are shortened.
func (d *differ) render(v any) string {
	switch x := v.(type) {
	case string:
		if _, err := uuid.Parse(x); err == nil {
			if n, ok := d.names[strings.ToLower(x)]; ok {
				return n
			}
		}
		return x
	case json.Number:
		return x.String()
	case bool, float64:
		return fmt.Sprint(x)
	}
	raw, _ := json.Marshal(v)
	if s := string(raw); len(s) > 60 {
		return s[:57] + "…"
	}
	return string(raw)
}

// leafDiff lists the changed leaves of two values: the fields of objects that differ, and whole
// arrays and scalars. A field that exists on one side only has the other side nil.
func leafDiff(old, cur any, path string) []field {
	om, ok1 := old.(map[string]any)
	cm, ok2 := cur.(map[string]any)
	if ok1 && ok2 {
		keys := map[string]bool{}
		for k := range om {
			keys[k] = true
		}
		for k := range cm {
			keys[k] = true
		}
		var out []field
		for _, k := range sortedKeys(keys) {
			out = append(out, leafDiff(om[k], cm[k], schema.Pointer(path, k))...)
		}
		return out
	}
	if reflect.DeepEqual(old, cur) {
		return nil
	}
	return []field{{path: path, old: old, new: cur}}
}
