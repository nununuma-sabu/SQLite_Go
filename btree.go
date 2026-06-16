package main

// getUnusedPageNum は新規ページとして使えるページ番号を返す。
// 現在はfree listを持たないため、PagerのNumPagesをそのまま返す。
func getUnusedPageNum(pager *Pager) uint32 {
	return pager.NumPages
}

// createNewRoot はroot leaf nodeの分割後、新しいinternal rootを作る。
// rightChildPageNumは分割で作られた右側ノードで、既存rootは左側子としてコピーされる。
func createNewRoot(table *Table, rightChildPageNum uint32) {
	root := getPage(table.Pager, table.RootPageNum)
	rightChild := getPage(table.Pager, rightChildPageNum)
	leftChildPageNum := getUnusedPageNum(table.Pager)
	leftChild := getPage(table.Pager, leftChildPageNum)

	if getNodeType(root) == NodeInternal {
		initializeInternalNode(rightChild)
		initializeInternalNode(leftChild)
	}

	copy(leftChild, root)
	setNodeRoot(leftChild, false)
	if getNodeType(leftChild) == NodeInternal {
		for i := uint32(0); i < internalNodeNumKeys(leftChild); i++ {
			child := getPage(table.Pager, internalNodeChild(leftChild, i))
			setNodeParent(child, leftChildPageNum)
		}
		child := getPage(table.Pager, internalNodeRightChild(leftChild))
		setNodeParent(child, leftChildPageNum)
	}

	initializeInternalNode(root)
	setNodeRoot(root, true)
	setInternalNodeNumKeys(root, 1)
	setInternalNodeChild(root, 0, leftChildPageNum)
	setInternalNodeKey(root, 0, getNodeMaxKey(table.Pager, leftChild))
	setInternalNodeRightChild(root, rightChildPageNum)
	setNodeRoot(rightChild, false)
	setNodeParent(leftChild, table.RootPageNum)
	setNodeParent(rightChild, table.RootPageNum)
}

// internal node内の古いseparator keyを新しいkeyに更新する。
func updateInternalNodeKey(node []byte, oldKey uint32, newKey uint32) {
	oldChildIndex := internalNodeFindChild(node, oldKey)
	setInternalNodeKey(node, oldChildIndex, newKey)
}

// 満杯のinternal nodeを分割し、新しいchildも適切な側へ挿入する。
func internalNodeSplitAndInsert(table *Table, parentPageNum uint32, childPageNum uint32) {
	oldPageNum := parentPageNum
	oldNode := getPage(table.Pager, parentPageNum)
	oldMax := getNodeMaxKey(table.Pager, oldNode)

	child := getPage(table.Pager, childPageNum)
	childMax := getNodeMaxKey(table.Pager, child)

	newPageNum := getUnusedPageNum(table.Pager)
	splittingRoot := isNodeRoot(oldNode)

	var parent []byte
	var newNode []byte
	if splittingRoot {
		createNewRoot(table, newPageNum)
		parent = getPage(table.Pager, table.RootPageNum)
		oldPageNum = internalNodeChild(parent, 0)
		oldNode = getPage(table.Pager, oldPageNum)
	} else {
		parent = getPage(table.Pager, nodeParent(oldNode))
		newNode = getPage(table.Pager, newPageNum)
		initializeInternalNode(newNode)
	}

	curPageNum := internalNodeRightChild(oldNode)
	cur := getPage(table.Pager, curPageNum)

	internalNodeInsert(table, newPageNum, curPageNum)
	setNodeParent(cur, newPageNum)
	setInternalNodeRightChild(oldNode, invalidPageNum)

	for i := int(internalNodeMaxCells - 1); i > int(internalNodeMaxCells/2); i-- {
		curPageNum = internalNodeChild(oldNode, uint32(i))
		cur = getPage(table.Pager, curPageNum)

		internalNodeInsert(table, newPageNum, curPageNum)
		setNodeParent(cur, newPageNum)
		setInternalNodeNumKeys(oldNode, internalNodeNumKeys(oldNode)-1)
	}

	oldNumKeys := internalNodeNumKeys(oldNode)
	setInternalNodeRightChild(oldNode, internalNodeChild(oldNode, oldNumKeys-1))
	setInternalNodeNumKeys(oldNode, oldNumKeys-1)

	maxAfterSplit := getNodeMaxKey(table.Pager, oldNode)
	destinationPageNum := newPageNum
	if childMax < maxAfterSplit {
		destinationPageNum = oldPageNum
	}

	internalNodeInsert(table, destinationPageNum, childPageNum)
	setNodeParent(child, destinationPageNum)

	updateInternalNodeKey(parent, oldMax, getNodeMaxKey(table.Pager, oldNode))

	if !splittingRoot {
		internalNodeInsert(table, nodeParent(oldNode), newPageNum)
		setNodeParent(newNode, nodeParent(oldNode))
	}
}

