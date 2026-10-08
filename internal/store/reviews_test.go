package store

import (
	"context"
	"errors"
	"testing"
)

func TestReviews(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	author, _ := db.LoginUser(ctx, "a", "a@student.bth.se", "A", false)
	r1, _ := db.LoginUser(ctx, "b", "b@student.bth.se", "B", false)
	r2, _ := db.LoginUser(ctx, "c", "c@student.bth.se", "C", false)
	repo, _ := db.CreateRepo(ctx, c.ID, "lab", "student", false)
	pr, err := db.CreatePull(ctx, repo.ID, author.ID, "T", "", "feat", "main")
	if err != nil {
		t.Fatal(err)
	}

	approve := func(u User, state, sha string) {
		t.Helper()
		if _, err := db.AddReview(ctx, pr.ID, u.ID, state, "", sha); err != nil {
			t.Fatal(err)
		}
	}
	check := func(sha string, want ApprovalState) {
		t.Helper()
		got, err := db.Approvals(ctx, pr.ID, sha)
		if err != nil || got != want {
			t.Fatalf("approvals at %s = %+v, %v; want %+v", sha, got, err, want)
		}
	}

	check("s1", ApprovalState{})
	approve(r1, ReviewApprove, "s1")
	approve(author, ReviewApprove, "s1") // own approval never counts
	approve(r2, ReviewComment, "s1")     // not decisive
	check("s1", ApprovalState{Approvals: 1})
	check("s2", ApprovalState{}) // new commits make the approval stale
	approve(r2, ReviewRequestChanges, "s1")
	check("s1", ApprovalState{Approvals: 1, ChangesRequested: 1})
	approve(r2, ReviewApprove, "s1") // latest decisive review wins
	check("s1", ApprovalState{Approvals: 2})

	g, err := db.AddComment(ctx, pr.ID, 0, r1.ID, "general", "", 0, "s1")
	if err != nil || g.Path != "" || g.Line != 0 || g.ReviewID != 0 {
		t.Fatalf("general: %+v, %v", g, err)
	}
	in, err := db.AddComment(ctx, pr.ID, 0, r1.ID, "inline", "a.go", 7, "s1")
	if err != nil || in.Path != "a.go" || in.Line != 7 {
		t.Fatalf("inline: %+v, %v", in, err)
	}
	if err := db.ResolveComment(ctx, pr.ID, in.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveComment(ctx, pr.ID+1, in.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-PR resolve: got %v", err)
	}
	cs, _ := db.Comments(ctx, pr.ID)
	if len(cs) != 2 || cs[0].Resolved || !cs[1].Resolved {
		t.Fatalf("comments: %+v", cs)
	}
	if rs, _ := db.Reviews(ctx, pr.ID); len(rs) != 5 || rs[0].Reviewer != r1.Username {
		t.Fatalf("reviews: %+v", rs)
	}

	if need, _ := db.RequiredApprovals(ctx, repo.ID, "main"); need != 0 {
		t.Fatalf("default requirement = %d", need)
	}
	if err := db.SetRequiredApprovals(ctx, repo.ID, "main", 2); err != nil {
		t.Fatal(err)
	}
	if err := db.SetRequiredApprovals(ctx, repo.ID, "main", 1); err != nil {
		t.Fatal(err)
	}
	if need, _ := db.RequiredApprovals(ctx, repo.ID, "main"); need != 1 {
		t.Fatalf("requirement = %d, want 1", need)
	}
	if need, _ := db.RequiredApprovals(ctx, repo.ID, "dev"); need != 0 {
		t.Fatalf("unmatched branch requirement = %d", need)
	}
	rules, _ := db.BranchRules(ctx, "oop", "lab")
	if len(rules) != 1 || rules[0].RequiredApprovals != 1 {
		t.Fatalf("rules: %+v", rules)
	}

	if ok, _ := db.IsRequestedReviewer(ctx, repo.ID, r1.ID); ok {
		t.Fatal("reviewer before request")
	}
	for range 2 {
		if err := db.RequestReview(ctx, pr.ID, r1.ID, author.ID); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _ := db.IsRequestedReviewer(ctx, repo.ID, r1.ID); !ok {
		t.Fatal("requested reviewer not recognised")
	}
	if names, _ := db.ReviewRequests(ctx, pr.ID); len(names) != 1 {
		t.Fatalf("requests: %v", names)
	}
	if err := db.SetPullState(ctx, pr.ID, PullClosed, ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := db.IsRequestedReviewer(ctx, repo.ID, r1.ID); ok {
		t.Fatal("closed PR still grants reviewer access")
	}
}
