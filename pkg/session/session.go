// Package session manages a single shared shell behind one pseudo-terminal and
// broadcasts its output to any number of attached viewers. The owner drives the
// shell (input + resize); spectators only receive output.
package session

import (
	"sync"

	"myterm/pkg/pty"
)

// Session is one shared terminal. The shell is started lazily (on the first
// owner) and persists across reconnects until it exits or the server stops.
type Session struct {
	mu     sync.Mutex
	shell  string
	args   []string
	pty    pty.PTY
	subs   map[int]chan []byte
	nextID int
	rows   int
	cols   int
}

// New creates a session that will launch shell with args on demand.
func New(shell string, args []string) *Session {
	return &Session{
		shell: shell,
		args:  args,
		subs:  make(map[int]chan []byte),
		rows:  24,
		cols:  80,
	}
}

// Attach registers a viewer and returns its id and a receive-only output channel.
func (s *Session) Attach() (int, <-chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	ch := make(chan []byte, 256)
	s.subs[id] = ch
	return id, ch
}

// Detach removes a viewer and closes its channel. Safe to call more than once.
func (s *Session) Detach(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.subs[id]; ok {
		close(ch)
		delete(s.subs, id)
	}
}

// Started reports whether the shell is currently running.
func (s *Session) Started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pty != nil
}

// EnsureStarted launches the shell if it is not already running. Owners call this.
func (s *Session) EnsureStarted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pty != nil {
		return
	}
	p, err := pty.Start(s.shell, s.args...)
	if err != nil {
		s.broadcastLocked([]byte("\r\nfailed to start shell: " + err.Error() + "\r\n"))
		return
	}
	_ = p.Resize(s.rows, s.cols)
	s.pty = p
	go s.readLoop(p)
}

func (s *Session) readLoop(p pty.PTY) {
	buf := make([]byte, 4096)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.mu.Lock()
			s.broadcastLocked(chunk)
			s.mu.Unlock()
		}
		if err != nil {
			s.mu.Lock()
			if s.pty == p {
				s.pty = nil
			}
			s.broadcastLocked([]byte("\r\n\x1b[90m[shell exited]\x1b[0m\r\n"))
			s.mu.Unlock()
			return
		}
	}
}

// broadcastLocked fans p out to every subscriber. The caller holds s.mu. A
// subscriber that cannot keep up is dropped (its channel is closed) so one slow
// client never stalls the shell for everyone else.
func (s *Session) broadcastLocked(p []byte) {
	for id, ch := range s.subs {
		select {
		case ch <- p:
		default:
			close(ch)
			delete(s.subs, id)
		}
	}
}

// Write sends input to the shell. No-op when the shell is not running.
func (s *Session) Write(p []byte) {
	s.mu.Lock()
	pt := s.pty
	s.mu.Unlock()
	if pt != nil {
		_, _ = pt.Write(p)
	}
}

// Resize updates the shell window size and remembers it for future shells.
func (s *Session) Resize(rows, cols int) {
	s.mu.Lock()
	s.rows, s.cols = rows, cols
	pt := s.pty
	s.mu.Unlock()
	if pt != nil {
		_ = pt.Resize(rows, cols)
	}
}

// Close terminates the shell and disconnects every viewer.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pty != nil {
		_ = s.pty.Close()
		s.pty = nil
	}
	for id, ch := range s.subs {
		close(ch)
		delete(s.subs, id)
	}
}
