package main

import "strings"

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
}

// TableSchema は任意カラムを持つテーブル定義を表す。
type TableSchema struct {
	Name    string
	Columns []Column
}

// NewColumn は宣言型からSQLite風の型アフィニティを推定してカラム定義を作る。
func NewColumn(name string, declaredType string) Column {
	return Column{
		Name:         name,
		DeclaredType: declaredType,
		Affinity:     InferTypeAffinity(declaredType),
	}
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
