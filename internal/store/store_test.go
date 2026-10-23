package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// backends returns the databases to test against. PostgreSQL runs only when
// TILEGATE_TEST_POSTGRES_URL is set (CI provides one).
func backends(t *testing.T) map[string]string {
	b := map[string]string{"sqlite": "sqlite://" + filepath.Join(t.TempDir(), "sub", "t.db")}
	if pg := os.Getenv("TILEGATE_TEST_POSTGRES_URL"); pg != "" {
		b["postgres"] = pg
	}
	return b
}

func TestFavorites(t *testing.T) {
	for name, url := range backends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.db.Exec(`DELETE FROM favorites`)

			for _, app := range []string{"grafana", "radarr", "grafana", "immich"} {
				if err := s.AddFavorite(ctx, "alice", app); err != nil {
					t.Fatal(err)
				}
			}
			s.AddFavorite(ctx, "bob", "jellyfin")
			if err := s.RemoveFavorite(ctx, "alice", "radarr"); err != nil {
				t.Fatal(err)
			}
			got, err := s.Favorites(ctx, "alice")
			if err != nil || !reflect.DeepEqual(got, []string{"grafana", "immich"}) {
				t.Errorf("alice favourites = %v, %v", got, err)
			}
			if got, _ := s.Favorites(ctx, "carol"); len(got) != 0 {
				t.Errorf("carol favourites = %v", got)
			}

			// Reopening must not re-run migrations or lose data.
			s.Close()
			s2, err := Open(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer s2.Close()
			if got, _ := s2.Favorites(ctx, "bob"); !reflect.DeepEqual(got, []string{"jellyfin"}) {
				t.Errorf("after reopen bob = %v", got)
			}
		})
	}
}

func TestOpenRejectsUnknownScheme(t *testing.T) {
	if _, err := Open(context.Background(), "mysql://x"); err == nil {
		t.Error("expected error")
	}
}

func TestPlaceholders(t *testing.T) {
	pg := &Store{postgres: true}
	if got := pg.q("a = ? AND b = ?"); got != "a = $1 AND b = $2" {
		t.Errorf("q = %q", got)
	}
}

func TestTheme(t *testing.T) {
	for name, url := range backends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.db.Exec(`DELETE FROM preferences`)

			if got, err := s.Theme(ctx, "alice"); got != "" || err != nil {
				t.Errorf("unset theme = %q, %v", got, err)
			}
			s.SetTheme(ctx, "alice", "dark")
			s.SetTheme(ctx, "alice", "light")
			if got, _ := s.Theme(ctx, "alice"); got != "light" {
				t.Errorf("theme = %q, want light", got)
			}
		})
	}
}

func TestPostgresURL(t *testing.T) {
	got := PostgresConfig{Host: "tilegate-db-rw", Database: "tilegate", User: "tilegate", Password: "p@ss:w/rd?#", SSLMode: "require"}.URL()
	want := "postgres://tilegate:p%40ss%3Aw%2Frd%3F%23@tilegate-db-rw:5432/tilegate?sslmode=require"
	if got != want {
		t.Errorf("URL() =\n %s\nwant\n %s", got, want)
	}
	if got := (PostgresConfig{Host: "fd00::1", Port: "6432", Database: "x", User: "u"}).URL(); got != "postgres://u:@[fd00::1]:6432/x" {
		t.Errorf("IPv6 URL = %s", got)
	}
}
