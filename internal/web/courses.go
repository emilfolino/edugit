package web

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

const (
	inviteLifetime = 14 * 24 * time.Hour
	invitePrefix   = "inv_"
	maxEnrollBatch = 500
)

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

// CourseStore is the course persistence the course pages need.
type CourseStore interface {
	CreateCourse(ctx context.Context, slug, title, term string) (store.Course, error)
	CourseBySlug(ctx context.Context, slug string) (store.Course, error)
	SetArchived(ctx context.Context, courseID int64, archived bool) error
	UserCourses(ctx context.Context, userID int64) ([]store.CourseRole, error)
	AllCourses(ctx context.Context) ([]store.CourseRole, error)
	Roster(ctx context.Context, courseID int64) ([]store.Member, error)
	EnrollEmail(ctx context.Context, courseID int64, email, role, source string) error
	RemoveEnrollment(ctx context.Context, courseID int64, email string) error
	RemoveMembership(ctx context.Context, courseID, userID int64) error
	UserByEmail(ctx context.Context, email string) (store.User, error)
	SetInvite(ctx context.Context, courseID int64, tokenHash string, expires time.Time) error
	InviteExpiry(ctx context.Context, courseID int64) (time.Time, error)
	RevokeInvite(ctx context.Context, courseID int64) error
	InviteCourse(ctx context.Context, tokenHash string, now time.Time) (store.Course, error)
	JoinByInvite(ctx context.Context, tokenHash string, userID int64, now time.Time) (store.Course, error)
}

func (s *Server) routeCourses(mux *http.ServeMux) {
	mux.HandleFunc("POST /courses", s.createCourse)
	mux.HandleFunc("GET /courses/{slug}", s.coursePage)
	mux.HandleFunc("POST /courses/{slug}/archive", s.archiveCourse)
	mux.HandleFunc("POST /courses/{slug}/enroll", s.enroll)
	mux.HandleFunc("POST /courses/{slug}/role", s.setRole)
	mux.HandleFunc("POST /courses/{slug}/remove", s.removeMember)
	mux.HandleFunc("POST /courses/{slug}/invite", s.createInvite)
	mux.HandleFunc("POST /courses/{slug}/invite/revoke", s.revokeInvite)
	if s.opts.Repos != nil && s.opts.Disk != nil {
		mux.HandleFunc("POST /courses/{slug}/repos", s.createRepo)
		mux.HandleFunc("POST /courses/{slug}/repos/{repo}/template", s.setTemplate)
	}
	if s.opts.Repos != nil && s.opts.Disk != nil && s.opts.Assignments != nil {
		s.routeAssignments(mux)
	}
	if s.opts.Repos != nil && s.opts.Pulls != nil && s.opts.PullGit != nil {
		s.routePulls(mux)
	}
	mux.HandleFunc("GET /join/{token}", s.joinPage)
	mux.HandleFunc("POST /join/{token}", s.join)
}

func (s *Server) principal(ctx context.Context, u store.User) (authz.Principal, error) {
	return s.opts.Authz.Principal(ctx, u)
}

// loadCourse finds the course named in the path and checks that u may do
// action on it. Courses the user cannot view are reported as missing so
// their existence is not revealed.
func (s *Server) loadCourse(w http.ResponseWriter, r *http.Request, u store.User, action authz.Action) (store.Course, authz.Principal, bool) {
	c, err := s.opts.Courses.CourseBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Course{}, authz.Principal{}, false
	}
	var p authz.Principal
	if err == nil {
		p, err = s.principal(r.Context(), u)
	}
	if err != nil {
		s.fail(w, "load course", err)
		return store.Course{}, authz.Principal{}, false
	}
	res := authz.Resource{CourseID: c.ID}
	switch {
	case !authz.Can(p, authz.CourseView, res):
		http.NotFound(w, r)
	case !authz.Can(p, action, res):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		return c, p, true
	}
	return store.Course{}, authz.Principal{}, false
}

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, u store.User) {
	p := s.newPage(r, "edugit")
	var err error
	if u.IsAdmin {
		p.Courses, err = s.opts.Courses.AllCourses(r.Context())
	} else {
		p.Courses, err = s.opts.Courses.UserCourses(r.Context(), u.ID)
	}
	if err != nil {
		s.fail(w, "list courses", err)
		return
	}
	s.render(w, "index.html", p, http.StatusOK)
}

