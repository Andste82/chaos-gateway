package linux

import (
	"encoding/json"
)

// Qdisc is one entry of `tc -j qdisc show`. Options stay raw: their shape depends on the kind.
type Qdisc struct {
	Kind    string          `json:"kind"`
	Handle  string          `json:"handle"`
	Dev     string          `json:"dev"`
	Parent  string          `json:"parent"`
	Root    bool            `json:"root"`
	Refcnt  int             `json:"refcnt"`
	Options json.RawMessage `json:"options"`
}

// Class is one entry of `tc -j class show`.
type Class struct {
	Kind    string          `json:"class"`
	Handle  string          `json:"handle"`
	Dev     string          `json:"dev"`
	Parent  string          `json:"parent"`
	Leaf    string          `json:"leaf"`
	Root    bool            `json:"root"`
	Rate    int64           `json:"rate"`
	Ceil    int64           `json:"ceil"`
	Options json.RawMessage `json:"options"`
}

// Filter is one entry of `tc -j filter show`. tc prints a bare header entry (without options)
// before each filter; the entry with options carries the filter itself.
type Filter struct {
	Protocol string          `json:"protocol"`
	Pref     int             `json:"pref"`
	Kind     string          `json:"kind"`
	Chain    int             `json:"chain"`
	Options  json.RawMessage `json:"options"`
}

// ParseQdiscs parses `tc -j qdisc show`.
func ParseQdiscs(data []byte) ([]Qdisc, error) { return parseList[Qdisc]("tc qdisc", data) }

// ParseClasses parses `tc -j class show`.
func ParseClasses(data []byte) ([]Class, error) { return parseList[Class]("tc class", data) }

// ParseFilters parses `tc -j filter show` and drops the bare header entries.
func ParseFilters(data []byte) ([]Filter, error) {
	all, err := parseList[Filter]("tc filter", data)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, f := range all {
		if len(f.Options) > 0 {
			out = append(out, f)
		}
	}
	return out, nil
}
