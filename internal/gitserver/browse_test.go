package gitserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidPath(t *testing.T) {
	for p, want := range map[string]bool{"": true, "a/b.txt": true, "a/": true, "../x": false, "a/../b": false, "/abs": false, "a//b": false, "a\nb": false, "./a": false} {
		if validPath(p) != want {
			t.Errorf("validPath(%q) = %v", p, !want)
		}
	}
}

func TestBrowse(t *testing.T) {
	ctx := context.Background()
	repos, err := NewRepos(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.Create(ctx, "c", "r", "main"); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(work, "src"), 0o755)
	os.WriteFile(filepath.Join(work, "src", "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(work, "README.md"), []byte("hi\n"), 0o644)
	os.WriteFile(filepath.Join(work, "bin.dat"), []byte{0, 1, 2}, 0o644)
	run("add", ".")
	run("commit", "-qm", "first")
	bare, _ := repos.Path("c", "r")
	run("push", "-q", bare, "main")

	root, err := repos.Tree(ctx, "c", "r", "main", "")
	if err != nil || len(root) != 3 || !root[0].Dir || root[0].Name != "src" {
		t.Fatalf("root = %+v, %v", root, err)
	}
	sub, err := repos.Tree(ctx, "c", "r", "main", "src")
	if err != nil || len(sub) != 1 || sub[0].Name != "a.go" {
		t.Fatalf("sub = %+v, %v", sub, err)
	}
	b, err := repos.File(ctx, "c", "r", "main", "src/a.go")
	if err != nil || b.Content != "package a\n" || b.Binary {
		t.Fatalf("blob = %+v, %v", b, err)
	}
	if b, _ := repos.File(ctx, "c", "r", "main", "bin.dat"); !b.Binary || b.Content != "" {
		t.Fatalf("binary = %+v", b)
	}
	if _, err := repos.File(ctx, "c", "r", "main", "src"); !errors.Is(err, ErrNoPath) {
		t.Fatalf("dir as file: %v", err)
	}
	if _, err := repos.Tree(ctx, "c", "r", "nope", ""); !errors.Is(err, ErrNoPath) {
		t.Fatalf("missing ref: %v", err)
	}
	if _, err := repos.File(ctx, "c", "r", "main", "../../etc/passwd"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("traversal: %v", err)
	}
	log, err := repos.Log(ctx, "c", "r", "main", "", 10)
	if err != nil || len(log) != 1 || log[0].Subject != "first" {
		t.Fatalf("log = %+v, %v", log, err)
	}
	c, patch, err := repos.CommitDiff(ctx, "c", "r", log[0].SHA)
	if err != nil || c.Subject != "first" || !strings.Contains(patch, "+package a") {
		t.Fatalf("commit = %+v %q %v", c, patch, err)
	}
	bl, err := repos.Blame(ctx, "c", "r", "main", "src/a.go")
	if err != nil || len(bl) != 1 || bl[0].Author != "A" || !bl[0].First || bl[0].Text != "package a" {
		t.Fatalf("blame = %+v, %v", bl, err)
	}
	bs, err := repos.Branches(ctx, "c", "r")
	if err != nil || len(bs) != 1 || bs[0] != "main" {
		t.Fatalf("branches = %v, %v", bs, err)
	}
}
