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
	if schema.SerializedRowSize() > leafNodeMaxPayloadSize {
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

	statement.RowToInsert = Row{
		Values: make(map[string]Value, len(schema.Columns)),
	}

	for i, column := range schema.Columns {
		value, result := parseColumnValue(fields[i+1], column)
		if result != PrepareSuccess {
			return result
		}

		statement.RowToInsert.Values[column.Name] = value
	}

	return PrepareSuccess
}

type insertField struct {
	Value  string
	Quoted bool
}

func parseInsertFields(input string) ([]insertField, bool) {
	fields := []insertField{}
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
			fields = append(fields, insertField{Value: value, Quoted: true})
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
		fields = append(fields, insertField{Value: input[start:i]})
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

	definitions, ok := splitSQLList(trimmed[openParen+1 : closeParen])
	if !ok {
		return TableSchema{}, false
	}
	columns := make([]Column, 0, len(definitions))
	tablePrimaryKey := ""
	for _, definition := range definitions {
		if isTableConstraint(definition) {
			primaryKey, ok := parseTablePrimaryKeyConstraint(definition)
			if !ok {
				return TableSchema{}, false
			}
			if tablePrimaryKey != "" {
				return TableSchema{}, false
			}
			tablePrimaryKey = primaryKey
			continue
		}

		column, ok := parseColumnDefinition(definition)
		if !ok {
			return TableSchema{}, false
		}

		columns = append(columns, column)
	}

	if tablePrimaryKey != "" {
		found := false
		for i := range columns {
			if strings.EqualFold(columns[i].Name, tablePrimaryKey) {
				columns[i].PrimaryKey = true
				columns[i].PrimaryKeyConstraint = true
				found = true
				break
			}
		}
		if !found {
			return TableSchema{}, false
		}
	}
	applyImplicitIDPrimaryKey(columns)

	schema := TableSchema{
		Name:    tableName,
		Columns: columns,
	}
	if !schema.IsUsable() {
		return TableSchema{}, false
	}

	return schema, true
}

func applyImplicitIDPrimaryKey(columns []Column) {
	for _, column := range columns {
		if column.PrimaryKey {
			return
		}
	}

	for i := range columns {
		if strings.EqualFold(columns[i].Name, idColumnName) && columns[i].Affinity == AffinityInteger {
			columns[i].PrimaryKey = true
			return
		}
	}
}

func splitSQLList(input string) ([]string, bool) {
	items := []string{}
	start := 0
	depth := 0
	inString := false

	for i := 0; i < len(input); i++ {
		switch input[i] {
		case '\'':
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
		case '(':
			if !inString {
				depth++
			}
		case ')':
			if !inString {
				depth--
				if depth < 0 {
					return nil, false
				}
			}
		case ',':
			if !inString && depth == 0 {
				item := strings.TrimSpace(input[start:i])
				if item == "" {
					return nil, false
				}
				items = append(items, item)
				start = i + 1
			}
		}
	}
	if inString || depth != 0 {
		return nil, false
	}

	item := strings.TrimSpace(input[start:])
	if item == "" {
		return nil, false
	}

	return append(items, item), true
}

func isTableConstraint(definition string) bool {
	tokens := strings.Fields(definition)
	if len(tokens) == 0 {
		return false
	}
	index := 0
	if strings.EqualFold(tokens[index], "constraint") {
		index += 2
	}
	if index >= len(tokens) {
		return false
	}

	return strings.EqualFold(tokens[index], "primary") ||
		strings.EqualFold(tokens[index], "unique") ||
		strings.EqualFold(tokens[index], "check") ||
		strings.EqualFold(tokens[index], "foreign")
}

func parseTablePrimaryKeyConstraint(definition string) (string, bool) {
	normalized := strings.TrimSpace(definition)
	tokens := strings.Fields(normalized)
	index := 0
	if len(tokens) >= 2 && strings.EqualFold(tokens[0], "constraint") {
		index = 2
	}
	if index+1 >= len(tokens) || !strings.EqualFold(tokens[index], "primary") || !strings.EqualFold(tokens[index+1], "key") {
		return "", false
	}

	openParen := strings.Index(normalized, "(")
	closeParen := strings.LastIndex(normalized, ")")
	if openParen < 0 || closeParen < openParen || strings.TrimSpace(normalized[closeParen+1:]) != "" {
		return "", false
	}

	columns, ok := splitSQLList(normalized[openParen+1 : closeParen])
	if !ok || len(columns) != 1 {
		return "", false
	}

	return strings.TrimSpace(columns[0]), true
}

