package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
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

	want := strings.Join([]string{
		"Executed.",
		"Executed.",
		"Executed.",
		"(Alice; A, keeps -- text)",
		"(Bob, NULL)",
		"Executed.",
	}, "\n") + "\n"
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (1, Alice Smith, alice smith@example.com)",
		"(2, Bob's note, bob@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (1, alice, alice@example.com)",
		"(2, bob, bob@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (1, alice, alice@example.com)",
		"(2, bob, bob@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (alice, alice@example.com)",
		"(bob, bob@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (2, bob, bob@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (2, alice.smith@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (1)",
		"Executed.",
		"db > (2)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (Alice)",
		"(Bob)",
		"Executed.",
		"db > (Alice)",
		"(Carol)",
		"Executed.",
		"db > (2)",
		"(3)",
		"Executed.",
		"db > (2)",
		"(3)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (Alice)",
		"(Bob)",
		"Executed.",
		"db > (1)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (Alice)",
		"(Carol)",
		"Executed.",
		"db > (Alice)",
		"Executed.",
		"db > (2)",
		"(3)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (Alice)",
		"Executed.",
		"db > (Alice)",
		"Executed.",
		"db > (Alice)",
		"(Carol)",
		"Executed.",
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (Alice, 165.2)",
		"Executed.",
		"db > ",
	}, "\n")
	if got != want {
		t.Fatalf("expected output %q, got %q", want, got)
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > (1, alice, alice@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (2, bob, bob@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := []string{
		"db > (1, user1, person1@example.com)",
		"(2, user2, person2@example.com)",
		"(3, user3, person3@example.com)",
		"(4, user4, person4@example.com)",
		"(5, user5, person5@example.com)",
		"(6, user6, person6@example.com)",
		"(7, user7, person7@example.com)",
		"(8, user8, person8@example.com)",
		"(9, user9, person9@example.com)",
		"(10, user10, person10@example.com)",
		"(11, user11, person11@example.com)",
		"(12, user12, person12@example.com)",
		"(13, user13, person13@example.com)",
		"(14, user14, person14@example.com)",
		"(15, user15, person15@example.com)",
		"Executed.",
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > (1, " + longUsername + ", " + longEmail + ")",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (1, Alice, 165.2, 54.3)",
		"(2, Bob, 172.4, 68.1)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (10, 1, Alice)",
		"(10, 2, Bob)",
		"Executed.",
		"db > (10, 2, Bob)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Error: Constraint violation.",
		"db > (1, 100, Alice)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (1, NULL, NULL)",
		"(2, NULL, NULL)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (1, null)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > Executed.",
		"db > (2, Bob, 172.4, 68.1)",
		"Executed.",
		"db > ",
	}, "\n")
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
	want = strings.Join([]string{
		"db > (1, Alice, 165.2, 54.3)",
		"(2, Bob, 172.4, 68.1)",
		"Executed.",
		"db > ",
	}, "\n")
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

func TestRunAllowsRowsLargerThanDefaultRowSizeWhenTheyFitLeafPage(t *testing.T) {
	got := runTempScript(t, []string{
		"create table huge (id integer, first_name text, last_name text)",
		"insert 1 Alice Smith",
		"select",
		".exit",
	})

	want := strings.Join([]string{
		"db > Executed.",
		"db > Executed.",
		"db > (1, Alice, Smith)",
		"Executed.",
		"db > ",
	}, "\n")
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
	want = strings.Join([]string{
		"db > (1, user1, person1@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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

	want := strings.Join([]string{
		"db > Executed.",
		"db > Error: Duplicate key.",
		"db > (1, user1, person1@example.com)",
		"Executed.",
		"db > ",
	}, "\n")
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
