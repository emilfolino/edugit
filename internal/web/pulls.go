package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/store"
)

// PullStore is the pull request metadata the PR pages need.
type PullStore interface {
	CreatePull(ctx context.Context, repoID, authorID int64, title, body, head, base string) (store.PullRequest, error)
	Pull(ctx context.Context, repoID int64, number int) (store.PullRequest, error)
	Pulls(ctx context.Context, repoID int64, state string) ([]store.PullRequest, error)
	SetPullState(ctx context.Context, id int64, state, mergeCommit string) error
	ReopenPull(ctx context.Context, id int64) error

	AddReview(ctx context.Context, prID, reviewerID int64, state, body, sha string) (store.Review, error)
	Reviews(ctx context.Context, prID int64) ([]store.Review, error)
	AddComment(ctx context.Context, prID, reviewID, authorID int64, body, path string, line int, sha string) (store.Comment, error)
	Comments(ctx context.Context, prID int64) ([]store.Comment, error)
	ResolveComment(ctx context.Context, prID, commentID int64, resolved bool) error
	Approvals(ctx context.Context, prID int64, sha string) (store.ApprovalState, error)
	RequestReview(ctx context.Context, prID, reviewerID, byID int64) error
	ReviewRequests(ctx context.Context, prID int64) ([]string, error)
	IsRequestedReviewer(ctx context.Context, repoID, userID int64) (bool, error)
	RequiredApprovals(ctx context.Context, repoID int64, branch string) (int, error)
	SetRequiredApprovals(ctx context.Context, repoID int64, pattern string, n int) error
}

// PullGit is the git access pull requests need. *gitserver.Repos implements
// it.
type PullGit interface {
	Resolve(ctx context.Context, course, name, branch string) (string, error)
	Commits(ctx context.Context, course, name, base, head string) ([]gitserver.Commit, error)
	Diff(ctx context.Context, course, name, base, head string) (string, error)
	Mergeable(ctx context.Context, course, name, base, head string) (bool, error)
	Merge(ctx context.Context, course, name, base, head, strategy string, by gitserver.Identity, msg string) (string, error)
}

// pullsView is the data of the pull request list page.
type pullsView struct {
	Repo        string
	Pulls       []store.PullRequest
	CanWrite    bool
	CanFeedback bool
	CanProtect  bool
}

// pullView is the data of a single pull request page.
type pullView struct {
	Repo      string
	PR        store.PullRequest
	Commits   []gitserver.Commit
	Files     []diffFile
	Split     bool
	Mergeable bool
	// Gone is set when the branches can no longer be resolved.
	Gone     bool
	CanWrite bool

	Reviews   []store.Review
	Comments  []store.Comment
	Requested []string
	Approval  store.ApprovalState
	Required  int
	CanReview bool
	// CanRequest is set for staff and the author, who may ask for reviewers.
	CanRequest bool

	// Checks are the CI runs of the head commit.
	Checks    []store.CIRun
	ChecksURL string
}

func (s *Server) routePulls(mux *http.ServeMux) {
	const base = "/courses/{slug}/repos/{repo}/pulls"
	mux.HandleFunc("GET "+base, s.pullList)
	mux.HandleFunc("POST "+base, s.pullCreate)
	mux.HandleFunc("POST "+base+"/feedback", s.pullFeedback)
	mux.HandleFunc("GET "+base+"/{n}", s.pullShow)
	mux.HandleFunc("POST "+base+"/{n}/merge", s.pullMerge)
	mux.HandleFunc("POST "+base+"/{n}/close", s.pullClose)
	mux.HandleFunc("POST "+base+"/{n}/reopen", s.pullReopen)
	mux.HandleFunc("POST "+base+"/{n}/comments", s.reviewComment)
	mux.HandleFunc("POST "+base+"/{n}/comments/{id}/resolve", s.reviewResolve)
	mux.HandleFunc("POST "+base+"/{n}/reviews", s.reviewSubmit)
	mux.HandleFunc("POST "+base+"/{n}/reviewers", s.reviewRequest)
	mux.HandleFunc("POST /courses/{slug}/repos/{repo}/protection", s.setProtection)
}