func parseColumnDefinition(definition string) (Column, bool) {
	parts := strings.Fields(strings.TrimSpace(definition))
	if len(parts) < 2 {
		return Column{}, false
	}

	constraintIndex := len(parts)
	for i := 1; i < len(parts); i++ {
		if isColumnConstraintKeyword(parts[i]) {
			constraintIndex = i
			break
		}
	}
	if constraintIndex == 1 {
		return Column{}, false
	}

	column := NewColumn(parts[0], strings.Join(parts[1:constraintIndex], " "))
	for i := constraintIndex; i < len(parts); i++ {
		if strings.EqualFold(parts[i], "constraint") {
			i++
			if i >= len(parts) {
				return Column{}, false
			}
			continue
		}

		switch {
		case strings.EqualFold(parts[i], "primary"):
			if i+1 >= len(parts) || !strings.EqualFold(parts[i+1], "key") {
				return Column{}, false
			}
			column.PrimaryKey = true
			column.PrimaryKeyConstraint = true
			i++
		case strings.EqualFold(parts[i], "not"):
			if i+1 >= len(parts) || !strings.EqualFold(parts[i+1], "null") {
				return Column{}, false
			}
			column.NotNull = true
			i++
		case strings.EqualFold(parts[i], "unique"):
			column.Unique = true
		case strings.EqualFold(parts[i], "asc"),
			strings.EqualFold(parts[i], "desc"),
			strings.EqualFold(parts[i], "autoincrement"):
		case strings.EqualFold(parts[i], "on"):
			if i+2 >= len(parts) || !strings.EqualFold(parts[i+1], "conflict") || !isConflictResolution(parts[i+2]) {
				return Column{}, false
			}
			i += 2
		default:
			return Column{}, false
		}
	}

	return column, true
}

func isColumnConstraintKeyword(token string) bool {
	return strings.EqualFold(token, "constraint") ||
		strings.EqualFold(token, "primary") ||
		strings.EqualFold(token, "not") ||
		strings.EqualFold(token, "unique") ||
		strings.EqualFold(token, "check") ||
		strings.EqualFold(token, "default") ||
		strings.EqualFold(token, "collate") ||
		strings.EqualFold(token, "references") ||
		strings.EqualFold(token, "generated")
}

func isConflictResolution(token string) bool {
	return strings.EqualFold(token, "rollback") ||
		strings.EqualFold(token, "abort") ||
		strings.EqualFold(token, "fail") ||
		strings.EqualFold(token, "ignore") ||
		strings.EqualFold(token, "replace")
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

	if strings.HasPrefix(strings.ToLower(input), "select") {
		statement.Type = StatementSelect
		if strings.Contains(strings.ToLower(input), " from ") {
			columns, result := parseSelectStatement(input, schema)
			if result != PrepareSuccess {
				return result
			}
			statement.SelectColumns = columns
			return PrepareSuccess
		}

		fields := strings.Fields(input)
		if len(fields) == 1 {
			statement.SelectColumns = schema.Columns
			return PrepareSuccess
		}
		if len(fields) != 2 {
			return PrepareSyntaxError
		}

		key, err := strconv.ParseInt(fields[1], 10, 32)
		if err != nil {
			return PrepareSyntaxError
		}
		primaryKeyColumn, ok := schema.PrimaryKeyColumn()
		if !ok || !primaryKeyColumn.ValidateIntegerValue(key) {
			return PrepareNegativeID
		}

		selectByKey := uint32(key)
		statement.SelectByKey = &selectByKey
		statement.SelectColumns = schema.Columns
		return PrepareSuccess
	}

	return PrepareUnrecognizedStatement
}

func parseSelectStatement(input string, schema TableSchema) ([]Column, PrepareResult) {
	trimmed := strings.TrimSpace(input)
	trimmed = strings.TrimSuffix(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)

	const selectPrefix = "select "
	if !strings.HasPrefix(strings.ToLower(trimmed), selectPrefix) {
		return nil, PrepareSyntaxError
	}

	body := strings.TrimSpace(trimmed[len(selectPrefix):])
	fromIndex := findSelectFromIndex(body)
	if fromIndex < 0 {
		return nil, PrepareSyntaxError
	}

	columnList := strings.TrimSpace(body[:fromIndex])
	tableName := strings.TrimSpace(body[fromIndex+len(" from "):])
	if columnList == "" || tableName == "" || !strings.EqualFold(tableName, schema.Name) {
		return nil, PrepareSyntaxError
	}

	if columnList == "*" {
		return schema.Columns, PrepareSuccess
	}

	columnNames, ok := splitSQLList(columnList)
	if !ok {
		return nil, PrepareSyntaxError
	}
	columns := make([]Column, 0, len(columnNames))
	for _, columnName := range columnNames {
		columnName = strings.TrimSpace(columnName)
		if columnName == "" || columnName == "*" {
			return nil, PrepareSyntaxError
		}
		column, ok := schema.Column(columnName)
		if !ok {
			return nil, PrepareSyntaxError
		}
		columns = append(columns, column)
	}

	return columns, PrepareSuccess
}

