package web

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

// GradeStore is the rubric and grade persistence the grading pages need.
type GradeStore interface {
	Rubric(ctx context.Context, assignmentID int64) ([]store.Criterion, error)
	AddCriterion(ctx context.Context, assignmentID int64, title string, maxPoints int) error
	DeleteCriterion(ctx context.Context, assignmentID, id int64) error
	GradeFor(ctx context.Context, repoID int64) (store.Grade, error)
	GradeTotals(ctx context.Context, assignmentID int64) (map[int64]int, error)
	SaveGrade(ctx context.Context, assignmentID, repoID, graderID int64, feedback string, scores map[int64]int) error
	GradeRows(ctx context.Context, assignmentID int64) ([]store.GradeRow, error)
}

// gradingView is the data of the grading pages.
type gradingView struct {
	Base    string // /courses/{slug}/assignments/{a}/grading
	Rubric  []store.Criterion
	Max     int
	Repos   []gradingRepo
	Repo    string // set on the single-repo form
	Scores  map[int64]int
	Comment string
}

type gradingRepo struct {
	Name, Owner string
	Graded      bool
	Points      int
}

// gradeView is a student's view of their own grade.
type gradeView struct {
	Rubric   []store.Criterion
	Scores   map[int64]int
	Total    int
	Max      int
	Feedback string
}

func (s *Server) routeGrading(mux *http.ServeMux) {
	mux.HandleFunc("GET /courses/{slug}/assignments/{a}/grading", s.gradingPage)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/grading/rubric", s.addCriterion)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/grading/rubric/{id}/delete", s.deleteCriterion)
	mux.HandleFunc("GET /courses/{slug}/assignments/{a}/grading/{repo}", s.gradeForm)
	mux.HandleFunc("POST /courses/{slug}/assignments/{a}/grading/{repo}", s.saveGrade)
	mux.HandleFunc("GET /courses/{slug}/assignments/{a}/grades.csv", s.gradesCSV)
}

func maxPoints(rubric []store.Criterion) int {
	n := 0
	for _, c := range rubric {
		n += c.MaxPoints
	}
	return n
}

// loadGrading resolves the course and assignment for a staff grading request.
func (s *Server) loadGrading(w http.ResponseWriter, r *http.Request, post bool) (u store.User, c store.Course, a store.Assignment, ok bool) {
	if post {
		u, ok = s.requirePost(w, r)
	} else {
		u, ok = s.requireUser(w, r)
	}
	if !ok {
		return
	}
	if c, _, ok = s.loadCourse(w, r, u, authz.AssignmentManage); !ok {
		return
	}
	a, ok = s.loadAssignment(w, r, c)
	return
}

func (s *Server) gradingPage(w http.ResponseWriter, r *http.Request) {
	_, c, a, ok := s.loadGrading(w, r, false)
	if !ok {
		return
	}
	s.renderGrading(w, r, c, a, "", "", http.StatusOK)
}

func (s *Server) renderGrading(w http.ResponseWriter, r *http.Request, c store.Course, a store.Assignment, repo, msg string, status int) {
	ctx := r.Context()
	v := &gradingView{Base: "/courses/" + c.Slug + "/assignments/" + a.Slug + "/grading", Repo: repo}
	var err error
	if v.Rubric, err = s.opts.Grades.Rubric(ctx, a.ID); err != nil {
		s.fail(w, "load rubric", err)
		return
	}
	v.Max = maxPoints(v.Rubric)
	repos, err := s.opts.Assignments.AssignmentRepos(ctx, a.ID)
	if err != nil {
		s.fail(w, "list assignment repos", err)
		return
	}
	totals, err := s.opts.Grades.GradeTotals(ctx, a.ID)
	if err != nil {
		s.fail(w, "grade totals", err)
		return
	}
	for _, ar := range repos {
		pts, graded := totals[ar.RepoID]
		v.Repos = append(v.Repos, gradingRepo{Name: ar.Name, Owner: ar.Owner, Graded: graded, Points: pts})
		if ar.Name == repo {
			g, err := s.opts.Grades.GradeFor(ctx, ar.RepoID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				s.fail(w, "load grade", err)
				return
			}
			v.Scores, v.Comment = g.Scores, g.Feedback
		}
	}
	p := s.newPage(r, a.Title)
	p.Error = msg
	p.Course, p.Asg, p.Grading = c, &assignmentView{Assignment: a}, v
	s.render(w, "grading.html", p, status)
}

func (s *Server) addCriterion(w http.ResponseWriter, r *http.Request) {
	u, c, a, ok := s.loadGrading(w, r, true)
	if !ok {
		return
	}
	title := strings.TrimSpace(r.PostFormValue("title"))
	pts, err := strconv.Atoi(r.PostFormValue("max"))
	if title == "" || len(title) > 120 || err != nil || pts < 1 || pts > 1000 {
		s.renderGrading(w, r, c, a, "", "Give the criterion a title and 1-1000 points.", http.StatusBadRequest)
		return
	}
	if err := s.opts.Grades.AddCriterion(r.Context(), a.ID, title, pts); err != nil {
		s.fail(w, "add criterion", err)
		return
	}
	s.audit(r, u, "rubric.add", c.Slug+"/"+a.Slug, title)
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug+"/grading", http.StatusSeeOther)
}

