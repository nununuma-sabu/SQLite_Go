package main

import (
	"fmt"
	"io"
)

// .exit のようにDB本体ではなくREPLを制御するコマンドを処理する。
func doMetaCommand(input string, table *Table, out io.Writer) MetaCommandResult {
	if input == ".exit" {
		return MetaCommandSuccess
	}
	if input == ".btree" {
		fmt.Fprintln(out, "Tree:")
		printTree(table.Pager, table.RootPageNum, 0, out)
		return MetaCommandSuccess
	}
	if input == ".constants" {
		fmt.Fprintln(out, "Constants:")
		printConstants(out)
		return MetaCommandSuccess
	}
	if input == ".schema" {
		fmt.Fprintln(out, table.Schema.CreateStatement())
		return MetaCommandSuccess
	}

	return MetaCommandUnrecognizedCommand
}
