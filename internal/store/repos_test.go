package store

import (
	"context"
	"errors"
	"testing"
)

func TestRepos(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.CreateCourse(ctx, "oop", "OOP", "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.LoginUser(ctx, "oid", "a@student.bth.se", "A", false)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := db.CreateRepo(ctx, c.ID, "material", "teacher", true)
	if err != nil {
		t.Fatal(err)
	}
	sol, err := db.CreateRepo(ctx, c.ID, "solutions", "teacher", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateRepo(ctx, c.ID, "material", "teacher", false); err == nil {
		t.Error("duplicate name accepted")
	}
	if err := db.AddRepoMember(ctx, sol.ID, u.ID); err != nil {
		t.Fatal(err)
	}

	list, err := db.CourseRepos(ctx, c.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "material" || list[0].Member || !list[0].IsTemplate || !list[1].Member {
		t.Errorf("list = %+v", list)
	}

	if err := db.SetTemplate(ctx, sol.ID, true); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := db.RepoByName(ctx, "oop", "solutions", u.ID); !r.IsTemplate {
		t.Error("template flag not set")
	}
	if err := db.SetTemplate(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown repo: got %v, want ErrNotFound", err)
	}

	if _, err := db.db.ExecContext(ctx,
		`INSERT INTO branch_protections (repo_id, pattern, require_pr) VALUES (?, 'main', 1)`, mat.ID); err != nil {
		t.Fatal(err)
	}
	rules, err := db.BranchRules(ctx, "oop", "material")
	if err != nil || len(rules) != 1 || rules[0].Pattern != "main" || !rules[0].RequirePR || rules[0].AllowForce {
		t.Errorf("rules = %+v, %v", rules, err)
	}
	if rules, _ := db.BranchRules(ctx, "oop", "nope"); len(rules) != 0 {
		t.Errorf("unknown repo has rules: %+v", rules)
	}

	if err := db.DeleteRepo(ctx, mat.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.RepoByName(ctx, "oop", "material", u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted repo: got %v", err)
	}
}
