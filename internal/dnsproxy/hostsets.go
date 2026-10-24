package dnsproxy

import (
	"context"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the proxy's side of the hostname selectors (plan §2.6, §3.11, M20). The kernel matches addresses,
// so the proxy records which addresses it answered a device for the names that rules and faults select
// (`hostname_sets` of its configuration): after the answer is known and before it is sent, it reports (client,
// name, addresses, TTL) to the API, which has them written into the DNS-derived sets and returns when that is
// done. The answer waits for the call, so the device's first packet already matches.
//
// A name is a selector's if the question or a CNAME target in the answer matches it (it follows CNAMEs); the
// addresses are the A records of the answer, with the smallest TTL. Reports of concurrent queries are
// collected for at most BatchWindow and made as one call; one that was made a moment ago for the same
// client, set and addresses is not made again (the API holds an element for at least ten minutes).

// ResolutionSink receives the resolutions the proxy reports; the API implements it behind an HTTP call.
type ResolutionSink interface {
	PostResolutions(ctx context.Context, rs []model.DnsResolution) error
}

// callTimeout bounds the wait of an answer for the call: a gateway that cannot write the sets in time answers the
// device anyway (the faults of the names then miss the first packets, which the log of the proxy says).
const callTimeout = 2 * time.Second

// repeatAfter is how long a report that was made stands for a repetition of itself.
const repeatAfter = time.Minute

type reporter struct {
	sink   ResolutionSink
	clock  clock.Clock
	window time.Duration
	log    *slog.Logger

	mu      sync.Mutex
	pending []*pendingReport
	running bool
	recent  map[string]time.Time
}

type pendingReport struct {
	rs   []model.DnsResolution
	keys []string
	done chan error
}

func newReporter(sink ResolutionSink, c clock.Clock, window time.Duration, log *slog.Logger) *reporter {
	return &reporter{sink: sink, clock: c, window: window, log: log, recent: map[string]time.Time{}}
}

// forget drops the memory of what was reported.
func (r *reporter) forget() {
	r.mu.Lock()
	r.recent = map[string]time.Time{}
	r.mu.Unlock()
}

func resolutionKey(set string, client netip.Addr, addrs []string) string {
	a := append([]string(nil), addrs...)
	sort.Strings(a)
	return set + "|" + client.String() + "|" + strings.Join(a, ",")
}

// report makes the call for the resolutions that are new and waits for it; an error is the call's.
func (r *reporter) report(ctx context.Context, rs []model.DnsResolution) error {
	now := r.clock.Now()
	var fresh []model.DnsResolution
	var keys []string
	r.mu.Lock()
	for _, x := range rs {
		c, _ := netip.ParseAddr(x.Client)
		k := resolutionKey(x.Set, c, x.Addresses)
		if t, ok := r.recent[k]; ok && now.Sub(t) < repeatAfter {
			continue
		}
		fresh = append(fresh, x)
		keys = append(keys, k)
	}
	if len(fresh) == 0 {
		r.mu.Unlock()
		return nil
	}
	p := &pendingReport{rs: fresh, keys: keys, done: make(chan error, 1)}
	r.pending = append(r.pending, p)
	start := !r.running
	r.running = true
	r.mu.Unlock()
	if start {
		go r.flush()
	}
	select {
	case err := <-p.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// flush waits for the batch window, makes one call for everything that has gathered, and goes on while
// reports are waiting.
func (r *reporter) flush() {
	for {
		<-r.clock.After(r.window)
		r.mu.Lock()
		batch := r.pending
		r.pending = nil
		if len(batch) == 0 {
			r.running = false
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		var all []model.DnsResolution
		for _, p := range batch {
			all = append(all, p.rs...)
		}
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		err := r.sink.PostResolutions(ctx, all)
		cancel()
		now := r.clock.Now()
		r.mu.Lock()
		if err == nil {
			for _, p := range batch {
				for _, k := range p.keys {
					r.recent[k] = now
				}
			}
			if len(r.recent) > 20000 {
				for k, t := range r.recent {
					if now.Sub(t) >= repeatAfter {
						delete(r.recent, k)
					}
				}
			}
		}
		r.mu.Unlock()
		for _, p := range batch {
			p.done <- err
		}
	}
}

// resolutionsOf lists what a response says for the hostname sets: one resolution for every set that has a
// name of the question or of a CNAME in the answer, when the client is one the set records, with the A records of
// the answer.
func (p *parsedConfig) resolutionsOf(client netip.Addr, resp *dns.Msg) []model.DnsResolution {
	if len(p.sets) == 0 || resp == nil || resp.Rcode != dns.RcodeSuccess || len(resp.Question) == 0 {
		return nil
	}
	var addrs []string
	ttl := uint32(0)
	for _, rr := range resp.Answer {
		if a, ok := rr.(*dns.A); ok {
			addrs = append(addrs, a.A.String())
			if t := a.Hdr.Ttl; len(addrs) == 1 || t < ttl {
				ttl = t
			}
		}
	}
	if len(addrs) == 0 {
		return nil
	}
	names := []string{domain.HostnamePattern(resp.Question[0].Name)}
	for _, rr := range resp.Answer {
		if c, ok := rr.(*dns.CNAME); ok {
			names = append(names, domain.HostnamePattern(c.Target))
		}
	}
	var out []model.DnsResolution
	for _, s := range p.sets {
		if len(s.sources) > 0 && !contains(s.sources, client) {
			continue
		}
		matched := ""
		for _, n := range names {
			for _, pat := range s.names {
				if domain.HostnameMatches(pat, n) {
					matched = n
					break
				}
			}
			if matched != "" {
				break
			}
		}
		if matched == "" {
			continue
		}
		out = append(out, model.DnsResolution{Set: s.id, Client: client.String(), Name: matched, Addresses: addrs, Ttl: int(ttl)})
	}
	return out
}

// report enters the addresses of an answer into the DNS-derived sets and returns when the gateway has done so.
func (s *Server) report(ctx context.Context, client netip.Addr, r *dns.Msg, resp *dns.Msg) {
	if s.rep == nil {
		return
	}
	pc := s.parsed.Load()
	if pc == nil {
		return
	}
	rs := pc.resolutionsOf(client, resp)
	if len(rs) == 0 {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := s.rep.report(cctx, rs); err != nil {
		s.opt.Log.Warn("the answer goes out although the gateway did not take its addresses into the sets: a hostname fault may miss the first packets", "name", r.Question[0].Name, "error", err)
	}
}
