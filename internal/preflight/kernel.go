package preflight

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

// MinKernel is the oldest kernel the plan supports: the Ubuntu 24.04 GA kernel (plan §1.5, D1).
const MinKernel = "6.8"

// Release is a parsed kernel release such as "6.8.0-142-generic".
type Release struct{ Major, Minor int }

// ParseRelease reads the major and minor number from a release string.
func ParseRelease(s string) (Release, error) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return Release{}, fmt.Errorf("preflight: cannot parse kernel release %q", s)
	}
	major, err1 := strconv.Atoi(parts[0])
	minorDigits := parts[1]
	if i := strings.IndexFunc(minorDigits, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		minorDigits = minorDigits[:i]
	}
	minor, err2 := strconv.Atoi(minorDigits)
	if err1 != nil || err2 != nil {
		return Release{}, fmt.Errorf("preflight: cannot parse kernel release %q", s)
	}
	return Release{Major: major, Minor: minor}, nil
}

// AtLeast reports whether r is at least min.
func (r Release) AtLeast(min Release) bool {
	return r.Major > min.Major || (r.Major == min.Major && r.Minor >= min.Minor)
}

// CheckKernel returns an error if the release is older than MinKernel or unreadable.
func CheckKernel(release string) error {
	got, err := ParseRelease(release)
	if err != nil {
		return err
	}
	min, _ := ParseRelease(MinKernel)
	if !got.AtLeast(min) {
		return fmt.Errorf("kernel %s is older than the minimum %s", strings.TrimSpace(release), MinKernel)
	}
	return nil
}

// Status says where a module is.
type Status int

const (
	// Missing means the kernel neither has the module loaded nor can it load it.
	Missing Status = iota
	// Available means the module exists on disk and modprobe can load it.
	Available
	// Loaded means the module is loaded or built into the kernel.
	Loaded
)

func (s Status) String() string {
	switch s {
	case Loaded:
		return "loaded"
	case Available:
		return "available"
	default:
		return "missing"
	}
}

// ModuleReport is the state of one module.
type ModuleReport struct {
	Module
	Status Status
}

// Env is the view of the machine the checks read: a file system rooted at "/" and the kernel
// release. Tests substitute an in-memory file system.
type Env struct {
	FS      fs.FS
	Release string
}

// normalize maps the module names of the kernel (dashes or underscores) to one spelling.
func normalize(name string) string { return strings.ReplaceAll(name, "-", "_") }

// moduleIndex reads the names of all modules listed in a modules.dep or modules.builtin file.
func moduleIndex(fsys fs.FS, file string) map[string]bool {
	out := map[string]bool{}
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		// "kernel/net/sched/sch_netem.ko.zst: kernel/..." — only the part before the colon names
		// the module itself.
		if i := strings.IndexByte(line, ':'); i >= 0 {
			line = line[:i]
		}
		base := path.Base(strings.TrimSpace(line))
		if i := strings.Index(base, ".ko"); i >= 0 {
			out[normalize(base[:i])] = true
		}
	}
	return out
}

// CheckModules reports the state of the given modules on env.
func CheckModules(env Env, mods []Module) []ModuleReport {
	release := strings.TrimSpace(env.Release)
	dir := path.Join("lib/modules", release)
	if _, err := fs.Stat(env.FS, dir); err != nil {
		// some layouts have no /lib -> usr/lib link
		if _, err := fs.Stat(env.FS, path.Join("usr/lib/modules", release)); err == nil {
			dir = path.Join("usr/lib/modules", release)
		}
	}
	available := moduleIndex(env.FS, path.Join(dir, "modules.dep"))
	builtin := moduleIndex(env.FS, path.Join(dir, "modules.builtin"))
	out := make([]ModuleReport, 0, len(mods))
	for _, m := range mods {
		name := normalize(m.Name)
		r := ModuleReport{Module: m, Status: Missing}
		if _, err := fs.Stat(env.FS, path.Join("sys/module", name)); err == nil || builtin[name] {
			r.Status = Loaded
		} else if available[name] {
			r.Status = Available
		}
		out = append(out, r)
	}
	return out
}

// MissingModules returns the required modules among reports that are missing.
func MissingModules(reports []ModuleReport) []Module {
	var out []Module
	for _, r := range reports {
		if r.Status == Missing && !r.Later {
			out = append(out, r.Module)
		}
	}
	return out
}
