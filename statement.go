package main

import (
	"io"
	"strconv"
	"strings"
)

// insert入力をRow付きのステートメントへ変換する。
func prepareInsert(input string, statement *Statement, schema TableSchema) PrepareResult {
	statement.Type = StatementInsert

	fields := strings.Fields(input)
	if len(fields) < 4 {
		return PrepareSyntaxError
	}

	id, err := strconv.ParseInt(fields[1], 10, 32)
	if err != nil {
		return PrepareSyntaxError
	}
	idColumn, ok := schema.Column(idColumnName)
	if !ok || !idColumn.ValidateIntegerValue(id) {
		return PrepareNegativeID
	}

	username := fields[2]
	email := fields[3]
	usernameColumn, ok := schema.Column(usernameColumnName)
	if !ok || !usernameColumn.ValidateTextValue(username) {
		return PrepareStringTooLong
	}
	emailColumn, ok := schema.Column(emailColumnName)
	if !ok || !emailColumn.ValidateTextValue(email) {
		return PrepareStringTooLong
	}

	statement.RowToInsert = Row{
		ID:       uint32(id),
		Username: username,
		Email:    email,
	}

	return PrepareSuccess
}

// 入力文字列を実行可能なステートメントへ変換する。
func prepareStatement(input string, statement *Statement, schema TableSchema) PrepareResult {
	if strings.HasPrefix(input, "insert") {
		return prepareInsert(input, statement, schema)
	}

	if strings.HasPrefix(input, "select") {
		statement.Type = StatementSelect
		fields := strings.Fields(input)
		if len(fields) == 1 {
			return PrepareSuccess
		}
		if len(fields) != 2 {
			return PrepareSyntaxError
		}

		id, err := strconv.ParseInt(fields[1], 10, 32)
		if err != nil {
			return PrepareSyntaxError
		}
		idColumn, ok := schema.PrimaryKeyColumn()
		if !ok || !idColumn.ValidateIntegerValue(id) {
			return PrepareNegativeID
		}

		selectByID := uint32(id)
		statement.SelectByID = &selectByID
		return PrepareSuccess
	}

	return PrepareUnrecognizedStatement
}

// insertステートメントを実行し、B-Tree内の適切な位置へ行を追加する。
func executeInsert(statement *Statement, table *Table) ExecuteResult {
	keyToInsert := statement.RowToInsert.ID
	cursor := tableFind(table, keyToInsert)
	leafNode := getPage(table.Pager, cursor.PageNum)
	numCells := leafNodeNumCells(leafNode)
	if cursor.CellNum < numCells {
		keyAtIndex := leafNodeKey(leafNode, cursor.CellNum)
		if keyAtIndex == keyToInsert {
			return ExecuteDuplicateKey
		}
	}

	leafNodeInsert(cursor, keyToInsert, statement.RowToInsert)

	return ExecuteSuccess
}

// selectステートメントを実行し、保存済みの全行を出力する。
func executeSelect(statement *Statement, table *Table, out io.Writer) ExecuteResult {
	if statement.SelectByID != nil {
		cursor := tableFind(table, *statement.SelectByID)
		node := getPage(table.Pager, cursor.PageNum)
		if cursor.CellNum < leafNodeNumCells(node) && leafNodeKey(node, cursor.CellNum) == *statement.SelectByID {
			printRow(deserializeRow(cursorValue(cursor)), out)
		}
		return ExecuteSuccess
	}

	cursor := tableStart(table)
	for !cursor.EndOfTable {
		printRow(deserializeRow(cursorValue(cursor)), out)
		cursorAdvance(cursor)
	}

	return ExecuteSuccess
}

// パース済みステートメントを実行する。
func executeStatement(statement *Statement, table *Table, out io.Writer) ExecuteResult {
	switch statement.Type {
	case StatementInsert:
		return executeInsert(statement, table)
	case StatementSelect:
		return executeSelect(statement, table, out)
	}

	return ExecuteSuccess
}
