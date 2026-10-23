package server

import (
	"net/http"
	"slices"
	"strings"

	"github.com/chillincool/tilegate/internal/auth"
)

const themeCookie = "tilegate_theme"

var themeModes = []string{"auto", "light", "dark"}

// userTheme picks the page's light/dark mode: the user's own choice if they
// made one (from the database, which follows them across devices, then the
// cookie), otherwise the admin's default. It's decided on the server so the
// page never flashes the wrong theme while loading.
func (s *Server) userTheme(r *http.Request, u *auth.User, def string) string {
	if s.DB != nil && u.Subject != "" {
		t, err := s.DB.Theme(r.Context(), u.Subject)
		if err != nil {
			s.Log.Error("loading theme preference", "err", err, "user", u.Subject)
		} else if slices.Contains(themeModes, t) {
			return t
		}
	}
	if c, err := r.Cookie(themeCookie); err == nil && slices.Contains(themeModes, c.Value) {
		return c.Value
	}
	return def
}

func (s *Server) registerTheme(mux *http.ServeMux) {
	mux.Handle("PUT /api/theme/{mode}", http.NewCrossOriginProtection().Handler(http.HandlerFunc(s.setTheme)))
}

func (s *Server) setTheme(w http.ResponseWriter, r *http.Request) {
	u := s.user(w, r)
	if u == nil {
		return
	}
	mode := r.PathValue("mode")
	if !slices.Contains(themeModes, mode) {
		http.Error(w, "mode must be auto, light or dark", http.StatusBadRequest)
		return
	}
	// The cookie makes the choice work without a database, and before the
	// database copy is read on another device.
	http.SetCookie(w, &http.Cookie{
		Name:     themeCookie,
		Value:    mode,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
	})
	if s.DB != nil && u.Subject != "" {
		if err := s.DB.SetTheme(r.Context(), u.Subject, mode); err != nil {
			s.Log.Error("saving theme preference", "err", err, "user", u.Subject)
			http.Error(w, "could not save", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
