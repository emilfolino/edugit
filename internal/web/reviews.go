package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

const maxCommentLen = 10000

// loadReviews fills the review state of v for the pull request page.
func (s *Server) loadReviews(ctx context.Context, pc pullCtx, v *pullView) error {
	pull := v.PR
	var err error
	if v.Reviews, err = s.opts.Pulls.Reviews(ctx, pull.ID); err != nil {
		return err
	}
	if v.Comments, err = s.opts.Pulls.Comments(ctx, pull.ID); err != nil {
		return err
	}
	if v.Requested, err = s.opts.Pulls.ReviewRequests(ctx, pull.ID); err != nil {
		return err
	}
	if v.Required, err = s.opts.Pulls.RequiredApprovals(ctx, pc.repo.ID, pull.Base); err != nil {
		return err
	}
	open := pull.State == store.PullOpen
	if open {
		if hs, herr := s.opts.PullGit.Resolve(ctx, pc.c.Slug, pc.repo.Name, pull.Head); herr == nil {
			if v.Approval, err = s.opts.Pulls.Approvals(ctx, pull.ID, hs); err != nil {
				return err
			}
		}
	}
	v.CanReview = open && pc.can(authz.RepoReview)
	v.CanRequest = open && pc.can(authz.RepoWrite) && (pull.AuthorID == pc.user.ID || authz.Can(pc.pr, authz.RosterView, pc.res))
	return nil
}

// approvalGate returns a refusal message if the branch protection of the base
// branch requires approvals the pull request at head lacks. It fails closed.
func (s *Server) approvalGate(ctx context.Context, pc pullCtx, pull store.PullRequest, head string) string {
	need, err := s.opts.Pulls.RequiredApprovals(ctx, pc.repo.ID, pull.Base)
	if err != nil {
		s.log.Error("required approvals", "err", err)
		return "Could not check the required approvals."
	}
	st, err := s.opts.Pulls.Approvals(ctx, pull.ID, head)
	if err != nil {
		s.log.Error("approvals", "err", err)
		return "Could not check the approvals."
	}
	switch {
	case st.ChangesRequested > 0:
		return "A reviewer has requested changes."
	case st.Approvals < need:
		return "This branch needs " + itoa(need) + " approval(s) on the latest commit; it has " + itoa(st.Approvals) + "."
	}
	return ""
}

// loadOpenPull is the common start of the review handlers: an authenticated
// POST on an open pull request where the user may do action.
func (s *Server) loadOpenPull(w http.ResponseWriter, r *http.Request, action authz.Action) (pullCtx, store.PullRequest, bool) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return pullCtx{}, store.PullRequest{}, false
	}
	pc, ok := s.loadPullRepo(w, r, u, action)
	if !ok {
		return pullCtx{}, store.PullRequest{}, false
	}
	pull, ok := s.loadPull(w, r, pc)
	if !ok {
		return pullCtx{}, store.PullRequest{}, false
	}
	if pull.State != store.PullOpen {
		s.renderPull(w, r, pc, pull, "This pull request is not open.", http.StatusBadRequest)
		return pullCtx{}, store.PullRequest{}, false
	}
	return pc, pull, true
}

// headSHA resolves the head branch of pull, reporting a refusal on failure.
func (s *Server) headSHA(w http.ResponseWriter, r *http.Request, pc pullCtx, pull store.PullRequest) (string, bool) {
	hs, err := s.opts.PullGit.Resolve(r.Context(), pc.c.Slug, pc.repo.Name, pull.Head)
	if err != nil {
		s.renderPull(w, r, pc, pull, "The head branch no longer exists.", http.StatusBadRequest)
		return "", false
	}
	return hs, true
}

// commentBody builds the stored text: the comment plus an optional suggestion
// block that reviewers can read as a proposed replacement of the line.
func commentBody(r *http.Request, inline bool) (string, bool) {
	body := strings.TrimSpace(r.PostFormValue("body"))
	if inline {
		if sg := strings.TrimRight(r.PostFormValue("suggestion"), "\r\n"); sg != "" {
			body += "\n\n```suggestion\n" + strings.ReplaceAll(sg, "\r\n", "\n") + "\n```"
		}
	}
	return body, body != "" && len(body) <= maxCommentLen
}

func (s *Server) reviewComment(w http.ResponseWriter, r *http.Request) {
	pc, pull, ok := s.loadOpenPull(w, r, authz.RepoReview)
	if !ok {
		return
	}
	path := strings.TrimSpace(r.PostFormValue("path"))
	line, _ := strconv.Atoi(r.PostFormValue("line"))
	inline := path != ""
	body, valid := commentBody(r, inline)
	switch {
	case !valid:
		s.renderPull(w, r, pc, pull, "Write a comment of up to 10000 characters.", http.StatusBadRequest)
		return
	case inline && (len(path) > 500 || line < 1):
		s.renderPull(w, r, pc, pull, "Give a valid file and line number.", http.StatusBadRequest)
		return
	case !inline:
		line = 0
	}
	hs, ok := s.headSHA(w, r, pc, pull)
	if !ok {
		return
	}
	if _, err := s.opts.Pulls.AddComment(r.Context(), pull.ID, 0, pc.user.ID, body, path, line, hs); err != nil {
		s.fail(w, "add comment", err)
		return
	}
	s.audit(r, pc.user, "pull.comment", pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(pull.Number))
	http.Redirect(w, r, pc.base()+"/"+itoa(pull.Number)+"#discussion", http.StatusSeeOther)
}

