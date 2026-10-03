package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/auth"
)

// minPassword is the shortest admin password (the API's rule).
const minPassword = 12

// runAdmin is `chaosgw admin ...`: administration from the host. Today only `reset-password`
// (plan §2.16): it sets the admin password without the old one and ends every session, including
// those of the running API, which notices the change.
func runAdmin(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprintln(stderr, "usage: chaosgw admin reset-password --secrets-dir <dir> (--password-stdin | --password-file <file>)")
		return 2
	}
	fs := flag.NewFlagSet("chaosgw admin reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	secretsDir := fs.String("secrets-dir", "/var/lib/chaosgw/secrets", "directory of the secrets (the same the API uses)")
	fromStdin := fs.Bool("password-stdin", false, "read the new password from standard input (one line)")
	file := fs.String("password-file", "", "read the new password from this file (first line)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *fromStdin == (*file != "") {
		fmt.Fprintln(stderr, "chaosgw admin reset-password: give exactly one of --password-stdin and --password-file")
		return 2
	}
	var r io.Reader = stdin
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw admin reset-password: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(stderr, "chaosgw admin reset-password: no password given")
		return 1
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < minPassword {
		fmt.Fprintf(stderr, "chaosgw admin reset-password: the password needs at least %d characters\n", minPassword)
		return 1
	}
	dir := filepath.Join(*secretsDir, "auth")
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); err != nil {
		fmt.Fprintf(stderr, "chaosgw admin reset-password: there is no admin account in %s: finish the setup first\n", *secretsDir)
		return 1
	}
	st, err := auth.Open(dir)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw admin reset-password: %v\n", err)
		return 1
	}
	if !st.SetupCompleted() {
		fmt.Fprintln(stderr, "chaosgw admin reset-password: the setup is not finished: there is no admin account yet")
		return 1
	}
	if err := st.ResetPassword(pw); err != nil {
		fmt.Fprintf(stderr, "chaosgw admin reset-password: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "The admin password was reset; all sessions have ended.")
	return 0
}
