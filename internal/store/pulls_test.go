package store

import (
	"context"
	"errors"
	"testing"
)

func TestPulls(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	u, _ := db.LoginUser(ctx, "oid", "a@student.bth.se", "A", false)
	r, err := db.CreateRepo(ctx, c.ID, "lab", "student", false)
	if err != nil {
		t.Fatal(err)
	}

	p1, err := db.CreatePull(ctx, r.ID, u.ID, "First", "body", "feat", "main")
	if err != nil || p1.Number != 1 || p1.Author != u.Username || p1.State != PullOpen {
		t.Fatalf("create: %+v, %v", p1, err)
	}
	if _, err := db.CreatePull(ctx, r.ID, u.ID, "Dup", "", "feat", "main"); !errors.Is(err, ErrPullExists) {
		t.Fatalf("duplicate: got %v, want ErrPullExists", err)
	}
	p2, err := db.CreatePull(ctx, r.ID, u.ID, "Second", "", "other", "main")
	if err != nil || p2.Number != 2 {
		t.Fatalf("second: %+v, %v", p2, err)
	}

	got, err := db.Pull(ctx, r.ID, 1)
	if err != nil || got.Title != "First" || got.CreatedAt.IsZero() {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if _, err := db.Pull(ctx, r.ID, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: got %v", err)
	}

	touching, _ := db.OpenPullsTouching(ctx, r.ID, "feat")
	if len(touching) != 1 || touching[0].Number != 1 {
		t.Fatalf("touching: %+v", touching)
	}
	if touching, _ = db.OpenPullsTouching(ctx, r.ID, "main"); len(touching) != 2 {
		t.Fatalf("touching base: %+v", touching)
	}

	if err := db.SetPullState(ctx, p1.ID, PullMerged, "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPullState(ctx, p1.ID, PullClosed, ""); !errors.Is(err, ErrPullClosed) {
		t.Fatalf("second transition: got %v, want ErrPullClosed", err)
	}
	got, _ = db.Pull(ctx, r.ID, 1)
	if got.State != PullMerged || got.MergeCommit != "abc123" {
		t.Fatalf("merged: %+v", got)
	}

	open, _ := db.Pulls(ctx, r.ID, PullOpen)
	all, _ := db.Pulls(ctx, r.ID, "")
	if len(open) != 1 || len(all) != 2 || all[0].Number != 2 {
		t.Fatalf("lists: open %d, all %+v", len(open), all)
	}

	// Reopen is refused while another PR is open for the pair.
	if err := db.SetPullState(ctx, p2.ID, PullClosed, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ReopenPull(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPullState(ctx, p2.ID, PullClosed, ""); err != nil {
		t.Fatal(err)
	}
	p3, _ := db.CreatePull(ctx, r.ID, u.ID, "Third", "", "other", "main")
	if err := db.ReopenPull(ctx, p2.ID); !errors.Is(err, ErrPullExists) {
		t.Fatalf("reopen with open twin: got %v", err)
	}
	_ = p3

	rr, slug, err := db.RepoByID(ctx, r.ID)
	if err != nil || rr.Name != "lab" || slug != "oop" {
		t.Fatalf("RepoByID: %+v %q %v", rr, slug, err)
	}
}
