package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilfolino/edugit/internal/auth"
)

func TestRateLimit(t *testing.T) {
	h := RateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), false)
	do := func(method, path, session string) int {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "10.0.0.1:1"
		if session != "" {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: session})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < writeBudget; i++ {
		if got := do("POST", "/courses", "alice"); got != 200 {
			t.Fatalf("request %d: %d", i, got)
		}
	}
	if got := do("POST", "/courses", "alice"); got != 429 {
		t.Errorf("over budget: %d", got)
	}
	if got := do("POST", "/courses", "bob"); got != 200 {
		t.Errorf("another session throttled: %d", got)
	}
	if got := do("GET", "/courses", "alice"); got != 200 {
		t.Errorf("reads must not be throttled: %d", got)
	}
	if got := do("POST", "/git/oop/r.git/git-upload-pack", "alice"); got != 200 {
		t.Errorf("git must not be throttled: %d", got)
	}
	for i := 0; i < loginBudget; i++ {
		do("GET", "/saml/login", "")
	}
	if got := do("GET", "/saml/login", ""); got != 429 {
		t.Errorf("login over budget: %d", got)
	}
	if got := do("POST", "/dev/login", ""); got != 429 {
		t.Errorf("dev login shares the sign-in budget: %d", got)
	}
}
