// Command chaosctl is the CLI for CI pipelines (plan §2.15, M18): apply profiles, set faults with a TTL, run
// scenarios and wait for their reports, wait for devices, and run a command under a fault. The commands are in
// package internal/ctl.
package main

import (
	"io"
	"os"

	"github.com/Andste82/chaos-gateway/internal/ctl"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return ctl.Run(args, ctl.Env{Stdin: stdin, Stdout: stdout, Stderr: stderr})
}
