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

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

// fakeDisk records bare-repo operations; fail makes Create error.
type fakeDisk struct {
	fail    bool
	created []string
}

func (d *fakeDisk) Create(_ context.Context, course, name, _ string) error {
	if d.fail {
		return errors.New("disk full")
	}
	d.created = append(d.created, course+"/"+name)
	return nil
}

func (d *fakeDisk) Delete(string, string) error { return nil }

func TestRepos(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sess := &auth.Sessions{Backend: db}
	disk := &fakeDisk{}
	srv, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Sessions: sess, Tokens: db, Courses: db, Audit: db, Repos: db, Disk: disk,
		Authz:   &authz.Authorizer{Source: db},
		Domains: auth.Domains{Staff: "bth.se", Student: "student.bth.se"}, PublicURL: "https://x.test", LoginURL: "/saml/login",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	login := func(oid, email string, admin bool) func(path string, form url.Values) *httptest.ResponseRecorder {
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
	student := login("o2", "s@student.bth.se", false)
	teacher("/courses/oop/enroll", url.Values{"emails": {"email\ns@student.bth.se,student\n"}})

	mk := func(name string, tmpl bool) *httptest.ResponseRecorder {
		f := url.Values{"name": {name}}
		if tmpl {
			f.Set("template", "1")
		}
		return teacher("/courses/oop/repos", f)
	}
	if w := mk("material", false); w.Code != http.StatusSeeOther {
		t.Fatalf("create: got %d, want 303", w.Code)
	}
	if w := mk("starter", true); w.Code != http.StatusSeeOther {
		t.Fatalf("create template: got %d, want 303", w.Code)
	}
	if w := mk("starter", true); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate: got %d, want 400", w.Code)
	}
	if w := mk("../evil", false); w.Code != http.StatusBadRequest {
		t.Fatalf("bad name: got %d, want 400", w.Code)
	}
	if got := strings.Join(disk.created, ","); got != "oop/material,oop/starter" {
		t.Fatalf("disk: got %q", got)
	}

	// A failed disk step must not leave a metadata row behind.
	disk.fail = true
	if w := mk("broken", false); w.Code != http.StatusBadRequest {
		t.Fatalf("disk failure: got %d, want 400", w.Code)
	}
	disk.fail = false
	if w := mk("broken", false); w.Code != http.StatusSeeOther {
		t.Fatalf("retry after rollback: got %d, want 303", w.Code)
	}

	body := student("/courses/oop", nil).Body.String()
	if !strings.Contains(body, "starter") || strings.Contains(body, "material") || strings.Contains(body, "broken") {
		t.Fatalf("student sees wrong repos: %s", body)
	}
	if strings.Contains(body, "Create repository") || strings.Contains(body, "template\"") && strings.Contains(body, "Mark as template") {
		t.Fatalf("student sees admin controls")
	}
	body = teacher("/courses/oop", nil).Body.String()
	if !strings.Contains(body, "material") || !strings.Contains(body, "https://x.test/git/oop/material.git") {
		t.Fatalf("teacher view: %s", body)
	}

	if w := student("/courses/oop/repos", url.Values{"name": {"mine"}}); w.Code != http.StatusForbidden {
		t.Fatalf("student create: got %d, want 403", w.Code)
	}
	if w := student("/courses/oop/repos/material/template", url.Values{"template": {"1"}}); w.Code != http.StatusForbidden {
		t.Fatalf("student toggle: got %d, want 403", w.Code)
	}
	if w := teacher("/courses/oop/repos/material/template", url.Values{"template": {"1"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("toggle: got %d, want 303", w.Code)
	}
	if !strings.Contains(student("/courses/oop", nil).Body.String(), "material") {
		t.Fatal("student should see material once it is a template")
	}
	if w := teacher("/courses/oop/repos/nope/template", url.Values{"template": {"1"}}); w.Code != http.StatusNotFound {
		t.Fatalf("unknown repo: got %d, want 404", w.Code)
	}
}
