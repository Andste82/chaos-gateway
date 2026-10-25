package tlsresponder

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the hand-over of a connection to the interception proxy (plan §2.8, M22). The kernel redirects the
// connections of the case `intercept` like those of every other case, so they arrive here with their original
// destination, and the responder decides them like the others (the API says which case decides). For the case
// `intercept` the responder does not answer the connection: it opens a connection to the proxy (mitmproxy in the same
// namespace, in `regular` mode), asks it with `CONNECT <original destination>` to take the connection to that
// destination, and relays the bytes of the device and the proxy to each other. What the proxy needs to know about
// the connection travels in the headers of that request: the client, the overlay and the device. The proxy never
// sees the device's address otherwise (the connection it accepts comes from the responder), and the responder
// does not see inside the TLS the proxy terminates, so a rule that paces the response asks the responder to do it
// (the control interface below), where the bytes pass.

const (
	// HeaderConnection is the header of the CONNECT request with the id of the connection: the proxy names it when it asks
	// for the connection to be paced.
	HeaderConnection = "X-Chaosgw-Connection"
	// HeaderClient is the device's address, HeaderOverlay the overlay of the case that decides the connection, HeaderDevice
	// the device (when the gateway knows it) and HeaderSNI the server name of the ClientHello (when it has one).
	HeaderClient  = "X-Chaosgw-Client"
	HeaderOverlay = "X-Chaosgw-Overlay"
	HeaderDevice  = "X-Chaosgw-Device"
	HeaderSNI     = "X-Chaosgw-Sni"
	// DefaultProxyAddr is where the interception proxy listens inside the service namespace, DefaultControlAddr where the
	// responder takes the proxy's requests to pace a connection.
	DefaultProxyAddr   = "127.0.0.1:9444"
	DefaultControlAddr = "127.0.0.1:9445"
	// maxProxyHead bounds the answer of the proxy to the CONNECT request.
	maxProxyHead = 16 << 10
)

