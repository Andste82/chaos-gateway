package linux

import "strings"

// DockerUserState is what Chaos Gateway keeps in Docker's DOCKER-USER chain: the interfaces with
// an accept rule for incoming (-i) and outgoing (-o) traffic, recognized by the rule comment.
type DockerUserState struct {
	ChainExists bool
	In          []string
	Out         []string
	// OursFirst is false when a foreign rule that ends the traversal (Docker's RETURN) stands in
	// front of ours: such a rule makes ours ineffective.
	OursFirst bool
}

// ParseDockerUser parses `iptables -S DOCKER-USER`.
func ParseDockerUser(out string) *DockerUserState {
	st := &DockerUserState{ChainExists: true, OursFirst: true}
	foreign := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "-A" {
			continue
		}
		ours := false
		for i, w := range f {
			if w == "--comment" && i+1 < len(f) && strings.Trim(f[i+1], `"`) == "chaosgw" {
				ours = true
			}
		}
		if !ours {
			// only a rule that ends the traversal in front of ours makes ours ineffective
			for i, w := range f {
				if w == "-j" && i+1 < len(f) {
					switch f[i+1] {
					case "RETURN", "ACCEPT", "DROP", "REJECT":
						foreign = true
					}
				}
			}
			continue
		}
		if foreign {
			st.OursFirst = false
		}
		for i, w := range f {
			if i+1 >= len(f) {
				break
			}
			switch w {
			case "-i":
				st.In = append(st.In, f[i+1])
			case "-o":
				st.Out = append(st.Out, f[i+1])
			}
		}
	}
	return st
}
