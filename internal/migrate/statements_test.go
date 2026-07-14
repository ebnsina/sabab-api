package migrate

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitStatements(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "single statement without trailing semicolon",
			sql:  "SELECT 1",
			want: []string{"SELECT 1"},
		},
		{
			name: "two statements",
			sql:  "CREATE TABLE a (x UInt8) ENGINE = Memory; CREATE TABLE b (y UInt8) ENGINE = Memory;",
			want: []string{
				"CREATE TABLE a (x UInt8) ENGINE = Memory",
				"CREATE TABLE b (y UInt8) ENGINE = Memory",
			},
		},
		{
			name: "trailing semicolon produces no empty statement",
			sql:  "SELECT 1;",
			want: []string{"SELECT 1"},
		},
		{
			name: "blank input produces nothing",
			sql:  "   \n\n  ",
			want: nil,
		},
		{
			// The whole reason this is not strings.Split: a semicolon inside a
			// string literal must not end the statement.
			name: "semicolon inside a single-quoted string is not a separator",
			sql:  "INSERT INTO t VALUES ('a;b'); SELECT 1",
			want: []string{"INSERT INTO t VALUES ('a;b')", "SELECT 1"},
		},
		{
			name: "escaped quote inside a string does not end the string",
			sql:  `INSERT INTO t VALUES ('it\'s; fine'); SELECT 2`,
			want: []string{`INSERT INTO t VALUES ('it\'s; fine')`, "SELECT 2"},
		},
		{
			name: "semicolon inside a backtick identifier is not a separator",
			sql:  "SELECT `weird;col` FROM t; SELECT 3",
			want: []string{"SELECT `weird;col` FROM t", "SELECT 3"},
		},
		{
			name: "line comment is stripped, including any semicolon in it",
			sql:  "SELECT 1 -- trailing; comment\n; SELECT 2",
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "hash line comment is stripped",
			sql:  "SELECT 1 # note; here\n; SELECT 2",
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "block comment is stripped",
			sql:  "SELECT /* a; b */ 1; SELECT 2",
			want: []string{"SELECT  1", "SELECT 2"},
		},
		{
			// A comment-only file must not produce a statement, or Apply would
			// send an empty query to ClickHouse.
			name: "comment-only input produces nothing",
			sql:  "-- just a note\n/* and another */\n",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.sql)
			if !slices.Equal(got, tc.want) {
				t.Errorf("splitStatements(%q)\n got: %#v\nwant: %#v", tc.sql, got, tc.want)
			}
		})
	}
}

// The real migration must survive the splitter: it contains comments with
// apostrophes, a Map type, and a materialized view.
func TestSplitStatementsOnRealMigration(t *testing.T) {
	const sql = `
-- ClickHouse's event plane; don't let this apostrophe break anything.
CREATE TABLE IF NOT EXISTS errors
(
    project_id UInt64 CODEC(Delta, ZSTD(1)),
    level      Enum8('debug' = 1, 'error' = 4),
    tags       Map(LowCardinality(String), String)
)
ENGINE = MergeTree
ORDER BY (project_id);

CREATE MATERIALIZED VIEW IF NOT EXISTS mv TO target AS
SELECT project_id FROM errors GROUP BY project_id;
`
	got := splitStatements(sql)
	if len(got) != 2 {
		t.Fatalf("want 2 statements, got %d: %#v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "CREATE TABLE IF NOT EXISTS errors") {
		t.Errorf("first statement is not the table: %q", got[0])
	}
	if !strings.Contains(got[0], "Enum8('debug' = 1, 'error' = 4)") {
		t.Errorf("enum literal was mangled: %q", got[0])
	}
	if !strings.HasPrefix(got[1], "CREATE MATERIALIZED VIEW") {
		t.Errorf("second statement is not the MV: %q", got[1])
	}
}
