// Package server exposes the web terminal UI, authentication, and a /ws endpoint
// that bridges a browser to the shared shell session.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"myterm/pkg/auth"
	"myterm/pkg/session"
)

// Config wires the server's dependencies.
type Config struct {
	Auth    *auth.Authenticator
	Session *session.Session
}

// Server serves the UI, login flow, and terminal websocket.
type Server struct {
	auth *auth.Authenticator
	sess *session.Session
}

// New returns a Server built from cfg.
func New(cfg Config) *Server {
	return &Server{auth: cfg.Auth, sess: cfg.Session}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Cross-site WebSocket hijacking is prevented by the SameSite=Strict session
	// cookie: a cross-site page cannot attach the cookie to its handshake, so the
	// auth check in handleWS rejects it. Returning true here keeps the upgrade
	// working through tunnels/proxies that rewrite the Host header (e.g. Phase 4).
	CheckOrigin: func(r *http.Request) bool { return true },
}

// control is a JSON message exchanged over the websocket as a TEXT frame.
// Keystrokes and shell output use BINARY frames instead.
type control struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// Routes builds the HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/ws", s.handleWS)
	mux.Handle("/", s.requireAuth(http.FileServer(http.FS(staticFS()))))
	return mux
}

type ctxKey int

const roleKey ctxKey = 0

// requireAuth redirects unauthenticated page requests to the login screen.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := s.auth.RoleFromRequest(r)
		if role == auth.RoleNone {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), roleKey, role)))
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		token := strings.TrimSpace(r.FormValue("token"))
		if s.auth.RoleForToken(token) == auth.RoleNone {
			w.WriteHeader(http.StatusUnauthorized)
			renderLogin(w, "Invalid token.")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     auth.CookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			Secure:   isHTTPS(r),
			MaxAge:   int(12 * time.Hour / time.Second),
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if s.auth.RoleFromRequest(r) != auth.RoleNone {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	renderLogin(w, "")
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	role := s.auth.RoleFromRequest(r)
	if role == auth.RoleNone {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	id, out := s.sess.Attach()
	defer s.sess.Detach(id)

	started := false
	if role == auth.RoleOwner {
		s.sess.EnsureStarted()
		started = true
	} else {
		started = s.sess.Started()
	}

	// This goroutine is the ONLY writer on conn (gorilla allows one concurrent
	// writer). It announces the role, then streams session output.
	go func() {
		rm, _ := json.Marshal(map[string]string{"type": "role", "role": role.String()})
		_ = conn.WriteMessage(websocket.TextMessage, rm)
		if !started {
			_ = conn.WriteMessage(websocket.TextMessage,
				[]byte("\r\n\x1b[90m[waiting for the owner to start a session]\x1b[0m\r\n"))
		}
		for chunk := range out {
			if werr := conn.WriteMessage(websocket.BinaryMessage, chunk); werr != nil {
				return
			}
		}
		_ = conn.Close()
	}()

	// Read loop: browser -> session. Spectator input is ignored.
	for {
		mt, data, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		if role != auth.RoleOwner {
			continue // read-only
		}
		switch mt {
		case websocket.TextMessage:
			var c control
			if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Rows > 0 && c.Cols > 0 {
				s.sess.Resize(c.Rows, c.Cols)
			}
		case websocket.BinaryMessage:
			s.sess.Write(data)
		}
	}
}

// isHTTPS reports whether the original client request used TLS, honoring the
// X-Forwarded-Proto header set by tunnels/reverse proxies.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
