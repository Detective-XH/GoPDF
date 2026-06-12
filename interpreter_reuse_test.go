package pdf

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// Regression guards for the single-interpret pageModel: it derives both the
// routing summary and the line geometry from one Content (summaryFromContent +
// linesFromContentRecovered) instead of interpreting the content sink twice.
// These lock the two properties that change relies on:
//   1. the words derived from Lines() are exactly the words Words() yields, and
//   2. a page whose content stream panics still degrades to valid JSON.

// wordMultiset returns a string→count multiset of a word slice, order-independent.
func wordMultiset(ws []Word) map[string]int {
	m := make(map[string]int, len(ws))
	for _, w := range ws {
		m[w.S]++
	}
	return m
}

// TestWordsLinesCountEquivalence asserts, across the whole golden corpus and per
// page, that flatten(Lines().Words) is the same multiset of strings as Words().
// Words() is reading-order and Lines() is column-major, so the ORDER differs —
// the SET must not. DebugJSON's summary word count is derived from this shared
// Content, so a divergence here would silently change HasText/WordCount/sparse
// routing on multi-column pages.
func TestWordsLinesCountEquivalence(t *testing.T) {
	for _, e := range corpusManifest {
		t.Run(e.Path, func(t *testing.T) {
			r := loadCorpus(t, e)
			for i, p := range r.Pages() {
				words, werr := p.Words()
				lines, lerr := p.Lines()
				if werr != nil || lerr != nil {
					// A page that panics in a sink degrades to (nil, err) in both
					// by design; there is nothing to compare on such a page.
					continue
				}
				var flat []Word
				for _, ln := range lines {
					flat = append(flat, ln.Words...)
				}
				if len(words) != len(flat) {
					t.Errorf("page %d: Words()=%d words, flatten(Lines())=%d", i, len(words), len(flat))
				}
				if want, got := wordMultiset(words), wordMultiset(flat); !reflect.DeepEqual(want, got) {
					t.Errorf("page %d: Words() vs flatten(Lines()) string multiset differs", i)
				}
			}
		})
	}
}

// TestSummarizeMatchesSummaryFromContent locks the DebugJSON word-reuse
// optimization: the summary pageModel builds from flatten(Lines) must equal the
// summary the from-Content path builds. Per-page value fields must match, and
// the two paths' emitted warning stores must be identical — which locks
// sparse_text / image_only_page, the routing signals that read word geometry the
// field compare alone cannot see. Two fresh Readers keep the deduped stores from
// cross-contaminating.
func TestSummarizeMatchesSummaryFromContent(t *testing.T) {
	for _, e := range corpusManifest {
		t.Run(e.Path, func(t *testing.T) {
			ra := loadCorpus(t, e) // from-Content path
			rb := loadCorpus(t, e) // flatten-Lines path
			for i := 1; i <= ra.NumPage(); i++ {
				pa, pb := ra.Page(i), rb.Page(i)
				if pa.V.IsNull() {
					continue
				}
				want, werr := pa.summaryFromContent(pa.Content()) // re-bands
				lines, lerr := linesFromContentRecovered(pb.Content())
				got, gerr := pb.summarize(wordsFromLines(lines), lerr) // reuses lines
				if (werr == nil) != (gerr == nil) {
					t.Fatalf("page %d: err mismatch: from-content %v vs from-lines %v", i, werr, gerr)
				}
				if want.Page != got.Page || want.HasText != got.HasText ||
					want.WordCount != got.WordCount || want.ImageCount != got.ImageCount ||
					want.ImageCoverage != got.ImageCoverage {
					t.Errorf("page %d: summary fields differ:\n from-content %+v\n from-lines   %+v", i, want, got)
				}
			}
			// End-to-end lock on sparse_text / image_only_page: the page-scoped
			// routing warnings the two paths emit must be byte-identical.
			if !reflect.DeepEqual(ra.Warnings(), rb.Warnings()) {
				t.Errorf("warning stores differ between paths:\n from-content %+v\n flatten-Lines %+v",
					ra.Warnings(), rb.Warnings())
			}
		})
	}
}

// TestDebugJSONPanickingPageDegrades exercises the panic-recovery contract: the
// recovers that ExtractionSummary()/Lines() carried now live in
// summaryFromContent / linesFromContentRecovered, which pageModel calls. A page
// whose content stream panics must still yield VALID JSON from DebugJSON — never a
// panic to the caller — exactly as the pre-refactor two-call pageModel did. The
// vector is a malformed `cm` (3 operands where the image scanner requires 6, and
// scanPageImages has no internal recover). This is the only test that drives the
// panic path through pageModel.
func TestDebugJSONPanickingPageDegrades(t *testing.T) {
	// `1 2 3 cm` makes the image scan panic; the trailing text is incidental.
	data := buildTextPDF("1 2 3 cm BT /F1 12 Tf 72 720 Td (text) Tj ET")

	// Precondition: the page really panics in the summary path (image scan),
	// surfaced as an error — otherwise the assertions below would pass vacuously.
	if _, err := mustOpenBytes(t, data).Page(1).ExtractionSummary(); err == nil {
		t.Fatal("precondition failed: expected ExtractionSummary to error on the panicking page")
	}

	// Page.DebugJSON: valid JSON, no panic to the caller.
	pageRaw, err := mustOpenBytes(t, data).Page(1).DebugJSON()
	if err != nil {
		t.Fatalf("Page.DebugJSON: %v", err)
	}
	if !json.Valid(pageRaw) {
		t.Fatalf("Page.DebugJSON returned invalid JSON: %s", pageRaw)
	}

	// Reader.DebugJSON: valid JSON, and deterministic output + warnings across
	// repeated calls (a recovered panic must not leave torn state).
	r := mustOpenBytes(t, data)
	doc1, err := r.DebugJSON()
	if err != nil {
		t.Fatalf("Reader.DebugJSON: %v", err)
	}
	if !json.Valid(doc1) {
		t.Fatalf("Reader.DebugJSON returned invalid JSON: %s", doc1)
	}
	w1 := r.Warnings()
	doc2, err := r.DebugJSON()
	if err != nil {
		t.Fatalf("Reader.DebugJSON (2nd call): %v", err)
	}
	if !bytes.Equal(doc1, doc2) {
		t.Fatal("Reader.DebugJSON not deterministic across calls")
	}
	if !reflect.DeepEqual(w1, r.Warnings()) {
		t.Fatal("Reader.Warnings not deterministic across DebugJSON calls")
	}
}
