package main

import (
	"bytes"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// prepareCreateTable はCREATE TABLE文を解析し、実行用Statementへ格納する。
// inputにはユーザー入力のSQL全体を渡し、statementには解析結果のスキーマと置換指定を書き込む。
// 戻り値は解析成功、構文エラー、行サイズ超過などの準備段階の結果を表す。
func prepareCreateTable(input string, statement *Statement) PrepareResult {
	schema, replace, ok := parseCreateTableStatement(input)
	if !ok {
		return PrepareSyntaxError
	}
	if schema.SerializedRowSize() > leafNodeMaxPayloadSize {
		return PrepareRowTooLarge
	}

	statement.Type = StatementCreateTable
	statement.Schema = schema
	statement.ReplaceTable = replace
	return PrepareSuccess
}

// prepareAlterTable はALTER TABLE ... ADD COLUMN文を解析する。
// tableから対象テーブルの現スキーマを参照し、追加可能なNULL許容カラムだけをStatementに保持する。
// 戻り値は対象テーブル・カラム定義・制約の検査結果をPrepareResultで返す。
func prepareAlterTable(input string, statement *Statement, table *Table) PrepareResult {
	trimmed := strings.TrimSpace(input)
	trimmed = strings.TrimSuffix(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)

	const alterPrefix = "alter table "
	if !strings.HasPrefix(strings.ToLower(trimmed), alterPrefix) {
		return PrepareSyntaxError
	}

	body := strings.TrimSpace(trimmed[len(alterPrefix):])
	addColumnIndex := findTopLevelClauseIndex(body, " add column ")
	if addColumnIndex < 0 {
		return PrepareSyntaxError
	}

	tableName := strings.TrimSpace(body[:addColumnIndex])
	if !isIdentifier(tableName) {
		return PrepareSyntaxError
	}
	definition, ok := tableDefinition(table, tableName)
	if !ok {
		return PrepareSyntaxError
	}

	columnInput := strings.TrimSpace(body[addColumnIndex+len(" add column "):])
	column, ok := parseColumnDefinition(columnInput)
	if !ok {
		return PrepareSyntaxError
	}
	if _, exists := definition.Schema.Column(column.Name); exists {
		return PrepareSyntaxError
	}
	if column.PrimaryKey || column.NotNull {
		return PrepareConstraintViolation
	}

	updatedSchema := definition.Schema
	updatedSchema.Columns = append(append([]Column(nil), updatedSchema.Columns...), column)
	if !updatedSchema.IsUsable() {
		return PrepareSyntaxError
	}
	if updatedSchema.SerializedRowSize() > leafNodeMaxPayloadSize {
		return PrepareRowTooLarge
	}

	statement.Type = StatementAlterTable
	statement.TargetTable = definition.Schema.Name
	statement.AlterColumn = column
	return PrepareSuccess
}

// prepareInsert はINSERT入力をRow付きのステートメントへ変換する。
// inputをトークン化し、tableに登録された対象スキーマへ合わせて各値をValueへ変換する。
// 戻り値は構文、型、制約、サイズの検査結果を表す。
func prepareInsert(input string, statement *Statement, table *Table) PrepareResult {
	statement.Type = StatementInsert

	fields, ok := parseInsertFields(input)
	if !ok {
		return PrepareSyntaxError
	}
	targetTable := table.Schema.Name
	valueOffset := 1
	if len(fields) >= 4 &&
		!fields[1].Quoted &&
		!fields[2].Quoted &&
		strings.EqualFold(fields[1].Value, "into") {
		targetTable = fields[2].Value
		valueOffset = 3
		if !strings.EqualFold(fields[valueOffset].Value, "values") {
			return PrepareSyntaxError
		}
		valueOffset++
	}
	definition, ok := tableDefinition(table, targetTable)
	if !ok {
		return PrepareSyntaxError
	}
	schema := definition.Schema
	if len(fields) != len(schema.Columns)+1 {
		if valueOffset != 1 && len(fields)-valueOffset != len(schema.Columns) {
			return PrepareSyntaxError
		}
		if valueOffset == 1 {
			return PrepareSyntaxError
		}
	}
	if valueOffset == 1 && len(fields) != len(schema.Columns)+1 {
		return PrepareSyntaxError
	}

	statement.TargetTable = schema.Name
	statement.RowToInsert = Row{
		Values: make(map[string]Value, len(schema.Columns)),
	}

	for i, column := range schema.Columns {
		value, result := parseColumnValue(fields[i+valueOffset], column)
		if result != PrepareSuccess {
			return result
		}

		statement.RowToInsert.Values[column.Name] = value
	}

	return PrepareSuccess
}

// prepareUpdate はUPDATE文を解析し、SET句とWHERE句をStatementへ格納する。
// tableは対象テーブルの存在確認とカラム解決に使う。
// 戻り値はSET句の値変換、WHERE条件解析、対象テーブル検査の結果を表す。
func prepareUpdate(input string, statement *Statement, table *Table) PrepareResult {
	statement.Type = StatementUpdate

	trimmed := strings.TrimSpace(input)
	trimmed = strings.TrimSuffix(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)

	const updatePrefix = "update "
	if !strings.HasPrefix(strings.ToLower(trimmed), updatePrefix) {
		return PrepareSyntaxError
	}

	body := strings.TrimSpace(trimmed[len(updatePrefix):])
	setIndex := findTopLevelClauseIndex(body, " set ")
	if setIndex < 0 {
		return PrepareSyntaxError
	}

	tableName := strings.TrimSpace(body[:setIndex])
	if !isIdentifier(tableName) {
		return PrepareSyntaxError
	}
	definition, ok := tableDefinition(table, tableName)
	if !ok {
		return PrepareSyntaxError
	}

	setWhereInput := strings.TrimSpace(body[setIndex+len(" set "):])
	if setWhereInput == "" {
		return PrepareSyntaxError
	}

	whereIndex := findSelectWhereIndex(setWhereInput)
	setInput := setWhereInput
	whereInput := ""
	if whereIndex >= 0 {
		setInput = strings.TrimSpace(setWhereInput[:whereIndex])
		whereInput = strings.TrimSpace(setWhereInput[whereIndex+len(" where "):])
		if whereInput == "" {
			return PrepareSyntaxError
		}
	}
	if setInput == "" {
		return PrepareSyntaxError
	}

	resolver := newColumnResolver(definition.Schema, []TableReference{{Name: definition.Schema.Name, Schema: definition.Schema, RootPageNum: definition.RootPageNum}}, false)
	assignments, result := parseUpdateAssignments(setInput, resolver)
	if result != PrepareSuccess {
		return result
	}

	var where WhereExpression
	if whereInput != "" {
		where, result = parseWhereExpression(whereInput, resolver)
		if result != PrepareSuccess {
			return result
		}
	}

	statement.TargetTable = definition.Schema.Name
	statement.UpdateAssignments = assignments
	statement.UpdateWhere = where
	return PrepareSuccess
}

// prepareDelete はDELETE FROM文を解析し、対象テーブルとWHERE句をStatementへ格納する。
// WHERE句が省略された場合はゼロ値のWhereExpressionを保持し、実行時に全行削除として扱う。
// 戻り値は対象テーブルやWHERE条件の解析結果を表す。
func prepareDelete(input string, statement *Statement, table *Table) PrepareResult {
	statement.Type = StatementDelete

	trimmed := strings.TrimSpace(input)
	trimmed = strings.TrimSuffix(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)

	const deletePrefix = "delete from "
	if !strings.HasPrefix(strings.ToLower(trimmed), deletePrefix) {
		return PrepareSyntaxError
	}

	body := strings.TrimSpace(trimmed[len(deletePrefix):])
	if body == "" {
		return PrepareSyntaxError
	}

	whereIndex := findSelectWhereIndex(body)
	tableName := body
	whereInput := ""
	if whereIndex >= 0 {
		tableName = strings.TrimSpace(body[:whereIndex])
		whereInput = strings.TrimSpace(body[whereIndex+len(" where "):])
		if whereInput == "" {
			return PrepareSyntaxError
		}
	}
	tableName = strings.TrimSpace(tableName)
	if !isIdentifier(tableName) {
		return PrepareSyntaxError
	}

	definition, ok := tableDefinition(table, tableName)
	if !ok {
		return PrepareSyntaxError
	}

	resolver := newColumnResolver(definition.Schema, []TableReference{{Name: definition.Schema.Name, Schema: definition.Schema, RootPageNum: definition.RootPageNum}}, false)
	var where WhereExpression
	if whereInput != "" {
		var result PrepareResult
		where, result = parseWhereExpression(whereInput, resolver)
		if result != PrepareSuccess {
			return result
		}
	}

	statement.TargetTable = definition.Schema.Name
	statement.DeleteWhere = where
	return PrepareSuccess
}

// parseUpdateAssignments はUPDATEのSET句を複数代入へ分解する。
// inputは "name = 'Alice', height = 160" のようなSET句本体で、resolverはカラム名解決に使う。
// 戻り値は更新カラムと値の一覧、または構文・制約エラーを表すPrepareResultを返す。
func parseUpdateAssignments(input string, resolver columnResolver) ([]UpdateAssignment, PrepareResult) {
	assignmentInputs, ok := splitSQLList(input)
	if !ok || len(assignmentInputs) == 0 {
		return nil, PrepareSyntaxError
	}

	assignments := make([]UpdateAssignment, 0, len(assignmentInputs))
	assignedColumns := map[string]struct{}{}
	for _, assignmentInput := range assignmentInputs {
		columnName, valueInput, ok := splitUpdateAssignment(assignmentInput)
		if !ok {
			return nil, PrepareSyntaxError
		}
		column, ok := resolver.Column(columnName)
		if !ok {
			return nil, PrepareSyntaxError
		}
		normalizedName := strings.ToLower(column.Name)
		if _, exists := assignedColumns[normalizedName]; exists {
			return nil, PrepareSyntaxError
		}
		valueField, ok := parseUpdateValue(valueInput)
		if !ok {
			return nil, PrepareSyntaxError
		}
		value, result := parseColumnValue(valueField, column)
		if result != PrepareSuccess {
			return nil, result
		}

		assignedColumns[normalizedName] = struct{}{}
		assignments = append(assignments, UpdateAssignment{Column: column, Value: value})
	}

	return assignments, PrepareSuccess
}

// splitUpdateAssignment は1つの代入式をカラム名と値文字列へ分割する。
// 戻り値のboolは、トップレベルの '=' があり左右が空でない場合だけtrueになる。
func splitUpdateAssignment(input string) (string, string, bool) {
	index := findTopLevelAssignmentOperator(input)
	if index < 0 {
		return "", "", false
	}
	columnName := strings.TrimSpace(input[:index])
	valueInput := strings.TrimSpace(input[index+1:])
	if columnName == "" || valueInput == "" {
		return "", "", false
	}

	return columnName, valueInput, true
}

// findTopLevelAssignmentOperator は引用符や括弧の内側を除いて代入用 '=' の位置を探す。
// 戻り値は見つかったバイト位置で、見つからない場合や括弧が壊れている場合は-1を返す。
func findTopLevelAssignmentOperator(input string) int {
	inString := false
	depth := 0
	for i := 0; i < len(input); i++ {
		if input[i] == '\'' {
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch input[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return -1
			}
		case '=':
			if depth == 0 {
				return i
			}
		}
	}
	if inString || depth != 0 {
		return -1
	}

	return -1
}

// parseUpdateValue はUPDATEの右辺値をINSERTと同じ値トークンとして解析する。
// 戻り値のboolは、右辺が単一の値として解釈できた場合だけtrueになる。
func parseUpdateValue(input string) (insertField, bool) {
	fields, ok := parseInsertFields(input)
	if !ok || len(fields) != 1 {
		return insertField{}, false
	}

	return fields[0], true
}

type insertField struct {
	Value  string
	Quoted bool
}

// parseInsertFields は空白区切りの値列を、引用文字列を考慮してinsertFieldへ分解する。
// 戻り値のboolは、未終端文字列や不正な引用符がない場合だけtrueになる。
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

// parseSQLStringLiteral はstart位置の単一引用符からSQL文字列リテラルを読む。
// 戻り値は展開済み文字列、リテラル直後の位置、解析成功可否を返す。
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

// mysqlEscapedByte はMySQL風のバックスラッシュエスケープを1バイトへ変換する。
// 戻り値のboolは、既知のエスケープ文字だった場合だけtrueになる。
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

// parseCreateTable はcreate table入力からテーブル名とカラム定義を取り出す簡易ラッパー。
// 戻り値のboolは、CREATE TABLEとして正しく解析できた場合だけtrueになる。
func parseCreateTable(input string) (TableSchema, bool) {
	schema, _, ok := parseCreateTableStatement(input)
	return schema, ok
}

// parseCreateTableStatement はCREATE TABLE / CREATE OR REPLACE TABLE文を解析する。
// 戻り値はスキーマ、OR REPLACE指定の有無、解析成功可否の順で返す。
func parseCreateTableStatement(input string) (TableSchema, bool, bool) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)
	prefix := "create table"
	replace := false
	if strings.HasPrefix(lower, "create or replace table") {
		prefix = "create or replace table"
		replace = true
	}
	if !strings.HasPrefix(lower, prefix) {
		return TableSchema{}, false, false
	}

	openParen := strings.Index(trimmed, "(")
	closeParen := strings.LastIndex(trimmed, ")")
	if openParen < 0 || closeParen < openParen {
		return TableSchema{}, false, false
	}
	if strings.TrimSpace(trimmed[closeParen+1:]) != "" {
		return TableSchema{}, false, false
	}

	tableName := strings.TrimSpace(trimmed[len(prefix):openParen])
	if tableName == "" {
		return TableSchema{}, false, false
	}

	definitions, ok := splitSQLList(trimmed[openParen+1 : closeParen])
	if !ok {
		return TableSchema{}, false, false
	}
	columns := make([]Column, 0, len(definitions))
	tablePrimaryKey := ""
	for _, definition := range definitions {
		if isTableConstraint(definition) {
			primaryKey, ok := parseTablePrimaryKeyConstraint(definition)
			if !ok {
				return TableSchema{}, false, false
			}
			if tablePrimaryKey != "" {
				return TableSchema{}, false, false
			}
			tablePrimaryKey = primaryKey
			continue
		}

		column, ok := parseColumnDefinition(definition)
		if !ok {
			return TableSchema{}, false, false
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
			return TableSchema{}, false, false
		}
	}
	applyImplicitIDPrimaryKey(columns)

	schema := TableSchema{
		Name:    tableName,
		Columns: columns,
	}
	if !schema.IsUsable() {
		return TableSchema{}, false, false
	}

	return schema, replace, true
}