// pullCtx is what every PR handler needs.
type pullCtx struct {
	user store.User
	c    store.Course
	pr   authz.Principal
	repo store.Repo
	res  authz.Resource
}

func (pc pullCtx) can(a authz.Action) bool { return authz.Can(pc.pr, a, pc.res) }

// loadPullRepo resolves the repository named in the path for a user who may
// read it (404 otherwise) and checks action on it (403).
func (s *Server) loadPullRepo(w http.ResponseWriter, r *http.Request, u store.User, action authz.Action) (pullCtx, bool) {
	c, pr, ok := s.loadCourse(w, r, u, authz.CourseView)
	if !ok {
		return pullCtx{}, false
	}
	rec, member, err := s.opts.Repos.RepoByName(r.Context(), c.Slug, r.PathValue("repo"), u.ID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return pullCtx{}, false
	}
	if err != nil {
		s.fail(w, "load repo", err)
		return pullCtx{}, false
	}
	res := repoResource(store.RepoEntry{Repo: rec, Member: member})
	res.CommitMethods = c.CommitMethods
	if res.IsReviewer, err = s.opts.Pulls.IsRequestedReviewer(r.Context(), rec.ID, u.ID); err != nil {
		s.fail(w, "load reviewer", err)
		return pullCtx{}, false
	}
	if !authz.Can(pr, authz.RepoRead, res) {
		http.NotFound(w, r)
		return pullCtx{}, false
	}
	pc := pullCtx{user: u, c: c, pr: pr, repo: rec, res: res}
	if action != authz.RepoRead && !pc.can(action) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return pullCtx{}, false
	}
	return pc, true
}

func (pc pullCtx) base() string {
	return "/courses/" + pc.c.Slug + "/repos/" + pc.repo.Name + "/pulls"
}

func (s *Server) pullList(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoRead)
	if !ok {
		return
	}
	s.renderPulls(w, r, pc, "", http.StatusOK)
}

func (s *Server) renderPulls(w http.ResponseWriter, r *http.Request, pc pullCtx, msg string, status int) {
	list, err := s.opts.Pulls.Pulls(r.Context(), pc.repo.ID, "")
	if err != nil {
		s.fail(w, "list pulls", err)
		return
	}
	p := s.newPage(r, "Pull requests · "+pc.repo.Name)
	p.Course, p.Error = pc.c, msg
	p.Pulls = &pullsView{
		Repo: pc.repo.Name, Pulls: list, CanWrite: pc.can(authz.RepoWrite),
		CanFeedback: pc.repo.Kind != string(authz.KindTeacher) && pc.can(authz.RepoWrite) && authz.Can(pc.pr, authz.RosterView, pc.res),
		CanProtect:  pc.can(authz.RepoWrite) && authz.Can(pc.pr, authz.RosterView, pc.res),
	}
	s.render(w, "pulls.html", p, status)
}

// identity is the committer recorded for a merge.
func identity(u store.User) gitserver.Identity {
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	email := u.Email
	if email == "" {
		email = u.Username + "@users.invalid"
	}
	return gitserver.Identity{Name: name, Email: email}
}

// openPull validates and records a pull request from head into base. It
// returns a user-facing refusal message, or "" and the number.
func (s *Server) openPull(r *http.Request, pc pullCtx, title, body, head, base string) (string, int) {
	ctx := r.Context()
	title = strings.TrimSpace(title)
	switch {
	case title == "" || len(title) > 200:
		return "Give the pull request a title of up to 200 characters.", 0
	case len(body) > 10000:
		return "The description is too long.", 0
	case head == base:
		return "The head and base branches must differ.", 0
	}
	hs, err := s.opts.PullGit.Resolve(ctx, pc.c.Slug, pc.repo.Name, head)
	if err != nil {
		return "Branch " + head + " was not found.", 0
	}
	bs, err := s.opts.PullGit.Resolve(ctx, pc.c.Slug, pc.repo.Name, base)
	if err != nil {
		return "Branch " + base + " was not found.", 0
	}
	ahead, err := s.opts.PullGit.Commits(ctx, pc.c.Slug, pc.repo.Name, bs, hs)
	if err != nil {
		s.log.Error("list commits", "err", err)
		return "Could not compare the branches.", 0
	}
	if len(ahead) == 0 {
		return head + " has no commits that " + base + " lacks.", 0
	}
	pull, err := s.opts.Pulls.CreatePull(ctx, pc.repo.ID, pc.user.ID, title, body, head, base)
	if errors.Is(err, store.ErrPullExists) {
		return "An open pull request for these branches already exists.", 0
	}
	if err != nil {
		s.log.Error("create pull", "err", err)
		return "Could not create the pull request.", 0
	}
	s.audit(r, pc.user, "pull.create", pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(pull.Number))
	return "", pull.Number
}

