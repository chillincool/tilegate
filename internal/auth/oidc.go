package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	sessionCookie = "tilegate_session"
	loginCookie   = "tilegate_login"
	loginTTL      = 10 * time.Minute
	// Browsers drop cookies larger than about 4KB.
	maxCookieSize = 4000
)

// OIDCConfig configures the built-in login.
type OIDCConfig struct {
	Issuer string
	// DiscoveryURL, if set, is where the provider metadata is fetched from
	// instead of Issuer. Use it when tilegate reaches the IdP on a different
	// address than browsers do (an in-cluster Service, or docker-compose).
	DiscoveryURL          string
	ClientID              string
	ClientSecret          string
	RedirectURL           string
	PostLogoutRedirectURL string
	Scopes                []string
	SessionSecret         string
	SessionTTL            time.Duration
}

// OIDC runs the authorization code flow (with PKCE) and keeps the result in
// an encrypted session cookie.
type OIDC struct {
	cfg   OIDCConfig
	jar   cookieJar
	paths ClaimPaths
	fail  ErrorPage
	log   *slog.Logger

	mu       sync.Mutex
	provider *oidcProvider
}

type oidcProvider struct {
	oauth2     oauth2.Config
	idVerifier *oidc.IDTokenVerifier
	atVerifier *oidc.IDTokenVerifier
	endSession string
}

type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
}

// NewOIDC validates the config. The IdP is contacted lazily, so tilegate can
// start before the IdP is reachable.
func NewOIDC(cfg OIDCConfig, paths ClaimPaths, fail ErrorPage, log *slog.Logger) (*OIDC, error) {
	var missing []string
	for name, v := range map[string]string{
		"TILEGATE_OIDC_ISSUER":       cfg.Issuer,
		"TILEGATE_OIDC_CLIENT_ID":    cfg.ClientID,
		"TILEGATE_OIDC_REDIRECT_URL": cfg.RedirectURL,
		"TILEGATE_SESSION_SECRET":    cfg.SessionSecret,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("oidc mode requires %s", strings.Join(missing, ", "))
	}
	redirect, err := url.Parse(cfg.RedirectURL)
	if err != nil || redirect.Host == "" {
		return nil, fmt.Errorf("invalid redirect URL %q", cfg.RedirectURL)
	}
	if redirect.Path != "/auth/callback" {
		return nil, fmt.Errorf("redirect URL path must be /auth/callback, got %q", redirect.Path)
	}
	if cfg.PostLogoutRedirectURL == "" {
		cfg.PostLogoutRedirectURL = redirect.Scheme + "://" + redirect.Host + "/"
	}
	s, err := newSealer(cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	return &OIDC{
		cfg:   cfg,
		jar:   cookieJar{sealer: s, secure: redirect.Scheme == "https"},
		paths: paths,
		fail:  fail,
		log:   log,
	}, nil
}

func (o *OIDC) getProvider(ctx context.Context) (*oidcProvider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.provider, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	discover := o.cfg.Issuer
	if o.cfg.DiscoveryURL != "" {
		discover = o.cfg.DiscoveryURL
		ctx = oidc.InsecureIssuerURLContext(ctx, o.cfg.Issuer)
	}
	p, err := oidc.NewProvider(ctx, discover)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&meta)
	o.provider = &oidcProvider{
		oauth2: oauth2.Config{
			ClientID:     o.cfg.ClientID,
			ClientSecret: o.cfg.ClientSecret,
			RedirectURL:  o.cfg.RedirectURL,
			Endpoint:     p.Endpoint(),
			Scopes:       o.cfg.Scopes,
		},
		idVerifier: p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID}),
		// Access tokens are usually issued for other audiences (Keycloak uses
		// "account"), so only the signature, issuer and expiry are checked.
		atVerifier: p.Verifier(&oidc.Config{SkipClientIDCheck: true}),
		endSession: meta.EndSession,
	}
	o.log.Info("oidc provider ready", "issuer", o.cfg.Issuer)
	return o.provider, nil
}

// Ready reports whether the IdP can be reached; used by the readiness probe.
func (o *OIDC) Ready(ctx context.Context) error {
	_, err := o.getProvider(ctx)
	return err
}

