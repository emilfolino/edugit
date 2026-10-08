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
	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/store"
)

const deadlineLayout = "2006-01-02T15:04" // datetime-local, read and shown as UTC

var (
	teamNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	badNameRE  = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// AssignmentStore is the assignment persistence the assignment pages need.
type AssignmentStore interface {
	CreateAssignment(ctx context.Context, a store.Assignment) (store.Assignment, error)
	CourseAssignments(ctx context.Context, courseID int64) ([]store.Assignment, error)
	AssignmentBySlug(ctx context.Context, courseID int64, slug string) (store.Assignment, error)
	AssignmentRepoFor(ctx context.Context, assignmentID, userID int64) (store.Repo, error)
	AssignmentRepos(ctx context.Context, assignmentID int64) ([]store.AssignedRepo, error)
	CreateAssignmentRepo(ctx context.Context, a store.Assignment, name string, userID int64) (store.Repo, error)
	CreateTeam(ctx context.Context, a store.Assignment, teamName, repoName string, userID int64) (store.Team, error)
	DeleteTeam(ctx context.Context, teamID int64) error
	JoinTeam(ctx context.Context, a store.Assignment, teamID, userID int64) error
	LeaveTeam(ctx context.Context, assignmentID, userID int64) error
	Teams(ctx context.Context, assignmentID int64) ([]store.Team, error)
	SetExtension(ctx context.Context, assignmentID, userID int64, deadline time.Time) error
	EffectiveDeadline(ctx context.Context, a store.Assignment, userID int64) (time.Time, error)
	PendingStudents(ctx context.Context, a store.Assignment) ([]store.User, error)
}

// assignmentView is the assignment page's data.
type assignmentView struct {
	store.Assignment
	Effective time.Time // the viewer's deadline, extensions included
	Open      bool      // new repos and teams may still be created by the viewer
	Own       *repoView // the viewer's repo, if any
	Teams     []store.Team
	InTeam    bool
	Repos     []store.AssignedRepo
	Pending   int
	CanManage bool
	CanAccept bool
}

func (s *Server) routeAssignments(mux *http.ServeMux) {
	mux.HandleFunc("POST /courses/{slug}/assignments", s.createAssignment)
	mux.HandleFunc("GET /courses/{slug}/assignments/{a}", s.assignmentPage)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/accept", s.acceptAssignment)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/generate", s.generateAssignment)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/extend", s.extendAssignment)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/teams", s.createTeam)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/teams/{id}/join", s.joinTeam)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/teams/leave", s.leaveTeam)
}

// repoName builds the generated repo name <assignment>-<suffix>, sanitised to
// a valid repo name.
func repoName(assignment, suffix string) string {
	suffix = strings.Trim(badNameRE.ReplaceAllString(strings.ToLower(suffix), "-"), "-._")
	name := assignment + "-" + suffix
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

func parseDeadline(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation(deadlineLayout, v, time.UTC)
}

func (s *Server) createAssignment(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.AssignmentManage)
	if !ok {
		return
	}
	a := store.Assignment{
		CourseID: c.ID,
		Slug:     strings.TrimSpace(r.PostFormValue("slug")),
		Title:    strings.TrimSpace(r.PostFormValue("title")),
		Mode:     r.PostFormValue("mode"),
		History:  r.PostFormValue("history"),
		TeamSize: 1,
	}
	deadline, derr := parseDeadline(r.PostFormValue("deadline"))
	a.Deadline = deadline
	if a.Mode == "team" {
		a.TeamSize, _ = strconv.Atoi(r.PostFormValue("team_size"))
	}
	var msg string
	tmpl, _, terr := s.opts.Repos.RepoByName(r.Context(), c.Slug, r.PostFormValue("template"), 0)
	switch {
	case c.Archived:
		msg = "This course is archived."
	case !slugRE.MatchString(a.Slug):
		msg = "The short name must be 2-40 lowercase letters, digits or hyphens."
	case a.Title == "" || len(a.Title) > 120:
		msg = "Give the assignment a title (up to 120 characters)."
	case a.Mode != "individual" && a.Mode != "team":
		msg = "Choose individual or team mode."
	case a.History != "fresh" && a.History != "copy":
		msg = "Choose fresh or copied history."
	case a.Mode == "team" && (a.TeamSize < 2 || a.TeamSize > 20):
		msg = "Team size must be between 2 and 20."
	case derr != nil:
		msg = "The deadline must be a date and time."
	case terr != nil || tmpl.Kind != "teacher":
		msg = "Choose a teacher repository as the template."
	}
	if msg == "" {
		a.TemplateRepoID = tmpl.ID
		if _, err := s.opts.Assignments.CreateAssignment(r.Context(), a); store.IsConflict(err) {
			msg = "An assignment with that short name already exists."
		} else if err != nil {
			s.fail(w, "create assignment", err)
			return
		}
	}
	if msg != "" {
		p := s.newPage(r, c.Title)
		p.Error = msg
		s.renderCourse(w, r, u, c, pr, p, http.StatusBadRequest)
		return
	}
	s.audit(r, u, "assignment.create", c.Slug+"/"+a.Slug, "mode="+a.Mode)
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug, http.StatusSeeOther)
}

