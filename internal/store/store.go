// Package store persists per-user data (favourites and preferences) in
// SQLite or PostgreSQL.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Store is a database holding per-user data.
type Store struct {
	db       *sql.DB
	postgres bool
}

// Open connects to the database named by dsn and applies any pending
// migrations. Supported forms:
//
//	sqlite:///data/tilegate.db   (absolute path)
//	sqlite://tilegate.db         (relative to the working directory)
//	postgres://user:pass@host:5432/db?sslmode=require
func Open(ctx context.Context, dsn string) (*Store, error) {
	var s Store
	var err error
	switch {
	case strings.HasPrefix(dsn, "sqlite://"):
		path := strings.TrimPrefix(dsn, "sqlite://")
		if path == "" {
			return nil, errors.New("sqlite URL needs a file path, e.g. sqlite:///data/tilegate.db")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, err
		}
		s.db, err = sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
		// One connection serialises writes, which avoids SQLITE_BUSY; the
		// load here is a few small queries per page view.
		s.db.SetMaxOpenConns(1)
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		s.postgres = true
		s.db, err = sql.Open("pgx", dsn)
		s.db.SetMaxOpenConns(5)
	default:
		return nil, errors.New("database URL must start with sqlite:// or postgres://")
	}
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.db.PingContext(pingCtx); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	if err := s.migrate(ctx); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	return &s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database is reachable; used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// migrations are applied in order and never edited once released; add new
// ones to the end. Each must work on both SQLite and PostgreSQL.
var migrations = []string{
	`CREATE TABLE favorites (
		user_id    TEXT   NOT NULL,
		app_id     TEXT   NOT NULL,
		created_at BIGINT NOT NULL,
		PRIMARY KEY (user_id, app_id)
	)`,
	`CREATE TABLE preferences (
		user_id TEXT NOT NULL PRIMARY KEY,
		theme   TEXT NOT NULL DEFAULT ''
	)`,
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.postgres {
		// Several replicas may start at once; only one migrates at a time.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7461676174)`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tilegate_schema (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM tilegate_schema`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO tilegate_schema (version) VALUES (0)`); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this tilegate (%d); upgrade tilegate", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE tilegate_schema SET version = ?`), len(migrations)); err != nil {
		return err
	}
	return tx.Commit()
}

// q rewrites ? placeholders to $1, $2, … for PostgreSQL.
func (s *Store) q(query string) string {
	if !s.postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Favorites returns the user's favourite app ids, oldest first.
func (s *Store) Favorites(ctx context.Context, user string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT app_id FROM favorites WHERE user_id = ? ORDER BY created_at, app_id`), user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AddFavorite adds an app to the user's favourites; adding one twice is a no-op.
func (s *Store) AddFavorite(ctx context.Context, user, app string) error {
	_, err := s.db.ExecContext(ctx,
		s.q(`INSERT INTO favorites (user_id, app_id, created_at) VALUES (?, ?, ?) ON CONFLICT (user_id, app_id) DO NOTHING`),
		user, app, time.Now().UnixMicro())
	return err
}

// RemoveFavorite removes an app from the user's favourites.
func (s *Store) RemoveFavorite(ctx context.Context, user, app string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM favorites WHERE user_id = ? AND app_id = ?`), user, app)
	return err
}

// Theme returns the user's saved theme choice, or "" if they haven't made one.
func (s *Store) Theme(ctx context.Context, user string) (string, error) {
	var theme string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT theme FROM preferences WHERE user_id = ?`), user).Scan(&theme)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return theme, err
}

// SetTheme saves the user's theme choice.
func (s *Store) SetTheme(ctx context.Context, user, theme string) error {
	_, err := s.db.ExecContext(ctx,
		s.q(`INSERT INTO preferences (user_id, theme) VALUES (?, ?) ON CONFLICT (user_id) DO UPDATE SET theme = excluded.theme`),
		user, theme)
	return err
}

// PostgresConfig describes a PostgreSQL connection as separate fields, the way
// most Kubernetes setups (including CloudNativePG) hand out credentials.
type PostgresConfig struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
	// SSLMode is passed through as sslmode (disable, require, verify-full, …).
	SSLMode string
}

// URL builds a postgres:// URL, escaping the user and password.
func (c PostgresConfig) URL() string {
	port := c.Port
	if port == "" {
		port = "5432"
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   net.JoinHostPort(c.Host, port),
		Path:   "/" + c.Database,
	}
	if c.SSLMode != "" {
		u.RawQuery = url.Values{"sslmode": {c.SSLMode}}.Encode()
	}
	return u.String()
}
