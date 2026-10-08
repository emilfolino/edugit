// Package web contains the HTTP handlers, HTML templates and static assets
// of the edugit user interface. All assets are embedded in the binary.
package web

import (
	"context"
	"embed"
	"errors"
	"github.com/emilfolino/edugit/internal/hooks"
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
	"github.com/emilfolino/edugit/internal/i18n"
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
	// Assignments enables assignments; it needs Repos and Disk.
	Assignments AssignmentStore
	// Grades enables rubrics and grading; it needs Assignments.
	Grades GradeStore
	// Pulls and PullGit enable pull requests; they need Repos.
	Pulls   PullStore
	PullGit PullGit
	// Issues enables the issue tracker; it needs Pulls (for repo loading).
	Issues IssueStore
	// CI enables the check pages and PR checks; it needs Pulls.
	CI CIStore
	// Browse enables the read-only repository browser; it needs Pulls.
	Browse BrowseGit
	// Editor enables committing from the browser; it needs Browse. Policy
	// and Sink are the same push policy and sink the git hooks use, so an
	// editor commit obeys branch protection and fires the same events as a
	// CLI push.
	Editor EditGit
	Policy hooks.Policy
	Sink   hooks.Sink
	// Sites and SiteDB enable static course sites; they need Repos.
	Sites  SitePublisher
	SiteDB SiteStore
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
	mux.HandleFunc("POST /lang", s.setLang)
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
	L        i18n.Lang
	Langs    []i18n.Lang
	Path     string // current request path, for the language switcher
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
	Teaching      []store.CourseRole // courses where the viewer is teacher or course admin
	Studying      []store.CourseRole // the remaining enrolments
	Course        store.Course
	Site          *store.Site
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
	Assignments   []store.Assignment
	CanAssign     bool
	Asg           *assignmentView
	Grading       *gradingView
	Pulls         *pullsView
	Pull          *pullView
	Issues        *issuesView
	Issue         *issueView
	CI            *ciView
	Browse        *browseView
	Edit          *editView
}

func (s *Server) newPage(r *http.Request, title string) page {
	p := page{Title: title, LoginURL: s.opts.LoginURL, L: i18n.FromRequest(r), Langs: i18n.Supported, Path: r.URL.Path}
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
	return s.requirePostLimit(w, r, 1<<16)
}

// requirePostLimit is requirePost with a custom request body limit.
func (s *Server) requirePostLimit(w http.ResponseWriter, r *http.Request, limit int64) (store.User, bool) {
	u, ok := s.opts.Sessions.User(r)
	if !ok {
		http.Error(w, "sign in required", http.StatusUnauthorized)
		return store.User{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
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

// setLang stores the chosen UI language in a cookie and returns to the page
// the user came from. It needs no session, so it also works before sign-in.
func (s *Server) setLang(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<12)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	l, ok := i18n.Parse(r.PostFormValue("lang"))
	if !ok {
		http.Error(w, "unsupported language", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: i18n.Cookie, Value: string(l), Path: "/",
		MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.opts.PublicURL, "https://"),
	})
	next := r.PostFormValue("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}
