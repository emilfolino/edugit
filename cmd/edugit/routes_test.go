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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilfolino/edugit/internal/config"
)

// status fetches path without following redirects.
func (b *browser) status(path string) (int, string) {
	b.t.Helper()
	c := *b.c
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(b.base + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// TestRouteAccess checks the read routes (browser, site, CI, grading) against
// each kind of visitor.
func TestRouteAccess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Config{
		Addr: "127.0.0.1:0", DataDir: t.TempDir(), DevLogin: true,
		AdminEmails: []string{"teach@bth.se"}, StaffDomain: "bth.se", StudentDomain: "student.bth.se",
		ShutdownTimeout: time.Second, CIRuntime: "true", CIWorkers: 1, // a runtime that never runs real jobs
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
	teacher.must("/courses/oop/repos", url.Values{"name": {"solutions"}})
	teacher.must("/courses/oop/enroll", url.Values{"emails": {"a@student.bth.se\nb@student.bth.se"}, "role": {"student"}})

	work := t.TempDir()
	tok := teacher.token()
	dir := filepath.Join(work, "m")
	git(t, work, "init", "-q", "-b", "main", dir)
	write(t, filepath.Join(dir, "README.md"), "# Starter\n")
	write(t, filepath.Join(dir, "public/index.html"), "<h1>Hi</h1>")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "starter")
	git(t, dir, "push", "-q", fmt.Sprintf("http://t:%s@%s/git/oop/material.git", tok, strings.TrimPrefix(srv.URL, "http://")), "main")
	teacher.must("/courses/oop/site", url.Values{"repo": {"material"}, "dir": {"public"}})
	teacher.must("/courses/oop/assignments", url.Values{
		"slug": {"lab1"}, "title": {"Lab 1"}, "mode": {"individual"}, "history": {"fresh"}, "template": {"material"},
	})
	a := newBrowser(t, srv.URL, "a@student.bth.se")
	a.must("/courses/oop/assignments/lab1/accept", url.Values{})
	b := newBrowser(t, srv.URL, "b@student.bth.se")
	outsider := newBrowser(t, srv.URL, "o@student.bth.se")
	jar, _ := cookiejar.New(nil)
	anon := &browser{t: t, base: srv.URL, c: &http.Client{Jar: jar}}

	const (
		ok     = 200
		hidden = 404
		login  = 303
	)
	repo := "/courses/oop/repos/lab1-a"
	tests := []struct {
		name string
		path string
		// expected status per visitor
		teacher, owner, peer, outsider, anon int
	}{
		{"tree", repo + "/tree", ok, ok, hidden, hidden, login},
		{"blob", repo + "/blob/README.md", ok, ok, hidden, hidden, login},
		{"history", repo + "/commits", ok, ok, hidden, hidden, login},
		{"blame", repo + "/blame/README.md", ok, ok, hidden, hidden, login},
		{"ci", repo + "/ci", ok, ok, hidden, hidden, login},
		{"ci run not found", repo + "/ci/999", hidden, hidden, hidden, hidden, login},
		{"ci run bad id", repo + "/ci/x", hidden, hidden, hidden, hidden, login},
		{"issues", repo + "/issues", ok, ok, hidden, hidden, login},
		{"pulls", repo + "/pulls", ok, ok, hidden, hidden, login},
		{"hidden teacher repo", "/courses/oop/repos/solutions/tree", ok, hidden, hidden, hidden, login},
		{"template repo", "/courses/oop/repos/material/tree", ok, ok, ok, hidden, login},
		{"site", "/sites/oop/", ok, ok, ok, hidden, login},
		{"grading", "/courses/oop/assignments/lab1/grading", ok, 403, 403, hidden, login},
		{"grade form", "/courses/oop/assignments/lab1/grading/lab1-a", ok, 403, 403, hidden, login},
		{"grades csv", "/courses/oop/assignments/lab1/grades.csv", ok, 403, 403, hidden, login},
		{"export", "/courses/oop/export.zip", ok, 403, 403, hidden, login},
	}
	for _, tt := range tests {
		for who, want := range map[string]int{"teacher": tt.teacher, "owner": tt.owner, "peer": tt.peer, "outsider": tt.outsider, "anon": tt.anon} {
			br := map[string]*browser{"teacher": teacher, "owner": a, "peer": b, "outsider": outsider, "anon": anon}[who]
			got, body := br.status(tt.path)
			// Hiding a course may be 403 or 404 for a non-member of a course page; both mean "no".
			if got != want && !(want == hidden && got == 403) {
				t.Errorf("%s as %s: %d, want %d (%.80s)", tt.name, who, got, want, body)
			}
		}
	}

	// Content, not just status.
	if _, body := a.status(repo + "/blob/README.md"); !strings.Contains(body, "Starter") {
		t.Errorf("blob lacks content: %.200s", body)
	}
	if _, body := b.status("/sites/oop/"); !strings.Contains(body, "Hi") {
		t.Errorf("site lacks content: %.200s", body)
	}

	// Traversal and bad refs never reach git.
	for _, p := range []string{
		repo + "/blob/..%2f..%2fetc/passwd",
		repo + "/blob/a/../../b",
		repo + "/tree?ref=--output%3d/tmp/x",
		repo + "/blob/README.md?ref=a..b",
		"/sites/oop/..%2f..%2fsecret",
	} {
		if got, body := teacher.status(p); got == 200 {
			t.Errorf("%s = 200: %.100s", p, body)
		}
	}
}
