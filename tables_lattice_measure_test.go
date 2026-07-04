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
	// Synthetic FP discriminator: isolated shaded prose bands + callout boxes; no tables.
	// Tests that fill-rect content the lattice does not close into a grid stays 0-FP.
	"discriminator/shaded-non-table.pdf",
	"tables/nist-hb44-appc-2026.pdf",
	"tables/irs-p55b-2025-excerpt.pdf",
	// borderless cell-grid corpus — included to empirically confirm the "borderless ->
	// ~0 ruling-line edges -> lattice N/A" scoping claim (advisor's falsification check).
	"tables/irs-db-t4-3-2025.pdf",
	"tables/eia-aer-t3-1-2011.pdf",
}

// discriminatorFixtures are the 8 FP-gate fixtures (no tables expected).
var discriminatorFixtures = allFixtures[:8]

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

// latticeEdgeCounts is one fixture's aggregate stroke/rect/edge tally.
type latticeEdgeCounts struct {
	strokes, rects, thinRects, rawH, rawV, mergedH, mergedV int
}

// latticeEdgeInventoryWant pins the exact per-fixture counts TestLatticeEdgeInventory
// asserts. Counts pinned 2026-07-04 against the committed testdata/corpus/ fixtures
// (deterministic: same content stream in, same edgesFromContent/mergeEdges out). An
// intentional edge-pool change (edgesFromContent, mergeEdges, or the thin-rect/merge
// tolerances here) updates these numbers — with render-truth justification, not just a
// re-run — since this is the regression lock on that pipeline, not a diagnostic dump.
var latticeEdgeInventoryWant = map[string]latticeEdgeCounts{
	"multicolumn/fr-2024-06543.pdf":      {strokes: 7, rects: 0, thinRects: 0, rawH: 7, rawV: 0, mergedH: 7, mergedV: 0},
	"multicolumn/fr-2024-01353.pdf":      {strokes: 7, rects: 0, thinRects: 0, rawH: 7, rawV: 0, mergedH: 7, mergedV: 0},
	"cjk/irs-p850-zh-hant.pdf":           {strokes: 133, rects: 9, thinRects: 0, rawH: 109, rawV: 60, mergedH: 62, mergedV: 46},
	"cjk/udhr-zh-hans.pdf":               {strokes: 0, rects: 0, thinRects: 0, rawH: 0, rawV: 0, mergedH: 0, mergedV: 0},
	"cjk/udhr-ja.pdf":                    {strokes: 0, rects: 0, thinRects: 0, rawH: 0, rawV: 0, mergedH: 0, mergedV: 0},
	"cjk/udhr-ko.pdf":                    {strokes: 0, rects: 0, thinRects: 0, rawH: 0, rawV: 0, mergedH: 0, mergedV: 0},
	"cyrillic/udhr-ru.pdf":               {strokes: 0, rects: 0, thinRects: 0, rawH: 0, rawV: 0, mergedH: 0, mergedV: 0},
	"discriminator/shaded-non-table.pdf": {strokes: 0, rects: 8, thinRects: 0, rawH: 16, rawV: 16, mergedH: 16, mergedV: 16},
	"tables/nist-hb44-appc-2026.pdf":     {strokes: 0, rects: 17920, thinRects: 6746, rawH: 26114, rawV: 30836, mergedH: 741, mergedV: 276},
	"tables/irs-p55b-2025-excerpt.pdf":   {strokes: 1503, rects: 565, thinRects: 2, rawH: 2577, rawV: 1182, mergedH: 399, mergedV: 117},
	"tables/irs-db-t4-3-2025.pdf":        {strokes: 33, rects: 1, thinRects: 0, rawH: 33, rawV: 4, mergedH: 9, mergedV: 4},
	"tables/eia-aer-t3-1-2011.pdf":       {strokes: 32, rects: 83, thinRects: 1, rawH: 182, rawV: 180, mergedH: 36, mergedV: 13},
}

// TestLatticeEdgeInventory is the regression lock on edgesFromContent/mergeEdges: it
// computes aggregate edge/stroke/rect counts for every fixture in allFixtures across
// all pages and asserts each against latticeEdgeInventoryWant's pinned exact counts.
// Uses t.Errorf (not Fatalf) so every drifted fixture surfaces in one run. Logf lines
// are kept so `-v` still prints the full per-fixture breakdown.
func TestLatticeEdgeInventory(t *testing.T) {
	for _, rel := range allFixtures {
		pages := latticeFixturePages(t, rel)

		var got latticeEdgeCounts

		for _, c := range pages {
			got.strokes += len(c.Stroke)
			got.rects += len(c.Rect)
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
					got.thinRects++
				}
			}
			raw := edgesFromContent(c)
			for _, e := range raw {
				if e.orient == 'h' {
					got.rawH++
				} else {
					got.rawV++
				}
			}
			merged := mergeEdges(raw, 3, 3)
			for _, e := range merged {
				if e.orient == 'h' {
					got.mergedH++
				} else {
					got.mergedV++
				}
			}
		}

		t.Logf("%-34s strokes=%d rects=%d thin=%d rawH=%d rawV=%d mergedH=%d mergedV=%d",
			rel, got.strokes, got.rects, got.thinRects, got.rawH, got.rawV, got.mergedH, got.mergedV)

		want, ok := latticeEdgeInventoryWant[rel]
		if !ok {
			t.Errorf("%s: no pinned counts in latticeEdgeInventoryWant (new fixture added to allFixtures without pinning)", rel)
			continue
		}
		if got != want {
			t.Errorf("%s: edge inventory drifted\n got  %+v\n want %+v", rel, got, want)
		}
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
			openTables := latticeTablesOpen(c, nil, [4]float64{}, nil)
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
