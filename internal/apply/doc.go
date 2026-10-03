// Package apply carries a compiled target state into the kernel and checks the result (plan §2.14,
// §3.2): it reads the current state through the executor, plans the difference, applies it in one
// request and verifies it by reading the state back.
//
// The package knows nothing about revisions, confirmation or rollback; the engine decides which
// target to apply, and what to do when it fails.
package apply