func findSelectFromIndex(body string) int {
	lower := strings.ToLower(body)
	inString := false
	for i := 0; i <= len(lower)-len(" from "); i++ {
		if body[i] == '\'' {
			if inString && i+1 < len(body) && body[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if !inString && strings.HasPrefix(lower[i:], " from ") {
			return i
		}
	}

	return -1
}

func parseColumnValue(field insertField, column Column) (Value, PrepareResult) {
	if !field.Quoted && strings.EqualFold(field.Value, "null") {
		if column.NotNull || column.PrimaryKey {
			return Value{}, PrepareConstraintViolation
		}
		return Value{StorageClass: StorageNull}, PrepareSuccess
	}

	switch column.Affinity {
	case AffinityInteger:
		integer, err := strconv.ParseInt(field.Value, 10, 32)
		if err != nil {
			return Value{}, PrepareSyntaxError
		}
		if !column.ValidateIntegerValue(integer) {
			return Value{}, PrepareNegativeID
		}
		return Value{StorageClass: StorageInteger, Integer: integer}, PrepareSuccess
	case AffinityReal:
		real, err := strconv.ParseFloat(field.Value, 64)
		if err != nil {
			return Value{}, PrepareSyntaxError
		}
		return Value{StorageClass: StorageReal, Real: real}, PrepareSuccess
	case AffinityText:
		if !column.ValidateTextValue(field.Value) {
			return Value{}, PrepareStringTooLong
		}
		return Value{StorageClass: StorageText, Text: field.Value}, PrepareSuccess
	default:
		return Value{}, PrepareSyntaxError
	}
}

// insertステートメントを実行し、B-Tree内の適切な位置へ行を追加する。
func executeInsert(statement *Statement, table *Table) ExecuteResult {
	keyToInsert, ok := rowKey(statement.RowToInsert, table.Schema)
	if !ok {
		return ExecuteConstraintViolation
	}
	cursor := tableFind(table, keyToInsert)
	leafNode := getPage(table.Pager, cursor.PageNum)
	numCells := leafNodeNumCells(leafNode)
	if cursor.CellNum < numCells {
		keyAtIndex := leafNodeKey(leafNode, cursor.CellNum)
		if keyAtIndex == keyToInsert {
			return ExecuteDuplicateKey
		}
	}
	if violatesUniqueConstraint(statement.RowToInsert, table) {
		return ExecuteConstraintViolation
	}

	leafNodeInsert(cursor, keyToInsert, statement.RowToInsert)

	return ExecuteSuccess
}

func violatesUniqueConstraint(row Row, table *Table) bool {
	for _, column := range table.Schema.Columns {
		if !column.Unique || column.PrimaryKey {
			continue
		}

		value := rowValue(row, column)
		if value.StorageClass == StorageNull {
			continue
		}
		cursor := tableStart(table)
		for !cursor.EndOfTable {
			existingRow := deserializeRow(cursorValue(cursor), table.Schema)
			if valuesEqual(rowValue(existingRow, column), value) {
				return true
			}
			cursorAdvance(cursor)
		}
	}

	return false
}

func valuesEqual(left Value, right Value) bool {
	if left.StorageClass != right.StorageClass {
		return false
	}

	switch left.StorageClass {
	case StorageInteger:
		return left.Integer == right.Integer
	case StorageReal:
		return left.Real == right.Real
	case StorageText:
		return left.Text == right.Text
	case StorageNull:
		return true
	default:
		return false
	}
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
	columns := statement.SelectColumns
	if len(columns) == 0 {
		columns = table.Schema.Columns
	}
	if statement.SelectByKey != nil {
		cursor := tableFind(table, *statement.SelectByKey)
		node := getPage(table.Pager, cursor.PageNum)
		if cursor.CellNum < leafNodeNumCells(node) && leafNodeKey(node, cursor.CellNum) == *statement.SelectByKey {
			printColumns(deserializeRow(cursorValue(cursor), table.Schema), columns, out)
		}
		return ExecuteSuccess
	}

	cursor := tableStart(table)
	for !cursor.EndOfTable {
		printColumns(deserializeRow(cursorValue(cursor), table.Schema), columns, out)
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
