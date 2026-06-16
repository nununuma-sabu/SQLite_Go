package main

import "fmt"

// tableStart はテーブルの先頭行を指すCursorを作成する。
// 戻り値は空テーブルの場合EndOfTable=trueになる。
func tableStart(table *Table) *Cursor {
	cursor := tableFind(table, 0)
	node := getPage(table.Pager, cursor.PageNum)

	cursor.EndOfTable = leafNodeNumCells(node) == 0
	return cursor
}

// tableEnd はテーブル末尾、つまり挿入位置として使えるCursorを作成する。
// 戻り値は常にEndOfTable=trueで、末尾セル番号を保持する。
func tableEnd(table *Table) *Cursor {
	rootNode := getPage(table.Pager, table.RootPageNum)
	return &Cursor{
		Table:      table,
		PageNum:    table.RootPageNum,
		CellNum:    leafNodeNumCells(rootNode),
		EndOfTable: true,
	}
}

// tableFind は主キーkeyを検索し、見つかった位置または挿入すべき位置のCursorを返す。
// B-Treeのrootがleaf/internalのどちらでも適切な探索関数へ委譲する。
func tableFind(table *Table, key uint32) *Cursor {
	rootPageNum := table.RootPageNum
	rootNode := getPage(table.Pager, rootPageNum)

	switch getNodeType(rootNode) {
	case NodeLeaf:
		return leafNodeFind(table, rootPageNum, key)
	case NodeInternal:
		return internalNodeFind(table, rootPageNum, key)
	default:
		panic(fmt.Sprintf("unknown node type: %d", getNodeType(rootNode)))
	}
}

// internal nodeで、指定キーが含まれるべきchildのindexを返す。
func internalNodeFindChild(node []byte, key uint32) uint32 {
	numKeys := internalNodeNumKeys(node)
	minIndex := uint32(0)
	maxIndex := numKeys
	for minIndex != maxIndex {
		index := (minIndex + maxIndex) / 2
		keyToRight := internalNodeKey(node, index)
		if keyToRight >= key {
			maxIndex = index
		} else {
			minIndex = index + 1
		}
	}

	return minIndex
}

// internal node内で探索すべき子を二分探索し、再帰的に目的のleafへ進む。
func internalNodeFind(table *Table, pageNum uint32, key uint32) *Cursor {
	node := getPage(table.Pager, pageNum)
	childIndex := internalNodeFindChild(node, key)
	childNum := internalNodeChild(node, childIndex)
	child := getPage(table.Pager, childNum)
	switch getNodeType(child) {
	case NodeLeaf:
		return leafNodeFind(table, childNum, key)
	case NodeInternal:
		return internalNodeFind(table, childNum, key)
	default:
		panic(fmt.Sprintf("unknown node type: %d", getNodeType(child)))
	}
}

// leaf node内でキーを二分探索し、見つかった位置または挿入位置を返す。
func leafNodeFind(table *Table, pageNum uint32, key uint32) *Cursor {
	node := getPage(table.Pager, pageNum)
	numCells := leafNodeNumCells(node)

	cursor := &Cursor{
		Table:   table,
		PageNum: pageNum,
	}

	minIndex := uint32(0)
	onePastMaxIndex := numCells
	for onePastMaxIndex != minIndex {
		index := (minIndex + onePastMaxIndex) / 2
		keyAtIndex := leafNodeKey(node, index)

		if key == keyAtIndex {
			cursor.CellNum = index
			return cursor
		}
		if key < keyAtIndex {
			onePastMaxIndex = index
		} else {
			minIndex = index + 1
		}
	}

	cursor.CellNum = minIndex
	return cursor
}

// cursorValue はCursorが指すセルのペイロード領域を返す。
// 戻り値はシリアライズ済みRowのバイト列で、deserializeRowの入力になる。
func cursorValue(cursor *Cursor) []byte {
	page := getPage(cursor.Table.Pager, cursor.PageNum)
	return leafNodeValue(cursor.Table.Pager, page, cursor.CellNum)
}

// cursorAdvance はCursorを次の行へ進める。
// leaf末尾に到達した場合は右隣leafへ移動し、次ページがなければEndOfTable=trueにする。
func cursorAdvance(cursor *Cursor) {
	node := getPage(cursor.Table.Pager, cursor.PageNum)
	cursor.CellNum++
	if cursor.CellNum >= leafNodeNumCells(node) {
		nextPageNum := leafNodeNextLeaf(node)
		if nextPageNum == 0 {
			cursor.EndOfTable = true
			return
		}

		cursor.PageNum = nextPageNum
		cursor.CellNum = 0
	}
}
