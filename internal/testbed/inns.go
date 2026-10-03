package testbed

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// InNamedNS runs fn on a thread inside the named network namespace (one under /run/netns) and
// returns what fn returns. Sockets that fn creates stay in the namespace when it returns. Entering a
// namespace changes the calling thread: fn runs on a goroutine of its own, locked to its thread,
// and when the thread cannot be restored it stays locked so it ends with the goroutine instead of
// going back into the pool in the wrong namespace.
func InNamedNS(name string, fn func() error) error {
	ch := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		orig, err := os.Open("/proc/thread-self/ns/net")
		if err != nil {
			ch <- err
			runtime.UnlockOSThread()
			return
		}
		defer func() { _ = orig.Close() }()
		var target *os.File
		for _, dir := range []string{"/run/netns/", "/var/run/netns/"} {
			if target, err = os.Open(dir + name); err == nil {
				break
			}
		}
		if target == nil {
			ch <- fmt.Errorf("network namespace %q not found", name)
			runtime.UnlockOSThread()
			return
		}
		err = unix.Setns(int(target.Fd()), unix.CLONE_NEWNET)
		_ = target.Close()
		if err != nil {
			ch <- fmt.Errorf("enter namespace %s: %w", name, err)
			runtime.UnlockOSThread()
			return
		}
		ferr := fn()
		if rerr := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); rerr != nil {
			ch <- fmt.Errorf("leave namespace %s: %w (fn: %v)", name, rerr, ferr)
			return // the thread stays locked
		}
		runtime.UnlockOSThread()
		ch <- ferr
	}()
	return <-ch
}

// Do runs fn inside the namespace (see InNamedNS).
func (n *Namespace) Do(fn func() error) error { return InNamedNS(n.Name, fn) }

// ListenIn listens on addr inside the named namespace; the socket works from any thread afterwards.
func ListenIn(ns, network, addr string) (net.Listener, error) {
	var l net.Listener
	err := InNamedNS(ns, func() (err error) { l, err = net.Listen(network, addr); return })
	return l, err
}

// ListenPacketIn is ListenIn for datagram sockets.
func ListenPacketIn(ns, network, addr string) (net.PacketConn, error) {
	var pc net.PacketConn
	err := InNamedNS(ns, func() (err error) { pc, err = net.ListenPacket(network, addr); return })
	return pc, err
}

// DialerIn returns a dial function whose connections start inside the namespace. The socket is
// created there, so the connection stays in it. Only the connect call runs inside; a name is not
// resolved.
func DialerIn(ns string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var c net.Conn
		err := InNamedNS(ns, func() (err error) {
			var d net.Dialer
			c, err = d.DialContext(ctx, network, addr)
			return
		})
		return c, err
	}
}
