package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chillincool/tilegate/internal/apps"
	"github.com/chillincool/tilegate/internal/auth"
)

// fixedAuth signs in a user with the given groups, or nobody if groups is nil.
type fixedAuth struct{ groups []string }

func (f fixedAuth) User(*http.Request) (*auth.User, error) {
	if f.groups == nil {
		return nil, nil
	}
	return &auth.User{Subject: "u", Name: "Ünal", Claims: apps.Identity{"groups": f.groups}}, nil
}
func (fixedAuth) Challenge(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }
func (fixedAuth) LogoutPath() string                               { return "/logout" }
func (fixedAuth) Register(*http.ServeMux)                          {}

func newTestServer(t *testing.T, groups []string) http.Handler {
	t.Helper()
	p := filepath.Join(t.TempDir(), "apps.yaml")
	os.WriteFile(p, []byte(`
title: Test Apps
theme:
  mode: light
  tileSize: 80
  logo: https://example.com/logo.svg
apps:
  - name: Grafana
    url: https://grafana.example.com
    category: Monitoring
    roles: [admins]
  - name: Jellyfin
    url: https://jellyfin.example.com
    category: Media
  - name: Wiki
    url: https://wiki.example.com
    description: Team <notes>
`), 0o644)
	store, err := apps.NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:  store,
		Filter: apps.Filter{DefaultClaims: []string{"groups"}},
		Auth:   fixedAuth{groups},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return &testHandler{s: s, Handler: s.Handler()}
}

