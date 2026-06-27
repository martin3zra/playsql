package playsql_test

import (
	"context"
	"testing"

	"github.com/martin3zra/playsql"
)

type aPost struct {
	playsql.Model
	ID            int64       `db:"id" play:"pk,incrementing"`
	Title         string      `db:"title" play:"fillable"`
	CommentsCount int64       `db:"comments_count" play:"readonly"`     // Strategy A
	VotesSum      int64       `db:"comments_sum_votes" play:"readonly"` // Strategy A
	Comments      []*aComment `play:"hasMany,foreignKey=post_id"`
}

func (aPost) TableName() string { return "a_posts" }

type aComment struct {
	playsql.Model
	ID       int64 `db:"id" play:"pk,incrementing"`
	PostID   int64 `db:"post_id" play:"fillable"`
	Votes    int64 `db:"votes" play:"fillable"`
	Approved bool  `db:"approved" play:"fillable"`
}

func (aComment) TableName() string { return "a_comments" }

// setupAgg: p1 "hello" has comments (votes 2 approved, 3 not); p2 "empty" none.
func setupAgg(t *testing.T) (*playsql.DB, map[string]int64) {
	t.Helper()
	db, err := playsql.Open(playsql.Config{Driver: playsql.SQLite, Database: ":memory:"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE a_posts (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE a_comments (id INTEGER PRIMARY KEY, post_id INTEGER, votes INTEGER, approved BOOLEAN)`,
	} {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	ids := map[string]int64{}
	for _, title := range []string{"hello", "empty"} {
		p := &aPost{Title: title}
		if err := db.Insert(ctx, p); err != nil {
			t.Fatalf("insert post: %v", err)
		}
		ids[title] = p.ID
	}
	for _, c := range []aComment{
		{PostID: ids["hello"], Votes: 2, Approved: true},
		{PostID: ids["hello"], Votes: 3, Approved: false},
	} {
		if err := db.Insert(ctx, &c); err != nil {
			t.Fatalf("insert comment: %v", err)
		}
	}
	return db, ids
}

func byTitle(posts []aPost) map[string]aPost {
	m := map[string]aPost{}
	for _, p := range posts {
		m[p.Title] = p
	}
	return m
}

func TestWithCount_StrategyA(t *testing.T) {
	db, _ := setupAgg(t)
	got, err := playsql.Query[aPost](db).WithCount("Comments").Get(context.Background())
	if err != nil {
		t.Fatalf("withcount: %v", err)
	}
	m := byTitle(got)
	if m["hello"].CommentsCount != 2 {
		t.Fatalf("hello count = %d, want 2", m["hello"].CommentsCount)
	}
	if m["empty"].CommentsCount != 0 {
		t.Fatalf("empty count = %d, want 0", m["empty"].CommentsCount)
	}
}

func TestWithSum_StrategyA(t *testing.T) {
	db, _ := setupAgg(t)
	got, err := playsql.Query[aPost](db).WithSum("Comments", "votes").Get(context.Background())
	if err != nil {
		t.Fatalf("withsum: %v", err)
	}
	m := byTitle(got)
	if m["hello"].VotesSum != 5 {
		t.Fatalf("hello sum = %d, want 5", m["hello"].VotesSum)
	}
	if m["empty"].VotesSum != 0 { // NULL over empty set -> zero
		t.Fatalf("empty sum = %d, want 0", m["empty"].VotesSum)
	}
}

func TestWithCount_StrategyB_Extras(t *testing.T) {
	// exPost (from existence_test) embeds Model but has no comments_count field.
	db, _ := setupBlog(t)
	got, err := playsql.Query[exPost](db).WithCount("Comments").Get(context.Background())
	if err != nil {
		t.Fatalf("withcount B: %v", err)
	}
	for _, p := range got {
		switch p.Title {
		case "hello":
			if p.CountOf("Comments") != 2 {
				t.Fatalf("hello CountOf = %d, want 2", p.CountOf("Comments"))
			}
		case "empty":
			if p.CountOf("Comments") != 0 {
				t.Fatalf("empty CountOf = %d, want 0", p.CountOf("Comments"))
			}
		}
	}
}

func TestWithMinMaxAvg_Extras(t *testing.T) {
	db, _ := setupAgg(t)
	got, err := playsql.Query[aPost](db).
		WithMin("Comments", "votes").
		WithMax("Comments", "votes").
		WithAvg("Comments", "votes").
		Get(context.Background())
	if err != nil {
		t.Fatalf("min/max/avg: %v", err)
	}
	hello := byTitle(got)["hello"]
	if v, _ := hello.Aggregate("comments_min_votes"); toI(v) != 2 {
		t.Fatalf("min = %v, want 2", v)
	}
	if v, _ := hello.Aggregate("comments_max_votes"); toI(v) != 3 {
		t.Fatalf("max = %v, want 3", v)
	}
	if v, ok := hello.Aggregate("comments_avg_votes"); !ok || toF(v) < 2.4 || toF(v) > 2.6 {
		t.Fatalf("avg = %v, want ~2.5", v)
	}
}

func TestWithExists_Extras(t *testing.T) {
	db, _ := setupAgg(t)
	got, err := playsql.Query[aPost](db).WithExists("Comments").Get(context.Background())
	if err != nil {
		t.Fatalf("withexists: %v", err)
	}
	for _, p := range got {
		v, _ := p.Aggregate("comments_exists")
		switch p.Title {
		case "hello":
			if toI(v) != 1 {
				t.Fatalf("hello exists = %v, want 1", v)
			}
		case "empty":
			if toI(v) != 0 {
				t.Fatalf("empty exists = %v, want 0", v)
			}
		}
	}
}

func TestWithCount_AliasAndConstrain(t *testing.T) {
	db, _ := setupAgg(t)
	got, err := playsql.Query[aPost](db).
		WithCount("Comments", playsql.As("approved_count"),
			playsql.Constrain(func(q *playsql.Builder) { q.WhereEq("approved", true) })).
		Get(context.Background())
	if err != nil {
		t.Fatalf("alias/constrain: %v", err)
	}
	hello := byTitle(got)["hello"]
	if v, _ := hello.Aggregate("approved_count"); toI(v) != 1 { // only 1 approved
		t.Fatalf("approved_count = %v, want 1", v)
	}
}

func TestReadonlyField_PlainQueryWorks(t *testing.T) {
	// A model with readonly aggregate fields must still select/insert normally:
	// readonly columns are excluded from the default projection and from writes.
	db, _ := setupAgg(t)
	got, err := playsql.Query[aPost](db).OrderBy("id", playsql.Asc).Get(context.Background())
	if err != nil {
		t.Fatalf("plain get: %v", err)
	}
	if len(got) != 2 || got[0].Title != "hello" {
		t.Fatalf("unexpected: %+v", got)
	}
	if got[0].CommentsCount != 0 { // not requested -> zero, no error
		t.Fatalf("CommentsCount should be 0 without WithCount, got %d", got[0].CommentsCount)
	}
}

func TestWithCount_UnknownRelation(t *testing.T) {
	db, _ := setupAgg(t)
	_, err := playsql.Query[aPost](db).WithCount("Nope").Get(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown relation")
	}
}

// small numeric coercions for raw aggregate values in tests.
func toI(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return -1
	}
}

func toF(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	default:
		return -1
	}
}
