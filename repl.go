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

		// 先頭が . の入力はDB操作ではなくメタコマンドとして扱う。
		if strings.HasPrefix(input, ".") {
			switch doMetaCommand(input, table, out) {
			case MetaCommandSuccess:
				if input == ".exit" {
					return ExitSuccess
				}
				continue
			case MetaCommandUnrecognizedCommand:
				fmt.Fprintf(out, "Unrecognized command '%s'.\n", input)
				continue
			}
		}

		var statement Statement
		switch prepareStatement(input, &statement, table.Schema) {
		case PrepareSuccess:
		case PrepareNegativeID:
			fmt.Fprintln(out, "Primary key must be positive.")
			continue
		case PrepareStringTooLong:
			fmt.Fprintln(out, "String is too long.")
			continue
		case PrepareRowTooLarge:
			fmt.Fprintln(out, "Row is too large.")
			continue
		case PrepareSyntaxError:
			fmt.Fprintln(out, "Syntax error. Could not parse statement.")
			continue
		case PrepareUnrecognizedStatement:
			fmt.Fprintf(out, "Unrecognized keyword at start of '%s'.\n", input)
			continue
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
	}
}
