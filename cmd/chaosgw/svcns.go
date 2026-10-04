package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
)

// runSvcNS is `chaosgw svcns`: the holder of the service namespace (plan §3.3). It does nothing but
// keep the network namespace of its container alive. The container runs without a network and in the
// host's PID namespace, so the PID it writes is the one the executor (also in the host's PID
// namespace) attaches the namespace of: `ip netns attach`, then the veth pair to the gateway.
func runSvcNS(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("svcns", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pidFile := fs.String("pid-file", "/run/chaosgw/svcns/pid", "where to write the PID and the network namespace inode of this process")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := writePID(*pidFile); err != nil {
		fmt.Fprintf(stderr, "chaosgw svcns: %v\n", err)
		return 1
	}
	defer func() { _ = os.Remove(*pidFile) }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	return 0
}

// writePID writes this process's PID and its network namespace inode, space-separated, atomically:
// the API reads the file at any time. The inode lets the executor refuse to attach to this PID once it
// has been reused by another process (M6b-10): a bare PID file cannot tell the two apart.
func writePID(path string) error {
	inode, err := selfNetnsInode()
	if err != nil {
		return fmt.Errorf("this process's own network namespace: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pid-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(strconv.Itoa(os.Getpid()) + " " + strconv.FormatUint(inode, 10) + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// selfNetnsInode is this process's own network namespace, identified the same way the executor
// identifies one (the inode of the ns/net file).
func selfNetnsInode() (uint64, error) {
	fi, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("no inode information for /proc/self/ns/net")
	}
	return st.Ino, nil
}
