package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/hooks"
)

// EditGit is the write access the browser editor needs. *gitserver.Repos
// implements it.
type EditGit interface {
	CommitFiles(ctx context.Context, course, name, branch, from, expect string, changes []gitserver.FileChange, by gitserver.Identity, msg string) (string, error)
	Resolve(ctx context.Context, course, name, branch string) (string, error)
}

// editView is the data of the editor page.
type editView struct {
	Message string
	Content string
	Base    string // branch tip the edit started from
	New     bool   // the file does not exist yet
	Error   string
	NewName string // proposed new branch
}

const maxEditForm = gitserver.MaxEditBytes + 1<<16

func (s *Server) routeEditor(mux *http.ServeMux) {
	const base = "/courses/{slug}/repos/{repo}/edit"
	mux.HandleFunc("GET "+base+"/{path...}", s.editForm)
	mux.HandleFunc("POST "+base, s.editSave)
}

// editCtx is browseCtx plus the check that the viewer may edit here: the
// action is denied when the course restricts students to the CLI.
func (s *Server) editCtx(w http.ResponseWriter, r *http.Request) (pullCtx, *browseView, bool) {
	pc, v, ok := s.browseCtx(w, r, "")
	if !ok {
		return pc, nil, false
	}
	if !pc.can(authz.RepoEdit) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return pc, nil, false
	}
	return pc, v, true
}

func (s *Server) editForm(w http.ResponseWriter, r *http.Request) {
	pc, v, ok := s.editCtx(w, r)
	if !ok {
		return
	}
	ev := &editView{}
	if b, err := s.opts.Browse.File(r.Context(), pc.c.Slug, pc.repo.Name, v.Ref, v.Path); err == nil {
		if b.Binary || b.Truncated {
			http.Error(w, "this file cannot be edited in the browser", http.StatusUnprocessableEntity)
			return
		}
		ev.Content = b.Content
	} else if errors.Is(err, gitserver.ErrNoPath) {
		ev.New = true
	} else {
		s.browseErr(w, r, err)
		return
	}
	ev.Base, _ = s.opts.Editor.Resolve(r.Context(), pc.c.Slug, pc.repo.Name, v.Ref)
	s.renderEdit(w, r, pc, v, ev, http.StatusOK)
}

func (s *Server) renderEdit(w http.ResponseWriter, r *http.Request, pc pullCtx, v *browseView, ev *editView, status int) {
	p := s.newPage(r, v.Path)
	p.Course, p.Browse, p.Edit = pc.c, v, ev
	s.render(w, "edit.html", p, status)
}

func (s *Server) editSave(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePostLimit(w, r, maxEditForm)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoEdit)
	if !ok {
		return
	}
	ctx := r.Context()
	ref, path := r.PostFormValue("ref"), strings.TrimSpace(r.PostFormValue("path"))
	newBranch := strings.TrimSpace(r.PostFormValue("new_branch"))
	msg := strings.TrimSpace(r.PostFormValue("message"))
	del := r.PostFormValue("delete") != ""
	content := strings.ReplaceAll(r.PostFormValue("content"), "\r\n", "\n")

	v := &browseView{Repo: pc.repo.Name, Ref: ref, Path: path, Base: "/courses/" + pc.c.Slug + "/repos/" + pc.repo.Name}
	v.Q = "?ref=" + url.QueryEscape(ref)
	ev := &editView{Message: msg, Content: content, Base: r.PostFormValue("base"), NewName: newBranch}
	v.Branches, _ = s.opts.Browse.Branches(ctx, pc.c.Slug, pc.repo.Name)
	reject := func(status int, why string) {
		ev.Error = why
		s.renderEdit(w, r, pc, v, ev, status)
	}

	if msg == "" {
		reject(http.StatusBadRequest, "A commit message is required.")
		return
	}
	if len(content) > gitserver.MaxEditBytes {
		reject(http.StatusRequestEntityTooLarge, "The file is too large to edit in the browser.")
		return
	}
	target, from, expect := ref, "", ev.Base
	old := hooksNonZero
	if newBranch != "" {
		target, from, expect, old = newBranch, ref, "", hooksZero
	}
	req := hooks.Request{Course: pc.c.Slug, Repo: pc.repo.Name, User: u.Username,
		Updates: []hooks.Update{{Ref: "refs/heads/" + target, Old: old, New: hooksNonZero}}}
	if s.opts.Policy != nil {
		why, err := s.opts.Policy.CheckPush(ctx, req)
		if err != nil {
			s.fail(w, "check push policy", err)
			return
		}
		if len(why) > 0 {
			reject(http.StatusForbidden, strings.Join(why, " ")+" Use “new branch” to commit elsewhere.")
			return
		}
	}
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	sha, err := s.opts.Editor.CommitFiles(ctx, pc.c.Slug, pc.repo.Name, target, from, expect,
		[]gitserver.FileChange{{Path: path, Content: []byte(content), Delete: del}},
		gitserver.Identity{Name: name, Email: u.Email}, msg)
	switch {
	case errors.Is(err, gitserver.ErrStale):
		reject(http.StatusConflict, "The branch changed since you opened the file. Copy your edit, reload and try again.")
		return
	case errors.Is(err, gitserver.ErrNothingToCommit):
		reject(http.StatusBadRequest, "Nothing changed.")
		return
	case errors.Is(err, gitserver.ErrBadPath), errors.Is(err, gitserver.ErrInvalidName), errors.Is(err, gitserver.ErrNoPath):
		reject(http.StatusBadRequest, "Invalid path or branch name.")
		return
	case err != nil:
		s.fail(w, "commit from editor", err)
		return
	}
	if req.Updates[0].Old == hooksNonZero {
		req.Updates[0].Old = expect
	}
	req.Updates[0].New = sha
	if s.opts.Sink != nil {
		if err := s.opts.Sink.Pushed(ctx, req); err != nil {
			s.log.Error("editor push sink", "err", err)
		}
	}
	s.audit(r, u, "repo.edit", pc.c.Slug+"/"+pc.repo.Name, target+" "+path)
	http.Redirect(w, r, v.Base+"/blob/"+path+"?ref="+url.QueryEscape(target), http.StatusSeeOther)
}

// Placeholder object IDs for the pre-commit policy check, which only looks
// at whether a ref is created or deleted.
const (
	hooksZero    = "0000000000000000000000000000000000000000"
	hooksNonZero = "1111111111111111111111111111111111111111"
)
