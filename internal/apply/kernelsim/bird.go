package kernelsim

import (
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// birdCmd simulates `bird -p -c file` (always accepts; the real parser is exercised by the bird
// package tests) and `birdc -s sock configure|show protocols all`. The simulated instance starts
// to run with the first configure and then reports no protocols.
func (k *Kernel) birdCmd(c executor.Command) (executor.Result, error) {
	a := c.Args
	switch {
	case c.Tool == executor.ToolBird && len(a) == 3 && a[0] == "-p":
		return executor.Result{}, nil
	case c.Tool == executor.ToolBirdc && len(a) == 3 && a[0] == "-s" && a[2] == "configure":
		k.birdRunning = true
		return okr("BIRD 2.18 ready.\nReading configuration from /etc/bird/chaosgw.conf\nReconfigured\n")
	case c.Tool == executor.ToolBirdc && len(a) == 5 && a[0] == "-s" && a[2] == "show":
		if !k.birdRunning {
			return executor.Result{Exit: 1, Stderr: "birdc: Unable to connect to server control socket: No such file or directory\n"}, nil
		}
		return okr("BIRD 2.18 ready.\nName       Proto      Table      State  Since         Info\n")
	}
	return fail("unsupported bird command %v", a)
}
