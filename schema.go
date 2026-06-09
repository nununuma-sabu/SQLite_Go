package main

import "strings"

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
	Name         string
	DeclaredType string
	Affinity     TypeAffinity
	MaxLength    uint32
	PrimaryKey   bool
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
func NewColumn(name string, declaredType string) Column {
	return Column{
		Name:         name,
		DeclaredType: declaredType,
		Affinity:     InferTypeAffinity(declaredType),
	}
}

// DefaultTableSchema は現在の固定Row実装に対応するスキーマを返す。
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

func (schema TableSchema) Column(name string) (Column, bool) {
	for _, column := range schema.Columns {
		if strings.EqualFold(column.Name, name) {
			return column, true
		}
	}

	return Column{}, false
}

func (schema TableSchema) PrimaryKeyColumn() (Column, bool) {
	for _, column := range schema.Columns {
		if column.PrimaryKey {
			return column, true
		}
	}

	return Column{}, false
}

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

func (column Column) ValidateIntegerValue(value int64) bool {
	return column.Affinity == AffinityInteger && value >= 0
}

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
