package main

import (
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// wantKeys is the exact metadata key set every Document must carry — the
// LangChain/LlamaIndex-aligned loader contract. The test asserts this set exactly
// (no missing, no extra), enforcing the stable-key-set guarantee.
var wantKeys = []string{
	"page", "page_label", "total_pages",
	"title", "author", "subject", "creator", "producer",
	"creationdate", "moddate",
	"extraction_confidence",
}

func TestLoadDocumentsMetadataContract(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "corpus", "bench", "synthetic-multipage.pdf")
	docs, err := loadDocuments(path)
	if err != nil {
		t.Fatalf("loadDocuments: %v", err)
	}

	const wantPages = 24 // synthetic-multipage.pdf is a 24-page fixture.
	if len(docs) != wantPages {
		t.Fatalf("got %d documents, want %d (one per page)", len(docs), wantPages)
	}

	for i, d := range docs {
		assertExactKeys(t, i, d.Metadata)

		if got := d.Metadata["page"]; got != i {
			t.Errorf("doc %d: page = %v, want %d (0-based, contiguous)", i, got, i)
		}
		if got := d.Metadata["total_pages"]; got != wantPages {
			t.Errorf("doc %d: total_pages = %v, want %d", i, got, wantPages)
		}
		if got, want := d.Metadata["page_label"], strconv.Itoa(i+1); got != want {
			t.Errorf("doc %d: page_label = %v, want %q", i, got, want)
		}
		if got := d.Metadata["extraction_confidence"]; got != "text" {
			t.Errorf("doc %d: extraction_confidence = %v, want %q", i, got, "text")
		}
	}
}

func TestLoadDocumentsDegradedPageNotAborted(t *testing.T) {
	// A page whose strict extraction fails must still be emitted with its routing
	// signal, never abort the whole document — surfacing that signal is the loader's
	// purpose. malformed-truncated.pdf is a 1-page fixture that classifies degraded
	// and whose strict GetPlainText errors.
	path := filepath.Join("..", "..", "testdata", "corpus", "signals", "malformed-truncated.pdf")
	docs, err := loadDocuments(path)
	if err != nil {
		t.Fatalf("loadDocuments aborted on a degraded page: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d documents, want 1", len(docs))
	}
	d := docs[0]
	assertExactKeys(t, 0, d.Metadata)
	if got := d.Metadata["extraction_confidence"]; got != "degraded" {
		t.Errorf("extraction_confidence = %v, want %q", got, "degraded")
	}
	if d.PageContent != "" {
		t.Errorf("PageContent = %q, want empty (degraded page)", d.PageContent)
	}
}

func assertExactKeys(t *testing.T, doc int, meta map[string]any) {
	t.Helper()
	got := make([]string, 0, len(meta))
	for k := range meta {
		got = append(got, k)
	}
	want := append([]string(nil), wantKeys...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("doc %d: metadata keys = %v, want %v", doc, got, want)
	}
	for j := range want {
		if got[j] != want[j] {
			t.Fatalf("doc %d: metadata keys = %v, want %v", doc, got, want)
		}
	}
}
