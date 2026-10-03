package preflight

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Tools the namespace testbed calls (plan §4.5, test tools in the image).
var TestbedTools = []string{"ip", "tc", "nft", "ping", "ethtool", "wg", "wg-quick", "bird", "birdc"}

// Report is the result of a preflight run.
type Report struct {
	Release string
	// KernelErr is set when the kernel is too old or its release cannot be read.
	KernelErr error
	Modules   []ModuleReport
	// MissingTools lists tools that are not on PATH.
	MissingTools []string
	// NamespaceErr is set when the process cannot create a named network namespace, as in an
	// unprivileged container.
	NamespaceErr error
}

// OK reports whether the machine can run the namespace testbed directly (level 1).
func (r Report) OK() bool {
	return r.KernelErr == nil && len(MissingModules(r.Modules)) == 0 &&
		len(r.MissingTools) == 0 && r.NamespaceErr == nil
}

// Problems lists what is wrong, one line each; empty when OK.
func (r Report) Problems() []string {
	var out []string
	if r.KernelErr != nil {
		out = append(out, "kernel: "+r.KernelErr.Error())
	}
	if miss := MissingModules(r.Modules); len(miss) > 0 {
		names := make([]string, len(miss))
		for i, m := range miss {
			names[i] = m.Name
		}
		out = append(out, "kernel modules missing: "+strings.Join(names, ", "))
	}
	if len(r.MissingTools) > 0 {
		out = append(out, "tools missing: "+strings.Join(r.MissingTools, ", "))
	}
	if r.NamespaceErr != nil {
		out = append(out, "cannot create network namespaces: "+r.NamespaceErr.Error())
	}
	return out
}

// String formats the report for a terminal.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "kernel %s\n", r.Release)
	for _, m := range r.Modules {
		note := ""
		if m.Later {
			note = " (needed after V1)"
		}
		fmt.Fprintf(&b, "  module %-16s %-9s %s [%s]%s\n", m.Name, m.Status, m.Feature, m.Milestone, note)
	}
	if probs := r.Problems(); len(probs) == 0 {
		b.WriteString("ok: the namespace testbed can run here (level 1)\n")
	} else {
		for _, p := range probs {
			fmt.Fprintf(&b, "problem: %s\n", p)
		}
	}
	return b.String()
}

// HostEnv returns the environment of the running machine.
func HostEnv() (Env, error) {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return Env{}, err
	}
	rel := make([]byte, 0, len(u.Release))
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		rel = append(rel, byte(c))
	}
	return Env{FS: os.DirFS("/"), Release: string(rel)}, nil
}

// Run checks the running machine. Creating and removing a throw-away namespace is the only
// change it makes.
func Run(ctx context.Context) (Report, error) {
	env, err := HostEnv()
	if err != nil {
		return Report{}, err
	}
	r := Report{Release: env.Release}
	r.KernelErr = CheckKernel(env.Release)
	r.Modules = CheckModules(env, Modules())
	for _, tool := range TestbedTools {
		if _, err := exec.LookPath(tool); err != nil {
			r.MissingTools = append(r.MissingTools, tool)
		}
	}
	if len(r.MissingTools) == 0 {
		r.NamespaceErr = CheckNamespaces(ctx)
	}
	return r, nil
}

// CheckNamespaces creates and deletes a named network namespace, the operation every testbed
// needs. It fails in an unprivileged container (no CAP_SYS_ADMIN, no writable /run/netns).
func CheckNamespaces(ctx context.Context) error {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	name := "pfchk-" + hex.EncodeToString(b[:])
	if out, err := exec.CommandContext(ctx, "ip", "netns", "add", name).CombinedOutput(); err != nil {
		return fmt.Errorf("ip netns add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "ip", "netns", "del", name).CombinedOutput(); err != nil {
		return fmt.Errorf("ip netns del: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ModprobeArgs returns the arguments that load mods with one modprobe call. Modules that do not
// exist are not an error for the caller to handle here: -a loads every module it can and reports
// the rest.
func ModprobeArgs(mods []Module) []string {
	args := []string{"-a", "-q"}
	for _, m := range mods {
		args = append(args, m.Name)
	}
	return args
}

// Load loads the modules with modprobe. It needs privileges.
func Load(ctx context.Context, mods []Module) error {
	out, err := exec.CommandContext(ctx, "modprobe", ModprobeArgs(mods)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("modprobe: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
