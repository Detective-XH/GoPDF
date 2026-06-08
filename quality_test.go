package pdf

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

// Extraction Quality Score (baseline): Page.ExtractionSignal and
// Reader.DocumentSummary. Per-fixture signal characterization for the signals/
// corpus lives in corpus_signals_test.go (signalExpectations + assertSignalValue);
// this file covers the document-level aggregation, edge cases, determinism, and
// the document-scoped warning reducer. Synthetic byte-level fixtures here were
// built and run before the wider suite (plans-conventions fixture-risk rule).

// openSynthetic opens a buildPDFFromObjects fixture or fails the test.
func openSynthetic(t *testing.T, objs []string) *Reader {
	t.Helper()
	r, err := OpenBytes(buildPDFFromObjects(objs))
	if err != nil {
		t.Fatalf("OpenBytes(synthetic): %v", err)
	}
	return r
}

// contentObj formats a content-stream object with the correct /Length.
func contentObj(stream string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
}

// onePageText is a minimal one-page PDF whose single page draws the given text
// run with a standard Helvetica font (no decode warnings).
func onePageText(t *testing.T, run string) *Reader {
	t.Helper()
	return openSynthetic(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		contentObj(run),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

// TestExtractionSignalNullPage: a null/absent page reports SignalEmpty and never
// panics (finding: don't let an absent page crash the routing call).
func TestExtractionSignalNullPage(t *testing.T) {
	r := onePageText(t, "BT /F1 12 Tf 72 700 Td (only page) Tj ET")
	if got := r.Page(1 << 20).ExtractionSignal(); got != SignalEmpty {
		t.Errorf("absent page signal = %q, want %q", got, SignalEmpty)
	}
}

// TestExtractionSignalEmptyPage: a page with no /Contents classifies SignalEmpty
// (exercises the 4th enum value deterministically).
func TestExtractionSignalEmptyPage(t *testing.T) {
	r := openSynthetic(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
	})
	if got := r.Page(1).ExtractionSignal(); got != SignalEmpty {
		t.Errorf("no-/Contents page signal = %q, want %q", got, SignalEmpty)
	}
}

// TestDocumentSummaryEmptyDoc: a /Count 0 document yields a zero-value-shaped
// DocumentSummary and does not panic.
func TestDocumentSummaryEmptyDoc(t *testing.T) {
	r := openSynthetic(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	})
	ds := r.DocumentSummary()
	if ds.TotalPages != 0 || ds.Pages != nil || ds.Warnings != nil {
		t.Errorf("empty doc summary = %+v, want zero-shaped", ds)
	}
	if ds.TextPages+ds.ImageOnlyPages+ds.EmptyPages+ds.DegradedPages != 0 {
		t.Errorf("empty doc has nonzero tallies: %+v", ds)
	}
}

// TestDocumentSummaryNullSlots: null page slots are skipped (not recorded as
// SignalEmpty) and reported via WarningNullPageSlot; len(Pages) < TotalPages.
func TestDocumentSummaryNullSlots(t *testing.T) {
	run := "BT /F1 12 Tf 72 700 Td (page four) Tj ET"
	r := openSynthetic(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 4 >>",    // declares 4 pages
		"<< /Type /Pages /Parent 2 0 R /Kids [] /Count 3 >>", // slots 1-3 are null
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 5 0 R /Resources << /Font << /F1 6 0 R >> >> >>",
		contentObj(run),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
	ds := r.DocumentSummary()
	if ds.TotalPages != 4 {
		t.Errorf("TotalPages = %d, want 4", ds.TotalPages)
	}
	if len(ds.Pages) != 1 {
		t.Fatalf("len(Pages) = %d, want 1 (null slots must be skipped, not recorded)", len(ds.Pages))
	}
	if ds.Pages[0].Page != 4 || ds.Pages[0].Signal != SignalText {
		t.Errorf("Pages[0] = %+v, want {Page:4 Signal:text}", ds.Pages[0])
	}
	if ds.EmptyPages != 0 {
		t.Errorf("EmptyPages = %d, want 0 (a null slot must never become SignalEmpty)", ds.EmptyPages)
	}
	nulls := 0
	for _, w := range r.Warnings() {
		if w.Code == WarningNullPageSlot {
			nulls++
		}
	}
	if nulls != 3 {
		t.Errorf("WarningNullPageSlot count = %d, want 3 (slots 1-3)", nulls)
	}
}

// TestDocumentSummaryCleanCorpus locks the probed per-page tally on a real
// all-text fixture: cjk/irs-p850-zh-hant.pdf is 22 pages, every page text. This
// also locks the GetPlainText-vs-Words text-authority agreement on real content.
func TestDocumentSummaryCleanCorpus(t *testing.T) {
	data, err := os.ReadFile(corpusPath("cjk/irs-p850-zh-hant.pdf"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	ds := r.DocumentSummary()
	if ds.TotalPages != 22 || len(ds.Pages) != 22 {
		t.Fatalf("TotalPages=%d len(Pages)=%d, want 22/22", ds.TotalPages, len(ds.Pages))
	}
	if ds.TextPages != 22 || ds.ImageOnlyPages != 0 || ds.EmptyPages != 0 || ds.DegradedPages != 0 {
		t.Errorf("tally = {text:%d image:%d empty:%d degraded:%d}, want all 22 text",
			ds.TextPages, ds.ImageOnlyPages, ds.EmptyPages, ds.DegradedPages)
	}
	if ds.TextPages+ds.ImageOnlyPages+ds.EmptyPages+ds.DegradedPages != len(ds.Pages) {
		t.Errorf("tallies do not sum to len(Pages)=%d", len(ds.Pages))
	}
	for i, ps := range ds.Pages {
		if ps.Page != i+1 || ps.Signal != SignalText {
			t.Errorf("Pages[%d] = %+v, want {Page:%d Signal:text}", i, ps, i+1)
		}
	}
}

// TestDocumentSummaryDeterministic: two passes on the same Reader are identical.
func TestDocumentSummaryDeterministic(t *testing.T) {
	r := onePageText(t, "BT /F1 12 Tf 72 700 Td (deterministic) Tj ET")
	a, b := r.DocumentSummary(), r.DocumentSummary()
	if !reflect.DeepEqual(a, b) {
		t.Errorf("DocumentSummary not deterministic:\n%+v\n%+v", a, b)
	}
}

// TestDocumentSummaryConcurrent is the concurrency-contract test for the new
// aggregation: GOMAXPROCS goroutines call DocumentSummary on one shared Reader
// (cold cache), all compared to a single-goroutine baseline from a separate
// Reader. Mirrors TestExtractionSummaryDeterministicConcurrent / the takeSnapshot
// discipline; DocumentSummary also joins TestConcurrentExtraction via takeSnapshot.
func TestDocumentSummaryConcurrent(t *testing.T) {
	data, err := os.ReadFile(corpusPath("cjk/irs-p850-zh-hant.pdf"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rBase, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes (baseline): %v", err)
	}
	want := rBase.DocumentSummary()
	if want.TotalPages == 0 || len(want.Pages) == 0 {
		t.Fatalf("baseline summary empty: %+v", want)
	}
	rShared, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes (shared): %v", err)
	}
	var wg sync.WaitGroup
	for w := range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			if got := rShared.DocumentSummary(); !reflect.DeepEqual(got, want) {
				t.Errorf("worker %d: concurrent DocumentSummary diverged from baseline", w)
			}
		})
	}
	wg.Wait()
}

