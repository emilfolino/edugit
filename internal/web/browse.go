package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/gitserver"
)

// BrowseGit is the read-only git access the repository browser needs.
// *gitserver.Repos implements it.
type BrowseGit interface {
	DefaultBranch(ctx context.Context, course, name string) (string, error)
	Branches(ctx context.Context, course, name string) ([]string, error)
	Tree(ctx context.Context, course, name, ref, path string) ([]gitserver.TreeEntry, error)
	File(ctx context.Context, course, name, ref, path string) (gitserver.Blob, error)
	Log(ctx context.Context, course, name, ref, path string, limit int) ([]gitserver.Commit, error)
	CommitDiff(ctx context.Context, course, name, sha string) (gitserver.Commit, string, error)
	Blame(ctx context.Context, course, name, ref, path string) ([]gitserver.BlameLine, error)
}

// crumb is one step of the path above a tree or file.
type crumb struct{ Name, URL string }

// codeLine is one highlighted line of a file.
type codeLine struct {
	N    int
	HTML template.HTML
}

// browseView is the data of the repository browser pages.
type browseView struct {
	Repo     string
	Ref      string
	Path     string
	Branches []string
	Crumbs   []crumb
	Entries  []gitserver.TreeEntry
	Blob     gitserver.Blob
	Lines    []codeLine
	Blame    []gitserver.BlameLine
	Commits  []gitserver.Commit
	Commit   gitserver.Commit
	Files    []diffFile
	// Base is the repository URL prefix; Q is the "?ref=" query to keep.
	Base string
	Q    string
	// CanEdit shows the editor links.
	CanEdit bool
}

func (s *Server) routeBrowse(mux *http.ServeMux) {
	const base = "/courses/{slug}/repos/{repo}"
	mux.HandleFunc("GET "+base+"/tree", s.browseTree)
	mux.HandleFunc("GET "+base+"/tree/{path...}", s.browseTree)
	mux.HandleFunc("GET "+base+"/blob/{path...}", s.browseBlob)
	mux.HandleFunc("GET "+base+"/blame/{path...}", s.browseBlame)
	mux.HandleFunc("GET "+base+"/commits", s.browseCommits)
	mux.HandleFunc("GET "+base+"/commit/{sha}", s.browseCommit)
}

// browseCtx authorises the request (read access) and fills the common view
// fields. It reports false after writing the response.
func (s *Server) browseCtx(w http.ResponseWriter, r *http.Request, title string) (pullCtx, *browseView, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return pullCtx{}, nil, false
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoRead)
	if !ok {
		return pullCtx{}, nil, false
	}
	ctx := r.Context()
	v := &browseView{Repo: pc.repo.Name, Path: strings.TrimSuffix(r.PathValue("path"), "/")}
	v.CanEdit = s.opts.Editor != nil && pc.can(authz.RepoEdit)
	v.Base = "/courses/" + pc.c.Slug + "/repos/" + pc.repo.Name
	v.Ref = r.URL.Query().Get("ref")
	var err error
	if v.Branches, err = s.opts.Browse.Branches(ctx, pc.c.Slug, pc.repo.Name); err != nil {
		s.fail(w, "list branches", err)
		return pullCtx{}, nil, false
	}
	if v.Ref == "" {
		if v.Ref, err = s.opts.Browse.DefaultBranch(ctx, pc.c.Slug, pc.repo.Name); err != nil || len(v.Branches) == 0 {
			// A repository with no commits yet has nothing to browse.
			v.Ref = ""
		}
	}
	if v.Ref != "" {
		v.Q = "?ref=" + url.QueryEscape(v.Ref)
	}
	if v.Path != "" {
		acc := ""
		for _, seg := range strings.Split(v.Path, "/") {
			acc += "/" + seg
			v.Crumbs = append(v.Crumbs, crumb{Name: seg, URL: v.Base + "/tree" + acc + v.Q})
		}
	}
	return pc, v, true
}

func (s *Server) browseRender(w http.ResponseWriter, r *http.Request, pc pullCtx, v *browseView, name, title string, status int) {
	p := s.newPage(r, title)
	p.Course, p.Browse = pc.c, v
	s.render(w, name, p, status)
}

// browseErr maps a missing ref or path to 404 and everything else to 500.
func (s *Server) browseErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, gitserver.ErrNoPath) || errors.Is(err, gitserver.ErrInvalidName) {
		http.NotFound(w, r)
		return
	}
	s.fail(w, "browse repository", err)
}

func (s *Server) browseTree(w http.ResponseWriter, r *http.Request) {
	pc, v, ok := s.browseCtx(w, r, "")
	if !ok {
		return
	}
	if v.Ref != "" {
		var err error
		if v.Entries, err = s.opts.Browse.Tree(r.Context(), pc.c.Slug, pc.repo.Name, v.Ref, v.Path); err != nil {
			s.browseErr(w, r, err)
			return
		}
	}
	s.browseRender(w, r, pc, v, "tree.html", pc.repo.Name, http.StatusOK)
}

func (s *Server) browseBlob(w http.ResponseWriter, r *http.Request) {
	pc, v, ok := s.browseCtx(w, r, "")
	if !ok {
		return
	}
	b, err := s.opts.Browse.File(r.Context(), pc.c.Slug, pc.repo.Name, v.Ref, v.Path)
	if err != nil {
		s.browseErr(w, r, err)
		return
	}
	v.Blob = b
	for i, h := range highlight(v.Path, b.Content) {
		v.Lines = append(v.Lines, codeLine{N: i + 1, HTML: h})
	}
	s.browseRender(w, r, pc, v, "blob.html", v.Path, http.StatusOK)
}

func (s *Server) browseBlame(w http.ResponseWriter, r *http.Request) {
	pc, v, ok := s.browseCtx(w, r, "")
	if !ok {
		return
	}
	var err error
	if v.Blame, err = s.opts.Browse.Blame(r.Context(), pc.c.Slug, pc.repo.Name, v.Ref, v.Path); err != nil {
		s.browseErr(w, r, err)
		return
	}
	s.browseRender(w, r, pc, v, "blame.html", v.Path, http.StatusOK)
}

func (s *Server) browseCommits(w http.ResponseWriter, r *http.Request) {
	pc, v, ok := s.browseCtx(w, r, "")
	if !ok {
		return
	}
	v.Path = r.URL.Query().Get("path")
	if v.Ref != "" {
		var err error
		if v.Commits, err = s.opts.Browse.Log(r.Context(), pc.c.Slug, pc.repo.Name, v.Ref, v.Path, 100); err != nil {
			s.browseErr(w, r, err)
			return
		}
	}
	s.browseRender(w, r, pc, v, "commits.html", pc.repo.Name, http.StatusOK)
}

func (s *Server) browseCommit(w http.ResponseWriter, r *http.Request) {
	pc, v, ok := s.browseCtx(w, r, "")
	if !ok {
		return
	}
	c, patch, err := s.opts.Browse.CommitDiff(r.Context(), pc.c.Slug, pc.repo.Name, r.PathValue("sha"))
	if err != nil {
		s.browseErr(w, r, err)
		return
	}
	v.Commit, v.Files = c, parseDiff(patch)
	s.browseRender(w, r, pc, v, "commit.html", c.Subject, http.StatusOK)
}
