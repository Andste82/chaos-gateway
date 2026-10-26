package executor

import (
	"context"
	"encoding/json"
	"fmt"
)

// SetWriter writes the elements of DNS-derived sets (NftDNSElements). The production writer keeps a netlink
// connection open (netlinkSets, plan §3.4); a Runner that is not the real one gets one that calls the nft
// tool, which is what the tests of the apply and of the engine run against the simulated kernel.
type SetWriter interface {
	// WriteDNSElements writes the updates of one request in the network namespace ns ("" is the
	// executor's own) as one transaction: all or nothing.
	WriteDNSElements(ctx context.Context, ns string, updates []DNSSetUpdate) error
	// Close releases the connection.
	Close() error
}

// WithSetWriter replaces how DNS-derived sets are written. The default is a persistent netlink connection when
// the executor runs the real tools (ExecRunner), the nft tool otherwise.
func WithSetWriter(w SetWriter) Option { return func(e *Executor) { e.sets = w } }

// dnsCommands is the nftables JSON of the updates: the deletes of the elements that are refreshed
// (refreshDeletes) and of those that are taken out (explicitDeletes), then the adds with their lifetimes,
// one command per set. It is nil when there is nothing to send.
func dnsCommands(updates []DNSSetUpdate, refreshDeletes, explicitDeletes bool) []byte {
	key := func(e DNSElement) any { return map[string]any{"concat": []any{e.Client, e.Addr}} }
	var cmds []any
	for _, u := range updates {
		var del []any
		for _, e := range u.Elements {
			if (e.Replace && refreshDeletes) || (e.Delete && explicitDeletes) {
				del = append(del, key(e))
			}
		}
		if len(del) > 0 {
			cmds = append(cmds, map[string]any{"delete": map[string]any{"element": map[string]any{
				"family": NftFamily, "table": NftTable, "name": u.Set, "elem": del}}})
		}
	}
	for _, u := range updates {
		add := make([]any, 0, len(u.Elements))
		for _, e := range u.Elements {
			if !e.Delete {
				add = append(add, map[string]any{"elem": map[string]any{"val": key(e), "timeout": e.TimeoutSeconds}})
			}
		}
		if len(add) > 0 {
			cmds = append(cmds, map[string]any{"add": map[string]any{"element": map[string]any{
				"family": NftFamily, "table": NftTable, "name": u.Set, "elem": add}}})
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	b, _ := json.Marshal(map[string]any{"nftables": cmds})
	return b
}

// refreshes reports whether an update deletes an element: a refresh, or an element that is taken out.
func refreshes(updates []DNSSetUpdate) bool {
	for _, u := range updates {
		for _, e := range u.Elements {
			if e.Replace || e.Delete {
				return true
			}
		}
	}
	return false
}

// deletesOf returns every element that is taken out, as an update of its own each: written one by one,
// one that is not there does not stop the others.
func deletesOf(updates []DNSSetUpdate) []DNSSetUpdate {
	var out []DNSSetUpdate
	for _, u := range updates {
		for _, e := range u.Elements {
			if e.Delete {
				out = append(out, DNSSetUpdate{Set: u.Set, Elements: []DNSElement{e}})
			}
		}
	}
	return out
}

// cliSets writes through `nft -j -f -`.
type cliSets struct{ run Runner }

func (w cliSets) Close() error { return nil }

func (w cliSets) WriteDNSElements(ctx context.Context, ns string, updates []DNSSetUpdate) error {
	err := w.write(ctx, ns, dnsCommands(updates, true, true))
	if err != nil && refreshes(updates) {
		// an element that was to be refreshed or taken out ran out in the meantime: the delete finds
		// nothing and fails the transaction. The adds alone are the right thing then, and the elements
		// to take out go one by one (one that is not there is what was wanted).
		if err = w.write(ctx, ns, dnsCommands(updates, false, false)); err == nil {
			for _, d := range deletesOf(updates) {
				_ = w.write(ctx, ns, dnsCommands([]DNSSetUpdate{d}, false, true))
			}
		}
	}
	return err
}

func (w cliSets) write(ctx context.Context, ns string, ruleset []byte) error {
	if ruleset == nil {
		return nil
	}
	c := Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: string(ruleset), NS: ns}
	res, err := w.run.Run(ctx, c)
	if err != nil {
		return err
	}
	if res.Exit != 0 {
		return &CommandError{Cmd: c, Exit: res.Exit, Stderr: res.Stderr}
	}
	return nil
}

// planDNSElements is the operation as a step of a request that also holds other operations: the adds
// through the nft tool (a refresh is an add here; the netlink path does the delete). An element that is
// taken out must be there in this form (the engine never sends one beside other operations).
func planDNSElements(o *NftDNSElements) []Step {
	return []Step{{Cmd: Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: string(dnsCommands(o.Updates, false, true)), NS: o.NS}}}
}

// isDNSElements reports whether a request consists of DNS-set updates only (at least one).
func isDNSElements(ops []Operation) bool {
	if len(ops) == 0 {
		return false
	}
	for _, op := range ops {
		if _, ok := op.(*NftDNSElements); !ok {
			return false
		}
	}
	return true
}

// doDNS runs a request of DNS-set updates beside the writer: it never enters the queue (plan §3.11: top
// priority, concurrent to plans), only waits for the update before it, as the netlink connection is used
// by one request at a time.
func (e *Executor) doDNS(ctx context.Context, ops []Operation) (Outcome, error) {
	enqueued := e.clock.Now()
	select {
	case e.dnsSlot <- struct{}{}:
	case <-e.done:
		return Outcome{}, ErrClosed
	case <-ctx.Done():
		return Outcome{Generation: e.Generation(), Enqueued: enqueued}, ctx.Err()
	}
	defer func() { <-e.dnsSlot }()
	e.rmu.Lock()
	if e.closed {
		e.rmu.Unlock()
		return Outcome{}, ErrClosed
	}
	e.rwg.Add(1)
	e.rmu.Unlock()
	defer e.rwg.Done()
	out := Outcome{Enqueued: enqueued, Started: e.clock.Now()}
	for i, op := range ops {
		o := op.(*NftDNSElements)
		if err := e.sets.WriteDNSElements(ctx, o.NS, o.Updates); err != nil {
			e.log.Warn("writing DNS-derived set elements failed", "error", err)
			out.Generation = e.Generation()
			return out, fmt.Errorf("operation %d (%s): %w", i, op.OpType(), err)
		}
		out.Completed++
	}
	out.Generation = e.Generation()
	return out, nil
}
