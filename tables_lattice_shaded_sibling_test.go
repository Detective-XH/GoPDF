package pdf

// tables_lattice_shaded_sibling_test.go — synthetic unit tests for inferShadedSiblingRows's
// donor-provenance gate: the mechanism that fixed the real irs-p1040 row-collapse defect while
// declining to regress a real wrapped-header table (destatis-aussenhandel-tabellen-2026.pdf,
// local-only fixture, not committed — this file locks the SAME discriminating mechanism with a
// synthetic fixture instead). See rectOnlyHEdges / shadedSiblingBoundaryIsRectDerived.
//
// Both tests below share IDENTICAL cell/word geometry — a 3-row donor column plus one tall,
// same-span recipient cell whose own words already sit at the donor's row pitch (so if split,
// every resulting band would be non-empty and non-destructive; the consequence invariant alone
// would happily commit the split). Only rectH differs: TestInferShadedSiblingRowsStrokedDonorNoSplit
// passes no rect-derived edges at all (the donor's boundaries are stroke/other-derived, exactly
// the destatis shape — a column that is ALREADY split for a reason unrelated to fill shading),
// TestInferShadedSiblingRowsFilledDonorSplits passes edges matching the donor's exact boundaries
// (a genuine alternating-shading fill, exactly the irs-p1040 shape). Splitting in the second case
// but not the first, on otherwise-identical geometry, proves rectH — not the consequence
// invariant, not the cell shapes — is what discriminates.

import "testing"

// shadedSiblingFixture returns a 3-row donor column (x=[0,20]) and one tall, same-span
// recipient cell (x=[20,90]) with per-row words in both, at the SAME row pitch (top-origin
// y=[0,10],[10,20],[20,30]) — geometry deliberately shaped so the consequence invariant alone
// (every band non-empty, no word straddles a boundary) would always allow a split; only the
// donor-provenance gate can tell the two tests below apart.
func shadedSiblingFixture() (cells []lCell, words []Word) {
	cells = []lCell{
		{x0: 0, top: 0, x1: 20, bottom: 10},  // donor row 0
		{x0: 0, top: 10, x1: 20, bottom: 20}, // donor row 1
		{x0: 0, top: 20, x1: 20, bottom: 30}, // donor row 2
		{x0: 20, top: 0, x1: 90, bottom: 30}, // recipient: tall, uncollapsed, same span
	}
	words = []Word{
		wordAtBand("d0", 5, 10, 0, 10),
		wordAtBand("d1", 5, 10, 10, 20),
		wordAtBand("d2", 5, 10, 20, 30),
		wordAtBand("r0", 30, 10, 0, 10),
		wordAtBand("r1", 30, 10, 10, 20),
		wordAtBand("r2", 30, 10, 20, 30),
	}
	return cells, words
}

// shadedSiblingFilledDonorEdges returns h-edges that back the donor column's exact boundaries
// (x0=0,x1=20 at each of the 4 row-boundary Y positions) — what rectOnlyHEdges would return for
// a page whose donor column is a genuine alternating-row-shading fill (the irs-p1040 shape).
func shadedSiblingFilledDonorEdges() []lEdge {
	var h []lEdge
	for _, y := range []float64{0, 10, 20, 30} {
		h = append(h, lEdge{orient: 'h', x0: 0, x1: 20, top: y, bottom: y})
	}
	return h
}

// shadedSiblingWideFrameEdges returns h-edges at the SAME Y positions as
// shadedSiblingFilledDonorEdges, but with a wide x-span (x0=0,x1=500) instead of matching the
// donor column's own x0=0,x1=20 — a genuinely fill-derived edge (passes the provenance axis),
// but from a table-wide background/frame fill that merely happens to pass through these Y
// positions, not a real per-row shading fill bounding THIS column. This is the real-world
// destatis false-donor shape at the geometry level (its regression's donor showed an x-span
// mismatch of hundreds of points against a genuine per-row fill's near-zero mismatch — see the
// spike-verdict doc's shadedSiblingRectSpanTol comment) and is a DIFFERENT axis than the
// stroked-donor test above: this donor genuinely IS fill-derived, it just doesn't bound this
// column.
func shadedSiblingWideFrameEdges() []lEdge {
	var h []lEdge
	for _, y := range []float64{0, 10, 20, 30} {
		h = append(h, lEdge{orient: 'h', x0: 0, x1: 500, top: y, bottom: y})
	}
	return h
}

// TestInferShadedSiblingRowsStrokedDonorNoSplit locks the destatis-shaped regression fix: a
// donor column that is already row-split for a reason OTHER than fill shading (no matching
// rect-derived edges at all — e.g. an ordinary ruling stroke, or an unrelated already-split
// sub-column) must NOT donate its row boundaries onto the recipient, even though the recipient's
// own words would make the split locally lossless and non-destructive if it fired.
func TestInferShadedSiblingRowsStrokedDonorNoSplit(t *testing.T) {
	cells, words := shadedSiblingFixture()
	got := inferShadedSiblingRows(cells, words, nil)
	if len(got) != len(cells) {
		t.Fatalf("stroked donor: got %d cells, want %d (unchanged — no rect-derived donor)", len(got), len(cells))
	}
	for _, c := range got {
		if c.x0 == 20 && c.x1 == 90 && !(c.top == 0 && c.bottom == 30) {
			t.Errorf("stroked donor: recipient was split (top=%v bottom=%v); want it untouched at [0,30]", c.top, c.bottom)
		}
	}
}

