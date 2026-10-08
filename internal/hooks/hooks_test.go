package hooks

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/emilfolino/edugit/internal/gitserver"
)

// TestMain lets the test binary double as the "edugit" executable the
// installed hooks call.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "hook" {
		os.Exit(Run(context.Background(), os.Args[2], os.Stdin, os.Stderr, os.Getenv))
	}
	os.Exit(m.Run())
}

func TestUpdate_IsCreateDelete(t *testing.T) {
	z := strings.Repeat("0", 40)
	a := strings.Repeat("a", 40)
	if !(Update{Old: z, New: a}).IsCreate() || (Update{Old: a, New: a}).IsCreate() {
		t.Error("IsCreate wrong")
	}
	if !(Update{Old: a, New: z}).IsDelete() || (Update{Old: a, New: a}).IsDelete() {
		t.Error("IsDelete wrong")
	}
	if !(Update{Old: strings.Repeat("0", 64), New: a}).IsCreate() {
		t.Error("sha256 zero not recognised")
	}
}

type rules []Rule

func (r rules) Rules(context.Context, string, string) ([]Rule, error) { return r, nil }

func TestProtection_CheckPush(t *testing.T) {
	z, a, b := strings.Repeat("0", 40), strings.Repeat("a", 40), strings.Repeat("b", 40)
	tests := []struct {
		name  string
		rules []Rule
		u     Update
		want  int // number of rejections
	}{
		{"unprotected branch", []Rule{{Pattern: "main", RequirePR: true}}, Update{Ref: "refs/heads/dev", Old: a, New: b}, 0},
		{"direct push needs PR", []Rule{{Pattern: "main", RequirePR: true}}, Update{Ref: "refs/heads/main", Old: a, New: b}, 1},
		{"create protected", []Rule{{Pattern: "main", RequirePR: true}}, Update{Ref: "refs/heads/main", Old: z, New: b}, 1},
		{"no PR required, ff ok", []Rule{{Pattern: "main"}}, Update{Ref: "refs/heads/main", Old: a, New: b}, 0},
		{"force refused", []Rule{{Pattern: "main"}}, Update{Ref: "refs/heads/main", Old: a, New: b, Forced: true}, 1},
		{"force allowed", []Rule{{Pattern: "main", AllowForce: true}}, Update{Ref: "refs/heads/main", Old: a, New: b, Forced: true}, 0},
		{"delete refused even if force allowed", []Rule{{Pattern: "main", AllowForce: true}}, Update{Ref: "refs/heads/main", Old: a, New: z}, 1},
		{"glob", []Rule{{Pattern: "release/*", RequirePR: true}}, Update{Ref: "refs/heads/release/1", Old: a, New: b}, 1},
		{"glob does not cross slash", []Rule{{Pattern: "release/*", RequirePR: true}}, Update{Ref: "refs/heads/release/1/x", Old: a, New: b}, 0},
		{"tags ignored", []Rule{{Pattern: "*", RequirePR: true}}, Update{Ref: "refs/tags/v1", Old: z, New: b}, 0},
		{"first match wins", []Rule{{Pattern: "main", AllowForce: true}, {Pattern: "*", RequirePR: true}}, Update{Ref: "refs/heads/main", Old: a, New: b, Forced: true}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Protection{Source: rules(tt.rules)}.CheckPush(context.Background(), Request{Updates: []Update{tt.u}})
			if err != nil || len(got) != tt.want {
				t.Errorf("got %v, %v; want %d rejections", got, err, tt.want)
			}
		})
	}
}

type recorder struct {
	mu     sync.Mutex
	pushed []Request
}

func (r *recorder) Pushed(_ context.Context, req Request) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushed = append(r.pushed, req)
	return nil
}

func (r *recorder) all() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Request(nil), r.pushed...)
}

