package pdf

import (
	"strings"
	"testing"
)

// TestBEACIDFontWidthLabelDecode guards the effectiveWidth DW=0 fix end-to-end via
// the Page.Words() path. The BEA fixture (bea-scb-gdp-2024-t1.pdf) contains a
// Type0 Bold-Cambria font whose CIDFont descendant has DW=0. Before the fix, every
// CID absent from /W received a zero advance, stacking glyphs at the same position
// and scrambling all row labels. After the fix, cidWidth returns 1000 for absent
// CIDs so the layout engine spaces glyphs correctly.
//
// GetPlainText would be vacuous here: it decodes in stream order and bypasses the
// layout widths entirely. Only Words() exercises layoutComposite → cidWidth, which
// is the actual fix point (glyphs must be sorted by X to form readable words).
//
// Ground truth: verified from Page(1).Words() joined with "|" on the fixed tree:
// "Personal|consumption|expenditures" and "Gross|private|domestic|investment"
// appear in correct order; "esxp" (characteristic scramble of pre-fix stacking)
// is absent.
func TestBEACIDFontWidthLabelDecode(t *testing.T) {
	fh, r, err := Open("testdata/corpus/tables/bea-scb-gdp-2024-t1.pdf")
	if err != nil {
		t.Fatalf("open BEA fixture: %v", err)
	}
	defer func() { _ = fh.Close() }()

	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}

	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = w.S
	}
	got := strings.Join(parts, " ")

	// These phrases must appear with correct word order — only possible if per-CID
	// widths placed the Bold-Cambria glyphs correctly so wordsFromBand sorts them
	// by ascending X into readable sequences.
	correctedPhrases := []string{
		"Personal consumption expenditures",
		"Gross private domestic investment",
	}
	for _, phrase := range correctedPhrases {
		if !strings.Contains(got, phrase) {
			t.Errorf("BEA DW=0 regression: phrase %q not found in Words output", phrase)
		}
	}

	// "esxp" is a characteristic scramble fragment from the pre-fix stacking bug
	// (characters from "expenditures" and "Personal" overlap at position 0).
	// Its absence confirms cidWidth is active and glyphs are correctly placed.
	if strings.Contains(got, "esxp") {
		t.Errorf("BEA DW=0 regression: scramble fragment %q found; cidWidth fix may have regressed", "esxp")
	}
}
