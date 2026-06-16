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

// printRow は1行を現在のスキーマ順に表形式で出力する。
// rowが出力対象、schemaが表示列の順序、outが書き込み先を表す。
func printRow(row Row, schema TableSchema, out io.Writer) {
	printColumns(row, schema.Columns, out)
}

// printColumns は1行から指定されたカラムだけを表形式で出力する。
// columnsの順序が表示順になり、戻り値は持たずoutへ直接書き込む。
func printColumns(row Row, columns []Column, out io.Writer) {
	printRows([]Row{row}, columns, out)
}

// printRows は複数Rowを指定カラム順の表形式へ変換して出力する。
// rowsが空の場合は何も出力せず、戻り値は持たない。
func printRows(rows []Row, columns []Column, out io.Writer) {
	valueRows := make([][]Value, 0, len(rows))
	for _, row := range rows {
		values := make([]Value, 0, len(columns))
		for _, column := range columns {
			values = append(values, rowValue(row, column))
		}
		valueRows = append(valueRows, values)
	}

	printValueRows(columnNames(columns), valueRows, out)
}

// printValueRows は表示ヘッダと値行からASCII表を出力する。
// headersは列名、rowsは表示値の2次元配列で、列幅は内容から自動計算する。
func printValueRows(headers []string, rows [][]Value, out io.Writer) {
	if len(headers) == 0 {
		return
	}

	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, value := range row {
			if width := len(formatValue(value)); width > widths[i] {
				widths[i] = width
			}
		}
	}

	printTableSeparator(widths, out)
	printTableValues(headers, widths, out)
	printTableSeparator(widths, out)
	for _, row := range rows {
		values := make([]string, 0, len(headers))
		for _, value := range row {
			values = append(values, formatValue(value))
		}
		printTableValues(values, widths, out)
		printTableSeparator(widths, out)
	}
}

// columnNames はColumn一覧から表示用の列名だけを取り出す。
// 戻り値の順序は入力columnsの順序と同じ。
func columnNames(columns []Column) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}

	return names
}

// printTableValues は1行分の文字列値を指定幅でパディングして出力する。
// widthsは各列の最小幅で、戻り値は持たずoutへ書き込む。
func printTableValues(values []string, widths []int, out io.Writer) {
	fmt.Fprint(out, "|")
	for i, value := range values {
		fmt.Fprintf(out, " %-*s |", widths[i], value)
	}
	fmt.Fprintln(out)
}

// printTableSeparator は表の区切り線を列幅に合わせて出力する。
// widthsの各要素に左右余白分を加えた長さで線を作る。
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

// serializedRowSize はRowを現在の可変長レコード形式で保存するためのバイト数を返す。
// rowの実値とschemaの列順からNULL/数値/文字列/BLOBの格納サイズを合計する。
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

// serializeRow はRowをページ内に保存できるバイト列へ変換する。
// sourceが保存対象、schemaが列順と型、destinationが書き込み先バッファで、戻り値は持たない。
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

// deserializeRow は保存済みバイト列からRowを復元する。
// sourceの先頭マジックで現行可変長形式、旧可変長形式、固定長形式を判定して適切に読む。
func deserializeRow(source []byte, schema TableSchema) Row {
	if len(source) >= len(rowRecordFormatMagic) && string(source[:len(rowRecordFormatMagic)]) == rowRecordFormatMagic {
		return deserializeRecordRow(source, schema)
	}
	if len(source) >= len(legacyRowRecordFormatMagic) && string(source[:len(legacyRowRecordFormatMagic)]) == legacyRowRecordFormatMagic {
		return deserializeLegacyRecordRow(source, schema)
	}

	return deserializeFixedRow(source, schema)
}

// deserializeRecordRow は現行の可変長レコード形式からRowを復元する。
// schemaに追加された列がsourceに存在しない場合は、スキーマ変更後の既存行としてNULLを補う。
func deserializeRecordRow(source []byte, schema TableSchema) Row {
	row := Row{
		Values: make(map[string]Value, len(schema.Columns)),
	}

	offset := len(rowRecordFormatMagic)
	for _, column := range schema.Columns {
		value := Value{}
		if offset >= len(source) {
			row.Values[column.Name] = Value{StorageClass: StorageNull}
			continue
		}
		value.StorageClass = StorageClass(source[offset])
		offset++

		switch value.StorageClass {
		case StorageNull:
		case StorageInteger:
			if offset+idSize > len(source) {
				row.Values[column.Name] = Value{StorageClass: StorageNull}
				offset = len(source)
				continue
			}
			value.Integer = int64(binary.LittleEndian.Uint32(source[offset : offset+idSize]))
			offset += idSize
		case StorageReal:
			if offset+8 > len(source) {
				row.Values[column.Name] = Value{StorageClass: StorageNull}
				offset = len(source)
				continue
			}
			value.Real = math.Float64frombits(binary.LittleEndian.Uint64(source[offset : offset+8]))
			offset += 8
		case StorageText:
			if offset >= len(source) {
				row.Values[column.Name] = Value{StorageClass: StorageNull}
				continue
			}
			length := int(source[offset])
			offset++
			if offset+length > len(source) {
				row.Values[column.Name] = Value{StorageClass: StorageNull}
				offset = len(source)
				continue
			}
			value.Text = string(source[offset : offset+length])
			offset += length
		case StorageBlob:
			if offset+4 > len(source) {
				row.Values[column.Name] = Value{StorageClass: StorageNull}
				offset = len(source)
				continue
			}
			length := int(binary.LittleEndian.Uint32(source[offset : offset+4]))
			offset += 4
			if offset+length > len(source) {
				row.Values[column.Name] = Value{StorageClass: StorageNull}
				offset = len(source)
				continue
			}
			value.Blob = append([]byte(nil), source[offset:offset+length]...)
			offset += length
		}

		row.Values[column.Name] = value
	}

	return row
}

// deserializeLegacyRecordRow は旧可変長形式の行データを現在のRowへ復元する。
// 戻り値はschemaの列名をキーにしたValueマップを持つRow。
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

// deserializeFixedRow は初期実装の固定長行形式からRowを復元する。
// 旧DBファイル互換のために残しており、schemaのRowLayoutで列位置を決める。
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

// rowValue はRowから指定Columnの値を取り出す。
// 値が存在しない場合は、ALTER TABLE後の未保存列などとしてNULLを返す。
func rowValue(row Row, column Column) Value {
	if value, ok := row.Values[column.Name]; ok {
		return value
	}

	return Value{StorageClass: StorageNull}
}

// rowKey はRowからスキーマの主キー値をuint32として取り出す。
// 戻り値のboolは主キー列が存在し、INTEGERかつ有効範囲にある場合だけtrueになる。
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
	return formatValue(rowValue(row, column))
}

// formatValue はValueを画面表示用の文字列へ変換する。
// NULLやBLOBなどもSELECT結果で読める表記にして返す。
func formatValue(value Value) string {
	if value.StorageClass == StorageNull {
		return "NULL"
	}

	switch value.StorageClass {
	case StorageInteger:
		return strconv.FormatInt(value.Integer, 10)
	case StorageReal:
		return strconv.FormatFloat(value.Real, 'f', -1, 64)
	case StorageText:
		return value.Text
	case StorageBlob:
		return fmt.Sprintf("BLOB(%d bytes)", len(value.Blob))
	default:
		return "NULL"
	}
}