// TestInferShadedSiblingRowsFilledDonorSplits locks the positive irs-p1040 mechanism
// independent of the one real corpus fixture: identical geometry to the stroked-donor test
// above, but rectH now backs the donor's exact boundaries — a genuine fill-derived donor must
// split the recipient into one cell per donor row.
func TestInferShadedSiblingRowsFilledDonorSplits(t *testing.T) {
	cells, words := shadedSiblingFixture()
	rectH := shadedSiblingFilledDonorEdges()
	got := inferShadedSiblingRows(cells, words, rectH)

	var recipientPieces []lCell
	for _, c := range got {
		if c.x0 == 20 && c.x1 == 90 {
			recipientPieces = append(recipientPieces, c)
		}
	}
	if len(recipientPieces) != 3 {
		t.Fatalf("filled donor: recipient split into %d pieces, want 3\ngot=%v", len(recipientPieces), got)
	}
	wantBands := [][2]float64{{0, 10}, {10, 20}, {20, 30}}
	for i, want := range wantBands {
		found := false
		for _, p := range recipientPieces {
			if p.top == want[0] && p.bottom == want[1] {
				found = true
			}
		}
		if !found {
			t.Errorf("filled donor: missing recipient band [%v,%v] (piece %d)\ngot=%v", want[0], want[1], i, recipientPieces)
		}
	}
}

// TestInferShadedSiblingRowsWideFrameDonorNoSplit locks the SECOND, independent discriminating
// axis (x-span match, not fill-provenance): a genuinely fill-derived edge whose x-span is much
// wider than the donor column (a table-wide background/frame fill that happens to pass through
// the same Y positions, not a real per-row shading fill bounding this column) must NOT qualify
// as a donor, even though it passes the provenance check TestInferShadedSiblingRowsFilledDonorSplits
// exercises. Same geometry as the two tests above; only the edges' x-span differs.
func TestInferShadedSiblingRowsWideFrameDonorNoSplit(t *testing.T) {
	cells, words := shadedSiblingFixture()
	rectH := shadedSiblingWideFrameEdges()
	got := inferShadedSiblingRows(cells, words, rectH)
	if len(got) != len(cells) {
		t.Fatalf("wide-frame donor: got %d cells, want %d (unchanged — edge x-span does not bound this column)", len(got), len(cells))
	}
	for _, c := range got {
		if c.x0 == 20 && c.x1 == 90 && !(c.top == 0 && c.bottom == 30) {
			t.Errorf("wide-frame donor: recipient was split (top=%v bottom=%v); want it untouched at [0,30]", c.top, c.bottom)
		}
	}
}

// TestRectOnlyHEdgesAdjacentDonorsNotJoined drives the REAL production path — real Rect structs
// through rectOnlyHEdges (not hand-built lEdges the 3 tests above use) — for the specific
// regression an adversarial review round caught: two ADJACENT genuinely-shaded donor columns
// (x=[0,20] and x=[20,40]) sharing exact row boundaries. mergeEdges' joinEdges step would extend
// each fill edge's x-span across its touching neighbor before shadedSiblingBoundaryIsRectDerived
// ever saw them (x=[0,20] and x=[20,40] joining into one x=[0,40] edge), so BOTH donors would
// fail their own x-span match — a false negative, not a false positive (joining only WIDENS a
// span, which can only make the match harder, never spuriously easier), but a real under-fire on
// what is plausibly the modal shape this feature targets. rectOnlyHEdges snaps Y but does not
// join X-spans for exactly this reason; this test locks that via the real Rect-to-edge path.
func TestRectOnlyHEdgesAdjacentDonorsNotJoined(t *testing.T) {
	mkRow := func(x0, x1, yTopPDF, yBotPDF float64) Rect {
		return Rect{Min: Point{x0, yBotPDF}, Max: Point{x1, yTopPDF}}
	}
	// Two adjacent 3-row donor columns, PDF bottom-origin, landing at top-origin y=0,10,20,30
	// after content.go's top=-Y convention (matching shadedSiblingFixture's row pitch).
	rects := []Rect{
		mkRow(0, 20, 0, -10), mkRow(0, 20, -10, -20), mkRow(0, 20, -20, -30),
		mkRow(20, 40, 0, -10), mkRow(20, 40, -10, -20), mkRow(20, 40, -20, -30),
	}
	rectH := rectOnlyHEdges(rects)
	for _, y := range []float64{0, 10, 20, 30} {
		if !shadedSiblingBoundaryIsRectDerived(y, 0, 20, rectH) {
			t.Errorf("donor A [0,20] boundary at y=%v not recognized as rect-derived (adjacent donor B's edge must not have joined into it)", y)
		}
		if !shadedSiblingBoundaryIsRectDerived(y, 20, 40, rectH) {
			t.Errorf("donor B [20,40] boundary at y=%v not recognized as rect-derived (adjacent donor A's edge must not have joined into it)", y)
		}
	}
}
