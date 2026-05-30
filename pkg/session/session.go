// Package session manages a single shared shell behind one pseudo-terminal and
// broadcasts its output to any number of attached viewers. The owner drives the
// shell (input + resize); spectators only receive output.
//
// A bounded scrollback buffer of recent output is kept so that a viewer who
// attaches late (a joining spectator, or the owner reconnecting after a dropped
// connection) is replayed recent history instead of seeing a blank screen.
package session

import (
	"encoding/json"
	"sync"

	"myterm/pkg/pty"
)

// Frame is one message destined for a viewer's websocket. Text frames carry
// JSON control/status messages; binary frames carry raw shell output.
type Frame struct {
	Text bool
	Data []byte
}

type sizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// Session is one shared terminal. The shell is started lazily (on the first
// owner) and persists across reconnects until it exits or the server stops.
type Session struct {
	mu        sync.Mutex
	shell     string
	args      []string
	pty       pty.PTY
	subs      map[int]chan Frame
	nextID    int
	rows      int
	cols      int
	scroll    []byte // recent raw output, capped at maxScroll
	maxScroll int
}

// New creates a session that launches shell with args on demand and retains up
// to scrollbackBytes of recent output for replay.
func New(shell string, args []string, scrollbackBytes int) *Session {
	if scrollbackBytes <= 0 {
		scrollbackBytes = 256 * 1024
	}
	return &Session{
		shell:     shell,
		args:      args,
		subs:      make(map[int]chan Frame),
		rows:      24,
		cols:      80,
		maxScroll: scrollbackBytes,
	}
}

// Attach registers a viewer and returns its id and output channel. The channel
// is primed with the current size and the scrollback buffer so the viewer can
// render recent history immediately.
func (s *Session) Attach() (int, <-chan Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	ch := make(chan Frame, 256)
	s.subs[id] = ch
	ch <- s.sizeFrameLocked()
	if len(s.scroll) > 0 {
		cp := make([]byte, len(s.scroll))
		copy(cp, s.scroll)
		ch <- Frame{Data: cp}
	}
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
		s.broadcastLocked(Frame{Data: []byte("\r\nfailed to start shell: " + err.Error() + "\r\n")})
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
			s.appendScrollLocked(chunk)
			s.broadcastLocked(Frame{Data: chunk})
			s.mu.Unlock()
		}
		if err != nil {
			s.mu.Lock()
			if s.pty == p {
				s.pty = nil
			}
			s.scroll = nil // shell is gone; don't replay a dead session
			s.broadcastLocked(Frame{Data: []byte("\r\n\x1b[90m[shell exited]\x1b[0m\r\n")})
			s.mu.Unlock()
			return
		}
	}
}

// appendScrollLocked appends p to the scrollback, trimming the oldest bytes in
// place once the cap is exceeded. Caller holds s.mu.
func (s *Session) appendScrollLocked(p []byte) {
	s.scroll = append(s.scroll, p...)
	if over := len(s.scroll) - s.maxScroll; over > 0 {
		n := copy(s.scroll, s.scroll[over:]) // copy handles the overlap (memmove)
		s.scroll = s.scroll[:n]
	}
}

// broadcastLocked fans f out to every subscriber. The caller holds s.mu. A
// subscriber that cannot keep up is dropped (its channel is closed) so one slow
// client never stalls the shell for everyone else.
func (s *Session) broadcastLocked(f Frame) {
	for id, ch := range s.subs {
		select {
		case ch <- f:
		default:
			close(ch)
			delete(s.subs, id)
		}
	}
}

func (s *Session) sizeFrameLocked() Frame {
	b, _ := json.Marshal(sizeMsg{Type: "size", Cols: s.cols, Rows: s.rows})
	return Frame{Text: true, Data: b}
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

// Resize updates the shell window size, remembers it for future shells, and
// notifies every viewer of the new dimensions.
func (s *Session) Resize(rows, cols int) {
	s.mu.Lock()
	s.rows, s.cols = rows, cols
	pt := s.pty
	s.broadcastLocked(s.sizeFrameLocked())
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
