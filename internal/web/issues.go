package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

// IssueStore is the persistence the issue tracker needs.
type IssueStore interface {
	CreateIssue(ctx context.Context, repoID, authorID int64, title, body string) (store.Issue, error)
	Issue(ctx context.Context, repoID int64, number int) (store.Issue, error)
	Issues(ctx context.Context, repoID int64, state, label string) ([]store.Issue, error)
	SetIssueState(ctx context.Context, id int64, state string) error
	CloseIssues(ctx context.Context, repoID int64, numbers []int) ([]int, error)
	AddIssueComment(ctx context.Context, issueID, authorID int64, body string) error
	IssueComments(ctx context.Context, issueID int64) ([]store.IssueComment, error)
	AddIssueLabel(ctx context.Context, repoID, issueID int64, name string) error
	RemoveIssueLabel(ctx context.Context, repoID, issueID int64, name string) error
	Labels(ctx context.Context, repoID int64) ([]string, error)
	SetIssueAssignee(ctx context.Context, issueID, userID int64, assigned bool) error
	CreateMilestone(ctx context.Context, repoID int64, title, due string) error
	Milestones(ctx context.Context, repoID int64) ([]store.Milestone, error)
	SetIssueMilestone(ctx context.Context, repoID, issueID int64, title string) error
}

type issuesView struct {
	Repo       string
	Base       string
	Issues     []store.Issue
	State      string
	Label      string
	Labels     []string
	Milestones []store.Milestone
	CanIssue   bool
	CanManage  bool
}

type issueView struct {
	Issue      store.Issue
	Base       string
	Comments   []store.IssueComment
	Milestones []store.Milestone
	CanIssue   bool // comment
	CanState   bool // close or reopen
	CanManage  bool // labels, assignees, milestone
}

func (s *Server) routeIssues(mux *http.ServeMux) {
	const b = "/courses/{slug}/repos/{repo}/issues"
	mux.HandleFunc("GET "+b, s.issueList)
	mux.HandleFunc("POST "+b, s.issueCreate)
	mux.HandleFunc("POST "+b+"/milestones", s.milestoneCreate)
	mux.HandleFunc("GET "+b+"/{n}", s.issueShow)
	mux.HandleFunc("POST "+b+"/{n}/comment", s.issueComment)
	mux.HandleFunc("POST "+b+"/{n}/state", s.issueState)
	mux.HandleFunc("POST "+b+"/{n}/meta", s.issueMeta)
}

func (pc pullCtx) issuesBase() string {
	return "/courses/" + pc.c.Slug + "/repos/" + pc.repo.Name + "/issues"
}

func (s *Server) issueList(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoRead)
	if !ok {
		return
	}
	s.renderIssues(w, r, pc, "", http.StatusOK)
}

func (s *Server) renderIssues(w http.ResponseWriter, r *http.Request, pc pullCtx, msg string, status int) {
	state := r.URL.Query().Get("state")
	if state != store.IssueOpen && state != store.IssueClosed && state != "all" {
		state = store.IssueOpen
	}
	filter, label := state, strings.TrimSpace(r.URL.Query().Get("label"))
	if state == "all" {
		filter = ""
	}
	ctx := r.Context()
	v := &issuesView{Repo: pc.repo.Name, Base: pc.issuesBase(), State: state, Label: label,
		CanIssue: pc.can(authz.RepoIssue), CanManage: pc.can(authz.RepoWrite)}
	var err error
	if v.Issues, err = s.opts.Issues.Issues(ctx, pc.repo.ID, filter, label); err != nil {
		s.fail(w, "list issues", err)
		return
	}
	if v.Labels, err = s.opts.Issues.Labels(ctx, pc.repo.ID); err != nil {
		s.fail(w, "list labels", err)
		return
	}
	if v.Milestones, err = s.opts.Issues.Milestones(ctx, pc.repo.ID); err != nil {
		s.fail(w, "list milestones", err)
		return
	}
	p := s.newPage(r, "Issues · "+pc.repo.Name)
	p.Course, p.Error, p.Issues = pc.c, msg, v
	s.render(w, "issues.html", p, status)
}

