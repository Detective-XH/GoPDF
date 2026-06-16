package pdf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firstIndexContaining returns the index of the first block in blocks whose S
// contains substr, or -1 if no block matches.
func firstIndexContaining(blocks []Block, substr string) int {
	for i, b := range blocks {
		if strings.Contains(b.S, substr) {
			return i
		}
	}
	return -1
}

// TestMultiColumnReadingOrderFR locks column-major reading order on BOTH 3-column FR pages:
// Blocks() must emit each column's content in left-to-right column order, and no block may
// merge two adjacent columns. The phrases + their order are taken from `pdftotext -layout`
// (rendered truth), NOT from GoPDF's own output (anti-circular). Both fixtures are Federal
// Register; cross-publisher column-major is separately demonstrated on a non-FR doc (spike 3,
// LA County precinct bulletin) — see the generalization note. col phrases verified during
// implementation against pdftotext -layout output.
func TestMultiColumnReadingOrderFR(t *testing.T) {
	cases := []struct {
		path         string    // relative to corpusRoot
		colPhrases   []string  // one col-unique phrase per column, in left-to-right order
		mustNotMerge [2]string // a pair that must never share one block (adjacent-column split)
	}{
		{
			"multicolumn/fr-2024-06543.pdf",
			// col1: "Public Participation" (left column heading, col-unique)
			// col2: "any personal information you have" (col2-unique; plan's "www.regulations.gov"
			//        is also in col1, so replaced with this col-unique phrase from pdftotext)
			// col3: "to navigation. In addition" (col3-unique)
			[]string{"Public Participation", "any personal information you have", "to navigation. In addition"},
			[2]string{"any personal information you have", "to navigation. In addition"},
		},
		{
			"multicolumn/fr-2024-01353.pdf",
			// col1: "supplement to the GEIS" (col1-unique)
			// col2: "adams.html. To begin the search" (col2-unique)
			// col3: "PENSION BENEFIT GUARANTY" (col3-unique)
			[]string{"supplement to the GEIS", "adams.html. To begin the search", "PENSION BENEFIT GUARANTY"},
			[2]string{"adams.html. To begin the search", "PENSION BENEFIT GUARANTY"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(corpusRoot, tc.path))
			if err != nil {
				t.Fatalf("read fixture %s: %v", tc.path, err)
			}
			r, err := OpenBytes(data)
			if err != nil {
				t.Fatalf("open fixture %s: %v", tc.path, err)
			}
			blocks, err := r.Page(1).Blocks()
			if err != nil {
				t.Fatalf("Blocks() %s: %v", tc.path, err)
			}

			// Lock column-major order: each col phrase must appear AFTER the previous.
			prev := -1
			for i, ph := range tc.colPhrases {
				idx := firstIndexContaining(blocks, ph)
				if idx < 0 {
					t.Fatalf("col%d phrase %q not found in any block", i+1, ph)
				}
				if idx <= prev {
					t.Errorf("column-major order broken at col%d %q: block index %d not after %d",
						i+1, ph, idx, prev)
				}
				prev = idx
			}

			// Lock adjacent-column split: the two mustNotMerge phrases must never share one block.
			for _, b := range blocks {
				if strings.Contains(b.S, tc.mustNotMerge[0]) && strings.Contains(b.S, tc.mustNotMerge[1]) {
					t.Errorf("adjacent columns merged in one block: %q", b.S)
				}
			}
		})
	}
}
