package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnrollment(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.CreateCourse(ctx, "oop", "OOP", "HT26")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateCourse(ctx, "oop", "Again", ""); err == nil {
		t.Error("duplicate slug accepted")
	}

	// Pending enrolment, case-insensitive, bound at first login.
	if err := db.EnrollEmail(ctx, c.ID, " Ada@Student.BTH.se ", "student", "import"); err != nil {
		t.Fatal(err)
	}
	r, _ := db.Roster(ctx, c.ID)
	if len(r) != 1 || !r[0].Pending() || r[0].Email != "ada@student.bth.se" {
		t.Fatalf("roster before login: %+v", r)
	}
	u, err := db.LoginUser(ctx, "oid-ada", "ada@student.bth.se", "Ada", false)
	if err != nil {
		t.Fatal(err)
	}
	roles, _ := db.Roles(ctx, u.ID)
	if roles[c.ID] != "student" {
		t.Errorf("roles after login: %v", roles)
	}
	if r, _ = db.Roster(ctx, c.ID); len(r) != 1 || r[0].Pending() || r[0].UserID != u.ID {
		t.Errorf("roster after login: %+v", r)
	}

	// Enrolling a known user is immediate and replaces the role.
	if err := db.EnrollEmail(ctx, c.ID, "ada@student.bth.se", "teacher", "manual"); err != nil {
		t.Fatal(err)
	}
	if roles, _ = db.Roles(ctx, u.ID); roles[c.ID] != "teacher" {
		t.Errorf("role not replaced: %v", roles)
	}

	// A login never overrides an existing membership.
	if err := db.EnrollEmail(ctx, c.ID, "bo@bth.se", "teacher", "manual"); err != nil {
		t.Fatal(err)
	}
	if err := db.RemoveEnrollment(ctx, c.ID, "BO@bth.se"); err != nil {
		t.Fatal(err)
	}
	if err := db.RemoveEnrollment(ctx, c.ID, "bo@bth.se"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove: got %v, want ErrNotFound", err)
	}

	// Courses and archive.
	got, err := db.UserCourses(ctx, u.ID)
	if err != nil || len(got) != 1 || got[0].Role != "teacher" || got[0].Term != "HT26" {
		t.Errorf("user courses: %+v, %v", got, err)
	}
	if err := db.SetArchived(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if c2, _ := db.CourseBySlug(ctx, "oop"); !c2.Archived {
		t.Error("not archived")
	}
	if err := db.SetArchived(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("archive missing: %v", err)
	}
	if _, err := db.CourseBySlug(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing slug: %v", err)
	}
}

func TestInvite(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	staff, _ := db.LoginUser(ctx, "s", "t@bth.se", "T", false)
	stu, _ := db.LoginUser(ctx, "u", "u@student.bth.se", "U", false)
	if err := db.SetMembership(ctx, c.ID, staff.ID, "teacher", "manual"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.SetInvite(ctx, c.ID, "h1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.JoinByInvite(ctx, "wrong", stu.ID, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown link: %v", err)
	}
	if _, err := db.JoinByInvite(ctx, "h1", stu.ID, now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired link: %v", err)
	}
	if _, err := db.JoinByInvite(ctx, "h1", stu.ID, now); err != nil {
		t.Fatal(err)
	}
	if roles, _ := db.Roles(ctx, stu.ID); roles[c.ID] != "student" {
		t.Errorf("roles: %v", roles)
	}
	// Staff using the link keep their role.
	if _, err := db.JoinByInvite(ctx, "h1", staff.ID, now); err != nil {
		t.Fatal(err)
	}
	if roles, _ := db.Roles(ctx, staff.ID); roles[c.ID] != "teacher" {
		t.Errorf("staff downgraded: %v", roles)
	}
	// A new link replaces the old one.
	if err := db.SetInvite(ctx, c.ID, "h2", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InviteCourse(ctx, "h1", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("old link still live: %v", err)
	}
	// Archived courses refuse the link; revoking removes it.
	_ = db.SetArchived(ctx, c.ID, true)
	if _, err := db.JoinByInvite(ctx, "h2", stu.ID, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("archived: %v", err)
	}
	_ = db.RevokeInvite(ctx, c.ID)
	if _, err := db.InviteExpiry(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after revoke: %v", err)
	}
}
