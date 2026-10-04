package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/clock"
)

// minPassword is the shortest admin password (the API's rule).
const minPassword = 12

// runAdmin is `chaosgw admin ...`: administration from the host. Today only `reset-password`
// (plan §2.16): it sets the admin password without the old one and ends every session, including
// those of the running API, which notices the change.
func runAdmin(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprintln(stderr, "usage: chaosgw admin reset-password --secrets-dir <dir> [--data-dir <dir>] (--password-stdin | --password-file <file> | interactively on a terminal)")
		return 2
	}
	fs := flag.NewFlagSet("chaosgw admin reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	secretsDir := fs.String("secrets-dir", "/var/lib/chaosgw/secrets", "directory of the secrets (the same the API uses)")
	dataDir := fs.String("data-dir", "/var/lib/chaosgw/api", "directory of the audit log (the same the API uses)")
	fromStdin := fs.Bool("password-stdin", false, "read the new password from standard input (one line)")
	file := fs.String("password-file", "", "read the new password from this file (first line)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || (*fromStdin && *file != "") {
		fmt.Fprintln(stderr, "chaosgw admin reset-password: give at most one of --password-stdin and --password-file")
		return 2
	}
	pw, code := readNewPassword(*fromStdin, *file, stdin, stderr)
	if code != 0 {
		return code
	}
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
	if err := recordPasswordReset(*dataDir); err != nil {
		fmt.Fprintf(stderr, "chaosgw admin reset-password: the password was reset, but the audit log could not record it: %v\n", err)
	}
	fmt.Fprintln(stdout, "The admin password was reset; all sessions have ended.")
	return 0
}

// readNewPassword returns the new password from --password-stdin, --password-file, or (when
// neither is given and stdin is a terminal) an interactive, confirmed prompt. code is 0 on
// success, 2 when none of those applies (no flag, no terminal), 1 on any other failure.
func readNewPassword(fromStdin bool, file string, stdin io.Reader, stderr io.Writer) (string, int) {
	if !fromStdin && file == "" {
		f, ok := stdin.(*os.File)
		if !ok || !term.IsTerminal(int(f.Fd())) {
			fmt.Fprintln(stderr, "chaosgw admin reset-password: give --password-stdin or --password-file, or run this on a terminal")
			return "", 2
		}
		fmt.Fprint(stderr, "New admin password: ")
		p1, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw admin reset-password: %v\n", err)
			return "", 1
		}
		fmt.Fprint(stderr, "Confirm: ")
		p2, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw admin reset-password: %v\n", err)
			return "", 1
		}
		if string(p1) != string(p2) {
			fmt.Fprintln(stderr, "chaosgw admin reset-password: the passwords do not match")
			return "", 1
		}
		return string(p1), 0
	}
	r := stdin
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw admin reset-password: %v\n", err)
			return "", 1
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(stderr, "chaosgw admin reset-password: no password given")
		return "", 1
	}
	return strings.TrimRight(line, "\r\n"), 0
}

// recordPasswordReset audits the reset as the CLI's own doing (plan §2.16: every change is
// recorded), separate from whatever actor the running API process would otherwise attribute it to.
func recordPasswordReset(dataDir string) error {
	lg, err := audit.Open(dataDir, &clock.Real{})
	if err != nil {
		return err
	}
	defer func() { _ = lg.Close() }()
	_, err = lg.Append(audit.Entry{Actor: audit.Actor{Type: "system", ID: "cli"}, Via: "cli", Action: "auth.password_reset"})
	return err
}
