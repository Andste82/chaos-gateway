package main

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// toolsKernel is a simulated kernel that claims to have every tool: the host checks run only on a runner that has tools (an
// executor on a simulated kernel has none to report), and here they may fail, since only the presence of one check is looked at.
type toolsKernel struct{ *kernelsim.Kernel }

func (toolsKernel) Path(executor.Tool) (string, bool) { return "/bin/true", true }

// The host clock is read through adjtimex, which every Linux kernel answers; an error here would make the check host.clock a
// permanent warning on a healthy host.
func TestTheHostClockCanBeRead(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("adjtimex needs Linux")
	}
	st, err := hostClock()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if st.MaxError < 0 {
		t.Errorf("a negative maximum error: %+v", st)
	}
}

// The process builds its executor from baseExecOptions: the preflight of such an executor lists the check host.clock. Dropping
// WithHostClock from the options (the check would silently be absent from the health report and the log) fails here.
func TestTheExecutorOfTheProcessReportsTheHostClock(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("adjtimex needs Linux")
	}
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	k := kernelsim.New()
	k.AddDockerChain()
	ex, err := executor.New(toolsKernel{k}, baseExecOptions(log, dir+"/state.json", dir, nil, "", "")...)
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	for _, c := range ex.HostPreflight(context.Background()).Checks {
		if c.ID == "host.clock" {
			return
		}
	}
	t.Errorf("the preflight of the executor has no check host.clock")
}
