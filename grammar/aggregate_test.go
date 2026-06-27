package grammar

import "testing"

func TestCompileSelect_WithCount(t *testing.T) {
	q := CompiledQuery{
		Table: "posts",
		Aggregates: []AggregateSelect{{
			Func:  "COUNT",
			Table: "comments",
			On:    []WhereClause{corr("comments.post_id", "posts.id")},
			Alias: "comments_count",
		}},
	}
	sql, args := Postgres{}.CompileSelect(q)
	want := `SELECT *, (SELECT COUNT(*) FROM "comments" WHERE "comments"."post_id" = "posts"."id") AS "comments_count" FROM "posts"`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("unconstrained count should have no args, got %v", args)
	}
}

func TestCompileSelect_WithSum(t *testing.T) {
	q := CompiledQuery{
		Table:   "posts",
		Columns: []string{"id"},
		Aggregates: []AggregateSelect{{
			Func:   "SUM",
			Column: "comments.votes",
			Table:  "comments",
			On:     []WhereClause{corr("comments.post_id", "posts.id")},
			Alias:  "comments_sum_votes",
		}},
	}
	sql, _ := Postgres{}.CompileSelect(q)
	want := `SELECT "id", (SELECT SUM("comments"."votes") FROM "comments" ` +
		`WHERE "comments"."post_id" = "posts"."id") AS "comments_sum_votes" FROM "posts"`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileSelect_WithExists_CaseForm(t *testing.T) {
	q := CompiledQuery{
		Table: "posts",
		Aggregates: []AggregateSelect{{
			Func:  "EXISTS",
			Table: "comments",
			On:    []WhereClause{corr("comments.post_id", "posts.id")},
			Alias: "comments_exists",
		}},
	}
	sql, _ := MSSQL{}.CompileSelect(q)
	want := `SELECT *, CASE WHEN EXISTS (SELECT 1 FROM [comments] ` +
		`WHERE [comments].[post_id] = [posts].[id]) THEN 1 ELSE 0 END AS [comments_exists] FROM [posts]`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
}

func TestCompileSelect_AggregateBindOrdering(t *testing.T) {
	// A constrained aggregate ($1 inside the subquery) precedes the WHERE bind
	// ($2): the select-column binds must number before the WHERE binds.
	q := CompiledQuery{
		Table:   "posts",
		Columns: []string{"id"},
		Aggregates: []AggregateSelect{{
			Func:   "COUNT",
			Table:  "comments",
			On:     []WhereClause{corr("comments.post_id", "posts.id")},
			Wheres: []WhereClause{{Kind: WhereBasic, Boolean: "AND", Column: "approved", Op: "=", Value: false}},
			Alias:  "pending_count",
		}},
		Wheres: []WhereClause{{Kind: WhereBasic, Column: "published", Op: "=", Value: true}},
	}
	sql, args := Postgres{}.CompileSelect(q)
	want := `SELECT "id", (SELECT COUNT(*) FROM "comments" ` +
		`WHERE "comments"."post_id" = "posts"."id" AND "approved" = $1) AS "pending_count" ` +
		`FROM "posts" WHERE "published" = $2`
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 2 || args[0] != false || args[1] != true {
		t.Errorf("args = %v, want [false true]", args)
	}
}

func TestCompileSelect_WithCount_ManyToMany(t *testing.T) {
	// roles counted through the role_user pivot via an IN-subquery.
	q := CompiledQuery{
		Table: "users",
		Aggregates: []AggregateSelect{{
			Func:  "COUNT",
			Table: "roles",
			On: []WhereClause{{
				Kind:   WhereInSub,
				Column: "roles.id",
				Sub: &Subselect{
					Column: "role_user.role_id",
					Table:  "role_user",
					Wheres: []WhereClause{corr("role_user.user_id", "users.id")},
				},
			}},
			Alias: "roles_count",
		}},
	}
	sql, _ := MySQL{}.CompileSelect(q)
	want := "SELECT *, (SELECT COUNT(*) FROM `roles` WHERE `roles`.`id` IN " +
		"(SELECT `role_user`.`role_id` FROM `role_user` WHERE `role_user`.`user_id` = `users`.`id`)) " +
		"AS `roles_count` FROM `users`"
	if sql != want {
		t.Errorf("sql:\n got: %s\nwant: %s", sql, want)
	}
}
