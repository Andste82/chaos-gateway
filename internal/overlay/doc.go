// Package overlay is the store of overlays (plan §2.1.1): what tests switch on and off at runtime,
// layered over the configuration. It is in memory only. Overlays are never revisioned and never
// written to disk, so a restart starts from a clean baseline: the kernel state is recompiled from
// the committed revision and the observed state alone.
//
// An overlay has an owner (a user, an API token or a run), an optional TTL and an optional lease
// that the owner must renew. Both run on the monotonic clock of internal/clock, never on wall
// time, which can jump (a Raspberry Pi has no real-time clock). Writing an overlay with the key
// of an existing one (owner, kind, target, selector; domain.OverlayKey) replaces it and keeps its
// id.
//
// The store is a passive data structure. It starts no goroutines and sends no events: every
// mutation returns the Changes it made, and the state owner (plan §3.11) publishes them as events
// and gives the batch its generation. Expiry is driven from outside: NextDeadline says when
// something falls due, Expire removes what has, and an Expirer arms a timer on the injected clock
// for that. Nothing is removed implicitly, so a write that arrives just before a deadline is
// ordered before the expiry, as it would be on the state owner's single goroutine.
//
// The store is safe for concurrent use (its own mutex, never held while calling out), but the
// state owner is its only writer in practice. Overlays it hands out share their bodies with the
// store and must be treated as immutable, like everything in a published snapshot.
package overlay