func (s *Server) renderCourse(w http.ResponseWriter, r *http.Request, u store.User, c store.Course, pr authz.Principal, p page, status int) {
	res := authz.Resource{CourseID: c.ID}
	p.Course = c
	p.CanManage = authz.Can(pr, authz.CourseManage, res)
	p.CanRoster = authz.Can(pr, authz.RosterView, res)
	if s.opts.Repos != nil {
		var err error
		if p.Repos, err = s.repoViews(r, u, c, pr); err != nil {
			s.fail(w, "list repos", err)
			return
		}
		p.CanCreateRepo = s.opts.Disk != nil && !c.Archived && authz.Can(pr, authz.RepoCreate, res)
	}
	if s.opts.Assignments != nil && s.opts.Disk != nil {
		var err error
		if p.Assignments, err = s.opts.Assignments.CourseAssignments(r.Context(), c.ID); err != nil {
			s.fail(w, "list assignments", err)
			return
		}
		p.CanAssign = !c.Archived && authz.Can(pr, authz.AssignmentManage, res)
	}
	if p.CanRoster {
		var err error
		if p.Roster, err = s.opts.Courses.Roster(r.Context(), c.ID); err != nil {
			s.fail(w, "load roster", err)
			return
		}
		if exp, err := s.opts.Courses.InviteExpiry(r.Context(), c.ID); err == nil {
			p.InviteExpires = exp
		}
	}
	s.render(w, "course.html", p, status)
}

func (s *Server) coursePage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.CourseView)
	if !ok {
		return
	}
	s.renderCourse(w, r, u, c, pr, s.newPage(r, c.Title), http.StatusOK)
}

func (s *Server) createCourse(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	pr, err := s.principal(r.Context(), u)
	if err != nil {
		s.fail(w, "create course", err)
		return
	}
	if !authz.Can(pr, authz.CourseCreate, authz.Resource{}) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("slug"))
	title := strings.TrimSpace(r.PostFormValue("title"))
	term := strings.TrimSpace(r.PostFormValue("term"))
	admin, aerr := parseEmail(r.PostFormValue("admin"))
	var msg string
	switch {
	case !slugRE.MatchString(slug):
		msg = "The short name must be 2-40 lowercase letters, digits or hyphens."
	case title == "" || len(title) > 120 || len(term) > 40:
		msg = "Give the course a title (up to 120 characters) and a short term."
	case aerr != nil:
		msg = "Enter the course admin's email address."
	case !s.opts.Domains.AllowsStaffRole(admin):
		msg = "That address is not eligible for a staff role."
	}
	if msg != "" {
		s.dashboardError(w, r, msg)
		return
	}
	c, err := s.opts.Courses.CreateCourse(r.Context(), slug, title, term)
	if err != nil {
		if _, e := s.opts.Courses.CourseBySlug(r.Context(), slug); e == nil {
			s.dashboardError(w, r, "A course with that short name already exists.")
			return
		}
		s.fail(w, "create course", err)
		return
	}
	if err := s.opts.Courses.EnrollEmail(r.Context(), c.ID, admin, "course_admin", "manual"); err != nil {
		s.fail(w, "enroll course admin", err)
		return
	}
	s.audit(r, u, "course.create", slug, "course admin "+admin)
	http.Redirect(w, r, "/courses/"+slug, http.StatusSeeOther)
}

func (s *Server) dashboardError(w http.ResponseWriter, r *http.Request, msg string) {
	p := s.newPage(r, "edugit")
	p.Error = msg
	courses, err := s.opts.Courses.AllCourses(r.Context())
	if err != nil {
		s.fail(w, "list courses", err)
		return
	}
	p.Courses = courses
	s.render(w, "index.html", p, http.StatusBadRequest)
}

func (s *Server) archiveCourse(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, _, ok := s.loadCourse(w, r, u, authz.CourseManage)
	if !ok {
		return
	}
	archived := r.PostFormValue("archived") == "1"
	if err := s.opts.Courses.SetArchived(r.Context(), c.ID, archived); err != nil {
		s.fail(w, "archive course", err)
		return
	}
	s.audit(r, u, "course.archive", c.Slug, "archived="+strconv.FormatBool(archived))
	http.Redirect(w, r, "/courses/"+c.Slug, http.StatusSeeOther)
}

