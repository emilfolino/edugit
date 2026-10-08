package web

import (
	"context"
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
	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

// fakeEditor records commits and implements BrowseGit (partly) and EditGit.
type fakeEditor struct {
	BrowseGit
	commits []string // "branch|from|expect|path"
	stale   bool
}

func (f *fakeEditor) DefaultBranch(context.Context, string, string) (string, error) {
	return "main", nil
}
func (f *fakeEditor) Branches(context.Context, string, string) ([]string, error) {
	return []string{"main"}, nil
}
func (f *fakeEditor) File(_ context.Context, _, _, _, path string) (gitserver.Blob, error) {
	if path == "new-file.txt" {
		return gitserver.Blob{}, gitserver.ErrNoPath
	}
	return gitserver.Blob{Content: "hello\n", Size: 6}, nil
}
func (f *fakeEditor) Resolve(context.Context, string, string, string) (string, error) {
	return "m1", nil
}
func (f *fakeEditor) CommitFiles(_ context.Context, _, _, branch, from, expect string, ch []gitserver.FileChange, _ gitserver.Identity, _ string) (string, error) {
	if f.stale {
		return "", gitserver.ErrStale
	}
	f.commits = append(f.commits, branch+"|"+from+"|"+expect+"|"+ch[0].Path)
	return "c1", nil
}

type denyMain struct{}

func (denyMain) CheckPush(_ context.Context, req hooks.Request) ([]string, error) {
	if req.Updates[0].Ref == "refs/heads/main" {
		return []string{"branch main is protected"}, nil
	}
	return nil, nil
}

type recSink struct{ got []hooks.Request }

func (s *recSink) Pushed(_ context.Context, req hooks.Request) error {
	s.got = append(s.got, req)
	return nil
}

func TestEditor(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sess := &auth.Sessions{Backend: db}
	ed, sink := &fakeEditor{}, &recSink{}
	srv, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Sessions: sess, Tokens: db, Courses: db, Audit: db, Repos: db, Disk: &fakeDisk{}, Assignments: db,
		Pulls: db, PullGit: &fakeGit{}, Issues: db, Browse: ed, Editor: ed, Policy: denyMain{}, Sink: sink,
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
	teacher("/courses/oop/enroll", url.Values{"emails": {"a@student.bth.se"}})
	a := login("oa", "a@student.bth.se", false)
	teacher("/courses/oop/repos", url.Values{"name": {"starter"}, "template": {"1"}})
	future := time.Now().UTC().Add(48 * time.Hour).Format(deadlineLayout)
	teacher("/courses/oop/assignments", url.Values{"slug": {"lab1"}, "title": {"Lab"}, "template": {"starter"},
		"mode": {"individual"}, "history": {"fresh"}, "team_size": {"2"}, "deadline": {future}})
	a("/courses/oop/assignments/lab1/accept", url.Values{})

	const repo = "/courses/oop/repos/lab1-a"
	save := func(extra url.Values) url.Values {
		f := url.Values{"ref": {"main"}, "path": {"a.txt"}, "content": {"x"}, "message": {"m"}, "base": {"m1"}}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	code := func(w *httptest.ResponseRecorder, want int, what string) {
		t.Helper()
		if w.Code != want {
			t.Errorf("%s: %d, want %d: %s", what, w.Code, want, w.Body.String())
		}
	}

	code(a(repo+"/edit/a.txt?ref=main", nil), http.StatusOK, "edit form")
	if w := a(repo+"/edit/a.txt?ref=main", nil); !strings.Contains(w.Body.String(), "hello") {
		t.Error("form lacks file content")
	}
	// Protected branch is refused; a new branch is allowed and the sink hears of it.
	code(a(repo+"/edit", save(nil)), http.StatusForbidden, "protected main")
	code(a(repo+"/edit", save(url.Values{"new_branch": {"fix"}})), http.StatusSeeOther, "new branch")
	if len(ed.commits) != 1 || ed.commits[0] != "fix|main||a.txt" {
		t.Errorf("commits = %v", ed.commits)
	}
	if len(sink.got) != 1 || sink.got[0].Updates[0].Ref != "refs/heads/fix" || sink.got[0].User == "" {
		t.Errorf("sink = %+v", sink.got)
	}
	// Missing message, stale head.
	code(a(repo+"/edit", save(url.Values{"new_branch": {"fix"}, "message": {""}})), http.StatusBadRequest, "no message")
	ed.stale = true
	code(a(repo+"/edit", save(url.Values{"ref": {"fix"}})), http.StatusConflict, "stale")
	ed.stale = false

	// CLI-only course: the editor is closed to students, open to staff.
	code(teacher("/courses/oop/commit-methods", url.Values{"methods": {"cli"}}), http.StatusSeeOther, "set cli")
	code(a(repo+"/edit/a.txt?ref=main", nil), http.StatusForbidden, "cli-only form")
	code(a(repo+"/edit", save(url.Values{"new_branch": {"fix2"}})), http.StatusForbidden, "cli-only save")
	code(teacher(repo+"/edit/a.txt?ref=main", nil), http.StatusOK, "staff form")
}
