package pdf

import (
	"os"
	"strings"
	"testing"
)

// TestCIDFontWordGeometry guards the effectiveWidth fix: every non-whitespace
// word extracted from a Type0/CIDFont page must carry W > 0. Regression: before
// the fix, the Width path ignored CIDFont metrics (no /DW path), so all glyphs
// had W == 0, causing every glyph to split into a separate word and breaking
// table cell matching. Uses irs-p1040 (Type0/CIDFontType2 fonts with /W + /DW).
func TestCIDFontWordGeometry(t *testing.T) {
	data, err := os.ReadFile(corpusPath("hard/irs-p1040-tax-tables-excerpt.pdf"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}
	if len(words) == 0 {
		t.Fatal("Words returned no words on page 1")
	}
	for _, w := range words {
		if strings.TrimSpace(w.S) != "" && w.W == 0 {
			t.Errorf("CIDFont word %q has W == 0; effectiveWidth /DW default not applied", w.S)
		}
	}
}
