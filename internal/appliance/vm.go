package appliance

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Key is the throw-away SSH key of one run.
type Key struct {
	Signer ssh.Signer
	// Authorized is the line for authorized_keys.
	Authorized string
}

// NewKey creates an ed25519 key.
func NewKey() (*Key, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	return &Key{Signer: signer, Authorized: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))}, nil
}

// VM is a running gateway machine.
type VM struct {
	Dir       string
	SerialLog string
	// Addr is the address SSH reaches the VM at (its management address).
	Addr string

	host Host
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	key  *Key

	mu     sync.Mutex
	client *ssh.Client
	log    bytes.Buffer
}

// Boot creates an overlay of the base image, starts QEMU with the NICs of the config and returns
// at once; WaitSSH waits for the machine. QEMU runs through the host's sudo: it attaches to the
// tap devices.
func Boot(ctx context.Context, h Host, dir, baseImage, seed string, nics []NIC, addr string, key *Key) (*VM, error) {
	overlay := filepath.Join(dir, "disk.qcow2")
	if out, err := exec.CommandContext(ctx, "qemu-img", "create", "-q", "-f", "qcow2", "-b", baseImage, "-F", "qcow2", overlay, "10G").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("appliance: qemu-img: %v\n%s", err, out)
	}
	cfg := VMConfig{Disk: overlay, Seed: seed, NICs: nics, SerialLog: filepath.Join(dir, "serial.log"), KVM: HasKVM()}
	argv := h.argv(append(Cmd{"qemu-system-x86_64"}, QEMUArgs(cfg)...))
	vm := &VM{Dir: dir, SerialLog: cfg.SerialLog, Addr: addr, host: h, key: key, done: make(chan struct{})}
	vm.cmd = exec.Command(argv[0], argv[1:]...)
	vm.cmd.Stdout, vm.cmd.Stderr = &lockedWriter{vm}, &lockedWriter{vm}
	if err := vm.cmd.Start(); err != nil {
		return nil, fmt.Errorf("appliance: start QEMU: %w", err)
	}
	go func() {
		vm.err = vm.cmd.Wait()
		close(vm.done)
	}()
	return vm, nil
}

type lockedWriter struct{ vm *VM }

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.vm.mu.Lock()
	defer w.vm.mu.Unlock()
	return w.vm.log.Write(b)
}

// QEMULog is what QEMU itself printed.
func (v *VM) QEMULog() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.log.String()
}

// WaitSSH waits until the VM accepts the harness's key.
func (v *VM) WaitSSH(ctx context.Context, d time.Duration) error {
	deadline := time.Now().Add(d)
	cfg := &ssh.ClientConfig{User: GuestUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(v.key.Signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // a throw-away VM on a private bridge
		Timeout:         5 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-v.done:
			return fmt.Errorf("appliance: QEMU exited before SSH came up: %v\n%s", v.err, v.QEMULog())
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		c, err := ssh.Dial("tcp", net.JoinHostPort(v.Addr, "22"), cfg)
		if err == nil {
			v.mu.Lock()
			v.client = c
			v.mu.Unlock()
			return nil
		}
		last = err
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("appliance: the VM does not accept SSH within %v: %v", d, last)
}

// Result is the outcome of a command in the VM.
type Result struct {
	Stdout, Stderr string
	Exit           int
}

// Run runs a shell command in the VM with the given standard input.
func (v *VM) Run(ctx context.Context, command string, stdin io.Reader) (Result, error) {
	v.mu.Lock()
	c := v.client
	v.mu.Unlock()
	if c == nil {
		return Result{}, errors.New("appliance: not connected")
	}
	s, err := c.NewSession()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = s.Close() }()
	var out, errb bytes.Buffer
	s.Stdout, s.Stderr, s.Stdin = &out, &errb, stdin
	done := make(chan error, 1)
	go func() { done <- s.Run(command) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = s.Close()
		return Result{Stdout: out.String(), Stderr: errb.String(), Exit: -1}, ctx.Err()
	}
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *ssh.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.Exit = ee.ExitStatus()
	default:
		return res, err
	}
	return res, nil
}

// Must runs a command and fails on a non-zero exit.
func (v *VM) Must(ctx context.Context, command string) (string, error) {
	r, err := v.Run(ctx, command, nil)
	if err != nil {
		return r.Stdout + r.Stderr, err
	}
	if r.Exit != 0 {
		return r.Stdout + r.Stderr, fmt.Errorf("appliance: %q exited with %d\n%s%s", command, r.Exit, r.Stdout, r.Stderr)
	}
	return r.Stdout, nil
}

// Put writes a file in the VM.
func (v *VM) Put(ctx context.Context, remote string, mode os.FileMode, src io.Reader) error {
	cmd := fmt.Sprintf("sudo install -d -m 0755 %s && sudo tee %s >/dev/null && sudo chmod %o %s", shq(filepath.Dir(remote)), shq(remote), mode.Perm(), shq(remote))
	r, err := v.Run(ctx, cmd, src)
	if err != nil {
		return err
	}
	if r.Exit != 0 {
		return fmt.Errorf("appliance: put %s: exit %d: %s", remote, r.Exit, r.Stderr)
	}
	return nil
}

// PutFile copies a local file into the VM.
func (v *VM) PutFile(ctx context.Context, local, remote string, mode os.FileMode) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return v.Put(ctx, remote, mode, f)
}

// shq quotes a word for the shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Close ends the SSH connection and stops the VM.
func (v *VM) Close() {
	v.mu.Lock()
	if v.client != nil {
		_ = v.client.Close()
		v.client = nil
	}
	v.mu.Unlock()
	select {
	case <-v.done:
		return
	default:
	}
	// QEMU runs as root through sudo: ask it to stop with a signal it handles
	if v.cmd.Process != nil {
		a := v.host.argv(Cmd{"kill", "-TERM", fmt.Sprint(v.cmd.Process.Pid)})
		_ = exec.Command(a[0], a[1:]...).Run()
		_ = v.cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-v.done:
	case <-time.After(15 * time.Second):
		_ = v.cmd.Process.Kill()
		<-v.done
	}
}
