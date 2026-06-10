package main

import "os"

// Statement はパース済みの命令を表す。
type Statement struct {
	Type          StatementType
	RowToInsert   Row
	SelectByKey   *uint32
	SelectColumns []Column
	SelectWhere   WhereExpression
	SelectOrderBy *OrderByClause
	SelectLimit   *uint32
	Schema        TableSchema
}

// OrderByClause はSELECTのORDER BY指定を表す。
type OrderByClause struct {
	Column    Column
	Direction SortDirection
}

// SortDirection はORDER BYの昇順・降順を表す。
type SortDirection int

const (
	SortAscending SortDirection = iota
	SortDescending
)

// WhereExpression はWHERE条件式を表す。
type WhereExpression struct {
	Kind      WhereExpressionKind
	Condition WhereCondition
	Left      *WhereExpression
	Right     *WhereExpression
}

// WhereCondition はSELECTの単一WHERE条件を表す。
type WhereCondition struct {
	Column   Column
	Operator WhereOperator
	Value    Value
}

// WhereExpressionKind はWHERE条件式の種類を表す。
type WhereExpressionKind int

const (
	WhereExpressionNone WhereExpressionKind = iota
	WhereExpressionCondition
	WhereExpressionAnd
	WhereExpressionOr
)

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
