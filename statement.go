package main

import (
	"io"
	"strconv"
	"strings"
	"unicode"
)

func prepareCreateTable(input string, statement *Statement) PrepareResult {
	schema, ok := parseCreateTable(input)
	if !ok {
		return PrepareSyntaxError
	}
	if schema.RowLayout().Size > rowSize {
		return PrepareRowTooLarge
	}

	statement.Type = StatementCreateTable
	statement.Schema = schema
	return PrepareSuccess
}

// insert入力をRow付きのステートメントへ変換する。
func prepareInsert(input string, statement *Statement, schema TableSchema) PrepareResult {
	statement.Type = StatementInsert

	fields, ok := parseInsertFields(input)
	if !ok {
		return PrepareSyntaxError
	}
	if len(fields) != len(schema.Columns)+1 {
		return PrepareSyntaxError
	}

	primaryKeyColumn, ok := schema.PrimaryKeyColumn()
	if !ok {
		return PrepareSyntaxError
	}

	statement.RowToInsert = Row{
		Values: make(map[string]Value, len(schema.Columns)),
	}

	for i, column := range schema.Columns {
		rawValue := fields[i+1]
		value, result := parseColumnValue(rawValue, column)
		if result != PrepareSuccess {
			return result
		}

		statement.RowToInsert.Values[column.Name] = value
		if strings.EqualFold(column.Name, primaryKeyColumn.Name) {
			statement.RowToInsert.ID = uint32(value.Integer)
		}
		switch strings.ToLower(column.Name) {
		case usernameColumnName:
			statement.RowToInsert.Username = value.Text
		case emailColumnName:
			statement.RowToInsert.Email = value.Text
		}
	}

	return PrepareSuccess
}

func parseInsertFields(input string) ([]string, bool) {
	fields := []string{}
	for i := 0; i < len(input); {
		for i < len(input) && unicode.IsSpace(rune(input[i])) {
			i++
		}
		if i >= len(input) {
			break
		}

		if input[i] == '\'' {
			value, next, ok := parseSQLStringLiteral(input, i)
			if !ok {
				return nil, false
			}
			fields = append(fields, value)
			i = next
			if i < len(input) && !unicode.IsSpace(rune(input[i])) {
				return nil, false
			}
			continue
		}

		start := i
		for i < len(input) && !unicode.IsSpace(rune(input[i])) {
			if input[i] == '\'' {
				return nil, false
			}
			i++
		}
		fields = append(fields, input[start:i])
	}

	return fields, true
}

func parseSQLStringLiteral(input string, start int) (string, int, bool) {
	var builder strings.Builder
	for i := start + 1; i < len(input); i++ {
		switch input[i] {
		case '\'':
			if i+1 < len(input) && input[i+1] == '\'' {
				builder.WriteByte('\'')
				i++
				continue
			}
			return builder.String(), i + 1, true
		case '\\':
			if i+1 >= len(input) {
				return "", 0, false
			}
			escaped, ok := mysqlEscapedByte(input[i+1])
			if !ok {
				builder.WriteByte(input[i+1])
			} else {
				builder.WriteByte(escaped)
			}
			i++
		default:
			builder.WriteByte(input[i])
		}
	}

	return "", 0, false
}

func mysqlEscapedByte(value byte) (byte, bool) {
	switch value {
	case '\'':
		return '\'', true
	case '"':
		return '"', true
	case 'b':
		return '\b', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	case 'Z':
		return 26, true
	case '\\':
		return '\\', true
	default:
		return 0, false
	}
}

