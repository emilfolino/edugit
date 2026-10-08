package store

import (
	"context"
	"errors"
	"testing"
)

func TestGrades(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, _ := db.CreateCourse(ctx, "oop", "OOP", "")
	tmpl, _ := db.CreateRepo(ctx, c.ID, "starter", "teacher", true)
	stu, err := db.LoginUser(ctx, "a@student.bth.se", "a@student.bth.se", "a", false)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMembership(ctx, c.ID, stu.ID, "student", "manual")
	a, err := db.CreateAssignment(ctx, Assignment{CourseID: c.ID, TemplateRepoID: tmpl.ID, Slug: "lab1", Title: "Lab 1",
		Mode: "individual", History: "fresh", TeamSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := db.CreateAssignmentRepo(ctx, a, "lab1-a", stu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddCriterion(ctx, a.ID, "Tests", 5); err != nil {
		t.Fatal(err)
	}
	if err := db.AddCriterion(ctx, a.ID, "Style", 3); err != nil {
		t.Fatal(err)
	}
	rubric, _ := db.Rubric(ctx, a.ID)
	if len(rubric) != 2 || rubric[0].Title != "Tests" {
		t.Fatalf("rubric = %+v", rubric)
	}
	if _, err := db.GradeFor(ctx, repo.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungraded: %v", err)
	}
	if err := db.SaveGrade(ctx, a.ID, repo.ID, stu.ID, "", map[int64]int{rubric[0].ID: 6}); err == nil {
		t.Error("score above maximum accepted")
	}
	if err := db.SaveGrade(ctx, a.ID, repo.ID, stu.ID, "good", map[int64]int{rubric[0].ID: 4, rubric[1].ID: 3}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGrade(ctx, a.ID, repo.ID, stu.ID, "better", map[int64]int{rubric[0].ID: 5, rubric[1].ID: 3}); err != nil {
		t.Fatal(err)
	}
	g, err := db.GradeFor(ctx, repo.ID)
	if err != nil || g.Feedback != "better" || g.Scores[rubric[0].ID] != 5 {
		t.Fatalf("grade = %+v, %v", g, err)
	}
	totals, _ := db.GradeTotals(ctx, a.ID)
	if totals[repo.ID] != 8 {
		t.Errorf("total = %d, want 8", totals[repo.ID])
	}
	rows, err := db.GradeRows(ctx, a.ID)
	if err != nil || len(rows) != 1 || !rows[0].Graded || rows[0].Points != 8 || rows[0].Repo != "lab1-a" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if err := db.DeleteCriterion(ctx, a.ID, rubric[1].ID); err != nil {
		t.Fatal(err)
	}
	if totals, _ = db.GradeTotals(ctx, a.ID); totals[repo.ID] != 5 {
		t.Errorf("total after delete = %d, want 5", totals[repo.ID])
	}
}
