// Package auth works out who is making a request, either by running an OIDC
// login itself or by trusting what an auth proxy in front of it asserts.
package auth

import (
	"net/http"
	"slices"

	"github.com/chillincool/tilegate/internal/apps"
	"github.com/chillincool/tilegate/internal/claims"
)

// User is the signed-in user as far as tilegate cares.
type User struct {
	Subject string        `json:"sub"`
	Name    string        `json:"name,omitempty"`
	Email   string        `json:"email,omitempty"`
	Claims  apps.Identity `json:"claims,omitempty"`
}

// Authenticator is implemented by each auth mode.
type Authenticator interface {
	// User returns the signed-in user, or nil if there is none.
	User(r *http.Request) (*User, error)
	// Challenge responds to a page request from someone who isn't signed in.
	Challenge(w http.ResponseWriter, r *http.Request)
	// LogoutPath is the href for the sign-out link, or "" to hide it.
	LogoutPath() string
	// Register adds any mode-specific routes.
	Register(mux *http.ServeMux)
}

// ErrorPage renders a user-facing error. The server package supplies it.
type ErrorPage func(w http.ResponseWriter, r *http.Request, status int, msg string)

// ClaimPaths returns the claim paths the current apps config refers to. It is
// a func so auth always sees the latest config after a reload.
type ClaimPaths func() []string

// userFromClaims builds a User from one or more decoded token payloads. Role
// values from every payload are merged, so roles may live in the ID token, the
// access token, or both.
func userFromClaims(paths []string, payloads ...map[string]any) *User {
	u := &User{Claims: apps.Identity{}}
	for _, p := range payloads {
		if p == nil {
			continue
		}
		if u.Subject == "" {
			u.Subject, _ = p["sub"].(string)
		}
		if u.Email == "" {
			u.Email, _ = p["email"].(string)
		}
		if u.Name == "" {
			for _, k := range []string{"name", "preferred_username", "email"} {
				if s, ok := p[k].(string); ok && s != "" {
					u.Name = s
					break
				}
			}
		}
		for _, path := range paths {
			for _, v := range claims.Lookup(p, claims.ParsePath(path)) {
				if !slices.Contains(u.Claims[path], v) {
					u.Claims[path] = append(u.Claims[path], v)
				}
			}
		}
	}
	if u.Name == "" {
		u.Name = u.Subject
	}
	return u
}

// wantsHTML reports whether the client is a browser navigating to a page, as
// opposed to an API call that should just get a 401.
func wantsHTML(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/"
}