// entry is one parsed line of an enrolment list.
type entry struct{ email, role string }

// parseEnrollList reads one person per line: an email, optionally followed
// by a role after a comma, semicolon or tab (so spreadsheet CSV exports
// work). Lines without an "@" (a header row) are ignored. Lines that look
// like emails but are invalid are returned in bad.
func parseEnrollList(text, defaultRole string) (list []entry, bad []string) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ';' || r == '\t' })
		if len(fields) == 0 || !strings.Contains(fields[0], "@") {
			continue
		}
		email, err := parseEmail(fields[0])
		if err != nil {
			bad = append(bad, strings.TrimSpace(fields[0]))
			continue
		}
		role := defaultRole
		if len(fields) > 1 {
			if r := strings.ToLower(strings.TrimSpace(fields[1])); r != "" {
				role = strings.ReplaceAll(r, " ", "_")
			}
		}
		list = append(list, entry{email, role})
	}
	return list, bad
}

// parseEmail returns the lowercased bare address.
func parseEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", errors.New("invalid email")
	}
	return strings.ToLower(s), nil
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.RosterEnroll)
	if !ok {
		return
	}
	canManage := authz.Can(pr, authz.CourseManage, authz.Resource{CourseID: c.ID})
	p := s.newPage(r, c.Title)
	if c.Archived {
		p.Error = "This course is archived. Restore it to change enrolment."
		s.renderCourse(w, r, u, c, pr, p, http.StatusBadRequest)
		return
	}
	defRole := r.PostFormValue("role")
	if defRole == "" {
		defRole = "student"
	}
	list, bad := parseEnrollList(r.PostFormValue("emails"), defRole)
	if len(list) == 0 && len(bad) == 0 {
		p.Error = "Enter one or more email addresses."
		s.renderCourse(w, r, u, c, pr, p, http.StatusBadRequest)
		return
	}
	if len(list) > maxEnrollBatch {
		p.Error = "Too many people at once; enrol at most 500 per batch."
		s.renderCourse(w, r, u, c, pr, p, http.StatusBadRequest)
		return
	}
	source := "manual"
	if len(list) > 1 {
		source = "import"
	}
	var skipped []string
	for _, e := range bad {
		skipped = append(skipped, e+" (invalid address)")
	}
	done := 0
	for _, e := range list {
		role := authz.ParseRole(e.role)
		switch {
		case role == authz.RoleNone:
			skipped = append(skipped, e.email+" (unknown role "+e.role+")")
			continue
		case role != authz.RoleStudent && !canManage:
			skipped = append(skipped, e.email+" (only a course admin assigns staff roles)")
			continue
		case role != authz.RoleStudent && !s.opts.Domains.AllowsStaffRole(e.email):
			skipped = append(skipped, e.email+" (address not eligible for a staff role)")
			continue
		}
		if err := s.opts.Courses.EnrollEmail(r.Context(), c.ID, e.email, e.role, source); err != nil {
			s.fail(w, "enroll", err)
			return
		}
		done++
	}
	s.audit(r, u, "roster.enroll", c.Slug, "enrolled="+itoa(done)+" skipped="+itoa(len(skipped)))
	p.Notice = "Enrolled " + itoa(done) + "."
	p.Skipped = skipped
	s.renderCourse(w, r, u, c, pr, p, http.StatusOK)
}

// member resolves the roster entry for the posted email.
func (s *Server) member(ctx context.Context, courseID int64, email string) (store.Member, bool, error) {
	roster, err := s.opts.Courses.Roster(ctx, courseID)
	if err != nil {
		return store.Member{}, false, err
	}
	for _, m := range roster {
		if strings.EqualFold(m.Email, email) {
			return m, true, nil
		}
	}
	return store.Member{}, false, nil
}

// changeMember is the shared guard of setRole and removeMember. Staff
// members may only be changed by a course admin, and nobody changes their
// own enrolment, so a course cannot lose its last admin by accident.
func (s *Server) changeMember(w http.ResponseWriter, r *http.Request) (u store.User, c store.Course, m store.Member, ok bool) {
	u, ok = s.requirePost(w, r)
	if !ok {
		return
	}
	var pr authz.Principal
	c, pr, ok = s.loadCourse(w, r, u, authz.RosterEnroll)
	if !ok {
		return
	}
	ok = false
	m, found, err := s.member(r.Context(), c.ID, r.PostFormValue("email"))
	switch {
	case err != nil:
		s.fail(w, "load member", err)
	case !found:
		http.NotFound(w, r)
	case c.Archived:
		http.Error(w, "course is archived", http.StatusBadRequest)
	case m.UserID == u.ID:
		http.Error(w, "you cannot change your own enrolment", http.StatusBadRequest)
	case m.Role != "student" && !authz.Can(pr, authz.CourseManage, authz.Resource{CourseID: c.ID}):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		ok = true
	}
	return
}

