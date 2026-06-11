package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

const overflowPayloadMagic = "OVF1"

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

func leafNodeContentStart(node []byte) uint32 {
	return uint32(binary.LittleEndian.Uint16(node[leafNodeContentStartOffset : leafNodeContentStartOffset+leafNodeContentStartSize]))
}

func setLeafNodeContentStart(node []byte, offset uint32) {
	binary.LittleEndian.PutUint16(node[leafNodeContentStartOffset:leafNodeContentStartOffset+leafNodeContentStartSize], uint16(offset))
}

func leafNodeCellPointer(node []byte, cellNum uint32) []byte {
	start := leafNodeHeaderSize + cellNum*leafNodeCellPointerSize
	return node[start : start+leafNodeCellPointerSize]
}

func leafNodeCellOffset(node []byte, cellNum uint32) uint32 {
	return uint32(binary.LittleEndian.Uint16(leafNodeCellPointer(node, cellNum)))
}

func setLeafNodeCellOffset(node []byte, cellNum uint32, offset uint32) {
	binary.LittleEndian.PutUint16(leafNodeCellPointer(node, cellNum), uint16(offset))
}

// leaf node内の指定セルを返す。
func leafNodeCell(node []byte, cellNum uint32) []byte {
	start := leafNodeCellOffset(node, cellNum)
	return node[start : start+leafNodeCellSizeAt(node, cellNum)]
}

func leafNodeCellSizeAt(node []byte, cellNum uint32) uint32 {
	start := leafNodeCellOffset(node, cellNum)
	payloadSize := leafNodeStoredPayloadSize(node, start)
	return leafNodeValueOffset + payloadSize
}

// leaf node内の指定セルからキーを読み取る。
func leafNodeKey(node []byte, cellNum uint32) uint32 {
	start := leafNodeCellOffset(node, cellNum)
	return binary.LittleEndian.Uint32(node[start+leafNodeKeyOffset : start+leafNodePayloadSizeOffset])
}

// leaf node内の指定セルへキーを書き込む。
func setLeafNodeKey(node []byte, cellNum uint32, key uint32) {
	start := leafNodeCellOffset(node, cellNum)
	binary.LittleEndian.PutUint32(node[start+leafNodeKeyOffset:start+leafNodePayloadSizeOffset], key)
}

// leaf node内の指定セルから値領域を返す。
func leafNodeValue(pager *Pager, node []byte, cellNum uint32) []byte {
	value := leafNodeLocalValue(node, cellNum)
	if !leafNodeCellUsesOverflow(node, cellNum) {
		return value
	}
	if len(value) < len(overflowPayloadMagic)+8 || string(value[:len(overflowPayloadMagic)]) != overflowPayloadMagic {
		panic("invalid overflow payload")
	}

	totalSize := binary.LittleEndian.Uint32(value[len(overflowPayloadMagic) : len(overflowPayloadMagic)+4])
	firstPageNum := binary.LittleEndian.Uint32(value[len(overflowPayloadMagic)+4 : len(overflowPayloadMagic)+8])
	return readOverflowPayload(pager, firstPageNum, totalSize)
}

func leafNodeLocalValue(node []byte, cellNum uint32) []byte {
	start := leafNodeCellOffset(node, cellNum)
	payloadSize := leafNodeStoredPayloadSize(node, start)
	return node[start+leafNodeValueOffset : start+leafNodeValueOffset+payloadSize]
}

func leafNodeStoredPayloadSize(node []byte, cellOffset uint32) uint32 {
	raw := binary.LittleEndian.Uint32(node[cellOffset+leafNodePayloadSizeOffset : cellOffset+leafNodeValueOffset])
	return raw &^ leafNodeOverflowPayloadFlag
}

func leafNodeCellUsesOverflow(node []byte, cellNum uint32) bool {
	start := leafNodeCellOffset(node, cellNum)
	raw := binary.LittleEndian.Uint32(node[start+leafNodePayloadSizeOffset : start+leafNodeValueOffset])
	return raw&leafNodeOverflowPayloadFlag != 0
}

func leafNodePointerArrayEnd(numCells uint32) uint32 {
	return leafNodeHeaderSize + numCells*leafNodeCellPointerSize
}

func leafNodeFreeSpace(node []byte) uint32 {
	return leafNodeContentStart(node) - leafNodePointerArrayEnd(leafNodeNumCells(node))
}

func leafNodeCanFit(node []byte, payloadSize uint32) bool {
	return leafNodeFreeSpace(node) >= leafNodeCellPointerSize+leafNodeValueOffset+leafNodeLocalPayloadSize(payloadSize)
}

func leafNodeLocalPayloadSize(payloadSize uint32) uint32 {
	if payloadSize > leafNodeMaxPayloadSize {
		return uint32(len(overflowPayloadMagic) + 8)
	}

	return payloadSize
}