// loadAssignment resolves the assignment in the path, writing 404 if absent.
func (s *Server) loadAssignment(w http.ResponseWriter, r *http.Request, c store.Course) (store.Assignment, bool) {
	a, err := s.opts.Assignments.AssignmentBySlug(r.Context(), c.ID, r.PathValue("a"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return a, false
	}
	if err != nil {
		s.fail(w, "load assignment", err)
		return a, false
	}
	return a, true
}

// open reports whether u may still create a repo or team for a: the course
// is live and the (extended) deadline has not passed.
func (s *Server) open(ctx context.Context, c store.Course, a store.Assignment, userID int64) (bool, time.Time, error) {
	d, err := s.opts.Assignments.EffectiveDeadline(ctx, a, userID)
	if err != nil {
		return false, d, err
	}
	return !c.Archived && (d.IsZero() || time.Now().Before(d)), d, nil
}

func (s *Server) assignmentPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.CourseView)
	if !ok {
		return
	}
	a, ok := s.loadAssignment(w, r, c)
	if !ok {
		return
	}
	s.renderAssignment(w, r, u, c, pr, a, s.newPage(r, a.Title), http.StatusOK)
}

func (s *Server) renderAssignment(w http.ResponseWriter, r *http.Request, u store.User, c store.Course, pr authz.Principal, a store.Assignment, p page, status int) {
	ctx := r.Context()
	res := authz.Resource{CourseID: c.ID}
	v := &assignmentView{Assignment: a}
	v.CanManage = authz.Can(pr, authz.AssignmentManage, res)
	v.CanAccept = authz.Can(pr, authz.AssignmentAccept, res)
	var err error
	if v.Open, v.Effective, err = s.open(ctx, c, a, u.ID); err != nil {
		s.fail(w, "assignment deadline", err)
		return
	}
	if rec, err := s.opts.Assignments.AssignmentRepoFor(ctx, a.ID, u.ID); err == nil {
		v.Own = &repoView{Name: rec.Name, Kind: rec.Kind, Archived: rec.Archived,
			CloneURL: s.baseURL(r) + "/git/" + c.Slug + "/" + rec.Name + ".git"}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "assignment repo", err)
		return
	}
	if a.Mode == "team" {
		if v.Teams, err = s.opts.Assignments.Teams(ctx, a.ID); err != nil {
			s.fail(w, "list teams", err)
			return
		}
		v.InTeam = v.Own != nil
	}
	if v.CanManage {
		if v.Repos, err = s.opts.Assignments.AssignmentRepos(ctx, a.ID); err != nil {
			s.fail(w, "list assignment repos", err)
			return
		}
		if a.Mode == "individual" {
			pending, err := s.opts.Assignments.PendingStudents(ctx, a)
			if err != nil {
				s.fail(w, "pending students", err)
				return
			}
			v.Pending = len(pending)
		}
	}
	p.Course, p.Asg = c, v
	s.render(w, "assignment.html", p, status)
}

// reject re-renders the assignment page with a message.
func (s *Server) reject(w http.ResponseWriter, r *http.Request, u store.User, c store.Course, pr authz.Principal, a store.Assignment, msg string) {
	p := s.newPage(r, a.Title)
	p.Error = msg
	s.renderAssignment(w, r, u, c, pr, a, p, http.StatusBadRequest)
}

