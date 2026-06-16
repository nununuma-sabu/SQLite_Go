package main

import (
	"sort"
	"strings"
)

const (
	defaultTableName     = "users"
	idColumnName         = "id"
	usernameColumnName   = "username"
	emailColumnName      = "email"
	idDeclaredType       = "INTEGER"
	usernameDeclaredType = "TEXT"
	emailDeclaredType    = "TEXT"
)

// StorageClass は値そのものの保存形式を表す。
// SQLiteのManifest Typingに近く、カラムではなく値に結びつく型として扱う。
type StorageClass int

const (
	StorageNull StorageClass = iota
	StorageInteger
	StorageReal
	StorageText
	StorageBlob
)

func (storageClass StorageClass) String() string {
	switch storageClass {
	case StorageNull:
		return "NULL"
	case StorageInteger:
		return "INTEGER"
	case StorageReal:
		return "REAL"
	case StorageText:
		return "TEXT"
	case StorageBlob:
		return "BLOB"
	default:
		return "UNKNOWN"
	}
}

// TypeAffinity はカラム宣言から推定される型の親和性を表す。
// 値の保存形式を強制するものではなく、将来の値変換で参照するヒントとして使う。
type TypeAffinity int

const (
	AffinityInteger TypeAffinity = iota
	AffinityText
	AffinityReal
	AffinityNumeric
	AffinityBlob
)

func (affinity TypeAffinity) String() string {
	switch affinity {
	case AffinityInteger:
		return "INTEGER"
	case AffinityText:
		return "TEXT"
	case AffinityReal:
		return "REAL"
	case AffinityNumeric:
		return "NUMERIC"
	case AffinityBlob:
		return "BLOB"
	default:
		return "UNKNOWN"
	}
}

// Column は将来のCREATE TABLEで利用するカラム定義を表す。
type Column struct {
	Name                 string
	DeclaredType         string
	Affinity             TypeAffinity
	MaxLength            uint32
	PrimaryKey           bool
	PrimaryKeyConstraint bool
	NotNull              bool
	Unique               bool
}

// TableSchema は任意カラムを持つテーブル定義を表す。
type TableSchema struct {
	Name    string
	Columns []Column
}

// RowLayout はスキーマから計算した1行分の固定長レイアウトを表す。
type RowLayout struct {
	ColumnOffsets map[string]uint32
	ColumnSizes   map[string]uint32
	Size          uint32
}

// NewColumn は宣言型からSQLite風の型アフィニティを推定してカラム定義を作る。
// nameがカラム名、declaredTypeがSQL上の型名で、戻り値は制約未設定のColumn。
func NewColumn(name string, declaredType string) Column {
	column := Column{
		Name:         name,
		DeclaredType: declaredType,
		Affinity:     InferTypeAffinity(declaredType),
	}
	if column.Affinity == AffinityText {
		column.MaxLength = defaultTextSize
	}

	return column
}

// DefaultTableSchema はデフォルトのusersテーブルスキーマを返す。
// 戻り値はid/username/emailを持ち、既存テストと旧DB互換の基準スキーマとして使う。
func DefaultTableSchema() TableSchema {
	return TableSchema{
		Name: defaultTableName,
		Columns: []Column{
			{
				Name:         idColumnName,
				DeclaredType: idDeclaredType,
				Affinity:     InferTypeAffinity(idDeclaredType),
				PrimaryKey:   true,
			},
			{
				Name:         usernameColumnName,
				DeclaredType: usernameDeclaredType,
				Affinity:     InferTypeAffinity(usernameDeclaredType),
				MaxLength:    columnUsernameSize,
			},
			{
				Name:         emailColumnName,
				DeclaredType: emailDeclaredType,
				Affinity:     InferTypeAffinity(emailDeclaredType),
				MaxLength:    columnEmailSize,
			},
		},
	}
}

// normalizeTableName はテーブル名をメタデータ検索用に正規化する。
// 戻り値は前後空白を除いた小文字文字列。
func normalizeTableName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// metadataTables はDBメタデータのテーブル一覧を名前検索用mapへ変換する。
// 戻り値のキーはnormalizeTableNameで正規化したテーブル名。
func metadataTables(metadata databaseMetadata) map[string]TableDefinition {
	tables := make(map[string]TableDefinition, len(metadata.Tables))
	for _, definition := range metadata.Tables {
		tables[normalizeTableName(definition.Schema.Name)] = definition
	}

	return tables
}

