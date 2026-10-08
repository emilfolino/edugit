package store

import (
	"context"
	"testing"
)

func TestLoginUser(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	u, err := s.LoginUser(ctx, "oid-1", "efo@bth.se", "Emil", true)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "efo" || !u.IsAdmin {
		t.Errorf("first login: got %+v, want username efo and admin", u)
	}

	// Same subject: updates in place, admin is sticky.
	u2, err := s.LoginUser(ctx, "oid-1", "emil@bth.se", "Emil F", false)
	if err != nil {
		t.Fatal(err)
	}
	if u2.ID != u.ID || u2.Email != "emil@bth.se" || !u2.IsAdmin {
		t.Errorf("relogin: got %+v, want same id, new email, still admin", u2)
	}

	// Different subject, same local part: username gets a suffix.
	u3, err := s.LoginUser(ctx, "oid-2", "efo@student.bth.se", "Other", false)
	if err != nil {
		t.Fatal(err)
	}
	if u3.Username != "efo-2" || u3.IsAdmin {
		t.Errorf("collision: got %+v, want username efo-2, not admin", u3)
	}
}

func TestUsernameFrom(t *testing.T) {
	tests := []struct{ email, subject, want string }{
		{"Anna.Svensson@bth.se", "x", "anna-svensson"},
		{"a+b@bth.se", "x", "ab"},
		{"", "12345678-aaaa", "user-12345678"},
	}
	for _, tt := range tests {
		if got := usernameFrom(tt.email, tt.subject); got != tt.want {
			t.Errorf("usernameFrom(%q): got %q, want %q", tt.email, got, tt.want)
		}
	}
}
