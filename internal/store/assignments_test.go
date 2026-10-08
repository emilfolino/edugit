package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAssignments(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	tmpl, _ := db.CreateRepo(ctx, c.ID, "starter", "teacher", true)
	var users []User
	for _, e := range []string{"a@student.bth.se", "b@student.bth.se", "c@student.bth.se"} {
		u, err := db.LoginUser(ctx, e, e, e, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetMembership(ctx, c.ID, u.ID, "student", "manual"); err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	dl := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	ind, err := db.CreateAssignment(ctx, Assignment{CourseID: c.ID, TemplateRepoID: tmpl.ID, Slug: "lab1", Title: "Lab 1",
		Mode: "individual", History: "fresh", TeamSize: 3, Deadline: dl})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAssignment(ctx, Assignment{CourseID: c.ID, TemplateRepoID: tmpl.ID, Slug: "lab1", Title: "x",
		Mode: "individual", History: "fresh", TeamSize: 3}); !IsConflict(err) {
		t.Errorf("duplicate slug error = %v, want conflict", err)
	}
	got, err := db.AssignmentBySlug(ctx, c.ID, "lab1")
	if err != nil || !got.Deadline.Equal(dl) || got.TemplateName != "starter" {
		t.Fatalf("AssignmentBySlug = %+v, %v", got, err)
	}
	if _, err := db.AssignmentBySlug(ctx, c.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing assignment error = %v", err)
	}

	t.Run("individual", func(t *testing.T) {
		if _, err := db.AssignmentRepoFor(ctx, ind.ID, users[0].ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("before accept: %v", err)
		}
		pending, _ := db.PendingStudents(ctx, ind)
		if len(pending) != 3 {
			t.Fatalf("pending = %d, want 3", len(pending))
		}
		r, err := db.CreateAssignmentRepo(ctx, ind, "lab1-a", users[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateAssignmentRepo(ctx, ind, "lab1-a2", users[0].ID); !errors.Is(err, ErrHasRepo) {
			t.Errorf("second repo error = %v, want ErrHasRepo", err)
		}
		if _, err := db.CreateRepo(ctx, c.ID, "lab1-a2", "student", false); err != nil {
			t.Errorf("failed second repo left a row behind: %v", err)
		}
		if _, member, _ := db.RepoByName(ctx, "oop", "lab1-a", users[0].ID); !member {
			t.Error("owner is not a repo member")
		}
		if _, member, _ := db.RepoByName(ctx, "oop", "lab1-a", users[1].ID); member {
			t.Error("other student is a repo member")
		}
		if got, err := db.AssignmentRepoFor(ctx, ind.ID, users[0].ID); err != nil || got.ID != r.ID {
			t.Errorf("AssignmentRepoFor = %+v, %v", got, err)
		}
		if pending, _ := db.PendingStudents(ctx, ind); len(pending) != 2 {
			t.Errorf("pending after accept = %d, want 2", len(pending))
		}
		repos, _ := db.AssignmentRepos(ctx, ind.ID)
		if len(repos) != 1 || repos[0].Owner != users[0].Username {
			t.Errorf("AssignmentRepos = %+v", repos)
		}
	})

	t.Run("extensions", func(t *testing.T) {
		later := dl.Add(48 * time.Hour)
		if err := db.SetExtension(ctx, ind.ID, users[0].ID, later); err != nil {
			t.Fatal(err)
		}
		if d, _ := db.EffectiveDeadline(ctx, ind, users[0].ID); !d.Equal(later) {
			t.Errorf("extended deadline = %v, want %v", d, later)
		}
		if d, _ := db.EffectiveDeadline(ctx, ind, users[1].ID); !d.Equal(dl) {
			t.Errorf("other user deadline = %v, want %v", d, dl)
		}
		// An extension earlier than the deadline never shortens it.
		_ = db.SetExtension(ctx, ind.ID, users[1].ID, dl.Add(-time.Hour))
		if d, _ := db.EffectiveDeadline(ctx, ind, users[1].ID); !d.Equal(dl) {
			t.Errorf("shortened deadline = %v, want %v", d, dl)
		}
		_ = db.SetExtension(ctx, ind.ID, users[0].ID, time.Time{})
		if d, _ := db.EffectiveDeadline(ctx, ind, users[0].ID); !d.Equal(dl) {
			t.Errorf("removed extension deadline = %v, want %v", d, dl)
		}
	})

	t.Run("teams", func(t *testing.T) {
		team, err := db.CreateAssignment(ctx, Assignment{CourseID: c.ID, TemplateRepoID: tmpl.ID, Slug: "proj", Title: "Project",
			Mode: "team", History: "copy", TeamSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		if d, _ := db.EffectiveDeadline(ctx, team, users[0].ID); !d.IsZero() {
			t.Errorf("no-deadline effective deadline = %v", d)
		}
		tm, err := db.CreateTeam(ctx, team, "alpha", "proj-alpha", users[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateTeam(ctx, team, "beta", "proj-beta", users[0].ID); !errors.Is(err, ErrAlreadyInTeam) {
			t.Errorf("second team error = %v, want ErrAlreadyInTeam", err)
		}
		if _, err := db.CreateTeam(ctx, team, "alpha", "proj-alpha2", users[1].ID); !IsConflict(err) {
			t.Errorf("duplicate team name error = %v, want conflict", err)
		}
		if err := db.JoinTeam(ctx, team, tm.ID, users[0].ID); !errors.Is(err, ErrAlreadyInTeam) {
			t.Errorf("rejoin error = %v", err)
		}
		if err := db.JoinTeam(ctx, team, tm.ID, users[1].ID); err != nil {
			t.Fatal(err)
		}
		if err := db.JoinTeam(ctx, team, tm.ID, users[2].ID); !errors.Is(err, ErrTeamFull) {
			t.Errorf("join full team error = %v, want ErrTeamFull", err)
		}
		if err := db.JoinTeam(ctx, ind, tm.ID, users[2].ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("join across assignments error = %v, want ErrNotFound", err)
		}
		if r, err := db.AssignmentRepoFor(ctx, team.ID, users[1].ID); err != nil || r.Name != "proj-alpha" || r.Kind != "team" {
			t.Errorf("member repo = %+v, %v", r, err)
		}
		if _, member, _ := db.RepoByName(ctx, "oop", "proj-alpha", users[1].ID); !member {
			t.Error("joined member is not a repo member")
		}
		teams, _ := db.Teams(ctx, team.ID)
		if len(teams) != 1 || len(teams[0].Members) != 2 {
			t.Fatalf("Teams = %+v", teams)
		}

		// Team mates share the latest extension.
		team.Deadline = dl
		later := dl.Add(24 * time.Hour)
		_ = db.SetExtension(ctx, team.ID, users[1].ID, later)
		if d, _ := db.EffectiveDeadline(ctx, team, users[0].ID); !d.Equal(later) {
			t.Errorf("team deadline = %v, want %v", d, later)
		}

		if err := db.LeaveTeam(ctx, team.ID, users[1].ID); err != nil {
			t.Fatal(err)
		}
		if _, member, _ := db.RepoByName(ctx, "oop", "proj-alpha", users[1].ID); member {
			t.Error("left member still has repo access")
		}
		if err := db.LeaveTeam(ctx, team.ID, users[1].ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("leave twice error = %v", err)
		}
		if err := db.JoinTeam(ctx, team, tm.ID, users[2].ID); err != nil {
			t.Errorf("join after leave: %v", err)
		}
	})
}

func TestSyncLocks(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	tmpl, _ := db.CreateRepo(ctx, c.ID, "starter", "teacher", true)
	u, _ := db.LoginUser(ctx, "a@student.bth.se", "a@student.bth.se", "a", false)
	if err := db.SetMembership(ctx, c.ID, u.ID, "student", "manual"); err != nil {
		t.Fatal(err)
	}
	dl := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	mk := func(slug string, deadline time.Time) (Assignment, Repo) {
		a, err := db.CreateAssignment(ctx, Assignment{CourseID: c.ID, TemplateRepoID: tmpl.ID, Slug: slug, Title: slug,
			Mode: "individual", History: "fresh", TeamSize: 3, Deadline: deadline})
		if err != nil {
			t.Fatal(err)
		}
		r, err := db.CreateAssignmentRepo(ctx, a, slug+"-a", u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a, r
	}
	archived := func(a Assignment) bool {
		rs, err := db.AssignmentRepos(ctx, a.ID)
		if err != nil || len(rs) != 1 {
			t.Fatalf("AssignmentRepos = %+v, %v", rs, err)
		}
		return rs[0].Archived
	}
	timed, _ := mk("timed", dl)
	open, _ := mk("open", time.Time{})

	if l, _, err := db.SyncLocks(ctx, dl.Add(-time.Hour)); err != nil || l != 0 || archived(timed) {
		t.Fatalf("before deadline: locked=%d err=%v archived=%v", l, err, archived(timed))
	}
	if l, _, err := db.SyncLocks(ctx, dl.Add(time.Minute)); err != nil || l != 1 || !archived(timed) {
		t.Fatalf("after deadline: locked=%d err=%v archived=%v", l, err, archived(timed))
	}
	if archived(open) {
		t.Error("assignment without a deadline was locked")
	}
	if l, _, _ := db.SyncLocks(ctx, dl.Add(2*time.Minute)); l != 0 {
		t.Errorf("second sync locked %d repos", l)
	}
	if err := db.SetExtension(ctx, timed.ID, u.ID, dl.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, re, err := db.SyncLocks(ctx, dl.Add(time.Hour)); err != nil || re != 1 || archived(timed) {
		t.Fatalf("after extension: reopened=%d err=%v archived=%v", re, err, archived(timed))
	}
	if l, _, _ := db.SyncLocks(ctx, dl.Add(72*time.Hour)); l != 1 || !archived(timed) {
		t.Errorf("extended deadline did not lock (locked=%d)", l)
	}
}

func TestDefaultProtection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	tmpl, _ := db.CreateRepo(ctx, c.ID, "starter", "teacher", true)
	u, _ := db.LoginUser(ctx, "a@student.bth.se", "a@student.bth.se", "a", false)
	_ = db.SetMembership(ctx, c.ID, u.ID, "student", "manual")
	a, _ := db.CreateAssignment(ctx, Assignment{CourseID: c.ID, TemplateRepoID: tmpl.ID, Slug: "l", Title: "l",
		Mode: "individual", History: "fresh", TeamSize: 3})
	if _, err := db.CreateAssignmentRepo(ctx, a, "l-a", u.ID); err != nil {
		t.Fatal(err)
	}
	rules, err := db.BranchRules(ctx, "oop", "l-a")
	if err != nil || len(rules) != len(ProtectedBranches) {
		t.Fatalf("BranchRules = %+v, %v", rules, err)
	}
	for _, r := range rules {
		if !r.RequirePR || r.AllowForce {
			t.Errorf("rule %+v is not PR-only without force", r)
		}
	}
}
