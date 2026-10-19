// Package ctl is chaosctl, the command line client of the gateway's API for CI pipelines (plan §2.15, M18):
// apply a profile or a fault with a TTL, run a scenario from a file and wait for its report, wait for a device,
// and run a command under a fault that is always taken away again. It talks to the REST API with a bearer
// token and has no other access to the gateway.
//
// Exit codes: the commands that decide something use 0 (it holds, the run passed), 1 (it does not hold in time,
// a check of the run failed) and 2 (the run ended as error or aborted, or the command itself failed: a bad
// argument, an unreachable gateway, a refused request). `chaosctl with` returns the exit code of its command, and
// 125 when chaosctl itself failed, 126 when the command could not be run, 127 when it was not found (the
// conventions of timeout(1) and env(1)), so its codes can be told apart from the command's own.
package ctl