func (s *Server) pullCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoWrite)
	if !ok {
		return
	}
	msg, n := s.openPull(r, pc, r.PostFormValue("title"), r.PostFormValue("body"),
		strings.TrimSpace(r.PostFormValue("head")), strings.TrimSpace(r.PostFormValue("base")))
	if msg != "" {
		s.renderPulls(w, r, pc, msg, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, pc.base()+"/"+itoa(n), http.StatusSeeOther)
}

// pullFeedback opens the staff feedback pull request (feedback into main) on
// a student or team repository.
func (s *Server) pullFeedback(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoWrite)
	if !ok {
		return
	}
	if pc.repo.Kind == string(authz.KindTeacher) || !authz.Can(pc.pr, authz.RosterView, pc.res) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	msg, n := s.openPull(r, pc, "Feedback", "Teacher feedback on your work.", store.FeedbackBranch, "main")
	if msg != "" {
		s.renderPulls(w, r, pc, msg, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, pc.base()+"/"+itoa(n), http.StatusSeeOther)
}

// loadPull finds the pull request named in the path.
func (s *Server) loadPull(w http.ResponseWriter, r *http.Request, pc pullCtx) (store.PullRequest, bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err == nil {
		var pull store.PullRequest
		if pull, err = s.opts.Pulls.Pull(r.Context(), pc.repo.ID, n); err == nil {
			return pull, true
		}
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, strconv.ErrSyntax) || errors.Is(err, strconv.ErrRange) {
		http.NotFound(w, r)
	} else {
		s.fail(w, "load pull", err)
	}
	return store.PullRequest{}, false
}

func (s *Server) pullShow(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoRead)
	if !ok {
		return
	}
	pull, ok := s.loadPull(w, r, pc)
	if !ok {
		return
	}
	s.renderPull(w, r, pc, pull, "", http.StatusOK)
}

func (s *Server) renderPull(w http.ResponseWriter, r *http.Request, pc pullCtx, pull store.PullRequest, msg string, status int) {
	ctx := r.Context()
	v := &pullView{Repo: pc.repo.Name, PR: pull, Split: r.URL.Query().Get("view") == "split", CanWrite: pc.can(authz.RepoWrite)}
	if pull.State == store.PullOpen {
		hs, herr := s.opts.PullGit.Resolve(ctx, pc.c.Slug, pc.repo.Name, pull.Head)
		bs, berr := s.opts.PullGit.Resolve(ctx, pc.c.Slug, pc.repo.Name, pull.Base)
		if herr != nil || berr != nil {
			v.Gone = true
		} else {
			var err error
			if v.Commits, err = s.opts.PullGit.Commits(ctx, pc.c.Slug, pc.repo.Name, bs, hs); err == nil {
				var raw string
				if raw, err = s.opts.PullGit.Diff(ctx, pc.c.Slug, pc.repo.Name, bs, hs); err == nil {
					v.Files = parseDiff(raw)
					v.Mergeable, err = s.opts.PullGit.Mergeable(ctx, pc.c.Slug, pc.repo.Name, bs, hs)
				}
			}
			if err != nil {
				s.fail(w, "pull details", err)
				return
			}
			if s.opts.CI != nil {
				// A failure to load checks must not hide the PR.
				if v.Checks, err = s.opts.CI.CIRunsForSHA(ctx, pc.repo.ID, hs); err != nil {
					s.log.Error("load checks", "err", err)
				}
				v.ChecksURL = "/courses/" + pc.c.Slug + "/repos/" + pc.repo.Name + "/ci"
			}
		}
	}
	if err := s.loadReviews(ctx, pc, v); err != nil {
		s.fail(w, "load reviews", err)
		return
	}
	p := s.newPage(r, "#"+itoa(pull.Number)+" "+pull.Title)
	p.Course, p.Error, p.Pull = pc.c, msg, v
	s.render(w, "pull.html", p, status)
}

