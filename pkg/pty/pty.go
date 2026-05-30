// Package pty starts an interactive shell behind a pseudo-terminal and exposes
// it as a simple read/write/resize interface. The implementation is selected at
// build time: ConPTY on Windows, a Unix PTY everywhere else.
package pty

// PTY is a running shell attached to a pseudo-terminal.
type PTY interface {
	// Read returns bytes produced by the shell (stdout + stderr merged).
	Read([]byte) (int, error)
	// Write sends bytes to the shell's stdin.
	Write([]byte) (int, error)
	// Resize informs the shell of a new window size, in character cells.
	Resize(rows, cols int) error
	// Close terminates the shell and releases the pseudo-terminal.
	Close() error
	// Wait blocks until the shell process exits.
	Wait() error
}