// generateRepo creates the disk repo for rec from the assignment template,
// reporting whether it succeeded; the caller undoes the metadata on failure.
func (s *Server) generateRepo(ctx context.Context, c store.Course, a store.Assignment, name string) error {
	return s.opts.Disk.Generate(ctx, c.Slug, a.TemplateName, name, a.History == "fresh")
}

// provision makes the metadata row and the disk repo for one student's
// individual repo, rolling the row back if the disk step fails.
func (s *Server) provision(ctx context.Context, c store.Course, a store.Assignment, user store.User) (string, error) {
	name := repoName(a.Slug, user.Username)
	rec, err := s.opts.Assignments.CreateAssignmentRepo(ctx, a, name, user.ID)
	if err != nil {
		return name, err
	}
	if err := s.generateRepo(ctx, c, a, name); err != nil {
		if derr := s.opts.Repos.DeleteRepo(ctx, rec.ID); derr != nil {
			s.log.Error("roll back assignment repo", "course", c.Slug, "repo", name, "err", derr)
		}
		return name, err
	}
	return name, nil
}

func (s *Server) acceptAssignment(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.AssignmentAccept)
	if !ok {
		return
	}
	a, ok := s.loadAssignment(w, r, c)
	if !ok {
		return
	}
	open, _, err := s.open(r.Context(), c, a, u.ID)
	switch {
	case err != nil:
		s.fail(w, "assignment deadline", err)
		return
	case a.Mode != "individual":
		s.reject(w, r, u, c, pr, a, "This is a team assignment; create or join a team.")
		return
	case !open:
		s.reject(w, r, u, c, pr, a, "The deadline has passed.")
		return
	}
	name, err := s.provision(r.Context(), c, a, u)
	switch {
	case errors.Is(err, store.ErrHasRepo):
		s.reject(w, r, u, c, pr, a, "You already have a repository for this assignment.")
		return
	case store.IsConflict(err):
		s.reject(w, r, u, c, pr, a, "A repository with that name already exists.")
		return
	case err != nil:
		s.fail(w, "accept assignment", err)
		return
	}
	s.audit(r, u, "assignment.accept", c.Slug+"/"+a.Slug, "repo="+name)
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug, http.StatusSeeOther)
}

func (s *Server) generateAssignment(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.AssignmentManage)
	if !ok {
		return
	}
	a, ok := s.loadAssignment(w, r, c)
	if !ok {
		return
	}
	if c.Archived || a.Mode != "individual" {
		s.reject(w, r, u, c, pr, a, "Bulk generation needs a live course and an individual assignment.")
		return
	}
	pending, err := s.opts.Assignments.PendingStudents(r.Context(), a)
	if err != nil {
		s.fail(w, "pending students", err)
		return
	}
	var made int
	p := s.newPage(r, a.Title)
	for _, st := range pending {
		if _, err := s.provision(r.Context(), c, a, st); err != nil {
			s.log.Error("bulk generate", "course", c.Slug, "assignment", a.Slug, "user", st.Username, "err", err)
			p.Skipped = append(p.Skipped, st.Username)
			continue
		}
		made++
	}
	s.audit(r, u, "assignment.generate", c.Slug+"/"+a.Slug, "created="+strconv.Itoa(made)+" failed="+strconv.Itoa(len(p.Skipped)))
	p.Notice = "Created " + strconv.Itoa(made) + " repositories."
	s.renderAssignment(w, r, u, c, pr, a, p, http.StatusOK)
}

