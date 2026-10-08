package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/store"
)

// RepoStore is the repository metadata the course pages need.
type RepoStore interface {
	CourseRepos(ctx context.Context, courseID, userID int64) ([]store.RepoEntry, error)
	RepoByName(ctx context.Context, courseSlug, name string, userID int64) (store.Repo, bool, error)
	CreateRepo(ctx context.Context, courseID int64, name, kind string, isTemplate bool) (store.Repo, error)
	SetTemplate(ctx context.Context, repoID int64, isTemplate bool) error
	DeleteRepo(ctx context.Context, repoID int64) error
}

// RepoDisk creates and removes bare repositories. *gitserver.Repos
// implements it.
type RepoDisk interface {
	Create(ctx context.Context, course, name, defaultBranch string) error
	Delete(course, name string) error
	// Generate creates name from the template repo's content, with fresh
	// (single-commit) or copied history.
	Generate(ctx context.Context, course, template, name string, fresh bool) error
	// Branch creates branch at the tip of from; DefaultBranch names HEAD.
	Branch(ctx context.Context, course, name, branch, from string) error
	DefaultBranch(ctx context.Context, course, name string) (string, error)
	// Replace swaps name for what create builds, keeping the old repo if
	// create fails.
	Replace(ctx context.Context, course, name string, create func(context.Context) error) error
}

// repoView is a repository as shown on the course page.
type repoView struct {
	Name       string
	Kind       string
	IsTemplate bool
	Archived   bool
	CloneURL   string
	CanAdmin   bool
}

// resource describes the repository for authz.Can.
func repoResource(e store.RepoEntry) authz.Resource {
	return authz.Resource{
		CourseID: e.CourseID, Repo: true, Kind: authz.RepoKind(e.Kind),
		IsTemplate: e.IsTemplate, Archived: e.Archived, IsMember: e.Member,
	}
}

// repoViews lists the repositories the user may read. Students therefore
// see only template teacher repos (and their own work), never solutions.
func (s *Server) repoViews(r *http.Request, u store.User, c store.Course, pr authz.Principal) ([]repoView, error) {
	list, err := s.opts.Repos.CourseRepos(r.Context(), c.ID, u.ID)
	if err != nil {
		return nil, err
	}
	var out []repoView
	for _, e := range list {
		res := repoResource(e)
		if !authz.Can(pr, authz.RepoRead, res) {
			continue
		}
		out = append(out, repoView{
			Name: e.Name, Kind: e.Kind, IsTemplate: e.IsTemplate, Archived: e.Archived,
			CloneURL: s.baseURL(r) + "/git/" + c.Slug + "/" + e.Name + ".git",
			CanAdmin: !c.Archived && e.Kind == "teacher" && authz.Can(pr, authz.RepoAdmin, res),
		})
	}
	return out, nil
}

// baseURL is the public address clone URLs are built from.
func (s *Server) baseURL(r *http.Request) string {
	if s.opts.PublicURL != "" {
		return s.opts.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) createRepo(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.RepoCreate)
	if !ok {
		return
	}
	name := r.PostFormValue("name")
	var msg string
	switch {
	case c.Archived:
		msg = "This course is archived."
	case !gitserver.ValidName(name):
		msg = "Repository names are lowercase letters, digits, dots, hyphens and underscores, starting with a letter or digit."
	}
	if msg == "" {
		msg = s.makeRepo(r, c, name, r.PostFormValue("template") == "1")
	}
	if msg != "" {
		p := s.newPage(r, c.Title)
		p.Error = msg
		s.renderCourse(w, r, u, c, pr, p, http.StatusBadRequest)
		return
	}
	s.audit(r, u, "repo.create", c.Slug+"/"+name, "template="+strconv.FormatBool(r.PostFormValue("template") == "1"))
	http.Redirect(w, r, "/courses/"+c.Slug, http.StatusSeeOther)
}

// makeRepo creates the metadata row and the bare repository, undoing the row
// if the disk step fails. It returns a user-facing message on a refusal; real
// failures are logged and reported generically.
func (s *Server) makeRepo(r *http.Request, c store.Course, name string, template bool) string {
	ctx := r.Context()
	if _, _, err := s.opts.Repos.RepoByName(ctx, c.Slug, name, 0); err == nil {
		return "A repository with that name already exists."
	}
	rec, err := s.opts.Repos.CreateRepo(ctx, c.ID, name, "teacher", template)
	if err != nil {
		s.log.Error("create repo", "err", err)
		return "Could not create the repository."
	}
	if err := s.opts.Disk.Create(ctx, c.Slug, name, "main"); err != nil {
		s.log.Error("create bare repo", "course", c.Slug, "repo", name, "err", err)
		if derr := s.opts.Repos.DeleteRepo(ctx, rec.ID); derr != nil {
			s.log.Error("roll back repo row", "course", c.Slug, "repo", name, "err", derr)
		}
		return "Could not create the repository."
	}
	return ""
}

func (s *Server) setTemplate(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.RepoAdmin)
	if !ok {
		return
	}
	rec, member, err := s.opts.Repos.RepoByName(r.Context(), c.Slug, r.PathValue("repo"), u.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && rec.Kind != "teacher") {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "load repo", err)
		return
	}
	res := repoResource(store.RepoEntry{Repo: rec, Member: member})
	if c.Archived || !authz.Can(pr, authz.RepoAdmin, res) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	on := r.PostFormValue("template") == "1"
	if err := s.opts.Repos.SetTemplate(r.Context(), rec.ID, on); err != nil {
		s.fail(w, "set template", err)
		return
	}
	s.audit(r, u, "repo.template", c.Slug+"/"+rec.Name, "template="+strconv.FormatBool(on))
	http.Redirect(w, r, "/courses/"+c.Slug, http.StatusSeeOther)
}
