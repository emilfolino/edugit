package authz

import "testing"

func TestCan(t *testing.T) {
	const course, other = 1, 2
	roles := func(r Role) Principal {
		return Principal{UserID: 7, Roles: map[int64]Role{course: r}}
	}
	admin := Principal{UserID: 1, IsAdmin: true}
	repo := func(kind RepoKind, tmpl, archived, member bool) Resource {
		return Resource{CourseID: course, Repo: true, Kind: kind, IsTemplate: tmpl, Archived: archived, IsMember: member}
	}
	courseRes := Resource{CourseID: course}
	reviewer := func(r Resource) Resource { r.IsReviewer = true; return r }

	tests := []struct {
		name string
		p    Principal
		a    Action
		res  Resource
		want bool
	}{
		{"admin creates course", admin, CourseCreate, Resource{}, true},
		{"teacher manages assignments", roles(RoleTeacher), AssignmentManage, courseRes, true},
		{"student cannot manage assignments", roles(RoleStudent), AssignmentManage, courseRes, false},
		{"admin manages assignments", admin, AssignmentManage, courseRes, true},
		{"student accepts", roles(RoleStudent), AssignmentAccept, courseRes, true},
		{"teacher cannot accept", roles(RoleTeacher), AssignmentAccept, courseRes, false},
		{"admin cannot accept", admin, AssignmentAccept, courseRes, false},
		{"student of other course cannot accept", Principal{Roles: map[int64]Role{other: RoleStudent}}, AssignmentAccept, courseRes, false},
		{"course admin cannot create course", roles(RoleCourseAdmin), CourseCreate, Resource{}, false},
		{"teacher cannot create course", roles(RoleTeacher), CourseCreate, Resource{}, false},
		{"admin views audit", admin, AuditView, Resource{}, true},
		{"course admin cannot view audit", roles(RoleCourseAdmin), AuditView, Resource{}, false},
		{"teacher cannot view audit", roles(RoleTeacher), AuditView, Resource{}, false},
		{"student cannot view audit", roles(RoleStudent), AuditView, Resource{}, false},

		{"admin views any course", admin, CourseView, courseRes, true},
		{"student views own course", roles(RoleStudent), CourseView, courseRes, true},
		{"stranger cannot view", roles(RoleNone), CourseView, courseRes, false},
		{"role in other course does not help", Principal{Roles: map[int64]Role{other: RoleCourseAdmin}}, CourseView, courseRes, false},

		{"course admin manages", roles(RoleCourseAdmin), CourseManage, courseRes, true},
		{"teacher cannot manage", roles(RoleTeacher), CourseManage, courseRes, false},
		{"student cannot manage", roles(RoleStudent), CourseManage, courseRes, false},
		{"admin manages", admin, CourseManage, courseRes, true},
		{"course admin of other course cannot manage", Principal{Roles: map[int64]Role{other: RoleCourseAdmin}}, CourseManage, courseRes, false},

		{"teacher views roster", roles(RoleTeacher), RosterView, courseRes, true},
		{"teacher enrolls", roles(RoleTeacher), RosterEnroll, courseRes, true},
		{"student cannot view roster", roles(RoleStudent), RosterView, courseRes, false},
		{"student cannot enroll", roles(RoleStudent), RosterEnroll, courseRes, false},
		{"staff of other course cannot view roster", Principal{Roles: map[int64]Role{other: RoleTeacher}}, RosterView, courseRes, false},
		{"admin views roster", admin, RosterView, courseRes, true},

		{"teacher creates repo", roles(RoleTeacher), RepoCreate, courseRes, true},
		{"student cannot create repo", roles(RoleStudent), RepoCreate, courseRes, false},

		{"teacher reads teacher repo", roles(RoleTeacher), RepoRead, repo(KindTeacher, false, false, false), true},
		{"teacher writes teacher repo", roles(RoleTeacher), RepoWrite, repo(KindTeacher, false, false, false), true},
		{"student reads template", roles(RoleStudent), RepoRead, repo(KindTeacher, true, false, false), true},
		{"student cannot read non-template teacher repo", roles(RoleStudent), RepoRead, repo(KindTeacher, false, false, false), false},
		{"student cannot write template", roles(RoleStudent), RepoWrite, repo(KindTeacher, true, false, false), false},
		{"student member of teacher repo still cannot write", roles(RoleStudent), RepoWrite, repo(KindTeacher, false, false, true), false},

		{"member reads own repo", roles(RoleStudent), RepoRead, repo(KindStudent, false, false, true), true},
		{"member writes own repo", roles(RoleStudent), RepoWrite, repo(KindStudent, false, false, true), true},
		{"other student cannot read", roles(RoleStudent), RepoRead, repo(KindStudent, false, false, false), false},
		{"other student cannot write", roles(RoleStudent), RepoWrite, repo(KindStudent, false, false, false), false},
		{"team member writes team repo", roles(RoleStudent), RepoWrite, repo(KindTeam, false, false, true), true},
		{"non-member cannot read team repo", roles(RoleStudent), RepoRead, repo(KindTeam, false, false, false), false},
		{"unenrolled former member loses access", roles(RoleNone), RepoRead, repo(KindStudent, false, false, true), false},
		{"teacher reads student repo", roles(RoleTeacher), RepoRead, repo(KindStudent, false, false, false), true},
		{"teacher writes student repo", roles(RoleTeacher), RepoWrite, repo(KindStudent, false, false, false), true},
		{"teacher of other course cannot read", Principal{Roles: map[int64]Role{other: RoleTeacher}}, RepoRead, repo(KindStudent, false, false, false), false},

		{"archived blocks member write", roles(RoleStudent), RepoWrite, repo(KindStudent, false, true, true), false},
		{"archived blocks admin write", admin, RepoWrite, repo(KindStudent, false, true, false), false},
		{"archived still readable", roles(RoleStudent), RepoRead, repo(KindStudent, false, true, true), true},

		{"requested peer reads repo", roles(RoleStudent), RepoRead, reviewer(repo(KindStudent, false, false, false)), true},
		{"requested peer reviews", roles(RoleStudent), RepoReview, reviewer(repo(KindStudent, false, false, false)), true},
		{"requested peer cannot write", roles(RoleStudent), RepoWrite, reviewer(repo(KindStudent, false, false, false)), false},
		{"requested peer cannot administer", roles(RoleStudent), RepoAdmin, reviewer(repo(KindStudent, false, false, false)), false},
		{"requested peer must be enrolled", roles(RoleNone), RepoRead, reviewer(repo(KindStudent, false, false, false)), false},
		{"requested peer must be enrolled to review", roles(RoleNone), RepoReview, reviewer(repo(KindStudent, false, false, false)), false},
		{"reviewer role in other course does not help", Principal{Roles: map[int64]Role{other: RoleStudent}}, RepoRead, reviewer(repo(KindStudent, false, false, false)), false},
		{"unrequested student cannot review", roles(RoleStudent), RepoReview, repo(KindStudent, false, false, false), false},
		{"member reviews own repo", roles(RoleStudent), RepoReview, repo(KindStudent, false, false, true), true},
		{"teacher reviews", roles(RoleTeacher), RepoReview, repo(KindStudent, false, false, false), true},
		{"teacher of other course cannot review", Principal{Roles: map[int64]Role{other: RoleTeacher}}, RepoReview, repo(KindStudent, false, false, false), false},
		{"review survives archiving", roles(RoleTeacher), RepoReview, repo(KindStudent, false, true, false), true},
		{"review on non-repo resource is denied", roles(RoleTeacher), RepoReview, courseRes, false},

		{"teacher administers repo", roles(RoleTeacher), RepoAdmin, repo(KindStudent, false, false, false), true},
		{"student member cannot administer", roles(RoleStudent), RepoAdmin, repo(KindStudent, false, false, true), false},
		{"repo action on non-repo resource is denied", roles(RoleTeacher), RepoRead, courseRes, false},
		{"unknown action is denied", roles(RoleCourseAdmin), Action("nope"), courseRes, false},
		{"nil principal roles", Principal{}, CourseView, courseRes, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Can(tt.p, tt.a, tt.res); got != tt.want {
				t.Errorf("Can(%s) = %v, want %v", tt.a, got, tt.want)
			}
		})
	}
}

func TestParseRole(t *testing.T) {
	for in, want := range map[string]Role{"student": RoleStudent, "teacher": RoleTeacher, "course_admin": RoleCourseAdmin, "": RoleNone, "root": RoleNone} {
		if got := ParseRole(in); got != want {
			t.Errorf("ParseRole(%q) = %v, want %v", in, got, want)
		}
	}
}
