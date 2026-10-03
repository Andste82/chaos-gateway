package bird

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ProtocolStatus is one protocol as `birdc show protocols all` reports it.
type ProtocolStatus struct {
	Name  string `json:"name"`
	Proto string `json:"proto"` // BGP, OSPF, Babel, Kernel, Static, Device, ...
	// State is up, down, start or flush; Info is BIRD's remark (Established, Connect, Active, Limit hit, ...).
	State string `json:"state"`
	Since string `json:"since"`
	Info  string `json:"info"`
	// Imported, Filtered and Exported are the route counts of the protocol's channel.
	Imported int `json:"imported"`
	Filtered int `json:"filtered"`
	Exported int `json:"exported"`
	// Neighbor is the neighbor address of a BGP protocol.
	Neighbor string `json:"neighbor,omitempty"`
	// LastError is the last error of a BGP protocol, empty when there is none.
	LastError string `json:"last_error,omitempty"`
	// ImportLimit is the configured import limit, 0 for none.
	ImportLimit int `json:"import_limit,omitempty"`
}

// Established reports whether the protocol's adjacency is up: a BGP session in the state
// Established, an OSPF or Babel protocol that is up.
func (p ProtocolStatus) Established() bool {
	if p.State != "up" {
		return false
	}
	switch p.Proto {
	case "BGP":
		return p.Info == "Established"
	}
	return true
}

var (
	headerRE  = regexp.MustCompile(`^Name\s+Proto\s+Table\s+State\s+Since\s+Info`)
	protoRE   = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\d{2}:\d{2}:\d{2}(?:\.\d+)?|\d{4}-\d{2}-\d{2}(?:\s+\d{2}:\d{2}:\d{2}(?:\.\d+)?)?)\s*(.*)$`)
	routesRE  = regexp.MustCompile(`^\s+Routes:\s+(\d+) imported(?:, (\d+) filtered)?(?:, (\d+) exported)?`)
	neighbRE  = regexp.MustCompile(`^\s+Neighbor address:\s+(\S+)`)
	lastErrRE = regexp.MustCompile(`^\s+Last error:\s+(.*\S)`)
	limitRE   = regexp.MustCompile(`^\s+Import limit:\s+(\d+)`)
)

// ParseProtocols parses `birdc show protocols [all]`.
func ParseProtocols(out string) ([]ProtocolStatus, error) {
	var res []ProtocolStatus
	inTable := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if headerRE.MatchString(line) {
			inTable = true
			continue
		}
		if !inTable || strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			m := protoRE.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("parse birdc output: %q", line)
			}
			res = append(res, ProtocolStatus{Name: m[1], Proto: m[2], State: m[4], Since: m[5], Info: strings.TrimSpace(m[6])})
			continue
		}
		if len(res) == 0 {
			continue
		}
		p := &res[len(res)-1]
		if m := routesRE.FindStringSubmatch(line); m != nil {
			p.Imported, _ = strconv.Atoi(m[1])
			p.Filtered, _ = strconv.Atoi(m[2])
			p.Exported, _ = strconv.Atoi(m[3])
		} else if m := neighbRE.FindStringSubmatch(line); m != nil {
			p.Neighbor = m[1]
		} else if m := lastErrRE.FindStringSubmatch(line); m != nil {
			p.LastError = m[1]
		} else if m := limitRE.FindStringSubmatch(line); m != nil {
			p.ImportLimit, _ = strconv.Atoi(m[1])
		}
	}
	if !inTable {
		return nil, fmt.Errorf("parse birdc output: no protocol table")
	}
	return res, nil
}
