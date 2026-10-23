package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const proxyHint = "No signed-in user was passed to tilegate. It expects to run behind an auth proxy that forwards the user's identity."

// ParseNets parses a comma-separated list of CIDRs or bare IPs.
func ParseNets(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// fromTrusted reports whether the direct peer is one of nets. An empty list
// trusts every peer.
func fromTrusted(r *http.Request, nets []netip.Prefix) bool {
	if len(nets) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, n := range nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// ProxyJWTConfig configures trusting a JWT forwarded by an auth proxy.
type ProxyJWTConfig struct {
	// Header carrying the token; a "Bearer " prefix is stripped.
	Header string
	Issuer string
	// JWKSURL skips discovery when set.
	JWKSURL      string
	DiscoveryURL string
	// Audience, if set, must appear in the token's aud claim.
	Audience    string
	LogoutURL   string
	TrustedNets []netip.Prefix
}

// ProxyJWT verifies a token the proxy forwards on every request, e.g. the
// access token Envoy Gateway passes with forwardAccessToken, or oauth2-proxy's
// --pass-access-token. The signature is checked, so a client that bypasses
// the proxy can't forge an identity.
type ProxyJWT struct {
	cfg   ProxyJWTConfig
	paths ClaimPaths
	fail  ErrorPage
	log   *slog.Logger

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

func NewProxyJWT(cfg ProxyJWTConfig, paths ClaimPaths, fail ErrorPage, log *slog.Logger) (*ProxyJWT, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("proxy-jwt mode requires TILEGATE_PROXY_JWT_ISSUER")
	}
	if cfg.Header == "" {
		cfg.Header = "Authorization"
	}
	return &ProxyJWT{cfg: cfg, paths: paths, fail: fail, log: log}, nil
}

func (p *ProxyJWT) getVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verifier != nil {
		return p.verifier, nil
	}
	vc := &oidc.Config{ClientID: p.cfg.Audience, SkipClientIDCheck: p.cfg.Audience == ""}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if p.cfg.JWKSURL != "" {
		p.verifier = oidc.NewVerifier(p.cfg.Issuer, oidc.NewRemoteKeySet(context.Background(), p.cfg.JWKSURL), vc)
		return p.verifier, nil
	}
	discover := p.cfg.Issuer
	if p.cfg.DiscoveryURL != "" {
		discover = p.cfg.DiscoveryURL
		ctx = oidc.InsecureIssuerURLContext(ctx, p.cfg.Issuer)
	}
	prov, err := oidc.NewProvider(ctx, discover)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	p.verifier = prov.Verifier(vc)
	return p.verifier, nil
}

// Ready reports whether the issuer's keys can be located.
func (p *ProxyJWT) Ready(ctx context.Context) error {
	_, err := p.getVerifier(ctx)
	return err
}

func (p *ProxyJWT) User(r *http.Request) (*User, error) {
	if !fromTrusted(r, p.cfg.TrustedNets) {
		return nil, nil
	}
	raw := strings.TrimSpace(r.Header.Get(p.cfg.Header))
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	if raw == "" {
		return nil, nil
	}
	v, err := p.getVerifier(r.Context())
	if err != nil {
		return nil, err
	}
	tok, err := v.Verify(r.Context(), raw)
	if err != nil {
		p.log.Warn("rejected forwarded token", "err", err, "remote", r.RemoteAddr)
		return nil, nil
	}
	var c map[string]any
	if err := tok.Claims(&c); err != nil {
		return nil, nil
	}
	return userFromClaims(p.paths(), c), nil
}

func (p *ProxyJWT) Challenge(w http.ResponseWriter, r *http.Request) {
	p.fail(w, r, http.StatusUnauthorized, proxyHint)
}

func (p *ProxyJWT) LogoutPath() string { return p.cfg.LogoutURL }

func (p *ProxyJWT) Register(*http.ServeMux) {}

// ProxyHeaderConfig configures trusting plain identity headers.
type ProxyHeaderConfig struct {
	UserHeader   string
	EmailHeader  string
	GroupsHeader string
	LogoutURL    string
	// TrustedNets is required: plain headers are only as trustworthy as the
	// network path they arrive on.
	TrustedNets []netip.Prefix
}

// ProxyHeader trusts unsigned headers such as oauth2-proxy's
// X-Auth-Request-User / X-Auth-Request-Groups, but only from trusted peers.
type ProxyHeader struct {
	cfg   ProxyHeaderConfig
	paths ClaimPaths
	fail  ErrorPage
}

func NewProxyHeader(cfg ProxyHeaderConfig, paths ClaimPaths, fail ErrorPage) (*ProxyHeader, error) {
	if len(cfg.TrustedNets) == 0 {
		return nil, errors.New("proxy-header mode requires TILEGATE_TRUSTED_PROXIES, otherwise anyone who can reach tilegate directly could claim any group")
	}
	if cfg.UserHeader == "" {
		cfg.UserHeader = "X-Forwarded-User"
	}
	if cfg.EmailHeader == "" {
		cfg.EmailHeader = "X-Forwarded-Email"
	}
	if cfg.GroupsHeader == "" {
		cfg.GroupsHeader = "X-Forwarded-Groups"
	}
	return &ProxyHeader{cfg: cfg, paths: paths, fail: fail}, nil
}

func (p *ProxyHeader) User(r *http.Request) (*User, error) {
	if !fromTrusted(r, p.cfg.TrustedNets) {
		return nil, nil
	}
	name := r.Header.Get(p.cfg.UserHeader)
	if name == "" {
		return nil, nil
	}
	var groups []string
	for _, v := range r.Header.Values(p.cfg.GroupsHeader) {
		for _, g := range strings.Split(v, ",") {
			if g = strings.TrimSpace(g); g != "" {
				groups = append(groups, g)
			}
		}
	}
	u := &User{Subject: name, Name: name, Email: r.Header.Get(p.cfg.EmailHeader), Claims: map[string][]string{}}
	// There is only one list of groups, so it stands in for every claim path.
	for _, path := range p.paths() {
		u.Claims[path] = groups
	}
	return u, nil
}

func (p *ProxyHeader) Challenge(w http.ResponseWriter, r *http.Request) {
	p.fail(w, r, http.StatusUnauthorized, proxyHint)
}

func (p *ProxyHeader) LogoutPath() string { return p.cfg.LogoutURL }

func (p *ProxyHeader) Register(*http.ServeMux) {}

// Dev signs everyone in as a fixed user. It exists for trying tilegate out and
// taking screenshots, and must never be exposed.
type Dev struct {
	Groups []string
	paths  ClaimPaths
}

func NewDev(groups []string, paths ClaimPaths) *Dev { return &Dev{Groups: groups, paths: paths} }

func (d *Dev) User(*http.Request) (*User, error) {
	u := &User{Subject: "dev", Name: "Dev User", Email: "dev@example.com", Claims: map[string][]string{}}
	for _, path := range d.paths() {
		u.Claims[path] = d.Groups
	}
	return u, nil
}

func (d *Dev) Challenge(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (d *Dev) LogoutPath() string { return "" }

func (d *Dev) Register(*http.ServeMux) {}
