package linux

import "strings"

// DockerUserState is what Chaos Gateway keeps in Docker's DOCKER-USER chain: the interfaces with
// an accept rule for incoming (-i) and outgoing (-o) traffic, recognized by the rule comment.
type DockerUserState struct {
	ChainExists bool
	In          []string
	Out         []string
	// Position is the index of the first foreign rule relative to ours: rules of Chaos Gateway
	// must stand in front of Docker's own (a RETURN at the end of the chain).
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
			foreign = true
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
