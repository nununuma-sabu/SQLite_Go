package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ユーザーに入力待ちであることを示すプロンプトを表示する。
func printPrompt(w io.Writer) {
	fmt.Fprint(w, prompt)
}

// 標準入力から1行ずつ読み取り、メタコマンドやステートメントを処理する。
// 戻り値はプロセスの終了コードとして利用する。
func run(in io.Reader, out io.Writer, table *Table) (code ExitCode) {
	scanner := bufio.NewScanner(in)
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintln(out, recovered)
			code = ExitFailure
		}
		if err := dbClose(table); err != nil {
			fmt.Fprintf(out, "Error closing db file: %v\n", err)
			code = ExitFailure
		}
	}()

	for {
		printPrompt(out)

		// 入力が読み取れない場合は、読み取りエラーとEOFを分けて扱う。
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				fmt.Fprintln(out, "Error reading input")
				return ExitFailure
			}
			return ExitSuccess
		}

		input := scanner.Text()

		exit, result := runInput(input, table, out)
		if exit {
			return result
		}
	}
}

func runSQLScript(in io.Reader, out io.Writer, table *Table) (code ExitCode) {
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintln(out, recovered)
			code = ExitFailure
		}
		if err := dbClose(table); err != nil {
			fmt.Fprintf(out, "Error closing db file: %v\n", err)
			code = ExitFailure
		}
	}()

	contents, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintln(out, "Error reading input")
		return ExitFailure
	}

	statements, ok := splitSQLStatements(string(contents))
	if !ok {
		fmt.Fprintln(out, "Syntax error. Could not parse statement.")
		return ExitSuccess
	}

	for _, input := range statements {
		exit, result := runInput(input, table, out)
		if exit {
			return result
		}
	}

	return ExitSuccess
}

func runInput(input string, table *Table, out io.Writer) (bool, ExitCode) {
	input = strings.TrimSpace(input)
	if input == "" {
		return false, ExitSuccess
	}

	// 先頭が . の入力はDB操作ではなくメタコマンドとして扱う。
	if strings.HasPrefix(input, ".") {
		switch doMetaCommand(input, table, out) {
		case MetaCommandSuccess:
			return input == ".exit", ExitSuccess
		case MetaCommandUnrecognizedCommand:
			fmt.Fprintf(out, "Unrecognized command '%s'.\n", input)
			return false, ExitSuccess
		}
	}

	var statement Statement
	switch prepareStatement(input, &statement, table) {
	case PrepareSuccess:
	case PrepareNegativeID:
		fmt.Fprintln(out, "Primary key must be positive.")
		return false, ExitSuccess
	case PrepareStringTooLong:
		fmt.Fprintln(out, "String is too long.")
		return false, ExitSuccess
	case PrepareRowTooLarge:
		fmt.Fprintln(out, "Row is too large.")
		return false, ExitSuccess
	case PrepareConstraintViolation:
		fmt.Fprintln(out, "Error: Constraint violation.")
		return false, ExitSuccess
	case PrepareSyntaxError:
		fmt.Fprintln(out, "Syntax error. Could not parse statement.")
		return false, ExitSuccess
	case PrepareUnrecognizedStatement:
		fmt.Fprintf(out, "Unrecognized keyword at start of '%s'.\n", input)
		return false, ExitSuccess
	}

	switch executeStatement(&statement, table, out) {
	case ExecuteSuccess:
		fmt.Fprintln(out, "Executed.")
	case ExecuteDuplicateKey:
		fmt.Fprintln(out, "Error: Duplicate key.")
	case ExecuteTableFull:
		fmt.Fprintln(out, "Error: Table full.")
	case ExecuteTableNotEmpty:
		fmt.Fprintln(out, "Error: Table is not empty.")
	case ExecuteConstraintViolation:
		fmt.Fprintln(out, "Error: Constraint violation.")
	}

	return false, ExitSuccess
}

func splitSQLStatements(input string) ([]string, bool) {
	input = stripSQLLineComments(input)
	statements := []string{}
	start := 0
	inString := false
	for i := 0; i < len(input); i++ {
		switch input[i] {
		case '\'':
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
		case ';':
			if !inString {
				statement := strings.TrimSpace(input[start:i])
				if statement != "" {
					statements = append(statements, statement)
				}
				start = i + 1
			}
		}
	}
	if inString {
		return nil, false
	}

	tail := strings.TrimSpace(input[start:])
	if tail != "" {
		statements = append(statements, tail)
	}

	return statements, true
}

func stripSQLLineComments(input string) string {
	var builder strings.Builder
	inString := false
	for i := 0; i < len(input); i++ {
		if input[i] == '\'' {
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				builder.WriteByte(input[i])
				i++
				builder.WriteByte(input[i])
				continue
			}
			inString = !inString
			builder.WriteByte(input[i])
			continue
		}
		if !inString && input[i] == '-' && i+1 < len(input) && input[i+1] == '-' {
			for i < len(input) && input[i] != '\n' {
				i++
			}
			if i < len(input) {
				builder.WriteByte(input[i])
			}
			continue
		}
		builder.WriteByte(input[i])
	}

	return builder.String()
}
