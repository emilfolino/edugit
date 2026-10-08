// Package authz is the single place that decides what a user may do.
//
// Every decision is course-scoped except for the global admin: a Principal
// carries a role per course, and a Resource names the course it belongs to,
// so a role in one course can never leak into another. Can is pure (no I/O)
// so it can be tested exhaustively; Authorizer gathers the facts from a
// Source and calls it.
package authz

// Role is a user's role within one course.
type Role int

// Roles in increasing order of privilege. RoleNone means not enrolled.
const (
	RoleNone Role = iota
	RoleStudent
	RoleTeacher
	RoleCourseAdmin
)

// ParseRole converts a stored role name; unknown names map to RoleNone so
// that a bad row fails closed.
func ParseRole(s string) Role {
	switch s {
	case "student":
		return RoleStudent
	case "teacher":
		return RoleTeacher
	case "course_admin":
		return RoleCourseAdmin
	}
	return RoleNone
}

// Action is something a principal may attempt.
type Action string

// Actions. Course actions apply to a course resource, repo actions to a repo.
const (
	CourseCreate Action = "course.create" // global admin only
	CourseView   Action = "course.view"
	CourseManage Action = "course.manage" // settings, roster, roles
	RosterView   Action = "roster.view"   // staff
	RosterEnroll Action = "roster.enroll" // staff, for students; roles need CourseManage
	RepoCreate   Action = "repo.create"
	RepoRead     Action = "repo.read" // clone, fetch, browse
	RepoWrite    Action = "repo.write"
	RepoAdmin    Action = "repo.admin" // settings, protection, delete
)

// RepoKind mirrors repos.kind.
type RepoKind string

// Repository kinds.
const (
	KindTeacher RepoKind = "teacher"
	KindStudent RepoKind = "student"
	KindTeam    RepoKind = "team"
)

// Principal is the acting user. Roles maps course ID to the user's role in
// that course; absent means RoleNone.
type Principal struct {
	UserID  int64
	IsAdmin bool
	Roles   map[int64]Role
}

// Resource is the object of an action. CourseID is always set; the repo
// fields matter only for repo actions (Repo true).
type Resource struct {
	CourseID int64

	Repo       bool
	Kind       RepoKind
	IsTemplate bool
	Archived   bool
	// IsMember reports an explicit repo_members row for the principal.
	IsMember bool
}

// Can reports whether p may perform a on res. It denies by default.
//
// Repo rules:
//   - Teacher repos: staff (teacher, course admin) read and write; students
//     may only read templates (starter code), never write.
//   - Student and team repos: members read and write, provided they are still
//     enrolled in the course; staff read and write too (feedback branches),
//     with branch protection still applying to pushes.
//   - Archived repos are read-only for everyone, global admin included.
//   - Staff manage repo settings in their course; students never do.
func Can(p Principal, a Action, res Resource) bool {
	if a == CourseCreate {
		return p.IsAdmin
	}
	if a == RepoWrite && res.Archived {
		return false
	}
	if p.IsAdmin {
		return true
	}
	role := p.Roles[res.CourseID]
	staff := role >= RoleTeacher

	switch a {
	case CourseView:
		return role > RoleNone
	case CourseManage:
		return role == RoleCourseAdmin
	case RosterView, RosterEnroll, RepoCreate:
		return staff
	case RepoAdmin:
		return staff
	case RepoRead, RepoWrite:
		if !res.Repo || role == RoleNone {
			return false
		}
		if staff {
			return true
		}
		switch res.Kind {
		case KindTeacher:
			return a == RepoRead && res.IsTemplate
		case KindStudent, KindTeam:
			return res.IsMember
		}
	}
	return false
}
