package store

import (
	"context"
	"errors"
	"testing"
)

func TestCIRuns(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	repo, err := db.CreateRepo(ctx, c.ID, "lab", "teacher", false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.CreateCIRun(ctx, repo.ID, "sha1", "main", "test", "{}")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := db.CreateCIRun(ctx, repo.ID, "sha2", "main", "lint", "{}")

	if err := db.StartCIRun(ctx, a); err != nil {
		t.Fatal(err)
	}
	if n, err := db.FailInterruptedCIRuns(ctx); err != nil || n != 1 {
		t.Fatalf("interrupted = %d, %v", n, err)
	}
	if r, _ := db.CIRunByID(ctx, repo.ID, a); r.Status != CIError || !r.Done() {
		t.Errorf("interrupted run = %+v", r)
	}
	q, err := db.QueuedCIRuns(ctx)
	if err != nil || len(q) != 1 || q[0].ID != b || q[0].Course != "oop" || q[0].Repo != "lab" {
		t.Fatalf("queued = %+v, %v", q, err)
	}
	if err := db.FinishCIRun(ctx, b, CISuccess, "ok"); err != nil {
		t.Fatal(err)
	}
	r, err := db.CIRunByID(ctx, repo.ID, b)
	if err != nil || r.Status != CISuccess || r.Log != "ok" || !r.Finished.Valid {
		t.Fatalf("finished run = %+v, %v", r, err)
	}
	if runs, _ := db.CIRunsForSHA(ctx, repo.ID, "sha2"); len(runs) != 1 || runs[0].Log != "" {
		t.Errorf("for sha = %+v (lists must omit logs)", runs)
	}
	if runs, _ := db.CIRuns(ctx, repo.ID, 10); len(runs) != 2 || runs[0].ID != b {
		t.Errorf("list order = %+v", runs)
	}
	if _, err := db.CIRunByID(ctx, repo.ID+1, a); !errors.Is(err, ErrNotFound) {
		t.Errorf("other repo's run: %v", err)
	}
}
