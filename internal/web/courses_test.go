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

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

func TestCourses(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sess := &auth.Sessions{Backend: db}
	srv, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Sessions: sess, Tokens: db, Courses: db, Audit: db, Authz: &authz.Authorizer{Source: db},
		Domains: auth.Domains{Staff: "bth.se", Student: "student.bth.se"}, PublicURL: "https://x.test", LoginURL: "/saml/login",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	login := func(oid, email string, admin bool) func(method, path string, form url.Values) *httptest.ResponseRecorder {
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
		return func(method, path string, form url.Values) *httptest.ResponseRecorder {
			if form == nil {
				form = url.Values{}
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
	student := login("o2", "s@student.bth.se", false)

	create := url.Values{"slug": {"oop"}, "title": {"OOP"}, "term": {"HT26"}, "admin": {"t@bth.se"}}
	if w := student("POST", "/courses", create); w.Code != http.StatusForbidden {
		t.Fatalf("student create: got %d, want 403", w.Code)
	}
	bad := url.Values{"slug": {"oop"}, "title": {"OOP"}, "admin": {"x@student.bth.se"}}
	if w := admin("POST", "/courses", bad); w.Code != http.StatusBadRequest {
		t.Fatalf("student-domain course admin: got %d, want 400", w.Code)
	}
	if w := admin("POST", "/courses", create); w.Code != http.StatusSeeOther {
		t.Fatalf("create: got %d, want 303", w.Code)
	}
	if w := admin("POST", "/courses", create); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate: got %d, want 400", w.Code)
	}

	teacher := login("o3", "t@bth.se", false) // binds the pending course_admin
	if w := teacher("GET", "/courses/oop", nil); w.Code != http.StatusOK {
		t.Fatalf("course page: got %d", w.Code)
	}
	if w := student("GET", "/courses/oop", nil); w.Code != http.StatusNotFound {
		t.Fatalf("outsider: got %d, want 404", w.Code)
	}

	w := teacher("POST", "/courses/oop/enroll", url.Values{
		"emails": {"email\ns@student.bth.se,student\nbad@student.bth.se,teacher\nnope@@x\n"},
	})
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Enrolled 1.") || !strings.Contains(body, "not eligible") || !strings.Contains(body, "invalid address") {
		t.Fatalf("import: %d %s", w.Code, body)
	}
	if w := student("GET", "/courses/oop", nil); w.Code != http.StatusOK {
		t.Fatalf("enrolled student: got %d", w.Code)
	}
	if w := student("POST", "/courses/oop/enroll", url.Values{"emails": {"z@student.bth.se"}}); w.Code != http.StatusForbidden {
		t.Fatalf("student enroll: got %d, want 403", w.Code)
	}
	if w := teacher("POST", "/courses/oop/role", url.Values{"email": {"t@bth.se"}, "role": {"student"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("self change: got %d, want 400", w.Code)
	}

	w = teacher("POST", "/courses/oop/invite", nil)
	i := strings.Index(w.Body.String(), "/join/")
	if w.Code != http.StatusOK || i < 0 {
		t.Fatalf("invite: %d", w.Code)
	}
	token := w.Body.String()[i+len("/join/"):]
	token = token[:strings.Index(token, "<")]
	newbie := login("o4", "n@student.bth.se", false)
	if w := newbie("GET", "/join/"+token, nil); w.Code != http.StatusOK {
		t.Fatalf("join page: %d", w.Code)
	}
	if w := newbie("POST", "/join/"+token, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("join: %d", w.Code)
	}
	if w := newbie("GET", "/join/bogus", nil); w.Code != http.StatusNotFound {
		t.Fatalf("bad token: %d", w.Code)
	}
	if w := teacher("POST", "/courses/oop/remove", url.Values{"email": {"n@student.bth.se"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", w.Code)
	}
	if w := teacher("POST", "/courses/oop/archive", url.Values{"archived": {"1"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("archive: %d", w.Code)
	}
	if w := teacher("POST", "/courses/oop/enroll", url.Values{"emails": {"q@student.bth.se"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("archived enroll: %d", w.Code)
	}

	// Everything above must be in the audit log, visible only to admins.
	if w := teacher("GET", "/admin/audit", nil); w.Code != http.StatusNotFound {
		t.Fatalf("teacher audit view: %d", w.Code)
	}
	w = admin("GET", "/admin/audit", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin audit view: %d", w.Code)
	}
	for _, want := range []string{"course.create", "roster.enroll", "invite.create", "invite.join", "roster.remove", "course.archive"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("audit page missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), token) {
		t.Error("audit page leaks the invite token")
	}
}