// applyImplicitIDPrimaryKey は明示PRIMARY KEYがない場合にinteger型id列を主キー扱いにする。
// columnsは呼び出し元が保持するスライスで、この関数は該当Columnを直接更新する。
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

// splitSQLList はカンマ区切りのSQL断片を、引用符や括弧の内側を保ったまま分割する。
// 戻り値のboolは、空要素や未終端文字列、括弧不整合がない場合だけtrueになる。
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

// parseTablePrimaryKeyConstraint はテーブル制約のPRIMARY KEY列名を取り出す。
// 現時点では単一カラム主キーだけを受け入れ、戻り値のboolで成功可否を返す。
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

// parseColumnDefinition は1カラム分の定義文字列をColumnへ変換する。
// 戻り値のboolは、型名と対応済み制約だけで構成されている場合にtrueになる。
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

// prepareStatement は入力文字列を実行可能なStatementへ変換する入口。
// sourceには*TableまたはTableSchemaを渡し、戻り値でREPLが表示すべき準備結果を返す。
func prepareStatement(input string, statement *Statement, source any) PrepareResult {
	table := statementTable(source)
	schema := table.Schema
	input = strings.TrimSpace(input)

	lowerInput := strings.ToLower(input)
	if strings.HasPrefix(lowerInput, "create table") || strings.HasPrefix(lowerInput, "create or replace table") {
		return prepareCreateTable(input, statement)
	}

	if strings.HasPrefix(lowerInput, "alter table ") {
		return prepareAlterTable(input, statement, table)
	}

	if strings.HasPrefix(input, "insert") {
		return prepareInsert(input, statement, table)
	}

	if strings.HasPrefix(lowerInput, "update ") {
		return prepareUpdate(input, statement, table)
	}

	if strings.HasPrefix(lowerInput, "delete ") {
		return prepareDelete(input, statement, table)
	}

	if strings.HasPrefix(strings.ToLower(input), "select") {
		statement.Type = StatementSelect
		if strings.Contains(strings.ToLower(input), " from ") {
			selectClause, result := parseSelectStatement(input, table)
			if result != PrepareSuccess {
				return result
			}
			statement.SelectColumns = selectClause.Columns
			statement.SelectItems = selectClause.Items
			statement.SelectDistinct = selectClause.Distinct
			statement.SelectFromDual = selectClause.FromDual
			statement.SelectSource = selectClause.Source
			statement.SelectJoin = selectClause.Join
			statement.SelectWhere = selectClause.Where
			statement.SelectGroupBy = selectClause.GroupBy
			statement.SelectHaving = selectClause.Having
			statement.SelectOrderBy = selectClause.OrderBy
			statement.SelectLimit = selectClause.Limit
			statement.SelectOffset = selectClause.Offset
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

// statementTable はprepareStatementが受け取るsourceをTableとして扱える形へ正規化する。
// sourceがTableSchemaの場合はテスト用の一時Tableを作り、戻り値として返す。
func statementTable(source any) *Table {
	switch value := source.(type) {
	case *Table:
		if value.Tables == nil {
			setTableDefinition(value, TableDefinition{Schema: value.Schema, RootPageNum: value.RootPageNum})
		}
		return value
	case TableSchema:
		return &Table{
			RootPageNum: defaultRootPageNum,
			Schema:      value,
			Tables: map[string]TableDefinition{
				normalizeTableName(value.Name): {Schema: value, RootPageNum: defaultRootPageNum},
			},
		}
	default:
		panic("unsupported prepare source")
	}
}

type selectClause struct {
	Columns  []Column
	Items    []SelectItem
	Distinct bool
	FromDual bool
	Source   TableReference
	Join     *JoinClause
	Where    WhereExpression
	GroupBy  []Column
	Having   HavingExpression
	OrderBy  []OrderByClause
	Limit    *uint32
	Offset   *uint32
}

type columnResolver struct {
	Schema         TableSchema
	References     []TableReference
	QualifyColumns bool
}

const (
	dualTableName   = "dual"
	dualColumnName  = "dummy"
	dualColumnValue = "X"
)

// newColumnResolver はSELECT/WHERE/HAVINGで使うカラム名解決器を生成する。
// qualifyColumnsがtrueの場合、JOIN時の衝突回避のために行内キーを "alias.column" 形式へ揃える。
func newColumnResolver(schema TableSchema, references []TableReference, qualifyColumns bool) columnResolver {
	return columnResolver{Schema: schema, References: references, QualifyColumns: qualifyColumns}
}

// Column は入力されたカラム名をスキーマ上のColumnへ解決する。
// 戻り値のboolは、未発見または曖昧な未修飾カラムではfalseになる。
func (resolver columnResolver) Column(name string) (Column, bool) {
	qualifier, columnName, qualified := splitQualifiedName(name)
	if qualified {
		for _, reference := range resolver.References {
			if referenceMatchesQualifier(reference, qualifier) {
				return resolver.resolvedColumn(reference, columnName)
			}
		}
		return Column{}, false
	}

	var resolved Column
	matches := 0
	for _, reference := range resolver.References {
		column, ok := resolver.resolvedColumn(reference, name)
		if ok {
			resolved = column
			matches++
		}
	}
	if matches != 1 {
		return Column{}, false
	}

	return resolved, true
}

func (resolver columnResolver) resolvedColumn(reference TableReference, columnName string) (Column, bool) {
	column, ok := reference.Schema.Column(columnName)
	if !ok {
		return Column{}, false
	}
	if resolver.QualifyColumns {
		column.Name = qualifiedColumnName(reference, column.Name)
	}

	return column, true
}

func referenceMatchesQualifier(reference TableReference, qualifier string) bool {
	return strings.EqualFold(reference.Name, qualifier) ||
		(reference.Alias != "" && strings.EqualFold(reference.Alias, qualifier))
}

func qualifiedColumnName(reference TableReference, columnName string) string {
	return referenceQualifier(reference) + "." + columnName
}

func referenceQualifier(reference TableReference) string {
	if reference.Alias != "" {
		return reference.Alias
	}

	return reference.Name
}

func splitQualifiedName(name string) (string, string, bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 2 {
		return "", name, false
	}
	qualifier := strings.TrimSpace(parts[0])
	columnName := strings.TrimSpace(parts[1])
	if qualifier == "" || columnName == "" {
		return "", "", false
	}

	return qualifier, columnName, true
}

// parseSelectStatement はFROM句を持つSELECT文を句ごとに解析する。
// tableはFROM/JOIN対象の実テーブル解決に使い、戻り値は実行に必要なselectClauseと解析結果を返す。
func parseSelectStatement(input string, table *Table) (selectClause, PrepareResult) {
	trimmed := strings.TrimSpace(input)
	trimmed = strings.TrimSuffix(trimmed, ";")
	trimmed = strings.TrimSpace(trimmed)

	const selectPrefix = "select "
	if !strings.HasPrefix(strings.ToLower(trimmed), selectPrefix) {
		return selectClause{}, PrepareSyntaxError
	}

	body := strings.TrimSpace(trimmed[len(selectPrefix):])
	fromIndex := findSelectFromIndex(body)
	if fromIndex < 0 {
		return selectClause{}, PrepareSyntaxError
	}

	columnList := strings.TrimSpace(body[:fromIndex])
	distinct := false
	if strings.HasPrefix(strings.ToLower(columnList), "distinct ") {
		distinct = true
		columnList = strings.TrimSpace(columnList[len("distinct "):])
	}
	tableWhereOrderAndLimit := strings.TrimSpace(body[fromIndex+len(" from "):])
	limitIndex := findSelectLimitIndex(tableWhereOrderAndLimit)
	tableWhereAndOrder := tableWhereOrderAndLimit
	var limit *uint32
	var offset *uint32
	if limitIndex >= 0 {
		tableWhereAndOrder = strings.TrimSpace(tableWhereOrderAndLimit[:limitIndex])
		limitInput := strings.TrimSpace(tableWhereOrderAndLimit[limitIndex+len(" limit "):])
		parsedLimit, parsedOffset, result := parseLimitClause(limitInput)
		if result != PrepareSuccess {
			return selectClause{}, result
		}
		limit = &parsedLimit
		offset = parsedOffset
	}

	orderByIndex := findSelectOrderByIndex(tableWhereAndOrder)
	tableAndWhere := tableWhereAndOrder
	orderByInput := ""
	var orderBy []OrderByClause
	if orderByIndex >= 0 {
		tableAndWhere = strings.TrimSpace(tableWhereAndOrder[:orderByIndex])
		orderByInput = strings.TrimSpace(tableWhereAndOrder[orderByIndex+len(" order by "):])
		if orderByInput == "" {
			return selectClause{}, PrepareSyntaxError
		}
	}

	havingIndex := findSelectHavingIndex(tableAndWhere)
	tableWhereAndGroup := tableAndWhere
	havingInput := ""
	var having HavingExpression
	if havingIndex >= 0 {
		tableWhereAndGroup = strings.TrimSpace(tableAndWhere[:havingIndex])
		havingInput = strings.TrimSpace(tableAndWhere[havingIndex+len(" having "):])
		if havingInput == "" {
			return selectClause{}, PrepareSyntaxError
		}
	}

	groupByIndex := findSelectGroupByIndex(tableWhereAndGroup)
	tableAndWhereOnly := tableWhereAndGroup
	groupByInput := ""
	var groupBy []Column
	if groupByIndex >= 0 {
		tableAndWhereOnly = strings.TrimSpace(tableWhereAndGroup[:groupByIndex])
		groupByInput = strings.TrimSpace(tableWhereAndGroup[groupByIndex+len(" group by "):])
	}

	whereIndex := findSelectWhereIndex(tableAndWhereOnly)
	tableReference := tableAndWhereOnly
	whereInput := ""
	var where WhereExpression
	if whereIndex >= 0 {
		tableReference = strings.TrimSpace(tableAndWhereOnly[:whereIndex])
		whereInput = strings.TrimSpace(tableAndWhereOnly[whereIndex+len(" where "):])
		if whereInput == "" {
			return selectClause{}, PrepareSyntaxError
		}
	}

	fromSource, result := parseFromSource(tableReference, table)
	if result != PrepareSuccess {
		return selectClause{}, result
	}
	fromDual := fromSource.FromDual
	sourceSchema := fromSource.Schema
	resolver := fromSource.Resolver
	if columnList == "" {
		return selectClause{}, PrepareSyntaxError
	}

	if whereInput != "" {
		expression, result := parseWhereExpression(whereInput, resolver)
		if result != PrepareSuccess {
			return selectClause{}, result
		}
		where = expression
	}
	if groupByInput != "" {
		columns, result := parseGroupByClause(groupByInput, resolver)
		if result != PrepareSuccess {
			return selectClause{}, result
		}
		groupBy = columns
	}
	if havingInput != "" {
		expression, result := parseHavingExpression(havingInput, resolver)
		if result != PrepareSuccess {
			return selectClause{}, result
		}
		having = expression
	}
	if orderByInput != "" {
		clauses, result := parseOrderByClause(orderByInput, resolver)
		if result != PrepareSuccess {
			return selectClause{}, result
		}
		orderBy = clauses
	}

	if columnList == "*" {
		if fromSource.Join != nil || len(groupBy) > 0 || having.Kind != WhereExpressionNone {
			return selectClause{}, PrepareSyntaxError
		}
		return selectClause{Columns: sourceSchema.Columns, Items: selectItemsFromColumns(sourceSchema.Columns), Distinct: distinct, FromDual: fromDual, Source: fromSource.Source, Join: fromSource.Join, Where: where, GroupBy: groupBy, Having: having, OrderBy: orderBy, Limit: limit, Offset: offset}, PrepareSuccess
	}

	itemInputs, ok := splitSQLList(columnList)
	if !ok {
		return selectClause{}, PrepareSyntaxError
	}
	columns := make([]Column, 0, len(itemInputs))
	items := make([]SelectItem, 0, len(itemInputs))
	for _, itemInput := range itemInputs {
		itemInput = strings.TrimSpace(itemInput)
		if itemInput == "" || itemInput == "*" {
			return selectClause{}, PrepareSyntaxError
		}
		expression, alias, expressionInput, result := parseSelectItem(itemInput, resolver)
		if result != PrepareSuccess {
			return selectClause{}, PrepareSyntaxError
		}
		header := alias
		if expression.Kind == ValueExpressionColumn {
			columns = append(columns, expression.Column)
			if header == "" {
				header = expression.Column.Name
			}
			items = append(items, SelectItem{Header: header, Expression: expression})
			continue
		}

		if header == "" {
			header = expressionInput
		}
		items = append(items, SelectItem{Header: header, Expression: expression})
	}
	if !selectItemsAreValidForGrouping(items, groupBy) {
		return selectClause{}, PrepareSyntaxError
	}
	if having.Kind != WhereExpressionNone {
		if len(groupBy) == 0 && !selectItemsContainAggregate(items) {
			return selectClause{}, PrepareSyntaxError
		}
		if !havingExpressionIsValidForGrouping(having, groupBy) {
			return selectClause{}, PrepareSyntaxError
		}
	}

	return selectClause{Columns: columns, Items: items, Distinct: distinct, FromDual: fromDual, Source: fromSource.Source, Join: fromSource.Join, Where: where, GroupBy: groupBy, Having: having, OrderBy: orderBy, Limit: limit, Offset: offset}, PrepareSuccess
}

type fromSource struct {
	Schema   TableSchema
	FromDual bool
	Source   TableReference
	Join     *JoinClause
	Resolver columnResolver
}

// parseFromSource はSELECTのFROM句を単一テーブル、dual、JOINのいずれかとして解釈する。
// 戻り値のfromSourceには、SELECT句やWHERE句で使うスキーマとカラム解決器を含める。
func parseFromSource(input string, table *Table) (fromSource, PrepareResult) {
	if joinIndex := findSelectJoinIndex(input); joinIndex >= 0 {
		return parseJoinSource(input, joinIndex, table)
	}

	reference, ok := parseTableReference(input)
	if !ok {
		return fromSource{}, PrepareSyntaxError
	}
	fromDual := strings.EqualFold(reference.Name, dualTableName)
	sourceSchema := table.Schema
	if fromDual {
		sourceSchema = DualTableSchema()
		reference.Schema = sourceSchema
	} else {
		definition, ok := tableDefinition(table, reference.Name)
		if !ok {
			return fromSource{}, PrepareSyntaxError
		}
		sourceSchema = definition.Schema
		reference.Schema = definition.Schema
		reference.RootPageNum = definition.RootPageNum
	}
	if !fromDual && reference.RootPageNum == metadataPageNum {
		return fromSource{}, PrepareSyntaxError
	}

	return fromSource{
		Schema:   sourceSchema,
		FromDual: fromDual,
		Source:   reference,
		Resolver: newColumnResolver(sourceSchema, []TableReference{reference}, false),
	}, PrepareSuccess
}

// parseJoinSource はINNER JOIN相当のFROM句を左右テーブルとON条件へ分解する。
// joinIndexはトップレベルのJOIN位置で、戻り値はJOIN用の複合スキーマとON条件を含む。
func parseJoinSource(input string, joinIndex int, table *Table) (fromSource, PrepareResult) {
	leftInput := strings.TrimSpace(input[:joinIndex])
	if strings.HasSuffix(strings.ToLower(leftInput), " inner") {
		leftInput = strings.TrimSpace(leftInput[:len(leftInput)-len(" inner")])
	}
	rightAndOn := strings.TrimSpace(input[joinIndex+len(" join "):])
	onIndex := findSelectOnIndex(rightAndOn)
	if leftInput == "" || onIndex < 0 {
		return fromSource{}, PrepareSyntaxError
	}
	rightInput := strings.TrimSpace(rightAndOn[:onIndex])
	onInput := strings.TrimSpace(rightAndOn[onIndex+len(" on "):])
	if rightInput == "" || onInput == "" {
		return fromSource{}, PrepareSyntaxError
	}

	leftReference, ok := parseTableReference(leftInput)
	if !ok {
		return fromSource{}, PrepareSyntaxError
	}
	rightReference, ok := parseTableReference(rightInput)
	if !ok {
		return fromSource{}, PrepareSyntaxError
	}
	leftDefinition, ok := tableDefinition(table, leftReference.Name)
	if !ok {
		return fromSource{}, PrepareSyntaxError
	}
	rightDefinition, ok := tableDefinition(table, rightReference.Name)
	if !ok {
		return fromSource{}, PrepareSyntaxError
	}
	leftReference.Schema = leftDefinition.Schema
	leftReference.RootPageNum = leftDefinition.RootPageNum
	rightReference.Schema = rightDefinition.Schema
	rightReference.RootPageNum = rightDefinition.RootPageNum
	if strings.EqualFold(referenceQualifier(leftReference), referenceQualifier(rightReference)) {
		return fromSource{}, PrepareSyntaxError
	}

	resolver := newColumnResolver(table.Schema, []TableReference{leftReference, rightReference}, true)
	onExpression, result := parseHavingExpression(onInput, resolver)
	if result != PrepareSuccess || havingExpressionContainsAggregate(onExpression) {
		return fromSource{}, PrepareSyntaxError
	}
	join := &JoinClause{Left: leftReference, Right: rightReference, On: onExpression}

	return fromSource{
		Schema:   leftDefinition.Schema,
		FromDual: false,
		Join:     join,
		Resolver: resolver,
	}, PrepareSuccess
}

// DualTableSchema はOracleのDUALに相当する1行仮想テーブルのスキーマを返す。
// 戻り値はSELECT式だけを評価したい場合のFROMソースとして使う。
func DualTableSchema() TableSchema {
	return TableSchema{
		Name: dualTableName,
		Columns: []Column{
			{
				Name:         dualColumnName,
				DeclaredType: "TEXT",
				Affinity:     AffinityText,
				MaxLength:    1,
			},
		},
	}
}

// dualRow はDUAL表の唯一の行を返す。
// 戻り値のdummy列にはOracle風に "X" を入れる。
func dualRow() Row {
	return Row{
		Values: map[string]Value{
			dualColumnName: {
				StorageClass: StorageText,
				Text:         dualColumnValue,
			},
		},
	}
}

// parseTableReference は "table" または "table alias" 形式の参照を解析する。
// 戻り値のboolは、テーブル名と任意エイリアスが識別子として妥当な場合だけtrueになる。
func parseTableReference(input string) (TableReference, bool) {
	fields := strings.Fields(strings.TrimSpace(input))
	switch len(fields) {
	case 1:
		return TableReference{Name: fields[0]}, true
	case 2:
		if strings.EqualFold(fields[1], "as") || !isIdentifier(fields[1]) {
			return TableReference{}, false
		}
		return TableReference{Name: fields[0], Alias: fields[1]}, true
	case 3:
		if !strings.EqualFold(fields[1], "as") || !isIdentifier(fields[2]) {
			return TableReference{}, false
		}
		return TableReference{Name: fields[0], Alias: fields[2]}, true
	default:
		return TableReference{}, false
	}
}

// parseSelectItem はSELECT句の1要素を値式と表示ヘッダへ変換する。
// 戻り値は式、ヘッダ名、式本体文字列、解析結果の順で返す。
func parseSelectItem(input string, resolver columnResolver) (ValueExpression, string, string, PrepareResult) {
	input = strings.TrimSpace(input)
	if input == "" {
		return ValueExpression{}, "", "", PrepareSyntaxError
	}
	if expression, alias, expressionInput, ok, result := parseExplicitSelectItemAlias(input, resolver); ok {
		return expression, alias, expressionInput, result
	}
	if expression, result := parseValueExpression(input, resolver); result == PrepareSuccess {
		return expression, "", input, PrepareSuccess
	}

	expressionInput, alias, ok := parseImplicitSelectItemAlias(input)
	if !ok {
		return ValueExpression{}, "", "", PrepareSyntaxError
	}
	expression, result := parseValueExpression(expressionInput, resolver)
	if result != PrepareSuccess {
		return ValueExpression{}, "", "", result
	}

	return expression, alias, expressionInput, PrepareSuccess
}

func parseExplicitSelectItemAlias(input string, resolver columnResolver) (ValueExpression, string, string, bool, PrepareResult) {
	input = strings.TrimSpace(input)
	if index := findTopLevelKeyword(input, "as"); index >= 0 {
		expression := strings.TrimSpace(input[:index])
		alias := strings.TrimSpace(input[index+len(" as "):])
		if expression == "" || !isIdentifier(alias) {
			return ValueExpression{}, "", "", true, PrepareSyntaxError
		}
		parsed, result := parseValueExpression(expression, resolver)
		if result != PrepareSuccess {
			return ValueExpression{}, "", "", true, result
		}
		return parsed, alias, expression, true, PrepareSuccess
	}

	return ValueExpression{}, "", "", false, PrepareSuccess
}

func parseImplicitSelectItemAlias(input string) (string, string, bool) {
	index := lastTopLevelWhitespace(input)
	if index < 0 {
		return "", "", false
	}
	expression := strings.TrimSpace(input[:index])
	alias := strings.TrimSpace(input[index:])
	if expression == "" || !isIdentifier(alias) || isSelectAliasReserved(alias) {
		return "", "", false
	}

	return expression, alias, true
}

func lastTopLevelWhitespace(input string) int {
	inString := false
	depth := 0
	last := -1
	for i := 0; i < len(input); i++ {
		if input[i] == '\'' {
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch input[i] {
		case '(':
			depth++
			continue
		case ')':
			depth--
			if depth < 0 {
				return -1
			}
			continue
		}
		if depth == 0 && unicode.IsSpace(rune(input[i])) {
			last = i
		}
	}

	return last
}

func isIdentifier(input string) bool {
	if input == "" {
		return false
	}
	for i, value := range input {
		if i == 0 {
			if !unicode.IsLetter(value) && value != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_' {
			return false
		}
	}

	return true
}

func isSelectAliasReserved(input string) bool {
	return strings.EqualFold(input, "where") ||
		strings.EqualFold(input, "group") ||
		strings.EqualFold(input, "having") ||
		strings.EqualFold(input, "order") ||
		strings.EqualFold(input, "limit") ||
		strings.EqualFold(input, "and") ||
		strings.EqualFold(input, "or")
}

func selectItemsFromColumns(columns []Column) []SelectItem {
	items := make([]SelectItem, 0, len(columns))
	for _, column := range columns {
		items = append(items, SelectItem{
			Header: column.Name,
			Expression: ValueExpression{
				Kind:   ValueExpressionColumn,
				Column: column,
			},
		})
	}

	return items
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

func findSelectWhereIndex(body string) int {
	lower := strings.ToLower(body)
	inString := false
	for i := 0; i <= len(lower)-len(" where "); i++ {
		if body[i] == '\'' {
			if inString && i+1 < len(body) && body[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if !inString && strings.HasPrefix(lower[i:], " where ") {
			return i
		}
	}

	return -1
}

func findSelectJoinIndex(body string) int {
	return findTopLevelClauseIndex(body, " join ")
}

func findSelectOnIndex(body string) int {
	return findTopLevelClauseIndex(body, " on ")
}

func findSelectOrderByIndex(body string) int {
	lower := strings.ToLower(body)
	inString := false
	for i := 0; i <= len(lower)-len(" order by "); i++ {
		if body[i] == '\'' {
			if inString && i+1 < len(body) && body[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if !inString && strings.HasPrefix(lower[i:], " order by ") {
			return i
		}
	}

	return -1
}

func findTopLevelClauseIndex(body string, clause string) int {
	lower := strings.ToLower(body)
	inString := false
	depth := 0
	for i := 0; i <= len(lower)-len(clause); i++ {
		if body[i] == '\'' {
			if inString && i+1 < len(body) && body[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch body[i] {
		case '(':
			depth++
			continue
		case ')':
			depth--
			if depth < 0 {
				return -1
			}
			continue
		}
		if depth == 0 && strings.HasPrefix(lower[i:], clause) {
			return i
		}
	}

	return -1
}

func findSelectGroupByIndex(body string) int {
	lower := strings.ToLower(body)
	inString := false
	for i := 0; i <= len(lower)-len(" group by "); i++ {
		if body[i] == '\'' {
			if inString && i+1 < len(body) && body[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if !inString && strings.HasPrefix(lower[i:], " group by ") {
			return i
		}
	}

	return -1
}

func findSelectHavingIndex(body string) int {
	lower := strings.ToLower(body)
	inString := false
	for i := 0; i <= len(lower)-len(" having "); i++ {
		if body[i] == '\'' {
			if inString && i+1 < len(body) && body[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if !inString && strings.HasPrefix(lower[i:], " having ") {
			return i
		}
	}

	return -1
}

func findSelectLimitIndex(body string) int {
	lower := strings.ToLower(body)
	inString := false
	for i := 0; i <= len(lower)-len(" limit "); i++ {
		if body[i] == '\'' {
			if inString && i+1 < len(body) && body[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if !inString && strings.HasPrefix(lower[i:], " limit ") {
			return i
		}
	}

	return -1
}

// parseLimitClause はLIMIT句本体を件数と任意のOFFSETへ変換する。
// 戻り値はlimit値、offset値へのポインタ(nilなら未指定)、解析結果を返す。
func parseLimitClause(input string) (uint32, *uint32, PrepareResult) {
	fields := strings.Fields(input)
	if len(fields) != 1 && len(fields) != 3 {
		return 0, nil, PrepareSyntaxError
	}

	limit, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil {
		return 0, nil, PrepareSyntaxError
	}

	var offset *uint32
	if len(fields) == 3 {
		if !strings.EqualFold(fields[1], "offset") {
			return 0, nil, PrepareSyntaxError
		}
		parsedOffset, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil {
			return 0, nil, PrepareSyntaxError
		}
		offsetValue := uint32(parsedOffset)
		offset = &offsetValue
	}

	return uint32(limit), offset, PrepareSuccess
}

// parseGroupByClause はGROUP BY句のカラム一覧を解決済みColumnへ変換する。
// 戻り値はグループ化カラムの順序付き一覧と解析結果を返す。
func parseGroupByClause(input string, resolver columnResolver) ([]Column, PrepareResult) {
	columnNames, ok := splitSQLList(input)
	if !ok || len(columnNames) == 0 {
		return nil, PrepareSyntaxError
	}

	columns := make([]Column, 0, len(columnNames))
	for _, columnName := range columnNames {
		columnName = strings.TrimSpace(columnName)
		column, ok := resolver.Column(columnName)
		if columnName == "" || !ok {
			return nil, PrepareSyntaxError
		}
		columns = append(columns, column)
	}

	return columns, PrepareSuccess
}

// parseOrderByClause はORDER BY句を複数のソート条件へ変換する。
// 戻り値のスライス順は比較優先順位を表し、各要素にASC/DESCを保持する。
func parseOrderByClause(input string, resolver columnResolver) ([]OrderByClause, PrepareResult) {
	orderInputs, ok := splitSQLList(input)
	if !ok || len(orderInputs) == 0 {
		return nil, PrepareSyntaxError
	}

	clauses := make([]OrderByClause, 0, len(orderInputs))
	for _, orderInput := range orderInputs {
		fields := strings.Fields(orderInput)
		if len(fields) < 1 || len(fields) > 2 {
			return nil, PrepareSyntaxError
		}

		column, ok := resolver.Column(fields[0])
		if !ok {
			return nil, PrepareSyntaxError
		}

		direction := SortAscending
		if len(fields) == 2 {
			switch {
			case strings.EqualFold(fields[1], "asc"):
				direction = SortAscending
			case strings.EqualFold(fields[1], "desc"):
				direction = SortDescending
			default:
				return nil, PrepareSyntaxError
			}
		}

		clauses = append(clauses, OrderByClause{Column: column, Direction: direction})
	}

	return clauses, PrepareSuccess
}

// parseHavingExpression はHAVING句全体を条件式ツリーへ変換する。
// 戻り値は集約式を含められるHavingExpressionと解析結果を返す。
func parseHavingExpression(input string, resolver columnResolver) (HavingExpression, PrepareResult) {
	input = strings.TrimSpace(input)
	if input == "" {
		return HavingExpression{}, PrepareSyntaxError
	}
	return parseHavingOr(input, resolver)
}

func parseHavingOr(input string, resolver columnResolver) (HavingExpression, PrepareResult) {
	index := findTopLevelKeyword(input, "or")
	if index >= 0 {
		left, result := parseHavingOr(strings.TrimSpace(input[:index]), resolver)
		if result != PrepareSuccess {
			return HavingExpression{}, result
		}
		right, result := parseHavingAnd(strings.TrimSpace(input[index+len(" or "):]), resolver)
		if result != PrepareSuccess {
			return HavingExpression{}, result
		}
		leftNode := left
		rightNode := right
		return HavingExpression{Kind: WhereExpressionOr, Left: &leftNode, Right: &rightNode}, PrepareSuccess
	}

	return parseHavingAnd(input, resolver)
}

func parseHavingAnd(input string, resolver columnResolver) (HavingExpression, PrepareResult) {
	index := findTopLevelKeyword(input, "and")
	if index >= 0 {
		left, result := parseHavingAnd(strings.TrimSpace(input[:index]), resolver)
		if result != PrepareSuccess {
			return HavingExpression{}, result
		}
		right, result := parseHavingPrimary(strings.TrimSpace(input[index+len(" and "):]), resolver)
		if result != PrepareSuccess {
			return HavingExpression{}, result
		}
		leftNode := left
		rightNode := right
		return HavingExpression{Kind: WhereExpressionAnd, Left: &leftNode, Right: &rightNode}, PrepareSuccess
	}

	return parseHavingPrimary(input, resolver)
}

func parseHavingPrimary(input string, resolver columnResolver) (HavingExpression, PrepareResult) {
	input = strings.TrimSpace(input)
	if input == "" {
		return HavingExpression{}, PrepareSyntaxError
	}
	if hasWrappingParentheses(input) {
		return parseHavingOr(strings.TrimSpace(input[1:len(input)-1]), resolver)
	}

	condition, result := parseHavingCondition(input, resolver)
	if result != PrepareSuccess {
		return HavingExpression{}, result
	}

	return HavingExpression{Kind: WhereExpressionCondition, Condition: condition}, PrepareSuccess
}

func parseHavingCondition(input string, resolver columnResolver) (HavingCondition, PrepareResult) {
	if index, operator, ok := findTopLevelIsNullOperator(input); ok {
		left, result := parseValueExpression(strings.TrimSpace(input[:index]), resolver)
		if result != PrepareSuccess {
			return HavingCondition{}, result
		}
		return HavingCondition{Left: left, Operator: operator}, PrepareSuccess
	}

	index, operatorText, ok := findTopLevelComparisonOperator(input)
	if !ok {
		return HavingCondition{}, PrepareSyntaxError
	}
	left, result := parseValueExpression(strings.TrimSpace(input[:index]), resolver)
	if result != PrepareSuccess {
		return HavingCondition{}, result
	}
	right, result := parseValueExpression(strings.TrimSpace(input[index+len(operatorText):]), resolver)
	if result != PrepareSuccess {
		return HavingCondition{}, result
	}
	operator, ok := parseWhereComparisonOperator(operatorText)
	if !ok {
		return HavingCondition{}, PrepareSyntaxError
	}

	return HavingCondition{Left: left, Operator: operator, Right: right}, PrepareSuccess
}

func findTopLevelKeyword(input string, keyword string) int {
	lower := strings.ToLower(input)
	target := " " + strings.ToLower(keyword) + " "
	inString := false
	depth := 0
	found := -1
	for i := 0; i <= len(input)-len(target); i++ {
		if input[i] == '\'' {
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		switch input[i] {
		case '(':
			depth++
			continue
		case ')':
			depth--
			if depth < 0 {
				return -1
			}
			continue
		}
		if inString || depth != 0 {
			continue
		}
		if strings.HasPrefix(lower[i:], target) {
			found = i
		}
	}

	return found
}

func findTopLevelIsNullOperator(input string) (int, WhereOperator, bool) {
	index := findTopLevelSuffix(input, " is not null")
	if index >= 0 {
		return index, WhereIsNotNull, true
	}
	index = findTopLevelSuffix(input, " is null")
	if index >= 0 {
		return index, WhereIsNull, true
	}

	return 0, WhereEqual, false
}

func findTopLevelSuffix(input string, suffix string) int {
	lower := strings.ToLower(input)
	if !strings.HasSuffix(lower, suffix) {
		return -1
	}
	index := len(input) - len(suffix)
	if isTopLevelPosition(input, index) {
		return index
	}

	return -1
}

func findTopLevelComparisonOperator(input string) (int, string, bool) {
	inString := false
	depth := 0
	for i := 0; i < len(input); i++ {
		if input[i] == '\'' {
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch input[i] {
		case '(':
			depth++
			continue
		case ')':
			depth--
			if depth < 0 {
				return 0, "", false
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if i+1 < len(input) {
			token := input[i : i+2]
			switch token {
			case "!=", "<>", "<=", ">=":
				return i, token, true
			}
		}
		switch input[i] {
		case '=', '<', '>':
			return i, input[i : i+1], true
		}
	}

	return 0, "", false
}

func hasWrappingParentheses(input string) bool {
	if len(input) < 2 || input[0] != '(' || input[len(input)-1] != ')' {
		return false
	}
	inString := false
	depth := 0
	for i := 0; i < len(input); i++ {
		if input[i] == '\'' {
			if inString && i+1 < len(input) && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch input[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(input)-1 {
				return false
			}
			if depth < 0 {
				return false
			}
		}
	}

	return depth == 0 && !inString
}

func isTopLevelPosition(input string, targetIndex int) bool {
	inString := false
	depth := 0
	for i := 0; i < targetIndex; i++ {
		if input[i] == '\'' {
			if inString && i+1 < targetIndex && input[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch input[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
	}

	return !inString && depth == 0
}

// parseValueExpression はカラム参照、リテラル、集約関数、四則演算を値式ツリーへ変換する。
// resolverはカラム参照の解決に使い、戻り値は式ツリーと解析結果を返す。
func parseValueExpression(input string, resolver columnResolver) (ValueExpression, PrepareResult) {
	tokens, ok := parseValueExpressionTokens(input)
	if !ok || len(tokens) == 0 {
		return ValueExpression{}, PrepareSyntaxError
	}

	parser := valueExpressionParser{Tokens: tokens, Resolver: resolver}
	expression, result := parser.parseAdditive()
	if result != PrepareSuccess || parser.Position != len(tokens) {
		return ValueExpression{}, PrepareSyntaxError
	}
	if expression.Kind == ValueExpressionBinary && !isNumericExpression(expression) {
		return ValueExpression{}, PrepareSyntaxError
	}

	return expression, PrepareSuccess
}

func selectItemsAreValidForGrouping(items []SelectItem, groupBy []Column) bool {
	hasAggregate := false
	for _, item := range items {
		if expressionContainsAggregate(item.Expression) {
			hasAggregate = true
			break
		}
	}
	if !hasAggregate && len(groupBy) == 0 {
		return true
	}

	for _, item := range items {
		if expressionContainsAggregate(item.Expression) {
			continue
		}
		if item.Expression.Kind != ValueExpressionColumn || !columnInList(item.Expression.Column, groupBy) {
			return false
		}
	}

	return true
}

func expressionContainsAggregate(expression ValueExpression) bool {
	switch expression.Kind {
	case ValueExpressionAggregate:
		return true
	case ValueExpressionBinary:
		return (expression.Left != nil && expressionContainsAggregate(*expression.Left)) ||
			(expression.Right != nil && expressionContainsAggregate(*expression.Right))
	default:
		return false
	}
}

func columnInList(column Column, columns []Column) bool {
	for _, candidate := range columns {
		if strings.EqualFold(column.Name, candidate.Name) {
			return true
		}
	}

	return false
}

func havingExpressionIsValidForGrouping(expression HavingExpression, groupBy []Column) bool {
	switch expression.Kind {
	case WhereExpressionNone:
		return true
	case WhereExpressionCondition:
		return havingConditionIsValidForGrouping(expression.Condition, groupBy)
	case WhereExpressionAnd, WhereExpressionOr:
		return expression.Left != nil &&
			expression.Right != nil &&
			havingExpressionIsValidForGrouping(*expression.Left, groupBy) &&
			havingExpressionIsValidForGrouping(*expression.Right, groupBy)
	default:
		return false
	}
}

func havingExpressionContainsAggregate(expression HavingExpression) bool {
	switch expression.Kind {
	case WhereExpressionNone:
		return false
	case WhereExpressionCondition:
		return expressionContainsAggregate(expression.Condition.Left) ||
			expressionContainsAggregate(expression.Condition.Right)
	case WhereExpressionAnd, WhereExpressionOr:
		return (expression.Left != nil && havingExpressionContainsAggregate(*expression.Left)) ||
			(expression.Right != nil && havingExpressionContainsAggregate(*expression.Right))
	default:
		return false
	}
}

func havingConditionIsValidForGrouping(condition HavingCondition, groupBy []Column) bool {
	if !valueExpressionIsValidForHaving(condition.Left, groupBy) {
		return false
	}
	if condition.Operator == WhereIsNull || condition.Operator == WhereIsNotNull {
		return true
	}

	return valueExpressionIsValidForHaving(condition.Right, groupBy)
}

func valueExpressionIsValidForHaving(expression ValueExpression, groupBy []Column) bool {
	switch expression.Kind {
	case ValueExpressionColumn:
		return columnInList(expression.Column, groupBy)
	case ValueExpressionLiteral:
		return true
	case ValueExpressionAggregate:
		if expression.CountAll {
			return true
		}
		return expression.Argument != nil && !expressionContainsAggregate(*expression.Argument)
	case ValueExpressionBinary:
		return expression.Left != nil &&
			expression.Right != nil &&
			valueExpressionIsValidForHaving(*expression.Left, groupBy) &&
			valueExpressionIsValidForHaving(*expression.Right, groupBy)
	default:
		return false
	}
}

func isNumericExpression(expression ValueExpression) bool {
	switch expression.Kind {
	case ValueExpressionColumn:
		return expression.Column.Affinity == AffinityInteger || expression.Column.Affinity == AffinityReal
	case ValueExpressionLiteral:
		return isNumericValue(expression.Value)
	case ValueExpressionAggregate:
		switch expression.Function {
		case AggregateCount, AggregateSum, AggregateAvg:
			return true
		case AggregateMin, AggregateMax:
			return expression.Argument != nil && isNumericExpression(*expression.Argument)
		default:
			return false
		}
	case ValueExpressionBinary:
		if expression.Left == nil || expression.Right == nil {
			return false
		}
		return isNumericExpression(*expression.Left) && isNumericExpression(*expression.Right)
	default:
		return false
	}
}

type valueExpressionParser struct {
	Tokens   []insertField
	Position int
	Resolver columnResolver
}

func (parser *valueExpressionParser) parseAdditive() (ValueExpression, PrepareResult) {
	left, result := parser.parseMultiplicative()
	if result != PrepareSuccess {
		return ValueExpression{}, result
	}

	for {
		operator, ok := parser.matchArithmeticOperator("+", "-")
		if !ok {
			return left, PrepareSuccess
		}
		right, result := parser.parseMultiplicative()
		if result != PrepareSuccess {
			return ValueExpression{}, result
		}
		leftNode := left
		rightNode := right
		left = ValueExpression{
			Kind:     ValueExpressionBinary,
			Operator: operator,
			Left:     &leftNode,
			Right:    &rightNode,
		}
	}
}

func (parser *valueExpressionParser) parseMultiplicative() (ValueExpression, PrepareResult) {
	left, result := parser.parsePrimary()
	if result != PrepareSuccess {
		return ValueExpression{}, result
	}

	for {
		operator, ok := parser.matchArithmeticOperator("*", "/")
		if !ok {
			return left, PrepareSuccess
		}
		right, result := parser.parsePrimary()
		if result != PrepareSuccess {
			return ValueExpression{}, result
		}
		leftNode := left
		rightNode := right
		left = ValueExpression{
			Kind:     ValueExpressionBinary,
			Operator: operator,
			Left:     &leftNode,
			Right:    &rightNode,
		}
	}
}

func (parser *valueExpressionParser) parsePrimary() (ValueExpression, PrepareResult) {
	if parser.matchValue("(") {
		expression, result := parser.parseAdditive()
		if result != PrepareSuccess || !parser.matchValue(")") {
			return ValueExpression{}, PrepareSyntaxError
		}
		return expression, PrepareSuccess
	}

	token, ok := parser.consume()
	if !ok {
		return ValueExpression{}, PrepareSyntaxError
	}
	if token.Quoted {
		return ValueExpression{Kind: ValueExpressionLiteral, Value: Value{StorageClass: StorageText, Text: token.Value}}, PrepareSuccess
	}

	if function, ok := parseAggregateFunctionName(token.Value); ok && parser.peekValue("(") {
		return parser.parseAggregateExpression(function)
	}
	if column, ok := parser.Resolver.Column(token.Value); ok {
		return ValueExpression{Kind: ValueExpressionColumn, Column: column}, PrepareSuccess
	}
	if value, ok := parseNumericLiteral(token.Value); ok {
		return ValueExpression{Kind: ValueExpressionLiteral, Value: value}, PrepareSuccess
	}

	return ValueExpression{}, PrepareSyntaxError
}

func (parser *valueExpressionParser) parseAggregateExpression(function AggregateFunction) (ValueExpression, PrepareResult) {
	if !parser.matchValue("(") {
		return ValueExpression{}, PrepareSyntaxError
	}
	if function == AggregateCount && parser.matchValue("*") {
		if !parser.matchValue(")") {
			return ValueExpression{}, PrepareSyntaxError
		}
		return ValueExpression{Kind: ValueExpressionAggregate, Function: function, CountAll: true}, PrepareSuccess
	}

	argument, result := parser.parseAdditive()
	if result != PrepareSuccess || !parser.matchValue(")") {
		return ValueExpression{}, PrepareSyntaxError
	}
	if function == AggregateSum || function == AggregateAvg {
		if !isNumericExpression(argument) {
			return ValueExpression{}, PrepareSyntaxError
		}
	}
	if expressionContainsAggregate(argument) {
		return ValueExpression{}, PrepareSyntaxError
	}

	return ValueExpression{Kind: ValueExpressionAggregate, Function: function, Argument: &argument}, PrepareSuccess
}

func parseAggregateFunctionName(input string) (AggregateFunction, bool) {
	switch strings.ToLower(input) {
	case "count":
		return AggregateCount, true
	case "sum":
		return AggregateSum, true
	case "avg":
		return AggregateAvg, true
	case "min":
		return AggregateMin, true
	case "max":
		return AggregateMax, true
	default:
		return AggregateCount, false
	}
}

func (parser *valueExpressionParser) consume() (insertField, bool) {
	if parser.Position >= len(parser.Tokens) {
		return insertField{}, false
	}
	token := parser.Tokens[parser.Position]
	parser.Position++
	return token, true
}

func (parser *valueExpressionParser) matchValue(value string) bool {
	if parser.Position >= len(parser.Tokens) {
		return false
	}
	token := parser.Tokens[parser.Position]
	if token.Quoted || token.Value != value {
		return false
	}
	parser.Position++
	return true
}

func (parser *valueExpressionParser) peekValue(value string) bool {
	if parser.Position >= len(parser.Tokens) {
		return false
	}
	token := parser.Tokens[parser.Position]
	return !token.Quoted && token.Value == value
}

func (parser *valueExpressionParser) matchArithmeticOperator(values ...string) (ArithmeticOperator, bool) {
	if parser.Position >= len(parser.Tokens) {
		return ArithmeticAdd, false
	}
	token := parser.Tokens[parser.Position]
	if token.Quoted {
		return ArithmeticAdd, false
	}
	for _, value := range values {
		if token.Value != value {
			continue
		}
		parser.Position++
		switch value {
		case "+":
			return ArithmeticAdd, true
		case "-":
			return ArithmeticSubtract, true
		case "*":
			return ArithmeticMultiply, true
		case "/":
			return ArithmeticDivide, true
		}
	}

	return ArithmeticAdd, false
}

func parseValueExpressionTokens(input string) ([]insertField, bool) {
	tokens := []insertField{}
	for i := 0; i < len(input); {
		for i < len(input) && unicode.IsSpace(rune(input[i])) {
			i++
		}
		if i >= len(input) {
			break
		}

		switch input[i] {
		case '(', ')', '+', '*', '/':
			tokens = append(tokens, insertField{Value: input[i : i+1]})
			i++
			continue
		case '\'':
			value, next, ok := parseSQLStringLiteral(input, i)
			if !ok {
				return nil, false
			}
			tokens = append(tokens, insertField{Value: value, Quoted: true})
			i = next
			if i < len(input) && !unicode.IsSpace(rune(input[i])) && !isValueExpressionDelimiter(input[i]) {
				return nil, false
			}
			continue
		case '-':
			if isSignedNumericLiteralStart(input, i, tokens) {
				start := i
				i++
				for i < len(input) && isNumericLiteralByte(input[i]) {
					i++
				}
				tokens = append(tokens, insertField{Value: input[start:i]})
				continue
			}
			tokens = append(tokens, insertField{Value: input[i : i+1]})
			i++
			continue
		}

		start := i
		for i < len(input) && !unicode.IsSpace(rune(input[i])) && !isValueExpressionDelimiter(input[i]) {
			if input[i] == '\'' {
				return nil, false
			}
			i++
		}
		tokens = append(tokens, insertField{Value: input[start:i]})
	}

	return tokens, true
}

func isSignedNumericLiteralStart(input string, index int, tokens []insertField) bool {
	if index+1 >= len(input) || !isDigit(input[index+1]) {
		return false
	}
	if len(tokens) == 0 {
		return true
	}
	previous := tokens[len(tokens)-1]
	if previous.Quoted {
		return false
	}

	switch previous.Value {
	case "(", "+", "-", "*", "/":
		return true
	default:
		return false
	}
}

func isValueExpressionDelimiter(value byte) bool {
	return value == '(' || value == ')' || value == '+' || value == '-' || value == '*' || value == '/'
}

func isNumericLiteralByte(value byte) bool {
	return isDigit(value) || value == '.'
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func parseNumericLiteral(input string) (Value, bool) {
	if strings.Contains(input, ".") {
		real, err := strconv.ParseFloat(input, 64)
		if err != nil {
			return Value{}, false
		}
		return Value{StorageClass: StorageReal, Real: real}, true
	}

	integer, err := strconv.ParseInt(input, 10, 64)
	if err != nil {
		return Value{}, false
	}
	return Value{StorageClass: StorageInteger, Integer: integer}, true
}

// parseWhereExpression はWHERE句をAND/OR/括弧付きの条件式ツリーへ変換する。
// 戻り値は行単位で評価できるWhereExpressionと解析結果を返す。
func parseWhereExpression(input string, resolver columnResolver) (WhereExpression, PrepareResult) {
	tokens, ok := parseWhereTokens(input)
	if !ok || len(tokens) == 0 {
		return WhereExpression{}, PrepareSyntaxError
	}

	parser := whereExpressionParser{Tokens: tokens, Resolver: resolver}
	expression, result := parser.parseOr()
	if result != PrepareSuccess || parser.Position != len(tokens) {
		return WhereExpression{}, PrepareSyntaxError
	}

	return expression, PrepareSuccess
}

type whereExpressionParser struct {
	Tokens   []insertField
	Position int
	Resolver columnResolver
}

func (parser *whereExpressionParser) parseOr() (WhereExpression, PrepareResult) {
	left, result := parser.parseAnd()
	if result != PrepareSuccess {
		return WhereExpression{}, result
	}

	for parser.matchKeyword("or") {
		right, result := parser.parseAnd()
		if result != PrepareSuccess {
			return WhereExpression{}, result
		}
		leftNode := left
		rightNode := right
		left = WhereExpression{
			Kind:  WhereExpressionOr,
			Left:  &leftNode,
			Right: &rightNode,
		}
	}

	return left, PrepareSuccess
}

func (parser *whereExpressionParser) parseAnd() (WhereExpression, PrepareResult) {
	left, result := parser.parsePrimary()
	if result != PrepareSuccess {
		return WhereExpression{}, result
	}

	for parser.matchKeyword("and") {
		right, result := parser.parsePrimary()
		if result != PrepareSuccess {
			return WhereExpression{}, result
		}
		leftNode := left
		rightNode := right
		left = WhereExpression{
			Kind:  WhereExpressionAnd,
			Left:  &leftNode,
			Right: &rightNode,
		}
	}

	return left, PrepareSuccess
}

func (parser *whereExpressionParser) parsePrimary() (WhereExpression, PrepareResult) {
	if parser.matchValue("(") {
		expression, result := parser.parseOr()
		if result != PrepareSuccess || !parser.matchValue(")") {
			return WhereExpression{}, PrepareSyntaxError
		}
		return expression, PrepareSuccess
	}

	condition, result := parser.parseCondition()
	if result != PrepareSuccess {
		return WhereExpression{}, result
	}

	return WhereExpression{Kind: WhereExpressionCondition, Condition: condition}, PrepareSuccess
}

func (parser *whereExpressionParser) parseCondition() (WhereCondition, PrepareResult) {
	columnToken, ok := parser.consume()
	if !ok || columnToken.Quoted || isWhereReservedToken(columnToken.Value) {
		return WhereCondition{}, PrepareSyntaxError
	}

	column, ok := parser.Resolver.Column(columnToken.Value)
	if !ok {
		return WhereCondition{}, PrepareSyntaxError
	}

	operatorToken, ok := parser.consume()
	if !ok || operatorToken.Quoted {
		return WhereCondition{}, PrepareSyntaxError
	}

	if strings.EqualFold(operatorToken.Value, "is") {
		nextToken, ok := parser.consume()
		if ok && !nextToken.Quoted && strings.EqualFold(nextToken.Value, "null") {
			return WhereCondition{Column: column, Operator: WhereIsNull}, PrepareSuccess
		}
		if ok && !nextToken.Quoted && strings.EqualFold(nextToken.Value, "not") {
			nullToken, ok := parser.consume()
			if ok && !nullToken.Quoted && strings.EqualFold(nullToken.Value, "null") {
				return WhereCondition{Column: column, Operator: WhereIsNotNull}, PrepareSuccess
			}
		}
		return WhereCondition{}, PrepareSyntaxError
	}

	operator, ok := parseWhereComparisonOperator(operatorToken.Value)
	if !ok {
		return WhereCondition{}, PrepareSyntaxError
	}
	valueToken, ok := parser.consume()
	if !ok || isWhereReservedToken(valueToken.Value) {
		return WhereCondition{}, PrepareSyntaxError
	}
	if !valueToken.Quoted && strings.EqualFold(valueToken.Value, "null") {
		return WhereCondition{}, PrepareSyntaxError
	}
	value, result := parseColumnValue(valueToken, column)
	if result != PrepareSuccess {
		return WhereCondition{}, result
	}

	return WhereCondition{Column: column, Operator: operator, Value: value}, PrepareSuccess
}

func (parser *whereExpressionParser) consume() (insertField, bool) {
	if parser.Position >= len(parser.Tokens) {
		return insertField{}, false
	}
	token := parser.Tokens[parser.Position]
	parser.Position++
	return token, true
}

func (parser *whereExpressionParser) matchKeyword(keyword string) bool {
	if parser.Position >= len(parser.Tokens) {
		return false
	}
	token := parser.Tokens[parser.Position]
	if token.Quoted || !strings.EqualFold(token.Value, keyword) {
		return false
	}
	parser.Position++
	return true
}

func (parser *whereExpressionParser) matchValue(value string) bool {
	if parser.Position >= len(parser.Tokens) {
		return false
	}
	token := parser.Tokens[parser.Position]
	if token.Quoted || token.Value != value {
		return false
	}
	parser.Position++
	return true
}

func isWhereReservedToken(value string) bool {
	return value == "(" ||
		value == ")" ||
		strings.EqualFold(value, "and") ||
		strings.EqualFold(value, "or")
}

func parseWhereComparisonOperator(input string) (WhereOperator, bool) {
	switch input {
	case "=":
		return WhereEqual, true
	case "!=", "<>":
		return WhereNotEqual, true
	case "<":
		return WhereLessThan, true
	case "<=":
		return WhereLessThanOrEqual, true
	case ">":
		return WhereGreaterThan, true
	case ">=":
		return WhereGreaterThanOrEqual, true
	default:
		return WhereEqual, false
	}
}

func parseWhereTokens(input string) ([]insertField, bool) {
	tokens := []insertField{}
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
			tokens = append(tokens, insertField{Value: value, Quoted: true})
			i = next
			if i < len(input) && !isWhereTokenDelimiter(input[i]) {
				return nil, false
			}
			continue
		}

		if input[i] == '(' || input[i] == ')' {
			tokens = append(tokens, insertField{Value: input[i : i+1]})
			i++
			continue
		}

		if isWhereOperatorByte(input[i]) {
			operator, next, ok := parseWhereOperatorToken(input, i)
			if !ok {
				return nil, false
			}
			tokens = append(tokens, insertField{Value: operator})
			i = next
			continue
		}

		start := i
		for i < len(input) && !isWhereTokenDelimiter(input[i]) && !isWhereOperatorByte(input[i]) {
			if input[i] == '\'' {
				return nil, false
			}
			i++
		}
		tokens = append(tokens, insertField{Value: input[start:i]})
	}

	return tokens, true
}

func isWhereTokenDelimiter(value byte) bool {
	return unicode.IsSpace(rune(value)) || value == '(' || value == ')'
}

func isWhereOperatorByte(value byte) bool {
	return value == '=' || value == '!' || value == '<' || value == '>'
}

func parseWhereOperatorToken(input string, start int) (string, int, bool) {
	if start+1 < len(input) {
		token := input[start : start+2]
		switch token {
		case "!=", "<>", "<=", ">=":
			return token, start + 2, true
		}
	}

	switch input[start] {
	case '=', '<', '>':
		return input[start : start+1], start + 1, true
	default:
		return "", 0, false
	}
}

// parseColumnValue は文字列トークンをカラム型に合わせたValueへ変換する。
// fieldには引用済みかどうかも含み、戻り値は変換後Valueと型・制約検査結果を返す。
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
	case AffinityBlob:
		if field.Quoted || !strings.HasPrefix(field.Value, "@") || len(field.Value) == 1 {
			return Value{}, PrepareSyntaxError
		}
		blob, err := os.ReadFile(field.Value[1:])
		if err != nil {
			return Value{}, PrepareSyntaxError
		}
		if !column.ValidateBlobValue(blob) {
			return Value{}, PrepareStringTooLong
		}
		return Value{StorageClass: StorageBlob, Blob: blob}, PrepareSuccess
	default:
		return Value{}, PrepareSyntaxError
	}
}

// executeInsert はINSERTステートメントを実行し、B-Tree内の適切な位置へ行を追加する。
// statementには挿入先テーブル名とRowToInsertが入っている前提で、戻り値は重複キーや制約違反を表す。
func executeInsert(statement *Statement, table *Table) ExecuteResult {
	if statement.TargetTable == "" {
		statement.TargetTable = table.Schema.Name
	}
	definition, ok := tableDefinition(table, statement.TargetTable)
	if !ok {
		return ExecuteConstraintViolation
	}
	target := tableView(table, definition)
	keyToInsert, ok := rowKey(statement.RowToInsert, target.Schema)
	if !ok {
		return ExecuteConstraintViolation
	}
	cursor := tableFind(target, keyToInsert)
	leafNode := getPage(target.Pager, cursor.PageNum)
	numCells := leafNodeNumCells(leafNode)
	if cursor.CellNum < numCells {
		keyAtIndex := leafNodeKey(leafNode, cursor.CellNum)
		if keyAtIndex == keyToInsert {
			return ExecuteDuplicateKey
		}
	}
	if violatesUniqueConstraint(statement.RowToInsert, target) {
		return ExecuteConstraintViolation
	}

	leafNodeInsert(cursor, keyToInsert, statement.RowToInsert)

	return ExecuteSuccess
}

// executeUpdate はUPDATEステートメントを実行する。
// 対象テーブルの全行を読み、WHEREに一致した行だけSET値を反映し、検査後にB-Treeを再構築する。
// 戻り値は主キー重複やUNIQUE/NOT NULL違反を含む実行結果を返す。
func executeUpdate(statement *Statement, table *Table) ExecuteResult {
	definition, ok := tableDefinition(table, statement.TargetTable)
	if !ok {
		return ExecuteConstraintViolation
	}
	target := tableView(table, definition)
	rows := readAllRows(target)
	updatedRows := make([]Row, 0, len(rows))

	for _, row := range rows {
		if rowMatchesWhere(row, statement.UpdateWhere) {
			row = applyUpdateAssignments(row, statement.UpdateAssignments)
		}
		updatedRows = append(updatedRows, row)
	}

	if result := validateRowsForUpdate(updatedRows, target.Schema); result != ExecuteSuccess {
		return result
	}

	rebuildTableRows(target, updatedRows)
	return ExecuteSuccess
}

// executeDelete はDELETEステートメントを実行する。
// WHEREに一致する行を除外した行セットで対象テーブルを再構築し、戻り値で実行結果を返す。
func executeDelete(statement *Statement, table *Table) ExecuteResult {
	definition, ok := tableDefinition(table, statement.TargetTable)
	if !ok {
		return ExecuteConstraintViolation
	}
	target := tableView(table, definition)
	rows := readAllRows(target)
	remainingRows := make([]Row, 0, len(rows))

	for _, row := range rows {
		if rowMatchesWhere(row, statement.DeleteWhere) {
			continue
		}
		remainingRows = append(remainingRows, row)
	}

	rebuildTableRows(target, remainingRows)
	return ExecuteSuccess
}

// executeAlterTable はALTER TABLE ADD COLUMNを実行する。
// メタデータ上のスキーマにカラムを追加し、既存行は読み取り時に不足列をNULLとして扱う。
func executeAlterTable(statement *Statement, table *Table) ExecuteResult {
	definition, ok := tableDefinition(table, statement.TargetTable)
	if !ok {
		return ExecuteConstraintViolation
	}
	definition.Schema.Columns = append(append([]Column(nil), definition.Schema.Columns...), statement.AlterColumn)
	setTableDefinition(table, definition)
	table.HasMetadata = true
	return ExecuteSuccess
}

// applyUpdateAssignments は1行にUPDATEのSET句を適用した新しいRowを返す。
// 元のrowは直接変更せず、戻り値としてコピー済みの更新行を返す。
func applyUpdateAssignments(row Row, assignments []UpdateAssignment) Row {
	updated := Row{Values: make(map[string]Value, len(row.Values))}
	for name, value := range row.Values {
		updated.Values[name] = value
	}
	for _, assignment := range assignments {
		updated.Values[assignment.Column.Name] = assignment.Value
	}

	return updated
}

// validateRowsForUpdate はUPDATE後の全行がテーブル制約を満たすか検査する。
// 戻り値は主キー重複、主キー欠落、UNIQUE/NOT NULL違反などのExecuteResultを返す。
func validateRowsForUpdate(rows []Row, schema TableSchema) ExecuteResult {
	primaryKeys := map[uint32]struct{}{}
	uniqueValues := map[string]map[string]struct{}{}
	for _, column := range schema.Columns {
		if column.Unique && !column.PrimaryKey {
			uniqueValues[column.Name] = map[string]struct{}{}
		}
	}

	for _, row := range rows {
		key, ok := rowKey(row, schema)
		if !ok {
			return ExecuteConstraintViolation
		}
		if _, exists := primaryKeys[key]; exists {
			return ExecuteDuplicateKey
		}
		primaryKeys[key] = struct{}{}

		for _, column := range schema.Columns {
			value := rowValue(row, column)
			if (column.NotNull || column.PrimaryKey) && value.StorageClass == StorageNull {
				return ExecuteConstraintViolation
			}
			if !column.Unique || column.PrimaryKey || value.StorageClass == StorageNull {
				continue
			}
			key := valueKey(value)
			values := uniqueValues[column.Name]
			if _, exists := values[key]; exists {
				return ExecuteConstraintViolation
			}
			values[key] = struct{}{}
		}
	}

	return ExecuteSuccess
}

// rebuildTableRows は指定された行セットでテーブルのB-Treeを作り直す。
// 主キー変更や可変長行のサイズ変更を安全に反映するため、UPDATE/DELETEで共通利用する。
func rebuildTableRows(table *Table, rows []Row) {
	rootNode := getPage(table.Pager, table.RootPageNum)
	clear(rootNode)
	initializeLeafNode(rootNode)
	setNodeRoot(rootNode, true)

	for _, row := range rows {
		result := executeInsert(&Statement{Type: StatementInsert, TargetTable: table.Schema.Name, RowToInsert: row}, table)
		if result != ExecuteSuccess {
			panic("validated update rows failed to reinsert")
		}
	}
}

// violatesUniqueConstraint は挿入予定の行がUNIQUE制約に違反するか調べる。
// 戻り値はNULL以外の同値が既存行にある場合にtrueになる。
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

// valuesEqual は2つのValueが等しいかをSQLite風の数値比較を含めて判定する。
// 戻り値はストレージクラスが違っても数値同士なら数値として等しい場合にtrueになる。
func valuesEqual(left Value, right Value) bool {
	if isNumericValue(left) && isNumericValue(right) {
		return numericValueAsReal(left) == numericValueAsReal(right)
	}
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
	case StorageBlob:
		return bytes.Equal(left.Blob, right.Blob)
	case StorageNull:
		return true
	default:
		return false
	}
}

// compareValues はORDER BYや条件評価用に2つのValueを比較する。
// 戻り値は左<右で負、左=右で0、左>右で正、比較不能な型ではbool=falseを返す。
func compareValues(left Value, right Value) (int, bool) {
	if isNumericValue(left) && isNumericValue(right) {
		leftReal := numericValueAsReal(left)
		rightReal := numericValueAsReal(right)
		switch {
		case leftReal < rightReal:
			return -1, true
		case leftReal > rightReal:
			return 1, true
		default:
			return 0, true
		}
	}
	if left.StorageClass != right.StorageClass {
		return 0, false
	}

	switch left.StorageClass {
	case StorageInteger:
		switch {
		case left.Integer < right.Integer:
			return -1, true
		case left.Integer > right.Integer:
			return 1, true
		default:
			return 0, true
		}
	case StorageReal:
		switch {
		case left.Real < right.Real:
			return -1, true
		case left.Real > right.Real:
			return 1, true
		default:
			return 0, true
		}
	case StorageText:
		return strings.Compare(left.Text, right.Text), true
	default:
		return 0, false
	}
}

func executeCreateTable(statement *Statement, table *Table) ExecuteResult {
	existing, exists := tableDefinition(table, statement.Schema.Name)
	if statement.ReplaceTable {
		replaceTable(statement, table, existing, exists)
		return ExecuteSuccess
	}

	if exists {
		return ExecuteTableNotEmpty
	}
	if shouldReplaceDefaultTable(table) {
		delete(table.Tables, normalizeTableName(defaultTableName))
		setTableDefinition(table, TableDefinition{Schema: statement.Schema, RootPageNum: defaultRootPageNum})
		rootNode := getPage(table.Pager, defaultRootPageNum)
		clear(rootNode)
		initializeLeafNode(rootNode)
		setNodeRoot(rootNode, true)
		return ExecuteSuccess
	}
	if shouldRejectCreateBecauseDefaultHasRows(table) {
		return ExecuteTableNotEmpty
	}

	rootPageNum := getUnusedPageNum(table.Pager)
	rootNode := getPage(table.Pager, rootPageNum)
	initializeLeafNode(rootNode)
	setNodeRoot(rootNode, true)
	setTableDefinition(table, TableDefinition{Schema: statement.Schema, RootPageNum: rootPageNum})
	return ExecuteSuccess
}

func shouldReplaceDefaultTable(table *Table) bool {
	if len(table.Tables) != 1 {
		return false
	}
	defaultDefinition, ok := tableDefinition(table, defaultTableName)
	if !ok {
		return false
	}
	defaultTable := tableView(table, defaultDefinition)
	return tableIsEmpty(defaultTable)
}

func shouldRejectCreateBecauseDefaultHasRows(table *Table) bool {
	if len(table.Tables) != 1 {
		return false
	}
	defaultDefinition, ok := tableDefinition(table, defaultTableName)
	if !ok {
		return false
	}
	defaultTable := tableView(table, defaultDefinition)
	return !tableIsEmpty(defaultTable)
}

func replaceTable(statement *Statement, table *Table, existing TableDefinition, exists bool) {
	rootPageNum := existing.RootPageNum
	if !exists {
		if len(table.Tables) == 1 {
			if defaultDefinition, ok := tableDefinition(table, defaultTableName); ok {
				delete(table.Tables, normalizeTableName(defaultTableName))
				rootPageNum = defaultDefinition.RootPageNum
			} else {
				rootPageNum = getUnusedPageNum(table.Pager)
			}
		} else {
			rootPageNum = getUnusedPageNum(table.Pager)
		}
	}
	table.HasMetadata = true
	setTableDefinition(table, TableDefinition{Schema: statement.Schema, RootPageNum: rootPageNum})

	pager := table.Pager
	metadataPage := getPage(pager, metadataPageNum)
	if err := writeDatabaseMetadata(metadataPage, databaseMetadata{Tables: tableDefinitions(table)}); err != nil {
		panic(err)
	}

	rootNode := getPage(pager, rootPageNum)
	clear(rootNode)
	initializeLeafNode(rootNode)
	setNodeRoot(rootNode, true)
}

// 現在の実装では、既存データのあるテーブルのスキーマ変更は許可しない。
func tableIsEmpty(table *Table) bool {
	root := getPage(table.Pager, table.RootPageNum)
	if getNodeType(root) != NodeLeaf {
		return false
	}

	return leafNodeNumCells(root) == 0
}

// executeSelect はSELECTステートメントを実行し、結果行を表形式で出力する。
// WHERE、ORDER BY、GROUP BY/HAVING、DISTINCT、LIMIT/OFFSETの順に既存仕様を適用する。
// 戻り値は常に実行結果を表し、表示対象が0行でもExecuteSuccessになる。
func executeSelect(statement *Statement, table *Table, out io.Writer) ExecuteResult {
	columns := statement.SelectColumns
	if len(columns) == 0 {
		columns = table.Schema.Columns
	}
	items := statement.SelectItems
	if len(items) == 0 {
		items = selectItemsFromColumns(columns)
	}
	rows := selectRows(statement, table)
	if len(statement.SelectOrderBy) > 0 {
		sortRows(rows, statement.SelectOrderBy)
	}
	if len(statement.SelectGroupBy) > 0 || selectItemsContainAggregate(items) {
		valueRows := aggregateSelectRows(rows, items, statement.SelectGroupBy, statement.SelectHaving)
		if statement.SelectDistinct {
			valueRows = distinctValueRows(valueRows)
		}
		valueRows = pageValueRows(valueRows, statement.SelectLimit, statement.SelectOffset)
		if len(valueRows) > 0 {
			printValueRows(selectItemHeaders(items), valueRows, out)
		}
		return ExecuteSuccess
	}
	valueRows := selectValueRows(rows, items)
	if statement.SelectDistinct {
		valueRows = distinctValueRows(valueRows)
	}
	valueRows = pageValueRows(valueRows, statement.SelectLimit, statement.SelectOffset)

	if len(valueRows) > 0 {
		printValueRows(selectItemHeaders(items), valueRows, out)
	}

	return ExecuteSuccess
}

// selectItemsContainAggregate はSELECT項目に集約関数が含まれるか判定する。
// 戻り値はGROUP BYなしの集約SELECTとして扱うべき場合にtrueになる。
func selectItemsContainAggregate(items []SelectItem) bool {
	for _, item := range items {
		if expressionContainsAggregate(item.Expression) {
			return true
		}
	}

	return false
}

func selectItemHeaders(items []SelectItem) []string {
	headers := make([]string, 0, len(items))
	for _, item := range items {
		headers = append(headers, item.Header)
	}

	return headers
}

// selectValueRows はRow一覧からSELECT項目の値だけを評価した表形式データを作る。
// 戻り値は出力行ごとのValueスライスで、表示ヘッダは含まない。
func selectValueRows(rows []Row, items []SelectItem) [][]Value {
	valueRows := make([][]Value, 0, len(rows))
	for _, row := range rows {
		values := make([]Value, 0, len(items))
		for _, item := range items {
			value, ok := evaluateValueExpression(row, item.Expression)
			if !ok {
				value = Value{StorageClass: StorageNull}
			}
			values = append(values, value)
		}
		valueRows = append(valueRows, values)
	}

	return valueRows
}

// distinctValueRows は表示値が重複する行を取り除く。
// 戻り値は最初に現れた行の順序を保った重複除去後の行セット。
func distinctValueRows(rows [][]Value) [][]Value {
	distinctRows := make([][]Value, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		key := valueRowKey(row)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		distinctRows = append(distinctRows, row)
	}

	return distinctRows
}

func valueRowKey(row []Value) string {
	parts := make([]string, 0, len(row))
	for _, value := range row {
		parts = append(parts, valueKey(value))
	}

	return strings.Join(parts, "\x00")
}

// aggregateSelectRows はGROUP BYや集約関数を含むSELECT結果を作る。
// rowsをグループ化し、HAVINGを通ったグループだけをSELECT項目のValue列へ変換して返す。
func aggregateSelectRows(rows []Row, items []SelectItem, groupBy []Column, having HavingExpression) [][]Value {
	groups := groupRows(rows, groupBy)
	valueRows := make([][]Value, 0, len(groups))
	for _, group := range groups {
		if !groupMatchesHaving(group.Rows, group.Representative, having) {
			continue
		}
		values := make([]Value, 0, len(items))
		for _, item := range items {
			value, ok := evaluateGroupedValueExpression(group.Rows, group.Representative, item.Expression)
			if !ok {
				value = Value{StorageClass: StorageNull}
			}
			values = append(values, value)
		}
		valueRows = append(valueRows, values)
	}

	return valueRows
}

func groupMatchesHaving(rows []Row, representative Row, expression HavingExpression) bool {
	switch expression.Kind {
	case WhereExpressionNone:
		return true
	case WhereExpressionCondition:
		return groupMatchesHavingCondition(rows, representative, expression.Condition)
	case WhereExpressionAnd:
		if expression.Left == nil || expression.Right == nil {
			return false
		}
		return groupMatchesHaving(rows, representative, *expression.Left) &&
			groupMatchesHaving(rows, representative, *expression.Right)
	case WhereExpressionOr:
		if expression.Left == nil || expression.Right == nil {
			return false
		}
		return groupMatchesHaving(rows, representative, *expression.Left) ||
			groupMatchesHaving(rows, representative, *expression.Right)
	default:
		return false
	}
}

func groupMatchesHavingCondition(rows []Row, representative Row, condition HavingCondition) bool {
	left, ok := evaluateGroupedValueExpression(rows, representative, condition.Left)
	if !ok {
		return false
	}

	switch condition.Operator {
	case WhereIsNull:
		return left.StorageClass == StorageNull
	case WhereIsNotNull:
		return left.StorageClass != StorageNull
	}

	right, ok := evaluateGroupedValueExpression(rows, representative, condition.Right)
	if !ok {
		return false
	}

	switch condition.Operator {
	case WhereEqual:
		return valuesEqual(left, right)
	case WhereNotEqual:
		if left.StorageClass == StorageNull || right.StorageClass == StorageNull {
			return false
		}
		return !valuesEqual(left, right)
	case WhereLessThan:
		comparison, ok := compareValues(left, right)
		return ok && comparison < 0
	case WhereLessThanOrEqual:
		comparison, ok := compareValues(left, right)
		return ok && comparison <= 0
	case WhereGreaterThan:
		comparison, ok := compareValues(left, right)
		return ok && comparison > 0
	case WhereGreaterThanOrEqual:
		comparison, ok := compareValues(left, right)
		return ok && comparison >= 0
	default:
		return false
	}
}

type rowGroup struct {
	Key            string
	Representative Row
	Rows           []Row
}

func groupRows(rows []Row, groupBy []Column) []rowGroup {
	if len(groupBy) == 0 {
		return []rowGroup{{Rows: rows}}
	}

	groups := []rowGroup{}
	groupIndexes := map[string]int{}
	for _, row := range rows {
		key := groupKey(row, groupBy)
		index, ok := groupIndexes[key]
		if !ok {
			groups = append(groups, rowGroup{Key: key, Representative: row})
			index = len(groups) - 1
			groupIndexes[key] = index
		}
		groups[index].Rows = append(groups[index].Rows, row)
	}

	return groups
}

func groupKey(row Row, columns []Column) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, valueKey(rowValue(row, column)))
	}

	return strings.Join(parts, "\x00")
}

func valueKey(value Value) string {
	switch value.StorageClass {
	case StorageNull:
		return "n:"
	case StorageInteger:
		return "i:" + strconv.FormatInt(value.Integer, 10)
	case StorageReal:
		return "r:" + strconv.FormatFloat(value.Real, 'g', -1, 64)
	case StorageText:
		return "t:" + value.Text
	case StorageBlob:
		return "b:" + string(value.Blob)
	default:
		return "?:"
	}
}

// pageValueRows はOFFSETで読み飛ばした後にLIMITで件数を制限する。
// limitがnilの場合はページングなしで元のrowsを返し、offsetが範囲外なら空行セットを返す。
func pageValueRows(rows [][]Value, limit *uint32, offset *uint32) [][]Value {
	if limit == nil {
		return rows
	}
	if offset != nil {
		if uint32(len(rows)) <= *offset {
			return nil
		}
		rows = rows[*offset:]
	}
	if *limit == 0 {
		return nil
	}
	if uint32(len(rows)) <= *limit {
		return rows
	}

	return rows[:*limit]
}

// evaluateValueExpression は単一行に対して値式を評価する。
// 戻り値のboolは、0除算や不正な式などで値を作れない場合にfalseになる。
func evaluateValueExpression(row Row, expression ValueExpression) (Value, bool) {
	switch expression.Kind {
	case ValueExpressionColumn:
		return rowValue(row, expression.Column), true
	case ValueExpressionLiteral:
		return expression.Value, true
	case ValueExpressionBinary:
		if expression.Left == nil || expression.Right == nil {
			return Value{}, false
		}
		left, ok := evaluateValueExpression(row, *expression.Left)
		if !ok {
			return Value{}, false
		}
		right, ok := evaluateValueExpression(row, *expression.Right)
		if !ok {
			return Value{}, false
		}
		return evaluateArithmetic(left, right, expression.Operator)
	default:
		return Value{}, false
	}
}

// evaluateGroupedValueExpression は集約グループに対してSELECT/HAVING用の値式を評価する。
// rowsは同じグループの全行、representativeは非集約カラム参照に使う代表行。
func evaluateGroupedValueExpression(rows []Row, representative Row, expression ValueExpression) (Value, bool) {
	switch expression.Kind {
	case ValueExpressionAggregate:
		return evaluateAggregate(rows, expression)
	case ValueExpressionColumn:
		return rowValue(representative, expression.Column), true
	case ValueExpressionLiteral:
		return expression.Value, true
	case ValueExpressionBinary:
		if expression.Left == nil || expression.Right == nil {
			return Value{}, false
		}
		left, ok := evaluateGroupedValueExpression(rows, representative, *expression.Left)
		if !ok {
			return Value{}, false
		}
		right, ok := evaluateGroupedValueExpression(rows, representative, *expression.Right)
		if !ok {
			return Value{}, false
		}
		return evaluateArithmetic(left, right, expression.Operator)
	default:
		return Value{}, false
	}
}

// evaluateAggregate はCOUNT/SUM/AVG/MIN/MAXをグループ行に対して評価する。
// 戻り値のboolは未対応集約や不正な引数でfalseになる。
func evaluateAggregate(rows []Row, expression ValueExpression) (Value, bool) {
	switch expression.Function {
	case AggregateCount:
		if expression.CountAll {
			return Value{StorageClass: StorageInteger, Integer: int64(len(rows))}, true
		}
		count := int64(0)
		for _, row := range rows {
			value, ok := evaluateValueExpression(row, *expression.Argument)
			if ok && value.StorageClass != StorageNull {
				count++
			}
		}
		return Value{StorageClass: StorageInteger, Integer: count}, true
	case AggregateSum:
		return evaluateSum(rows, *expression.Argument)
	case AggregateAvg:
		return evaluateAvg(rows, *expression.Argument)
	case AggregateMin:
		return evaluateMinMax(rows, *expression.Argument, false)
	case AggregateMax:
		return evaluateMinMax(rows, *expression.Argument, true)
	default:
		return Value{}, false
	}
}

func evaluateSum(rows []Row, argument ValueExpression) (Value, bool) {
	sumInteger := int64(0)
	sumReal := float64(0)
	hasReal := false
	hasValue := false
	for _, row := range rows {
		value, ok := evaluateValueExpression(row, argument)
		if !ok {
			return Value{}, false
		}
		if value.StorageClass == StorageNull {
			continue
		}
		if !isNumericValue(value) {
			return Value{}, false
		}
		hasValue = true
		if value.StorageClass == StorageReal {
			hasReal = true
		}
		if hasReal {
			sumReal += numericValueAsReal(value)
			continue
		}
		sumInteger += value.Integer
	}
	if !hasValue {
		return Value{StorageClass: StorageNull}, true
	}
	if hasReal {
		return Value{StorageClass: StorageReal, Real: sumReal + float64(sumInteger)}, true
	}

	return Value{StorageClass: StorageInteger, Integer: sumInteger}, true
}

func evaluateAvg(rows []Row, argument ValueExpression) (Value, bool) {
	sum := float64(0)
	count := int64(0)
	for _, row := range rows {
		value, ok := evaluateValueExpression(row, argument)
		if !ok {
			return Value{}, false
		}
		if value.StorageClass == StorageNull {
			continue
		}
		if !isNumericValue(value) {
			return Value{}, false
		}
		sum += numericValueAsReal(value)
		count++
	}
	if count == 0 {
		return Value{StorageClass: StorageNull}, true
	}

	return Value{StorageClass: StorageReal, Real: sum / float64(count)}, true
}

func evaluateMinMax(rows []Row, argument ValueExpression, findMax bool) (Value, bool) {
	var result Value
	hasValue := false
	for _, row := range rows {
		value, ok := evaluateValueExpression(row, argument)
		if !ok {
			return Value{}, false
		}
		if value.StorageClass == StorageNull {
			continue
		}
		if !hasValue {
			result = value
			hasValue = true
			continue
		}
		comparison, ok := compareValues(value, result)
		if !ok {
			return Value{}, false
		}
		if (findMax && comparison > 0) || (!findMax && comparison < 0) {
			result = value
		}
	}
	if !hasValue {
		return Value{StorageClass: StorageNull}, true
	}

	return result, true
}

func evaluateArithmetic(left Value, right Value, operator ArithmeticOperator) (Value, bool) {
	if left.StorageClass == StorageNull || right.StorageClass == StorageNull {
		return Value{StorageClass: StorageNull}, true
	}
	if !isNumericValue(left) || !isNumericValue(right) {
		return Value{}, false
	}
	if operator == ArithmeticDivide {
		rightReal := numericValueAsReal(right)
		if rightReal == 0 {
			return Value{StorageClass: StorageNull}, true
		}
		return Value{StorageClass: StorageReal, Real: numericValueAsReal(left) / rightReal}, true
	}
	if left.StorageClass == StorageInteger && right.StorageClass == StorageInteger {
		switch operator {
		case ArithmeticAdd:
			return Value{StorageClass: StorageInteger, Integer: left.Integer + right.Integer}, true
		case ArithmeticSubtract:
			return Value{StorageClass: StorageInteger, Integer: left.Integer - right.Integer}, true
		case ArithmeticMultiply:
			return Value{StorageClass: StorageInteger, Integer: left.Integer * right.Integer}, true
		}
	}

	leftReal := numericValueAsReal(left)
	rightReal := numericValueAsReal(right)
	switch operator {
	case ArithmeticAdd:
		return Value{StorageClass: StorageReal, Real: leftReal + rightReal}, true
	case ArithmeticSubtract:
		return Value{StorageClass: StorageReal, Real: leftReal - rightReal}, true
	case ArithmeticMultiply:
		return Value{StorageClass: StorageReal, Real: leftReal * rightReal}, true
	default:
		return Value{}, false
	}
}

func isNumericValue(value Value) bool {
	return value.StorageClass == StorageInteger || value.StorageClass == StorageReal
}

func numericValueAsReal(value Value) float64 {
	if value.StorageClass == StorageReal {
		return value.Real
	}

	return float64(value.Integer)
}

// selectRows はSELECT対象のRowをWHERE適用済みで取得する。
// statementのFROM、JOIN、DUAL、主キー検索指定を見て、戻り値として対象行のスライスを返す。
func selectRows(statement *Statement, table *Table) []Row {
	if statement.SelectFromDual {
		row := dualRow()
		if rowMatchesWhere(row, statement.SelectWhere) {
			return []Row{row}
		}
		return nil
	}
	if statement.SelectJoin != nil {
		return selectJoinedRows(statement, table)
	}
	if statement.SelectSource.Name != "" && !strings.EqualFold(statement.SelectSource.Name, dualTableName) {
		table = tableView(table, TableDefinition{Schema: statement.SelectSource.Schema, RootPageNum: statement.SelectSource.RootPageNum})
	}

	if statement.SelectByKey != nil {
		cursor := tableFind(table, *statement.SelectByKey)
		node := getPage(table.Pager, cursor.PageNum)
		if cursor.CellNum < leafNodeNumCells(node) && leafNodeKey(node, cursor.CellNum) == *statement.SelectByKey {
			row := deserializeRow(cursorValue(cursor), table.Schema)
			if rowMatchesWhere(row, statement.SelectWhere) {
				return []Row{row}
			}
		}
		return nil
	}

	rows := []Row{}
	cursor := tableStart(table)
	for !cursor.EndOfTable {
		row := deserializeRow(cursorValue(cursor), table.Schema)
		if rowMatchesWhere(row, statement.SelectWhere) {
			rows = append(rows, row)
		}
		cursorAdvance(cursor)
	}

	return rows
}

// selectJoinedRows はJOIN対象の左右テーブルを総当たりし、ON条件に一致する結合行を返す。
// 戻り値のRowは "alias.column" 形式のキーで左右カラムを保持する。
func selectJoinedRows(statement *Statement, table *Table) []Row {
	leftRows := readAllRows(tableView(table, TableDefinition{Schema: statement.SelectJoin.Left.Schema, RootPageNum: statement.SelectJoin.Left.RootPageNum}))
	rightRows := readAllRows(tableView(table, TableDefinition{Schema: statement.SelectJoin.Right.Schema, RootPageNum: statement.SelectJoin.Right.RootPageNum}))
	rows := []Row{}
	for _, left := range leftRows {
		for _, right := range rightRows {
			joined := joinRows(left, statement.SelectJoin.Left, right, statement.SelectJoin.Right)
			if !groupMatchesHaving([]Row{joined}, joined, statement.SelectJoin.On) {
				continue
			}
			if rowMatchesWhere(joined, statement.SelectWhere) {
				rows = append(rows, joined)
			}
		}
	}

	return rows
}

// readAllRows は指定テーブルの全行をB-Tree順に読み出す。
// 戻り値はUPDATE/DELETE/SELECTの入力として使うRowスライス。
func readAllRows(table *Table) []Row {
	rows := []Row{}
	cursor := tableStart(table)
	for !cursor.EndOfTable {
		rows = append(rows, deserializeRow(cursorValue(cursor), table.Schema))
		cursorAdvance(cursor)
	}

	return rows
}

func joinRows(left Row, leftReference TableReference, right Row, rightReference TableReference) Row {
	values := make(map[string]Value, len(leftReference.Schema.Columns)+len(rightReference.Schema.Columns))
	for _, column := range leftReference.Schema.Columns {
		values[qualifiedColumnName(leftReference, column.Name)] = rowValue(left, column)
	}
	for _, column := range rightReference.Schema.Columns {
		values[qualifiedColumnName(rightReference, column.Name)] = rowValue(right, column)
	}

	return Row{Values: values}
}

// sortRows はORDER BY句に従ってRowスライスを安定ソートする。
// orderByの順に比較し、前の条件が同値だった場合だけ次の条件をタイブレークに使う。
func sortRows(rows []Row, orderBy []OrderByClause) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, clause := range orderBy {
			comparison := compareOrderByValues(rowValue(rows[i], clause.Column), rowValue(rows[j], clause.Column))
			if comparison == 0 {
				continue
			}
			if clause.Direction == SortDescending {
				return comparison > 0
			}
			return comparison < 0
		}
		return false
	})
}

func compareOrderByValues(left Value, right Value) int {
	if left.StorageClass == StorageNull && right.StorageClass == StorageNull {
		return 0
	}
	if left.StorageClass == StorageNull {
		return -1
	}
	if right.StorageClass == StorageNull {
		return 1
	}

	comparison, ok := compareValues(left, right)
	if !ok {
		return 0
	}

	return comparison
}

// rowMatchesWhere は1行がWHERE条件式に一致するか判定する。
// expressionがゼロ値の場合はWHERE省略とみなし、全行一致としてtrueを返す。
func rowMatchesWhere(row Row, expression WhereExpression) bool {
	switch expression.Kind {
	case WhereExpressionNone:
		return true
	case WhereExpressionCondition:
		return rowMatchesCondition(row, expression.Condition)
	case WhereExpressionAnd:
		if expression.Left == nil || expression.Right == nil {
			return false
		}
		return rowMatchesWhere(row, *expression.Left) && rowMatchesWhere(row, *expression.Right)
	case WhereExpressionOr:
		if expression.Left == nil || expression.Right == nil {
			return false
		}
		return rowMatchesWhere(row, *expression.Left) || rowMatchesWhere(row, *expression.Right)
	default:
		return false
	}
}

func rowMatchesCondition(row Row, condition WhereCondition) bool {
	value := rowValue(row, condition.Column)
	switch condition.Operator {
	case WhereEqual:
		return valuesEqual(value, condition.Value)
	case WhereNotEqual:
		if value.StorageClass == StorageNull {
			return false
		}
		return !valuesEqual(value, condition.Value)
	case WhereLessThan:
		comparison, ok := compareValues(value, condition.Value)
		return ok && comparison < 0
	case WhereLessThanOrEqual:
		comparison, ok := compareValues(value, condition.Value)
		return ok && comparison <= 0
	case WhereGreaterThan:
		comparison, ok := compareValues(value, condition.Value)
		return ok && comparison > 0
	case WhereGreaterThanOrEqual:
		comparison, ok := compareValues(value, condition.Value)
		return ok && comparison >= 0
	case WhereIsNull:
		return value.StorageClass == StorageNull
	case WhereIsNotNull:
		return value.StorageClass != StorageNull
	default:
		return false
	}
}

// executeStatement はパース済みステートメントを種類ごとの実行関数へ振り分ける。
// 戻り値はREPLが表示メッセージを決めるためのExecuteResult。
func executeStatement(statement *Statement, table *Table, out io.Writer) ExecuteResult {
	switch statement.Type {
	case StatementCreateTable:
		return executeCreateTable(statement, table)
	case StatementInsert:
		return executeInsert(statement, table)
	case StatementSelect:
		return executeSelect(statement, table, out)
	case StatementUpdate:
		return executeUpdate(statement, table)
	case StatementDelete:
		return executeDelete(statement, table)
	case StatementAlterTable:
		return executeAlterTable(statement, table)
	}

	return ExecuteSuccess
}
