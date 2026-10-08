package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestIssues(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	u, _ := db.LoginUser(ctx, "oid", "a@student.bth.se", "A", false)
	r, _ := db.CreateRepo(ctx, c.ID, "lab", "student", false)
	other, _ := db.CreateRepo(ctx, c.ID, "lab2", "student", false)

	i1, err := db.CreateIssue(ctx, r.ID, u.ID, "Bug", "details")
	if err != nil || i1.Number != 1 || i1.Author != u.Username || i1.State != IssueOpen {
		t.Fatalf("create: %+v, %v", i1, err)
	}
	if i2, _ := db.CreateIssue(ctx, r.ID, u.ID, "Two", ""); i2.Number != 2 {
		t.Fatalf("numbering: %d", i2.Number)
	}
	if o, _ := db.CreateIssue(ctx, other.ID, u.ID, "Other", ""); o.Number != 1 {
		t.Fatalf("numbers are per repo: %d", o.Number)
	}
	if _, err := db.Issue(ctx, r.ID, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}

	if err := db.AddIssueLabel(ctx, r.ID, i1.ID, "bug"); err != nil {
		t.Fatal(err)
	}
	_ = db.AddIssueLabel(ctx, r.ID, i1.ID, "bug") // idempotent
	if err := db.SetIssueAssignee(ctx, i1.ID, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateMilestone(ctx, r.ID, "v1", "2026-12-01"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetIssueMilestone(ctx, r.ID, i1.ID, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetIssueMilestone(ctx, r.ID, i1.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown milestone: %v", err)
	}
	got, _ := db.Issue(ctx, r.ID, 1)
	if !slices.Equal(got.Labels, []string{"bug"}) || !slices.Equal(got.Assignees, []string{u.Username}) || got.Milestone != "v1" {
		t.Fatalf("metadata: %+v", got)
	}
	if l, _ := db.Issues(ctx, r.ID, "", "bug"); len(l) != 1 || l[0].Number != 1 {
		t.Fatalf("label filter: %+v", l)
	}
	if ms, _ := db.Milestones(ctx, r.ID); len(ms) != 1 || ms[0].Open != 1 || ms[0].Closed != 0 {
		t.Fatalf("milestones: %+v", ms)
	}

	if err := db.AddIssueComment(ctx, i1.ID, u.ID, "hi"); err != nil {
		t.Fatal(err)
	}
	if cs, _ := db.IssueComments(ctx, i1.ID); len(cs) != 1 || cs[0].Body != "hi" || cs[0].Author != u.Username {
		t.Fatalf("comments: %+v", cs)
	}

	closed, err := db.CloseIssues(ctx, r.ID, []int{1, 7})
	if err != nil || !slices.Equal(closed, []int{1}) {
		t.Fatalf("close: %v, %v", closed, err)
	}
	if again, _ := db.CloseIssues(ctx, r.ID, []int{1}); len(again) != 0 {
		t.Fatalf("already closed: %v", again)
	}
	if l, _ := db.Issues(ctx, r.ID, IssueOpen, ""); len(l) != 1 || l[0].Number != 2 {
		t.Fatalf("open list: %+v", l)
	}
	if err := db.SetIssueState(ctx, i1.ID, IssueOpen); err != nil {
		t.Fatal(err)
	}
	if err := db.RemoveIssueLabel(ctx, r.ID, i1.ID, "bug"); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.Issue(ctx, r.ID, 1); got.State != IssueOpen || len(got.Labels) != 0 {
		t.Fatalf("reopen: %+v", got)
	}
}
