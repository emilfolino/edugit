package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/store"
)

// fakeGit serves canned branches and records merges.
type fakeGit struct {
	branches map[string]string
	ahead    int
	conflict bool
	merges   []string
	mergeErr error
}

func (g *fakeGit) Resolve(_ context.Context, _, _, branch string) (string, error) {
	if sha, ok := g.branches[branch]; ok {
		return sha, nil
	}
	return "", errors.New("no such branch")
}

func (g *fakeGit) Commits(context.Context, string, string, string, string) ([]gitserver.Commit, error) {
	out := make([]gitserver.Commit, g.ahead)
	for i := range out {
		out[i] = gitserver.Commit{SHA: "abcdef0123456789", Author: "A", Subject: "work", When: time.Now()}
	}
	return out, nil
}

func (g *fakeGit) Diff(context.Context, string, string, string, string) (string, error) {
	return "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-old\n+new\n", nil
}

func (g *fakeGit) Mergeable(context.Context, string, string, string, string) (bool, error) {
	return !g.conflict, nil
}

func (g *fakeGit) Merge(_ context.Context, _, _, base, head, strategy string, by gitserver.Identity, msg string) (string, error) {
	if g.mergeErr != nil {
		return "", g.mergeErr
	}
	g.merges = append(g.merges, base+"<-"+head+":"+strategy+":"+by.Email+":"+msg)
	return "merged0123", nil
}

func TestPulls(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sess := &auth.Sessions{Backend: db}
	git := &fakeGit{branches: map[string]string{"main": "m1", "topic": "t1", "feedback": "f1"}, ahead: 1}
	srv, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Sessions: sess, Tokens: db, Courses: db, Audit: db, Repos: db, Disk: &fakeDisk{}, Assignments: db,
		Pulls: db, PullGit: git,
		Authz:   &authz.Authorizer{Source: db},
		Domains: auth.Domains{Staff: "bth.se", Student: "student.bth.se"}, PublicURL: "https://x.test", LoginURL: "/saml/login",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	login := func(oid, email string, admin bool) func(string, url.Values) *httptest.ResponseRecorder {
		u, err := db.LoginUser(ctx, oid, email, email, admin)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		if err := sess.Start(rec, httptest.NewRequest("GET", "/", nil), u.ID); err != nil {
			t.Fatal(err)
		}
		cookie := rec.Result().Cookies()[0]
		probe := httptest.NewRequest("GET", "/", nil)
		probe.AddCookie(cookie)
		csrf := sess.CSRFToken(probe)
		return func(path string, form url.Values) *httptest.ResponseRecorder {
			method := "POST"
			if form == nil {
				method, form = "GET", url.Values{}
			}
			form.Set("csrf", csrf)
			r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			return w
		}
	}
	admin := login("o1", "efo@bth.se", true)
	admin("/courses", url.Values{"slug": {"oop"}, "title": {"OOP"}, "term": {"HT26"}, "admin": {"t@bth.se"}})
	teacher := login("o3", "t@bth.se", false)
	teacher("/courses/oop/enroll", url.Values{"emails": {"a@student.bth.se\nb@student.bth.se"}})
	a := login("oa", "a@student.bth.se", false)
	b := login("ob", "b@student.bth.se", false)
	teacher("/courses/oop/repos", url.Values{"name": {"starter"}, "template": {"1"}})
	future := time.Now().UTC().Add(48 * time.Hour).Format(deadlineLayout)
	teacher("/courses/oop/assignments", url.Values{"slug": {"lab1"}, "title": {"Lab"}, "template": {"starter"},
		"mode": {"individual"}, "history": {"fresh"}, "team_size": {"2"}, "deadline": {future}})
	a("/courses/oop/assignments/lab1/accept", url.Values{})

	base := "/courses/oop/repos/lab1-a/pulls"
	open := url.Values{"head": {"topic"}, "base": {"main"}, "title": {"My work"}}
	expect := func(w *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("%s: got %d, want %d: %s", what, w.Code, code, w.Body)
		}
	}

	expect(b(base, nil), http.StatusNotFound, "other student lists")
	expect(b(base, open), http.StatusNotFound, "other student creates")
	expect(a(base, url.Values{"head": {"topic"}, "base": {"main"}}), http.StatusBadRequest, "no title")
	expect(a(base, url.Values{"head": {"main"}, "base": {"main"}, "title": {"x"}}), http.StatusBadRequest, "same branch")
	expect(a(base, url.Values{"head": {"nope"}, "base": {"main"}, "title": {"x"}}), http.StatusBadRequest, "unknown branch")
	git.ahead = 0
	expect(a(base, open), http.StatusBadRequest, "nothing ahead")
	git.ahead = 2
	expect(a(base, open), http.StatusSeeOther, "create")
	expect(a(base, open), http.StatusBadRequest, "duplicate")

	w := a(base+"/1", nil)
	expect(w, http.StatusOK, "view")
	if body := w.Body.String(); !strings.Contains(body, "My work") || !strings.Contains(body, "<code>new</code>") {
		t.Fatalf("view: %s", body)
	}
	expect(a(base+"/1?view=split", nil), http.StatusOK, "split view")
	expect(a(base+"/9", nil), http.StatusNotFound, "unknown pull")
	expect(b(base+"/1", nil), http.StatusNotFound, "other student views")

	// Students cannot open the staff feedback PR; teachers can.
	expect(a(base+"/feedback", url.Values{}), http.StatusForbidden, "student feedback")
	expect(teacher(base+"/feedback", url.Values{}), http.StatusSeeOther, "teacher feedback")
	expect(teacher(base+"/feedback", url.Values{}), http.StatusBadRequest, "second feedback")
	expect(teacher("/courses/oop/repos/starter/pulls/feedback", url.Values{}), http.StatusForbidden, "feedback on teacher repo")

	// Merging.
	expect(a(base+"/1/merge", url.Values{"strategy": {"bogus"}}), http.StatusBadRequest, "bad strategy")
	git.mergeErr = gitserver.ErrConflict
	expect(a(base+"/1/merge", url.Values{"strategy": {"merge"}}), http.StatusBadRequest, "conflict")
	git.mergeErr = nil
	expect(a(base+"/1/merge", url.Values{"strategy": {"squash"}}), http.StatusSeeOther, "merge")
	if len(git.merges) != 1 || !strings.HasPrefix(git.merges[0], "main<-t1:squash:a@student.bth.se:") {
		t.Fatalf("merges: %v", git.merges)
	}
	expect(a(base+"/1/merge", url.Values{"strategy": {"merge"}}), http.StatusBadRequest, "merge twice")
	expect(a(base+"/1/reopen", url.Values{}), http.StatusBadRequest, "reopen merged")

	// Close and reopen.
	expect(a(base+"/2/close", url.Values{}), http.StatusSeeOther, "close")
	expect(a(base+"/2/close", url.Values{}), http.StatusBadRequest, "close twice")
	expect(a(base+"/2/reopen", url.Values{}), http.StatusSeeOther, "reopen")
}
