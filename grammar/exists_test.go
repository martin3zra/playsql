package grammar

import "testing"

func corr(inner, outer string) WhereClause {
	return WhereClause{Kind: WhereColumn, Column: inner, Op: "=", Second: outer}
}

func TestCompileSelect_PlainExists(t *testing.T) {
	q := CompiledQuery{
		Table: "posts",
		Wheres: []WhereClause{{
			Kind: WhereExists,
			Exists: &RelationExists{
				Table: "comments",
				On:    []WhereClause{corr("comments.post_id", "posts.id")},
			},
		}},
	}
	sql, args := Postgres{}.CompileSelect(q)
	want := `SELECT * FROM "posts" WHERE EXISTS (SELECT 1 FROM "comments" WHERE "comments"."post_id" = "posts"."id")`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("plain exists should have no args, got %v", args)
	}
}

func TestCompileSelect_NotExists(t *testing.T) {
	q := CompiledQuery{
		Table: "posts",
		Wheres: []WhereClause{{
			Kind: WhereExists,
			Exists: &RelationExists{
				Not:   true,
				Table: "comments",
				On:    []WhereClause{corr("comments.post_id", "posts.id")},
			},
		}},
	}
	sql, _ := Postgres{}.CompileSelect(q)
	want := `SELECT * FROM "posts" WHERE NOT EXISTS (SELECT 1 FROM "comments" WHERE "comments"."post_id" = "posts"."id")`
	if sql != want {
		t.Errorf("got: %s", sql)
	}
}

func TestCompileSelect_ExistsCountContiguousPlaceholders(t *testing.T) {
	// An outer bound predicate ($1), a closure predicate inside the subquery
	// ($2), and the COUNT comparison value ($3) must number contiguously.
	q := CompiledQuery{
		Table: "posts",
		Wheres: []WhereClause{
			{Kind: WhereBasic, Column: "published", Op: "=", Value: true},
			{Kind: WhereExists, Boolean: "AND", Exists: &RelationExists{
				Table:    "comments",
				On:       []WhereClause{corr("comments.post_id", "posts.id")},
				Wheres:   []WhereClause{{Kind: WhereBasic, Boolean: "AND", Column: "content", Op: "like", Value: "code%"}},
				CountOp:  ">=",
				CountVal: 3,
			}},
		},
	}
	sql, args := Postgres{}.CompileSelect(q)
	want := `SELECT * FROM "posts" WHERE "published" = $1 AND ` +
		`(SELECT COUNT(*) FROM "comments" WHERE "comments"."post_id" = "posts"."id" AND "content" like $2) >= $3`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 3 || args[0] != true || args[1] != "code%" || args[2] != 3 {
		t.Errorf("args = %v, want [true code%% 3]", args)
	}
}

func TestCompileSelect_BelongsToManyExists(t *testing.T) {
	// authors that have any book, through the author_book pivot.
	inner := &RelationExists{
		Table: "books",
		On:    []WhereClause{corr("books.id", "author_book.book_id")},
	}
	q := CompiledQuery{
		Table: "authors",
		Wheres: []WhereClause{{
			Kind: WhereExists,
			Exists: &RelationExists{
				Table:  "author_book",
				On:     []WhereClause{corr("author_book.author_id", "authors.id")},
				Wheres: []WhereClause{{Kind: WhereExists, Boolean: "AND", Exists: inner}},
			},
		}},
	}
	sql, _ := Postgres{}.CompileSelect(q)
	want := `SELECT * FROM "authors" WHERE EXISTS (SELECT 1 FROM "author_book" ` +
		`WHERE "author_book"."author_id" = "authors"."id" AND EXISTS (SELECT 1 FROM "books" ` +
		`WHERE "books"."id" = "author_book"."book_id"))`
	if sql != want {
		t.Errorf("m2m:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileSelect_ThroughExists(t *testing.T) {
	// regions that have articles, through writers (nested EXISTS).
	inner := &RelationExists{
		Table: "articles",
		On:    []WhereClause{corr("articles.writer_id", "writers.id")},
	}
	q := CompiledQuery{
		Table: "regions",
		Wheres: []WhereClause{{
			Kind: WhereExists,
			Exists: &RelationExists{
				Table:  "writers",
				On:     []WhereClause{corr("writers.region_id", "regions.id")},
				Wheres: []WhereClause{{Kind: WhereExists, Boolean: "AND", Exists: inner}},
			},
		}},
	}
	sql, _ := Postgres{}.CompileSelect(q)
	want := `SELECT * FROM "regions" WHERE EXISTS (SELECT 1 FROM "writers" ` +
		`WHERE "writers"."region_id" = "regions"."id" AND EXISTS (SELECT 1 FROM "articles" ` +
		`WHERE "articles"."writer_id" = "writers"."id"))`
	if sql != want {
		t.Errorf("through:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileSelect_ThroughCountInSubquery(t *testing.T) {
	// regions with >= 3 articles — far rows counted exactly via IN-subquery.
	q := CompiledQuery{
		Table: "regions",
		Wheres: []WhereClause{{
			Kind: WhereExists,
			Exists: &RelationExists{
				Table: "articles",
				On: []WhereClause{{
					Kind:   WhereInSub,
					Column: "articles.writer_id",
					Sub: &Subselect{
						Column: "writers.id",
						Table:  "writers",
						Wheres: []WhereClause{corr("writers.region_id", "regions.id")},
					},
				}},
				CountOp:  ">=",
				CountVal: 3,
			},
		}},
	}
	sql, args := Postgres{}.CompileSelect(q)
	want := `SELECT * FROM "regions" WHERE (SELECT COUNT(*) FROM "articles" ` +
		`WHERE "articles"."writer_id" IN (SELECT "writers"."id" FROM "writers" ` +
		`WHERE "writers"."region_id" = "regions"."id")) >= $1`
	if sql != want {
		t.Errorf("through count:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 1 || args[0] != 3 {
		t.Errorf("args = %v, want [3]", args)
	}
}

func TestCompileSelect_NestedExists(t *testing.T) {
	// posts that have a comment that has an image (dot-notation nesting).
	q := CompiledQuery{
		Table: "posts",
		Wheres: []WhereClause{{
			Kind: WhereExists,
			Exists: &RelationExists{
				Table: "comments",
				On:    []WhereClause{corr("comments.post_id", "posts.id")},
				Wheres: []WhereClause{{
					Kind: WhereExists, Boolean: "AND",
					Exists: &RelationExists{
						Table: "images",
						On:    []WhereClause{corr("images.comment_id", "comments.id")},
					},
				}},
			},
		}},
	}
	sql, _ := MySQL{}.CompileSelect(q)
	want := "SELECT * FROM `posts` WHERE EXISTS (SELECT 1 FROM `comments` " +
		"WHERE `comments`.`post_id` = `posts`.`id` AND EXISTS (SELECT 1 FROM `images` " +
		"WHERE `images`.`comment_id` = `comments`.`id`))"
	if sql != want {
		t.Errorf("nested:\n got: %s\nwant: %s", sql, want)
	}
}