func (s *Server) pullMerge(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoWrite)
	if !ok {
		return
	}
	pull, ok := s.loadPull(w, r, pc)
	if !ok {
		return
	}
	strategy := r.PostFormValue("strategy")
	if strategy != gitserver.StrategyMerge && strategy != gitserver.StrategySquash && strategy != gitserver.StrategyRebase {
		s.renderPull(w, r, pc, pull, "Choose a merge strategy.", http.StatusBadRequest)
		return
	}
	if pull.State != store.PullOpen {
		s.renderPull(w, r, pc, pull, "This pull request is not open.", http.StatusBadRequest)
		return
	}
	hs, err := s.opts.PullGit.Resolve(r.Context(), pc.c.Slug, pc.repo.Name, pull.Head)
	if err != nil {
		s.renderPull(w, r, pc, pull, "The head branch no longer exists.", http.StatusBadRequest)
		return
	}
	if msg := s.approvalGate(r.Context(), pc, pull, hs); msg != "" {
		s.renderPull(w, r, pc, pull, msg, http.StatusBadRequest)
		return
	}
	title := "Merge pull request #" + itoa(pull.Number) + " from " + pull.Head
	if strategy == gitserver.StrategySquash {
		title = pull.Title + " (#" + itoa(pull.Number) + ")"
	}
	sha, err := s.opts.PullGit.Merge(r.Context(), pc.c.Slug, pc.repo.Name, pull.Base, hs, strategy, identity(u), title)
	switch {
	case errors.Is(err, gitserver.ErrConflict):
		s.renderPull(w, r, pc, pull, "The branches conflict; resolve the conflicts first.", http.StatusBadRequest)
		return
	case errors.Is(err, gitserver.ErrNothingToMerge):
		s.renderPull(w, r, pc, pull, "There is nothing to merge.", http.StatusBadRequest)
		return
	case errors.Is(err, gitserver.ErrStale):
		s.renderPull(w, r, pc, pull, "The base branch moved while merging; try again.", http.StatusConflict)
		return
	case err != nil:
		s.fail(w, "merge pull", err)
		return
	}
	if err := s.opts.Pulls.SetPullState(r.Context(), pull.ID, store.PullMerged, sha); err != nil {
		s.fail(w, "record merge", err)
		return
	}
	s.audit(r, u, "pull.merge", pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(pull.Number)+" "+strategy)
	s.closeReferencedIssues(r, pc, pull)
	http.Redirect(w, r, pc.base()+"/"+itoa(pull.Number), http.StatusSeeOther)
}

func (s *Server) pullClose(w http.ResponseWriter, r *http.Request) {
	s.pullTransition(w, r, "pull.close", func(ctx context.Context, p store.PullRequest) error {
		return s.opts.Pulls.SetPullState(ctx, p.ID, store.PullClosed, "")
	})
}

func (s *Server) pullReopen(w http.ResponseWriter, r *http.Request) {
	s.pullTransition(w, r, "pull.reopen", func(ctx context.Context, p store.PullRequest) error {
		return s.opts.Pulls.ReopenPull(ctx, p.ID)
	})
}

func (s *Server) pullTransition(w http.ResponseWriter, r *http.Request, action string, do func(context.Context, store.PullRequest) error) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoWrite)
	if !ok {
		return
	}
	pull, ok := s.loadPull(w, r, pc)
	if !ok {
		return
	}
	switch err := do(r.Context(), pull); {
	case errors.Is(err, store.ErrPullClosed), errors.Is(err, store.ErrPullExists):
		s.renderPull(w, r, pc, pull, "The pull request cannot change state now.", http.StatusBadRequest)
	case err != nil:
		s.fail(w, action, err)
	default:
		s.audit(r, u, action, pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(pull.Number))
		http.Redirect(w, r, pc.base()+"/"+itoa(pull.Number), http.StatusSeeOther)
	}
}
