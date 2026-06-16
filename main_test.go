package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runScript(t *testing.T, dbPath string, commands []string) string {
	t.Helper()

	return runScriptWithExpectedCode(t, dbPath, commands, ExitSuccess)
}

func runScriptWithExpectedCode(t *testing.T, dbPath string, commands []string, wantCode ExitCode) string {
	t.Helper()

	table, err := dbOpen(dbPath)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	var out bytes.Buffer
	input := strings.Join(append(commands, ""), "\n")
	code := run(strings.NewReader(input), &out, table)
	if code != wantCode {
		t.Fatalf("expected exit code %d, got %d", wantCode, code)
	}

	return out.String()
}

func runTempScript(t *testing.T, commands []string) string {
	t.Helper()

	return runScript(t, filepath.Join(t.TempDir(), "test.db"), commands)
}

func runTempSQLScript(t *testing.T, script string) string {
	t.Helper()

	table, err := dbOpen(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	var out bytes.Buffer
	code := runSQLScript(strings.NewReader(script), &out, table)
	if code != ExitSuccess {
		t.Fatalf("expected exit code %d, got %d", ExitSuccess, code)
	}

	return out.String()
}

func expectedTableLines(prefix string, columns []string, rows ...[]string) []string {
	widths := make([]int, len(columns))
	for i, column := range columns {
		widths[i] = len(column)
	}
	for _, row := range rows {
		for i, value := range row {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}

	lines := []string{prefix + expectedTableSeparator(widths)}
	lines = append(lines, expectedTableValues(columns, widths))
	lines = append(lines, expectedTableSeparator(widths))
	for _, row := range rows {
		lines = append(lines, expectedTableValues(row, widths))
		lines = append(lines, expectedTableSeparator(widths))
	}

	return lines
}

func expectedTableSeparator(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("-", width+2))
	}

	return "+" + strings.Join(parts, "+") + "+"
}

func expectedTableValues(values []string, widths []int) string {
	parts := make([]string, 0, len(values))
	for i, value := range values {
		parts = append(parts, fmt.Sprintf(" %-*s ", widths[i], value))
	}

	return "|" + strings.Join(parts, "|") + "|"
}

func defaultRow(id uint32, username string, email string) Row {
	return Row{
		Values: map[string]Value{
			idColumnName: {
				StorageClass: StorageInteger,
				Integer:      int64(id),
			},
			usernameColumnName: {
				StorageClass: StorageText,
				Text:         username,
			},
			emailColumnName: {
				StorageClass: StorageText,
				Text:         email,
			},
		},
	}
}

func maxSizeRow(id uint32) Row {
	return defaultRow(id, strings.Repeat("u", columnUsernameSize), strings.Repeat("e", columnEmailSize))
}

func maxSizeInsertCommand(id uint32) string {
	return fmt.Sprintf("insert %d %s %s", id, strings.Repeat("u", columnUsernameSize), strings.Repeat("e", columnEmailSize))
}

