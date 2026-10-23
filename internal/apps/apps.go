// Package apps loads the apps YAML file and decides which apps a user may see.
package apps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"go.yaml.in/yaml/v3"
)

const dashboardIconsBase = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons"

// Config is the parsed apps file.
type Config struct {
	Title string `yaml:"title"`
	Theme Theme  `yaml:"theme"`
	Apps  []App  `yaml:"apps"`
}

// App is one launcher tile.
type App struct {
	// ID identifies the app in saved favourites. It defaults to a slug of the
	// name; set it explicitly to keep favourites when renaming an app.
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	// LoginURL, if set, is where the tile actually links: typically the app's
	// "sign in with SSO" endpoint, so users skip its local login page. A value
	// starting with "/" is resolved against URL.
	LoginURL    string `yaml:"loginURL"`
	Icon        string `yaml:"icon"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
	// Roles lists the roles/groups that may see this app; holding any one is
	// enough. An empty list means every signed-in user sees it.
	Roles []string `yaml:"roles"`
	// Claim overrides the global roles claim path(s) for this app only.
	Claim string `yaml:"claim"`
	// NewTab opens the app in a new browser tab.
	NewTab bool `yaml:"newTab"`

	// Derived at load time.
	Href    string `yaml:"-"`
	IconURL string `yaml:"-"`
	Initial string `yaml:"-"`
	Hue     int    `yaml:"-"`
}

// Parse decodes and validates an apps file. Unknown keys are rejected so that a
// typo such as "role:" can't silently make an app visible to everyone.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	// An empty file decodes to io.EOF; treat it as an empty config.
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if c.Title == "" {
		c.Title = "My Apps"
	}
	var errs []error
	if err := c.Theme.prepare(); err != nil {
		errs = append(errs, fmt.Errorf("theme: %w", err))
	}
	for i := range c.Apps {
		a := &c.Apps[i]
		if err := a.prepare(); err != nil {
			errs = append(errs, fmt.Errorf("apps[%d] (%s): %w", i, a.Name, err))
		}
	}
	seen := map[string]string{}
	for _, a := range c.Apps {
		if other, dup := seen[a.ID]; dup && a.ID != "" {
			errs = append(errs, fmt.Errorf("apps %q and %q both have id %q; give one of them a different id", other, a.Name, a.ID))
		}
		seen[a.ID] = a.Name
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &c, nil
}

func (a *App) prepare() error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return errors.New("name is required")
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url %q must be an absolute http(s) URL", a.URL)
	}
	a.Href = a.URL
	if a.LoginURL != "" {
		l, err := u.Parse(a.LoginURL)
		if err != nil || (l.Scheme != "http" && l.Scheme != "https") || l.Host == "" {
			return fmt.Errorf("loginURL %q must be an http(s) URL or a path starting with /", a.LoginURL)
		}
		a.Href = l.String()
	}
	if a.ID == "" {
		a.ID = slug(a.Name)
	} else if a.ID != slug(a.ID) {
		return fmt.Errorf("id %q may only contain lowercase letters, digits and dashes", a.ID)
	}
	if a.ID == "" {
		return errors.New("name has no letters or digits; set an id")
	}
	for i, r := range a.Roles {
		a.Roles[i] = strings.TrimSpace(r)
		if a.Roles[i] == "" {
			return errors.New("roles must not contain empty entries")
		}
	}
	a.IconURL = resolveIcon(a.Icon)
	a.Initial = strings.ToUpper(string([]rune(a.Name)[:1]))
	h := fnv.New32a()
	h.Write([]byte(a.Name))
	a.Hue = int(h.Sum32() % 360)
	return nil
}

// slug lowercases s and replaces each run of other characters with a dash:
// "Home Assistant" → "home-assistant".
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	return b.String()
}

// Lookup returns the app with the given id.
func (c *Config) Lookup(id string) (App, bool) {
	for _, a := range c.Apps {
		if a.ID == id {
			return a, true
		}
	}
	return App{}, false
}

// resolveIcon turns the icon field into a URL the browser can load:
//   - "" → no icon; the tile shows the app's initial instead
//   - "https://…", "/…", "data:…" → used as-is
//   - "di:grafana" → the dashboard-icons project on jsDelivr (svg unless an extension is given)
//   - anything else → a file in the local icons directory, served under /icons/
func resolveIcon(icon string) string {
	switch {
	case icon == "":
		return ""
	case strings.HasPrefix(icon, "http://"), strings.HasPrefix(icon, "https://"),
		strings.HasPrefix(icon, "/"), strings.HasPrefix(icon, "data:image/"):
		return icon
	case strings.HasPrefix(icon, "di:"):
		name := strings.TrimPrefix(icon, "di:")
		ext := strings.TrimPrefix(path.Ext(name), ".")
		if ext == "" {
			ext, name = "svg", name+".svg"
		}
		return dashboardIconsBase + "/" + ext + "/" + url.PathEscape(name)
	default:
		return "/icons/" + url.PathEscape(path.Clean("/" + icon)[1:])
	}
}

// Identity is what the filter needs to know about a user: the values found at
// each claim path, keyed by the path string as written in the configuration.
type Identity map[string][]string

// Filter decides app visibility.
type Filter struct {
	// DefaultClaims are the claim paths used for apps without their own "claim".
	DefaultClaims []string
}

// ClaimPaths returns every claim path the filter may consult for this config.
func (f Filter) ClaimPaths(c *Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range f.DefaultClaims {
		add(p)
	}
	for _, a := range c.Apps {
		add(a.Claim)
	}
	return out
}

// Visible returns the apps the identity may see, preserving file order.
func (f Filter) Visible(c *Config, id Identity) []App {
	out := make([]App, 0, len(c.Apps))
	for _, a := range c.Apps {
		if f.allowed(a, id) {
			out = append(out, a)
		}
	}
	return out
}

func (f Filter) allowed(a App, id Identity) bool {
	if len(a.Roles) == 0 {
		return true
	}
	paths := f.DefaultClaims
	if a.Claim != "" {
		paths = []string{a.Claim}
	}
	for _, p := range paths {
		for _, have := range id[p] {
			for _, want := range a.Roles {
				if have == want {
					return true
				}
			}
		}
	}
	return false
}

// Store holds the current config and reloads it when the file changes, which
// covers Kubernetes ConfigMap updates without a pod restart.
type Store struct {
	path string
	cur  atomic.Pointer[Config]
	sum  [32]byte
}

// NewStore loads the file once; a broken file at startup is a fatal error.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	if _, err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns the current config.
func (s *Store) Get() *Config { return s.cur.Load() }

func (s *Store) reload() (bool, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(data)
	if sum == s.sum {
		return false, nil
	}
	// Remember the content even if it's invalid, so a broken file is
	// reported once rather than on every poll.
	s.sum = sum
	c, err := Parse(data)
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.path, err)
	}
	s.cur.Store(c)
	return true, nil
}

// Watch polls the file until ctx is done. An invalid edit is logged and the
// previous config stays in place.
func (s *Store) Watch(ctx context.Context, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			changed, err := s.reload()
			switch {
			case err != nil:
				log.Error("apps file reload failed, keeping previous config", "err", err)
			case changed:
				log.Info("apps file reloaded", "apps", len(s.Get().Apps))
			}
		}
	}
}
