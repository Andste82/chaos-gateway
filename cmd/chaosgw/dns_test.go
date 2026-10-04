package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/dnsproxy"
)

func TestTheHolderWritesItsPIDAndStopsOnSIGTERM(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pid")
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() { done <- run([]string{"svcns", "--pid-file", file}, &bytes.Buffer{}, &stderr) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(file)
		if err == nil {
			fields := strings.Fields(string(raw))
			pid, perr := strconv.Atoi(fields[0])
			if len(fields) != 2 || perr != nil || pid != os.Getpid() {
				t.Fatalf("%q", raw)
			}
			if _, ierr := strconv.ParseUint(fields[1], 10, 64); ierr != nil {
				t.Fatalf("no namespace inode: %q", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid file: %v %s", err, stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d: %s", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the holder does not stop")
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the pid file stays")
	}
	// an unwritable place is an error, not a silent holder
	if code := run([]string{"svcns", "--pid-file", "/nonexistent-dir/pid"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Errorf("exit %d", code)
	}
}

func TestTheDNSHealthCheckNeedsAProxyThatAnswers(t *testing.T) {
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.LocalAddr().String()
	_ = l.Close()
	var out, errb bytes.Buffer
	if code := run([]string{"dns", "--health", "--listen", addr}, &out, &errb); code == 0 {
		t.Fatalf("a health check without a proxy succeeded: %s", out.String())
	}
	srv := dnsproxy.New(dnsproxy.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, addr) }()
	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("no proxy")
	}
	out.Reset()
	// a proxy without a configuration answers SERVFAIL: it is up
	if code := run([]string{"dns", "--health", "--listen", addr}, &out, &errb); code != 0 || !strings.Contains(out.String(), "ok") {
		t.Errorf("exit %d: %s %s", code, out.String(), errb.String())
	}
	cancel()
	<-done
}
