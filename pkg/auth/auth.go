// Package auth handles access control for the web terminal. A request carries a
// token in a SameSite=Strict cookie; the token maps to a role. SameSite=Strict
// is what protects against cross-site WebSocket hijacking: a malicious page
// cannot attach this cookie to its own WebSocket handshake.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

// CookieName is the session cookie that stores the access token.
const CookieName = "myterm_session"

// Role is the access level granted to a connection.
type Role int

const (
	RoleNone      Role = iota // not authenticated
	RoleSpectator             // read-only viewer
	RoleOwner                 // full read/write
)

func (r Role) String() string {
	switch r {
	case RoleOwner:
		return "owner"
	case RoleSpectator:
		return "spectator"
	default:
		return "none"
	}
}

// Authenticator validates tokens against the configured owner and (optional)
// spectator credentials.
type Authenticator struct {
	ownerToken     string
	spectatorToken string // empty => spectators disabled
}

// New returns an Authenticator. An empty spectatorToken disables spectators.
func New(ownerToken, spectatorToken string) *Authenticator {
	return &Authenticator{ownerToken: ownerToken, spectatorToken: spectatorToken}
}

// GenerateToken returns a 24-byte URL-safe random token.
func GenerateToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// RoleForToken resolves a raw token to a role using constant-time comparison.
func (a *Authenticator) RoleForToken(token string) Role {
	if token == "" {
		return RoleNone
	}
	if eq(token, a.ownerToken) {
		return RoleOwner
	}
	if a.spectatorToken != "" && eq(token, a.spectatorToken) {
		return RoleSpectator
	}
	return RoleNone
}

// RoleFromRequest resolves the role from the request's session cookie.
func (a *Authenticator) RoleFromRequest(r *http.Request) Role {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return RoleNone
	}
	return a.RoleForToken(c.Value)
}

// SpectatorsEnabled reports whether a spectator token is configured.
func (a *Authenticator) SpectatorsEnabled() bool { return a.spectatorToken != "" }

func eq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
