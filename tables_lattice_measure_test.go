package pdf

import (
	"os"
	"testing"
)

// allFixtures is the full inventory used by TestLatticeEdgeInventory.
var allFixtures = []string{
	"multicolumn/fr-2024-06543.pdf",
	"multicolumn/fr-2024-01353.pdf",
	"cjk/irs-p850-zh-hant.pdf",
	"cjk/udhr-zh-hans.pdf",
	"cjk/udhr-ja.pdf",
	"cjk/udhr-ko.pdf",
	"cyrillic/udhr-ru.pdf",
	"tables/nist-hb44-appc-2026.pdf",
	"tables/irs-p55b-2025-excerpt.pdf",
	// borderless cell-grid corpus — included to empirically confirm the "borderless ->
	// ~0 ruling-line edges -> lattice N/A" scoping claim (advisor's falsification check).
	"tables/irs-db-t4-3-2025.pdf",
	"tables/eia-aer-t3-1-2011.pdf",
}

// discriminatorFixtures are the 7 FP-gate fixtures (no tables expected).
var discriminatorFixtures = allFixtures[:7]

// latticeFixturePages opens a corpus PDF (path relative to testdata/corpus/) and returns the
// Content of every page. Skips/fails gracefully per the repo's existing test idiom.
func latticeFixturePages(t *testing.T, rel string) []Content {
	t.Helper()
	path := "testdata/corpus/" + rel
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("latticeFixturePages: open %s: %v", rel, err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("latticeFixturePages: stat %s: %v", rel, err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("latticeFixturePages: NewReader %s: %v", rel, err)
	}
	var pages []Content
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		pages = append(pages, p.Content())
	}
	return pages
}

// TestLatticeEdgeInventory prints aggregate edge/stroke/rect counts for all 9
// fixtures across all pages. Never fails — diagnostic dump only; run with -v.
func TestLatticeEdgeInventory(t *testing.T) {
	for _, rel := range allFixtures {
		pages := latticeFixturePages(t, rel)

		var (
			totalStrokes   int
			totalRects     int
			totalThinRects int
			totalRawH      int
			totalRawV      int
			totalMergedH   int
			totalMergedV   int
		)

		for _, c := range pages {
			totalStrokes += len(c.Stroke)
			totalRects += len(c.Rect)
			for _, rc := range c.Rect {
				w := rc.Max.X - rc.Min.X
				h := rc.Max.Y - rc.Min.Y
				if w < 0 {
					w = -w
				}
				if h < 0 {
					h = -h
				}
				if w < 3 || h < 3 {
					totalThinRects++
				}
			}
			raw := edgesFromContent(c)
			for _, e := range raw {
				if e.orient == 'h' {
					totalRawH++
				} else {
					totalRawV++
				}
			}
			merged := mergeEdges(raw, 3, 3)
			for _, e := range merged {
				if e.orient == 'h' {
					totalMergedH++
				} else {
					totalMergedV++
				}
			}
		}

		t.Logf("%-34s strokes=%d rects=%d thin=%d rawH=%d rawV=%d mergedH=%d mergedV=%d",
			rel, totalStrokes, totalRects, totalThinRects, totalRawH, totalRawV, totalMergedH, totalMergedV)
	}
}

// TestLatticeFalsePositiveGate runs latticeTables AND latticeTablesOpen over the 7
// discriminator fixtures (no-table documents) and asserts zero detections for both.
// For the open run, nil words + zero media are passed — recovery is a no-op when
// latticeTables yields 0 tables, so no words are needed to confirm the FP property.
func TestLatticeFalsePositiveGate(t *testing.T) {
	type detection struct {
		rel       string
		page      int
		cellCount int
		kind      string
	}
	var detections []detection
	total := 0
	openTotal := 0

	for _, rel := range discriminatorFixtures {
		pages := latticeFixturePages(t, rel)
		for pageIdx, c := range pages {
			// closed-only (existing gate)
			tables := latticeTables(c)
			for _, tbl := range tables {
				if len(tbl) > 1 {
					total++
					detections = append(detections, detection{
						rel:       rel,
						page:      pageIdx + 1,
						cellCount: len(tbl),
						kind:      "closed",
					})
				}
			}
			// open-recovery (new gate): nil words + zero media — safe because
			// recoverOpenColumns is never called when latticeTables returns 0 tables.
			openTables := latticeTablesOpen(c, nil, [4]float64{})
			for _, tbl := range openTables {
				if len(tbl) > 1 {
					openTotal++
					detections = append(detections, detection{
						rel:       rel,
						page:      pageIdx + 1,
						cellCount: len(tbl),
						kind:      "open",
					})
				}
			}
		}
	}

	// Log per-fixture/per-page breakdown so a nonzero result is diagnosable.
	for _, d := range detections {
		t.Logf("FP detection [%s]: fixture=%s page=%d cells=%d", d.kind, d.rel, d.page, d.cellCount)
	}
	t.Logf("lattice closed FP gate total: %d (want 0)", total)
	t.Logf("open-recovery FP: %d/%d discriminator pages (want 0)", openTotal, len(discriminatorFixtures))

	if total != 0 {
		t.Errorf("lattice closed FP gate: %d detections across discriminators (want 0)", total)
	}
	if openTotal != 0 {
		t.Errorf("open-recovery FP gate: %d detections across discriminators (want 0)", openTotal)
	}
}