// internalNodeInsert はinternal nodeへ子ページ参照を追加する。
// parentPageNumが挿入先、childPageNumが追加する子で、必要に応じてinternal nodeを分割する。
func internalNodeInsert(table *Table, parentPageNum uint32, childPageNum uint32) {
	parent := getPage(table.Pager, parentPageNum)
	child := getPage(table.Pager, childPageNum)
	childMaxKey := getNodeMaxKey(table.Pager, child)
	index := internalNodeFindChild(parent, childMaxKey)

	originalNumKeys := internalNodeNumKeys(parent)
	if originalNumKeys >= internalNodeMaxCells {
		internalNodeSplitAndInsert(table, parentPageNum, childPageNum)
		return
	}

	rightChildPageNum := internalNodeRightChild(parent)
	if rightChildPageNum == invalidPageNum {
		setInternalNodeRightChild(parent, childPageNum)
		setNodeParent(child, parentPageNum)
		return
	}

	rightChild := getPage(table.Pager, rightChildPageNum)

	setInternalNodeNumKeys(parent, originalNumKeys+1)

	if childMaxKey > getNodeMaxKey(table.Pager, rightChild) {
		setInternalNodeChild(parent, originalNumKeys, rightChildPageNum)
		setInternalNodeKey(parent, originalNumKeys, getNodeMaxKey(table.Pager, rightChild))
		setInternalNodeRightChild(parent, childPageNum)
	} else {
		for i := originalNumKeys; i > index; i-- {
			copy(internalNodeCell(parent, i), internalNodeCell(parent, i-1))
		}
		setInternalNodeChild(parent, index, childPageNum)
		setInternalNodeKey(parent, index, childMaxKey)
	}

	setNodeParent(child, parentPageNum)
}

// leafNodeSplitAndInsert は満杯のleaf nodeを左右に分割し、新しいキーとRowも正しい側へ挿入する。
// cursorが分割対象位置、key/valueが新規挿入行で、親ノードのキー更新も行う。
func leafNodeSplitAndInsert(cursor *Cursor, key uint32, value Row) {
	oldNode := getPage(cursor.Table.Pager, cursor.PageNum)
	oldMax := getNodeMaxKey(cursor.Table.Pager, oldNode)
	newPageNum := getUnusedPageNum(cursor.Table.Pager)
	newNode := getPage(cursor.Table.Pager, newPageNum)
	initializeLeafNode(newNode)
	setNodeParent(newNode, nodeParent(oldNode))
	setLeafNodeNextLeaf(newNode, leafNodeNextLeaf(oldNode))
	setLeafNodeNextLeaf(oldNode, newPageNum)

	cells := leafNodeCells(cursor.Table.Pager, oldNode)
	payload := make([]byte, serializedRowSize(value, cursor.Table.Schema))
	serializeRow(value, cursor.Table.Schema, payload)
	cells = append(cells, leafCell{})
	insertIndex := int(cursor.CellNum)
	copy(cells[insertIndex+1:], cells[insertIndex:])
	cells[insertIndex] = leafCell{Key: key, Payload: payload}

	clearLeafNodeCells(oldNode)
	clearLeafNodeCells(newNode)

	leftSplitCount := uint32((len(cells) + 1) / 2)
	for i, cell := range cells {
		if uint32(i) < leftSplitCount {
			leafNodeWritePayloadCell(cursor.Table.Pager, oldNode, uint32(i), cell.Key, cell.Payload)
			setLeafNodeNumCells(oldNode, uint32(i)+1)
			continue
		}

		indexWithinNode := uint32(i) - leftSplitCount
		leafNodeWritePayloadCell(cursor.Table.Pager, newNode, indexWithinNode, cell.Key, cell.Payload)
		setLeafNodeNumCells(newNode, indexWithinNode+1)
	}

	if isNodeRoot(oldNode) {
		createNewRoot(cursor.Table, newPageNum)
		return
	}

	parentPageNum := nodeParent(oldNode)
	newMax := getNodeMaxKey(cursor.Table.Pager, oldNode)
	parent := getPage(cursor.Table.Pager, parentPageNum)

	updateInternalNodeKey(parent, oldMax, newMax)
	internalNodeInsert(cursor.Table, parentPageNum, newPageNum)
}

type leafCell struct {
	Key     uint32
	Payload []byte
}

func leafNodeCells(pager *Pager, node []byte) []leafCell {
	numCells := leafNodeNumCells(node)
	cells := make([]leafCell, 0, numCells)
	for i := uint32(0); i < numCells; i++ {
		payload := leafNodeValue(pager, node, i)
		payloadCopy := make([]byte, len(payload))
		copy(payloadCopy, payload)
		cells = append(cells, leafCell{
			Key:     leafNodeKey(node, i),
			Payload: payloadCopy,
		})
	}

	return cells
}

// leafNodeInsert はleaf nodeへキーとRowを挿入する。
// 空きが足りない場合はleafNodeSplitAndInsertへ委譲し、戻り値は持たない。
func leafNodeInsert(cursor *Cursor, key uint32, value Row) {
	node := getPage(cursor.Table.Pager, cursor.PageNum)
	numCells := leafNodeNumCells(node)
	payloadSize := serializedRowSize(value, cursor.Table.Schema)
	if !leafNodeCanFit(node, payloadSize) {
		leafNodeSplitAndInsert(cursor, key, value)
		return
	}

	if cursor.CellNum < numCells {
		// 挿入位置より後ろのセルを1つずつ後方へずらして空きを作る。
		leafNodeShiftCellPointersRight(node, cursor.CellNum, numCells)
	}

	setLeafNodeNumCells(node, numCells+1)
	leafNodeWriteCell(cursor.Table.Pager, node, cursor.CellNum, key, value, cursor.Table.Schema)
}
