package gitserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pullFixture builds a repo with main and a two-commit feature branch.
func pullFixture(t *testing.T) (r *Repos, dir string) {
	t.Helper()
	ctx := context.Background()
	r = newRepos(t)
	if err := r.Create(ctx, "c1", "demo", "main"); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, work, "init", "--quiet", "-b", "main")
	write("a.txt", "one\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "base")
	git(t, work, "checkout", "--quiet", "-b", "feature")
	write("b.txt", "b\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "add b")
	write("c.txt", "c\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "add c")
	dir, _ = r.Path("c1", "demo")
	git(t, dir, "fetch", "--quiet", work, "+refs/heads/*:refs/heads/*")
	return r, dir
}

func TestPulls_ReadAndMergeable(t *testing.T) {
	ctx := context.Background()
	r, dir := pullFixture(t)
	base, _ := r.Resolve(ctx, "c1", "demo", "main")
	head, err := r.Resolve(ctx, "c1", "demo", "feature")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.Commits(ctx, "c1", "demo", base, head)
	if err != nil || len(cs) != 2 || cs[0].Subject != "add b" || cs[1].Subject != "add c" {
		t.Fatalf("Commits = %+v, %v", cs, err)
	}
	diff, err := r.Diff(ctx, "c1", "demo", base, head)
	if err != nil || !strings.Contains(diff, "+++ b/b.txt") || !strings.Contains(diff, "+++ b/c.txt") {
		t.Fatalf("Diff = %q, %v", diff, err)
	}
	if ok, err := r.Mergeable(ctx, "c1", "demo", base, head); err != nil || !ok {
		t.Fatalf("Mergeable = %v, %v", ok, err)
	}
	if _, err := r.Resolve(ctx, "c1", "demo", "nope"); err == nil {
		t.Error("Resolve found a missing branch")
	}

	// Conflicting change on main.
	work := t.TempDir()
	git(t, work, "init", "--quiet", "-b", "x")
	git(t, work, "fetch", "--quiet", dir, "main")
	git(t, work, "checkout", "--quiet", "FETCH_HEAD")
	_ = os.WriteFile(filepath.Join(work, "b.txt"), []byte("other\n"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "conflict")
	git(t, dir, "fetch", "--quiet", work, "+HEAD:refs/heads/main")
	base, _ = r.Resolve(ctx, "c1", "demo", "main")
	if ok, err := r.Mergeable(ctx, "c1", "demo", base, head); err != nil || ok {
		t.Fatalf("Mergeable with conflict = %v, %v", ok, err)
	}
	if _, err := r.Merge(ctx, "c1", "demo", "main", head, StrategyMerge, Identity{"Bot", "bot@x"}, "m"); !errors.Is(err, ErrConflict) {
		t.Errorf("Merge conflict error = %v", err)
	}
}

func TestPulls_Merge(t *testing.T) {
	by := Identity{Name: "Merger", Email: "m@x.test"}
	for _, tc := range []struct {
		strategy string
		parents  int
		commits  int // commits added to main
	}{
		{StrategyMerge, 2, 3},
		{StrategySquash, 1, 1},
		{StrategyRebase, 1, 2},
	} {
		t.Run(tc.strategy, func(t *testing.T) {
			ctx := context.Background()
			r, dir := pullFixture(t)
			base, _ := r.Resolve(ctx, "c1", "demo", "main")
			head, _ := r.Resolve(ctx, "c1", "demo", "feature")
			sha, err := r.Merge(ctx, "c1", "demo", "main", head, tc.strategy, by, "Merge it")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(git(t, dir, "rev-parse", "main")); got != sha {
				t.Errorf("main = %s, want %s", got, sha)
			}
			if got := len(strings.Fields(git(t, dir, "rev-list", "--parents", "-1", "main"))) - 1; got != tc.parents {
				t.Errorf("parents = %d, want %d", got, tc.parents)
			}
			if got := len(strings.Fields(git(t, dir, "rev-list", base+"..main"))); got != tc.commits {
				t.Errorf("new commits = %d, want %d", got, tc.commits)
			}
			if got := git(t, dir, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(got, "c.txt") {
				t.Errorf("tree = %q", got)
			}
			if got := strings.TrimSpace(git(t, dir, "log", "-1", "--format=%cn", "main")); got != "Merger" {
				t.Errorf("committer = %q", got)
			}
			if tc.strategy == StrategyRebase {
				if got := strings.TrimSpace(git(t, dir, "log", "-1", "--format=%an", "main")); got != "t" {
					t.Errorf("rebase author = %q, want original", got)
				}
			}
			if _, err := r.Merge(ctx, "c1", "demo", "main", head, tc.strategy, by, "again"); err == nil {
				t.Error("second merge succeeded")
			}
		})
	}
	if _, err := (&Repos{}).Merge(context.Background(), "c1", "demo", "main", "x", "bogus", by, ""); err == nil {
		t.Error("bad head accepted")
	}
}
