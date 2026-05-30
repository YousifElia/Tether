//go:build windows

package pty

import (
	"context"
	"strings"

	"github.com/UserExistsError/conpty"
)

type winPTY struct {
	cpty *conpty.ConPty
}

// Start launches shell with args attached to a Windows ConPTY.
func Start(shell string, args ...string) (PTY, error) {
	cmdline := shell
	if len(args) > 0 {
		cmdline = shell + " " + strings.Join(args, " ")
	}
	c, err := conpty.Start(cmdline)
	if err != nil {
		return nil, err
	}
	return &winPTY{cpty: c}, nil
}

func (p *winPTY) Read(b []byte) (int, error)  { return p.cpty.Read(b) }
func (p *winPTY) Write(b []byte) (int, error) { return p.cpty.Write(b) }

// conpty.Resize takes (width, height) i.e. (cols, rows).
func (p *winPTY) Resize(rows, cols int) error { return p.cpty.Resize(cols, rows) }

func (p *winPTY) Close() error { return p.cpty.Close() }

func (p *winPTY) Wait() error {
	_, err := p.cpty.Wait(context.Background())
	return err
}