func (s *Server) extendAssignment(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.AssignmentManage)
	if !ok {
		return
	}
	a, ok := s.loadAssignment(w, r, c)
	if !ok {
		return
	}
	email, eerr := parseEmail(r.PostFormValue("email"))
	until, derr := parseDeadline(r.PostFormValue("deadline"))
	target, terr := s.opts.Courses.UserByEmail(r.Context(), email)
	switch {
	case eerr != nil:
		s.reject(w, r, u, c, pr, a, "Enter the student's email address.")
		return
	case derr != nil:
		s.reject(w, r, u, c, pr, a, "The deadline must be a date and time; leave it empty to remove an extension.")
		return
	case terr != nil:
		s.reject(w, r, u, c, pr, a, "No signed-in user has that address.")
		return
	}
	if err := s.opts.Assignments.SetExtension(r.Context(), a.ID, target.ID, until); err != nil {
		s.fail(w, "set extension", err)
		return
	}
	s.audit(r, u, "assignment.extend", c.Slug+"/"+a.Slug, target.Username+" until "+until.Format(deadlineLayout))
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug, http.StatusSeeOther)
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.AssignmentAccept)
	if !ok {
		return
	}
	a, ok := s.loadAssignment(w, r, c)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	open, _, err := s.open(r.Context(), c, a, u.ID)
	switch {
	case err != nil:
		s.fail(w, "assignment deadline", err)
		return
	case a.Mode != "team":
		s.reject(w, r, u, c, pr, a, "This is an individual assignment.")
		return
	case !open:
		s.reject(w, r, u, c, pr, a, "The deadline has passed.")
		return
	case !teamNameRE.MatchString(name):
		s.reject(w, r, u, c, pr, a, "Team names are lowercase letters, digits and hyphens (up to 40).")
		return
	}
	repo := repoName(a.Slug, name)
	if !gitserver.ValidName(repo) {
		s.reject(w, r, u, c, pr, a, "That team name does not make a valid repository name.")
		return
	}
	t, err := s.opts.Assignments.CreateTeam(r.Context(), a, name, repo, u.ID)
	switch {
	case errors.Is(err, store.ErrAlreadyInTeam):
		s.reject(w, r, u, c, pr, a, "You are already in a team for this assignment.")
		return
	case store.IsConflict(err):
		s.reject(w, r, u, c, pr, a, "A team or repository with that name already exists.")
		return
	case err != nil:
		s.fail(w, "create team", err)
		return
	}
	if err := s.generateRepo(r.Context(), c, a, repo); err != nil {
		if derr := s.opts.Assignments.DeleteTeam(r.Context(), t.ID); derr != nil {
			s.log.Error("roll back team", "course", c.Slug, "team", name, "err", derr)
		}
		s.fail(w, "generate team repo", err)
		return
	}
	s.audit(r, u, "team.create", c.Slug+"/"+a.Slug, "team="+name)
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug, http.StatusSeeOther)
}

func (s *Server) joinTeam(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.AssignmentAccept)
	if !ok {
		return
	}
	a, ok := s.loadAssignment(w, r, c)
	if !ok {
		return
	}
	id, perr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if perr != nil || a.Mode != "team" {
		http.NotFound(w, r)
		return
	}
	open, _, err := s.open(r.Context(), c, a, u.ID)
	if err != nil {
		s.fail(w, "assignment deadline", err)
		return
	}
	if !open {
		s.reject(w, r, u, c, pr, a, "The deadline has passed.")
		return
	}
	switch err := s.opts.Assignments.JoinTeam(r.Context(), a, id, u.ID); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrTeamFull):
		s.reject(w, r, u, c, pr, a, "That team is full.")
		return
	case errors.Is(err, store.ErrAlreadyInTeam):
		s.reject(w, r, u, c, pr, a, "You are already in a team for this assignment.")
		return
	case err != nil:
		s.fail(w, "join team", err)
		return
	}
	s.audit(r, u, "team.join", c.Slug+"/"+a.Slug, "team="+strconv.FormatInt(id, 10))
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug, http.StatusSeeOther)
}

func (s *Server) leaveTeam(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.AssignmentAccept)
	if !ok {
		return
	}
	a, ok := s.loadAssignment(w, r, c)
	if !ok {
		return
	}
	open, _, err := s.open(r.Context(), c, a, u.ID)
	if err != nil {
		s.fail(w, "assignment deadline", err)
		return
	}
	if !open {
		s.reject(w, r, u, c, pr, a, "The deadline has passed; ask a teacher to change your team.")
		return
	}
	switch err := s.opts.Assignments.LeaveTeam(r.Context(), a.ID, u.ID); {
	case errors.Is(err, store.ErrNotFound):
		s.reject(w, r, u, c, pr, a, "You are not in a team.")
		return
	case err != nil:
		s.fail(w, "leave team", err)
		return
	}
	s.audit(r, u, "team.leave", c.Slug+"/"+a.Slug, "")
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug, http.StatusSeeOther)
}