func (s *Server) setRole(w http.ResponseWriter, r *http.Request) {
	u, c, m, ok := s.changeMember(w, r)
	if !ok {
		return
	}
	role := r.PostFormValue("role")
	pr, err := s.principal(r.Context(), u)
	if err != nil {
		s.fail(w, "set role", err)
		return
	}
	switch {
	case authz.ParseRole(role) == authz.RoleNone:
		http.Error(w, "unknown role", http.StatusBadRequest)
		return
	case role != "student" && !authz.Can(pr, authz.CourseManage, authz.Resource{CourseID: c.ID}):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case role != "student" && !s.opts.Domains.AllowsStaffRole(m.Email):
		http.Error(w, "that address is not eligible for a staff role", http.StatusBadRequest)
		return
	}
	if err := s.opts.Courses.EnrollEmail(r.Context(), c.ID, m.Email, role, "manual"); err != nil {
		s.fail(w, "set role", err)
		return
	}
	s.audit(r, u, "roster.role", c.Slug, m.Email+" -> "+role)
	http.Redirect(w, r, "/courses/"+c.Slug, http.StatusSeeOther)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	u, c, m, ok := s.changeMember(w, r)
	if !ok {
		return
	}
	var err error
	if m.Pending() {
		err = s.opts.Courses.RemoveEnrollment(r.Context(), c.ID, m.Email)
	} else {
		err = s.opts.Courses.RemoveMembership(r.Context(), c.ID, m.UserID)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "remove member", err)
		return
	}
	s.audit(r, u, "roster.remove", c.Slug, m.Email)
	http.Redirect(w, r, "/courses/"+c.Slug, http.StatusSeeOther)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, pr, ok := s.loadCourse(w, r, u, authz.RosterEnroll)
	if !ok {
		return
	}
	if c.Archived {
		http.Error(w, "course is archived", http.StatusBadRequest)
		return
	}
	secret, hash, err := auth.NewSecret(invitePrefix)
	if err == nil {
		err = s.opts.Courses.SetInvite(r.Context(), c.ID, hash, time.Now().Add(inviteLifetime))
	}
	if err != nil {
		s.fail(w, "create invite", err)
		return
	}
	s.audit(r, u, "invite.create", c.Slug, "")
	p := s.newPage(r, c.Title)
	p.Link = s.opts.PublicURL + "/join/" + secret
	s.renderCourse(w, r, u, c, pr, p, http.StatusOK)
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, _, ok := s.loadCourse(w, r, u, authz.RosterEnroll)
	if !ok {
		return
	}
	if err := s.opts.Courses.RevokeInvite(r.Context(), c.ID); err != nil {
		s.fail(w, "revoke invite", err)
		return
	}
	s.audit(r, u, "invite.revoke", c.Slug, "")
	http.Redirect(w, r, "/courses/"+c.Slug, http.StatusSeeOther)
}

// joinPage asks for confirmation before enrolling, so that link previews
// and prefetching cannot enrol anyone.
func (s *Server) joinPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	c, err := s.opts.Courses.InviteCourse(r.Context(), auth.HashSecret(r.PathValue("token")), time.Now())
	if err != nil || c.Archived {
		if err == nil || errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.fail(w, "invite", err)
		return
	}
	p := s.newPage(r, "Join "+c.Title)
	p.Course = c
	p.Link = r.PathValue("token")
	s.render(w, "join.html", p, http.StatusOK)
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requirePost(w, r)
	if !ok {
		return
	}
	c, err := s.opts.Courses.JoinByInvite(r.Context(), auth.HashSecret(r.PathValue("token")), u.ID, time.Now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "join", err)
	default:
		s.audit(r, u, "invite.join", c.Slug, "")
		http.Redirect(w, r, "/courses/"+c.Slug, http.StatusSeeOther)
	}
}
