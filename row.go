package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

var defaultRowLayout = DefaultTableSchema().RowLayout()

// Rowを画面表示用の形式で出力する。
func printRow(row Row, schema TableSchema, out io.Writer) {
	values := make([]string, 0, len(schema.Columns))
	for _, column := range schema.Columns {
		values = append(values, formatRowValue(row, column))
	}

	fmt.Fprintf(out, "(%s)\n", strings.Join(values, ", "))
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
func serializeRow(source Row, schema TableSchema, destination []byte) {
	layout := schema.RowLayout()
	clear(destination)

	for _, column := range schema.Columns {
		start, end := mustColumnRange(layout, column.Name)
		value := rowValue(source, column)

		switch column.Affinity {
		case AffinityInteger:
			binary.LittleEndian.PutUint32(destination[start:end], uint32(value.Integer))
		case AffinityReal:
			binary.LittleEndian.PutUint64(destination[start:end], math.Float64bits(value.Real))
		case AffinityText:
			writeFixedString(destination[start:end], value.Text)
		}
	}
}

// 固定長のバイト列からRowを復元する。
func deserializeRow(source []byte, schema TableSchema) Row {
	layout := schema.RowLayout()
	row := Row{
		Values: make(map[string]Value, len(schema.Columns)),
	}

	for _, column := range schema.Columns {
		start, end := mustColumnRange(layout, column.Name)
		value := Value{}

		switch column.Affinity {
		case AffinityInteger:
			value.StorageClass = StorageInteger
			value.Integer = int64(binary.LittleEndian.Uint32(source[start:end]))
			if column.PrimaryKey {
				row.ID = uint32(value.Integer)
			}
		case AffinityReal:
			value.StorageClass = StorageReal
			value.Real = math.Float64frombits(binary.LittleEndian.Uint64(source[start:end]))
		case AffinityText:
			value.StorageClass = StorageText
			value.Text = readFixedString(source[start:end])
			switch strings.ToLower(column.Name) {
			case usernameColumnName:
				row.Username = value.Text
			case emailColumnName:
				row.Email = value.Text
			}
		default:
			value.StorageClass = StorageNull
		}

		row.Values[column.Name] = value
	}

	return row
}

func mustColumnRange(layout RowLayout, columnName string) (uint32, uint32) {
	start, end, ok := layout.ColumnRange(columnName)
	if !ok {
		panic(fmt.Sprintf("missing column in row layout: %s", columnName))
	}

	return start, end
}

func rowValue(row Row, column Column) Value {
	if column.PrimaryKey {
		return Value{StorageClass: StorageInteger, Integer: int64(row.ID)}
	}
	if value, ok := row.Values[column.Name]; ok {
		return value
	}

	switch strings.ToLower(column.Name) {
	case usernameColumnName:
		return Value{StorageClass: StorageText, Text: row.Username}
	case emailColumnName:
		return Value{StorageClass: StorageText, Text: row.Email}
	}

	return Value{StorageClass: StorageNull}
}

func formatRowValue(row Row, column Column) string {
	value := rowValue(row, column)

	switch column.Affinity {
	case AffinityInteger:
		return strconv.FormatInt(value.Integer, 10)
	case AffinityReal:
		return strconv.FormatFloat(value.Real, 'f', -1, 64)
	case AffinityText:
		return value.Text
	default:
		return "NULL"
	}
}