func (s *Server) issueCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoIssue)
	if !ok {
		return
	}
	title, body := strings.TrimSpace(r.PostFormValue("title")), strings.TrimSpace(r.PostFormValue("body"))
	if title == "" || len(title) > 200 {
		s.renderIssues(w, r, pc, "Give the issue a title of at most 200 characters.", http.StatusBadRequest)
		return
	}
	i, err := s.opts.Issues.CreateIssue(r.Context(), pc.repo.ID, u.ID, title, body)
	if err != nil {
		s.fail(w, "create issue", err)
		return
	}
	http.Redirect(w, r, pc.issuesBase()+"/"+itoa(i.Number), http.StatusSeeOther)
}

func (s *Server) milestoneCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoWrite)
	if !ok {
		return
	}
	title, due := strings.TrimSpace(r.PostFormValue("title")), strings.TrimSpace(r.PostFormValue("due"))
	if _, err := time.Parse("2006-01-02", due); due != "" && err != nil || title == "" || len(title) > 100 {
		s.renderIssues(w, r, pc, "Give a title and an optional due date as YYYY-MM-DD.", http.StatusBadRequest)
		return
	}
	if err := s.opts.Issues.CreateMilestone(r.Context(), pc.repo.ID, title, due); err != nil {
		s.fail(w, "create milestone", err)
		return
	}
	http.Redirect(w, r, pc.issuesBase(), http.StatusSeeOther)
}

// loadIssue parses {n} and loads the issue, answering 404 when absent.
func (s *Server) loadIssue(w http.ResponseWriter, r *http.Request, pc pullCtx) (store.Issue, bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.NotFound(w, r)
		return store.Issue{}, false
	}
	i, err := s.opts.Issues.Issue(r.Context(), pc.repo.ID, n)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Issue{}, false
	}
	if err != nil {
		s.fail(w, "load issue", err)
		return store.Issue{}, false
	}
	return i, true
}

// issuePost is the common prologue of the issue mutations.
func (s *Server) issuePost(w http.ResponseWriter, r *http.Request, action authz.Action) (pullCtx, store.Issue, bool) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return pullCtx{}, store.Issue{}, false
	}
	pc, ok := s.loadPullRepo(w, r, u, action)
	if !ok {
		return pullCtx{}, store.Issue{}, false
	}
	i, ok := s.loadIssue(w, r, pc)
	return pc, i, ok
}

func (s *Server) issueShow(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pc, ok := s.loadPullRepo(w, r, u, authz.RepoRead)
	if !ok {
		return
	}
	i, ok := s.loadIssue(w, r, pc)
	if !ok {
		return
	}
	s.renderIssue(w, r, pc, i, "", http.StatusOK)
}

func (s *Server) renderIssue(w http.ResponseWriter, r *http.Request, pc pullCtx, i store.Issue, msg string, status int) {
	// Reload so the page reflects the change just made.
	i, err := s.opts.Issues.Issue(r.Context(), pc.repo.ID, i.Number)
	if err != nil {
		s.fail(w, "reload issue", err)
		return
	}
	v := &issueView{Issue: i, Base: pc.issuesBase(), CanIssue: pc.can(authz.RepoIssue),
		CanManage: pc.can(authz.RepoWrite),
		CanState:  pc.can(authz.RepoIssue) && (i.AuthorID == pc.user.ID || pc.can(authz.RepoWrite))}
	if v.Comments, err = s.opts.Issues.IssueComments(r.Context(), i.ID); err != nil {
		s.fail(w, "load comments", err)
		return
	}
	if v.Milestones, err = s.opts.Issues.Milestones(r.Context(), pc.repo.ID); err != nil {
		s.fail(w, "load milestones", err)
		return
	}
	p := s.newPage(r, "#"+itoa(i.Number)+" "+i.Title)
	p.Course, p.Error, p.Issue = pc.c, msg, v
	s.render(w, "issue.html", p, status)
}

