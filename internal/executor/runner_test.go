package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

func TestRunnerArgv(t *testing.T) {
	r := &ExecRunner{paths: map[Tool]string{ToolIP: "/usr/sbin/ip", ToolNft: "/usr/sbin/nft", ToolTC: "/usr/sbin/tc"}}
	for _, c := range []struct {
		cmd  Command
		want string
	}{
		{Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}}, "/usr/sbin/nft -j -f -"},
		{Command{Tool: ToolTC, Args: []string{"-batch", "-"}, NS: "gw"}, "/usr/sbin/ip netns exec gw /usr/sbin/tc -batch -"},
		{Command{Tool: ToolIP, Args: []string{"-j", "link"}, NS: "a.b-c"}, "/usr/sbin/ip netns exec a.b-c /usr/sbin/ip -j link"},
	} {
		argv, err := r.argv(c.cmd)
		if err != nil || strings.Join(argv, " ") != c.want {
			t.Errorf("%v: %v %v", c.cmd, argv, err)
		}
	}
	if _, err := r.argv(Command{Tool: ToolEthtool}); err == nil {
		t.Error("a tool without a fixed path must fail")
	}
	if _, err := r.argv(Command{Tool: ToolNft, NS: "x y"}); err == nil {
		t.Error("an invalid namespace name must fail")
	}
	if _, err := r.argv(Command{Tool: ToolNft, NS: "gw"}); err != nil {
		t.Error(err)
	}
	r2 := &ExecRunner{paths: map[Tool]string{ToolNft: "/usr/sbin/nft"}}
	if _, err := r2.argv(Command{Tool: ToolNft, NS: "gw"}); err == nil {
		t.Error("a namespace needs ip")
	}
}

func TestRunnerUsesOnlyFixedAbsolutePaths(t *testing.T) {
	for tool, list := range candidates {
		for _, p := range list {
			if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
				t.Errorf("%s: %q is not a fixed absolute path", tool, p)
			}
		}
	}
	r := NewExecRunner()
	for tool, p := range r.paths {
		if !strings.HasPrefix(p, "/") {
			t.Errorf("%s resolved to %q", tool, p)
		}
	}
}

// The read path runs the real ip against this machine: it needs no privileges.
func TestRunnerRunsRealIPAndTheParsersAcceptItsOutput(t *testing.T) {
	r := NewExecRunner()
	if _, ok := r.Path(ToolIP); !ok {
		t.Skip("ip is not installed")
	}
	e := newExec(t, r)
	for _, what := range []string{ReadLinks, ReadAddrs, ReadRoutes, ReadRules} {
		out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"`+what+`"}`))
		if err != nil || len(out.Data) != 1 {
			t.Fatalf("%s: %+v %v", what, out, err)
		}
		var items []json.RawMessage
		if err := json.Unmarshal(out.Data[0], &items); err != nil || len(items) == 0 {
			t.Errorf("%s: %s %v", what, out.Data[0], err)
		}
	}
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"links"}`))
	if err != nil {
		t.Fatal(err)
	}
	var links []linux.Link
	_ = json.Unmarshal(out.Data[0], &links)
	var lo bool
	for _, l := range links {
		lo = lo || l.Name == "lo"
	}
	if !lo {
		t.Errorf("no loopback in %+v", links)
	}
}

func TestRunnerReportsExitStatusAndMissingNamespace(t *testing.T) {
	r := NewExecRunner()
	if _, ok := r.Path(ToolIP); !ok {
		t.Skip("ip is not installed")
	}
	res, err := r.Run(context.Background(), Command{Tool: ToolIP, Args: []string{"-j", "link", "show", "dev", "does-not-exist0"}})
	if err != nil || res.Exit == 0 || res.Stderr == "" {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = r.Run(context.Background(), Command{Tool: ToolIP, Args: []string{"-j", "link"}, NS: "cgx-no-such-ns"})
	if err != nil || res.Exit == 0 {
		t.Fatalf("a missing namespace is a failed command: %+v %v", res, err)
	}
}

// M6a-04 test: Stream runs a real process, delivers its stdout line by line as it is written (not
// only once it exits), and stop kills it instead of waiting for it to exit on its own.
func TestRunnerStreamsRealOutputAndStopKillsIt(t *testing.T) {
	const sh = Tool("sh")
	r := &ExecRunner{paths: map[Tool]string{sh: "/bin/sh"}}
	// `exec` replaces the shell with sleep in place, instead of forking it as a child that would
	// keep the stdout pipe open on its own after the shell is killed.
	lines, stop, err := r.Stream(context.Background(), Command{Tool: sh, Args: []string{"-c", "echo one; echo two; exec sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, want := range []string{"one", "two"} {
		select {
		case got := <-lines:
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no line (want %q)", want)
		}
	}
	if time.Since(start) > 10*time.Second {
		t.Error("the lines took long enough to suggest they only arrived once the process exited")
	}
	stop() // must return well before the 30s sleep would, by killing the process
	if time.Since(start) > 10*time.Second {
		t.Error("stop did not kill the process promptly")
	}
	select {
	case _, ok := <-lines:
		if ok {
			t.Error("a line after stop")
		}
	case <-time.After(time.Second):
		t.Fatal("the channel did not close")
	}
}

func TestRunnerStreamNeedsAFixedPath(t *testing.T) {
	r := &ExecRunner{paths: map[Tool]string{}}
	if _, _, err := r.Stream(context.Background(), Command{Tool: ToolConntrack}); err == nil {
		t.Error("accepted")
	}
}

func TestRunnerTimeoutAndLimits(t *testing.T) {
	r := NewExecRunner()
	if _, ok := r.Path(ToolIP); !ok {
		t.Skip("ip is not installed")
	}
	r.Timeout = time.Nanosecond
	if _, err := r.Run(context.Background(), Command{Tool: ToolIP, Args: []string{"-j", "link"}}); err == nil {
		t.Error("a timeout must be an error")
	}
	var b limitedBuffer
	big := make([]byte, maxOutput)
	_, _ = b.Write(big)
	_, _ = b.Write([]byte("more"))
	if b.Len() != maxOutput {
		t.Errorf("the buffer grew beyond its limit: %d", b.Len())
	}
}
