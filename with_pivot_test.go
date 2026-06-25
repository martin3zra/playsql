package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

// Student <-> Course via enrollments, with a pivot column "grade".
type Student struct {
	ID      int64     `db:"id" play:"pk,incrementing"`
	Name    string    `db:"name"`
	Courses []*Course `play:"belongsToMany,pivot=course_student,withPivot=grade"`
}

func (Student) TableName() string { return "students" }

type Course struct {
	ID    int64          `db:"id" play:"pk,incrementing"`
	Title string         `db:"title"`
	Pivot map[string]any `play:"pivot"`
}

func (Course) TableName() string { return "courses" }

func TestWithPivot(t *testing.T) {
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	stmts := []string{
		`CREATE TABLE students (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE courses (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE course_student (student_id INTEGER, course_id INTEGER, grade TEXT)`,
		`INSERT INTO students (id, name) VALUES (1,'Ann'),(2,'Bob')`,
		`INSERT INTO courses (id, title) VALUES (1,'Go'),(2,'SQL')`,
		// Ann/Go=A, Ann/SQL=B, Bob/Go=C  (same course, different grades per student)
		`INSERT INTO course_student (student_id, course_id, grade) VALUES (1,1,'A'),(1,2,'B'),(2,1,'C')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	var students []Student
	if err := db.Model(&Student{}).With("Courses").OrderBy("id", playsql.Asc).Get(ctx, &students); err != nil {
		t.Fatalf("get: %v", err)
	}

	ann := students[0]
	if len(ann.Courses) != 2 {
		t.Fatalf("Ann want 2 courses, got %d", len(ann.Courses))
	}
	grades := map[string]any{}
	for _, c := range ann.Courses {
		grades[c.Title] = c.Pivot["grade"]
	}
	if grades["Go"] != "A" || grades["SQL"] != "B" {
		t.Fatalf("Ann pivot grades wrong: %+v", grades)
	}

	// Bob shares course "Go" but with a different grade -> per-pair copy.
	bob := students[1]
	if len(bob.Courses) != 1 || bob.Courses[0].Pivot["grade"] != "C" {
		t.Fatalf("Bob pivot grade should be C, got %+v", bob.Courses)
	}
}
