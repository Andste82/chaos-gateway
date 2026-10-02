// Package executor is the privileged part of Chaos Gateway: the only process that writes the
// kernel's network configuration (plan §3.1). It accepts a closed set of typed operations, checks
// the scope of each (§2.16), turns it into command invocations with argument arrays (no shell,
// fixed binary paths) and runs all of them one at a time.
//
//	op.go      the operation types and the strict decoder
//	scope.go   what an operation may touch
//	plan.go    operation -> commands
//	runner.go  command execution (fixed binaries, optional network namespace)
//	exec.go    the serialized queue, generations and state reads
//	proto.go   the wire protocol: framing, version handshake
//	server.go  Unix socket server with SO_PEERCRED check; client.go is the other side
package executor
