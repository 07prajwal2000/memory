package bm25

import (
	"testing"
)

func TestBM25Basic(t *testing.T) {
	idx := NewIndex(1.2, 0.75)
	idx.AddDocument(Document{ID: "d1", DocID: "d1", Text: "The quick brown fox jumps over the lazy dog."})
	idx.AddDocument(Document{ID: "d2", DocID: "d2", Text: "Database query optimization and SQLite virtual tables."})
	idx.AddDocument(Document{ID: "d3", DocID: "d3", Text: "Brown bears and foxes in northern wildlife reserves."})

	hits := idx.Search("foxes and brown animals", 10)
	if len(hits) == 0 {
		t.Fatalf("expected hits, got none")
	}
	// d1 and d3 contain "brown" / "fox", d2 does not
	if hits[0].ID != "d1" && hits[0].ID != "d3" {
		t.Errorf("expected d1 or d3 top hit, got %s", hits[0].ID)
	}

	hits2 := idx.Search("SQLite optimization", 10)
	if len(hits2) == 0 || hits2[0].ID != "d2" {
		t.Errorf("expected d2 top hit for sqlite optimization, got %v", hits2)
	}
}
