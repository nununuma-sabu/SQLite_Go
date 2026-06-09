package main

const prompt = "db > "

const (
	columnUsernameSize = 32
	columnEmailSize    = 255

	idSize       = 4
	usernameSize = columnUsernameSize + 1
	emailSize    = columnEmailSize + 1

	idOffset       = 0
	usernameOffset = idOffset + idSize
	emailOffset    = usernameOffset + usernameSize
	rowSize        = idSize + usernameSize + emailSize

	pageSize              = 4096
	tableMaxPages         = 100
	invalidPageNum uint32 = 1<<32 - 1

	nodeTypeSize           = 1
	nodeTypeOffset         = 0
	isRootSize             = 1
	isRootOffset           = nodeTypeOffset + nodeTypeSize
	parentPointerSize      = 4
	parentPointerOffset    = isRootOffset + isRootSize
	commonNodeHeaderSize   = nodeTypeSize + isRootSize + parentPointerSize
	leafNodeNumCellsSize   = 4
	leafNodeNumCellsOffset = commonNodeHeaderSize
	leafNodeNextLeafSize   = 4
	leafNodeNextLeafOffset = leafNodeNumCellsOffset + leafNodeNumCellsSize
	leafNodeHeaderSize     = commonNodeHeaderSize + leafNodeNumCellsSize + leafNodeNextLeafSize

	leafNodeKeySize         = 4
	leafNodeKeyOffset       = 0
	leafNodeValueSize       = rowSize
	leafNodeValueOffset     = leafNodeKeyOffset + leafNodeKeySize
	leafNodeCellSize        = leafNodeKeySize + leafNodeValueSize
	leafNodeSpaceForCells   = pageSize - leafNodeHeaderSize
	leafNodeMaxCells        = leafNodeSpaceForCells / leafNodeCellSize
	leafNodeRightSplitCount = (leafNodeMaxCells + 1) / 2
	leafNodeLeftSplitCount  = (leafNodeMaxCells + 1) - leafNodeRightSplitCount

	internalNodeNumKeysSize      = 4
	internalNodeNumKeysOffset    = commonNodeHeaderSize
	internalNodeRightChildSize   = 4
	internalNodeRightChildOffset = internalNodeNumKeysOffset + internalNodeNumKeysSize
	internalNodeHeaderSize       = commonNodeHeaderSize + internalNodeNumKeysSize + internalNodeRightChildSize
	internalNodeKeySize          = 4
	internalNodeChildSize        = 4
	internalNodeCellSize         = internalNodeChildSize + internalNodeKeySize
	internalNodeMaxCells         = 3
)

// ExitCode はプログラムの終了状態を表す。
type ExitCode int

const (
	ExitSuccess ExitCode = iota
	ExitFailure
)

// MetaCommandResult は .exit のようなメタコマンドの処理結果を表す。
type MetaCommandResult int

const (
	MetaCommandSuccess MetaCommandResult = iota
	MetaCommandUnrecognizedCommand
)

// ExecuteResult はステートメント実行の結果を表す。
type ExecuteResult int

const (
	ExecuteSuccess ExecuteResult = iota
	ExecuteDuplicateKey
	ExecuteTableFull
)

// PrepareResult は入力文字列を実行可能なステートメントへ変換できたかを表す。
type PrepareResult int

const (
	PrepareSuccess PrepareResult = iota
	PrepareNegativeID
	PrepareStringTooLong
	PrepareSyntaxError
	PrepareUnrecognizedStatement
)

// StatementType は実行するステートメントの種類を表す。
type StatementType int

const (
	StatementInsert StatementType = iota
	StatementSelect
)

// NodeType はB-Treeノードの種類を表す。
type NodeType int

const (
	NodeInternal NodeType = iota
	NodeLeaf
)
