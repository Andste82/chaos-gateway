package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/google/nftables"
)

// netlinkSets writes the elements of DNS-derived sets over a netlink connection that stays open (plan
// §3.4: "DNS-derived address-set updates go through a persistent netlink connection (google/nftables)
// held by the executor, instead of one nft process per answer"). One connection per network namespace
// is opened when the first update for it comes and kept: a lasting connection, so an update is one
// batch of messages on an open socket. The sets themselves are looked up once and remembered by name;
// an apply that deletes and creates a set makes the remembered one stale, which the kernel answers
// with ENOENT, and the update looks the set up again once.
//
// The kernel serializes the transactions of nftables, so this runs beside a plan that is applying a
// ruleset. The executor lets one request at a time use it (Executor.dnsSlot).
type netlinkSets struct {
	log *slog.Logger
	mu  sync.Mutex
	ns  map[string]*nlConn
}

type nlConn struct {
	conn *nftables.Conn
	sets map[string]*nftables.Set
	// nsFd is the file of the namespace the connection was opened in; -1 for the executor's own
	nsFd int
}

func newNetlinkSets(log *slog.Logger) *netlinkSets {
	return &netlinkSets{log: log, ns: map[string]*nlConn{}}
}

func (n *netlinkSets) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var errs error
	for k, c := range n.ns {
		errs = errors.Join(errs, c.conn.CloseLasting())
		if c.nsFd >= 0 {
			errs = errors.Join(errs, syscall.Close(c.nsFd))
		}
		delete(n.ns, k)
	}
	return errs
}

// connFor returns the connection of a namespace, opening it on first use.
func (n *netlinkSets) connFor(ns string) (*nlConn, error) {
	if c, ok := n.ns[ns]; ok {
		return c, nil
	}
	opts := []nftables.ConnOption{nftables.AsLasting()}
	fd := -1
	if ns != "" {
		f, err := os.Open("/run/netns/" + ns)
		if err != nil {
			return nil, fmt.Errorf("the namespace %s: %w", ns, err)
		}
		// the connection duplicates what it needs: the descriptor is kept to be closed with it
		fd, err = syscall.Dup(int(f.Fd()))
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		opts = append(opts, nftables.WithNetNSFd(fd))
	}
	conn, err := nftables.New(opts...)
	if err != nil {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
		return nil, err
	}
	c := &nlConn{conn: conn, sets: map[string]*nftables.Set{}, nsFd: fd}
	n.ns[ns] = c
	return c, nil
}

// drop forgets a connection that failed: the next update opens a new one.
func (n *netlinkSets) drop(ns string) {
	if c, ok := n.ns[ns]; ok {
		_ = c.conn.CloseLasting()
		if c.nsFd >= 0 {
			_ = syscall.Close(c.nsFd)
		}
		delete(n.ns, ns)
	}
}

var nftTable = &nftables.Table{Family: nftables.TableFamilyINet, Name: NftTable}

func (c *nlConn) set(name string) (*nftables.Set, error) {
	if s, ok := c.sets[name]; ok {
		return s, nil
	}
	s, err := c.conn.GetSetByName(nftTable, name)
	if err != nil {
		return nil, fmt.Errorf("set %s: %w", name, err)
	}
	c.sets[name] = s
	return s, nil
}

// key is the key of an element: the two addresses one after the other (a concatenation of two
// `ipv4_addr` fields, four bytes each).
func elementKey(e DNSElement) ([]byte, error) {
	client, err := netip.ParseAddr(e.Client)
	if err != nil {
		return nil, err
	}
	addr, err := netip.ParseAddr(e.Addr)
	if err != nil {
		return nil, err
	}
	c, a := client.As4(), addr.As4()
	return append(c[:], a[:]...), nil
}

func (n *netlinkSets) WriteDNSElements(ctx context.Context, ns string, updates []DNSSetUpdate) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	err := n.write(ns, updates, true)
	if errors.Is(err, syscall.ENOENT) {
		// an element to refresh ran out, or the set was replaced by an apply since it was looked up:
		// forget the sets and write the adds alone (the delete of an element that is gone fails the batch)
		if c, ok := n.ns[ns]; ok {
			c.sets = map[string]*nftables.Set{}
		}
		err = n.write(ns, updates, false)
	}
	if err != nil && !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.EEXIST) {
		// the connection may be out of step (the kernel closed it, the namespace went): the next update
		// opens a new one
		n.drop(ns)
	}
	if err != nil {
		return fmt.Errorf("nft_dns_elements: %w", err)
	}
	return nil
}

func (n *netlinkSets) write(ns string, updates []DNSSetUpdate, withDeletes bool) error {
	c, err := n.connFor(ns)
	if err != nil {
		return err
	}
	// everything that can fail without touching the connection comes first: the messages that are
	// queued on it are sent by the next Flush, whoever queued them
	type queued struct {
		set      *nftables.Set
		add, del []nftables.SetElement
	}
	var batch []queued
	for _, u := range updates {
		set, err := c.set(u.Set)
		if err != nil {
			return err
		}
		q := queued{set: set}
		for _, e := range u.Elements {
			k, err := elementKey(e)
			if err != nil {
				return err
			}
			q.add = append(q.add, nftables.SetElement{Key: k, Timeout: time.Duration(e.TimeoutSeconds) * time.Second})
			if withDeletes && e.Replace {
				q.del = append(q.del, nftables.SetElement{Key: k})
			}
		}
		batch = append(batch, q)
	}
	for _, q := range batch {
		if len(q.del) > 0 {
			if err := c.conn.SetDeleteElements(q.set, q.del); err != nil {
				return err
			}
		}
		if err := c.conn.SetAddElements(q.set, q.add); err != nil {
			return err
		}
	}
	return c.conn.Flush()
}
