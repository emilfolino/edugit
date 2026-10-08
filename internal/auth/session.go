package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/emilfolino/edugit/internal/store"
)

const (
	sessionCookie = "edugit_session"
	// SessionCookie is the session cookie name, for rate limiting by session.
	SessionCookie = sessionCookie
	// CSRFField is the form field carrying the CSRF token.
	CSRFField = "csrf"
	// TokenPrefix marks personal access tokens so leaked ones are
	// recognisable by secret scanners.
	TokenPrefix = "edg_"
)

// Backend is the persistence Sessions needs; *store.Store implements it.
type Backend interface {
	CreateSession(ctx context.Context, userID int64, secretHash string, now, expires time.Time) error
	SessionUser(ctx context.Context, secretHash string, now time.Time) (store.User, error)
	DeleteSession(ctx context.Context, secretHash string) error
	TokenUser(ctx context.Context, secretHash string, now time.Time) (store.User, error)
}

// Sessions manages browser sessions, CSRF protection and token
// authentication. It is safe for concurrent use.
type Sessions struct {
	Backend Backend
	// Secure sets the Secure cookie attribute; enable it when served over https.
	Secure bool
	// TTL is the session lifetime. Zero means 12 hours.
	TTL time.Duration
	// Now overrides the clock in tests.
	Now func() time.Time
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sessions) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return 12 * time.Hour
}

// NewSecret returns a random secret with the given prefix and its storage
// hash. Only the hash is ever persisted.
func NewSecret(prefix string) (secret, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate secret: %w", err)
	}
	secret = prefix + base64.RawURLEncoding.EncodeToString(b)
	return secret, HashSecret(secret), nil
}

// HashSecret returns the storage hash of a session or token secret. A fast
// hash is appropriate because secrets are 256 bits of randomness.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Start creates a new session for userID and sets its cookie. A fresh secret
// is issued on every login, so a pre-login cookie can never be fixated.
func (s *Sessions) Start(w http.ResponseWriter, r *http.Request, userID int64) error {
	secret, hash, err := NewSecret("")
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.Backend.CreateSession(r.Context(), userID, hash, now, now.Add(s.ttl())); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	http.SetCookie(w, s.cookie(secret, int(s.ttl().Seconds())))
	return nil
}

// End deletes the request's session and clears the cookie.
func (s *Sessions) End(w http.ResponseWriter, r *http.Request) error {
	http.SetCookie(w, s.cookie("", -1))
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return s.Backend.DeleteSession(r.Context(), HashSecret(c.Value))
}

func (s *Sessions) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// User returns the signed-in user of the request, or false.
func (s *Sessions) User(r *http.Request) (store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return store.User{}, false
	}
	u, err := s.Backend.SessionUser(r.Context(), HashSecret(c.Value), s.now())
	return u, err == nil
}

// CSRFToken returns the CSRF token to embed in forms for the request's
// session. It is derived from the session secret, so it needs no storage.
func (s *Sessions) CSRFToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("edugit-csrf\x00" + c.Value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// CheckCSRF verifies a state-changing request: the form token must match the
// session, and a browser-supplied Origin or Sec-Fetch-Site must not be
// cross-site. Callers parse the form first.
func (s *Sessions) CheckCSRF(r *http.Request) error {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return errors.New("cross-site request")
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return errors.New("foreign origin")
		}
	}
	want := s.CSRFToken(r)
	got := r.PostFormValue(CSRFField)
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return errors.New("bad csrf token")
	}
	return nil
}

// GitUser authenticates a git request by HTTP Basic auth, where the password
// is a personal access token. The username is ignored so that clients may
// send anything. It reports false when credentials are absent or invalid.
func (s *Sessions) GitUser(r *http.Request) (store.User, bool) {
	_, pw, ok := r.BasicAuth()
	if !ok || pw == "" {
		return store.User{}, false
	}
	u, err := s.Backend.TokenUser(r.Context(), HashSecret(pw), s.now())
	return u, err == nil
}

type userKey struct{}

// WithUser returns ctx carrying u.
func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// UserFrom returns the user stored by WithUser.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey{}).(store.User)
	return u, ok
}
