package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecureHeaders(t *testing.T) {
	h := SecureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/site" {
			w.Header().Set("Content-Security-Policy", "sandbox")
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") ||
		rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", rec.Header())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/site", nil))
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox" {
		t.Errorf("handler override lost: %q", got)
	}
}
