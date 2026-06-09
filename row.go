package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

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
	binary.LittleEndian.PutUint32(destination[idOffset:usernameOffset], source.ID)
	writeFixedString(destination[usernameOffset:emailOffset], source.Username)
	writeFixedString(destination[emailOffset:rowSize], source.Email)
}

// 固定長のバイト列からRowを復元する。
func deserializeRow(source []byte) Row {
	return Row{
		ID:       binary.LittleEndian.Uint32(source[idOffset:usernameOffset]),
		Username: readFixedString(source[usernameOffset:emailOffset]),
		Email:    readFixedString(source[emailOffset:rowSize]),
	}
}
