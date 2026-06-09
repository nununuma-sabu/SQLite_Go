package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ノード種別を読み取る。
func getNodeType(node []byte) NodeType {
	return NodeType(node[nodeTypeOffset])
}

// ノード種別を書き込む。
func setNodeType(node []byte, nodeType NodeType) {
	node[nodeTypeOffset] = byte(nodeType)
}

// root nodeかどうかを読み取る。
func isNodeRoot(node []byte) bool {
	return node[isRootOffset] != 0
}

// root nodeかどうかを書き込む。
func setNodeRoot(node []byte, isRoot bool) {
	if isRoot {
		node[isRootOffset] = 1
		return
	}
	node[isRootOffset] = 0
}

// 親ノードのページ番号を読み取る。
func nodeParent(node []byte) uint32 {
	return binary.LittleEndian.Uint32(node[parentPointerOffset : parentPointerOffset+parentPointerSize])
}

// 親ノードのページ番号を書き込む。
func setNodeParent(node []byte, pageNum uint32) {
	binary.LittleEndian.PutUint32(node[parentPointerOffset:parentPointerOffset+parentPointerSize], pageNum)
}

// internal nodeが持つキー数を読み取る。
func internalNodeNumKeys(node []byte) uint32 {
	return binary.LittleEndian.Uint32(node[internalNodeNumKeysOffset : internalNodeNumKeysOffset+internalNodeNumKeysSize])
}

// internal nodeが持つキー数を書き込む。
func setInternalNodeNumKeys(node []byte, numKeys uint32) {
	binary.LittleEndian.PutUint32(node[internalNodeNumKeysOffset:internalNodeNumKeysOffset+internalNodeNumKeysSize], numKeys)
}

// internal nodeの右端の子ページ番号を読み取る。
func internalNodeRightChild(node []byte) uint32 {
	return binary.LittleEndian.Uint32(node[internalNodeRightChildOffset : internalNodeRightChildOffset+internalNodeRightChildSize])
}

// internal nodeの右端の子ページ番号を書き込む。
func setInternalNodeRightChild(node []byte, pageNum uint32) {
	binary.LittleEndian.PutUint32(node[internalNodeRightChildOffset:internalNodeRightChildOffset+internalNodeRightChildSize], pageNum)
}

// internal node内の指定セルを返す。
func internalNodeCell(node []byte, cellNum uint32) []byte {
	start := internalNodeHeaderSize + cellNum*internalNodeCellSize
	return node[start : start+internalNodeCellSize]
}

// internal nodeの子ページ番号を読み取る。
func internalNodeChild(node []byte, childNum uint32) uint32 {
	numKeys := internalNodeNumKeys(node)
	if childNum > numKeys {
		panic(fmt.Sprintf("Tried to access childNum %d > numKeys %d", childNum, numKeys))
	}
	if childNum == numKeys {
		rightChild := internalNodeRightChild(node)
		if rightChild == invalidPageNum {
			panic("Tried to access right child of node, but was invalid page")
		}
		return rightChild
	}
	child := binary.LittleEndian.Uint32(internalNodeCell(node, childNum)[:internalNodeChildSize])
	if child == invalidPageNum {
		panic(fmt.Sprintf("Tried to access child %d of node, but was invalid page", childNum))
	}
	return child
}

// internal nodeの子ページ番号を書き込む。
func setInternalNodeChild(node []byte, childNum uint32, pageNum uint32) {
	numKeys := internalNodeNumKeys(node)
	if childNum > numKeys {
		panic(fmt.Sprintf("Tried to access childNum %d > numKeys %d", childNum, numKeys))
	}
	if childNum == numKeys {
		setInternalNodeRightChild(node, pageNum)
		return
	}
	binary.LittleEndian.PutUint32(internalNodeCell(node, childNum)[:internalNodeChildSize], pageNum)
}

// internal nodeのキーを読み取る。
func internalNodeKey(node []byte, keyNum uint32) uint32 {
	cell := internalNodeCell(node, keyNum)
	return binary.LittleEndian.Uint32(cell[internalNodeChildSize : internalNodeChildSize+internalNodeKeySize])
}

// internal nodeのキーを書き込む。
func setInternalNodeKey(node []byte, keyNum uint32, key uint32) {
	cell := internalNodeCell(node, keyNum)
	binary.LittleEndian.PutUint32(cell[internalNodeChildSize:internalNodeChildSize+internalNodeKeySize], key)
}

// ノードが表すsubtree内の最大キーを返す。
func getNodeMaxKey(pager *Pager, node []byte) uint32 {
	if getNodeType(node) == NodeLeaf {
		return leafNodeKey(node, leafNodeNumCells(node)-1)
	}

	rightChild := getPage(pager, internalNodeRightChild(node))
	return getNodeMaxKey(pager, rightChild)
}

// leaf nodeが持つセル数を読み取る。
func leafNodeNumCells(node []byte) uint32 {
	return binary.LittleEndian.Uint32(node[leafNodeNumCellsOffset : leafNodeNumCellsOffset+leafNodeNumCellsSize])
}

