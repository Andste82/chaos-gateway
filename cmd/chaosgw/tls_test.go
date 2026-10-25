package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

func TestTheTLSHealthCheckNeedsAResponderThatAccepts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	var out, errb bytes.Buffer
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if code := run([]string{"tls", "--health", "--listen", addr}, &out, &errb); code != 0 || !strings.Contains(out.String(), "ok") {
		t.Errorf("exit %d: %s %s", code, out.String(), errb.String())
	}
	_ = l.Close()
	out.Reset()
	if code := run([]string{"tls", "--health", "--listen", addr}, &out, &errb); code == 0 {
		t.Errorf("a health check without a responder succeeded: %s", out.String())
	}
}

func TestTheTLSResponderRefusesABadServiceAddress(t *testing.T) {
	var errb bytes.Buffer
	if code := run([]string{"tls", "--service-addr", "nope"}, &bytes.Buffer{}, &errb); code != 2 || !strings.Contains(errb.String(), "--service-addr") {
		t.Errorf("exit %d: %s", code, errb.String())
	}
}

// The responder takes the address of the interception proxy and of its own control interface (M22); both may be left empty, and the defaults
// are the ones deploy/compose.tlsproxy.yaml starts the proxy with.
func TestTheTLSResponderTakesTheAddressesOfTheProxyAndItsControl(t *testing.T) {
	var errb bytes.Buffer
	// the flags are read before the service address is checked, so a bad address after them proves that they were accepted
	if code := run([]string{"tls", "--proxy", "", "--control", "", "--service-addr", "nope"}, &bytes.Buffer{}, &errb); code != 2 || !strings.Contains(errb.String(), "--service-addr") {
		t.Errorf("exit %d: %s", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"tls", "--proxy", "127.0.0.1:9444", "--control", "127.0.0.1:9445", "--service-addr", "nope"}, &bytes.Buffer{}, &errb); code != 2 || strings.Contains(errb.String(), "flag provided but not defined") {
		t.Errorf("exit %d: %s", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"tls", "--no-such-flag"}, &bytes.Buffer{}, &errb); code != 2 || !strings.Contains(errb.String(), "not defined") {
		t.Errorf("exit %d: %s", code, errb.String())
	}
}
