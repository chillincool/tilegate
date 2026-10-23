package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func failPage(w http.ResponseWriter, _ *http.Request, status int, msg string) {
	http.Error(w, msg, status)
}

func paths(p ...string) ClaimPaths { return func() []string { return p } }

// fakeIdP is just enough of an OIDC provider to drive the code flow.
type fakeIdP struct {
	*httptest.Server
	key      *rsa.PrivateKey
	clientID string
	// claims added to every token
	idClaims, atClaims map[string]any
	// what /authorize received, checked by the token endpoint
	nonce, challenge string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key, clientID: "tilegate"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.URL,
			"authorization_endpoint":                f.URL + "/authorize",
			"token_endpoint":                        f.URL + "/token",
			"jwks_uri":                              f.URL + "/jwks",
			"end_session_endpoint":                  f.URL + "/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.nonce, f.challenge = q.Get("nonce"), q.Get("code_challenge")
		if q.Get("code_challenge_method") != "S256" {
			http.Error(w, "pkce required", 400)
			return
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=c0de&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "c0de" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		id := map[string]any{"aud": f.clientID, "nonce": f.nonce}
		for k, v := range f.idClaims {
			id[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": f.sign(t, f.atClaims),
			"id_token":     f.sign(t, id),
			"token_type":   "Bearer",
			"expires_in":   300,
		})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIdP) sign(t *testing.T, c map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithHeader("kid", "k1").WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{"iss": f.URL, "sub": "u1", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
	for k, v := range c {
		all[k] = v
	}
	b, _ := json.Marshal(all)
	obj, err := signer.Sign(b)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := obj.CompactSerialize()
	return s
}

func TestSealer(t *testing.T) {
	s, err := newSealer(strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.seal("a", map[string]string{"k": "v"}, time.Minute)
	var got map[string]string
	if err := s.open("a", v, &got); err != nil || got["k"] != "v" {
		t.Fatalf("roundtrip: %v %v", got, err)
	}
	if err := s.open("b", v, &got); err == nil {
		t.Error("value sealed for cookie a was accepted as cookie b")
	}
	other, _ := newSealer(strings.Repeat("y", 32))
	if err := other.open("a", v, &got); err == nil {
		t.Error("value accepted under a different secret")
	}
	old, _ := s.seal("a", 1, -time.Second)
	if err := s.open("a", old, &got); err != errExpired {
		t.Errorf("expired value: %v", err)
	}
	if _, err := newSealer("short"); err == nil {
		t.Error("short secret accepted")
	}
}

func TestOIDCFlow(t *testing.T) {
	idp := newFakeIdP(t)
	idp.idClaims = map[string]any{"name": "Alice", "email": "a@example.com", "groups": []string{"family"}}
	idp.atClaims = map[string]any{"aud": "account", "realm_access": map[string]any{"roles": []string{"admins"}}}

	mux := http.NewServeMux()
	app := httptest.NewServer(mux)
	defer app.Close()

	o, err := NewOIDC(OIDCConfig{
		Issuer:        idp.URL,
		ClientID:      idp.clientID,
		RedirectURL:   app.URL + "/auth/callback",
		Scopes:        []string{"openid"},
		SessionSecret: strings.Repeat("s", 32),
		SessionTTL:    time.Hour,
	}, paths("groups", "realm_access.roles"), failPage, discard)
	if err != nil {
		t.Fatal(err)
	}
	o.Register(mux)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		u, _ := o.User(r)
		if u == nil {
			o.Challenge(w, r)
			return
		}
		json.NewEncoder(w).Encode(u)
	})

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(app.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "Alice" || u.Subject != "u1" {
		t.Errorf("user = %+v", u)
	}
	if !reflect.DeepEqual(u.Claims["groups"], []string{"family"}) || !reflect.DeepEqual(u.Claims["realm_access.roles"], []string{"admins"}) {
		t.Errorf("claims = %v", u.Claims)
	}

	// Logout clears the session and sends the browser to the IdP.
	noFollow := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noFollow.Get(app.URL + "/logout")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, idp.URL+"/logout?") || !strings.Contains(loc, "client_id=tilegate") {
		t.Errorf("logout redirect = %q", loc)
	}
	resp, _ = noFollow.Get(app.URL + "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("after logout status = %d, want redirect to login", resp.StatusCode)
	}
}

func TestOIDCRejectsForgedState(t *testing.T) {
	idp := newFakeIdP(t)
	o, err := NewOIDC(OIDCConfig{
		Issuer: idp.URL, ClientID: "tilegate", RedirectURL: "http://app/auth/callback",
		SessionSecret: strings.Repeat("s", 32), SessionTTL: time.Hour,
	}, paths("groups"), failPage, discard)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	o.callback(w, httptest.NewRequest("GET", "/auth/callback?code=c0de&state=forged", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/auth/login" {
		t.Errorf("forged state: %d %s", w.Code, w.Header().Get("Location"))
	}
	if strings.Contains(w.Header().Get("Set-Cookie"), sessionCookie) {
		t.Error("session issued for forged state")
	}
}

func TestProxyJWT(t *testing.T) {
	idp := newFakeIdP(t)
	nets, _ := ParseNets("10.0.0.0/8")
	p, err := NewProxyJWT(ProxyJWTConfig{Issuer: idp.URL, TrustedNets: nets}, paths("groups"), failPage, discard)
	if err != nil {
		t.Fatal(err)
	}
	good := idp.sign(t, map[string]any{"preferred_username": "bob", "groups": []string{"admins"}})

	req := func(remote, auth string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		return r
	}
	u, err := p.User(req("10.1.2.3:5555", "Bearer "+good))
	if err != nil || u == nil || u.Name != "bob" || u.Claims["groups"][0] != "admins" {
		t.Fatalf("valid token: %+v %v", u, err)
	}
	if u, _ := p.User(req("192.168.1.1:5555", "Bearer "+good)); u != nil {
		t.Error("token accepted from untrusted peer")
	}
	// Re-sign the payload with a different key: must be rejected.
	evil := newFakeIdP(t)
	evil.Close()
	evil.URL = idp.URL
	forged := evil.sign(t, map[string]any{"groups": []string{"admins"}})
	if u, _ := p.User(req("10.1.2.3:5555", "Bearer "+forged)); u != nil {
		t.Error("forged token accepted")
	}
	if u, _ := p.User(req("10.1.2.3:5555", "")); u != nil {
		t.Error("missing token accepted")
	}
}

func TestProxyHeader(t *testing.T) {
	if _, err := NewProxyHeader(ProxyHeaderConfig{}, paths("groups"), failPage); err == nil {
		t.Fatal("proxy-header without trusted proxies must be refused")
	}
	nets, _ := ParseNets("127.0.0.1, fd00::/8")
	p, _ := NewProxyHeader(ProxyHeaderConfig{TrustedNets: nets}, paths("groups"), failPage)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-User", "carol")
	r.Header.Set("X-Forwarded-Groups", "a, b")
	u, _ := p.User(r)
	if u == nil || !reflect.DeepEqual(u.Claims["groups"], []string{"a", "b"}) {
		t.Fatalf("user = %+v", u)
	}
	r.RemoteAddr = "[fd12::1]:1234"
	if u, _ := p.User(r); u == nil {
		t.Error("ipv6 trusted peer rejected")
	}
	r.RemoteAddr = "8.8.8.8:1234"
	if u, _ := p.User(r); u != nil {
		t.Error("untrusted peer accepted")
	}
}
