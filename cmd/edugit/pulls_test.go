package main

import (
	"context"
	"testing"

	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

func TestPullSink(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, err := db.LoginUser(ctx, "o1", "a@bth.se", "A", true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateCourse(ctx, "oop", "OOP", "HT26")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := db.CreateRepo(ctx, c.ID, "r", "teacher", false)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := db.CreatePull(ctx, repo.ID, u.ID, "t", "", "topic", "main")
	if err != nil {
		t.Fatal(err)
	}
	sink := pullSink{db}
	push := func(ref, newSHA string) {
		t.Helper()
		err := sink.Pushed(ctx, hooks.Request{Course: "oop", Repo: "r", Updates: []hooks.Update{{Ref: ref, Old: "a", New: newSHA}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	state := func() string {
		t.Helper()
		p, err := db.Pull(ctx, repo.ID, pr.Number)
		if err != nil {
			t.Fatal(err)
		}
		return p.State
	}
	push("refs/heads/topic", "b")
	push("refs/tags/v1", "b")
	if got := state(); got != store.PullOpen {
		t.Fatalf("after push: %s", got)
	}
	push("refs/heads/topic", "0000000000000000000000000000000000000000")
	if got := state(); got != store.PullClosed {
		t.Fatalf("after delete: %s", got)
	}
}
