// Package web contains the HTTP handlers, HTML templates and static assets
// of the edugit user interface. All assets are embedded in the binary.
package web

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// TokenStore is the token persistence the account pages need.
type TokenStore interface {
	CreateToken(ctx context.Context, userID int64, name, secretHash string, now, expires time.Time) error
	ListTokens(ctx context.Context, userID int64) ([]store.Token, error)
	RevokeToken(ctx context.Context, userID, id int64) error
}

// Options configures the optional, authenticated part of the UI. With a nil
// Sessions only the public pages are served.
type Options struct {
	Sessions *auth.Sessions
	Tokens   TokenStore
	// Courses and Authz enable the course pages; both are required together
	// with Sessions.
	Courses CourseStore
	Authz   *authz.Authorizer
	// Repos and Disk enable repository management on the course page; both
	// are required together with Courses.
	Repos RepoStore
	Disk  RepoDisk
	// Audit receives security-relevant events and serves the admin viewer;
	// nil disables both.
	Audit Auditor
	// Domains gates which addresses may hold staff roles.
	Domains auth.Domains
	// PublicURL prefixes absolute links such as course invites.
	PublicURL string
	// LoginURL is the sign-in entry point, empty when SSO is not configured.
	LoginURL string
}

// Server serves the web UI.
type Server struct {
	log   *slog.Logger
	pages *template.Template
	opts  Options
}

// New builds a Server with its templates parsed.
func New(log *slog.Logger, opts Options) (*Server, error) {
	pages, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{log: log, pages: pages, opts: opts}, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /{$}", s.index)
	if s.opts.Sessions != nil {
		mux.HandleFunc("GET /account/tokens", s.tokens)
		mux.HandleFunc("POST /account/tokens", s.createToken)
		mux.HandleFunc("POST /account/tokens/{id}/revoke", s.revokeToken)
		mux.HandleFunc("POST /logout", s.logout)
		if s.opts.Courses != nil {
			s.routeCourses(mux)
		}
		if s.opts.Audit != nil && s.opts.Authz != nil {
			mux.HandleFunc("GET /admin/audit", s.auditLog)
		}
	}
	return mux
}

// page is the data passed to every template.
type page struct {
	Title    string
	User     *store.User
	CSRF     string
	LoginURL string
	Tokens   []store.Token
	Secret   string
	Error    string

	Notice  string
	Skipped []string
	// Link is a freshly created invite link, or the invite token on the join page.
	Link          string
	Courses       []store.CourseRole
	Course        store.Course
	Roster        []store.Member
	CanManage     bool
	CanRoster     bool
	InviteExpires time.Time
	CanAudit      bool
	Entries       []store.AuditEntry
	Next          int64
	Filter        string
	Repos         []repoView
	CanCreateRepo bool
}

func (s *Server) newPage(r *http.Request, title string) page {
	p := page{Title: title, LoginURL: s.opts.LoginURL}
	if s.opts.Sessions != nil {
		if u, ok := s.opts.Sessions.User(r); ok {
			p.User = &u
			p.CSRF = s.opts.Sessions.CSRFToken(r)
		}
	}
	return p
}

func (s *Server) render(w http.ResponseWriter, name string, p page, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.pages.ExecuteTemplate(w, name, p); err != nil {
		s.log.Error("render page", "page", name, "err", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if s.opts.Courses != nil {
		if u, ok := s.opts.Sessions.User(r); ok {
			s.dashboard(w, r, u)
			return
		}
	}
	s.render(w, "index.html", s.newPage(r, "edugit"), http.StatusOK)
}

// requireUser returns the signed-in user, or redirects to sign-in and
// returns false.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, ok := s.opts.Sessions.User(r)
	if !ok {
		if s.opts.LoginURL == "" {
			http.Error(w, "sign-in is not configured", http.StatusServiceUnavailable)
		} else {
			http.Redirect(w, r, s.opts.LoginURL+"?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		}
		return store.User{}, false
	}
	return u, true
}

// requirePost authenticates a state-changing request and checks CSRF.
func (s *Server) requirePost(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, ok := s.opts.Sessions.User(r)
	if !ok {
		http.Error(w, "sign in required", http.StatusUnauthorized)
		return store.User{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return store.User{}, false
	}
	if err := s.opts.Sessions.CheckCSRF(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return store.User{}, false
	}
	return u, true
}

func (s *Server) tokens(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.renderTokens(w, r, u, "", "", http.StatusOK)
}

func (s *Server) renderTokens(w http.ResponseWriter, r *http.Request, u store.User, secret, msg string, status int) {
	p := s.newPage(r, "Access tokens")
	list, err := s.opts.Tokens.ListTokens(r.Context(), u.ID)
	if err != nil {
		s.log.Error("list tokens", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	p.Tokens, p.Secret, p.Error = list, secret, msg
	s.render(w, "tokens.html", p, status)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	days, _ := strconv.Atoi(r.PostFormValue("days"))
	if name == "" || len(name) > 80 || days < 1 || days > 365 {
		s.renderTokens(w, r, u, "", "Give the token a name (up to 80 characters) and a valid expiry.", http.StatusBadRequest)
		return
	}
	secret, hash, err := auth.NewSecret(auth.TokenPrefix)
	if err == nil {
		now := time.Now()
		err = s.opts.Tokens.CreateToken(r.Context(), u.ID, name, hash, now, now.AddDate(0, 0, days))
	}
	if err != nil {
		s.log.Error("create token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.audit(r, u, "token.create", u.Username, name)
	s.renderTokens(w, r, u, secret, "", http.StatusOK)
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch err := s.opts.Tokens.RevokeToken(r.Context(), u.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.log.Error("revoke token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	default:
		s.audit(r, u, "token.revoke", u.Username, "id="+itoa(int(id)))
		http.Redirect(w, r, "/account/tokens", http.StatusSeeOther)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	s.audit(r, u, "auth.logout", u.Username, "")
	if err := s.opts.Sessions.End(w, r); err != nil {
		s.log.Error("end session", "err", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
