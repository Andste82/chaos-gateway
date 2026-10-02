// Command chaosctl is the CLI for CI pipelines (plan §2.15, M18): apply profiles, set faults
// with a TTL, run scenarios, wait for devices. Until M18 only `version` works.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Andste82/chaos-gateway/internal/version"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(stdout, version.String("chaosctl"))
		return 0
	}
	fmt.Fprintln(stderr, "usage: chaosctl version\nthe other commands arrive with milestone M18")
	return 2
}
