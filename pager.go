package main

import (
	"fmt"
	"io"
	"os"
)

// pagerOpen はDBファイルを開き、ページキャッシュを管理するPagerを作成する。
// filenameが対象ファイルで、戻り値はPagerとOSファイル操作エラーを返す。
func pagerOpen(filename string) (*Pager, error) {
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
		return nil, err
	}
	fileLength := info.Size()
	if fileLength%pageSize != 0 {
		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
		return nil, fmt.Errorf("db file is not a whole number of pages")
	}

	return &Pager{
		File:       file,
		FileLength: fileLength,
		NumPages:   uint32(fileLength / pageSize),
	}, nil
}

// dbOpen はDBファイルを開き、Table管理構造を初期化する。
// 空ファイルならメタデータページとデフォルトテーブルを作り、既存ファイルならメタデータを復元する。
func dbOpen(filename string) (*Table, error) {
	pager, err := pagerOpen(filename)
	if err != nil {
		return nil, err
	}

	table := &Table{
		Pager:       pager,
		RootPageNum: 0,
		Schema:      DefaultTableSchema(),
	}
	table.Tables = map[string]TableDefinition{
		normalizeTableName(table.Schema.Name): {Schema: table.Schema, RootPageNum: defaultRootPageNum},
	}

	if pager.NumPages == 0 {
		table.RootPageNum = defaultRootPageNum
		table.HasMetadata = true
		metadataPage := getPage(pager, metadataPageNum)
		if err := writeDatabaseMetadata(metadataPage, databaseMetadata{Tables: tableDefinitions(table)}); err != nil {
			return nil, err
		}

		rootNode := getPage(pager, table.RootPageNum)
		initializeLeafNode(rootNode)
		setNodeRoot(rootNode, true)
		return table, nil
	}

	firstPage := getPage(pager, metadataPageNum)
	if isMetadataPage(firstPage) {
		metadata, err := readDatabaseMetadata(firstPage)
		if err != nil {
			return nil, err
		}
		table.RootPageNum = defaultRootPageNum
		table.Tables = metadataTables(metadata)
		activeDefinition := tableDefinitions(table)[0]
		if defaultDefinition, ok := tableDefinition(table, defaultTableName); ok {
			activeDefinition = defaultDefinition
		}
		table.Schema = activeDefinition.Schema
		table.RootPageNum = activeDefinition.RootPageNum
		table.HasMetadata = true
	}

	return table, nil
}

// getPage は指定ページ番号のページをキャッシュから取得し、未読ならファイルから読み込む。
// 戻り値はページサイズ分のバイトスライスで、呼び出し側が直接内容を更新できる。
func getPage(pager *Pager, pageNum uint32) []byte {
	if pageNum >= tableMaxPages {
		panic(fmt.Sprintf("Tried to fetch page number out of bounds. %d >= %d", pageNum, tableMaxPages))
	}

	if pager.Pages[pageNum] == nil {
		// キャッシュミス時だけページを確保し、ファイルに既存データがあれば読み込む。
		page := make([]byte, pageSize)
		numPages := pager.FileLength / pageSize
		if pager.FileLength%pageSize != 0 {
			numPages++
		}

		if int64(pageNum) < numPages {
			_, err := pager.File.ReadAt(page, int64(pageNum)*pageSize)
			if err != nil && err != io.EOF {
				panic(fmt.Sprintf("Error reading file: %v", err))
			}
		}

		pager.Pages[pageNum] = page
		if pageNum >= pager.NumPages {
			pager.NumPages = pageNum + 1
		}
	}

	return pager.Pages[pageNum]
}

// pagerFlush はキャッシュ上の1ページをDBファイルへ書き戻す。
// 戻り値はseek/writeの失敗をerrorとして返す。
func pagerFlush(pager *Pager, pageNum uint32) error {
	page := pager.Pages[pageNum]
	if page == nil {
		return fmt.Errorf("Tried to flush null page")
	}

	bytesWritten, err := pager.File.WriteAt(page, int64(pageNum)*pageSize)
	if err != nil {
		return err
	}
	if bytesWritten != pageSize {
		return io.ErrShortWrite
	}

	offset := int64(pageNum+1) * pageSize
	if offset > pager.FileLength {
		pager.FileLength = offset
	}

	return nil
}

// dbClose はキャッシュされたページをDBファイルへflushし、ファイルを閉じる。
// メタデータを最新のテーブル定義で書き直し、戻り値でflush/closeのエラーを返す。
func dbClose(table *Table) error {
	pager := table.Pager
	if table.HasMetadata {
		metadataPage := getPage(pager, metadataPageNum)
		if err := writeDatabaseMetadata(metadataPage, databaseMetadata{Tables: tableDefinitions(table)}); err != nil {
			return err
		}
	}

	for i := uint32(0); i < pager.NumPages; i++ {
		if pager.Pages[i] == nil {
			continue
		}
		if err := pagerFlush(pager, i); err != nil {
			return err
		}
		pager.Pages[i] = nil
	}

	return pager.File.Close()
}