func (s *Server) reviewResolve(w http.ResponseWriter, r *http.Request) {
	pc, pull, ok := s.loadOpenPull(w, r, authz.RepoReview)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch err := s.opts.Pulls.ResolveComment(r.Context(), pull.ID, id, r.PostFormValue("resolved") != "0"); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "resolve comment", err)
	default:
		s.audit(r, pc.user, "pull.resolve", pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(pull.Number)+" comment "+itoa(int(id)))
		http.Redirect(w, r, pc.base()+"/"+itoa(pull.Number)+"#discussion", http.StatusSeeOther)
	}
}

func (s *Server) reviewSubmit(w http.ResponseWriter, r *http.Request) {
	pc, pull, ok := s.loadOpenPull(w, r, authz.RepoReview)
	if !ok {
		return
	}
	state := r.PostFormValue("state")
	body := strings.TrimSpace(r.PostFormValue("body"))
	switch {
	case state != store.ReviewComment && state != store.ReviewApprove && state != store.ReviewRequestChanges:
		s.renderPull(w, r, pc, pull, "Choose comment, approve or request changes.", http.StatusBadRequest)
		return
	case len(body) > maxCommentLen || (state != store.ReviewApprove && body == ""):
		s.renderPull(w, r, pc, pull, "Write a review of up to 10000 characters.", http.StatusBadRequest)
		return
	case state != store.ReviewComment && pull.AuthorID == pc.user.ID:
		s.renderPull(w, r, pc, pull, "You cannot approve or request changes on your own pull request.", http.StatusBadRequest)
		return
	}
	hs, ok := s.headSHA(w, r, pc, pull)
	if !ok {
		return
	}
	if _, err := s.opts.Pulls.AddReview(r.Context(), pull.ID, pc.user.ID, state, body, hs); err != nil {
		s.fail(w, "add review", err)
		return
	}
	s.audit(r, pc.user, "pull.review", pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(pull.Number)+" "+state)
	http.Redirect(w, r, pc.base()+"/"+itoa(pull.Number)+"#discussion", http.StatusSeeOther)
}

// reviewRequest asks an enrolled course member to review. Staff and the
// author may do so, which is how students are paired for peer review.
func (s *Server) reviewRequest(w http.ResponseWriter, r *http.Request) {
	pc, pull, ok := s.loadOpenPull(w, r, authz.RepoWrite)
	if !ok {
		return
	}
	if pull.AuthorID != pc.user.ID && !authz.Can(pc.pr, authz.RosterView, pc.res) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("reviewer"))
	roster, err := s.opts.Courses.Roster(r.Context(), pc.c.ID)
	if err != nil {
		s.fail(w, "load roster", err)
		return
	}
	var reviewer int64
	for _, m := range roster {
		if !m.Pending() && strings.EqualFold(m.Username, name) {
			reviewer = m.UserID
		}
	}
	if reviewer == 0 || reviewer == pull.AuthorID {
		s.renderPull(w, r, pc, pull, "Choose another enrolled member of this course.", http.StatusBadRequest)
		return
	}
	if err := s.opts.Pulls.RequestReview(r.Context(), pull.ID, reviewer, pc.user.ID); err != nil {
		s.fail(w, "request review", err)
		return
	}
	s.audit(r, pc.user, "pull.reviewer", pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(pull.Number)+" "+name)
	http.Redirect(w, r, pc.base()+"/"+itoa(pull.Number), http.StatusSeeOther)
}

// setProtection sets how many approvals merging into a branch pattern needs.
func (s *Server) setProtection(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoWrite)
	if !ok {
		return
	}
	if !authz.Can(pc.pr, authz.RosterView, pc.res) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pattern := strings.TrimSpace(r.PostFormValue("pattern"))
	n, err := strconv.Atoi(r.PostFormValue("approvals"))
	if pattern == "" || len(pattern) > 100 || err != nil || n < 0 || n > 10 {
		s.renderPulls(w, r, pc, "Give a branch pattern and 0 to 10 approvals.", http.StatusBadRequest)
		return
	}
	if err := s.opts.Pulls.SetRequiredApprovals(r.Context(), pc.repo.ID, pattern, n); err != nil {
		s.fail(w, "set protection", err)
		return
	}
	s.audit(r, u, "repo.protection", pc.c.Slug+"/"+pc.repo.Name, pattern+" approvals="+itoa(n))
	http.Redirect(w, r, pc.base(), http.StatusSeeOther)
}