func leafNodeWriteCell(pager *Pager, node []byte, cellNum uint32, key uint32, row Row, schema TableSchema) {
	payload := make([]byte, serializedRowSize(row, schema))
	serializeRow(row, schema, payload)
	leafNodeWritePayloadCell(pager, node, cellNum, key, payload)
}

func leafNodeWritePayloadCell(pager *Pager, node []byte, cellNum uint32, key uint32, payload []byte) {
	payloadSize := uint32(len(payload))
	localPayload := payload
	usesOverflow := payloadSize > leafNodeMaxPayloadSize
	if usesOverflow {
		firstPageNum := writeOverflowPayload(pager, payload)
		localPayload = make([]byte, len(overflowPayloadMagic)+8)
		copy(localPayload, overflowPayloadMagic)
		binary.LittleEndian.PutUint32(localPayload[len(overflowPayloadMagic):len(overflowPayloadMagic)+4], payloadSize)
		binary.LittleEndian.PutUint32(localPayload[len(overflowPayloadMagic)+4:len(overflowPayloadMagic)+8], firstPageNum)
	}

	localPayloadSize := uint32(len(localPayload))
	cellSize := leafNodeValueOffset + localPayloadSize
	cellOffset := leafNodeContentStart(node) - cellSize

	setLeafNodeContentStart(node, cellOffset)
	setLeafNodeCellOffset(node, cellNum, cellOffset)
	binary.LittleEndian.PutUint32(node[cellOffset+leafNodeKeyOffset:cellOffset+leafNodePayloadSizeOffset], key)
	storedPayloadSize := localPayloadSize
	if usesOverflow {
		storedPayloadSize |= leafNodeOverflowPayloadFlag
	}
	binary.LittleEndian.PutUint32(node[cellOffset+leafNodePayloadSizeOffset:cellOffset+leafNodeValueOffset], storedPayloadSize)
	copy(node[cellOffset+leafNodeValueOffset:cellOffset+leafNodeValueOffset+localPayloadSize], localPayload)
}

func writeOverflowPayload(pager *Pager, payload []byte) uint32 {
	firstPageNum := getUnusedPageNum(pager)
	pageNum := firstPageNum
	remaining := payload
	for {
		page := getPage(pager, pageNum)
		clear(page)

		chunkSize := len(remaining)
		if chunkSize > overflowPagePayloadCapacity {
			chunkSize = overflowPagePayloadCapacity
		}
		remaining = remaining[chunkSize:]

		nextPageNum := uint32(0)
		if len(remaining) > 0 {
			nextPageNum = getUnusedPageNum(pager)
		}

		binary.LittleEndian.PutUint32(page[overflowPageNextPageOffset:overflowPagePayloadSizeOffset], nextPageNum)
		binary.LittleEndian.PutUint32(page[overflowPagePayloadSizeOffset:overflowPageHeaderSize], uint32(chunkSize))
		copy(page[overflowPageHeaderSize:overflowPageHeaderSize+chunkSize], payload[:chunkSize])
		payload = payload[chunkSize:]

		if nextPageNum == 0 {
			break
		}
		pageNum = nextPageNum
	}

	return firstPageNum
}

func readOverflowPayload(pager *Pager, firstPageNum uint32, totalSize uint32) []byte {
	payload := make([]byte, 0, totalSize)
	pageNum := firstPageNum
	for pageNum != 0 && uint32(len(payload)) < totalSize {
		page := getPage(pager, pageNum)
		nextPageNum := binary.LittleEndian.Uint32(page[overflowPageNextPageOffset:overflowPagePayloadSizeOffset])
		chunkSize := binary.LittleEndian.Uint32(page[overflowPagePayloadSizeOffset:overflowPageHeaderSize])
		payload = append(payload, page[overflowPageHeaderSize:overflowPageHeaderSize+chunkSize]...)
		pageNum = nextPageNum
	}
	if uint32(len(payload)) != totalSize {
		panic("overflow payload is truncated")
	}

	return payload
}

func leafNodeShiftCellPointersRight(node []byte, firstCell uint32, numCells uint32) {
	for i := numCells; i > firstCell; i-- {
		copy(leafNodeCellPointer(node, i), leafNodeCellPointer(node, i-1))
	}
}

func clearLeafNodeCells(node []byte) {
	setLeafNodeNumCells(node, 0)
	setLeafNodeContentStart(node, pageSize)
	clear(node[leafNodeHeaderSize:])
}

// 新しいleaf nodeを初期化する。
func initializeLeafNode(node []byte) {
	setNodeType(node, NodeLeaf)
	setNodeRoot(node, false)
	setLeafNodeNumCells(node, 0)
	setLeafNodeNextLeaf(node, 0)
	setLeafNodeContentStart(node, pageSize)
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
