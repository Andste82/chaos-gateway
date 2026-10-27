package diag

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"golang.org/x/net/icmp"
	"golang.org/x/sys/unix"
)

// Net is the network stack a diagnostic runs on: the gateway's own. Addresses are IP literals, resolving a name is the
// diagnostic's business.
type Net interface {
	// DialContext connects a "tcp" or "udp" socket to addr (IPv4 and port).
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	// ListenICMP opens the socket of the echoes. datagram is true for the unprivileged kind, where the kernel
	// keeps the identifier of the echoes and delivers only the replies to this socket.
	ListenICMP() (conn net.PacketConn, datagram bool, err error)
}

// HostNet is the network stack of the process, or of the network namespace Namespace (one under /run/netns; the
// gateway's namespace in the testbed). The sockets are created inside the namespace and stay in it.
type HostNet struct {
	Namespace string
}

// DialContext implements Net.
func (h HostNet) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var c net.Conn
	err := inNamespace(h.Namespace, func() (err error) {
		var d net.Dialer
		c, err = d.DialContext(ctx, network, addr)
		return
	})
	return c, err
}

// ListenICMP implements Net: the datagram socket needs `net.ipv4.ping_group_range` to hold the process's group (the
// host setup writes it), the raw socket the capability NET_RAW.
func (h HostNet) ListenICMP() (net.PacketConn, bool, error) {
	var pc net.PacketConn
	var datagram bool
	err := inNamespace(h.Namespace, func() error {
		c, derr := icmp.ListenPacket("udp4", "0.0.0.0")
		if derr == nil {
			pc, datagram = c, true
			return nil
		}
		c, rerr := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if rerr != nil {
			return fmt.Errorf("cannot open an ICMP socket (%w); the group of the process needs to be in net.ipv4.ping_group_range, or the process needs CAP_NET_RAW", derr)
		}
		pc = c
		return nil
	})
	return pc, datagram, err
}

// InNamespace runs f with the calling thread in the named network namespace (empty: its own), which the sockets f makes
// keep; the probes' DHCP client and echo measurement (M25) make their sockets this way.
func InNamespace(name string, f func() error) error { return inNamespace(name, f) }

// inNamespace runs f with the calling thread in the named network namespace (/run/netns/<name>), which the sockets f
// makes keep. The thread goes back to its own namespace afterwards; a thread that cannot is not given back to the Go
// runtime.
func inNamespace(name string, f func() error) error {
	if name == "" {
		return f()
	}
	runtime.LockOSThread()
	own, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		runtime.UnlockOSThread()
		return err
	}
	defer func() { _ = own.Close() }()
	target, err := os.Open("/run/netns/" + name)
	if err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("the network namespace %s: %w", name, err)
	}
	defer func() { _ = target.Close() }()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("cannot enter the network namespace %s: %w", name, err)
	}
	ferr := f()
	if err := unix.Setns(int(own.Fd()), unix.CLONE_NEWNET); err != nil {
		// the thread stays locked: it ends with this goroutine instead of running other work in the wrong namespace
		return errors.Join(ferr, fmt.Errorf("cannot leave the network namespace %s: %w", name, err))
	}
	runtime.UnlockOSThread()
	return ferr
}
