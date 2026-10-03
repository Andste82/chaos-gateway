package linux

import (
	"encoding/json"
	"fmt"
)

// Neighbor is an entry of `ip -j neigh show`: the ARP table.
type Neighbor struct {
	Dst    string   `json:"dst"`
	Dev    string   `json:"dev"`
	LLAddr string   `json:"lladdr"`
	State  []string `json:"state"`
}

// Usable reports whether the entry maps the address to a MAC: FAILED and INCOMPLETE entries do not.
func (n Neighbor) Usable() bool {
	if n.LLAddr == "" {
		return false
	}
	for _, s := range n.State {
		if s == "FAILED" || s == "INCOMPLETE" {
			return false
		}
	}
	return true
}

// Stale reports whether the kernel has not confirmed the entry lately.
func (n Neighbor) Stale() bool {
	for _, s := range n.State {
		if s == "STALE" || s == "DELAY" || s == "PROBE" {
			return true
		}
	}
	return false
}

// ParseNeighbors parses `ip -j neigh show`.
func ParseNeighbors(b []byte) ([]Neighbor, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out []Neighbor
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("linux: neighbors: %w", err)
	}
	return out, nil
}