// intercept hands a connection that the case `intercept` decides to the proxy. hello is what the device sent so far (the
// ClientHello, or the first bytes of a connection that is no TLS) and herr why the read of it ended: a connection
// that says nothing at all has nothing to hand over.
func (s *Server) intercept(ctx context.Context, c net.Conn, hello []byte, herr error, info ConnInfo, dec Decision,
	rep *model.TlsHandshakeReport, finish func(model.TlsHandshakeReportOutcome, string)) {
	if herr != nil && !errors.Is(herr, errNotTLS) {
		finish(helloFailure(herr))
		return
	}
	if !dec.Passthrough {
		// the proxy's own connection leaves through the gateway's service path, which the access matrix does not see: a
		// connection the gateway would not have forwarded must not get through it
		s.denied.Add(1)
		s.opt.Log.Debug("a connection of an interception case that the gateway would not forward is reset", "client", info.Client, "dst", info.Dst)
		reset(c)
		return
	}
	id := newConnID()
	up, br, err := s.connectProxy(ctx, id, info, dec)
	if err != nil {
		s.proxyFailures.Add(1)
		s.opt.Log.Warn("cannot hand a connection to the interception proxy: it is reset", "client", info.Client, "dst", info.Dst, "error", err)
		finish(model.TlsHandshakeReportOutcomeProxyUnavailable, "")
		reset(c)
		return
	}
	defer func() { _ = up.Close() }()
	if herr != nil {
		hello = append(hello, s.firstBytes(c)...)
	}
	// the proxy asks for the pacing and the abort of the connection by its id, which it has been told, and only after it has
	// the device's first bytes, which are sent below
	p := s.newPacer(id, c, up)
	defer s.dropPacer(id)
	s.intercepted.Add(1)
	finish(model.TlsHandshakeReportOutcomeIntercepted, "")
	if _, err := up.Write(hello); err != nil {
		reset(c)
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		// device -> proxy; the end of the device's stream ends the proxy's request side
		_, _ = io.Copy(up, c)
		if tc, ok := up.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		// proxy -> device, at the rate the proxy asked for; what the proxy already sent after its answer is in br
		_ = s.pace(c, br, p)
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	// both directions end when the connection does: the proxy closes its side when the device has gone, and the device's
	// side ends when the case is removed (hold resets c) or the responder stops
	<-done
	<-done
}

// connectProxy opens the connection to the proxy and asks it to take the connection to the original destination. The
// reader returned holds what the proxy sent after the answer.
func (s *Server) connectProxy(ctx context.Context, id string, info ConnInfo, dec Decision) (net.Conn, *bufio.Reader, error) {
	if s.opt.Proxy == "" {
		return nil, nil, errors.New("no interception proxy is configured")
	}
	dctx, cancel := context.WithTimeout(ctx, s.opt.DialTimeout)
	up, err := s.opt.ProxyDial(dctx, "tcp", s.opt.Proxy)
	cancel()
	if err != nil {
		return nil, nil, fmt.Errorf("the proxy does not answer: %w", err)
	}
	dst := info.Dst.String()
	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", dst, dst)
	fmt.Fprintf(&b, "%s: %s\r\n%s: %s\r\n", HeaderConnection, id, HeaderClient, info.Client)
	if dec.Overlay != "" {
		fmt.Fprintf(&b, "%s: %s\r\n", HeaderOverlay, dec.Overlay)
	}
	if dec.Device != "" {
		fmt.Fprintf(&b, "%s: %s\r\n", HeaderDevice, dec.Device)
	}
	if info.SNI != "" && !strings.ContainsAny(info.SNI, "\r\n") {
		fmt.Fprintf(&b, "%s: %s\r\n", HeaderSNI, info.SNI)
	}
	b.WriteString("\r\n")
	_ = up.SetDeadline(s.opt.Clock.Now().Add(proxyAnswerTimeout))
	if _, err := io.WriteString(up, b.String()); err != nil {
		_ = up.Close()
		return nil, nil, err
	}
	br := bufio.NewReaderSize(up, 32<<10)
	line, err := br.ReadString('\n')
	if err != nil {
		_ = up.Close()
		return nil, nil, fmt.Errorf("the proxy gave no answer: %w", err)
	}
	// "HTTP/1.1 200 Connection established", then the headers up to the empty line
	parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
	head := len(line)
	for {
		h, err := br.ReadString('\n')
		head += len(h)
		if err != nil || head > maxProxyHead {
			_ = up.Close()
			return nil, nil, errors.New("the answer of the proxy is not an HTTP response")
		}
		if strings.TrimSpace(h) == "" {
			break
		}
	}
	if len(parts) < 2 || parts[1] != "200" {
		_ = up.Close()
		return nil, nil, fmt.Errorf("the proxy refused the connection: %s", strings.TrimSpace(line))
	}
	_ = up.SetDeadline(time.Time{})
	return up, br, nil
}

// firstBytes reads what the device has sent beyond what the read of the ClientHello took: for a connection that is no TLS the
// responder knows only the first five bytes, and the proxy decides which protocol it is (HTTP, or a stream it only counts) from
// the first bytes it gets, so they go to it together instead of in pieces. It does not wait for data that is not there.
func (s *Server) firstBytes(c net.Conn) []byte {
	_ = c.SetReadDeadline(s.opt.Clock.Now().Add(firstBytesWait))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	buf := make([]byte, 16<<10)
	n, _ := c.Read(buf)
	return buf[:n]
}

// firstBytesWait is how long firstBytes waits for the rest of the first segment of the device.
const firstBytesWait = 100 * time.Millisecond

// proxyAnswerTimeout is how long the proxy may take to answer the CONNECT request: it connects to the destination first.
const proxyAnswerTimeout = 15 * time.Second

func newConnID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ---- pacing: the control interface of the proxy

// pacer is a connection handed to the proxy, as the control interface knows it: the rate at which the bytes towards
// the device are sent, in bytes per second (0 for no limit), and the two connections.
type pacer struct {
	rate atomic.Int64
	// next is when the next byte may be sent; only the copy loop touches it
	next time.Time
	// device is the connection of the device, proxy the one to the proxy
	device, proxy net.Conn
}

func (s *Server) newPacer(id string, device, proxy net.Conn) *pacer {
	p := &pacer{device: device, proxy: proxy}
	s.pacerMu.Lock()
	s.pacers[id] = p
	s.pacerMu.Unlock()
	return p
}

func (s *Server) dropPacer(id string) {
	s.pacerMu.Lock()
	delete(s.pacers, id)
	s.pacerMu.Unlock()
}

// Throttle sets the rate in bit/s at which the bytes to the device of a handed-over connection are sent from now on
// (0 removes the limit); false for a connection the responder does not hold (it ended).
func (s *Server) Throttle(connection string, bitsPerSecond int64) bool {
	s.pacerMu.Lock()
	p := s.pacers[connection]
	s.pacerMu.Unlock()
	if p == nil {
		return false
	}
	p.rate.Store(max(bitsPerSecond, 0) / 8)
	return true
}

// pace copies the bytes of the proxy to the device and holds the rate of the pacer.
func (s *Server) pace(dst net.Conn, src io.Reader, p *pacer) error {
	buf := make([]byte, 32<<10)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if err := s.send(dst, buf[:n], p); err != nil {
				return err
			}
		}
		if rerr != nil {
			return rerr
		}
	}
}

