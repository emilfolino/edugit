package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/store"
)

func TestAuthorizer_Git(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mk := func(sub, email string, admin bool) store.User {
		u, err := db.LoginUser(ctx, sub, email, sub, admin)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	alice, bob, teach, stranger, root := mk("alice", "alice@student.bth.se", false), mk("bob", "bob@student.bth.se", false),
		mk("teach", "t@bth.se", false), mk("stranger", "s@student.bth.se", false), mk("root", "r@bth.se", true)

	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	c2, _ := db.CreateCourse(ctx, "web", "Web", "")
	for _, m := range []struct {
		c    store.Course
		u    store.User
		role string
	}{{c, alice, "student"}, {c, bob, "student"}, {c, teach, "teacher"}, {c2, stranger, "teacher"}} {
		if err := db.SetMembership(ctx, m.c.ID, m.u.ID, m.role, "manual"); err != nil {
			t.Fatal(err)
		}
	}
	mine, err := db.CreateRepo(ctx, c.ID, "alice-lab1", "student", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddRepoMember(ctx, mine.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateRepo(ctx, c.ID, "starter", "teacher", true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateRepo(ctx, c.ID, "solutions", "teacher", false); err != nil {
		t.Fatal(err)
	}

	var caller *store.User
	z := &authz.Authorizer{Source: db, Authenticate: func(*http.Request) (store.User, bool) {
		if caller == nil {
			return store.User{}, false
		}
		return *caller, true
	}}
	req := httptest.NewRequest("GET", "/", nil)

	tests := []struct {
		name   string
		who    *store.User
		course string
		repo   string
		write  bool
		want   error
	}{
		{"anonymous", nil, "oop", "alice-lab1", false, gitserver.ErrUnauthenticated},
		{"owner clones", &alice, "oop", "alice-lab1", false, nil},
		{"owner pushes", &alice, "oop", "alice-lab1", true, nil},
		{"classmate sees nothing", &bob, "oop", "alice-lab1", false, gitserver.ErrNotFound},
		{"teacher clones", &teach, "oop", "alice-lab1", false, nil},
		{"teacher of other course sees nothing", &stranger, "oop", "alice-lab1", false, gitserver.ErrNotFound},
		{"student clones template", &alice, "oop", "starter", false, nil},
		{"student cannot push template", &alice, "oop", "starter", true, gitserver.ErrForbidden},
		{"student cannot see solutions", &alice, "oop", "solutions", false, gitserver.ErrNotFound},
		{"admin reads solutions", &root, "oop", "solutions", false, nil},
		{"missing repo", &alice, "oop", "nope", false, gitserver.ErrNotFound},
		{"wrong course for repo name", &alice, "web", "alice-lab1", false, gitserver.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller = tt.who
			if got := z.Git(req, tt.course, tt.repo, tt.write); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("editor-only course refuses student pushes", func(t *testing.T) {
		if err := db.SetCommitMethods(ctx, c.ID, "editor"); err != nil {
			t.Fatal(err)
		}
		defer db.SetCommitMethods(ctx, c.ID, "both")
		for _, tt := range []struct {
			name  string
			who   *store.User
			write bool
			want  error
		}{
			{"student push", &alice, true, gitserver.ErrForbidden},
			{"student clone", &alice, false, nil},
			{"teacher push", &teach, true, nil},
		} {
			caller = tt.who
			if got := z.Git(req, "oop", "alice-lab1", tt.write); got != tt.want {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
			}
		}
	})

	t.Run("cli-only course allows student pushes", func(t *testing.T) {
		if err := db.SetCommitMethods(ctx, c.ID, "cli"); err != nil {
			t.Fatal(err)
		}
		defer db.SetCommitMethods(ctx, c.ID, "both")
		caller = &alice
		if got := z.Git(req, "oop", "alice-lab1", true); got != nil {
			t.Errorf("got %v", got)
		}
	})

	t.Run("unenrolled member loses access", func(t *testing.T) {
		if err := db.RemoveMembership(ctx, c.ID, alice.ID); err != nil {
			t.Fatal(err)
		}
		caller = &alice
		if got := z.Git(req, "oop", "alice-lab1", false); got != gitserver.ErrNotFound {
			t.Errorf("got %v, want ErrNotFound", got)
		}
	})
}