// create table入力からテーブル名とカラム定義を取り出す。
func parseCreateTable(input string) (TableSchema, bool) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)
	const prefix = "create table"
	if !strings.HasPrefix(lower, prefix) {
		return TableSchema{}, false
	}

	openParen := strings.Index(trimmed, "(")
	closeParen := strings.LastIndex(trimmed, ")")
	if openParen < 0 || closeParen < openParen {
		return TableSchema{}, false
	}
	if strings.TrimSpace(trimmed[closeParen+1:]) != "" {
		return TableSchema{}, false
	}

	tableName := strings.TrimSpace(trimmed[len(prefix):openParen])
	if tableName == "" {
		return TableSchema{}, false
	}

	columnDefinitions := strings.Split(trimmed[openParen+1:closeParen], ",")
	columns := make([]Column, 0, len(columnDefinitions))
	for _, definition := range columnDefinitions {
		parts := strings.Fields(strings.TrimSpace(definition))
		if len(parts) < 2 {
			return TableSchema{}, false
		}

		columns = append(columns, NewColumn(parts[0], strings.Join(parts[1:], " ")))
	}

	schema := TableSchema{
		Name:    tableName,
		Columns: columns,
	}
	if !schema.IsUsable() {
		return TableSchema{}, false
	}

	return schema, true
}

// 入力文字列を実行可能なステートメントへ変換する。
func prepareStatement(input string, statement *Statement, schema TableSchema) PrepareResult {
	input = strings.TrimSpace(input)

	if strings.HasPrefix(strings.ToLower(input), "create table") {
		return prepareCreateTable(input, statement)
	}

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

func parseColumnValue(rawValue string, column Column) (Value, PrepareResult) {
	switch column.Affinity {
	case AffinityInteger:
		integer, err := strconv.ParseInt(rawValue, 10, 32)
		if err != nil {
			return Value{}, PrepareSyntaxError
		}
		if !column.ValidateIntegerValue(integer) {
			return Value{}, PrepareNegativeID
		}
		return Value{StorageClass: StorageInteger, Integer: integer}, PrepareSuccess
	case AffinityReal:
		real, err := strconv.ParseFloat(rawValue, 64)
		if err != nil {
			return Value{}, PrepareSyntaxError
		}
		return Value{StorageClass: StorageReal, Real: real}, PrepareSuccess
	case AffinityText:
		if !column.ValidateTextValue(rawValue) {
			return Value{}, PrepareStringTooLong
		}
		return Value{StorageClass: StorageText, Text: rawValue}, PrepareSuccess
	default:
		return Value{}, PrepareSyntaxError
	}
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

func executeCreateTable(statement *Statement, table *Table) ExecuteResult {
	if !tableIsEmpty(table) {
		return ExecuteTableNotEmpty
	}

	table.Schema = statement.Schema
	return ExecuteSuccess
}

// 現在の実装では、既存データのあるテーブルのスキーマ変更は許可しない。
func tableIsEmpty(table *Table) bool {
	root := getPage(table.Pager, table.RootPageNum)
	if getNodeType(root) != NodeLeaf {
		return false
	}

	return leafNodeNumCells(root) == 0
}

// selectステートメントを実行し、保存済みの全行を出力する。
func executeSelect(statement *Statement, table *Table, out io.Writer) ExecuteResult {
	if statement.SelectByID != nil {
		cursor := tableFind(table, *statement.SelectByID)
		node := getPage(table.Pager, cursor.PageNum)
		if cursor.CellNum < leafNodeNumCells(node) && leafNodeKey(node, cursor.CellNum) == *statement.SelectByID {
			printRow(deserializeRow(cursorValue(cursor), table.Schema), table.Schema, out)
		}
		return ExecuteSuccess
	}

	cursor := tableStart(table)
	for !cursor.EndOfTable {
		printRow(deserializeRow(cursorValue(cursor), table.Schema), table.Schema, out)
		cursorAdvance(cursor)
	}

	return ExecuteSuccess
}

// パース済みステートメントを実行する。
func executeStatement(statement *Statement, table *Table, out io.Writer) ExecuteResult {
	switch statement.Type {
	case StatementCreateTable:
		return executeCreateTable(statement, table)
	case StatementInsert:
		return executeInsert(statement, table)
	case StatementSelect:
		return executeSelect(statement, table, out)
	}

	return ExecuteSuccess
}
