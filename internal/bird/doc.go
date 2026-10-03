// Package bird holds what Chaos Gateway knows about BIRD 2 (plan §2.2.2): the configuration it
// generates from the routing model, the safety checks on configuration text, the configuration of
// the remote side of a link, and the parsers for `birdc` output. Starting BIRD, writing the file and
// `birdc configure` are the executor's job.
//
// The generated configuration follows spike S15: learned routes are exported into Chaos Gateway's
// routing table only (a kernel protocol with `export where source ~ [...]`), every neighbor has an
// import filter that rejects the default route and the protected prefixes (management, uplink and
// the gateway's own networks), and an import limit protects against a neighbor that announces too
// much.
package bird
