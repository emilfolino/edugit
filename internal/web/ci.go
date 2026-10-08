package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

// CIStore is the CI run storage the web layer reads.
type CIStore interface {
	CIRuns(ctx context.Context, repoID int64, limit int) ([]store.CIRun, error)
	CIRunsForSHA(ctx context.Context, repoID int64, sha string) ([]store.CIRun, error)
	CIRunByID(ctx context.Context, repoID, id int64) (store.CIRun, error)
}

type ciView struct {
	Repo string
	Base string
	Runs []store.CIRun
	Run  *store.CIRun
}

func (s *Server) routeCI(mux *http.ServeMux) {
	const b = "/courses/{slug}/repos/{repo}/ci"
	mux.HandleFunc("GET "+b, s.ciList)
	mux.HandleFunc("GET "+b+"/{id}", s.ciShow)
}

func (s *Server) ciList(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoRead)
	if !ok {
		return
	}
	runs, err := s.opts.CI.CIRuns(r.Context(), pc.repo.ID, 50)
	if err != nil {
		s.fail(w, "list ci runs", err)
		return
	}
	p := s.newPage(r, "Checks · "+pc.repo.Name)
	p.Course = pc.c
	p.CI = &ciView{Repo: pc.repo.Name, Base: "/courses/" + pc.c.Slug + "/repos/" + pc.repo.Name + "/ci", Runs: runs}
	s.render(w, "ci.html", p, http.StatusOK)
}

func (s *Server) ciShow(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoRead)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	run, err := s.opts.CI.CIRunByID(r.Context(), pc.repo.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "load ci run", err)
		return
	}
	p := s.newPage(r, run.Job+" · "+pc.repo.Name)
	p.Course = pc.c
	p.CI = &ciView{Repo: pc.repo.Name, Base: "/courses/" + pc.c.Slug + "/repos/" + pc.repo.Name + "/ci", Run: &run}
	s.render(w, "ci.html", p, http.StatusOK)
}
