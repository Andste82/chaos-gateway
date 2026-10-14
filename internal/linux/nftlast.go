package linux

import (
	"encoding/json"
	"time"
)

// The `last` statement (kernel 5.17+, nftables 1.0.1+) notes when its rule last matched. `nft -j list`
// prints it as {"last": {"used": <ms>}}, the milliseconds since that match when the listing was made, or
// as {"last": null} while the rule has not matched (plan §2.17 "last match", docs/open-items.md
// P3-M13-01). A rule that is created with {"last": {"used": N}} starts as if it had matched N ms before.

// CounterName returns the named counter the rule counts into ({"counter": "<name>"}), "" for none.
func (r NftRule) CounterName() string {
	for _, raw := range r.Expr {
		var st struct {
			Counter json.RawMessage `json:"counter"`
		}
		if json.Unmarshal(raw, &st) != nil || len(st.Counter) == 0 {
			continue
		}
		var name string
		if json.Unmarshal(st.Counter, &name) == nil {
			return name
		}
	}
	return ""
}

// Last reports the rule's `last` statement: has says whether the rule has one, set whether it has matched
// since it was created (or was created with a time), used how long before the listing that match was.
func (r NftRule) Last() (used time.Duration, set, has bool) {
	for _, raw := range r.Expr {
		var st map[string]json.RawMessage
		if json.Unmarshal(raw, &st) != nil || len(st) != 1 {
			continue
		}
		arg, ok := st["last"]
		if !ok {
			continue
		}
		var v *struct {
			Used *int64 `json:"used"`
		}
		if json.Unmarshal(arg, &v) != nil || v == nil || v.Used == nil || *v.Used < 0 {
			return 0, false, true
		}
		return time.Duration(*v.Used) * time.Millisecond, true, true
	}
	return 0, false, false
}

// LastUsed returns, by named counter, how long before the listing a rule that counts into it and has a
// `last` statement last matched: the most recent of them when several rules share the counter (an access
// rule stands in the forward and the input hook). A counter whose rules have not matched is left out.
func (r *Ruleset) LastUsed() map[string]time.Duration {
	out := map[string]time.Duration{}
	if r == nil {
		return out
	}
	for _, o := range r.Objects {
		if o.Rule == nil {
			continue
		}
		used, set, _ := o.Rule.Last()
		name := o.Rule.CounterName()
		if !set || name == "" {
			continue
		}
		if prev, ok := out[name]; !ok || used < prev {
			out[name] = used
		}
	}
	return out
}

// LastUsedIn returns, by chain and named counter ("<chain>\x00<counter>"), how long before the listing the
// rule last matched, for the rules with a `last` statement that matched: what a transaction that makes the
// chain anew carries over to the rule that replaces it.
func (r *Ruleset) LastUsedIn() map[string]time.Duration {
	out := map[string]time.Duration{}
	if r == nil {
		return out
	}
	for _, o := range r.Objects {
		if o.Rule == nil {
			continue
		}
		used, set, _ := o.Rule.Last()
		name := o.Rule.CounterName()
		if !set || name == "" {
			continue
		}
		k := LastKey(o.Rule.Chain, name)
		if prev, ok := out[k]; !ok || used < prev {
			out[k] = used
		}
	}
	return out
}

// LastKey is the key of LastUsedIn.
func LastKey(chain, counter string) string { return chain + "\x00" + counter }
