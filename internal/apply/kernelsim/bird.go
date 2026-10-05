package kernelsim

import (
	"context"
	"fmt"
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

// SetBirdRunning makes the simulated BIRD daemon unreachable (false) or reachable again (true),
// independent of whether it has ever been configured (M4c-02).
func (k *Kernel) SetBirdRunning(running bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.birdRunning = running
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

// HasConntrackWatch reports whether a conntrack watch (M6a-04) is currently open: tests use it to
// wait for FollowConntrack's watch to actually be listening before calling ScriptConntrackEvent.
func (k *Kernel) HasConntrackWatch() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.conntrackEvents != nil
}

// ScriptConntrackEvent delivers one line to an open conntrack watch (M6a-04: `executor.Watch`,
// `engine.FollowConntrack`), as if the kernel had just reported it over `conntrack -E`. It blocks
// until a watch is listening (tests call it after starting the watch they want to drive) and does
// nothing if none is open.
func (k *Kernel) ScriptConntrackEvent(line string) {
	k.mu.Lock()
	ch := k.conntrackEvents
	k.mu.Unlock()
	if ch != nil {
		ch <- line
	}
}

// Stream implements executor.Streamer for a conntrack watch (M6a-04): the real tool is never run;
// the lines are whatever ScriptConntrackEvent delivers. Only one watch at a time is simulated.
func (k *Kernel) Stream(ctx context.Context, c executor.Command) (<-chan string, func(), error) {
	if c.Tool != executor.ToolConntrack {
		return nil, nil, fmt.Errorf("kernelsim: cannot stream %s", c.Tool)
	}
	ch := make(chan string)
	k.mu.Lock()
	k.conntrackEvents = ch
	k.mu.Unlock()
	cctx, cancel := context.WithCancel(ctx)
	out := make(chan string)
	done := make(chan struct{})
	go func() {
		defer close(out)
		defer close(done)
		for {
			select {
			case line := <-ch:
				select {
				case out <- line:
				case <-cctx.Done():
					return
				}
			case <-cctx.Done():
				return
			}
		}
	}()
	stop := func() {
		cancel()
		<-done
		k.mu.Lock()
		if k.conntrackEvents == ch {
			k.conntrackEvents = nil
		}
		k.mu.Unlock()
	}
	return out, stop, nil
}
