package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Streamer starts a long-running command and streams its stdout line by line (M6a-04: `conntrack
// -E`). The lines channel closes once the command exits, for any reason; stop ends it early and
// blocks until the process has been reaped. The production runner (ExecRunner) implements it; a
// Runner that does not is simply unable to serve a watch.
type Streamer interface {
	Stream(ctx context.Context, c Command) (lines <-chan string, stop func(), err error)
}

// Result is the outcome of one command.
type Result struct {
	Stdout string
	Stderr string
	Exit   int
}

// Runner runs commands. The production implementation is ExecRunner; tests use a fake.
type Runner interface {
	// Run returns an error only when the command could not run at all (binary missing, killed by
	// the context); a non-zero exit status is reported in Result.Exit.
	Run(ctx context.Context, c Command) (Result, error)
}

// candidates lists the fixed locations of each binary. PATH is never consulted.
var candidates = map[Tool][]string{
	ToolIP:        {"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip", "/bin/ip"},
	ToolNft:       {"/usr/sbin/nft", "/sbin/nft", "/usr/bin/nft"},
	ToolTC:        {"/usr/sbin/tc", "/sbin/tc", "/usr/bin/tc"},
	ToolEthtool:   {"/usr/sbin/ethtool", "/sbin/ethtool", "/usr/bin/ethtool"},
	ToolIptables:  {"/usr/sbin/iptables", "/sbin/iptables", "/usr/bin/iptables"},
	ToolSysctl:    {"/usr/sbin/sysctl", "/sbin/sysctl", "/usr/bin/sysctl"},
	ToolWg:        {"/usr/bin/wg", "/usr/sbin/wg", "/bin/wg"},
	ToolBird:      {"/usr/sbin/bird", "/usr/bin/bird", "/sbin/bird"},
	ToolBirdc:     {"/usr/sbin/birdc", "/usr/bin/birdc", "/sbin/birdc"},
	ToolConntrack: {"/usr/sbin/conntrack", "/sbin/conntrack", "/usr/bin/conntrack"},
}

const (
	defaultTimeout = 60 * time.Second
	maxOutput      = 16 << 20
)

// ExecRunner runs the real tools. In a network namespace the command runs as
// `ip netns exec <ns> <tool> ...`.
type ExecRunner struct {
	paths   map[Tool]string
	Timeout time.Duration
}

// NewExecRunner resolves every tool to its fixed path. Tools that are not installed stay
// unresolved; running them fails.
func NewExecRunner() *ExecRunner {
	r := &ExecRunner{paths: map[Tool]string{}, Timeout: defaultTimeout}
	for tool, list := range candidates {
		for _, p := range list {
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
				r.paths[tool] = p
				break
			}
		}
	}
	return r
}

// Path returns the resolved binary of a tool.
func (r *ExecRunner) Path(t Tool) (string, bool) { p, ok := r.paths[t]; return p, ok }

// argv builds the argument vector; it is separate from Run so the command building is testable.
func (r *ExecRunner) argv(c Command) ([]string, error) {
	bin, ok := r.paths[c.Tool]
	if !ok {
		return nil, fmt.Errorf("%s is not installed in a fixed location", c.Tool)
	}
	if c.NS == "" {
		return append([]string{bin}, c.Args...), nil
	}
	ip, ok := r.paths[ToolIP]
	if !ok {
		return nil, errors.New("ip is not installed in a fixed location")
	}
	if !nsName.MatchString(c.NS) {
		return nil, fmt.Errorf("invalid namespace name %q", c.NS)
	}
	return append([]string{ip, "netns", "exec", c.NS, bin}, c.Args...), nil
}

// Stream implements Streamer: it starts the command (no timeout; it runs until stop or ctx ends)
// and sends each line of its stdout on the returned channel, which closes once the process has
// exited and been reaped, by any path (ctx cancelled, stop called, or the command exiting on its
// own, which is always an error for a long-running watch).
func (r *ExecRunner) Stream(ctx context.Context, c Command) (<-chan string, func(), error) {
	argv, err := r.argv(c)
	if err != nil {
		return nil, nil, err
	}
	cctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}
	lines := make(chan string)
	finished := make(chan struct{})
	go func() {
		defer close(lines)
		defer close(finished)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 4<<10), maxOutput)
	scan:
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-cctx.Done():
				break scan
			}
		}
		_ = cmd.Wait()
	}()
	stop := func() {
		cancel()
		<-finished
	}
	return lines, stop, nil
}

// Run implements Runner.
func (r *ExecRunner) Run(ctx context.Context, c Command) (Result, error) {
	argv, err := r.argv(c)
	if err != nil {
		return Result{}, err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.Stdin = strings.NewReader(c.Stdin)
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &ee) && ctx.Err() == nil:
		res.Exit = ee.ExitCode()
		return res, nil
	case ctx.Err() != nil:
		return res, fmt.Errorf("%s: %w", c, ctx.Err())
	}
	return res, fmt.Errorf("%s: %w", c, err)
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxOutput {
		return len(p), nil // drop the rest: the caller only needs a bounded excerpt
	}
	return b.Buffer.Write(p)
}
