package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// sealer encrypts and authenticates small values for storage in cookies, so
// sessions need no server-side store and survive restarts and multiple replicas.
type sealer struct {
	aead cipher.AEAD
}

func newSealer(secret string) (*sealer, error) {
	if len(secret) < 32 {
		return nil, errors.New("session secret must be at least 32 characters")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

type envelope struct {
	Exp  int64           `json:"exp"`
	Data json.RawMessage `json:"d"`
}

// seal encodes v with an expiry. The cookie name is bound in as additional
// data so a value sealed for one cookie can't be replayed as another.
func (s *sealer) seal(name string, v any, ttl time.Duration) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(envelope{Exp: time.Now().Add(ttl).Unix(), Data: data})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, plain, []byte(name))
	return base64.RawURLEncoding.EncodeToString(out), nil
}

var errExpired = errors.New("expired")

func (s *sealer) open(name, value string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return errors.New("short value")
	}
	plain, err := s.aead.Open(nil, raw[:ns], raw[ns:], []byte(name))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(plain, &env); err != nil {
		return err
	}
	if time.Now().Unix() > env.Exp {
		return errExpired
	}
	return json.Unmarshal(env.Data, v)
}

// cookieJar reads and writes sealed cookies with consistent attributes.
type cookieJar struct {
	sealer *sealer
	secure bool
}

func (j cookieJar) set(w http.ResponseWriter, name, path string, v any, ttl time.Duration) (int, error) {
	val, err := j.sealer.seal(name, v, ttl)
	if err != nil {
		return 0, err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    val,
		Path:     path,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   j.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return len(val), nil
}

func (j cookieJar) get(r *http.Request, name string, v any) error {
	c, err := r.Cookie(name)
	if err != nil {
		return err
	}
	return j.sealer.open(name, c.Value, v)
}

func (j cookieJar) clear(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   j.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
