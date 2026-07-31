package service

import (
	"reflect"
	"testing"

	"sekaitext/backend/internal/model"
)

func TestDecodeGlossarySnapshotRequiresAuthoritativeArrays(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "null root", body: `null`},
		{name: "empty object", body: `{}`},
		{name: "missing entries", body: `{"appellations":[]}`},
		{name: "missing appellations", body: `{"entries":[]}`},
		{name: "null entries", body: `{"entries":null,"appellations":[]}`},
		{name: "null appellations", body: `{"entries":[],"appellations":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeGlossarySnapshot([]byte(tt.body)); err == nil {
				t.Fatalf("DecodeGlossarySnapshot(%s) succeeded; incomplete snapshot must be rejected", tt.body)
			}
		})
	}
}

func TestDecodeGlossarySnapshotAcceptsExplicitEmptyArrays(t *testing.T) {
	got, err := DecodeGlossarySnapshot([]byte(`{"entries":[],"appellations":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := model.GlossaryData{
		Entries:      []model.GlossaryEntry{},
		Appellations: []model.Appellation{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestDecodeGlossarySnapshotDecodesOptionalGrammar(t *testing.T) {
	got, err := DecodeGlossarySnapshot([]byte(`{
		"entries":[{"id":"entry-1","source":"ミク","translation":"未来","category":"人名","origin":"remote"}],
		"appellations":[],
		"grammar":[{"id":"grammar-1","item":"〜ながら"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].ID != "entry-1" {
		t.Fatalf("entries were not decoded: %#v", got.Entries)
	}
	if len(got.Grammar) != 1 || got.Grammar[0].ID != "grammar-1" {
		t.Fatalf("grammar was not decoded: %#v", got.Grammar)
	}
}
