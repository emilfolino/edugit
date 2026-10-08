package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/sites"
	"github.com/emilfolino/edugit/internal/store"
)

// SiteStore persists the per-course site configuration.
type SiteStore interface {
	SetSite(ctx context.Context, courseID, repoID int64, branch, dir string) error
	ClearSite(ctx context.Context, courseID int64) error
	SiteByCourse(ctx context.Context, slug string) (store.Site, error)
}

// SitePublisher builds and serves the published sites. *sites.Publisher
// implements it.
type SitePublisher interface {
	Publish(ctx context.Context, course, repo, branch, dir string) error
	Remove(course string) error
	Handler(course, prefix string) (http.Handler, error)
}

func (s *Server) routeSites(mux *http.ServeMux) {
	mux.HandleFunc("POST /courses/{slug}/site", s.setSite)
	mux.HandleFunc("GET /sites/{slug}/{path...}", s.serveSite)
}

// serveSite serves the course site to anyone who can view the course.
func (s *Server) serveSite(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	c, _, ok := s.loadCourse(w, r, u, authz.CourseView)
	if !ok {
		return
	}
	h, err := s.opts.Sites.Handler(c.Slug, "/sites/"+c.Slug)
	if errors.Is(err, sites.ErrNoSite) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "serve site", err)
		return
	}
	// The site is user-authored content: keep it from running with the
	// app's origin privileges.
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-popups allow-forms allow-downloads")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	h.ServeHTTP(w, r)
}

// setSite configures (or, with an empty repo, removes) the course site and
// publishes it immediately.
func (s *Server) setSite(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, _, ok := s.loadCourse(w, r, u, authz.CourseManage)
	if !ok {
		return
	}
	back := "/courses/" + c.Slug
	name := strings.TrimSpace(r.PostFormValue("repo"))
	if name == "" {
		if err := s.opts.SiteDB.ClearSite(r.Context(), c.ID); err != nil {
			s.fail(w, "clear site", err)
			return
		}
		if err := s.opts.Sites.Remove(c.Slug); err != nil {
			s.fail(w, "remove site", err)
			return
		}
		s.audit(r, u, "course.site_off", c.Slug, "")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	repo, _, err := s.opts.Repos.RepoByName(r.Context(), c.Slug, name, u.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && repo.Kind != "teacher") {
		http.Error(w, "the site must come from a teacher repository", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.fail(w, "site repo", err)
		return
	}
	branch := strings.TrimSpace(r.PostFormValue("branch"))
	if branch == "" {
		branch = "main"
	}
	dir := strings.Trim(strings.TrimSpace(r.PostFormValue("dir")), "/")
	if err := s.opts.SiteDB.SetSite(r.Context(), c.ID, repo.ID, branch, dir); err != nil {
		s.fail(w, "set site", err)
		return
	}
	s.audit(r, u, "course.site_on", c.Slug, "repo="+name+" branch="+branch+" dir="+dir)
	if err := s.opts.Sites.Publish(r.Context(), c.Slug, name, branch, dir); err != nil {
		s.log.Warn("publish site", "course", c.Slug, "err", err)
		p := s.newPage(r, c.Title)
		p.Error = "The site settings were saved, but publishing failed: check that the branch exists and, if set, the directory."
		pr, perr := s.principal(r.Context(), u)
		if perr != nil {
			s.fail(w, "principal", perr)
			return
		}
		s.renderCourse(w, r, u, c, pr, p, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
