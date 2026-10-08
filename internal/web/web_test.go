package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/store"
)

func TestHandler(t *testing.T) {
	s, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	tests := []struct {
		path     string
		status   int
		contains string
	}{
		{"/", http.StatusOK, "edugit"},
		{"/healthz", http.StatusOK, "ok"},
		{"/static/style.css", http.StatusOK, "--ink"},
		{"/nope", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
			if rec.Code != tt.status {
				t.Fatalf("status: got %d, want %d", rec.Code, tt.status)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Errorf("body missing %q", tt.contains)
			}
		})
	}
}

func TestAccount(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, err := db.LoginUser(ctx, "oid", "a@bth.se", "A", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.LoginUser(ctx, "oid2", "b@bth.se", "B", false)
	if err != nil {
		t.Fatal(err)
	}
	sess := &auth.Sessions{Backend: db}
	srv, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Sessions: sess, Tokens: db, LoginURL: "/saml/login"})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	rec := httptest.NewRecorder()
	if err := sess.Start(rec, httptest.NewRequest("GET", "/", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	probe := httptest.NewRequest("GET", "/", nil)
	probe.AddCookie(cookie)
	csrf := sess.CSRFToken(probe)

	do := func(method, path string, form url.Values, signedIn bool) *httptest.ResponseRecorder {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if signedIn {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do("GET", "/account/tokens", nil, false); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/saml/login?next=") {
		t.Errorf("anonymous: got %d %q, want redirect to sign-in", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do("POST", "/account/tokens", url.Values{"name": {"x"}, "days": {"30"}, "csrf": {csrf}}, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous create: got %d, want 401", rec.Code)
	}
	if rec := do("POST", "/account/tokens", url.Values{"name": {"x"}, "days": {"30"}}, true); rec.Code != http.StatusForbidden {
		t.Errorf("missing csrf: got %d, want 403", rec.Code)
	}

	rec = do("POST", "/account/tokens", url.Values{"name": {"laptop"}, "days": {"30"}, "csrf": {csrf}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: got %d, want 200", rec.Code)
	}
	m := regexp.MustCompile(`edg_[A-Za-z0-9_-]+`).FindString(rec.Body.String())
	if m == "" {
		t.Fatal("new token not shown")
	}
	if got, err := db.TokenUser(ctx, auth.HashSecret(m), time.Now()); err != nil || got.ID != u.ID {
		t.Errorf("created token does not authenticate: %+v, %v", got, err)
	}
	if rec := do("GET", "/account/tokens", nil, true); strings.Contains(rec.Body.String(), m) || !strings.Contains(rec.Body.String(), "laptop") {
		t.Error("listing must show the name but never the secret")
	}
	if rec := do("POST", "/account/tokens", url.Values{"name": {""}, "days": {"30"}, "csrf": {csrf}}, true); rec.Code != http.StatusBadRequest {
		t.Errorf("empty name: got %d, want 400", rec.Code)
	}

	list, _ := db.ListTokens(ctx, u.ID)
	path := "/account/tokens/" + strconv.FormatInt(list[0].ID, 10) + "/revoke"
	// Another user's session cannot revoke it.
	if err := db.CreateSession(ctx, other.ID, auth.HashSecret("o"), time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeToken(ctx, other.ID, list[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("foreign revoke: got %v, want ErrNotFound", err)
	}
	if rec := do("POST", path, url.Values{"csrf": {csrf}}, true); rec.Code != http.StatusSeeOther {
		t.Errorf("revoke: got %d, want 303", rec.Code)
	}
	if _, err := db.TokenUser(ctx, auth.HashSecret(m), time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("revoked token still valid: %v", err)
	}

	if rec := do("POST", "/logout", url.Values{"csrf": {csrf}}, true); rec.Code != http.StatusSeeOther {
		t.Errorf("logout: got %d, want 303", rec.Code)
	}
	if rec := do("GET", "/account/tokens", nil, true); rec.Code != http.StatusSeeOther {
		t.Errorf("after logout: got %d, want redirect", rec.Code)
	}
}

func TestSetLang(t *testing.T) {
	srv, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	h := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	post := func(form string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/lang", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := post("lang=sv&next=/account/tokens")
	if rec.Code != 303 || rec.Header().Get("Location") != "/account/tokens" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "edugit_lang=sv") {
		t.Fatalf("cookie: %q", rec.Header().Get("Set-Cookie"))
	}
	if loc := post("lang=sv&next=//evil.example").Header().Get("Location"); loc != "/" {
		t.Fatalf("open redirect: %q", loc)
	}
	if post("lang=xx").Code != 400 {
		t.Fatal("unsupported language accepted")
	}
}
