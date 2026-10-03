// Command chaosgw is the Chaos Gateway core binary (plan §3.1). Its subcommands run in separate
// containers with only the privileges they need (§3.8): the API server, the privileged executor,
// the DNS proxy and the TLS responder. They arrive with the milestones named below.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Andste82/chaos-gateway/internal/version"
)

// subcommands maps each service to the milestone that implements it; main is nil until then.
var subcommands = []struct {
	name, milestone, summary string
	main                     func(args []string, stdout, stderr io.Writer) int
}{
	{"api", "M5", "REST API, web UI and scheduler (unprivileged)", runAPI},
	{"admin", "M5", "administration from the host: reset-password", func(args []string, stdout, stderr io.Writer) int { return runAdmin(args, stdout, stderr, os.Stdin) }},
	{"apply", "M4", "apply a configuration file without the API (bootstrap)", runApply},
	{"wg", "M4b", "export WireGuard client and link configurations", runWG},
	{"exec", "M3", "privileged executor for nftables, tc, routes and sysctls", runExec},
	{"dns", "M6b", "DNS proxy in the service namespace", nil},
	{"tls", "M21", "TLS responder in the service namespace", nil},
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, version.String("chaosgw"))
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	}
	for _, s := range subcommands {
		if s.name == args[0] {
			if s.main != nil {
				return s.main(args[1:], stdout, stderr)
			}
			fmt.Fprintf(stderr, "chaosgw %s is not implemented in this build (milestone %s)\n", s.name, s.milestone)
			return 2
		}
	}
	fmt.Fprintf(stderr, "chaosgw: unknown command %q\n\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: chaosgw <command>")
	fmt.Fprintln(w, "\ncommands:")
	fmt.Fprintln(w, "  version  print the version")
	for _, s := range subcommands {
		fmt.Fprintf(w, "  %-8s %s (%s)\n", s.name, s.summary, s.milestone)
	}
}
