package main

import "os"

// Statement はパース済みの命令を表す。
type Statement struct {
	Type          StatementType
	RowToInsert   Row
	SelectByKey   *uint32
	SelectColumns []Column
	SelectWhere   []WhereCondition
	Schema        TableSchema
}

// WhereCondition はSELECTの単一WHERE条件を表す。
type WhereCondition struct {
	Column   Column
	Operator WhereOperator
	Value    Value
}

// WhereOperator はWHERE条件で使える比較演算子を表す。
type WhereOperator int

const (
	WhereEqual WhereOperator = iota
	WhereNotEqual
	WhereLessThan
	WhereLessThanOrEqual
	WhereGreaterThan
	WhereGreaterThanOrEqual
	WhereIsNull
	WhereIsNotNull
)

// Row は1レコードを表す。
type Row struct {
	Values map[string]Value
}

// Value はRow内の1カラム値を表す。
type Value struct {
	StorageClass StorageClass
	Integer      int64
	Real         float64
	Text         string
}

// Table はページ配列を持つオンメモリのテーブルを表す。
type Table struct {
	Pager       *Pager
	RootPageNum uint32
	Schema      TableSchema
	HasMetadata bool
}

// Cursor はテーブル内の現在位置を表す。
type Cursor struct {
	Table      *Table
	PageNum    uint32
	CellNum    uint32
	EndOfTable bool
}

// Pager はDBファイルとページキャッシュを管理する。
type Pager struct {
	File       *os.File
	FileLength int64
	NumPages   uint32
	Pages      [tableMaxPages][]byte
}
