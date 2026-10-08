package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emilfolino/edugit/internal/store"
)

func newSessions(t *testing.T) (*Sessions, *store.Store, store.User) {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u, err := db.LoginUser(context.Background(), "oid", "a@bth.se", "A", false)
	if err != nil {
		t.Fatal(err)
	}
	return &Sessions{Backend: db, Secure: true}, db, u
}

func TestSessions_lifecycle(t *testing.T) {
	s, _, u := newSessions(t)
	rec := httptest.NewRecorder()
	if err := s.Start(rec, httptest.NewRequest("GET", "/", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	cs := rec.Result().Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly || !cs[0].Secure || cs[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes: got %+v", cs)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cs[0])
	if got, ok := s.User(req); !ok || got.ID != u.ID {
		t.Fatalf("User: got %+v, %v", got, ok)
	}

	if err := s.End(httptest.NewRecorder(), req); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.User(req); ok {
		t.Error("session still valid after End")
	}

	if _, ok := s.User(httptest.NewRequest("GET", "/", nil)); ok {
		t.Error("anonymous request reported as signed in")
	}
}

func TestSessions_expiry(t *testing.T) {
	s, _, u := newSessions(t)
	now := time.Now()
	s.Now = func() time.Time { return now }
	rec := httptest.NewRecorder()
	if err := s.Start(rec, httptest.NewRequest("GET", "/", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	now = now.Add(13 * time.Hour)
	if _, ok := s.User(req); ok {
		t.Error("expired session accepted")
	}
}

func TestSessions_CheckCSRF(t *testing.T) {
	s, _, u := newSessions(t)
	rec := httptest.NewRecorder()
	if err := s.Start(rec, httptest.NewRequest("GET", "/", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	probe := httptest.NewRequest("GET", "/", nil)
	probe.AddCookie(cookie)
	good := s.CSRFToken(probe)

	tests := []struct {
		name    string
		token   string
		headers map[string]string
		cookie  bool
		wantErr bool
	}{
		{"valid", good, nil, true, false},
		{"valid same-origin", good, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://example.com"}, true, false},
		{"wrong token", "x", nil, true, true},
		{"missing token", "", nil, true, true},
		{"no session", good, nil, false, true},
		{"cross-site fetch", good, map[string]string{"Sec-Fetch-Site": "cross-site"}, true, true},
		{"foreign origin", good, map[string]string{"Origin": "https://evil.example"}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{CSRFField: {tt.token}}
			req := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			if tt.cookie {
				req.AddCookie(cookie)
			}
			if err := s.CheckCSRF(req); (err != nil) != tt.wantErr {
				t.Errorf("got %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSessions_GitUser(t *testing.T) {
	s, db, u := newSessions(t)
	secret, hash, err := NewSecret(TokenPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix) {
		t.Errorf("secret %q lacks prefix", secret)
	}
	if err := db.CreateToken(context.Background(), u.ID, "t", hash, time.Now(), time.Time{}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("anything", secret)
	if got, ok := s.GitUser(req); !ok || got.ID != u.ID {
		t.Errorf("valid token: got %+v, %v", got, ok)
	}
	req.SetBasicAuth("anything", secret+"x")
	if _, ok := s.GitUser(req); ok {
		t.Error("wrong token accepted")
	}
	if _, ok := s.GitUser(httptest.NewRequest("GET", "/", nil)); ok {
		t.Error("missing credentials accepted")
	}
}