// tableDefinitions はTableが管理する全テーブル定義を安定した順序のスライスへ変換する。
// 戻り値はメタデータ保存時にJSON化される。
func tableDefinitions(table *Table) []TableDefinition {
	definitions := make([]TableDefinition, 0, len(table.Tables))
	for _, definition := range table.Tables {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool {
		return strings.ToLower(definitions[i].Schema.Name) < strings.ToLower(definitions[j].Schema.Name)
	})

	return definitions
}

// tableDefinition は名前からテーブル定義を取得する。
// 戻り値のboolは、指定名のテーブルが管理対象に存在する場合だけtrueになる。
func tableDefinition(table *Table, name string) (TableDefinition, bool) {
	definition, ok := table.Tables[normalizeTableName(name)]
	return definition, ok
}

// setTableDefinition はテーブル定義を登録または更新する。
// table.Schemaとtable.RootPageNumも同じ定義へ同期し、直近操作対象として扱えるようにする。
func setTableDefinition(table *Table, definition TableDefinition) {
	if table.Tables == nil {
		table.Tables = map[string]TableDefinition{}
	}
	table.Tables[normalizeTableName(definition.Schema.Name)] = definition
	table.Schema = definition.Schema
	table.RootPageNum = definition.RootPageNum
}

// tableView は同じPagerを共有しつつ、指定テーブル定義を操作対象にした軽量Tableを返す。
// 戻り値は複数テーブル環境でINSERT/SELECT/UPDATE/DELETE対象を切り替えるために使う。
func tableView(table *Table, definition TableDefinition) *Table {
	return &Table{
		Pager:       table.Pager,
		RootPageNum: definition.RootPageNum,
		Schema:      definition.Schema,
		HasMetadata: table.HasMetadata,
		Tables:      table.Tables,
	}
}

// Column はスキーマから名前一致するカラムを取得する。
// 戻り値のboolは、大文字小文字を無視して一致するカラムがある場合にtrueになる。
func (schema TableSchema) Column(name string) (Column, bool) {
	for _, column := range schema.Columns {
		if strings.EqualFold(column.Name, name) {
			return column, true
		}
	}

	return Column{}, false
}

// PrimaryKeyColumn はスキーマ内の主キーカラムを返す。
// 戻り値のboolは主キーが定義されている場合にtrueになる。
func (schema TableSchema) PrimaryKeyColumn() (Column, bool) {
	for _, column := range schema.Columns {
		if column.PrimaryKey {
			return column, true
		}
	}

	return Column{}, false
}

// IsUsable はスキーマが現在のエンジンで扱える形か検査する。
// 戻り値はテーブル名、重複列、型、単一INTEGER主キーの条件を満たす場合にtrueになる。
func (schema TableSchema) IsUsable() bool {
	if strings.TrimSpace(schema.Name) == "" || len(schema.Columns) == 0 {
		return false
	}

	seenColumns := make(map[string]struct{}, len(schema.Columns))
	primaryKeyCount := 0
	for _, column := range schema.Columns {
		normalizedName := strings.ToLower(strings.TrimSpace(column.Name))
		if normalizedName == "" {
			return false
		}
		if _, ok := seenColumns[normalizedName]; ok {
			return false
		}
		seenColumns[normalizedName] = struct{}{}
		if column.Affinity != AffinityBlob && column.StorageSize() == 0 {
			return false
		}
		if column.PrimaryKey {
			primaryKeyCount++
			if column.Affinity != AffinityInteger {
				return false
			}
		}
	}

	return primaryKeyCount == 1
}

// CreateStatement はスキーマを.schema表示用のCREATE TABLE文へ変換する。
// 戻り値は保存済み制約のうち現在表示対応しているものを含むSQL文字列。
func (schema TableSchema) CreateStatement() string {
	columnDefinitions := make([]string, 0, len(schema.Columns))
	for _, column := range schema.Columns {
		parts := []string{column.Name, column.DeclaredType}
		if column.PrimaryKeyConstraint {
			parts = append(parts, "primary key")
		}
		if column.NotNull {
			parts = append(parts, "not null")
		}
		if column.Unique {
			parts = append(parts, "unique")
		}
		columnDefinitions = append(columnDefinitions, strings.Join(parts, " "))
	}

	return "create table " + schema.Name + " (" + strings.Join(columnDefinitions, ", ") + ")"
}

