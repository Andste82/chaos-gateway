package linux

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// NftObject is one object of an `nft -j` ruleset: exactly one of the pointer fields is set for the
// kinds this package knows; objects of other kinds (counters, quotas, ...) are left empty (rules and sets keep their expressions and elements raw).
type NftObject struct {
	Table *NftTable `json:"table,omitempty"`
	Chain *NftChain `json:"chain,omitempty"`
	Rule  *NftRule  `json:"rule,omitempty"`
	Set   *NftSet   `json:"set,omitempty"`
	Map   *NftSet   `json:"map,omitempty"`
	// Counter is a named counter; its packet and byte counts are runtime data, not configuration.
	Counter *NftCounter `json:"counter,omitempty"`
}

// NftCounter is a named counter object.
type NftCounter struct {
	Family  string `json:"family"`
	Table   string `json:"table"`
	Name    string `json:"name"`
	Handle  int    `json:"handle"`
	Packets int64  `json:"packets"`
	Bytes   int64  `json:"bytes"`
}

// NftTable is a table.
type NftTable struct {
	Family string `json:"family"`
	Name   string `json:"name"`
	Handle int    `json:"handle"`
}

// NftChain is a chain; base chains have a hook.
type NftChain struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Name   string `json:"name"`
	Handle int    `json:"handle"`
	Type   string `json:"type"`
	Hook   string `json:"hook"`
	Prio   *int   `json:"prio"`
	Policy string `json:"policy"`
}

// NftRule is a rule; the expressions stay raw because their grammar is large.
type NftRule struct {
	Family  string            `json:"family"`
	Table   string            `json:"table"`
	Chain   string            `json:"chain"`
	Handle  int               `json:"handle"`
	Comment string            `json:"comment"`
	Expr    []json.RawMessage `json:"expr"`
}

// NftSet is a set or a map. Elements are raw: they are strings, numbers, prefixes or elem objects.
type NftSet struct {
	Family string            `json:"family"`
	Table  string            `json:"table"`
	Name   string            `json:"name"`
	Handle int               `json:"handle"`
	Type   json.RawMessage   `json:"type"`
	Flags  []string          `json:"flags"`
	Elem   []json.RawMessage `json:"elem"`
}

// Ruleset is the content of one `nft -j list ...` call.
type Ruleset struct {
	JSONSchemaVersion int
	Objects           []NftObject
}

// ParseNft parses the output of `nft -j list ...`. An empty output is an empty ruleset.
func ParseNft(data []byte) (*Ruleset, error) {
	data = trimSpace(data)
	if len(data) == 0 {
		return &Ruleset{}, nil
	}
	var doc struct {
		Nftables []json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse nft: %w", err)
	}
	rs := &Ruleset{}
	for i, raw := range doc.Nftables {
		var meta struct {
			Metainfo *struct {
				Version int `json:"json_schema_version"`
			} `json:"metainfo"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("parse nft object %d: %w", i, err)
		}
		if meta.Metainfo != nil {
			rs.JSONSchemaVersion = meta.Metainfo.Version
			continue
		}
		var o NftObject
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, fmt.Errorf("parse nft object %d: %w", i, err)
		}
		rs.Objects = append(rs.Objects, o)
	}
	return rs, nil
}

// Elements returns the elements of the set as normalized strings, sorted: addresses as they are,
// prefixes as addr/len, ranges as a-b, timeouts dropped (they are runtime data), concatenations
// joined with ".". It is the form verify compares.
func (s *NftSet) Elements() []string {
	out := make([]string, 0, len(s.Elem))
	for _, raw := range s.Elem {
		out = append(out, NormalizeElement(normalizeElem(raw)))
	}
	sort.Strings(out)
	return out
}

// NormalizeElement brings an element into the form nft prints: an address set shows a /32 prefix
// as the plain address. Verify normalizes both sides with it.
func NormalizeElement(e string) string { return strings.TrimSuffix(e, "/32") }

// Pairs returns the key/value elements of a map, normalized like Elements: the key as an address,
// a prefix or a concatenation joined with ".", the value as a decimal number (a "mark" map, such
// as the identity map) or the name of the chain a "verdict" element (a classification map) jumps
// to.
func (s *NftSet) Pairs() map[string]string {
	out := make(map[string]string, len(s.Elem))
	for _, raw := range s.Elem {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		e, ok := m["elem"].(map[string]any)
		if !ok {
			continue
		}
		out[NormalizeElement(elemString(e["key"]))] = mapValueString(e["val"])
	}
	return out
}

// mapValueString renders a map element's data as nft prints it: a decimal number as itself, a
// jump verdict as the chain's name.
func mapValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case map[string]any:
		if j, ok := x["jump"].(map[string]any); ok {
			if t, ok := j["target"].(string); ok {
				return t
			}
		}
	}
	return ""
}

func normalizeElem(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return elemString(v)
}

func elemString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case map[string]any:
		if e, ok := x["elem"]; ok { // an element with a timeout or a counter
			if m, ok := e.(map[string]any); ok {
				return elemString(m["val"])
			}
		}
		if p, ok := x["prefix"].(map[string]any); ok {
			return fmt.Sprintf("%s/%s", elemString(p["addr"]), elemString(p["len"]))
		}
		if r, ok := x["range"].([]any); ok && len(r) == 2 {
			return elemString(r[0]) + "-" + elemString(r[1])
		}
		if c, ok := x["concat"].([]any); ok {
			parts := make([]string, len(c))
			for i, p := range c {
				parts[i] = elemString(p)
			}
			return strings.Join(parts, ".")
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Counters returns the names of the named counters, sorted.
func (r *Ruleset) Counters() []string {
	var out []string
	for _, o := range r.Objects {
		if o.Counter != nil {
			out = append(out, o.Counter.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Chain returns the named chain.
func (r *Ruleset) Chain(name string) *NftChain {
	for _, o := range r.Objects {
		if o.Chain != nil && o.Chain.Name == name {
			return o.Chain
		}
	}
	return nil
}

// Tables returns the tables of the ruleset.
func (r *Ruleset) Tables() []NftTable {
	var out []NftTable
	for _, o := range r.Objects {
		if o.Table != nil {
			out = append(out, *o.Table)
		}
	}
	return out
}

// Rules returns the rules of one chain in order.
func (r *Ruleset) Rules(chain string) []NftRule {
	var out []NftRule
	for _, o := range r.Objects {
		if o.Rule != nil && o.Rule.Chain == chain {
			out = append(out, *o.Rule)
		}
	}
	return out
}

// Set returns the named set or map.
func (r *Ruleset) Set(name string) *NftSet {
	for _, o := range r.Objects {
		if o.Set != nil && o.Set.Name == name {
			return o.Set
		}
		if o.Map != nil && o.Map.Name == name {
			return o.Map
		}
	}
	return nil
}
