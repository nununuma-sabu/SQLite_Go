package main

import (
	"strings"
	"testing"
)

func TestInferTypeAffinity(t *testing.T) {
	tests := []struct {
		name         string
		declaredType string
		want         TypeAffinity
	}{
		{name: "empty type is blob", declaredType: "", want: AffinityBlob},
		{name: "int is integer", declaredType: "INT", want: AffinityInteger},
		{name: "varchar is text", declaredType: "VARCHAR(50)", want: AffinityText},
		{name: "text is text", declaredType: "TEXT", want: AffinityText},
		{name: "blob is blob", declaredType: "BLOB", want: AffinityBlob},
		{name: "real is real", declaredType: "REAL", want: AffinityReal},
		{name: "double is real", declaredType: "DOUBLE PRECISION", want: AffinityReal},
		{name: "decimal is numeric", declaredType: "DECIMAL(10,5)", want: AffinityNumeric},
		{name: "boolean is numeric", declaredType: "BOOLEAN", want: AffinityNumeric},
		{name: "date is numeric", declaredType: "DATE", want: AffinityNumeric},
		{name: "case insensitive", declaredType: "tinyint", want: AffinityInteger},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferTypeAffinity(tt.declaredType); got != tt.want {
				t.Fatalf("expected affinity %s, got %s", tt.want, got)
			}
		})
	}
}

func TestNewColumnSetsAffinity(t *testing.T) {
	column := NewColumn("age", "INT")

	if column.Name != "age" {
		t.Fatalf("expected name %q, got %q", "age", column.Name)
	}
	if column.DeclaredType != "INT" {
		t.Fatalf("expected declared type %q, got %q", "INT", column.DeclaredType)
	}
	if column.Affinity != AffinityInteger {
		t.Fatalf("expected affinity %s, got %s", AffinityInteger, column.Affinity)
	}
}

func TestDefaultTableSchema(t *testing.T) {
	schema := DefaultTableSchema()

	if schema.Name != defaultTableName {
		t.Fatalf("expected schema name %q, got %q", defaultTableName, schema.Name)
	}
	if len(schema.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(schema.Columns))
	}

	idColumn, ok := schema.PrimaryKeyColumn()
	if !ok {
		t.Fatal("expected primary key column")
	}
	if idColumn.Name != idColumnName {
		t.Fatalf("expected primary key %q, got %q", idColumnName, idColumn.Name)
	}
	if idColumn.Affinity != AffinityInteger {
		t.Fatalf("expected id affinity %s, got %s", AffinityInteger, idColumn.Affinity)
	}

	usernameColumn, ok := schema.Column(usernameColumnName)
	if !ok {
		t.Fatalf("expected column %q", usernameColumnName)
	}
	if usernameColumn.Affinity != AffinityText {
		t.Fatalf("expected username affinity %s, got %s", AffinityText, usernameColumn.Affinity)
	}
	if usernameColumn.MaxLength != columnUsernameSize {
		t.Fatalf("expected username max length %d, got %d", columnUsernameSize, usernameColumn.MaxLength)
	}

	emailColumn, ok := schema.Column(emailColumnName)
	if !ok {
		t.Fatalf("expected column %q", emailColumnName)
	}
	if emailColumn.Affinity != AffinityText {
		t.Fatalf("expected email affinity %s, got %s", AffinityText, emailColumn.Affinity)
	}
	if emailColumn.MaxLength != columnEmailSize {
		t.Fatalf("expected email max length %d, got %d", columnEmailSize, emailColumn.MaxLength)
	}
}

func TestDefaultTableSchemaRowLayout(t *testing.T) {
	layout := DefaultTableSchema().RowLayout()

	if layout.Size != 293 {
		t.Fatalf("expected row layout size %d, got %d", 293, layout.Size)
	}

	tests := []struct {
		columnName string
		start      uint32
		end        uint32
	}{
		{columnName: idColumnName, start: 0, end: 4},
		{columnName: usernameColumnName, start: 4, end: 37},
		{columnName: emailColumnName, start: 37, end: 293},
	}

	for _, tt := range tests {
		t.Run(tt.columnName, func(t *testing.T) {
			start, end, ok := layout.ColumnRange(tt.columnName)
			if !ok {
				t.Fatalf("expected column %q in row layout", tt.columnName)
			}
			if start != tt.start || end != tt.end {
				t.Fatalf("expected range [%d:%d], got [%d:%d]", tt.start, tt.end, start, end)
			}
		})
	}
}

func TestColumnValidation(t *testing.T) {
	idColumn := NewColumn("id", "INTEGER")
	if !idColumn.ValidateIntegerValue(1) {
		t.Fatal("expected positive integer to be valid")
	}
	if idColumn.ValidateIntegerValue(-1) {
		t.Fatal("expected negative integer to be invalid")
	}

	usernameColumn := NewColumn("username", "TEXT")
	usernameColumn.MaxLength = columnUsernameSize
	if !usernameColumn.ValidateTextValue("alice") {
		t.Fatal("expected text within limit to be valid")
	}
	if usernameColumn.ValidateTextValue(strings.Repeat("a", columnUsernameSize+1)) {
		t.Fatal("expected text over limit to be invalid")
	}
}

func TestStorageClassString(t *testing.T) {
	if got := StorageInteger.String(); got != "INTEGER" {
		t.Fatalf("expected storage class string %q, got %q", "INTEGER", got)
	}
}
