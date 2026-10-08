package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/emilfolino/edugit/internal/config"
	"github.com/emilfolino/edugit/internal/hooks"
)

// TestMain lets the test binary double as the "edugit" executable that the
// installed git hooks call.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "hook" {
		os.Exit(hooks.Run(context.Background(), os.Args[2], os.Stdin, os.Stderr, os.Getenv))
	}
	os.Exit(m.Run())
}

// browser is a signed-in HTTP client that handles the CSRF token.
type browser struct {
	t    *testing.T
	base string
	c    *http.Client
	csrf string
}

func newBrowser(t *testing.T, base, email string) *browser {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	b := &browser{t: t, base: base, c: &http.Client{Jar: jar}}
	if _, code := b.post("/dev/login", url.Values{"email": {email}}); code != 200 {
		t.Fatalf("login %s: %d", email, code)
	}
	body, _ := b.get("/")
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		// The dashboard only shows a form to admins; the token page always has one.
		body, _ = b.get("/account/tokens")
		m = regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	}
	if m == nil {
		t.Fatalf("no csrf token for %s", email)
	}
	b.csrf = m[1]
	return b
}

func (b *browser) get(path string) (string, int) {
	b.t.Helper()
	resp, err := b.c.Get(b.base + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode
}

func (b *browser) post(path string, v url.Values) (string, int) {
	b.t.Helper()
	if b.csrf != "" {
		v.Set("csrf", b.csrf)
	}
	resp, err := b.c.PostForm(b.base+path, v)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode
}

// must posts and fails the test on a non-2xx final status.
func (b *browser) must(path string, v url.Values) string {
	b.t.Helper()
	body, code := b.post(path, v)
	if code/100 != 2 {
		b.t.Fatalf("POST %s = %d: %.300s", path, code, body)
	}
	return body
}

func (b *browser) token() string {
	b.t.Helper()
	body := b.must("/account/tokens", url.Values{"name": {"e2e"}, "days": {"1"}})
	m := regexp.MustCompile(`edg_[A-Za-z0-9_-]+`).FindString(body)
	if m == "" {
		b.t.Fatalf("no token in response: %.300s", body)
	}
	return m
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func gitFails(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("git %s should have failed:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEndToEnd drives the real application wiring through a teacher and a
// student: course, template repo, site, assignment, branch push, pull request,
// merge, protection, grading. Sign-in uses dev-login; the SAML path has its
// own tests in internal/auth.
func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Config{
		Addr: "127.0.0.1:0", DataDir: t.TempDir(), DevLogin: true,
		AdminEmails: []string{"teach@bth.se"}, StaffDomain: "bth.se", StudentDomain: "student.bth.se",
		ShutdownTimeout: time.Second,
	}
	h, closeApp, err := newApp(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	srv := httptest.NewServer(h)
	defer srv.Close()

	teacher := newBrowser(t, srv.URL, "teach@bth.se")
	teacher.must("/courses", url.Values{"slug": {"oop"}, "title": {"OOP"}, "term": {"HT26"}, "admin": {"teach@bth.se"}})
	teacher.must("/courses/oop/repos", url.Values{"name": {"material"}, "template": {"1"}})
	teacher.must("/courses/oop/enroll", url.Values{"emails": {"stud@student.bth.se"}, "role": {"student"}})

	// Teacher seeds the template (starter code and a site) over git.
	tok := teacher.token()
	work := t.TempDir()
	remote := func(user, tok, repo string) string {
		return fmt.Sprintf("http://%s:%s@%s/git/oop/%s.git", user, tok, strings.TrimPrefix(srv.URL, "http://"), repo)
	}
	tdir := filepath.Join(work, "material")
	git(t, work, "init", "-q", "-b", "main", tdir)
	write(t, filepath.Join(tdir, "README.md"), "# Starter\n")
	write(t, filepath.Join(tdir, "public/index.html"), "<h1>Welcome to OOP</h1>")
	git(t, tdir, "add", ".")
	git(t, tdir, "commit", "-q", "-m", "starter")
	git(t, tdir, "push", "-q", remote("teacher", tok, "material"), "main")

	// Anonymous and bad-token access is refused.
	if out := gitFails(t, work, "ls-remote", remote("x", "edg_bogus", "material")); !strings.Contains(out, "401") && !strings.Contains(out, "uthentication") {
		t.Errorf("bad token: %s", out)
	}

	// Publishing the site serves the pushed directory.
	teacher.must("/courses/oop/site", url.Values{"repo": {"material"}, "branch": {"main"}, "dir": {"public"}})
	if body, code := teacher.get("/sites/oop/"); code != 200 || !strings.Contains(body, "Welcome to OOP") {
		t.Fatalf("site = %d %.200s", code, body)
	}

	// Assignment from the template; the student accepts.
	teacher.must("/courses/oop/assignments", url.Values{
		"slug": {"lab1"}, "title": {"Lab 1"}, "mode": {"individual"}, "history": {"fresh"}, "template": {"material"},
	})
	student := newBrowser(t, srv.URL, "stud@student.bth.se")
	student.must("/courses/oop/assignments/lab1/accept", url.Values{})
	repo := "lab1-stud"
	stok := student.token()

	// Direct pushes to main are rejected; a branch push plus PR and merge works.
	sdir := filepath.Join(work, "student")
	git(t, work, "clone", "-q", remote("stud", stok, repo), sdir)
	write(t, filepath.Join(sdir, "solution.txt"), "42\n")
	git(t, sdir, "add", ".")
	git(t, sdir, "commit", "-q", "-m", "solve")
	if out := gitFails(t, sdir, "push", "origin", "HEAD:main"); !strings.Contains(out, "protected") && !strings.Contains(out, "rejected") && !strings.Contains(out, "declined") {
		t.Errorf("direct push to main: %s", out)
	}
	git(t, sdir, "push", "-q", "origin", "HEAD:refs/heads/solve")
	pulls := "/courses/oop/repos/" + repo + "/pulls"
	student.must(pulls, url.Values{"title": {"Solve it"}, "body": {"Fixes nothing"}, "head": {"solve"}, "base": {"main"}})
	if body, _ := student.get(pulls + "/1"); !strings.Contains(body, "Solve it") {
		t.Fatalf("PR page: %.300s", body)
	}
	student.must(pulls+"/1/merge", url.Values{"strategy": {"merge"}})
	git(t, sdir, "pull", "-q", "origin", "main")
	if got, err := os.ReadFile(filepath.Join(sdir, "solution.txt")); err != nil || string(got) != "42\n" {
		t.Errorf("merged main lacks the solution: %q %v", got, err)
	}

	// Another student cannot see the repo; staff can grade it.
	other := newBrowser(t, srv.URL, "other@student.bth.se")
	if _, code := other.get("/courses/oop/repos/" + repo + "/tree"); code != 404 && code != 403 {
		t.Errorf("outsider sees student repo: %d", code)
	}
	teacher.must("/courses/oop/assignments/lab1/grading/rubric", url.Values{"title": {"Works"}, "max": {"10"}})
	if body, code := teacher.get("/courses/oop/assignments/lab1/grades.csv"); code != 200 || !strings.Contains(body, "stud@student.bth.se") {
		t.Errorf("grades.csv = %d %.200s", code, body)
	}

	// The export is a zip for staff only.
	if _, code := student.get("/courses/oop/export.zip"); code == 200 {
		t.Error("student downloaded the export")
	}
	if body, code := teacher.get("/courses/oop/export.zip"); code != 200 || !strings.HasPrefix(body, "PK") {
		t.Errorf("export = %d", code)
	}
}
