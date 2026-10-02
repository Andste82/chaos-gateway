package linux

import (
	"encoding/json"
	"fmt"
)

// NftObject is one object of an `nft -j` ruleset: exactly one of the pointer fields is set for the
// kinds this package knows; objects of other kinds (counters, quotas, ...) are left empty (rules and sets keep their expressions and elements raw).
type NftObject struct {
	Table *NftTable `json:"table,omitempty"`
	Chain *NftChain `json:"chain,omitempty"`
	Rule  *NftRule  `json:"rule,omitempty"`
	Set   *NftSet   `json:"set,omitempty"`
	Map   *NftSet   `json:"map,omitempty"`
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
