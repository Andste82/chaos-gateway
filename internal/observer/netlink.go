// Package observer turns kernel events into triggers: Watch listens on a netlink socket for
// changes of links, IPv4 addresses, routes and rules and tells its consumer, debounced, that the
// host changed (plan §2.2, §3.4: the gateway follows the uplink through netlink events, not by
// polling). The consumer reads the state and decides what it means.
package observer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

const (
	hostGroups  = unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV4_ROUTE | unix.RTMGRP_IPV4_RULE
	neighGroups = unix.RTMGRP_NEIGH
)

// Watch listens for network changes in the named network namespace ns ("" for the current one)
// and sends on the returned channel once per burst: after `debounce` without further events. The
// channel closes when ctx ends. Opening a socket in another namespace needs privileges; the
// current namespace needs none.
func Watch(ctx context.Context, ns string, clk clock.Clock, debounce0 time.Duration) (<-chan struct{}, error) {
	return watch(ctx, ns, clk, debounce0, hostGroups)
}

// WatchNeighbors is Watch for the neighbor table (ARP): it triggers when an entry appears, changes
// or goes. The consumer reads the table and decides what it means (plan §2.3).
func WatchNeighbors(ctx context.Context, ns string, clk clock.Clock, debounce0 time.Duration) (<-chan struct{}, error) {
	return watch(ctx, ns, clk, debounce0, neighGroups)
}

func watch(ctx context.Context, ns string, clk clock.Clock, debounce0 time.Duration, groups int) (<-chan struct{}, error) {
	fd, err := openSocket(ns, groups)
	if err != nil {
		return nil, err
	}
	out := make(chan struct{}, 1)
	events := make(chan struct{}, 1)
	var wg sync.WaitGroup

	// the reader: blocks in recvfrom with a timeout so it notices the end of the context
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(events)
		buf := make([]byte, 1<<16)
		for ctx.Err() == nil {
			n, _, err := unix.Recvfrom(fd, buf, 0)
			switch {
			case err == nil && n > 0:
				select {
				case events <- struct{}{}:
				default: // an event is already waiting for the debouncer
				}
			case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
			case errors.Is(err, unix.ENOBUFS):
				// events were lost in a burst: something changed, the consumer reads the state again
				select {
				case events <- struct{}{}:
				default:
				}
			case err == nil:
			case errors.Is(err, unix.EBADF), errors.Is(err, unix.ENOTSOCK), errors.Is(err, unix.EINVAL):
				return // the socket is gone; the consumer sees the closed channel
			default:
				time.Sleep(100 * time.Millisecond) // a transient error: keep listening
			}
		}
	}()

	// the debouncer: one trigger per burst
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(out)
		debounce(ctx, clk, debounce0, events, out)
	}()

	go func() {
		wg.Wait()
		_ = unix.Close(fd)
	}()
	return out, nil
}

// openSocket creates the netlink socket, inside the namespace when one is named. Entering a
// namespace changes the calling thread: the work happens in a goroutine of its own that is locked
// to its thread, and when the thread cannot be restored it is left locked so it ends with the
// goroutine instead of going back into the pool in the wrong namespace.
func openSocket(ns string, groups int) (int, error) {
	type result struct {
		fd  int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		var orig *os.File
		if ns != "" {
			var err error
			if orig, err = os.Open("/proc/thread-self/ns/net"); err != nil {
				ch <- result{-1, err}
				runtime.UnlockOSThread()
				return
			}
			defer func() { _ = orig.Close() }()
			target, err := openNS(ns)
			if err != nil {
				ch <- result{-1, err}
				runtime.UnlockOSThread()
				return
			}
			err = unix.Setns(int(target.Fd()), unix.CLONE_NEWNET)
			_ = target.Close()
			if err != nil {
				ch <- result{-1, fmt.Errorf("enter namespace %s: %w", ns, err)}
				runtime.UnlockOSThread()
				return
			}
		}
		fd, err := newSocket(groups)
		if orig != nil {
			if rerr := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); rerr != nil {
				// the thread stays in the other namespace: do not unlock it, let it die
				ch <- result{fd, err}
				return
			}
		}
		runtime.UnlockOSThread()
		ch <- result{fd, err}
	}()
	r := <-ch
	return r.fd, r.err
}

func openNS(ns string) (*os.File, error) {
	for _, dir := range []string{"/run/netns/", "/var/run/netns/"} {
		if f, err := os.Open(dir + ns); err == nil {
			return f, nil
		}
	}
	return nil, fmt.Errorf("network namespace %q not found", ns)
}

func newSocket(groups int) (int, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return -1, fmt.Errorf("netlink socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: uint32(groups)}); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("bind netlink socket: %w", err)
	}
	// a burst of events (Docker, routing daemons) must not overflow the receive buffer
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 1<<20)
	// wake up regularly to look at the context
	tv := unix.NsecToTimeval(int64(500 * time.Millisecond))
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// debounce sends on out once per burst of events on in: after d without a further event. It returns
// when in closes or ctx ends.
func debounce(ctx context.Context, clk clock.Clock, d time.Duration, in <-chan struct{}, out chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-in:
			if !ok {
				return
			}
		}
		t := clk.NewTimer(d)
	burst:
		for {
			select {
			case <-t.C():
				break burst
			case <-in:
				t.Stop()
				t = clk.NewTimer(d) // the burst goes on: wait for quiet again
			case <-ctx.Done():
				t.Stop()
				return
			}
		}
		select {
		case out <- struct{}{}:
		default:
		}
	}
}
