package gitserver

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommitFiles(t *testing.T) {
	ctx := context.Background()
	r, dir := pullFixture(t)
	by := Identity{Name: "Ada", Email: "ada@example.com"}
	tip, _ := r.Resolve(ctx, "c1", "demo", "main")

	// Edit an existing file and add a nested one on main.
	sha, err := r.CommitFiles(ctx, "c1", "demo", "main", "", tip, []FileChange{
		{Path: "a.txt", Content: []byte("two\n")},
		{Path: "dir/new.txt", Content: []byte("n\n")},
	}, by, "edit")
	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "show", sha+":a.txt"); got != "two\n" {
		t.Errorf("a.txt = %q", got)
	}
	if got := git(t, dir, "log", "-1", "--format=%an <%ae>|%cn|%P", sha); !strings.HasPrefix(got, "Ada <ada@example.com>|Ada|"+tip) {
		t.Errorf("log = %q", got)
	}

	// A stale expectation is refused and nothing moves.
	if _, err := r.CommitFiles(ctx, "c1", "demo", "main", "", tip, []FileChange{{Path: "a.txt", Content: []byte("x")}}, by, "m"); !errors.Is(err, ErrStale) {
		t.Errorf("stale err = %v", err)
	}
	// Identical content is no commit.
	if _, err := r.CommitFiles(ctx, "c1", "demo", "main", "", sha, []FileChange{{Path: "a.txt", Content: []byte("two\n")}}, by, "m"); !errors.Is(err, ErrNothingToCommit) {
		t.Errorf("noop err = %v", err)
	}
	// Delete.
	del, err := r.CommitFiles(ctx, "c1", "demo", "main", "", sha, []FileChange{{Path: "dir/new.txt", Delete: true}}, by, "rm")
	if err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "ls-tree", "-r", "--name-only", del); strings.Contains(out, "dir/") {
		t.Errorf("tree after delete: %q", out)
	}
	// New branch from main; main untouched.
	nb, err := r.CommitFiles(ctx, "c1", "demo", "topic", "main", "", []FileChange{{Path: "t.txt", Content: []byte("t")}}, by, "topic")
	if err != nil {
		t.Fatal(err)
	}
	if main, _ := r.Resolve(ctx, "c1", "demo", "main"); main != del {
		t.Errorf("main moved to %s", main)
	}
	if got, _ := r.Resolve(ctx, "c1", "demo", "topic"); got != nb {
		t.Errorf("topic = %s", got)
	}
	// Bad paths and missing source branch.
	for _, p := range []string{"", "../x", ".git/config", "a/.GIT/x", "/abs", "dir/"} {
		if _, err := r.CommitFiles(ctx, "c1", "demo", "main", "", "", []FileChange{{Path: p, Content: []byte("x")}}, by, "m"); !errors.Is(err, ErrBadPath) {
			t.Errorf("path %q err = %v", p, err)
		}
	}
	if _, err := r.CommitFiles(ctx, "c1", "demo", "nope", "", "", []FileChange{{Path: "x", Content: []byte("x")}}, by, "m"); err == nil {
		t.Error("missing branch without from accepted")
	}
}
