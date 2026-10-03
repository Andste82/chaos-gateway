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
	pidFile := fs.String("pid-file", "/run/chaosgw/svcns/pid", "where to write the PID of this process")
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

// writePID writes the PID atomically: the API reads the file at any time.
func writePID(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pid-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
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