func git(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.com")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := git(t, dir, args...); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestEndToEnd pushes through the real HTTP handler, real git, the installed
// hooks and the unix-socket bridge.
func TestEndToEnd(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp("", "edugit-hooks") // short path for the unix socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	sock := filepath.Join(tmp, "h.sock")

	rec := &recorder{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const secret = "s3cret"
	bridge := &Bridge{
		Secret: secret,
		Policy: Protection{Source: rules{{Pattern: "main", RequirePR: false}, {Pattern: "locked", RequirePR: true}}},
		Sink:   rec,
		Log:    log,
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- bridge.Serve(ctx, sock) }()
	t.Cleanup(func() { cancel(); <-served })
	for i := 0; i < 200; i++ { // wait for the socket
		if _, err := os.Stat(sock); err == nil {
			break
		}
		exec.Command("sleep", "0.01").Run()
	}

	repos, err := gitserver.NewRepos(filepath.Join(tmp, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	repos.InstallHooks = func(p string) error { return Install(p, exe) }
	if err := repos.Create(context.Background(), "c1", "demo", "main"); err != nil {
		t.Fatal(err)
	}

	h := &gitserver.Handler{
		Repos: repos,
		Log:   log,
		Auth:  func(*http.Request, string, string, bool) error { return nil },
		HookEnv: func(_ *http.Request, course, repo string) []string {
			return []string{EnvSocket + "=" + sock, EnvToken + "=" + secret, EnvCourse + "=" + course, EnvRepo + "=" + repo, EnvUser + "=alice"}
		},
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	work := t.TempDir()
	mustGit(t, work, "clone", srv.URL+"/git/c1/demo.git", "a")
	a := filepath.Join(work, "a")
	mustGit(t, a, "commit", "--allow-empty", "-m", "one")
	mustGit(t, a, "push", "origin", "HEAD:main")

	if got := rec.all(); len(got) != 1 || got[0].User != "alice" || got[0].Course != "c1" || got[0].Repo != "demo" ||
		len(got[0].Updates) != 1 || got[0].Updates[0].Ref != "refs/heads/main" || !got[0].Updates[0].IsCreate() {
		t.Fatalf("post-receive events = %+v", got)
	}

	t.Run("fast-forward allowed", func(t *testing.T) {
		mustGit(t, a, "commit", "--allow-empty", "-m", "two")
		mustGit(t, a, "push", "origin", "HEAD:main")
	})
	t.Run("force-push refused", func(t *testing.T) {
		mustGit(t, a, "reset", "--hard", "HEAD~1")
		mustGit(t, a, "commit", "--allow-empty", "-m", "rewritten")
		out, err := git(t, a, "push", "--force", "origin", "HEAD:main")
		if err == nil || !strings.Contains(out, "force-push is not allowed") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})
	t.Run("delete refused", func(t *testing.T) {
		out, err := git(t, a, "push", "origin", ":main")
		if err == nil || !strings.Contains(out, "cannot be deleted") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})
	t.Run("PR required refused", func(t *testing.T) {
		out, err := git(t, a, "push", "origin", "HEAD:locked")
		if err == nil || !strings.Contains(out, "pull request") {
			t.Errorf("err=%v out=%s", err, out)
		}
	})
	t.Run("rejected push leaves no ref", func(t *testing.T) {
		p, _ := repos.Path("c1", "demo")
		if out, _ := git(t, p, "rev-parse", "--verify", "-q", "refs/heads/locked"); strings.TrimSpace(out) != "" {
			t.Errorf("refs/heads/locked exists: %s", out)
		}
	})
	t.Run("feature branch allowed", func(t *testing.T) {
		mustGit(t, a, "push", "origin", "HEAD:feature")
	})
}

func TestBridge_RejectsBadSecret(t *testing.T) {
	b := &Bridge{Secret: "x", Policy: Protection{Source: rules{}}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(b.Handler())
	defer srv.Close()
	for _, auth := range []string{"", "Bearer nope"} {
		req, _ := http.NewRequest("POST", srv.URL+"/pre-receive", strings.NewReader("{}"))
		req.Header.Set("Authorization", auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Errorf("auth %q: status %d, want 401", auth, resp.StatusCode)
		}
	}
}

func TestRun_FailsClosedWithoutServer(t *testing.T) {
	env := map[string]string{EnvSocket: "/nonexistent.sock", EnvToken: "x", EnvCourse: "c", EnvRepo: "r"}
	getenv := func(k string) string { return env[k] }
	var sb strings.Builder
	if code := Run(context.Background(), PreReceive, strings.NewReader(""), &sb, getenv); code != 1 {
		t.Errorf("pre-receive code = %d, want 1", code)
	}
	if code := Run(context.Background(), PostReceive, strings.NewReader(""), &sb, getenv); code != 0 {
		t.Errorf("post-receive code = %d, want 0", code)
	}
	// Direct pushes to the bare repo (no env) are refused by pre-receive.
	if code := Run(context.Background(), PreReceive, strings.NewReader(""), &sb, func(string) string { return "" }); code != 1 {
		t.Errorf("no-env pre-receive code = %d, want 1", code)
	}
}

func TestInstall_RejectsUnsafePath(t *testing.T) {
	for _, bin := range []string{"relative/edugit", "/tmp/it's", "/tmp/a\nb"} {
		if err := Install(t.TempDir(), bin); err == nil {
			t.Errorf("Install(%q) succeeded", bin)
		}
	}
}