func (s *Server) deleteCriterion(w http.ResponseWriter, r *http.Request) {
	u, c, a, ok := s.loadGrading(w, r, true)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.opts.Grades.DeleteCriterion(r.Context(), a.ID, id); err != nil {
		s.fail(w, "delete criterion", err)
		return
	}
	s.audit(r, u, "rubric.delete", c.Slug+"/"+a.Slug, strconv.FormatInt(id, 10))
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug+"/grading", http.StatusSeeOther)
}

// assignedRepo finds the named repo among the assignment's repos, writing 404
// if it is not one of them.
func (s *Server) assignedRepo(w http.ResponseWriter, r *http.Request, a store.Assignment) (store.AssignedRepo, bool) {
	repos, err := s.opts.Assignments.AssignmentRepos(r.Context(), a.ID)
	if err != nil {
		s.fail(w, "list assignment repos", err)
		return store.AssignedRepo{}, false
	}
	for _, ar := range repos {
		if ar.Name == r.PathValue("repo") {
			return ar, true
		}
	}
	http.NotFound(w, r)
	return store.AssignedRepo{}, false
}

func (s *Server) gradeForm(w http.ResponseWriter, r *http.Request) {
	_, c, a, ok := s.loadGrading(w, r, false)
	if !ok {
		return
	}
	ar, ok := s.assignedRepo(w, r, a)
	if !ok {
		return
	}
	s.renderGrading(w, r, c, a, ar.Name, "", http.StatusOK)
}

func (s *Server) saveGrade(w http.ResponseWriter, r *http.Request) {
	u, c, a, ok := s.loadGrading(w, r, true)
	if !ok {
		return
	}
	ar, ok := s.assignedRepo(w, r, a)
	if !ok {
		return
	}
	rubric, err := s.opts.Grades.Rubric(r.Context(), a.ID)
	if err != nil {
		s.fail(w, "load rubric", err)
		return
	}
	scores := map[int64]int{}
	for _, cr := range rubric {
		v := strings.TrimSpace(r.PostFormValue("score-" + strconv.FormatInt(cr.ID, 10)))
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > cr.MaxPoints {
			s.renderGrading(w, r, c, a, ar.Name, "“"+cr.Title+"”: give 0-"+strconv.Itoa(cr.MaxPoints)+" points.", http.StatusBadRequest)
			return
		}
		scores[cr.ID] = n
	}
	feedback := strings.TrimSpace(r.PostFormValue("feedback"))
	if len(feedback) > 10000 {
		s.renderGrading(w, r, c, a, ar.Name, "The feedback is too long.", http.StatusBadRequest)
		return
	}
	if err := s.opts.Grades.SaveGrade(r.Context(), a.ID, ar.RepoID, u.ID, feedback, scores); err != nil {
		s.fail(w, "save grade", err)
		return
	}
	s.audit(r, u, "grade.save", c.Slug+"/"+ar.Name, "")
	http.Redirect(w, r, "/courses/"+c.Slug+"/assignments/"+a.Slug+"/grading", http.StatusSeeOther)
}

// csvCell neutralises spreadsheet formulas in user-controlled text.
func csvCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func (s *Server) gradesCSV(w http.ResponseWriter, r *http.Request) {
	u, c, a, ok := s.loadGrading(w, r, false)
	if !ok {
		return
	}
	rubric, err := s.opts.Grades.Rubric(r.Context(), a.ID)
	if err != nil {
		s.fail(w, "load rubric", err)
		return
	}
	rows, err := s.opts.Grades.GradeRows(r.Context(), a.ID)
	if err != nil {
		s.fail(w, "grade rows", err)
		return
	}
	s.audit(r, u, "grade.export", c.Slug+"/"+a.Slug, "")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+c.Slug+"-"+a.Slug+`-grades.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"username", "email", "repository", "points", "max", "feedback"})
	max := strconv.Itoa(maxPoints(rubric))
	for _, g := range rows {
		pts := ""
		if g.Graded {
			pts = strconv.Itoa(g.Points)
		}
		cw.Write([]string{csvCell(g.Username), csvCell(g.Email), csvCell(g.Repo), pts, max, csvCell(g.Feedback)})
	}
	cw.Flush()
}

// ownGrade loads the viewer's grade for their own repo, if graded.
func (s *Server) ownGrade(ctx context.Context, a store.Assignment, repoID int64) (*gradeView, error) {
	g, err := s.opts.Grades.GradeFor(ctx, repoID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rubric, err := s.opts.Grades.Rubric(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	v := &gradeView{Rubric: rubric, Scores: g.Scores, Max: maxPoints(rubric), Feedback: g.Feedback}
	for _, cr := range rubric {
		v.Total += g.Scores[cr.ID]
	}
	return v, nil
}