func TestRunExitsOnExitCommand(t *testing.T) {
	// .exit が入力されたら、未認識コマンドを出さずに終了する。
	got := runTempScript(t, []string{".exit"})

	want := "db > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsUnrecognizedMetaCommand(t *testing.T) {
	// . で始まる未知の入力は、未対応のメタコマンドとして表示する。
	got := runTempScript(t, []string{".unknown", ".exit"})

	want := "db > Unrecognized command '.unknown'.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsUnrecognizedStatement(t *testing.T) {
	// . で始まらない未知の入力は、未対応のステートメントとして表示する。
	got := runTempScript(t, []string{"hello", ".exit"})

	want := "db > Unrecognized keyword at start of 'hello'.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesInsertStatement(t *testing.T) {
	// insertで始まる入力はRowとして保存する。
	got := runTempScript(t, []string{"insert 1 user user@example.com", ".exit"})

	want := "db > Executed.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesUpdateWithWhere(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 165.2",
		"insert 2 Bob 172.4",
		"insert 3 Carol 158.9",
		"UPDATE people SET name = 'Bobby', height = 180.5 WHERE id = 2;",
		"UPDATE people SET height = 160 WHERE name = 'Carol' OR (id = 1 AND height < 170);",
		"SELECT id, name, height FROM people ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name", "height"}, []string{"1", "Alice", "160"}, []string{"2", "Bobby", "180.5"}, []string{"3", "Carol", "160"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesUpdateWithoutWhere(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"UPDATE users SET email = 'updated@example.com';",
		"SELECT id, username, email FROM users ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "alice", "updated@example.com"}, []string{"2", "bob", "updated@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesUpdatePrimaryKey(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 3 carol carol@example.com",
		"UPDATE users SET id = 2 WHERE username = 'carol';",
		"SELECT id, username FROM users ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username"}, []string{"1", "alice"}, []string{"2", "carol"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunRejectsUpdateDuplicatePrimaryKey(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"UPDATE users SET id = 1 WHERE username = 'bob';",
		"SELECT id, username FROM users ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Error: Duplicate key.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username"}, []string{"1", "alice"}, []string{"2", "bob"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunRejectsUpdateConstraintViolations(t *testing.T) {
	got := runTempScript(t, []string{
		"create table accounts (id integer primary key, username text unique, email text not null)",
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"UPDATE accounts SET username = alice WHERE id = 2;",
		"UPDATE accounts SET email = null WHERE id = 2;",
		"SELECT id, username, email FROM accounts ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Error: Constraint violation.",
		"db > Error: Constraint violation.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "alice", "alice@example.com"}, []string{"2", "bob", "bob@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesDeleteWithWhere(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 165.2",
		"insert 2 Bob 172.4",
		"insert 3 Carol 158.9",
		"insert 4 Dave 180.1",
		"DELETE FROM people WHERE height < 170 OR name = 'Dave';",
		"SELECT id, name FROM people ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name"}, []string{"2", "Bob"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesDeleteWithoutWhere(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"DELETE FROM users;",
		"select",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesDeleteFromNamedTable(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, department_id integer)",
		"create table departments (id integer primary key, name text)",
		"insert into people values 1 Alice 10",
		"insert into people values 2 Bob 20",
		"insert into departments values 10 Engineering",
		"DELETE FROM people WHERE department_id = 20;",
		"SELECT id, name FROM people ORDER BY id ASC;",
		"SELECT id, name FROM departments ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name"}, []string{"1", "Alice"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name"}, []string{"10", "Engineering"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunSQLScriptExecutesSemicolonSeparatedStatements(t *testing.T) {
	got := runTempSQLScript(t, `
-- setup
create table people (
  id integer primary key,
  name text,
  note text
);
insert 1 'Alice; A' 'keeps -- text';
insert 2 Bob null;
SELECT name, note FROM people;
`)

	wantLines := []string{
		"Executed.",
		"Executed.",
		"Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("", []string{"name", "note"}, []string{"Alice; A", "keeps -- text"}, []string{"Bob", "NULL"})...)
	wantLines = append(wantLines, "Executed.")
	want := strings.Join(wantLines, "\n") + "\n"
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunSQLScriptPrintsSyntaxErrorForUnterminatedString(t *testing.T) {
	got := runTempSQLScript(t, "insert 1 'alice;")

	want := "Syntax error. Could not parse statement.\n"
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesInsertStatementWithQuotedText(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 'Alice Smith' 'alice smith@example.com'",
		"insert 2 'Bob''s note' 'bob@example.com'",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "Alice Smith", "alice smith@example.com"}, []string{"2", "Bob's note", "bob@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestPrepareInsertParsesBackslashEscapesInQuotedText(t *testing.T) {
	var statement Statement
	result := prepareStatement("insert 1 'Alice\\nSmith' 'alice\\\\smith@example.com'", &statement, DefaultTableSchema())

	if result != PrepareSuccess {
		t.Fatalf("expected prepare success, got %d", result)
	}
	if got := statement.RowToInsert.Values[usernameColumnName].Text; got != "Alice\nSmith" {
		t.Fatalf("expected escaped newline, got %q", got)
	}
	if got := statement.RowToInsert.Values[emailColumnName].Text; got != "alice\\smith@example.com" {
		t.Fatalf("expected escaped backslash, got %q", got)
	}
}

func TestPrepareInsertRejectsUnterminatedQuotedText(t *testing.T) {
	var statement Statement
	result := prepareStatement("insert 1 'Alice Smith alice@example.com", &statement, DefaultTableSchema())

	if result != PrepareSyntaxError {
		t.Fatalf("expected syntax error, got %d", result)
	}
}

func TestRunExecutesSelectStatement(t *testing.T) {
	// selectは保存済みの全Rowを表示する。
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "alice", "alice@example.com"}, []string{"2", "bob", "bob@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectAllFromTable(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"SELECT * FROM users;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "alice", "alice@example.com"}, []string{"2", "bob", "bob@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectColumnsFromTable(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"SELECT username, email FROM users;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"username", "email"}, []string{"alice", "alice@example.com"}, []string{"bob", "bob@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereByPrimaryKey(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"insert 3 carol carol@example.com",
		"SELECT * FROM users WHERE id = 2;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"2", "bob", "bob@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereByTextColumn(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 'Alice Smith' alice.smith@example.com",
		"SELECT id, email FROM users WHERE username = 'Alice Smith';",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "email"}, []string{"2", "alice.smith@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereIsNull(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, nickname text, code integer unique)",
		"insert 1 null null",
		"insert 2 bob 20",
		"SELECT id FROM people WHERE nickname IS NULL;",
		"SELECT id FROM people WHERE code IS NOT NULL;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id"}, []string{"1"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id"}, []string{"2"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereComparisonOperators(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 165.2",
		"insert 2 Bob 172.4",
		"insert 3 Carol 158.9",
		"SELECT name FROM people WHERE height >= 165.2;",
		"SELECT name FROM people WHERE id <> 2;",
		"SELECT id FROM people WHERE name > 'Alice';",
		"SELECT id FROM people WHERE id!=1;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"}, []string{"Bob"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"}, []string{"Carol"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id"}, []string{"2"}, []string{"3"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id"}, []string{"2"}, []string{"3"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereAndConditions(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real, note text)",
		"insert 1 Alice 165.2 'research and design'",
		"insert 2 Bob 172.4 ops",
		"insert 3 Carol 158.9 'research and docs'",
		"SELECT name FROM people WHERE height >= 160 AND id < 3;",
		"SELECT id FROM people WHERE note = 'research and design' AND name = Alice;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"}, []string{"Bob"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id"}, []string{"1"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereOrConditions(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real, note text)",
		"insert 1 Alice 165.2 design",
		"insert 2 Bob 172.4 ops",
		"insert 3 Carol 158.9 'research or docs'",
		"SELECT name FROM people WHERE id = 1 OR height < 160;",
		"SELECT name FROM people WHERE id = 1 OR id = 2 AND height < 170;",
		"SELECT id FROM people WHERE note = 'research or docs' OR name = Bob;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"}, []string{"Carol"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id"}, []string{"2"}, []string{"3"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereParenthesizedConditions(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real, note text)",
		"insert 1 Alice 165.2 design",
		"insert 2 Bob 172.4 ops",
		"insert 3 Carol 158.9 docs",
		"SELECT name FROM people WHERE (id = 1 OR id = 2) AND height < 170;",
		"SELECT name FROM people WHERE id = 1 OR (id = 2 AND height < 170);",
		"SELECT name FROM people WHERE ((id = 1 OR id = 3) AND (height < 170 OR name = Bob));",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"}, []string{"Carol"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectOrderByAscAndDesc(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real, nickname text)",
		"insert 1 Alice 165.2 null",
		"insert 2 Bob 172.4 Bobby",
		"insert 3 Carol 158.9 null",
		"SELECT name, height FROM people ORDER BY height ASC;",
		"SELECT name FROM people ORDER BY name DESC;",
		"SELECT id, nickname FROM people ORDER BY nickname ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name", "height"}, []string{"Carol", "158.9"}, []string{"Alice", "165.2"}, []string{"Bob", "172.4"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Carol"}, []string{"Bob"}, []string{"Alice"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "nickname"}, []string{"1", "NULL"}, []string{"3", "NULL"}, []string{"2", "Bobby"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectOrderByMultipleColumns(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real, team text)",
		"insert 1 Alice 165.2 Red",
		"insert 2 Bob 172.4 Blue",
		"insert 3 Carol 165.2 Blue",
		"insert 4 Dave 172.4 Red",
		"SELECT name, height, team FROM people ORDER BY height ASC, team DESC, name ASC;",
		"SELECT name FROM people ORDER BY height DESC, name DESC LIMIT 3;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name", "height", "team"}, []string{"Alice", "165.2", "Red"}, []string{"Carol", "165.2", "Blue"}, []string{"Dave", "172.4", "Red"}, []string{"Bob", "172.4", "Blue"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Dave"}, []string{"Bob"}, []string{"Carol"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWhereOrderBy(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 165.2",
		"insert 2 Bob 172.4",
		"insert 3 Carol 158.9",
		"SELECT name FROM people WHERE id = 1 OR height < 170 ORDER BY height DESC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Alice"}, []string{"Carol"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectLimit(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"insert 3 carol carol@example.com",
		"SELECT username FROM users LIMIT 2;",
		"SELECT username FROM users WHERE id >= 2 LIMIT 1;",
		"SELECT username FROM users LIMIT 0;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"username"}, []string{"alice"}, []string{"bob"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"username"}, []string{"bob"})...)
	wantLines = append(wantLines, "Executed.", "db > Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectLimitOffset(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"insert 3 carol carol@example.com",
		"insert 4 dave dave@example.com",
		"SELECT username FROM users ORDER BY id ASC LIMIT 2 OFFSET 1;",
		"SELECT username FROM users ORDER BY id ASC LIMIT 2 OFFSET 3;",
		"SELECT username FROM users ORDER BY id ASC LIMIT 2 OFFSET 4;",
		"SELECT username FROM users ORDER BY id ASC LIMIT 0 OFFSET 1;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"username"}, []string{"bob"}, []string{"carol"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"username"}, []string{"dave"})...)
	wantLines = append(wantLines, "Executed.", "db > Executed.", "db > Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectOrderByLimit(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 165.2",
		"insert 2 Bob 172.4",
		"insert 3 Carol 158.9",
		"SELECT name FROM people ORDER BY height DESC LIMIT 2;",
		"SELECT name FROM people WHERE id = 1 OR height < 170 ORDER BY height ASC LIMIT 1;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Bob"}, []string{"Alice"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"name"}, []string{"Carol"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectArithmeticExpressions(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real, weight integer)",
		"insert 1 Alice 152.5 45",
		"insert 2 Bob 181 72",
		"SELECT name, height + 10, weight * 2, (height + weight) / 2 FROM people ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"name", "height + 10", "weight * 2", "(height + weight) / 2"},
		[]string{"Alice", "162.5", "90", "98.75"},
		[]string{"Bob", "191", "144", "126.5"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectDistinct(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 152.5",
		"insert 2 Bob 181",
		"insert 3 Alice 152.5",
		"SELECT DISTINCT name, height FROM people ORDER BY id ASC;",
		"SELECT DISTINCT name FROM people ORDER BY id ASC LIMIT 1;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"name", "height"},
		[]string{"Alice", "152.5"},
		[]string{"Bob", "181"},
	)...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"name"},
		[]string{"Alice"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectDistinctFromJoin(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 152.5",
		"insert 2 Bob 181",
		"insert 3 Carol 158.9",
		"SELECT DISTINCT b.name AS taller FROM people a JOIN people b ON a.height < b.height ORDER BY b.id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"taller"},
		[]string{"Bob"},
		[]string{"Carol"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectArithmeticWithNullAndDivisionByZero(t *testing.T) {
	got := runTempScript(t, []string{
		"create table scores (id integer primary key, points integer, bonus real)",
		"insert 1 10 2.5",
		"insert 2 null 1.5",
		"SELECT id, points + bonus, points / 0 FROM scores ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"id", "points + bonus", "points / 0"},
		[]string{"1", "12.5", "NULL"},
		[]string{"2", "NULL", "NULL"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidSelectArithmeticExpression(t *testing.T) {
	got := runTempScript(t, []string{
		"SELECT username + 1 FROM users;",
		"SELECT id + FROM users;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectFromDual(t *testing.T) {
	got := runTempScript(t, []string{
		"SELECT 1 + 2, (10 - 4) / 3 FROM dual;",
		"SELECT * FROM dual;",
		"SELECT dummy FROM dual WHERE dummy = 'X';",
		"SELECT count(*) FROM dual;",
		".exit",
	})

	wantLines := []string{}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"1 + 2", "(10 - 4) / 3"},
		[]string{"3", "2"},
	)...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"dummy"},
		[]string{"X"},
	)...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"dummy"},
		[]string{"X"},
	)...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"count(*)"},
		[]string{"1"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidSelectFromDual(t *testing.T) {
	got := runTempScript(t, []string{
		"SELECT username FROM dual;",
		"SELECT * FROM dual GROUP BY dummy;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesAggregateFunctions(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, region text, amount integer, score real)",
		"insert 1 East 10 1.5",
		"insert 2 East 20 2.5",
		"insert 3 West null 4",
		"SELECT count(*), count(amount), sum(amount), avg(score), min(region), max(amount), sum(amount) / count(amount) FROM sales;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"count(*)", "count(amount)", "sum(amount)", "avg(score)", "min(region)", "max(amount)", "sum(amount) / count(amount)"},
		[]string{"3", "2", "30", "2.6666666666666665", "East", "20", "15"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesGroupByAggregates(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, region text, amount integer, score real)",
		"insert 1 East 10 1.5",
		"insert 2 East 20 2.5",
		"insert 3 West 7 null",
		"SELECT region, count(*), sum(amount), avg(score), min(amount), max(amount) FROM sales GROUP BY region;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"region", "count(*)", "sum(amount)", "avg(score)", "min(amount)", "max(amount)"},
		[]string{"East", "2", "30", "2", "10", "20"},
		[]string{"West", "1", "7", "NULL", "7", "7"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesGroupByHaving(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, region text, amount integer, score real)",
		"insert 1 East 10 1.5",
		"insert 2 East 20 2.5",
		"insert 3 West 7 null",
		"insert 4 North 40 4",
		"SELECT region, count(*), sum(amount), avg(score) FROM sales GROUP BY region HAVING count(*) > 1 OR sum(amount) >= 40;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"region", "count(*)", "sum(amount)", "avg(score)"},
		[]string{"East", "2", "30", "2"},
		[]string{"North", "1", "40", "4"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesHavingWithoutGroupBy(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, amount integer)",
		"insert 1 10",
		"insert 2 20",
		"SELECT count(*), sum(amount) FROM sales HAVING sum(amount) > 20 AND avg(amount) = 15;",
		"SELECT count(*) FROM sales HAVING sum(amount) < 20;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"count(*)", "sum(amount)"},
		[]string{"2", "30"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesHavingIsNull(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, region text, score real)",
		"insert 1 East 1.5",
		"insert 2 West null",
		"SELECT region, avg(score) FROM sales GROUP BY region HAVING avg(score) IS NULL;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"region", "avg(score)"},
		[]string{"West", "NULL"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesDistinctAfterGroupBy(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, region text, amount integer)",
		"insert 1 East 10",
		"insert 2 West 10",
		"insert 3 North 20",
		"SELECT DISTINCT sum(amount) AS total FROM sales GROUP BY region;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"total"},
		[]string{"10"},
		[]string{"20"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesAggregateOnEmptyTable(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, amount integer)",
		"SELECT count(*), sum(amount), avg(amount), min(amount), max(amount) FROM sales;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"count(*)", "sum(amount)", "avg(amount)", "min(amount)", "max(amount)"},
		[]string{"0", "NULL", "NULL", "NULL", "NULL"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidAggregateSelect(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, region text, amount integer)",
		"SELECT region, count(*) FROM sales;",
		"SELECT region, count(*) FROM sales GROUP BY missing;",
		"SELECT sum(region) FROM sales;",
		"SELECT count(*) + region FROM sales GROUP BY region;",
		"SELECT count(*) FROM sales HAVING region = 'East';",
		"SELECT * FROM sales HAVING count(*) > 0;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectColumnsFromCustomTable(t *testing.T) {
	got := runTempScript(t, []string{
		"create table tbl1 (id integer primary key, column1 text, column2 real)",
		"insert 1 Alice 165.2",
		"SELECT column1, column2 FROM tbl1;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"column1", "column2"}, []string{"Alice", "165.2"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectWithTableAndColumnAliases(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 152.5",
		"insert 2 Bob 181",
		"SELECT p.name AS display_name, p.height + 1 adjusted_height FROM people AS p WHERE p.id = 1;",
		"SELECT person.name nickname FROM people person WHERE person.height > 170;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"display_name", "adjusted_height"},
		[]string{"Alice", "153.5"},
	)...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"nickname"},
		[]string{"Bob"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesGroupByWithTableAlias(t *testing.T) {
	got := runTempScript(t, []string{
		"create table sales (id integer primary key, region text, amount integer)",
		"insert 1 East 10",
		"insert 2 East 20",
		"insert 3 West 7",
		"SELECT s.region AS area, count(*) total, sum(s.amount) amount_total FROM sales s GROUP BY s.region HAVING sum(s.amount) > 10;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"area", "total", "amount_total"},
		[]string{"East", "2", "30"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesDualWithTableAlias(t *testing.T) {
	got := runTempScript(t, []string{
		"SELECT d.dummy AS marker FROM dual d;",
		".exit",
	})

	wantLines := expectedTableLines("db > ",
		[]string{"marker"},
		[]string{"X"},
	)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidAliases(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text)",
		"SELECT other.name FROM people p;",
		"SELECT p.name AS 1bad FROM people p;",
		"SELECT p.name FROM people AS;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesInnerJoinWithAliases(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, height real)",
		"insert 1 Alice 152.5",
		"insert 2 Bob 181",
		"insert 3 Carol 158.9",
		"SELECT a.name AS shorter, b.name AS taller FROM people a JOIN people b ON a.height < b.height WHERE a.id = 1 ORDER BY b.id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"shorter", "taller"},
		[]string{"Alice", "Bob"},
		[]string{"Alice", "Carol"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesInnerJoinWithGroupBy(t *testing.T) {
	got := runTempScript(t, []string{
		"create table employees (id integer primary key, name text, manager_id integer)",
		"insert 1 Alice null",
		"insert 2 Bob 1",
		"insert 3 Carol 1",
		"SELECT m.name AS manager, count(*) AS reports FROM employees e JOIN employees m ON e.manager_id = m.id GROUP BY m.name HAVING count(*) >= 2;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"manager", "reports"},
		[]string{"Alice", "2"},
	)...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunManagesMultipleTablesAndJoinsThem(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text, department_id integer)",
		"create table departments (id integer primary key, name text)",
		"insert into people values 1 Alice 10",
		"insert into people values 2 Bob 20",
		"insert into departments values 10 Engineering",
		"insert into departments values 20 Sales",
		"SELECT p.name AS person, d.name AS department FROM people p JOIN departments d ON p.department_id = d.id ORDER BY p.id ASC;",
		".schema",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ",
		[]string{"person", "department"},
		[]string{"Alice", "Engineering"},
		[]string{"Bob", "Sales"},
	)...)
	wantLines = append(wantLines,
		"Executed.",
		"db > create table departments (id integer primary key, name text)",
		"create table people (id integer primary key, name text, department_id integer)",
		"db > ",
	)
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPersistsMultipleTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	runScript(t, dbPath, []string{
		"create table people (id integer primary key, name text, department_id integer)",
		"create table departments (id integer primary key, name text)",
		"insert into people values 1 Alice 10",
		"insert into departments values 10 Engineering",
		".exit",
	})

	got := runScript(t, dbPath, []string{
		"SELECT p.name, d.name FROM people p JOIN departments d ON p.department_id = d.id;",
		".exit",
	})

	wantLines := expectedTableLines("db > ",
		[]string{"p.name", "d.name"},
		[]string{"Alice", "Engineering"},
	)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPersistsDelete(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	runScript(t, dbPath, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"insert 3 carol carol@example.com",
		"DELETE FROM users WHERE id = 2;",
		".exit",
	})

	got := runScript(t, dbPath, []string{
		"SELECT id, username FROM users ORDER BY id ASC;",
		".exit",
	})

	wantLines := expectedTableLines("db > ",
		[]string{"id", "username"},
		[]string{"1", "alice"},
		[]string{"3", "carol"},
	)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidJoin(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text)",
		"SELECT name FROM people a JOIN people b ON a.id = b.id;",
		"SELECT a.name FROM people a JOIN missing b ON a.id = b.id;",
		"SELECT a.name FROM people a JOIN people a ON a.id = a.id;",
		"SELECT a.name FROM people a JOIN people b;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunStoresBlobFromFile(t *testing.T) {
	dir := t.TempDir()
	blobPath := filepath.Join(dir, "sample.bin")
	blob := bytes.Repeat([]byte{0xab}, int(leafNodeMaxPayloadSize)+100)
	if err := os.WriteFile(blobPath, blob, 0o600); err != nil {
		t.Fatalf("failed to write blob fixture: %v", err)
	}

	dbPath := filepath.Join(dir, "test.db")
	got := runScript(t, dbPath, []string{
		"create table files (id integer primary key, name text, data blob)",
		fmt.Sprintf("insert 1 sample @%s", blobPath),
		fmt.Sprintf("select id, name, data from files where data = @%s", blobPath),
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name", "data"}, []string{"1", "sample", fmt.Sprintf("BLOB(%d bytes)", len(blob))})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}

	got = runScript(t, dbPath, []string{
		"select name, data from files",
		".exit",
	})

	wantLines = expectedTableLines("db > ", []string{"name", "data"}, []string{"sample", fmt.Sprintf("BLOB(%d bytes)", len(blob))})
	wantLines = append(wantLines, "Executed.", "db > ")
	want = strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected persisted output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidSelectFrom(t *testing.T) {
	got := runTempScript(t, []string{
		"SELECT missing FROM users;",
		"SELECT * FROM missing;",
		"SELECT * FROM users WHERE missing = 1;",
		"SELECT * FROM users WHERE id = null;",
		"SELECT * FROM users WHERE id = 1 AND;",
		"SELECT * FROM users WHERE id = 1 OR;",
		"SELECT * FROM users WHERE (id = 1 OR id = 2;",
		"SELECT * FROM users WHERE id = 1);",
		"SELECT * FROM users ORDER BY missing;",
		"SELECT * FROM users ORDER BY id SIDEWAYS;",
		"SELECT * FROM users ORDER BY id ASC, missing DESC;",
		"SELECT * FROM users ORDER BY id ASC, username SIDEWAYS;",
		"SELECT * FROM users LIMIT -1;",
		"SELECT * FROM users LIMIT 1 2;",
		"SELECT * FROM users LIMIT 1 OFFSET;",
		"SELECT * FROM users LIMIT 1 OFFSET -1;",
		"SELECT * FROM users LIMIT 1 OFFSET 1 2;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidUpdate(t *testing.T) {
	got := runTempScript(t, []string{
		"UPDATE missing SET username = bob;",
		"UPDATE users username = bob;",
		"UPDATE users SET;",
		"UPDATE users SET missing = bob;",
		"UPDATE users SET username;",
		"UPDATE users SET username = bob, username = carol;",
		"UPDATE users SET username = bob WHERE missing = 1;",
		"UPDATE users SET id = -1 WHERE username = bob;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Primary key must be positive.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidDelete(t *testing.T) {
	got := runTempScript(t, []string{
		"DELETE users WHERE id = 1;",
		"DELETE FROM;",
		"DELETE FROM missing;",
		"DELETE FROM users WHERE missing = 1;",
		"DELETE FROM users WHERE id = 1 AND;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunRejectsInvalidAlterTable(t *testing.T) {
	got := runTempScript(t, []string{
		"ALTER TABLE missing ADD COLUMN age integer;",
		"ALTER TABLE users ADD age integer;",
		"ALTER TABLE users ADD COLUMN id integer;",
		"ALTER TABLE users ADD COLUMN;",
		"ALTER TABLE users ADD COLUMN code integer primary key;",
		"ALTER TABLE users ADD COLUMN code text not null;",
		".exit",
	})

	want := strings.Join([]string{
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Syntax error. Could not parse statement.",
		"db > Error: Constraint violation.",
		"db > Error: Constraint violation.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesStatementWithSurroundingSpaces(t *testing.T) {
	got := runTempScript(t, []string{
		" insert 1 alice alice@example.com ",
		" select ",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "alice", "alice@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectByIDStatement(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"insert 2 bob bob@example.com",
		"insert 3 carol carol@example.com",
		"select 2",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"2", "bob", "bob@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunExecutesSelectByMissingIDStatement(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"select 99",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSyntaxErrorForInvalidSelectByID(t *testing.T) {
	got := runTempScript(t, []string{"select abc", ".exit"})

	want := "db > Syntax error. Could not parse statement.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsErrorForNegativeSelectID(t *testing.T) {
	got := runTempScript(t, []string{"select -1", ".exit"})

	want := "db > Primary key must be positive.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsAllRowsInMultiLevelTree(t *testing.T) {
	commands := make([]string, 0, 17)
	for i := uint32(1); i <= 15; i++ {
		commands = append(commands, fmt.Sprintf("insert %d user%d person%d@example.com", i, i, i))
	}
	commands = append(commands, "select")
	commands = append(commands, ".exit")

	got := runScript(t, filepath.Join(t.TempDir(), "test.db"), commands)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	want := expectedTableLines("db > ", []string{"id", "username", "email"},
		[]string{"1", "user1", "person1@example.com"},
		[]string{"2", "user2", "person2@example.com"},
		[]string{"3", "user3", "person3@example.com"},
		[]string{"4", "user4", "person4@example.com"},
		[]string{"5", "user5", "person5@example.com"},
		[]string{"6", "user6", "person6@example.com"},
		[]string{"7", "user7", "person7@example.com"},
		[]string{"8", "user8", "person8@example.com"},
		[]string{"9", "user9", "person9@example.com"},
		[]string{"10", "user10", "person10@example.com"},
		[]string{"11", "user11", "person11@example.com"},
		[]string{"12", "user12", "person12@example.com"},
		[]string{"13", "user13", "person13@example.com"},
		[]string{"14", "user14", "person14@example.com"},
		[]string{"15", "user15", "person15@example.com"},
	)
	want = append(want, "Executed.", "db > ")

	start := len(lines) - len(want)
	if start < 0 {
		t.Fatalf("expected at least %d lines, got %d: %q", len(want), len(lines), got)
	}
	if got := lines[start:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected tail %q, got %q", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestRunPrintsSyntaxErrorForInvalidInsert(t *testing.T) {
	// insertに必要な id username email が揃っていない場合は構文エラーにする。
	got := runTempScript(t, []string{"insert 1 alice", ".exit"})

	want := "db > Syntax error. Could not parse statement.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunAllowsMaxLengthStrings(t *testing.T) {
	longUsername := strings.Repeat("a", columnUsernameSize)
	longEmail := strings.Repeat("a", columnEmailSize)

	// usernameとemailは定義上の最大長ぴったりなら保存できる。
	got := runTempScript(t, []string{
		"insert 1 " + longUsername + " " + longEmail,
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", longUsername, longEmail})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsErrorForTooLongStrings(t *testing.T) {
	longUsername := strings.Repeat("a", columnUsernameSize+1)
	longEmail := strings.Repeat("a", columnEmailSize+1)

	// usernameまたはemailが定義上の最大長を超えたら保存しない。
	got := runTempScript(t, []string{
		"insert 1 " + longUsername + " " + longEmail,
		"select",
		".exit",
	})

	want := strings.Join([]string{
		"db > String is too long.",
		"db > Executed.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}

	got = runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"UPDATE users SET username = " + longUsername + " WHERE id = 1;",
		"select username from users",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > String is too long.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"username"}, []string{"alice"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want = strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected update output %q, got %q", want, got)
	}
}

func TestRunPrintsErrorForNegativeID(t *testing.T) {
	// idが負数の場合は保存しない。
	got := runTempScript(t, []string{
		"insert -1 cstack foo@bar.com",
		"select",
		".exit",
	})

	want := strings.Join([]string{
		"db > Primary key must be positive.",
		"db > Executed.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunCreatesTableAndUsesCustomSchema(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer, name text, height real, weight real)",
		"insert 1 Alice 165.2 54.3",
		"insert 2 Bob 172.4 68.1",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name", "height", "weight"}, []string{"1", "Alice", "165.2", "54.3"}, []string{"2", "Bob", "172.4", "68.1"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunAltersTableAddColumn(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text)",
		"insert 1 Alice",
		"ALTER TABLE people ADD COLUMN height real;",
		".schema",
		"SELECT id, name, height FROM people;",
		"UPDATE people SET height = 165.2 WHERE id = 1;",
		"insert 2 Bob 172.4",
		"SELECT id, name, height FROM people ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > create table people (id integer primary key, name text, height real)",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name", "height"}, []string{"1", "Alice", "NULL"})...)
	wantLines = append(wantLines, "Executed.", "db > Executed.", "db > Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name", "height"}, []string{"1", "Alice", "165.2"}, []string{"2", "Bob", "172.4"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPersistsAlterTableAddColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	runScript(t, dbPath, []string{
		"insert 1 alice alice@example.com",
		"ALTER TABLE users ADD COLUMN age integer;",
		"UPDATE users SET age = 30 WHERE id = 1;",
		".exit",
	})

	got := runScript(t, dbPath, []string{
		".schema",
		"insert 2 bob bob@example.com 40",
		"SELECT id, username, age FROM users ORDER BY id ASC;",
		".exit",
	})

	wantLines := []string{
		"db > create table users (id INTEGER, username TEXT, email TEXT, age integer)",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "age"}, []string{"1", "alice", "30"}, []string{"2", "bob", "40"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunCreatesTableWithColumnConstraints(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text not null, code integer unique)",
		".schema",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > create table people (id integer primary key, name text not null, code integer unique)",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunCreatesTableWithTablePrimaryKeyConstraint(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer, name text, primary key (id))",
		"insert 1 Alice",
		"insert 1 Bob",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Error: Duplicate key.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunUsesNamedPrimaryKeyColumnAsBTreeKey(t *testing.T) {
	got := runTempScript(t, []string{
		"create table accounts (id integer, account_id integer primary key, name text)",
		"insert 10 2 Bob",
		"insert 10 1 Alice",
		"select",
		"select 2",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "account_id", "name"}, []string{"10", "1", "Alice"}, []string{"10", "2", "Bob"})...)
	wantLines = append(wantLines, "Executed.")
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "account_id", "name"}, []string{"10", "2", "Bob"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunRejectsDuplicateUniqueColumnValue(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, code integer unique, name text)",
		"insert 1 100 Alice",
		"insert 2 100 Bob",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Error: Constraint violation.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "code", "name"}, []string{"1", "100", "Alice"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunStoresNullValues(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, nickname text, code integer unique)",
		"insert 1 null null",
		"insert 2 null null",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "nickname", "code"}, []string{"1", "NULL", "NULL"}, []string{"2", "NULL", "NULL"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunRejectsNullForNotNullColumn(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text not null)",
		"insert 1 null",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Error: Constraint violation.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunTreatsQuotedNullAsText(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer primary key, name text)",
		"insert 1 'null'",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name"}, []string{"1", "null"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunSelectsByIDWithCustomSchema(t *testing.T) {
	got := runTempScript(t, []string{
		"create table people (id integer, name text, height real, weight real)",
		"insert 1 Alice 165.2 54.3",
		"insert 2 Bob 172.4 68.1",
		"select 2",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name", "height", "weight"}, []string{"2", "Bob", "172.4", "68.1"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunKeepsCustomSchemaAfterClosingConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	got := runScript(t, dbPath, []string{
		"create table people (id integer, name text, height real, weight real)",
		"insert 1 Alice 165.2 54.3",
		"insert 2 Bob 172.4 68.1",
		".exit",
	})
	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}

	got = runScript(t, dbPath, []string{
		"select",
		".exit",
	})
	wantLines := expectedTableLines("db > ", []string{"id", "name", "height", "weight"}, []string{"1", "Alice", "165.2", "54.3"}, []string{"2", "Bob", "172.4", "68.1"})
	wantLines = append(wantLines, "Executed.", "db > ")
	want = strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSchemaAfterClosingConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	runScript(t, dbPath, []string{
		"create table people (id integer, name text, height real, weight real)",
		".exit",
	})

	got := runScript(t, dbPath, []string{
		".schema",
		".exit",
	})

	want := strings.Join([]string{
		"db > create table people (id integer, name text, height real, weight real)",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunRejectsCreateTableWhenTableHasRows(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"create table people (id integer, name text, height real, weight real)",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Error: Table is not empty.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunReplacesTableWithCreateOrReplace(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 alice alice@example.com",
		"create or replace table people (id integer primary key, name text, height real)",
		".schema",
		"select",
		"insert 2 Bob 172.4",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
		"db > create table people (id integer primary key, name text, height real)",
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name", "height"}, []string{"2", "Bob", "172.4"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPersistsCreateOrReplaceTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	runScript(t, dbPath, []string{
		"insert 1 alice alice@example.com",
		"create or replace table people (id integer primary key, name text)",
		"insert 2 Bob",
		".exit",
	})

	got := runScript(t, dbPath, []string{
		".schema",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > create table people (id integer primary key, name text)",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "name"}, []string{"2", "Bob"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunAllowsRowsLargerThanDefaultRowSizeWhenTheyFitLeafPage(t *testing.T) {
	got := runTempScript(t, []string{
		"create table huge (id integer, first_name text, last_name text)",
		"insert 1 Alice Smith",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Executed.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "first_name", "last_name"}, []string{"1", "Alice", "Smith"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunRejectsRowsLargerThanLeafPage(t *testing.T) {
	got := runTempScript(t, []string{
		"create table too_huge (id integer, c1 text, c2 text, c3 text, c4 text, c5 text, c6 text, c7 text, c8 text, c9 text, c10 text, c11 text, c12 text, c13 text, c14 text, c15 text, c16 text)",
		".exit",
	})

	want := "db > Row is too large.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsSevenLeafNodeBTree(t *testing.T) {
	ids := []uint32{
		58, 56, 8, 54, 77, 7, 25, 71, 13, 22,
		53, 51, 59, 32, 36, 79, 10, 33, 20, 4,
		35, 76, 49, 24, 70, 48, 39, 15, 47, 30,
		86, 31, 68, 37, 66, 63, 40, 78, 19, 46,
		14, 81, 72, 6, 50, 85, 67, 2, 55, 69,
		5, 65, 52, 1, 29, 9, 43, 75, 21, 82,
		12, 18, 60, 44,
	}
	commands := make([]string, 0, len(ids)+2)
	for _, id := range ids {
		commands = append(commands, maxSizeInsertCommand(id))
	}
	commands = append(commands, ".btree")
	commands = append(commands, ".exit")

	got := runScript(t, filepath.Join(t.TempDir(), "test.db"), commands)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	want := []string{
		"db > Tree:",
		"- internal (size 1)",
		"  - internal (size 2)",
		"    - leaf (size 7)",
		"      - 1",
		"      - 2",
		"      - 4",
		"      - 5",
		"      - 6",
		"      - 7",
		"      - 8",
		"    - key 8",
		"    - leaf (size 11)",
		"      - 9",
		"      - 10",
		"      - 12",
		"      - 13",
		"      - 14",
		"      - 15",
		"      - 18",
		"      - 19",
		"      - 20",
		"      - 21",
		"      - 22",
		"    - key 22",
		"    - leaf (size 8)",
		"      - 24",
		"      - 25",
		"      - 29",
		"      - 30",
		"      - 31",
		"      - 32",
		"      - 33",
		"      - 35",
		"  - key 35",
		"  - internal (size 3)",
		"    - leaf (size 12)",
		"      - 36",
		"      - 37",
		"      - 39",
		"      - 40",
		"      - 43",
		"      - 44",
		"      - 46",
		"      - 47",
		"      - 48",
		"      - 49",
		"      - 50",
		"      - 51",
		"    - key 51",
		"    - leaf (size 11)",
		"      - 52",
		"      - 53",
		"      - 54",
		"      - 55",
		"      - 56",
		"      - 58",
		"      - 59",
		"      - 60",
		"      - 63",
		"      - 65",
		"      - 66",
		"    - key 66",
		"    - leaf (size 7)",
		"      - 67",
		"      - 68",
		"      - 69",
		"      - 70",
		"      - 71",
		"      - 72",
		"      - 75",
		"    - key 75",
		"    - leaf (size 8)",
		"      - 76",
		"      - 77",
		"      - 78",
		"      - 79",
		"      - 81",
		"      - 82",
		"      - 85",
		"      - 86",
		"db > ",
	}

	start := len(lines) - len(want)
	if start < 0 {
		t.Fatalf("expected at least %d lines, got %d: %q", len(want), len(lines), got)
	}
	if got := lines[start:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected tail %q, got %q", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestRunKeepsDataAfterClosingConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	got := runScript(t, dbPath, []string{
		"insert 1 user1 person1@example.com",
		".exit",
	})
	want := "db > Executed.\ndb > "
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}

	got = runScript(t, dbPath, []string{
		"select",
		".exit",
	})
	wantLines := expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "user1", "person1@example.com"})
	wantLines = append(wantLines, "Executed.", "db > ")
	want = strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsOneNodeBTree(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 3 user3 person3@example.com",
		"insert 1 user1 person1@example.com",
		"insert 2 user2 person2@example.com",
		".btree",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Tree:",
		"- leaf (size 3)",
		"  - 1",
		"  - 2",
		"  - 3",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsThreeLeafNodeBTree(t *testing.T) {
	commands := make([]string, 0, 16)
	for i := uint32(1); i <= leafNodeMaxCells+1; i++ {
		commands = append(commands, maxSizeInsertCommand(i))
	}
	commands = append(commands, ".btree")
	commands = append(commands, maxSizeInsertCommand(15))
	commands = append(commands, ".exit")

	got := runScriptWithExpectedCode(t, filepath.Join(t.TempDir(), "test.db"), commands, ExitSuccess)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	want := []string{
		"db > Tree:",
		"- internal (size 1)",
		"  - leaf (size 7)",
		"    - 1",
		"    - 2",
		"    - 3",
		"    - 4",
		"    - 5",
		"    - 6",
		"    - 7",
		"  - key 7",
		"  - leaf (size 7)",
		"    - 8",
		"    - 9",
		"    - 10",
		"    - 11",
		"    - 12",
		"    - 13",
		"    - 14",
		"db > Executed.",
		"db > ",
	}

	start := len(lines) - len(want)
	if start < 0 {
		t.Fatalf("expected at least %d lines, got %d: %q", len(want), len(lines), got)
	}
	if got := lines[start:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected tail %q, got %q", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestRunPrintsFourLeafNodeBTree(t *testing.T) {
	ids := []uint32{
		18, 7, 10, 29, 23, 4, 14, 30, 15, 26,
		22, 19, 2, 1, 21, 11, 6, 20, 5, 8,
		9, 3, 12, 27, 17, 16, 13, 24, 25, 28,
	}
	commands := make([]string, 0, len(ids)+2)
	for _, id := range ids {
		commands = append(commands, maxSizeInsertCommand(id))
	}
	commands = append(commands, ".btree")
	commands = append(commands, ".exit")

	got := runScript(t, filepath.Join(t.TempDir(), "test.db"), commands)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	want := []string{
		"db > Tree:",
		"- internal (size 3)",
		"  - leaf (size 7)",
		"    - 1",
		"    - 2",
		"    - 3",
		"    - 4",
		"    - 5",
		"    - 6",
		"    - 7",
		"  - key 7",
		"  - leaf (size 8)",
		"    - 8",
		"    - 9",
		"    - 10",
		"    - 11",
		"    - 12",
		"    - 13",
		"    - 14",
		"    - 15",
		"  - key 15",
		"  - leaf (size 7)",
		"    - 16",
		"    - 17",
		"    - 18",
		"    - 19",
		"    - 20",
		"    - 21",
		"    - 22",
		"  - key 22",
		"  - leaf (size 8)",
		"    - 23",
		"    - 24",
		"    - 25",
		"    - 26",
		"    - 27",
		"    - 28",
		"    - 29",
		"    - 30",
		"db > ",
	}

	start := len(lines) - len(want)
	if start < 0 {
		t.Fatalf("expected at least %d lines, got %d: %q", len(want), len(lines), got)
	}
	if got := lines[start:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected tail %q, got %q", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestRunDetectsDuplicateIDAfterRootSplit(t *testing.T) {
	commands := make([]string, 0, 17)
	for i := uint32(1); i <= leafNodeMaxCells+1; i++ {
		commands = append(commands, maxSizeInsertCommand(i))
	}
	commands = append(commands, maxSizeInsertCommand(14))
	commands = append(commands, ".exit")

	got := runScript(t, filepath.Join(t.TempDir(), "test.db"), commands)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	want := "db > Error: Duplicate key."
	if got := lines[len(lines)-2]; got != want {
		t.Fatalf("expected output line %q, got %q", want, got)
	}
}

func TestRunPrintsErrorForDuplicateID(t *testing.T) {
	got := runTempScript(t, []string{
		"insert 1 user1 person1@example.com",
		"insert 1 user1 person1@example.com",
		"select",
		".exit",
	})

	wantLines := []string{
		"db > Executed.",
		"db > Error: Duplicate key.",
	}
	wantLines = append(wantLines, expectedTableLines("db > ", []string{"id", "username", "email"}, []string{"1", "user1", "person1@example.com"})...)
	wantLines = append(wantLines, "Executed.", "db > ")
	want := strings.Join(wantLines, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestRunPrintsConstants(t *testing.T) {
	got := runTempScript(t, []string{".constants", ".exit"})

	want := strings.Join([]string{
		"db > Constants:",
		"ROW_SIZE: 300",
		"COMMON_NODE_HEADER_SIZE: 6",
		"LEAF_NODE_HEADER_SIZE: 16",
		"LEAF_NODE_CELL_SIZE: 308",
		"LEAF_NODE_SPACE_FOR_CELLS: 4080",
		"LEAF_NODE_MAX_CELLS: 13",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestCursorPositions(t *testing.T) {
	table, err := dbOpen(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() {
		if err := dbClose(table); err != nil {
			t.Fatalf("failed to close test database: %v", err)
		}
	}()

	start := tableStart(table)
	if !start.EndOfTable {
		t.Fatal("expected empty table start cursor to be at end of table")
	}

	statement := &Statement{
		Type:        StatementInsert,
		RowToInsert: defaultRow(1, "alice", "alice@example.com"),
	}
	if got := executeInsert(statement, table); got != ExecuteSuccess {
		t.Fatalf("expected execute result %d, got %d", ExecuteSuccess, got)
	}

	start = tableStart(table)
	if start.EndOfTable {
		t.Fatal("expected non-empty table start cursor not to be at end of table")
	}
	if start.CellNum != 0 {
		t.Fatalf("expected start cursor cell 0, got %d", start.CellNum)
	}

	cursorAdvance(start)
	if !start.EndOfTable {
		t.Fatal("expected cursor to be at end of table after advancing past the only row")
	}

	end := tableEnd(table)
	if !end.EndOfTable {
		t.Fatal("expected table end cursor to be at end of table")
	}
	if end.CellNum != leafNodeNumCells(getPage(table.Pager, table.RootPageNum)) {
		t.Fatalf("expected end cursor cell %d, got %d", leafNodeNumCells(getPage(table.Pager, table.RootPageNum)), end.CellNum)
	}
}

func TestLeafNodeFindReturnsInsertionPosition(t *testing.T) {
	table, err := dbOpen(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() {
		if err := dbClose(table); err != nil {
			t.Fatalf("failed to close test database: %v", err)
		}
	}()

	for _, row := range []Row{
		defaultRow(1, "user1", "person1@example.com"),
		defaultRow(3, "user3", "person3@example.com"),
		defaultRow(5, "user5", "person5@example.com"),
	} {
		statement := &Statement{Type: StatementInsert, RowToInsert: row}
		if got := executeInsert(statement, table); got != ExecuteSuccess {
			t.Fatalf("expected execute result %d, got %d", ExecuteSuccess, got)
		}
	}

	tests := []struct {
		key      uint32
		cellNum  uint32
		testName string
	}{
		{key: 1, cellNum: 0, testName: "existing first key"},
		{key: 2, cellNum: 1, testName: "between first and second"},
		{key: 5, cellNum: 2, testName: "existing last key"},
		{key: 6, cellNum: 3, testName: "after last key"},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			cursor := tableFind(table, tt.key)
			if cursor.CellNum != tt.cellNum {
				t.Fatalf("expected cell %d, got %d", tt.cellNum, cursor.CellNum)
			}
		})
	}
}

func TestExecuteInsertSplitsRootLeaf(t *testing.T) {
	table, err := dbOpen(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() {
		if err := dbClose(table); err != nil {
			t.Fatalf("failed to close test database: %v", err)
		}
	}()
	for i := uint32(1); i <= leafNodeMaxCells+1; i++ {
		statement := &Statement{
			Type:        StatementInsert,
			RowToInsert: maxSizeRow(i),
		}
		if got := executeInsert(statement, table); got != ExecuteSuccess {
			t.Fatalf("expected execute result %d, got %d", ExecuteSuccess, got)
		}
	}

	root := getPage(table.Pager, table.RootPageNum)
	if got := getNodeType(root); got != NodeInternal {
		t.Fatalf("expected root node type %d, got %d", NodeInternal, got)
	}
	if got := internalNodeNumKeys(root); got != 1 {
		t.Fatalf("expected internal root to have 1 key, got %d", got)
	}
	if got := internalNodeKey(root, 0); got != 7 {
		t.Fatalf("expected separator key 7, got %d", got)
	}
}

func TestExecuteInsertKeepsShortRowsInSingleLeafPastMaxFixedCells(t *testing.T) {
	table, err := dbOpen(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() {
		if err := dbClose(table); err != nil {
			t.Fatalf("failed to close test database: %v", err)
		}
	}()

	for i := uint32(1); i <= leafNodeMaxCells+1; i++ {
		statement := &Statement{
			Type:        StatementInsert,
			RowToInsert: defaultRow(i, "u", "e"),
		}
		if got := executeInsert(statement, table); got != ExecuteSuccess {
			t.Fatalf("expected execute result %d, got %d", ExecuteSuccess, got)
		}
	}

	root := getPage(table.Pager, table.RootPageNum)
	if got := getNodeType(root); got != NodeLeaf {
		t.Fatalf("expected root node type %d, got %d", NodeLeaf, got)
	}
	if got := leafNodeNumCells(root); got != leafNodeMaxCells+1 {
		t.Fatalf("expected leaf to hold %d short rows, got %d", leafNodeMaxCells+1, got)
	}
}

func TestSerializeAndDeserializeRow(t *testing.T) {
	row := defaultRow(1, "alice", "alice@example.com")
	storage := make([]byte, rowSize)

	serializeRow(row, DefaultTableSchema(), storage)
	if string(storage[:len(rowRecordFormatMagic)]) != rowRecordFormatMagic {
		t.Fatalf("expected row format magic %q, got %q", rowRecordFormatMagic, string(storage[:len(rowRecordFormatMagic)]))
	}
	got := deserializeRow(storage, DefaultTableSchema())

	for _, column := range DefaultTableSchema().Columns {
		if !valuesEqual(rowValue(got, column), rowValue(row, column)) {
			t.Fatalf("expected row %#v, got %#v", row, got)
		}
	}
}

func TestSerializeAndDeserializeBlobRow(t *testing.T) {
	schema := TableSchema{
		Name: "files",
		Columns: []Column{
			{Name: "id", DeclaredType: "INTEGER", Affinity: AffinityInteger, PrimaryKey: true},
			{Name: "data", DeclaredType: "BLOB", Affinity: AffinityBlob},
		},
	}
	row := Row{
		Values: map[string]Value{
			"id":   {StorageClass: StorageInteger, Integer: 1},
			"data": {StorageClass: StorageBlob, Blob: []byte{0x00, 0x01, 0xfe, 0xff}},
		},
	}
	storage := make([]byte, serializedRowSize(row, schema))

	serializeRow(row, schema, storage)
	got := deserializeRow(storage, schema)

	for _, column := range schema.Columns {
		if !valuesEqual(rowValue(got, column), rowValue(row, column)) {
			t.Fatalf("expected row %#v, got %#v", row, got)
		}
	}
}

func TestDeserializeFixedRowFormat(t *testing.T) {
	row := defaultRow(1, "alice", "alice@example.com")
	storage := make([]byte, DefaultTableSchema().RowLayout().Size)
	layout := DefaultTableSchema().RowLayout()

	start, end, _ := layout.ColumnRange(idColumnName)
	binary.LittleEndian.PutUint32(storage[start:end], 1)
	start, end, _ = layout.ColumnRange(usernameColumnName)
	writeFixedString(storage[start:end], "alice")
	start, end, _ = layout.ColumnRange(emailColumnName)
	writeFixedString(storage[start:end], "alice@example.com")

	got := deserializeRow(storage, DefaultTableSchema())
	for _, column := range DefaultTableSchema().Columns {
		if !valuesEqual(rowValue(got, column), rowValue(row, column)) {
			t.Fatalf("expected row %#v, got %#v", row, got)
		}
	}
}