// leaf nodeが持つセル数を書き込む。
func setLeafNodeNumCells(node []byte, numCells uint32) {
	binary.LittleEndian.PutUint32(node[leafNodeNumCellsOffset:leafNodeNumCellsOffset+leafNodeNumCellsSize], numCells)
}

// leaf nodeの右隣leaf nodeページ番号を読み取る。0は右隣なしを表す。
func leafNodeNextLeaf(node []byte) uint32 {
	return binary.LittleEndian.Uint32(node[leafNodeNextLeafOffset : leafNodeNextLeafOffset+leafNodeNextLeafSize])
}

// leaf nodeの右隣leaf nodeページ番号を書き込む。
func setLeafNodeNextLeaf(node []byte, pageNum uint32) {
	binary.LittleEndian.PutUint32(node[leafNodeNextLeafOffset:leafNodeNextLeafOffset+leafNodeNextLeafSize], pageNum)
}

// leaf node内の指定セルを返す。
func leafNodeCell(node []byte, cellNum uint32) []byte {
	start := leafNodeHeaderSize + cellNum*leafNodeCellSize
	return node[start : start+leafNodeCellSize]
}

// leaf node内の指定セルからキーを読み取る。
func leafNodeKey(node []byte, cellNum uint32) uint32 {
	cell := leafNodeCell(node, cellNum)
	return binary.LittleEndian.Uint32(cell[leafNodeKeyOffset:leafNodeValueOffset])
}

// leaf node内の指定セルへキーを書き込む。
func setLeafNodeKey(node []byte, cellNum uint32, key uint32) {
	cell := leafNodeCell(node, cellNum)
	binary.LittleEndian.PutUint32(cell[leafNodeKeyOffset:leafNodeValueOffset], key)
}

// leaf node内の指定セルから値領域を返す。
func leafNodeValue(node []byte, cellNum uint32) []byte {
	cell := leafNodeCell(node, cellNum)
	return cell[leafNodeValueOffset : leafNodeValueOffset+leafNodeValueSize]
}

// 新しいleaf nodeを初期化する。
func initializeLeafNode(node []byte) {
	setNodeType(node, NodeLeaf)
	setNodeRoot(node, false)
	setLeafNodeNumCells(node, 0)
	setLeafNodeNextLeaf(node, 0)
}

// 新しいinternal nodeを初期化する。
func initializeInternalNode(node []byte) {
	setNodeType(node, NodeInternal)
	setNodeRoot(node, false)
	setInternalNodeNumKeys(node, 0)
	setInternalNodeRightChild(node, invalidPageNum)
}

// B-Tree実装で使う定数を出力する。
func printConstants(out io.Writer) {
	fmt.Fprintf(out, "ROW_SIZE: %d\n", rowSize)
	fmt.Fprintf(out, "COMMON_NODE_HEADER_SIZE: %d\n", commonNodeHeaderSize)
	fmt.Fprintf(out, "LEAF_NODE_HEADER_SIZE: %d\n", leafNodeHeaderSize)
	fmt.Fprintf(out, "LEAF_NODE_CELL_SIZE: %d\n", leafNodeCellSize)
	fmt.Fprintf(out, "LEAF_NODE_SPACE_FOR_CELLS: %d\n", leafNodeSpaceForCells)
	fmt.Fprintf(out, "LEAF_NODE_MAX_CELLS: %d\n", leafNodeMaxCells)
}

// 指定階層分だけインデントする。
func indent(out io.Writer, level uint32) {
	for i := uint32(0); i < level; i++ {
		fmt.Fprint(out, "  ")
	}
}

// B-Tree構造を再帰的に出力する。
func printTree(pager *Pager, pageNum uint32, indentationLevel uint32, out io.Writer) {
	node := getPage(pager, pageNum)

	switch getNodeType(node) {
	case NodeLeaf:
		numKeys := leafNodeNumCells(node)
		indent(out, indentationLevel)
		fmt.Fprintf(out, "- leaf (size %d)\n", numKeys)
		for i := uint32(0); i < numKeys; i++ {
			indent(out, indentationLevel+1)
			fmt.Fprintf(out, "- %d\n", leafNodeKey(node, i))
		}
	case NodeInternal:
		numKeys := internalNodeNumKeys(node)
		indent(out, indentationLevel)
		fmt.Fprintf(out, "- internal (size %d)\n", numKeys)
		if numKeys == 0 {
			return
		}
		for i := uint32(0); i < numKeys; i++ {
			printTree(pager, internalNodeChild(node, i), indentationLevel+1, out)
			indent(out, indentationLevel+1)
			fmt.Fprintf(out, "- key %d\n", internalNodeKey(node, i))
		}
		printTree(pager, internalNodeRightChild(node), indentationLevel+1, out)
	default:
		panic(fmt.Sprintf("unknown node type: %d", getNodeType(node)))
	}
}