// SerializedRowSize はスキーマ上の最大シリアライズサイズを返す。
// 戻り値はページ内に行が収まるかを事前判定するために使う。
func (schema TableSchema) SerializedRowSize() uint32 {
	size := uint32(len(rowRecordFormatMagic))
	for _, column := range schema.Columns {
		size += column.SerializedSize()
	}

	return size
}

// RowLayout は旧固定長形式を読むための列オフセット情報を作る。
// 戻り値は列名ごとの開始位置、サイズ、全体サイズを持つ。
func (schema TableSchema) RowLayout() RowLayout {
	layout := RowLayout{
		ColumnOffsets: make(map[string]uint32, len(schema.Columns)),
		ColumnSizes:   make(map[string]uint32, len(schema.Columns)),
	}

	for _, column := range schema.Columns {
		columnSize := column.StorageSize()
		layout.ColumnOffsets[column.Name] = layout.Size
		layout.ColumnSizes[column.Name] = columnSize
		layout.Size += columnSize
	}

	return layout
}

func (layout RowLayout) ColumnRange(columnName string) (uint32, uint32, bool) {
	offset, ok := layout.ColumnOffsets[columnName]
	if !ok {
		return 0, 0, false
	}

	size := layout.ColumnSizes[columnName]
	return offset, offset + size, true
}

func (column Column) StorageSize() uint32 {
	switch column.Affinity {
	case AffinityInteger:
		return idSize
	case AffinityText:
		return column.MaxLength + 1
	case AffinityReal:
		return 8
	case AffinityBlob, AffinityNumeric:
		if column.MaxLength > 0 {
			return column.MaxLength
		}
	}

	return 0
}

func (column Column) SerializedSize() uint32 {
	if column.Affinity == AffinityBlob {
		if column.MaxLength > 0 {
			return 1 + 4 + column.MaxLength
		}
		return 1 + 4
	}

	return 1 + column.StorageSize()
}

// ValidateBlobValue はBLOB値がカラム制約上保存可能か判定する。
// 戻り値は最大長制約がない、または制約以内の場合にtrueになる。
func (column Column) ValidateBlobValue(value []byte) bool {
	if column.Affinity != AffinityBlob {
		return false
	}
	if column.MaxLength == 0 {
		return true
	}

	return uint32(len(value)) <= column.MaxLength
}

// ValidateIntegerValue はINTEGER値がカラム制約上保存可能か判定する。
// 主キー列では正のuint32範囲に収まる場合だけtrueを返す。
func (column Column) ValidateIntegerValue(value int64) bool {
	if column.Affinity != AffinityInteger {
		return false
	}
	if column.PrimaryKey {
		return value >= 0
	}

	return true
}

// ValidateTextValue はTEXT値がカラムの最大長制約を満たすか判定する。
// 戻り値は最大長制約がない、または文字列長が制約以内の場合にtrueになる。
func (column Column) ValidateTextValue(value string) bool {
	if column.Affinity != AffinityText {
		return false
	}
	if column.MaxLength == 0 {
		return true
	}

	return uint32(len(value)) <= column.MaxLength
}

// InferTypeAffinity はSQLiteの型名解釈に近い順序で型アフィニティを推定する。
// InferTypeAffinity はSQLの宣言型名からSQLite風の型アフィニティを推定する。
// 戻り値はINTEGER/REAL/TEXT/BLOB/NUMERICのいずれか。
func InferTypeAffinity(declaredType string) TypeAffinity {
	normalized := strings.ToUpper(strings.TrimSpace(declaredType))
	if normalized == "" {
		return AffinityBlob
	}

	if strings.Contains(normalized, "INT") {
		return AffinityInteger
	}
	if strings.Contains(normalized, "CHAR") ||
		strings.Contains(normalized, "CLOB") ||
		strings.Contains(normalized, "TEXT") {
		return AffinityText
	}
	if strings.Contains(normalized, "BLOB") {
		return AffinityBlob
	}
	if strings.Contains(normalized, "REAL") ||
		strings.Contains(normalized, "FLOA") ||
		strings.Contains(normalized, "DOUB") {
		return AffinityReal
	}

	return AffinityNumeric
}
