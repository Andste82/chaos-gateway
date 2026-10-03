package kernelsim

import (
	"os"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/linux"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// birdCmd simulates `bird -p -c file` (always accepts; the real parser is exercised by the bird
// package tests) and `birdc -s sock configure|show protocols all`. The simulated instance starts
// to run with the first configure and then reports no protocols.
func (k *Kernel) birdCmd(c executor.Command) (executor.Result, error) {
	a := c.Args
	switch {
	case c.Tool == executor.ToolBird && len(a) == 3 && a[0] == "-p":
		// the simulation knows one error: a text with the words "syntax error" (the real parser is
		// exercised by the bird package tests)
		if b, err := os.ReadFile(a[2]); err == nil && strings.Contains(string(b), "syntax error") {
			return executor.Result{Exit: 1, Stderr: a[2] + ":9:1 syntax error, unexpected CF_SYM_UNDEFINED\n"}, nil
		}
		return executor.Result{}, nil
	case c.Tool == executor.ToolBirdc && len(a) == 3 && a[0] == "-s" && a[2] == "configure":
		k.birdRunning = true
		return okr("BIRD 2.18 ready.\nReading configuration from /etc/bird/chaosgw.conf\nReconfigured\n")
	case c.Tool == executor.ToolBirdc && len(a) == 5 && a[0] == "-s" && a[2] == "show":
		if !k.birdRunning {
			return executor.Result{Exit: 1, Stderr: "birdc: Unable to connect to server control socket: No such file or directory\n"}, nil
		}
		if k.birdShow != "" {
			return okr(k.birdShow)
		}
		return okr("BIRD 2.18 ready.\nName       Proto      Table      State  Since         Info\n")
	}
	return fail("unsupported bird command %v", a)
}

// SetBirdProtocols makes the simulated BIRD answer `show protocols all` with out.
func (k *Kernel) SetBirdProtocols(out string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.birdShow = out
}

// SetNeighbors makes `ip neigh show` answer with the given entries.
func (k *Kernel) SetNeighbors(n []linux.Neighbor) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.neighbors = append([]linux.Neighbor(nil), n...)
}

// SetConntrack makes `conntrack -L` print the given text.
func (k *Kernel) SetConntrack(text string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.conntrack = text
}
