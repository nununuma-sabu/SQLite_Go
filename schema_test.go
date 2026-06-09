package main

import "testing"

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

func TestStorageClassString(t *testing.T) {
	if got := StorageInteger.String(); got != "INTEGER" {
		t.Fatalf("expected storage class string %q, got %q", "INTEGER", got)
	}
}
