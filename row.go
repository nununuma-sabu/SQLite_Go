package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	rowRecordFormatMagic       = "SGR2"
	legacyRowRecordFormatMagic = "SGR1"
)

var defaultRowLayout = DefaultTableSchema().RowLayout()

// Rowを画面表示用の形式で出力する。
func printRow(row Row, schema TableSchema, out io.Writer) {
	printColumns(row, schema.Columns, out)
}

func printColumns(row Row, columns []Column, out io.Writer) {
	printRows([]Row{row}, columns, out)
}

func printRows(rows []Row, columns []Column, out io.Writer) {
	widths := make([]int, len(columns))
	for i, column := range columns {
		widths[i] = len(column.Name)
	}
	for _, row := range rows {
		for i, column := range columns {
			if width := len(formatRowValue(row, column)); width > widths[i] {
				widths[i] = width
			}
		}
	}

	printTableSeparator(widths, out)
	printTableValues(columnNames(columns), widths, out)
	printTableSeparator(widths, out)
	for _, row := range rows {
		values := make([]string, 0, len(columns))
		for _, column := range columns {
			values = append(values, formatRowValue(row, column))
		}
		printTableValues(values, widths, out)
		printTableSeparator(widths, out)
	}
}

func columnNames(columns []Column) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}

	return names
}

func printTableValues(values []string, widths []int, out io.Writer) {
	fmt.Fprint(out, "|")
	for i, value := range values {
		fmt.Fprintf(out, " %-*s |", widths[i], value)
	}
	fmt.Fprintln(out)
}

func printTableSeparator(widths []int, out io.Writer) {
	fmt.Fprint(out, "+")
	for _, width := range widths {
		fmt.Fprint(out, strings.Repeat("-", width+2))
		fmt.Fprint(out, "+")
	}
	fmt.Fprintln(out)
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

func serializedRowSize(row Row, schema TableSchema) uint32 {
	size := uint32(len(rowRecordFormatMagic))
	for _, column := range schema.Columns {
		value := rowValue(row, column)
		size++
		switch value.StorageClass {
		case StorageNull:
		case StorageInteger:
			size += idSize
		case StorageReal:
			size += 8
		case StorageText:
			size += 1 + uint32(len(value.Text))
		case StorageBlob:
			size += 4 + uint32(len(value.Blob))
		}
	}

	return size
}

// Rowをページ内に保存できる固定長のバイト列へ変換する。
func serializeRow(source Row, schema TableSchema, destination []byte) {
	clear(destination)
	if len(destination) == 0 {
		return
	}

	copy(destination, rowRecordFormatMagic)
	offset := len(rowRecordFormatMagic)
	for _, column := range schema.Columns {
		value := rowValue(source, column)
		destination[offset] = byte(value.StorageClass)
		offset++

		switch value.StorageClass {
		case StorageNull:
		case StorageInteger:
			binary.LittleEndian.PutUint32(destination[offset:offset+idSize], uint32(value.Integer))
			offset += idSize
		case StorageReal:
			binary.LittleEndian.PutUint64(destination[offset:offset+8], math.Float64bits(value.Real))
			offset += 8
		case StorageText:
			destination[offset] = byte(len(value.Text))
			offset++
			copy(destination[offset:offset+len(value.Text)], value.Text)
			offset += len(value.Text)
		case StorageBlob:
			binary.LittleEndian.PutUint32(destination[offset:offset+4], uint32(len(value.Blob)))
			offset += 4
			copy(destination[offset:offset+len(value.Blob)], value.Blob)
			offset += len(value.Blob)
		}
	}
}

// 固定長のバイト列からRowを復元する。
func deserializeRow(source []byte, schema TableSchema) Row {
	if len(source) >= len(rowRecordFormatMagic) && string(source[:len(rowRecordFormatMagic)]) == rowRecordFormatMagic {
		return deserializeRecordRow(source, schema)
	}
	if len(source) >= len(legacyRowRecordFormatMagic) && string(source[:len(legacyRowRecordFormatMagic)]) == legacyRowRecordFormatMagic {
		return deserializeLegacyRecordRow(source, schema)
	}

	return deserializeFixedRow(source, schema)
}

func deserializeRecordRow(source []byte, schema TableSchema) Row {
	row := Row{
		Values: make(map[string]Value, len(schema.Columns)),
	}

	offset := len(rowRecordFormatMagic)
	for _, column := range schema.Columns {
		value := Value{}
		value.StorageClass = StorageClass(source[offset])
		offset++

		switch value.StorageClass {
		case StorageNull:
		case StorageInteger:
			value.Integer = int64(binary.LittleEndian.Uint32(source[offset : offset+idSize]))
			offset += idSize
		case StorageReal:
			value.Real = math.Float64frombits(binary.LittleEndian.Uint64(source[offset : offset+8]))
			offset += 8
		case StorageText:
			length := int(source[offset])
			offset++
			value.Text = string(source[offset : offset+length])
			offset += length
		case StorageBlob:
			length := int(binary.LittleEndian.Uint32(source[offset : offset+4]))
			offset += 4
			value.Blob = append([]byte(nil), source[offset:offset+length]...)
			offset += length
		}

		row.Values[column.Name] = value
	}

	return row
}

func deserializeLegacyRecordRow(source []byte, schema TableSchema) Row {
	row := Row{
		Values: make(map[string]Value, len(schema.Columns)),
	}

	offset := len(legacyRowRecordFormatMagic)
	for _, column := range schema.Columns {
		value := Value{}

		switch column.Affinity {
		case AffinityInteger:
			value.StorageClass = StorageInteger
			value.Integer = int64(binary.LittleEndian.Uint32(source[offset : offset+idSize]))
			offset += idSize
		case AffinityReal:
			value.StorageClass = StorageReal
			value.Real = math.Float64frombits(binary.LittleEndian.Uint64(source[offset : offset+8]))
			offset += 8
		case AffinityText:
			value.StorageClass = StorageText
			length := int(source[offset])
			offset++
			value.Text = string(source[offset : offset+length])
			offset += length
		default:
			value.StorageClass = StorageNull
		}

		row.Values[column.Name] = value
	}

	return row
}

func deserializeFixedRow(source []byte, schema TableSchema) Row {
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
		case AffinityReal:
			value.StorageClass = StorageReal
			value.Real = math.Float64frombits(binary.LittleEndian.Uint64(source[start:end]))
		case AffinityText:
			value.StorageClass = StorageText
			value.Text = readFixedString(source[start:end])
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
	if value, ok := row.Values[column.Name]; ok {
		return value
	}

	return Value{StorageClass: StorageNull}
}

func rowKey(row Row, schema TableSchema) (uint32, bool) {
	primaryKeyColumn, ok := schema.PrimaryKeyColumn()
	if !ok {
		return 0, false
	}

	value := rowValue(row, primaryKeyColumn)
	if value.StorageClass != StorageInteger || !primaryKeyColumn.ValidateIntegerValue(value.Integer) {
		return 0, false
	}

	return uint32(value.Integer), true
}

func formatRowValue(row Row, column Column) string {
	value := rowValue(row, column)
	if value.StorageClass == StorageNull {
		return "NULL"
	}

	switch column.Affinity {
	case AffinityInteger:
		return strconv.FormatInt(value.Integer, 10)
	case AffinityReal:
		return strconv.FormatFloat(value.Real, 'f', -1, 64)
	case AffinityText:
		return value.Text
	case AffinityBlob:
		return fmt.Sprintf("BLOB(%d bytes)", len(value.Blob))
	default:
		return "NULL"
	}
}
