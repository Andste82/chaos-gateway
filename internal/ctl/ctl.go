package ctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/version"
)

// The exit codes of chaosctl (package comment).
const (
	ExitOK     = 0
	ExitFailed = 1
	ExitError  = 2
	// ExitInternal is `chaosctl with` failing itself, 126 and 127 its command that cannot be run or found.
	ExitInternal    = 125
	ExitNotRunnable = 126
	ExitNotFound    = 127
)

// Env is what chaosctl needs from the process around it, so that a test can run it in-process.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Getenv reads the environment (CHAOSGW_URL, CHAOSGW_TOKEN, …); nil: the process's.
	Getenv func(string) string
	// Environ is the environment a command of `chaosctl with` gets; nil: the process's.
	Environ []string
	// Signals delivers the interrupt, terminate and hang-up signals; nil: those of the process, subscribed to by
	// the commands that wait, before their first request.
	Signals <-chan os.Signal
	// Poll is the interval at which a waiting command asks the gateway; 0: one second.
	Poll time.Duration
	// PollTimeout bounds one request of a wait (a poll, a renewal); 0: 10 seconds.
	PollTimeout time.Duration
	// Clock times the polls, timeouts and renewals; nil: the real one.
	Clock clock.Clock
}

// exitError ends the command with a code and, when err is not nil, a message.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}
func (e *exitError) Unwrap() error { return e.err }

// failf is an error that ends the command with code.
func failf(code int, format string, a ...any) error {
	return &exitError{code: code, err: fmt.Errorf(format, a...)}
}

// exitWith ends the command with code and no message of its own (the command has said what it had to say).
func exitWith(code int) error { return &exitError{code: code} }

// app is one invocation of chaosctl.
type app struct {
	env  Env
	conn connection
	// asJSON prints the API's JSON where a command prints a result
	asJSON bool
	cl     *client
	// flags are the global flags, to tell which of them the command line set
	flags *pflag.FlagSet
}

// Run is chaosctl: args are the arguments after the program name. It returns the exit code.
func Run(args []string, env Env) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if env.Poll <= 0 {
		env.Poll = time.Second
	}
	if env.Clock == nil {
		env.Clock = &clock.Real{}
	}
	a := &app{env: env}
	root := a.root()
	root.SetArgs(args)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.SetIn(env.Stdin)
	defer func() {
		if a.cl != nil {
			a.cl.close()
		}
	}()
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintf(env.Stderr, "chaosctl: %v\n", ee.err)
		}
		return ee.code
	}
	// what cobra itself refuses: an unknown command or flag, a wrong number of arguments
	fmt.Fprintf(env.Stderr, "chaosctl: %v\nrun `chaosctl help` for the usage\n", err)
	return ExitError
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "chaosctl",
		Short: "Command line client of Chaos Gateway for CI pipelines",
		Long: `chaosctl drives a Chaos Gateway through its REST API: it applies profiles and faults with a TTL, runs
scenarios from files and waits for their reports, waits for devices, and runs a command under a fault that is
always taken away again.

The gateway and its token come from the flags or the environment:

  CHAOSGW_URL          address of the gateway, https://host:8443 (default https://127.0.0.1:8443)
  CHAOSGW_TOKEN        an API token (scope overlays is enough for everything but 'wg export')
  CHAOSGW_TOKEN_FILE   a file that holds the token
  CHAOSGW_CA_CERT      a PEM file with the CA or the certificate of the gateway
  CHAOSGW_FINGERPRINT  the SHA-256 fingerprint of the gateway's certificate (trusts a self-signed one)
  CHAOSGW_INSECURE     1: do not verify the certificate

A flag wins over the environment. The three ways to trust a gateway (--insecure, --fingerprint, --ca-cert) are
one setting: when the command line names any of them, CHAOSGW_INSECURE, CHAOSGW_FINGERPRINT and CHAOSGW_CA_CERT
are not read, so a variable of the shell cannot switch off what a flag pins. --insecure cannot be combined with
a fingerprint or a CA file (exit 2), and it prints a warning.

Exit codes: 0 it holds / the run passed, 1 it does not hold in time / a check failed, 2 the run ended as error
or aborted, or the command itself failed. 'chaosctl with' returns the code of its command, and 125 when
chaosctl failed, 126 when the command cannot be run, 127 when it is not found.`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		Version:           version.String("chaosctl"),
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	f := root.PersistentFlags()
	a.flags = f
	f.StringVar(&a.conn.url, "url", "", "address of the gateway (default $CHAOSGW_URL or "+defaultURL+")")
	f.StringVar(&a.conn.token, "token", "", "API token (default $CHAOSGW_TOKEN; --token-file keeps it out of the process list)")
	f.StringVar(&a.conn.tokenFile, "token-file", "", "file that holds the API token (default $CHAOSGW_TOKEN_FILE)")
	f.StringVar(&a.conn.caCert, "ca-cert", "", "PEM file with the CA or the certificate of the gateway (default $CHAOSGW_CA_CERT)")
	f.StringVar(&a.conn.fingerprint, "fingerprint", "", "SHA-256 fingerprint of the gateway's certificate (default $CHAOSGW_FINGERPRINT)")
	f.BoolVar(&a.conn.insecure, "insecure", false, "do not verify the certificate of the gateway ($CHAOSGW_INSECURE)")
	f.DurationVar(&a.conn.timeout, "request-timeout", 2*time.Minute, "how long one request to the gateway may take")
	f.BoolVar(&a.asJSON, "json", false, "print the gateway's JSON where a command prints a result")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &exitError{code: ExitError, err: err} })
	root.AddCommand(a.versionCmd(), a.profileCmd(), a.faultCmd(), a.dnsCmd(), a.tlsCmd(), a.dhcpCmd(), a.overlayCmd(), a.resetCmd(), a.runCmd(), a.abortCmd(), a.waitCmd(), a.withCmd(), a.explainCmd(), a.wgCmd())
	return root
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Print the version", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprintln(a.env.Stdout, version.String("chaosctl"))
			return nil
		},
	}
}

