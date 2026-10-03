// Package compiler turns a configuration revision, the host's observed state and a generation into
// the target state of the gateway: bridges, addresses, sysctls, routes and rules, the nftables
// layout and what verify compares against (plan §3.2).
//
// The compiler is a pure function: the same input gives the same target, byte for byte. It does
// not talk to the kernel; reading the current state and applying the difference is the job of
// internal/apply. M4 compiles the routed gateway: test networks as bridges, policy routing in
// table 100, forwarding and masquerade, gateway protection and the default access matrix. Faults,
// device identity and WireGuard networks follow with their milestones; the nftables layout
// already keeps dynamic sets and counters, deletes removed objects and hashes set definitions.
package compiler
