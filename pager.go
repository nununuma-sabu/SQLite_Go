package main

import (
	"fmt"
	"io"
	"os"
)

// DBファイルを開き、ページャを初期化する。
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

// DBファイルを開き、空ファイルならpage 0をleaf nodeとして初期化する。
func dbOpen(filename string) (*Table, error) {
	pager, err := pagerOpen(filename)
	if err != nil {
		return nil, err
	}

	table := &Table{
		Pager:       pager,
		RootPageNum: 0,
	}

	if pager.NumPages == 0 {
		rootNode := getPage(pager, 0)
		initializeLeafNode(rootNode)
		setNodeRoot(rootNode, true)
	}

	return table, nil
}

// 指定されたページをページャから取得する。
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

// 指定されたページ全体をDBファイルへ書き戻す。
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

// キャッシュされたページをDBファイルへflushし、ファイルを閉じる。
func dbClose(table *Table) error {
	pager := table.Pager

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
