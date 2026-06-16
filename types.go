package main

import "os"

// Statement はパース済みの命令を表す。
type Statement struct {
	Type              StatementType
	TargetTable       string
	RowToInsert       Row
	UpdateAssignments []UpdateAssignment
	UpdateWhere       WhereExpression
	DeleteWhere       WhereExpression
	AlterColumn       Column
	ReplaceTable      bool
	SelectByKey       *uint32
	SelectColumns     []Column
	SelectItems       []SelectItem
	SelectDistinct    bool
	SelectFromDual    bool
	SelectSource      TableReference
	SelectJoin        *JoinClause
	SelectWhere       WhereExpression
	SelectGroupBy     []Column
	SelectHaving      HavingExpression
	SelectOrderBy     []OrderByClause
	SelectLimit       *uint32
	SelectOffset      *uint32
	Schema            TableSchema
}

// UpdateAssignment はUPDATEのSET句に指定された1カラム分の更新値を表す。
type UpdateAssignment struct {
	Column Column
	Value  Value
}

// TableReference はFROM句に現れるテーブル参照を表す。
type TableReference struct {
	Name        string
	Alias       string
	Schema      TableSchema
	RootPageNum uint32
}

// JoinClause はINNER JOINの対象とON条件を表す。
type JoinClause struct {
	Left  TableReference
	Right TableReference
	On    HavingExpression
}

// SelectItem はSELECT句に指定された表示対象を表す。
type SelectItem struct {
	Header     string
	Expression ValueExpression
}

// ValueExpression はカラム参照、リテラル、数値演算式を表す。
type ValueExpression struct {
	Kind     ValueExpressionKind
	Column   Column
	Value    Value
	Function AggregateFunction
	CountAll bool
	Argument *ValueExpression
	Operator ArithmeticOperator
	Left     *ValueExpression
	Right    *ValueExpression
}

// ValueExpressionKind は値式の種類を表す。
type ValueExpressionKind int

const (
	ValueExpressionColumn ValueExpressionKind = iota
	ValueExpressionLiteral
	ValueExpressionAggregate
	ValueExpressionBinary
)

// AggregateFunction はSELECT句で使える集約関数を表す。
type AggregateFunction int

const (
	AggregateCount AggregateFunction = iota
	AggregateSum
	AggregateAvg
	AggregateMin
	AggregateMax
)

// ArithmeticOperator は数値式で使える演算子を表す。
type ArithmeticOperator int

const (
	ArithmeticAdd ArithmeticOperator = iota
	ArithmeticSubtract
	ArithmeticMultiply
	ArithmeticDivide
)

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

// HavingExpression はHAVING条件式を表す。
type HavingExpression struct {
	Kind      WhereExpressionKind
	Condition HavingCondition
	Left      *HavingExpression
	Right     *HavingExpression
}

// HavingCondition は集約後の単一HAVING条件を表す。
type HavingCondition struct {
	Left     ValueExpression
	Operator WhereOperator
	Right    ValueExpression
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
	Blob         []byte
}

// Table はページ配列を持つオンメモリのテーブルを表す。
type Table struct {
	Pager       *Pager
	RootPageNum uint32
	Schema      TableSchema
	HasMetadata bool
	Tables      map[string]TableDefinition
}

// TableDefinition はDB内の1テーブル分のメタデータを表す。
type TableDefinition struct {
	Schema      TableSchema `json:"schema"`
	RootPageNum uint32      `json:"root_page_num"`
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
