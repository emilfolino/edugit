package web

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

// Bundler writes a git bundle of a repo. *gitserver.Repos satisfies it.
type Bundler interface {
	Bundle(ctx context.Context, course, name string, w io.Writer) error
}

type exportManifest struct {
	Course      exportCourse       `json:"course"`
	ExportedAt  time.Time          `json:"exported_at"`
	Repos       []exportRepo       `json:"repos"`
	Assignments []exportAssignment `json:"assignments"`
	Skipped     []string           `json:"skipped_repos,omitempty"`
}

type exportCourse struct {
	Slug, Title, Term string
	Archived          bool
}

type exportRepo struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	IsTemplate bool   `json:"is_template"`
	Bundle     string `json:"bundle,omitempty"`
}

type exportAssignment struct {
	Slug     string            `json:"slug"`
	Title    string            `json:"title"`
	Mode     string            `json:"mode"`
	History  string            `json:"history"`
	TeamSize int               `json:"team_size"`
	Template string            `json:"template"`
	Deadline *time.Time        `json:"deadline,omitempty"`
	Rubric   []exportCriterion `json:"rubric"`
}

type exportCriterion struct {
	Title     string `json:"title"`
	MaxPoints int    `json:"max_points"`
}

func (s *Server) routeExport(mux *http.ServeMux) {
	mux.HandleFunc("GET /courses/{slug}/export.zip", s.exportCourse)
}

// exportCourse streams a zip of the course: a manifest, the roster, the grades
// of each assignment and a git bundle per repo. Student repos are included only
// with ?repos=all.
func (s *Server) exportCourse(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	c, _, ok := s.loadCourse(w, r, u, authz.CourseManage)
	if !ok {
		return
	}
	ctx := r.Context()
	asgs, err := s.opts.Assignments.CourseAssignments(ctx, c.ID)
	var repos []store.RepoEntry
	var roster []store.Member
	if err == nil {
		repos, err = s.opts.Repos.CourseRepos(ctx, c.ID, u.ID)
	}
	if err == nil {
		roster, err = s.opts.Courses.Roster(ctx, c.ID)
	}
	m := exportManifest{Course: exportCourse{c.Slug, c.Title, c.Term, c.Archived}, ExportedAt: time.Now().UTC()}
	grades := map[string][]store.GradeRow{}
	for _, a := range asgs {
		if err != nil {
			break
		}
		ea := exportAssignment{Slug: a.Slug, Title: a.Title, Mode: a.Mode, History: a.History,
			TeamSize: a.TeamSize, Template: a.TemplateName, Rubric: []exportCriterion{}}
		if !a.Deadline.IsZero() {
			d := a.Deadline
			ea.Deadline = &d
		}
		var rubric []store.Criterion
		if rubric, err = s.opts.Grades.Rubric(ctx, a.ID); err != nil {
			break
		}
		for _, cr := range rubric {
			ea.Rubric = append(ea.Rubric, exportCriterion{cr.Title, cr.MaxPoints})
		}
		m.Assignments = append(m.Assignments, ea)
		grades[a.Slug], err = s.opts.Grades.GradeRows(ctx, a.ID)
	}
	if err != nil {
		s.fail(w, "export course", err)
		return
	}
	s.audit(r, u, "course.export", c.Slug, r.URL.Query().Get("repos"))

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+c.Slug+`-export.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	// Past this point the status is sent; failures can only truncate the zip.
	for _, e := range repos {
		if e.Kind != "teacher" && r.URL.Query().Get("repos") != "all" {
			continue
		}
		er := exportRepo{Name: e.Name, Kind: e.Kind, IsTemplate: e.IsTemplate}
		bw, err := zw.CreateHeader(&zip.FileHeader{Name: "repos/" + e.Name + ".bundle", Method: zip.Store})
		if err == nil {
			err = s.opts.Bundler.Bundle(ctx, c.Slug, e.Name, bw)
		}
		if err != nil {
			s.log.Warn("export bundle", "course", c.Slug, "repo", e.Name, "err", err)
			m.Skipped = append(m.Skipped, e.Name)
		} else {
			er.Bundle = "repos/" + e.Name + ".bundle"
		}
		m.Repos = append(m.Repos, er)
	}
	if f, err := zw.Create("roster.csv"); err == nil {
		cw := csv.NewWriter(f)
		cw.Write([]string{"email", "username", "name", "role"})
		for _, mb := range roster {
			cw.Write([]string{csvCell(mb.Email), csvCell(mb.Username), csvCell(mb.DisplayName), mb.Role})
		}
		cw.Flush()
	}
	for slug, rows := range grades {
		f, err := zw.Create("grades/" + slug + ".csv")
		if err != nil {
			continue
		}
		cw := csv.NewWriter(f)
		cw.Write([]string{"username", "email", "repository", "points", "feedback"})
		for _, g := range rows {
			pts := ""
			if g.Graded {
				pts = strconv.Itoa(g.Points)
			}
			cw.Write([]string{csvCell(g.Username), csvCell(g.Email), csvCell(g.Repo), pts, csvCell(g.Feedback)})
		}
		cw.Flush()
	}
	if f, err := zw.Create("course.json"); err == nil {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		enc.Encode(m)
	}
}
