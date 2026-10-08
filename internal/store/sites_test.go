package store

import (
	"context"
	"errors"
	"testing"
)

func TestSites(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	other, _ := db.CreateCourse(ctx, "db", "DB", "")
	r1, _ := db.CreateRepo(ctx, c.ID, "site", "teacher", false)
	r2, _ := db.CreateRepo(ctx, c.ID, "docs", "teacher", false)

	if _, err := db.SiteByCourse(ctx, "oop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no site yet: %v", err)
	}
	if err := db.SetSite(ctx, c.ID, r1.ID, "main", "public"); err != nil {
		t.Fatal(err)
	}
	got, err := db.SiteByCourse(ctx, "oop")
	if err != nil || got.Repo != "site" || got.Branch != "main" || got.Dir != "public" || got.CourseID != c.ID {
		t.Fatalf("site = %+v, %v", got, err)
	}

	// A second SetSite replaces: one site per course.
	if err := db.SetSite(ctx, c.ID, r2.ID, "pages", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.SiteByCourse(ctx, "oop"); got.Repo != "docs" || got.Branch != "pages" || got.Dir != "" {
		t.Errorf("replaced site = %+v", got)
	}
	if _, err := db.SiteByCourse(ctx, other.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("other course has a site: %v", err)
	}

	// Deleting the source repo drops the configuration with it.
	if err := db.DeleteRepo(ctx, r2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SiteByCourse(ctx, "oop"); !errors.Is(err, ErrNotFound) {
		t.Errorf("site survived repo delete: %v", err)
	}

	if err := db.SetSite(ctx, c.ID, r1.ID, "main", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearSite(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SiteByCourse(ctx, "oop"); !errors.Is(err, ErrNotFound) {
		t.Errorf("site after clear: %v", err)
	}
}