// send writes the bytes to the device. Without a rate they go at once; with one they go in pieces of what the rate allows in a
// twentieth of a second, each piece when the bytes before it and itself have had the time they take at that rate (so the last
// byte of a response arrives when it would on a link of that speed, and a long response trickles instead of arriving in one
// burst). The rate is looked at for every piece: a change of it holds from the next piece on.
func (s *Server) send(dst net.Conn, data []byte, p *pacer) error {
	for len(data) > 0 {
		rate := p.rate.Load()
		if rate <= 0 {
			p.next = time.Time{}
			_, err := dst.Write(data)
			return err
		}
		piece := data
		if limit := int(max(rate/20, 64)); len(piece) > limit {
			piece = piece[:limit]
		}
		now := s.opt.Clock.Now()
		if p.next.Before(now) {
			p.next = now
		}
		p.next = p.next.Add(time.Duration(int64(len(piece))) * time.Second / time.Duration(rate))
		if wait := p.next.Sub(now); wait > 0 {
			<-s.opt.Clock.After(wait)
		}
		if _, err := dst.Write(piece); err != nil {
			return err
		}
		data = data[len(piece):]
	}
	return nil
}

// Abort resets the device's side of a handed-over connection and closes the proxy's: the connection ends as if the server
// had reset it. False for a connection the responder does not hold.
func (s *Server) Abort(connection string) bool {
	s.pacerMu.Lock()
	p := s.pacers[connection]
	s.pacerMu.Unlock()
	if p == nil {
		return false
	}
	s.aborted.Add(1)
	reset(p.device)
	_ = p.proxy.Close()
	return true
}

type throttleRequest struct {
	Connection string `json:"connection"`
	RateBps    int64  `json:"rate_bps"`
}

// ControlHandler is the interface the proxy uses to ask for a connection to be paced, `POST /throttle` with
// `{"connection": id, "rate_bps": n}` (`rate_bps` 0 removes the limit), or reset, `POST /abort` with `{"connection": id}`:
// a close of the proxy's connection would reach the device as an orderly end, an abort rule wants a reset. It listens inside the service namespace on the
// loopback only (ListenControl): the connections of the devices do not reach it, and the id of a connection is known to
// the proxy that was told it.
func (s *Server) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /throttle", func(w http.ResponseWriter, r *http.Request) {
		var req throttleRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Connection == "" || req.RateBps < 0 {
			http.Error(w, "a connection and a rate are needed", http.StatusBadRequest)
			return
		}
		if !s.Throttle(req.Connection, req.RateBps) {
			http.Error(w, "no such connection", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /abort", func(w http.ResponseWriter, r *http.Request) {
		var req throttleRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Connection == "" {
			http.Error(w, "a connection is needed", http.StatusBadRequest)
			return
		}
		if !s.Abort(req.Connection) {
			http.Error(w, "no such connection", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// ServeControl serves the control interface on the address until ctx ends.
func (s *Server) ServeControl(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.ServeControlOn(ctx, ln)
}

// ServeControlOn serves the control interface on the listener until ctx ends.
func (s *Server) ServeControlOn(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.ControlHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Intercepted is the number of connections handed to the proxy, ProxyFailures the number that could not be (the proxy was
// not there, or refused them) and Aborted the number the proxy asked to be reset.
func (s *Server) Intercepted() int64   { return s.intercepted.Load() }
func (s *Server) ProxyFailures() int64 { return s.proxyFailures.Load() }
func (s *Server) Aborted() int64       { return s.aborted.Load() }
