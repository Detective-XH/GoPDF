package main

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	pdf "github.com/Detective-XH/gopdf"
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
		// synthetic-multipage.pdf declares no /PageLabels tree, so page_label falls
		// back to the 1-based page number (this green run is also the canary for that
		// no-tree assumption).
		if got, want := d.Metadata["page_label"], strconv.Itoa(i+1); got != want {
			t.Errorf("doc %d: page_label = %v, want %q (fallback: no /PageLabels)", i, got, want)
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

// TestPageLayouts verifies the structured-layout sidecar aligns one-to-one with the
// plain-text Documents (the contract a consumer relies on: layouts[i] pairs with
// docs[i]) and that each entry is non-empty, valid JSON.
func TestPageLayouts(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "corpus", "bench", "synthetic-multipage.pdf")

	docs, err := loadDocuments(path)
	if err != nil {
		t.Fatalf("loadDocuments: %v", err)
	}

	f, r, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer func() { _ = f.Close() }()

	layouts := pageLayouts(r)

	// Alignment is the contract — not a magic page count.
	if len(layouts) != len(docs) {
		t.Fatalf("pageLayouts/Documents misaligned: %d layouts vs %d docs", len(layouts), len(docs))
	}
	for i, layout := range layouts {
		if len(layout) == 0 {
			t.Errorf("page %d: layout is empty, want non-empty JSON", i)
		}
		if !json.Valid(layout) {
			t.Errorf("page %d: layout is not valid JSON", i)
		}
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

// TestLoadDocumentsPageLabelsFromTree verifies the loader surfaces the document's
// OWN printed page labels (not the 1-based position) when it carries a /PageLabels
// tree. The IRS Pub 55-B excerpt prints labels "32".."47" (/S /D /St 32), so the
// printed label is a genuine offset from the 0-based page index — proving page_label
// is now distinct from page, not a tautology.
func TestLoadDocumentsPageLabelsFromTree(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "corpus", "tables", "irs-p55b-2025-excerpt.pdf")
	docs, err := loadDocuments(path)
	if err != nil {
		t.Fatalf("loadDocuments: %v", err)
	}
	want := []string{
		"32", "33", "34", "35", "36", "37", "38", "39",
		"40", "41", "42", "43", "44", "45", "46", "47",
	}
	if len(docs) != len(want) {
		t.Fatalf("docs: got %d, want %d", len(docs), len(want))
	}
	for i, d := range docs {
		if got := d.Metadata["page_label"]; got != want[i] {
			t.Errorf("doc %d: page_label = %v, want %q (printed label, not position)", i, got, want[i])
		}
		if got := d.Metadata["page"]; got != i {
			t.Errorf("doc %d: page = %v, want %d (0-based position, distinct from label)", i, got, i)
		}
	}
}

// TestPageLabelResolution table-tests the pure pageLabel helper across all four
// branches — the uncovered-empty-string and out-of-range fallbacks are unreachable
// through the fixture tests (the IRS fixture covers every page; synthetic has no
// tree), so they are pinned here directly.
func TestPageLabelResolution(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		n      int
		want   string
	}{
		{"nil_tree_falls_back_to_number", nil, 3, "3"},
		{"covered_uses_printed_label", []string{"i", "ii", "iii"}, 2, "ii"},
		{"uncovered_empty_falls_back", []string{"", "", "5"}, 1, "1"},
		{"out_of_range_falls_back", []string{"i"}, 5, "5"},
	}
	for _, c := range cases {
		if got := pageLabel(c.labels, c.n); got != c.want {
			t.Errorf("%s: pageLabel(%v, %d) = %q, want %q", c.name, c.labels, c.n, got, c.want)
		}
	}
}