// TestDocumentSummaryDocScopedWarnings proves the document-level confidence
// reducer: a page whose Tf names a font missing from /Resources fires the
// document-scoped WarningMissingGlyphMapping yet still extracts text. The page is
// SignalText, but the document is distinguishable from a clean one via Warnings
// (every entry Page==0). This is the honest baseline form of the "distinct
// signals" criterion for low-confidence documents; the encoding-specific corpus
// fixtures (missing /ToUnicode, fallback) arrive with later fixture work.
func TestDocumentSummaryDocScopedWarnings(t *testing.T) {
	missing := openSynthetic(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << >> >> >>",
		contentObj("BT /Missing 12 Tf 72 700 Td (hi there) Tj ET"),
	})
	ds := missing.DocumentSummary()
	if len(ds.Pages) != 1 || ds.Pages[0].Signal != SignalText {
		t.Fatalf("missing-font page = %+v, want one SignalText page", ds.Pages)
	}
	found := false
	for _, w := range ds.Warnings {
		if w.Page != 0 {
			t.Errorf("DocumentSummary.Warnings has page-scoped entry %+v (want doc-scoped only)", w)
		}
		if w.Code == WarningMissingGlyphMapping {
			found = true
		}
	}
	if !found {
		t.Errorf("DocumentSummary.Warnings = %+v, want WarningMissingGlyphMapping", ds.Warnings)
	}

	// A clean document is distinguishable: no doc-scoped reducer.
	clean := onePageText(t, "BT /F1 12 Tf 72 700 Td (clean) Tj ET")
	if cds := clean.DocumentSummary(); cds.Warnings != nil {
		t.Errorf("clean doc Warnings = %+v, want nil (distinguishable from low-confidence)", cds.Warnings)
	}
}

// TestExtractionSignalDistinct exercises all four baseline signal values.
func TestExtractionSignalDistinct(t *testing.T) {
	cases := []struct {
		name string
		open func(*testing.T) *Reader
		want ExtractionSignal
	}{
		{"image_only", func(t *testing.T) *Reader { return openCorpus(t, "signals/image-full-bleed.pdf") }, SignalImageOnly},
		{"text", func(t *testing.T) *Reader { return openCorpus(t, "signals/image-thumbnail-text.pdf") }, SignalText},
		{"degraded", func(t *testing.T) *Reader { return openCorpus(t, "signals/malformed-truncated.pdf") }, SignalDegraded},
		{"empty", func(t *testing.T) *Reader {
			return openSynthetic(t, []string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
			})
		}, SignalEmpty},
	}
	seen := map[ExtractionSignal]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.open(t).Page(1).ExtractionSignal(); got != c.want {
				t.Errorf("signal = %q, want %q", got, c.want)
			}
		})
		seen[c.want] = true
	}
	if len(seen) != 4 {
		t.Errorf("expected 4 distinct signal values, exercised %d", len(seen))
	}
}

// openCorpus opens a corpus fixture by relative path.
func openCorpus(t *testing.T, rel string) *Reader {
	t.Helper()
	data, err := os.ReadFile(corpusPath(rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes %s: %v", rel, err)
	}
	return r
}
