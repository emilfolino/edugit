package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/ratelimit"
)

// Request budgets per minute. Sign-in is keyed by client address (a campus NAT
// shares one, so the budget is generous); other writes by session, so one
// student's script cannot lock out a classroom.
const (
	loginBudget = 60
	writeBudget = 300
)

// RateLimit throttles sign-in endpoints and state-changing requests with 429.
// Git smart-HTTP (/git/) is excluded: git clients make several POSTs per push
// and have their own limits.
func RateLimit(next http.Handler, trustProxy bool) http.Handler {
	logins := &ratelimit.Failures{Max: loginBudget, Window: time.Minute}
	writes := &ratelimit.Failures{Max: writeBudget, Window: time.Minute}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var l *ratelimit.Failures
		var key string
		switch {
		case strings.HasPrefix(r.URL.Path, "/saml/") || r.URL.Path == "/dev/login":
			l, key = logins, ratelimit.ClientIP(r, trustProxy)
		case r.Method == http.MethodPost && !strings.HasPrefix(r.URL.Path, "/git/"):
			l = writes
			if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" {
				key = "s:" + auth.HashSecret(c.Value)
			} else {
				key = "ip:" + ratelimit.ClientIP(r, trustProxy)
			}
		default:
			next.ServeHTTP(w, r)
			return
		}
		if l.Blocked(key) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		l.Fail(key) // counts every request, not just failures
		next.ServeHTTP(w, r)
	})
}
