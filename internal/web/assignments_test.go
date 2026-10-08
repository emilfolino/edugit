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
	"github.com/emilfolino/edugit/internal/store"
)

func TestAssignments(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sess := &auth.Sessions{Backend: db}
	disk := &fakeDisk{}
	srv, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Sessions: sess, Tokens: db, Courses: db, Audit: db, Repos: db, Disk: disk, Assignments: db,
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
	teacher("/courses/oop/enroll", url.Values{"emails": {"a@student.bth.se\nb@student.bth.se\nc@student.bth.se\nlate@student.bth.se"}})
	a := login("oa", "a@student.bth.se", false)
	b := login("ob", "b@student.bth.se", false)
	c := login("oc", "c@student.bth.se", false)
	late := login("ol", "late@student.bth.se", false)
	teacher("/courses/oop/repos", url.Values{"name": {"starter"}, "template": {"1"}})

	future := time.Now().UTC().Add(48 * time.Hour).Format(deadlineLayout)
	past := time.Now().UTC().Add(-48 * time.Hour).Format(deadlineLayout)
	create := func(slug, mode, deadline string) *httptest.ResponseRecorder {
		return teacher("/courses/oop/assignments", url.Values{"slug": {slug}, "title": {"Lab " + slug}, "template": {"starter"},
			"mode": {mode}, "history": {"fresh"}, "team_size": {"2"}, "deadline": {deadline}})
	}
	if w := create("lab1", "individual", future); w.Code != http.StatusSeeOther {
		t.Fatalf("create: got %d, want 303: %s", w.Code, w.Body)
	}
	if w := create("lab1", "individual", future); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate: got %d, want 400", w.Code)
	}
	if w := create("x", "individual", future); w.Code != http.StatusBadRequest {
		t.Fatalf("bad slug: got %d, want 400", w.Code)
	}
	if w := a("/courses/oop/assignments", url.Values{"slug": {"lab9"}}); w.Code != http.StatusForbidden {
		t.Fatalf("student create: got %d, want 403", w.Code)
	}

	if w := a("/courses/oop/assignments/lab1/accept", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("accept: got %d, want 303: %s", w.Code, w.Body)
	}
	if !strings.Contains(strings.Join(disk.created, ","), "oop/lab1-a") {
		t.Fatalf("disk: %v", disk.created)
	}
	if w := a("/courses/oop/assignments/lab1/accept", url.Values{}); w.Code != http.StatusBadRequest {
		t.Fatalf("accept twice: got %d, want 400", w.Code)
	}
	if w := teacher("/courses/oop/assignments/lab1/accept", url.Values{}); w.Code != http.StatusForbidden {
		t.Fatalf("teacher accept: got %d, want 403", w.Code)
	}
	if body := a("/courses/oop/assignments/lab1", nil).Body.String(); !strings.Contains(body, "lab1-a.git") {
		t.Fatalf("student page: %s", body)
	}

	// Bulk generation covers the students without a repo.
	w := teacher("/courses/oop/assignments/lab1/generate", url.Values{})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Created 3 repositories") {
		t.Fatalf("generate: %d %s", w.Code, w.Body)
	}

	// Deadlines: closed after the deadline unless extended.
	create("lab2", "individual", past)
	if w := late("/courses/oop/assignments/lab2/accept", url.Values{}); w.Code != http.StatusBadRequest {
		t.Fatalf("late accept: got %d, want 400", w.Code)
	}
	if w := teacher("/courses/oop/assignments/lab2/extend", url.Values{"email": {"late@student.bth.se"}, "deadline": {future}}); w.Code != http.StatusSeeOther {
		t.Fatalf("extend: got %d, want 303: %s", w.Code, w.Body)
	}
	if w := late("/courses/oop/assignments/lab2/accept", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("extended accept: got %d, want 303", w.Code)
	}

	// Teams of two.
	create("proj", "team", future)
	if w := a("/courses/oop/assignments/proj/teams", url.Values{"name": {"Bad Name"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad team: got %d, want 400", w.Code)
	}
	if w := a("/courses/oop/assignments/proj/teams", url.Values{"name": {"red"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("team: got %d, want 303: %s", w.Code, w.Body)
	}
	if w := a("/courses/oop/assignments/proj/teams", url.Values{"name": {"blue"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("second team: got %d, want 400", w.Code)
	}
	teams, _ := db.Teams(ctx, 3)
	if len(teams) != 1 {
		t.Fatalf("teams: %+v", teams)
	}
	join := "/courses/oop/assignments/proj/teams/" + itoa(int(teams[0].ID)) + "/join"
	if w := b(join, url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("join: got %d, want 303", w.Code)
	}
	if w := c(join, url.Values{}); w.Code != http.StatusBadRequest {
		t.Fatalf("full team: got %d, want 400", w.Code)
	}
	if w := b("/courses/oop/assignments/proj/teams/leave", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("leave: got %d, want 303", w.Code)
	}
	if w := c(join, url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("join freed seat: got %d, want 303", w.Code)
	}
	if body := c("/courses/oop/assignments/proj", nil).Body.String(); !strings.Contains(body, "proj-red.git") {
		t.Fatalf("team page: %s", body)
	}

	// A failed disk step leaves nothing behind.
	disk.fail = true
	if w := late("/courses/oop/assignments/proj/teams", url.Values{"name": {"green"}}); w.Code != http.StatusInternalServerError {
		t.Fatalf("disk failure: got %d, want 500", w.Code)
	}
	disk.fail = false
	if w := late("/courses/oop/assignments/proj/teams", url.Values{"name": {"green"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("retry: got %d, want 303: %s", w.Code, w.Body)
	}
}
