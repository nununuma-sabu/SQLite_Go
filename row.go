package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

var defaultRowLayout = DefaultTableSchema().RowLayout()

// Rowを画面表示用の形式で出力する。
func printRow(row Row, out io.Writer) {
	fmt.Fprintf(out, "(%d, %s, %s)\n", row.ID, row.Username, row.Email)
}

// 固定長領域へ文字列を書き込む。余った領域はゼロ値のまま残る。
func writeFixedString(destination []byte, value string) {
	copy(destination, value)
}

// 固定長領域から、最初のゼロ値までを文字列として読み取る。
func readFixedString(source []byte) string {
	if index := strings.IndexByte(string(source), 0); index >= 0 {
		return string(source[:index])
	}

	return string(source)
}

// Rowをページ内に保存できる固定長のバイト列へ変換する。
func serializeRow(source Row, destination []byte) {
	idStart, idEnd := mustColumnRange(defaultRowLayout, idColumnName)
	usernameStart, usernameEnd := mustColumnRange(defaultRowLayout, usernameColumnName)
	emailStart, emailEnd := mustColumnRange(defaultRowLayout, emailColumnName)

	binary.LittleEndian.PutUint32(destination[idStart:idEnd], source.ID)
	writeFixedString(destination[usernameStart:usernameEnd], source.Username)
	writeFixedString(destination[emailStart:emailEnd], source.Email)
}

// 固定長のバイト列からRowを復元する。
func deserializeRow(source []byte) Row {
	idStart, idEnd := mustColumnRange(defaultRowLayout, idColumnName)
	usernameStart, usernameEnd := mustColumnRange(defaultRowLayout, usernameColumnName)
	emailStart, emailEnd := mustColumnRange(defaultRowLayout, emailColumnName)

	return Row{
		ID:       binary.LittleEndian.Uint32(source[idStart:idEnd]),
		Username: readFixedString(source[usernameStart:usernameEnd]),
		Email:    readFixedString(source[emailStart:emailEnd]),
	}
}

func mustColumnRange(layout RowLayout, columnName string) (uint32, uint32) {
	start, end, ok := layout.ColumnRange(columnName)
	if !ok {
		panic(fmt.Sprintf("missing column in row layout: %s", columnName))
	}

	return start, end
}
