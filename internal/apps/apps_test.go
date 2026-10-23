package apps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
title: Home
apps:
  - name: Grafana
    url: https://grafana.example.com
    icon: di:grafana
    roles: [admins, monitoring]
  - name: Radarr
    url: https://radarr.example.com
    icon: radarr.png
    roles: [access]
    claim: radarr_roles
  - name: Wiki
    url: https://wiki.example.com
`

func names(as []App) string {
	var n []string
	for _, a := range as {
		n = append(n, a.Name)
	}
	return strings.Join(n, ",")
}

func TestVisible(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	f := Filter{DefaultClaims: []string{"groups", "realm_access.roles"}}

	tests := []struct {
		id   Identity
		want string
	}{
		{Identity{}, "Wiki"},
		{Identity{"groups": {"admins"}}, "Grafana,Wiki"},
		{Identity{"realm_access.roles": {"monitoring"}}, "Grafana,Wiki"},
		{Identity{"radarr_roles": {"access"}}, "Radarr,Wiki"},
		// "access" in a default claim must not satisfy an app with its own claim.
		{Identity{"groups": {"access"}}, "Wiki"},
	}
	for _, tt := range tests {
		if got := names(f.Visible(c, tt.id)); got != tt.want {
			t.Errorf("Visible(%v) = %s, want %s", tt.id, got, tt.want)
		}
	}

	paths := f.ClaimPaths(c)
	if strings.Join(paths, ",") != "groups,realm_access.roles,radarr_roles" {
		t.Errorf("ClaimPaths = %v", paths)
	}
}

func TestParseRejects(t *testing.T) {
	bad := map[string]string{
		"unknown key":    "apps:\n  - name: A\n    url: https://a\n    role: [x]\n",
		"missing name":   "apps:\n  - url: https://a\n",
		"javascript url": "apps:\n  - name: A\n    url: javascript:alert(1)\n",
		"relative url":   "apps:\n  - name: A\n    url: /foo\n",
		"empty role":     "apps:\n  - name: A\n    url: https://a\n    roles: ['']\n",
	}
	for name, doc := range bad {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if c, err := Parse(nil); err != nil || c.Title != "My Apps" {
		t.Errorf("empty file: %v %v", c, err)
	}
}

func TestResolveIcon(t *testing.T) {
	tests := map[string]string{
		"":                 "",
		"https://x/y.png":  "https://x/y.png",
		"di:grafana":       dashboardIconsBase + "/svg/grafana.svg",
		"di:plex.png":      dashboardIconsBase + "/png/plex.png",
		"radarr.png":       "/icons/radarr.png",
		"../../etc/passwd": "/icons/etc%2Fpasswd",
	}
	for in, want := range tests {
		if got := resolveIcon(in); got != want {
			t.Errorf("resolveIcon(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoreReload(t *testing.T) {
	p := filepath.Join(t.TempDir(), "apps.yaml")
	os.WriteFile(p, []byte(sample), 0o644)
	s, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte(sample+"  - nme: typo\n"), 0o644)
	if _, err := s.reload(); err == nil {
		t.Fatal("invalid edit accepted")
	}
	if len(s.Get().Apps) != 3 {
		t.Error("previous config not kept after invalid edit")
	}
	if _, err := s.reload(); err != nil {
		t.Error("same invalid content reported twice")
	}
	os.WriteFile(p, []byte(sample+"  - name: New\n    url: https://new\n"), 0o644)
	if changed, err := s.reload(); !changed || err != nil || len(s.Get().Apps) != 4 {
		t.Errorf("fixed file not picked up: %v %v", changed, err)
	}
}

func TestTheme(t *testing.T) {
	c, err := Parse([]byte(`
theme:
  mode: dark
  tileSize: 84
  accent: "#ff6600"
  background: rgb(240 240 240)
  backgroundDark: "#101010"
  backgroundImage: bg.jpg
  backgroundImageDark: https://example.com/night.jpg
  logo: di:keycloak
  customCSS: ".label { font-weight: 700; }"
`))
	if err != nil {
		t.Fatal(err)
	}
	th := c.Theme
	for _, want := range []string{"--theme-tile: 84px", "--theme-accent: #ff6600", "--theme-bg: rgb(240 240 240)",
		"--theme-bg-dark: #101010", `--theme-bg-image: url("/icons/bg.jpg")`, `--theme-bg-image-dark: url("https://example.com/night.jpg")`, ".label { font-weight: 700; }"} {
		if !strings.Contains(th.CSS, want) {
			t.Errorf("theme CSS missing %q:\n%s", want, th.CSS)
		}
	}
	if th.LogoURL != dashboardIconsBase+"/svg/keycloak.svg" || th.CSSHash == "" {
		t.Errorf("theme = %+v", th)
	}

	if c, _ := Parse(nil); c.Theme.Mode != "auto" || c.Theme.TileSize != defaultTileSize {
		t.Errorf("defaults: %+v", c.Theme)
	}

	bad := map[string]string{
		"mode":        "theme: {mode: sepia}",
		"tile size":   "theme: {tileSize: 10}",
		"css escape":  "theme: {accent: 'red; } body { display: none'}",
		"url escape":  `theme: {backgroundImage: 'https://x/a")'}`,
		"unknown key": "theme: {colour: red}",
	}
	for name, doc := range bad {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestIDsAndLoginURL(t *testing.T) {
	c, err := Parse([]byte(`
apps:
  - name: Home Assistant
    url: https://hass.example.com
  - name: Grafana
    id: metrics
    url: https://grafana.example.com/
    loginURL: /login/generic_oauth
  - name: Paperless
    url: https://paperless.example.com
    loginURL: https://auth.example.com/start?app=paperless
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ id, href string }{
		{"home-assistant", "https://hass.example.com"},
		{"metrics", "https://grafana.example.com/login/generic_oauth"},
		{"paperless", "https://auth.example.com/start?app=paperless"},
	}
	for i, w := range want {
		if a := c.Apps[i]; a.ID != w.id || a.Href != w.href {
			t.Errorf("app %d: id %q href %q, want %q %q", i, a.ID, a.Href, w.id, w.href)
		}
	}
	if _, ok := c.Lookup("metrics"); !ok {
		t.Error("Lookup by id failed")
	}

	bad := map[string]string{
		"duplicate id": "apps:\n  - {name: A b, url: 'https://a'}\n  - {name: a-B, url: 'https://b'}\n",
		"bad id":       "apps:\n  - {name: A, id: 'Has Space', url: 'https://a'}\n",
		"no slug":      "apps:\n  - {name: '!!!', url: 'https://a'}\n",
		"login url":    "apps:\n  - {name: A, url: 'https://a', loginURL: 'javascript:x'}\n",
	}
	for name, doc := range bad {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
