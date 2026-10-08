package store

import (
	"context"
	"testing"
)

func TestAudit(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, err := db.LoginUser(ctx, "o", "a@bth.se", "A", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"x", "y", "x"} {
		if err := db.Audit(ctx, u.ID, a, "t", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Audit(ctx, 0, "z", "", ""); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name           string
		before, limit  int64
		action         string
		wantN          int
		wantFirstActor string
	}{
		{"all", 0, 10, "", 4, ""},
		{"limit", 0, 2, "", 2, ""},
		{"filter", 0, 10, "x", 2, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.AuditEntries(ctx, tt.before, int(tt.limit), tt.action)
			if err != nil || len(got) != tt.wantN {
				t.Fatalf("got %d entries, err %v; want %d", len(got), err, tt.wantN)
			}
			if tt.action != "" && got[0].Actor != u.Username {
				t.Errorf("actor %q, want %q", got[0].Actor, u.Username)
			}
		})
	}
	page, _ := db.AuditEntries(ctx, 0, 2, "")
	rest, _ := db.AuditEntries(ctx, page[1].ID, 10, "")
	if len(rest) != 2 || rest[0].ID >= page[1].ID {
		t.Errorf("paging wrong: %v", rest)
	}
}