// client connects on first use: flags win over the environment.
func (a *app) client() (*client, error) {
	if a.cl != nil {
		return a.cl, nil
	}
	c := a.conn
	pick := func(v *string, name string) {
		if *v == "" {
			*v = a.env.Getenv(name)
		}
	}
	pick(&c.url, "CHAOSGW_URL")
	pick(&c.token, "CHAOSGW_TOKEN")
	pick(&c.tokenFile, "CHAOSGW_TOKEN_FILE")
	// How the gateway is trusted is one setting with three spellings, so the source is decided for all of them
	// together: a flag on the command line replaces the trust variables of the environment, which must not
	// override a pinned fingerprint or a CA file, or be overridden half way (CHAOSGW_INSECURE=1 next to
	// --fingerprint would send the token to a peer that was never checked).
	source := "environment variables"
	if a.flags != nil && (a.flags.Changed("insecure") || a.flags.Changed("fingerprint") || a.flags.Changed("ca-cert")) {
		source = "flags"
	} else {
		pick(&c.caCert, "CHAOSGW_CA_CERT")
		pick(&c.fingerprint, "CHAOSGW_FINGERPRINT")
		switch strings.ToLower(a.env.Getenv("CHAOSGW_INSECURE")) {
		case "1", "true", "yes":
			c.insecure = true
		}
	}
	if c.insecure && (c.fingerprint != "" || c.caCert != "") {
		return nil, &exitError{code: ExitError, err: fmt.Errorf("insecure (no verification of the certificate) cannot be combined with a fingerprint or a CA file; the %s say both: "+
			"drop one (--insecure, or --fingerprint / --ca-cert)", source)}
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, &exitError{code: ExitError, err: err}
	}
	if c.insecure && strings.HasPrefix(cl.base, "https://") {
		fmt.Fprintln(a.env.Stderr, "chaosctl: warning: the certificate of the gateway is not verified (insecure); the token goes to whoever answers")
	}
	a.cl = cl
	return cl, nil
}

// signals returns the channel of the interrupt, terminate and hang-up signals and the function that stops listening.
// A command that waits or cleans up calls it once, before its first request: a signal that arrives earlier would
// kill chaosctl without the cleanup (SIGHUP is what a lost ssh session or a CI agent that gives up sends).
func (a *app) signals() (<-chan os.Signal, func()) {
	if a.env.Signals != nil {
		return a.env.Signals, func() {}
	}
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

// apiFail turns an error of a request into the end of the command.
func apiFail(err error) error {
	if err == nil {
		return nil
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return err
	}
	return &exitError{code: ExitError, err: err}
}

// cleanupContext is the context of the calls that undo what a command did: they must run when the command was
// interrupted too.
func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func (a *app) clk() clock.Clock { return a.env.Clock }

// since is the seconds that have passed since a reading of the monotonic clock.
func (a *app) since(started time.Duration) float64 { return (a.clk().Monotonic() - started).Seconds() }