func (s *Server) issueComment(w http.ResponseWriter, r *http.Request) {
	pc, i, ok := s.issuePost(w, r, authz.RepoIssue)
	if !ok {
		return
	}
	body := strings.TrimSpace(r.PostFormValue("body"))
	if body == "" {
		s.renderIssue(w, r, pc, i, "Write a comment first.", http.StatusBadRequest)
		return
	}
	if err := s.opts.Issues.AddIssueComment(r.Context(), i.ID, pc.user.ID, body); err != nil {
		s.fail(w, "add issue comment", err)
		return
	}
	http.Redirect(w, r, pc.issuesBase()+"/"+itoa(i.Number), http.StatusSeeOther)
}

// issueState closes or reopens; the author may do so on their own issue.
func (s *Server) issueState(w http.ResponseWriter, r *http.Request) {
	pc, i, ok := s.issuePost(w, r, authz.RepoIssue)
	if !ok {
		return
	}
	if i.AuthorID != pc.user.ID && !pc.can(authz.RepoWrite) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	state := store.IssueClosed
	if r.PostFormValue("state") == store.IssueOpen {
		state = store.IssueOpen
	}
	if err := s.opts.Issues.SetIssueState(r.Context(), i.ID, state); err != nil {
		s.fail(w, "set issue state", err)
		return
	}
	s.audit(r, pc.user, "issue."+state, pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(i.Number))
	http.Redirect(w, r, pc.issuesBase()+"/"+itoa(i.Number), http.StatusSeeOther)
}

// issueMeta edits labels, assignees and the milestone (RepoWrite).
func (s *Server) issueMeta(w http.ResponseWriter, r *http.Request) {
	pc, i, ok := s.issuePost(w, r, authz.RepoWrite)
	if !ok {
		return
	}
	ctx, op := r.Context(), r.PostFormValue("op")
	value := strings.TrimSpace(r.PostFormValue("value"))
	var err error
	switch op {
	case "label":
		if value == "" || len(value) > 50 {
			s.renderIssue(w, r, pc, i, "Give a label of at most 50 characters.", http.StatusBadRequest)
			return
		}
		err = s.opts.Issues.AddIssueLabel(ctx, pc.repo.ID, i.ID, value)
	case "unlabel":
		err = s.opts.Issues.RemoveIssueLabel(ctx, pc.repo.ID, i.ID, value)
	case "assign", "unassign":
		var uid int64
		roster, rerr := s.opts.Courses.Roster(ctx, pc.c.ID)
		if rerr != nil {
			s.fail(w, "load roster", rerr)
			return
		}
		for _, m := range roster {
			if !m.Pending() && strings.EqualFold(m.Username, value) {
				uid = m.UserID
			}
		}
		if uid == 0 {
			s.renderIssue(w, r, pc, i, "Choose an enrolled member of this course.", http.StatusBadRequest)
			return
		}
		err = s.opts.Issues.SetIssueAssignee(ctx, i.ID, uid, op == "assign")
	case "milestone":
		err = s.opts.Issues.SetIssueMilestone(ctx, pc.repo.ID, i.ID, value)
		if errors.Is(err, store.ErrNotFound) {
			s.renderIssue(w, r, pc, i, "No such milestone.", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.fail(w, "edit issue", err)
		return
	}
	http.Redirect(w, r, pc.issuesBase()+"/"+itoa(i.Number), http.StatusSeeOther)
}

var closesRef = regexp.MustCompile(`(?i)\b(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)\s+#(\d{1,9})\b`)

// referencedIssues extracts the issue numbers named by closing keywords
// ("Fixes #3") in text.
func referencedIssues(text string) []int {
	var out []int
	seen := map[int]bool{}
	for _, m := range closesRef.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// closeReferencedIssues closes the issues a merged pull request names. It is
// best effort: the merge already happened, so failures are only logged.
func (s *Server) closeReferencedIssues(r *http.Request, pc pullCtx, pull store.PullRequest) {
	if s.opts.Issues == nil {
		return
	}
	nums := referencedIssues(pull.Title + "\n" + pull.Body)
	if len(nums) == 0 {
		return
	}
	closed, err := s.opts.Issues.CloseIssues(r.Context(), pc.repo.ID, nums)
	if err != nil {
		s.log.Error("close referenced issues", "err", err)
	}
	for _, n := range closed {
		s.audit(r, pc.user, "issue.closed", pc.c.Slug+"/"+pc.repo.Name, "#"+itoa(n)+" by pull #"+itoa(pull.Number))
	}
}
