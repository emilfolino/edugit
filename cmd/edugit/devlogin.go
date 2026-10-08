package main

import (
	"log/slog"
	"net/http"
	"net/mail"
	"slices"
	"strings"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/store"
)

const devLoginForm = `<!doctype html><meta charset="utf-8"><title>dev login</title>
<h1>Development sign-in</h1>
<form method="post"><label>Email <input name="email" type="email" required autofocus></label>
<button>Sign in</button></form>`

// devLogin signs in as any email without authentication. It exists only for
// local development (config restricts it to a loopback address) and
// provisions the user like a real login would.
func devLogin(db *store.Store, sessions *auth.Sessions, admins []string, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(devLoginForm))
			return
		}
		email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
			http.Error(w, "invalid email", http.StatusBadRequest)
			return
		}
		u, err := db.LoginUser(r.Context(), "dev:"+email, email, email, slices.Contains(admins, email))
		if err == nil {
			err = sessions.Start(w, r, u.ID)
		}
		if err != nil {
			log.Error("dev login", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}
