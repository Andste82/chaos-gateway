package tlsresponder

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// captureHello returns the bytes a real TLS client sends first.
func captureHello(t *testing.T, cfg *tls.Config) []byte {
	t.Helper()
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close() }()
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 16<<10)
		n, _ := c2.Read(buf)
		got <- buf[:n]
		_ = c2.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tls.Client(c1, cfg).HandshakeContext(ctx)
	return <-got
}

func parse(t *testing.T, b []byte) (raw []byte, sni string, err error) {
	t.Helper()
	c1, c2 := net.Pipe()
	go func() { _, _ = c2.Write(b); _ = c2.Close() }()
	defer func() { _ = c1.Close() }()
	return readClientHello(c1, time.Now().Add(2*time.Second))
}

func TestTheServerNameIsReadFromARealClientHello(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  *tls.Config
		want string
	}{
		{"1.3", &tls.Config{ServerName: "Broker.Example.com"}, "broker.example.com"},
		{"1.2", &tls.Config{ServerName: "mqtt.example.org", MaxVersion: tls.VersionTLS12}, "mqtt.example.org"},
		{"a trailing dot", &tls.Config{ServerName: "broker.example.com."}, "broker.example.com"},
		{"no name: an address has none", &tls.Config{ServerName: "203.0.113.10", InsecureSkipVerify: true}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			hello := captureHello(t, c.cfg)
			raw, sni, err := parse(t, hello)
			if err != nil || sni != c.want || !bytes.Equal(raw, hello) {
				t.Fatalf("sni %q, err %v, raw %d of %d bytes", sni, err, len(raw), len(hello))
			}
		})
	}
}

// A ClientHello may be split into several records; the responder puts them together and keeps every byte for the
// TLS stack and for a connection it passes on.
func TestAClientHelloSplitOverRecordsIsReadInFull(t *testing.T) {
	hello := captureHello(t, &tls.Config{ServerName: "broker.example.com"})
	body := hello[recordHeader:]
	var split []byte
	for len(body) > 0 {
		n := min(len(body), 40)
		var h [recordHeader]byte
		h[0], h[1], h[2] = recordHandshake, 3, 3
		binary.BigEndian.PutUint16(h[3:], uint16(n))
		split = append(append(split, h[:]...), body[:n]...)
		body = body[n:]
	}
	raw, sni, err := parse(t, split)
	if err != nil || sni != "broker.example.com" || !bytes.Equal(raw, split) {
		t.Fatalf("sni %q err %v raw %d of %d", sni, err, len(raw), len(split))
	}
}

func TestWhatIsNotAClientHelloIsRefusedWithWhatWasRead(t *testing.T) {
	hello := captureHello(t, &tls.Config{ServerName: "x.example.com"})
	for name, in := range map[string][]byte{
		"plain text":                 []byte("GET / HTTP/1.1\r\n\r\n"),
		"an alert":                   {21, 3, 3, 0, 2, 2, 40},
		"a zero-length frame":        {22, 3, 3, 0, 0},
		"a message that is no hello": append([]byte{22, 3, 3, 0, 4}, 2, 0, 0, 0),
		"an oversize message":        append([]byte{22, 3, 3, 0, 4}, 1, 0xff, 0xff, 0xff),
	} {
		raw, sni, err := parse(t, in)
		if !errors.Is(err, errNotTLS) || sni != "" || len(raw) == 0 {
			t.Errorf("%s: %q %v %v", name, raw, sni, err)
		}
	}
	// a hello that is cut short: what was read comes back with the error
	raw, _, err := parse(t, hello[:len(hello)/2])
	if !errors.Is(err, io.ErrUnexpectedEOF) || !bytes.Equal(raw, hello[:len(hello)/2]) {
		t.Errorf("%d bytes, %v", len(raw), err)
	}
	// and a client that says nothing runs into the deadline
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()
	_, _, err = readClientHello(c1, time.Now().Add(50*time.Millisecond))
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("%v", err)
	}
}

// A message that is not well formed gives no name and no panic.
func TestAMalformedHelloBodyGivesNoNameAndNoPanic(t *testing.T) {
	hello := captureHello(t, &tls.Config{ServerName: "broker.example.com"})
	body := hello[recordHeader+4:] // the body of the ClientHello
	for n := 0; n <= len(body); n++ {
		_ = serverName(body[:n])
	}
	if serverName(body) != "broker.example.com" {
		t.Error("the whole body gives no name")
	}
	for _, in := range [][]byte{nil, {0}, make([]byte, 34), make([]byte, 200)} {
		if got := serverName(in); got != "" {
			t.Errorf("%v: %q", in, got)
		}
	}
}
