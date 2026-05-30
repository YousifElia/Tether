//go:build !windows

package pty

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

// Start launches shell with args attached to a new Unix pseudo-terminal.
func Start(shell string, args ...string) (PTY, error) {
	c := exec.Command(shell, args...)
	f, err := pty.Start(c)
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: c}, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixPTY) Resize(rows, cols int) error {
	return pty.Setsize(p.f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (p *unixPTY) Close() error { return p.f.Close() }
func (p *unixPTY) Wait() error  { return p.cmd.Wait() }
