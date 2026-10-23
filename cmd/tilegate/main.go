// Command tilegate serves a "My Apps" launcher page that shows each user only
// the apps their roles or groups allow.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chillincool/tilegate/internal/apps"
	"github.com/chillincool/tilegate/internal/auth"
	"github.com/chillincool/tilegate/internal/server"
	"github.com/chillincool/tilegate/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	server.Version = version
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		case "check":
			os.Exit(check(os.Args[2:]))
		case "healthcheck":
			os.Exit(healthcheck())
		default:
			fmt.Fprintf(os.Stderr, "usage: tilegate [version | check [apps.yaml] | healthcheck]\n")
			os.Exit(2)
		}
	}
	log := newLogger()
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// secretEnv reads KEY, or the file named by KEY_FILE (for mounted secrets).
func secretEnv(key string) (string, error) {
	if f := os.Getenv(key + "_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return strings.TrimSpace(string(b)), err
	}
	return os.Getenv(key), nil
}

func list(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, strings.TrimSpace(f))
	}
	return out
}

func duration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func newLogger() *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(env("TILEGATE_LOG_LEVEL", "info")))
	opts := &slog.HandlerOptions{Level: level}
	if env("TILEGATE_LOG_FORMAT", "text") == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func run(log *slog.Logger) error {
	catalog, err := apps.NewStore(env("TILEGATE_APPS_FILE", "/config/apps.yaml"))
	if err != nil {
		return fmt.Errorf("loading apps: %w", err)
	}
	filter := apps.Filter{DefaultClaims: list(env("TILEGATE_ROLES_CLAIM", "groups"))}
	paths := func() []string { return filter.ClaimPaths(catalog.Get()) }

	authn, err := newAuthenticator(env("TILEGATE_AUTH_MODE", "oidc"), paths, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	interval, err := duration("TILEGATE_RELOAD_INTERVAL", 10*time.Second)
	if err != nil {
		return err
	}
	if interval > 0 {
		go catalog.Watch(ctx, interval, log)
	}

	// The database is optional; without it the favourites feature is off.
	var userStore server.UserStore
	dbURL, err := databaseURL()
	if err != nil {
		return err
	}
	if dbURL != "" {
		wait, err := duration("TILEGATE_DATABASE_WAIT", 2*time.Minute)
		if err != nil {
			return err
		}
		db, err := openWithRetry(ctx, dbURL, wait, log)
		if err != nil {
			return err
		}
		defer db.Close()
		userStore = db
		// Name the database type without deriving anything from the URL, which may hold a password.
		kind := "postgres"
		if strings.HasPrefix(dbURL, "sqlite://") {
			kind = "sqlite"
		}
		log.Info("favourites enabled", "database", kind)
	}

	srv := &http.Server{
		Addr: env("TILEGATE_LISTEN", ":8080"),
		Handler: (&server.Server{
			Store:    catalog,
			Filter:   filter,
			Auth:     authn,
			IconsDir: os.Getenv("TILEGATE_ICONS_DIR"),
			Log:      log,
			DB:       userStore,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("tilegate listening", "addr", srv.Addr, "version", version,
		"apps", len(catalog.Get().Apps), "roles_claim", filter.DefaultClaims)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func newAuthenticator(mode string, paths auth.ClaimPaths, log *slog.Logger) (auth.Authenticator, error) {
	trusted, err := auth.ParseNets(os.Getenv("TILEGATE_TRUSTED_PROXIES"))
	if err != nil {
		return nil, fmt.Errorf("TILEGATE_TRUSTED_PROXIES: %w", err)
	}
	switch mode {
	case "oidc":
		secret, err := secretEnv("TILEGATE_OIDC_CLIENT_SECRET")
		if err != nil {
			return nil, err
		}
		sessionSecret, err := secretEnv("TILEGATE_SESSION_SECRET")
		if err != nil {
			return nil, err
		}
		ttl, err := duration("TILEGATE_SESSION_TTL", 8*time.Hour)
		if err != nil {
			return nil, err
		}
		return auth.NewOIDC(auth.OIDCConfig{
			Issuer:                os.Getenv("TILEGATE_OIDC_ISSUER"),
			DiscoveryURL:          os.Getenv("TILEGATE_OIDC_DISCOVERY_URL"),
			ClientID:              os.Getenv("TILEGATE_OIDC_CLIENT_ID"),
			ClientSecret:          secret,
			RedirectURL:           os.Getenv("TILEGATE_OIDC_REDIRECT_URL"),
			PostLogoutRedirectURL: os.Getenv("TILEGATE_OIDC_POST_LOGOUT_REDIRECT_URL"),
			Scopes:                list(env("TILEGATE_OIDC_SCOPES", "openid profile email")),
			SessionSecret:         sessionSecret,
			SessionTTL:            ttl,
		}, paths, server.ErrorPage, log)
	case "proxy-jwt":
		return auth.NewProxyJWT(auth.ProxyJWTConfig{
			Header:       env("TILEGATE_PROXY_JWT_HEADER", "Authorization"),
			Issuer:       os.Getenv("TILEGATE_PROXY_JWT_ISSUER"),
			JWKSURL:      os.Getenv("TILEGATE_PROXY_JWT_JWKS_URL"),
			DiscoveryURL: os.Getenv("TILEGATE_PROXY_JWT_DISCOVERY_URL"),
			Audience:     os.Getenv("TILEGATE_PROXY_JWT_AUDIENCE"),
			LogoutURL:    os.Getenv("TILEGATE_LOGOUT_URL"),
			TrustedNets:  trusted,
		}, paths, server.ErrorPage, log)
	case "proxy-header":
		return auth.NewProxyHeader(auth.ProxyHeaderConfig{
			UserHeader:   os.Getenv("TILEGATE_PROXY_USER_HEADER"),
			EmailHeader:  os.Getenv("TILEGATE_PROXY_EMAIL_HEADER"),
			GroupsHeader: os.Getenv("TILEGATE_PROXY_GROUPS_HEADER"),
			LogoutURL:    os.Getenv("TILEGATE_LOGOUT_URL"),
			TrustedNets:  trusted,
		}, paths, server.ErrorPage)
	case "dev":
		log.Warn("TILEGATE_AUTH_MODE=dev: every visitor is signed in as a test user. Never expose this.")
		return auth.NewDev(list(os.Getenv("TILEGATE_DEV_GROUPS")), paths), nil
	}
	return nil, fmt.Errorf("unknown TILEGATE_AUTH_MODE %q (want oidc, proxy-jwt, proxy-header or dev)", mode)
}

// databaseURL returns TILEGATE_DATABASE_URL, or builds a PostgreSQL URL from
// TILEGATE_DATABASE_HOST and friends, or "" when no database is configured.
func databaseURL() (string, error) {
	if u, err := secretEnv("TILEGATE_DATABASE_URL"); err != nil || u != "" {
		return u, err
	}
	host := os.Getenv("TILEGATE_DATABASE_HOST")
	if host == "" {
		return "", nil
	}
	password, err := secretEnv("TILEGATE_DATABASE_PASSWORD")
	if err != nil {
		return "", err
	}
	user := env("TILEGATE_DATABASE_USER", "tilegate")
	return store.PostgresConfig{
		Host:     host,
		Port:     os.Getenv("TILEGATE_DATABASE_PORT"),
		Database: env("TILEGATE_DATABASE_NAME", user),
		User:     user,
		Password: password,
		SSLMode:  os.Getenv("TILEGATE_DATABASE_SSLMODE"),
	}.URL(), nil
}

// openWithRetry keeps trying to reach the database for up to wait, since it
// often starts at the same time as tilegate (a new CloudNativePG cluster
// takes a minute or so).
func openWithRetry(ctx context.Context, dsn string, wait time.Duration, log *slog.Logger) (*store.Store, error) {
	deadline := time.Now().Add(wait)
	delay := time.Second
	for {
		db, err := store.Open(ctx, dsn)
		if err == nil || time.Now().Add(delay).After(deadline) {
			return db, err
		}
		log.Warn("database not ready yet, retrying", "err", err, "in", delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 10*time.Second)
	}
}

// check validates an apps file, for CI or before applying a ConfigMap.
func check(args []string) int {
	path := env("TILEGATE_APPS_FILE", "/config/apps.yaml")
	if len(args) > 0 {
		path = args[0]
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var c *apps.Config
		if c, err = apps.Parse(data); err == nil {
			fmt.Printf("%s: OK, %d apps\n", path, len(c.Apps))
			return 0
		}
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// healthcheck lets a container HEALTHCHECK work without curl in the image.
func healthcheck() int {
	addr := env("TILEGATE_LISTEN", ":8080")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return 0
		}
		err = errors.New(resp.Status)
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}
