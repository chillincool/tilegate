// Package server serves the launcher page and its small JSON API.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chillincool/tilegate/internal/apps"
	"github.com/chillincool/tilegate/internal/auth"
)

//go:embed templates static
var assets embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"mod": func(a, b int) int { return a % b },
	"initial": func(s string) string {
		for _, r := range s {
			return strings.ToUpper(string(r))
		}
		return "?"
	},
}).ParseFS(assets, "templates/*.html"))

// Version is set at build time and shown in the page footer and /healthz.
var Version = "dev"

// Server wires the apps store, filter and authenticator together.
type Server struct {
	Store    *apps.Store
	Filter   apps.Filter
	Auth     auth.Authenticator
	IconsDir string
	Log      *slog.Logger
	// DB is optional; nil turns favourites off and keeps theme choices in a cookie only.
	DB UserStore
}

type readier interface {
	Ready(ctx context.Context) error
}

// Handler returns the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", cacheFor(24*time.Hour, http.StripPrefix("/static/", http.FileServerFS(static))))
	if s.IconsDir != "" {
		mux.Handle("GET /icons/", cacheFor(24*time.Hour, http.StripPrefix("/icons/", http.FileServer(noDirs{http.Dir(s.IconsDir)}))))
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok " + Version + "\n"))
	})
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /api/apps", s.apiApps)
	mux.HandleFunc("GET /theme.css", s.themeCSS)
	s.registerFavorites(mux)
	s.registerTheme(mux)
	s.Auth.Register(mux)
	return securityHeaders(mux)
}

// ErrorPage renders a minimal error page; it is handed to the auth package.
func ErrorPage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.ExecuteTemplate(w, "error.html", map[string]any{
		"Status": status, "StatusText": http.StatusText(status), "Message": msg, "Version": Version,
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if rd, ok := s.Auth.(readier); ok {
		if err := rd.Ready(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	if s.DB != nil {
		if err := s.DB.Ping(r.Context()); err != nil {
			http.Error(w, "database: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	w.Write([]byte("ok\n"))
}

func (s *Server) user(w http.ResponseWriter, r *http.Request) *auth.User {
	u, err := s.Auth.User(r)
	if err != nil {
		s.Log.Error("authentication failed", "err", err)
		ErrorPage(w, r, http.StatusServiceUnavailable, "Sign-in is unavailable right now. Try again shortly.")
		return nil
	}
	if u == nil {
		s.Auth.Challenge(w, r)
	}
	return u
}

type group struct {
	Name  string
	Tiles []tile
}

// tile is an app plus its per-user state, as the template needs it.
type tile struct {
	apps.App
	Fav   bool
	Stars bool
}

// groupApps keeps file order: categories appear where their first app does,
// and uncategorised apps come first.
func groupApps(list []tile) []group {
	var out []group
	idx := map[string]int{}
	for _, t := range list {
		i, ok := idx[t.Category]
		if !ok {
			i = len(out)
			idx[t.Category] = i
			out = append(out, group{Name: t.Category})
		}
		out[i].Tiles = append(out[i].Tiles, t)
	}
	if i, ok := idx[""]; ok && i != 0 {
		out = append([]group{out[i]}, append(out[:i:i], out[i+1:]...)...)
	}
	return out
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	u := s.user(w, r)
	if u == nil {
		return
	}
	cfg := s.Store.Get()
	visible := s.Filter.Visible(cfg, u.Claims)
	tiles, favs := s.tiles(r, u, visible)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	err := tmpl.ExecuteTemplate(w, "page.html", map[string]any{
		"Title":     cfg.Title,
		"Theme":     cfg.Theme,
		"Mode":      s.userTheme(r, u, cfg.Theme.Mode),
		"User":      u,
		"Logout":    s.Auth.LogoutPath(),
		"Groups":    groupApps(tiles),
		"Favorites": favs,
		"Stars":     s.DB != nil,
		"Count":     len(visible),
		"Version":   Version,
	})
	if err != nil {
		s.Log.Error("render page", "err", err)
	}
}

// themeCSS serves the stylesheet generated from the theme section. The page
// links it with a content hash, so it can be cached for a long time.
func (s *Server) themeCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(s.Store.Get().Theme.CSS))
}

type apiApp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Href        string `json:"href"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

func (s *Server) apiApps(w http.ResponseWriter, r *http.Request) {
	u := s.user(w, r)
	if u == nil {
		return
	}
	visible := s.Filter.Visible(s.Store.Get(), u.Claims)
	out := make([]apiApp, 0, len(visible))
	for _, a := range visible {
		out = append(out, apiApp{a.ID, a.Name, a.URL, a.Href, a.IconURL, a.Description, a.Category})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Icons may come from any https host (e.g. a CDN), everything else is local.
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' https: data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func cacheFor(d time.Duration, next http.Handler) http.Handler {
	v := "public, max-age=" + strconv.Itoa(int(d.Seconds()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", v)
		next.ServeHTTP(w, r)
	})
}

// noDirs hides directory listings in the icons directory.
type noDirs struct{ fs http.FileSystem }

func (n noDirs) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err == nil && st.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}