func (o *OIDC) User(r *http.Request) (*User, error) {
	var u User
	switch err := o.jar.get(r, sessionCookie, &u); {
	case err == nil:
		return &u, nil
	case errors.Is(err, http.ErrNoCookie), errors.Is(err, errExpired):
		return nil, nil
	default:
		// Wrong key (secret rotated) or tampering: treat as signed out.
		o.log.Debug("discarding unreadable session cookie", "err", err)
		return nil, nil
	}
}

func (o *OIDC) Challenge(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (o *OIDC) LogoutPath() string { return "/logout" }

func (o *OIDC) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", o.login)
	mux.HandleFunc("GET /auth/callback", o.callback)
	mux.HandleFunc("GET /logout", o.logout)
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (o *OIDC) login(w http.ResponseWriter, r *http.Request) {
	p, err := o.getProvider(r.Context())
	if err != nil {
		o.log.Error("login unavailable", "err", err)
		o.fail(w, r, http.StatusServiceUnavailable, "The identity provider can't be reached right now. Try again shortly.")
		return
	}
	st := loginState{State: randomString(), Nonce: randomString(), Verifier: oauth2.GenerateVerifier()}
	if _, err := o.jar.set(w, loginCookie, "/auth/", st, loginTTL); err != nil {
		o.fail(w, r, http.StatusInternalServerError, "Couldn't start sign-in.")
		return
	}
	http.Redirect(w, r, p.oauth2.AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

func (o *OIDC) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		e = logSafe(e)
		o.log.Warn("idp returned error", "error", e, "description", logSafe(q.Get("error_description")))
		o.fail(w, r, http.StatusForbidden, "Sign-in was not completed: "+e)
		return
	}
	var st loginState
	if err := o.jar.get(r, loginCookie, &st); err != nil || st.State == "" || q.Get("state") != st.State {
		// Most often a stale tab or a bookmarked callback URL; just start over.
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	o.jar.clear(w, loginCookie, "/auth/")

	p, err := o.getProvider(r.Context())
	if err != nil {
		o.fail(w, r, http.StatusServiceUnavailable, "The identity provider can't be reached right now.")
		return
	}
	ctx := r.Context()
	if o.cfg.DiscoveryURL != "" {
		ctx = oidc.InsecureIssuerURLContext(ctx, o.cfg.Issuer)
	}
	tok, err := p.oauth2.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		o.log.Error("code exchange failed", "err", err)
		o.fail(w, r, http.StatusBadGateway, "Couldn't complete sign-in with the identity provider.")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idt, err := p.idVerifier.Verify(ctx, rawID)
	if err != nil {
		o.log.Error("id token rejected", "err", err)
		o.fail(w, r, http.StatusForbidden, "The identity provider returned an invalid token.")
		return
	}
	if idt.Nonce != st.Nonce {
		o.fail(w, r, http.StatusForbidden, "The identity provider returned an invalid token.")
		return
	}
	var idClaims, atClaims map[string]any
	if err := idt.Claims(&idClaims); err != nil {
		o.fail(w, r, http.StatusForbidden, "The identity provider returned an invalid token.")
		return
	}
	// Keycloak puts realm_access/resource_access roles in the access token by
	// default, so read it too when it is a JWT we can verify.
	if at, err := p.atVerifier.Verify(ctx, tok.AccessToken); err == nil {
		_ = at.Claims(&atClaims)
	}

	u := userFromClaims(o.paths(), idClaims, atClaims)
	n, err := o.jar.set(w, sessionCookie, "/", u, o.cfg.SessionTTL)
	if err != nil {
		o.fail(w, r, http.StatusInternalServerError, "Couldn't save the session.")
		return
	}
	if n > maxCookieSize {
		o.log.Warn("session cookie is larger than browsers accept; narrow the roles claim or the user's groups",
			"bytes", n, "user", u.Subject)
	}
	o.log.Info("user signed in", "sub", u.Subject, "name", u.Name)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (o *OIDC) logout(w http.ResponseWriter, r *http.Request) {
	o.jar.clear(w, sessionCookie, "/")
	p, err := o.getProvider(r.Context())
	if err != nil || p.endSession == "" {
		http.Redirect(w, r, o.cfg.PostLogoutRedirectURL, http.StatusFound)
		return
	}
	u, err := url.Parse(p.endSession)
	if err != nil {
		http.Redirect(w, r, o.cfg.PostLogoutRedirectURL, http.StatusFound)
		return
	}
	v := u.Query()
	v.Set("client_id", o.cfg.ClientID)
	v.Set("post_logout_redirect_uri", o.cfg.PostLogoutRedirectURL)
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}