// testHandler keeps the Server so a test can change its options.
type testHandler struct {
	http.Handler
	s *Server
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestPageFiltersServerSide(t *testing.T) {
	w := get(newTestServer(t, []string{"family"}), "/")
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if strings.Contains(body, "grafana.example.com") {
		t.Error("app the user may not see was sent to the browser")
	}
	for _, want := range []string{"jellyfin.example.com", "Team &lt;notes&gt;", ">Media<", "Ü"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Uncategorised apps come before categories.
	if strings.Index(body, "wiki.example.com") > strings.Index(body, "jellyfin.example.com") {
		t.Error("uncategorised app not listed first")
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestAPI(t *testing.T) {
	w := get(newTestServer(t, []string{"admins"}), "/api/apps")
	var got []apiApp
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if len(got) != 3 {
		t.Errorf("admin sees %d apps, want 3", len(got))
	}
	if w := get(newTestServer(t, nil), "/api/apps"); w.Code != 401 {
		t.Errorf("anonymous API status %d", w.Code)
	}
}

func TestStaticAndHealth(t *testing.T) {
	h := newTestServer(t, nil)
	for _, p := range []string{"/static/app.css", "/static/app.js", "/healthz", "/readyz"} {
		if w := get(h, p); w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestTheme(t *testing.T) {
	h := newTestServer(t, []string{"family"})
	page := get(h, "/").Body.String()
	for _, want := range []string{`data-theme="light"`, `class="logo" src="https://example.com/logo.svg"`, `href="/theme.css?h=`} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	w := get(h, "/theme.css")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "--theme-tile: 80px") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Errorf("theme.css: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
}

// memFavs is an in-memory FavoriteStore.
type memFavs map[string][]string

func (m memFavs) Favorites(_ context.Context, u string) ([]string, error) { return m[u], nil }
func (m memFavs) AddFavorite(_ context.Context, u, a string) error {
	if !slices.Contains(m[u], a) {
		m[u] = append(m[u], a)
	}
	return nil
}
func (m memFavs) RemoveFavorite(_ context.Context, u, a string) error {
	m[u] = slices.DeleteFunc(m[u], func(x string) bool { return x == a })
	return nil
}
func (memFavs) Ping(context.Context) error { return nil }

// Themes live in the same map under a "theme:" key, to keep the fake small.
func (m memFavs) Theme(_ context.Context, u string) (string, error) {
	if t := m["theme:"+u]; len(t) > 0 {
		return t[0], nil
	}
	return "", nil
}
func (m memFavs) SetTheme(_ context.Context, u, t string) error {
	m["theme:"+u] = []string{t}
	return nil
}

func newFavServer(t *testing.T, groups []string, favs memFavs) http.Handler {
	t.Helper()
	h := newTestServer(t, groups)
	s := h.(*testHandler).s
	s.DB = favs
	return s.Handler()
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestFavorites(t *testing.T) {
	// "grafana" is saved but this user can no longer see it.
	favs := memFavs{"u": {"grafana", "jellyfin"}}
	h := newFavServer(t, []string{"family"}, favs)

	page := do(h, "GET", "/", nil).Body.String()
	favSection := page[strings.Index(page, `id="favorites"`):strings.Index(page, "</section>")]
	if !strings.Contains(favSection, "jellyfin.example.com") || strings.Contains(page, "grafana.example.com") {
		t.Errorf("favourites section wrong:\n%s", favSection)
	}
	if !strings.Contains(page, `aria-pressed="true"`) {
		t.Error("favourite star not pressed")
	}

	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	if w := do(h, "PUT", "/api/favorites/wiki", same); w.Code != 204 {
		t.Errorf("add: %d %s", w.Code, w.Body)
	}
	if w := do(h, "PUT", "/api/favorites/grafana", same); w.Code != 404 {
		t.Errorf("adding an app the user can't see: %d", w.Code)
	}
	if w := do(h, "PUT", "/api/favorites/wiki", map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Errorf("cross-site request: %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/favorites/jellyfin", same); w.Code != 204 {
		t.Errorf("remove: %d", w.Code)
	}
	if got := do(h, "GET", "/api/favorites", nil).Body.String(); strings.TrimSpace(got) != `["wiki"]` {
		t.Errorf("list = %s", got)
	}
}

func TestFavoritesDisabled(t *testing.T) {
	h := newTestServer(t, []string{"family"})
	if strings.Contains(get(h, "/").Body.String(), `class="star"`) {
		t.Error("stars shown without a database")
	}
	if w := do(h, "PUT", "/api/favorites/wiki", nil); w.Code != 404 && w.Code != 405 {
		t.Errorf("API reachable without a database: %d", w.Code)
	}
}

func TestThemeToggle(t *testing.T) {
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}

	// No database: the choice lives in a cookie.
	h := newTestServer(t, []string{"family"})
	w := do(h, "PUT", "/api/theme/dark", same)
	if w.Code != 204 || !strings.Contains(w.Header().Get("Set-Cookie"), "tilegate_theme=dark") {
		t.Fatalf("set theme: %d %q", w.Code, w.Header().Get("Set-Cookie"))
	}
	// The test config's admin default is light; the user's cookie wins.
	page := do(h, "GET", "/", map[string]string{"Cookie": "tilegate_theme=dark"}).Body.String()
	if !strings.Contains(page, `data-theme="dark"`) || !strings.Contains(page, `data-mode="dark"`) {
		t.Error("cookie choice not rendered")
	}
	if page := get(h, "/").Body.String(); !strings.Contains(page, `data-theme="light"`) {
		t.Error("admin default not used without a choice")
	}
	if page := do(h, "GET", "/", map[string]string{"Cookie": "tilegate_theme=auto"}).Body.String(); strings.Contains(page, "data-theme=") {
		t.Error("auto should not force a theme")
	}
	if w := do(h, "PUT", "/api/theme/sepia", same); w.Code != 400 {
		t.Errorf("bad mode: %d", w.Code)
	}
	if w := do(h, "PUT", "/api/theme/dark", map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Errorf("cross-site: %d", w.Code)
	}

	// With a database, the saved choice wins over the cookie (it follows the
	// user to other devices).
	favs := memFavs{}
	h = newFavServer(t, []string{"family"}, favs)
	do(h, "PUT", "/api/theme/dark", same)
	if favs["theme:u"][0] != "dark" {
		t.Errorf("theme not saved to database: %v", favs)
	}
	if page := do(h, "GET", "/", map[string]string{"Cookie": "tilegate_theme=light"}).Body.String(); !strings.Contains(page, `data-theme="dark"`) {
		t.Error("database choice should win over cookie")
	}
}
