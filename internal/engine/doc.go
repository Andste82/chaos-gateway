// Package engine owns the desired state of the gateway and keeps the kernel on it (plan §3.11).
//
// One state-owner goroutine changes the desired state: the active revision, a revision that waits
// for confirmation and the observed host. Everyone else sends it commands and reads immutable
// snapshots. One apply loop compiles the latest desired state, applies and verifies it through the
// executor; commands accepted while an apply runs go into the next one. A failed apply restores the
// committed revision; commit-confirm rolls a risky change back when it is not confirmed in time.
package engine
