package testbed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The interception proxy of the gateway (plan §2.8, M22) for the tests: the Python sidecar in sidecars/tls-proxy, started the way
// the container starts it, with a mitmproxy that is installed in a virtual environment (the image of the testbed has one in
// /opt/mitm-venv, the development host in a place the environment variable names). It needs no root: it listens on the
// loopback of the machine it runs on, or, with a namespace, in that namespace (that needs root, like every namespace).

// MitmPythonEnv names the Python interpreter that has mitmproxy installed; the tests look for /opt/mitm-venv/bin/python and
// for the interpreter next to a `mitmdump` on the PATH when it is not set.
const MitmPythonEnv = "CHAOSGW_MITM_PYTHON"

// RequireMitmEnv, when set to "1", makes a test that needs mitmproxy fail where it would skip: the CI jobs that have it set it,
// so a missing installation cannot turn the tests green by skipping them.
const RequireMitmEnv = "CHAOSGW_REQUIRE_MITMPROXY"

// MitmPython returns the interpreter with mitmproxy, or false when there is none.
func MitmPython() (string, bool) {
	cands := []string{os.Getenv(MitmPythonEnv), "/opt/mitm-venv/bin/python"}
	if p, err := exec.LookPath("mitmdump"); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(p), "python"), filepath.Join(filepath.Dir(p), "python3"))
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if err := exec.Command(c, "-c", "import mitmproxy.tools.main").Run(); err == nil {
			return c, true
		}
	}
	return "", false
}

// SidecarDir is the directory of the sidecar's sources.
func SidecarDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "sidecars", "tls-proxy")
}

// ProxyOptions says how to start the interception proxy.
type ProxyOptions struct {
	// Listen is the address the proxy listens on, Control the URL of the control interface of the responder.
	Listen, Control string
	// API is the gateway's API as the proxy reaches it, TokenFile the service token and CertFile the API's certificate.
	API, TokenFile, CertFile string
	// CADir holds the test CA (the directory the API makes the CAs in).
	CADir string
	// UpstreamCA is a file of CA certificates that verify the servers the proxy connects to; empty uses the bundle of mitmproxy.
	UpstreamCA string
	// UpstreamInsecure is CHAOSGW_UPSTREAM_INSECURE=1: the proxy verifies no certificate of a server unless a case says otherwise.
	UpstreamInsecure bool
	// Namespace is the network namespace to start the proxy in; empty for the machine's own.
	Namespace string
	// Dir is the proxy's working directory (its configuration directory is made in it); required.
	Dir string
}

// Proxy is a running interception proxy.
type Proxy struct {
	cmd  *exec.Cmd
	out  *lockedBuffer
	done chan struct{}
	err  error
	opt  ProxyOptions
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// StartProxy starts the proxy (the launcher of the sidecar) and returns when the process runs; WaitReady waits for its port.
func StartProxy(python string, o ProxyOptions) (*Proxy, error) {
	if o.Dir == "" || o.Listen == "" || o.CADir == "" {
		return nil, errors.New("testbed: the proxy needs a directory, an address and the CA directory")
	}
	args := []string{"-m", "chaosgw_tlsproxy.launcher", "--listen", o.Listen, "--ca-dir", o.CADir, "--confdir", filepath.Join(o.Dir, "mitmproxy")}
	if o.API != "" {
		args = append(args, "--api", o.API)
	}
	if o.TokenFile != "" {
		args = append(args, "--service-token-file", o.TokenFile)
	}
	if o.CertFile != "" {
		args = append(args, "--api-cert-file", o.CertFile)
	}
	if o.Control != "" {
		args = append(args, "--control", o.Control)
	}
	if o.UpstreamCA != "" {
		args = append(args, "--upstream-ca-file", o.UpstreamCA)
	}
	if o.UpstreamInsecure {
		args = append(args, "--upstream-insecure")
	}
	name, full := python, append([]string{}, args...)
	if o.Namespace != "" {
		name, full = "ip", append([]string{"netns", "exec", o.Namespace, python}, args...)
	}
	cmd := exec.Command(name, full...)
	cmd.Dir = SidecarDir()
	cmd.Env = append(CleanEnv(os.Environ()), "PYTHONPATH="+SidecarDir(), "PYTHONUNBUFFERED=1", "HOME="+o.Dir)
	if v := os.Getenv("CHAOSGW_MITM_VERBOSITY"); v != "" {
		cmd.Env = append(cmd.Env, "CHAOSGW_MITM_VERBOSITY="+v) // debugging a test: what mitmproxy decides
	}
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Proxy{cmd: cmd, out: out, done: make(chan struct{}), opt: o}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}

// Output is what the proxy has written so far.
func (p *Proxy) Output() string { return p.out.String() }

// WaitReady returns when the proxy accepts connections on its address, or an error when it ended or the time is up.
func (p *Proxy) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	dial := (&net.Dialer{Timeout: time.Second}).DialContext
	if p.opt.Namespace != "" {
		dial = DialerIn(p.opt.Namespace)
	}
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return fmt.Errorf("the proxy ended: %w\n%s", p.err, p.Output())
		default:
		}
		c, err := dial(context.Background(), "tcp", p.opt.Listen)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the proxy does not listen on %s after %s\n%s", p.opt.Listen, timeout, p.Output())
}

// Stop ends the proxy and everything it started.
func (p *Proxy) Stop() {
	select {
	case <-p.done:
		return
	default:
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		<-p.done
	}
}

// Running reports whether the proxy process is alive.
func (p *Proxy) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Contains reports whether the proxy's output has the text.
func (p *Proxy) Contains(s string) bool { return strings.Contains(p.Output(), s) }
