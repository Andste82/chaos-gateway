package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/secrets"
)

type uidList []uint32

func (u *uidList) String() string { return fmt.Sprint(*u) }
func (u *uidList) Set(s string) error {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid uid %q", s)
	}
	*u = append(*u, uint32(n))
	return nil
}

// runExec is `chaosgw exec`: the privileged executor (plan §3.1, §3.8). With -health it instead
// connects to a running executor and checks the handshake, for the container's health check.
func runExec(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chaosgw exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", "/run/chaosgw/exec.sock", "path of the Unix socket")
	state := fs.String("state", "/var/lib/chaosgw/exec/state.json", "file that keeps the assigned interfaces across restarts")
	owner := fs.Int("socket-owner", -1, "uid that owns the socket file (-1: unchanged)")
	secretsDir := fs.String("secrets-dir", "", "directory with the secrets (WireGuard keys); the executor only reads it")
	birdDir := fs.String("bird-dir", "", "directory shared with the BIRD container (<instance>.conf and .ctl); without it dynamic routing is refused")
	health := fs.Bool("health", false, "check a running executor (handshake and generation) and exit")
	var allow uidList
	fs.Var(&allow, "allow-uid", "uid that may connect besides root (repeatable), e.g. the API container's user")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "chaosgw exec: unexpected arguments")
		return 2
	}
	if *health {
		return execHealth(*socket, stdout, stderr)
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "chaosgw exec: must run as root: it is the privileged component")
		return 1
	}
	// nothing the executor starts may gain more privileges than it has; files are private by default
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		fmt.Fprintf(stderr, "chaosgw exec: no_new_privs: %v\n", err)
		return 1
	}
	syscall.Umask(0o077)

	opts := []executor.Option{executor.WithLogger(log), executor.WithStateFile(*state)}
	if *birdDir != "" {
		opts = append(opts, executor.WithBirdDir(*birdDir))
	}
	if *secretsDir != "" {
		sec, err := secrets.OpenReadOnly(*secretsDir)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw exec: %v\n", err)
			return 1
		}
		opts = append(opts, executor.WithKeys(func(id string) (string, string, error) {
			k, err := sec.WireGuard(id)
			return k.PrivateKey, k.PresharedKey, err
		}))
	}
	ex, err := executor.New(executor.NewExecRunner(), opts...)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw exec: %v\n", err)
		return 1
	}
	defer ex.Close()
	l, err := executor.Listen(*socket, 0o660, *owner)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw exec: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	srv := &executor.Server{Exec: ex, Auth: executor.AllowUIDs(allow...), Log: log}
	log.Info("executor ready", "socket", *socket, "protocol", executor.ProtocolVersion, "allowed_uids", []uint32(allow))
	if err := srv.Serve(ctx, l); err != nil {
		fmt.Fprintf(stderr, "chaosgw exec: %v\n", err)
		return 1
	}
	log.Info("executor stopped")
	return 0
}

func execHealth(socket string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := executor.Dial(ctx, socket, executor.DialOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "executor unhealthy: %v\n", err)
		return 1
	}
	defer func() { _ = c.Close() }()
	gen, err := c.Generation(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "executor unhealthy: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok protocol=%d generation=%d\n", executor.ProtocolVersion, gen)
	return 0
}
