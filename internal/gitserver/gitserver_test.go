package gitserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newRepos(t *testing.T) *Repos {
	t.Helper()
	r, err := NewRepos(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestValidName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"web-101", true},
		{"a", true},
		{"starter.code_v2", true},
		{"", false},
		{"..", false},
		{"a..b", false},
		{".hidden", false},
		{"-flag", false},
		{"UPPER", false},
		{"a/b", false},
		{"a\\b", false},
		{"x.git", false},
		{"x.lock", false},
		{strings.Repeat("a", 101), false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := validName(tt.in); got != tt.want {
				t.Errorf("validName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRepos_PathRejectsTraversal(t *testing.T) {
	r := newRepos(t)
	for _, c := range [][2]string{{"..", "x"}, {"x", ".."}, {"a/../b", "x"}, {"x", "../../etc"}, {"", "x"}} {
		if _, err := r.Path(c[0], c[1]); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Path(%q,%q) err = %v, want ErrInvalidName", c[0], c[1], err)
		}
	}
	p, err := r.Path("c1", "r1")
	if err != nil || !strings.HasPrefix(p, r.root) {
		t.Errorf("Path = %q, %v", p, err)
	}
}

func TestRepos_Lifecycle(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)

	if err := r.Create(ctx, "c1", "one", "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, "c1", "one", "main"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate create err = %v, want ErrExists", err)
	}
	if err := r.Create(ctx, "c1", "bad", "-x"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("bad branch err = %v", err)
	}
	head, _ := os.ReadFile(filepath.Join(r.root, "c1", "one.git", "HEAD"))
	if strings.TrimSpace(string(head)) != "ref: refs/heads/main" {
		t.Errorf("HEAD = %q", head)
	}
	if n, err := r.DiskUsage("c1", "one"); err != nil || n <= 0 {
		t.Errorf("DiskUsage = %d, %v", n, err)
	}
	if err := r.GC(ctx, "c1", "one"); err != nil {
		t.Errorf("GC: %v", err)
	}

	if err := r.Rename("c1", "one", "two"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.Exists("c1", "one"); ok {
		t.Error("old name still exists")
	}
	if err := r.Create(ctx, "c1", "three", "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.Rename("c1", "two", "three"); !errors.Is(err, ErrExists) {
		t.Errorf("rename onto existing err = %v", err)
	}
	if err := r.Rename("c1", "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename missing err = %v", err)
	}

	list, err := r.List()
	if err != nil || len(list) != 2 {
		t.Errorf("List = %v, %v", list, err)
	}

	if err := r.Delete("c1", "two"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete("c1", "two"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing err = %v", err)
	}
	// No leftover temp dirs from Create.
	entries, _ := os.ReadDir(filepath.Join(r.root, "c1"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".new-") {
			t.Errorf("leftover temp dir %s", e.Name())
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gitFails runs git and returns its output, expecting failure.
func gitFails(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.com")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("git %s unexpectedly succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

func newServer(t *testing.T, auth Authorizer, mutate func(*Handler)) (*Repos, *httptest.Server) {
	t.Helper()
	r := newRepos(t)
	h := &Handler{Repos: r, Auth: auth, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if mutate != nil {
		mutate(h)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return r, srv
}

func allowAll(*http.Request, string, string, bool) error { return nil }

func TestHandler_CloneAndPush(t *testing.T) {
	for _, proto := range []string{"0", "2"} {
		t.Run("protocol"+proto, func(t *testing.T) {
			r, srv := newServer(t, allowAll, nil)
			if err := r.Create(context.Background(), "c1", "demo", "main"); err != nil {
				t.Fatal(err)
			}
			url := srv.URL + "/git/c1/demo.git"
			work := t.TempDir()
			cfg := []string{"-c", "protocol.version=" + proto}

			// Clone the empty repo, commit, push.
			git(t, work, append(cfg, "clone", url, "a")...)
			a := filepath.Join(work, "a")
			if err := os.WriteFile(filepath.Join(a, "f.txt"), []byte("hello\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			git(t, a, "add", ".")
			git(t, a, "commit", "-m", "first")
			git(t, a, "push", "origin", "HEAD:main")

			// A second clone sees the commit; push a branch and fetch it back.
			git(t, work, append(cfg, "clone", url, "b")...)
			b := filepath.Join(work, "b")
			if got, _ := os.ReadFile(filepath.Join(b, "f.txt")); string(got) != "hello\n" {
				t.Errorf("cloned content = %q", got)
			}
			git(t, b, "checkout", "-b", "feature")
			git(t, b, "commit", "--allow-empty", "-m", "second")
			git(t, b, "push", "origin", "feature")
			git(t, a, append(cfg, "fetch", "origin")...)
			if out := git(t, a, "log", "--format=%s", "origin/feature"); !strings.Contains(out, "second") {
				t.Errorf("fetched log = %q", out)
			}
		})
	}
}

func TestHandler_Authorization(t *testing.T) {
	auth := func(r *http.Request, course, repo string, write bool) error {
		_, pw, ok := r.BasicAuth()
		switch {
		case !ok:
			return ErrUnauthenticated
		case pw == "reader" && !write:
			return nil
		case pw == "writer":
			return nil
		case pw == "hidden":
			return ErrNotFound
		}
		return ErrForbidden
	}
	r, srv := newServer(t, auth, nil)
	if err := r.Create(context.Background(), "c1", "demo", "main"); err != nil {
		t.Fatal(err)
	}
	get := func(path, pw string) int {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if pw != "" {
			req.SetBasicAuth("u", pw)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	tests := []struct {
		name, path, pw string
		want           int
	}{
		{"anon read", "/git/c1/demo.git/info/refs?service=git-upload-pack", "", 401},
		{"reader read", "/git/c1/demo.git/info/refs?service=git-upload-pack", "reader", 200},
		{"reader push", "/git/c1/demo.git/info/refs?service=git-receive-pack", "reader", 403},
		{"writer push", "/git/c1/demo.git/info/refs?service=git-receive-pack", "writer", 200},
		{"hidden", "/git/c1/demo.git/info/refs?service=git-upload-pack", "hidden", 404},
		{"missing repo", "/git/c1/nope.git/info/refs?service=git-upload-pack", "writer", 404},
		{"no .git suffix", "/git/c1/demo/info/refs?service=git-upload-pack", "writer", 404},
		{"traversal", "/git/c1/..%2f..%2fx.git/info/refs?service=git-upload-pack", "writer", 404},
		{"dumb protocol", "/git/c1/demo.git/info/refs", "writer", 403},
		{"bad service", "/git/c1/demo.git/info/refs?service=git-evil", "writer", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := get(tt.path, tt.pw); got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHandler_PushLimits(t *testing.T) {
	r, srv := newServer(t, allowAll, func(h *Handler) { h.MaxRepoBytes = 1 })
	if err := r.Create(context.Background(), "c1", "demo", "main"); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	git(t, work, "clone", srv.URL+"/git/c1/demo.git", "a")
	a := filepath.Join(work, "a")
	git(t, a, "commit", "--allow-empty", "-m", "x")
	// The repo already exceeds 1 byte, so the push must be refused.
	out := gitFails(t, a, "push", "origin", "HEAD:main")
	if !strings.Contains(out, "413") && !strings.Contains(out, "limit") {
		t.Errorf("unexpected push failure output: %s", out)
	}
}
