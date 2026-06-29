package pdf

// tables_lattice_combbody_test.go — synthetic unit + integration tests for inferCombBodyRows,
// the "comb-body" partial-table recovery: a closed lattice whose closed cells are ALL in a
// top header band, below which vertical rules extend down like comb teeth into a data body
// that has NO horizontal rules. Geometry is built directly as []lCell / []lEdge / []Word so
// the unit tests validate the synthesis algorithm decoupled from PDF/font parsing; the
// integration tests drive the real latticeTablesOpen pipeline via synthetic Content.
// Fixture geometry mirrors the cz-czso p753 unemployment table (comb-body Variant 3).

import "testing"

// combBodyFixture builds the base geometry shared by most comb-body tests:
// a 3-column header row, two interior crossing V-edges, and one header-bottom H-edge.
// Coordinates are in top-origin space (y↓).
//
//	Header row [10..20]: col0=[0..100], col1=[100..200], col2=[200..300]
//	H-edge at y=20, x=[0..300]   ← header-bottom rule / table bound
//	V-edge at x=100, y=[10..80]  ← col0/col1 divider (crossing)
//	V-edge at x=200, y=[10..80]  ← col1/col2 divider (crossing)
//	bodyBot = 80
func combBodyFixture() (closed []lCell, hEdges, vEdges []lEdge) {
	closed = []lCell{
		{x0: 0, top: 10, x1: 100, bottom: 20},
		{x0: 100, top: 10, x1: 200, bottom: 20},
		{x0: 200, top: 10, x1: 300, bottom: 20},
	}
	hEdges = []lEdge{
		{orient: 'h', x0: 0, x1: 300, top: 20, bottom: 20}, // header-bottom rule
	}
	vEdges = []lEdge{
		{orient: 'v', x0: 100, top: 10, bottom: 80}, // col0/col1 divider
		{orient: 'v', x0: 200, top: 10, bottom: 80}, // col1/col2 divider
	}
	return closed, hEdges, vEdges
}

// alignedBodyWords returns 3×3 body words cleanly aligned to the three columns so that
// no column cut straddles any glyph box.
//
//	Band y=35: "v00" in col0, "v01" in col1, "v02" in col2
//	Band y=55: "v10", "v11", "v12"
//	Band y=75: "v20", "v21", "v22"
//
// Column anchors: col0 uses X=10 W=40 (ax=30), col1 uses X=110 W=40 (ax=130),
// col2 uses X=210 W=40 (ax=230) — all well inside their respective intervals.
// Every band fills both non-label columns (col1, col2) → combBodyDataRows = 3.
func alignedBodyWords() []Word {
	var words []Word
	bands := [][2]float64{{30, 40}, {50, 60}, {70, 80}}
	labels := [][]string{
		{"v00", "v01", "v02"},
		{"v10", "v11", "v12"},
		{"v20", "v21", "v22"},
	}
	// (x, w) positions well inside each column — no column cut at x=100 or x=200
	// falls within combBodyStraddleTol (0.5 pt) of any glyph box edge.
	colX := [][2]float64{{10, 40}, {110, 40}, {210, 40}}
	for r, b := range bands {
		for c := range 3 {
			words = append(words, wordAtBand(labels[r][c], colX[c][0], colX[c][1], b[0], b[1]))
		}
	}
	return words
}

// TestInferCombBodyRowsAligned is the headline check: an aligned comb-body table fires,
// synthesizing one cell per (band × column), and every data value lands in its column.
func TestInferCombBodyRowsAligned(t *testing.T) {
	closed, hEdges, vEdges := combBodyFixture()
	words := alignedBodyWords()
	allClosed := [][]lCell{closed}

	synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, vEdges)
	// 3 bands × 3 columns = 9 synthesized cells.
	if len(synth) != 9 {
		t.Fatalf("synthesized cells: got %d, want 9 (3 bands × 3 columns)", len(synth))
	}

	// Full grid: 1 header row + 3 body rows, 3 columns each.
	grid := reconstructGrid(append(closed, synth...), words)
	if len(grid) != 4 {
		t.Fatalf("grid rows: got %d, want 4 (1 header + 3 body)\ngrid=%v", len(grid), grid)
	}
	if len(grid[0]) != 3 {
		t.Fatalf("grid cols: got %d, want 3\ngrid=%v", len(grid[0]), grid)
	}

	// Body rows (grid[1..3]): verify correct column placement.
	wantRows := [][]string{
		{"v00", "v01", "v02"},
		{"v10", "v11", "v12"},
		{"v20", "v21", "v22"},
	}
	for ri, want := range wantRows {
		row := grid[ri+1]
		for ci, wv := range want {
			if row[ci] != wv {
				t.Errorf("row %d col %d = %q, want %q\ngrid=%v", ri, ci, row[ci], wv, grid)
			}
		}
	}
}

// TestInferCombBodyRowsStraddleBails asserts the anti-straddle gate: a body word whose
// glyph box spans a column cut causes recovery to bail, leaving the table as an honest
// miss rather than garbling values into wrong columns. Modelled on the cz-czso p753
// employees table, which has several straddles in its first row band.
func TestInferCombBodyRowsStraddleBails(t *testing.T) {
	closed, hEdges, vEdges := combBodyFixture()
	allClosed := [][]lCell{closed}

	// Body words: mostly aligned, but one straddles the cut at x=100.
	// Straddling word: X=90, W=20 → glyph box [90..110].
	// Cut at 100 is inside [90+0.5, 110-0.5] = [90.5, 109.5] → straddle confirmed.
	words := alignedBodyWords()
	straddle := wordAtBand("STRADDLE", 90, 20, 30, 40)
	words = append(words, straddle)

	synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, vEdges)
	if synth != nil {
		t.Errorf("straddle table: got %d synthesized cells, want 0 (anti-straddle gate must bail)",
			len(synth))
	}
}

// TestInferCombBodyRowsNormalTableUntouched asserts the key 0-FP guard for a normally-ruled
// table: its V-edges (column dividers) are BOUNDED by the bottom H-rule and do not extend
// below it as "comb teeth", so crossingV is empty and recovery bails (len(crossingV) <
// combBodyMinCols). This mirrors the real p55 control table observed during prototyping.
func TestInferCombBodyRowsNormalTableUntouched(t *testing.T) {
	// Fully-ruled 3×3 table: header + two data rows, all cells closed.
	// All three bands span [10,20], [30,40], [50,60].
	var closed []lCell
	for _, b := range [][2]float64{{10, 20}, {30, 40}, {50, 60}} {
		for _, x := range [][2]float64{{0, 100}, {100, 200}, {200, 300}} {
			closed = append(closed, lCell{x0: x[0], top: b[0], x1: x[1], bottom: b[1]})
		}
	}
	allClosed := [][]lCell{closed}

	// H-edge at y=60 = headerBot (max cell bottom), so gotBound = true.
	hEdges := []lEdge{
		{orient: 'h', x0: 0, x1: 300, top: 60, bottom: 60},
	}
	// V-edges terminate AT y=60 (the bottom rule), not below — no comb teeth.
	// Crossing check: e.bottom > headerBot+3 → 60 > 63 → false → crossingV is empty.
	vEdges := []lEdge{
		{orient: 'v', x0: 100, top: 10, bottom: 60},
		{orient: 'v', x0: 200, top: 10, bottom: 60},
	}

	synth := inferCombBodyRows(closed, closed, 0, allClosed, nil, hEdges, vEdges)
	if synth != nil {
		t.Errorf("normal ruled table: got %d synthesized cells, want 0 (V-edges don't cross below bottom rule → crossingV=0 → must bail)",
			len(synth))
	}
}

// TestInferCombBodyRowsCrossTableGuard asserts the cross-table body-occupancy guard:
// if any other closed table has cells whose centers fall inside the header's body region,
// the recovery bails (foreignInBody>0). This prevents over-firing on a normal page where
// a closed data table sits immediately below the header section.
func TestInferCombBodyRowsCrossTableGuard(t *testing.T) {
	// Table 0: header-only (3 cells in y=[10..20]).
	closed0, hEdges, vEdges := combBodyFixture()

	// Table 1: a separate closed table occupying the body region y=[25..55].
	// Its cell centers (cy=40) fall inside the body region (20+3, 80+3) = (23, 83).
	closed1 := []lCell{
		{x0: 0, top: 25, x1: 100, bottom: 55},
		{x0: 100, top: 25, x1: 200, bottom: 55},
		{x0: 200, top: 25, x1: 300, bottom: 55},
	}
	allClosed := [][]lCell{closed0, closed1}

	synth := inferCombBodyRows(closed0, closed0, 0, allClosed, alignedBodyWords(), hEdges, vEdges)
	if synth != nil {
		t.Errorf("cross-table guard: got %d synthesized cells, want 0 (foreign body cells → must bail)",
			len(synth))
	}
}

// TestInferCombBodyRowsFewerThanMinColsBails asserts that the recovery bails when fewer
// than combBodyMinCols (2) interior crossing V-edges are detected — including the case
// where V-edges exist but are at the page margin (outside the table x-bounds) and are
// excluded by the strictly-interior filter.
func TestInferCombBodyRowsFewerThanMinColsBails(t *testing.T) {
	closed, hEdges, _ := combBodyFixture()
	allClosed := [][]lCell{closed}
	words := alignedBodyWords()

	t.Run("no-crossing-vEdges", func(t *testing.T) {
		synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, nil)
		if synth != nil {
			t.Errorf("got %d cells, want 0 (no V-edges → must bail)", len(synth))
		}
	})

	t.Run("page-border-only", func(t *testing.T) {
		// Verticals at exactly tableLeft (x=0) and tableRight (x=300) are NOT strictly
		// interior: x=0 is not > tableLeft+1=1 and x=300 is not < tableRight-1=299.
		borderVEdges := []lEdge{
			{orient: 'v', x0: 0, top: 10, bottom: 80},   // left border — excluded
			{orient: 'v', x0: 300, top: 10, bottom: 80}, // right border — excluded
		}
		synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, borderVEdges)
		if synth != nil {
			t.Errorf("got %d cells, want 0 (page-border verticals excluded → 0 crossing → must bail)",
				len(synth))
		}
	})

	t.Run("one-interior-vEdge", func(t *testing.T) {
		// One interior crossing V-edge gives only 1 column cut, below combBodyMinCols (2).
		oneVEdge := []lEdge{
			{orient: 'v', x0: 100, top: 10, bottom: 80},
		}
		synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, oneVEdge)
		if synth != nil {
			t.Errorf("got %d cells, want 0 (1 interior V-edge < min %d → must bail)",
				len(synth), combBodyMinCols)
		}
	})
}

// TestInferCombBodyRowsRowCoOccupancyGate locks the row co-occupancy gate
// (combBodyMinDataRows): a comb-body shape that passes the geometric preconditions
// (bounds, crossing V-edges, no foreign body cells, no straddle) but whose body words do
// NOT fill >= 2 NON-LABEL columns in the SAME band, for >= 2 distinct bands, is rejected.
// Each subtest reproduces a render-confirmed false-positive mode found on non-table pages.
// All use the no-straddle column anchors (X=10/110/210, W=40) so ONLY the row co-occupancy
// gate can be responsible for the bail.
func TestInferCombBodyRowsRowCoOccupancyGate(t *testing.T) {
	closed, hEdges, vEdges := combBodyFixture()
	allClosed := [][]lCell{closed}

	t.Run("label-column-only", func(t *testing.T) {
		// TOC mode: every body word sits in col0 (label) across 3 bands → 0 non-label cols.
		var words []Word
		for _, b := range [][2]float64{{30, 40}, {50, 60}, {70, 80}} {
			words = append(words, wordAtBand("L", 10, 40, b[0], b[1]))
		}
		if synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, vEdges); synth != nil {
			t.Errorf("label-only: got %d cells, want 0 (0 data rows < %d)", len(synth), combBodyMinDataRows)
		}
	})

	t.Run("one-data-column", func(t *testing.T) {
		// formula/prose mode: label column + exactly ONE non-label column (col1) across 3 bands.
		// No band fills >= 2 non-label columns → 0 data rows.
		var words []Word
		for _, b := range [][2]float64{{30, 40}, {50, 60}, {70, 80}} {
			words = append(words, wordAtBand("L", 10, 40, b[0], b[1]))
			words = append(words, wordAtBand("d", 110, 40, b[0], b[1]))
		}
		if synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, vEdges); synth != nil {
			t.Errorf("one-data-column: got %d cells, want 0 (0 data rows < %d)", len(synth), combBodyMinDataRows)
		}
	})

	t.Run("staggered-map-labels", func(t *testing.T) {
		// The pl-gus p314 airport-map FP mode: BOTH non-label columns ARE each occupied across
		// >= 2 distinct bands — so the OLD per-column occupancy gate scored 2 and FIRED (the
		// false positive) — but the labels are SCATTERED: no single band fills both columns
		// together, so the NEW row co-occupancy gate scores 0 data rows and BAILS. This needs
		// 4 bands (pigeonhole: 2+2 column occupancies across only 3 bands force a collision):
		// col1 in bands 0,2; col2 in bands 1,3. bodyBot=80 keeps all ay (35,50,65,80) < 86.
		words := []Word{
			wordAtBand("m0", 110, 40, 30, 40), // col1, band0
			wordAtBand("m1", 210, 40, 45, 55), // col2, band1
			wordAtBand("m2", 110, 40, 60, 70), // col1, band2
			wordAtBand("m3", 210, 40, 75, 85), // col2, band3
		}
		if synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, vEdges); synth != nil {
			t.Errorf("staggered-map: got %d cells, want 0 (old per-column gate FIRES here; row gate must BAIL — no band fills 2 non-label cols)", len(synth))
		}
	})

	t.Run("single-data-row", func(t *testing.T) {
		// Exactly ONE band fills 2 non-label columns; combBodyMinDataRows requires 2. → BAIL.
		words := []Word{
			wordAtBand("L", 10, 40, 30, 40),
			wordAtBand("a", 110, 40, 30, 40), // col1, band0
			wordAtBand("b", 210, 40, 30, 40), // col2, band0
			wordAtBand("L2", 10, 40, 50, 60), // band1: label only
		}
		if synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, vEdges); synth != nil {
			t.Errorf("single-data-row: got %d cells, want 0 (1 data row < %d)", len(synth), combBodyMinDataRows)
		}
	})
}

// TestCombBodyDataRowsCounts directly locks the data-row count the gate computes: the aligned
// good fixture scores 3 (3 bands each fill cols 1 and 2), at or above combBodyMinDataRows,
// which is why TestInferCombBodyRowsAligned still FIRES; the staggered-map fixture scores 0.
func TestCombBodyDataRowsCounts(t *testing.T) {
	cols := []combCol{{0, 100}, {100, 200}, {200, 300}}
	bands := []float64{35, 55, 75}
	if got := combBodyDataRows(alignedBodyWords(), bands, cols); got != 3 {
		t.Errorf("aligned data rows = %d, want 3 (3 bands × 2 non-label cols)", got)
	}
	// Staggered map labels (the p314 mode): BOTH non-label columns occupied in >= 2 distinct
	// bands (col1 in bands 0,2; col2 in bands 1,3) — the OLD per-column gate would score 2 —
	// but never two together in one band, so the row co-occupancy gate scores 0.
	bands4 := []float64{35, 50, 65, 80}
	staggered := []Word{
		wordAtBand("m0", 110, 40, 30, 40), // col1, band0
		wordAtBand("m1", 210, 40, 45, 55), // col2, band1
		wordAtBand("m2", 110, 40, 60, 70), // col1, band2
		wordAtBand("m3", 210, 40, 75, 85), // col2, band3
	}
	if got := combBodyDataRows(staggered, bands4, cols); got != 0 {
		t.Errorf("staggered-map data rows = %d, want 0 (no band fills 2 non-label cols)", got)
	}
}

// TestInferCombBodyRowsNoDoubleRecover locks the H3 correctness fix: body-word selection is
// done against the AUGMENTED cells (the `placed` argument), not the pristine closed snapshot.
// When an earlier recovery pass has already placed cells covering the body words, those words
// are no longer "unplaced", so the comb-body pass synthesizes nothing — it cannot double-cover
// words another pass already recovered. The geometry (pristine `closed`) still satisfies every
// structural precondition, so the bail is attributable ONLY to the placed-cell filter.
func TestInferCombBodyRowsNoDoubleRecover(t *testing.T) {
	closed, hEdges, vEdges := combBodyFixture()
	words := alignedBodyWords()
	allClosed := [][]lCell{closed}

	// placed = header cells PLUS cells that already cover the entire body region, as if an
	// earlier recovery pass had reconstructed it. Every body word now falls inside an
	// existing placed cell.
	placed := append([]lCell(nil), closed...)
	for _, b := range [][2]float64{{30, 40}, {50, 60}, {70, 80}} {
		for _, x := range [][2]float64{{0, 100}, {100, 200}, {200, 300}} {
			placed = append(placed, lCell{x0: x[0], top: b[0], x1: x[1], bottom: b[1]})
		}
	}

	// Control: with placed == closed (header only), the same inputs DO fire — proving the
	// preconditions are satisfied and the nil below is caused by the placed filter alone.
	if synth := inferCombBodyRows(closed, closed, 0, allClosed, words, hEdges, vEdges); len(synth) != 9 {
		t.Fatalf("control (placed==closed): got %d cells, want 9 (must fire)", len(synth))
	}
	if synth := inferCombBodyRows(closed, placed, 0, allClosed, words, hEdges, vEdges); synth != nil {
		t.Errorf("no-double-recover: got %d cells, want 0 (all body words already placed → none to synthesize)", len(synth))
	}
}

// combBodyStrokeContent builds a synthetic Content whose strokes form a comb-body table:
// a 3-column closed header band (PDF Y 180..190) over a body (Y down to 130) carrying only
// the vertical comb teeth (no horizontal body rules). All coordinates are PDF bottom-origin.
func combBodyStrokeContent() Content {
	seg := func(x0, y0, x1, y1 float64) Stroke {
		return Stroke{From: Point{x0, y0}, To: Point{x1, y1}}
	}
	return Content{
		Stroke: []Stroke{
			seg(0, 190, 300, 190),   // header top rule
			seg(0, 180, 300, 180),   // header-bottom rule (table bound)
			seg(0, 190, 0, 130),     // left border (excluded: not strictly interior)
			seg(100, 190, 100, 130), // col0/col1 comb tooth (crossing)
			seg(200, 190, 200, 130), // col1/col2 comb tooth (crossing)
			seg(300, 190, 300, 130), // right border (excluded)
		},
	}
}

// combBodyStrokeWords returns 3 body data rows aligned to the three columns of
// combBodyStrokeContent (PDF bottom-origin). Each band fills both non-label columns.
func combBodyStrokeWords() []Word {
	mk := func(s string, x float64) Word { return Word{S: s, X: x, W: 40, H: 10} }
	var words []Word
	for _, row := range []struct {
		y          float64
		c0, c1, c2 string
	}{
		{170, "lbA", "a1", "a2"},
		{155, "lbB", "b1", "b2"},
		{140, "lbC", "c1", "c2"},
	} {
		w0, w1, w2 := mk(row.c0, 10), mk(row.c1, 110), mk(row.c2, 210)
		w0.Y, w1.Y, w2.Y = row.y, row.y, row.y
		words = append(words, w0, w1, w2)
	}
	return words
}

// TestLatticeTablesOpenCombBodyFires is the L10 integration test: the full latticeTablesOpen
// pipeline (edges → lattice → recovery passes) reconstructs a comb-body table from synthetic
// stroke Content + body words. It proves the wiring end-to-end — the header-only closed lattice
// plus the comb teeth recover into a 1-header + 3-data-row grid.
func TestLatticeTablesOpenCombBodyFires(t *testing.T) {
	c := combBodyStrokeContent()
	words := combBodyStrokeWords()
	media := [4]float64{0, 0, 300, 200}

	lattices := latticeTablesOpen(c, words, media)
	if len(lattices) != 1 {
		t.Fatalf("lattices: got %d tables, want 1\n%v", len(lattices), lattices)
	}

	grid := reconstructGrid(lattices[0], words)
	if len(grid) != 4 {
		t.Fatalf("grid rows: got %d, want 4 (1 header + 3 data)\ngrid=%v", len(grid), grid)
	}
	wantBody := [][]string{
		{"lbA", "a1", "a2"},
		{"lbB", "b1", "b2"},
		{"lbC", "c1", "c2"},
	}
	for ri, want := range wantBody {
		for ci, wv := range want {
			if grid[ri+1][ci] != wv {
				t.Errorf("row %d col %d = %q, want %q\ngrid=%v", ri+1, ci, grid[ri+1][ci], wv, grid)
			}
		}
	}
}

// TestLatticeTablesOpenCombBodyNoDuplicate is the L10 no-double-append integration check:
// driving the real pipeline must not produce duplicate/overlapping cells for the same body
// region. Because body-word selection runs against the augmented table cells, no word is
// recovered twice; the resulting lattice has exactly the 3 header cells + 9 synthesized body
// cells (3 bands × 3 columns), with no duplicate cell geometry.
func TestLatticeTablesOpenCombBodyNoDuplicate(t *testing.T) {
	c := combBodyStrokeContent()
	words := combBodyStrokeWords()
	media := [4]float64{0, 0, 300, 200}

	lattices := latticeTablesOpen(c, words, media)
	if len(lattices) != 1 {
		t.Fatalf("lattices: got %d tables, want 1", len(lattices))
	}
	cells := lattices[0]
	if len(cells) != 12 {
		t.Fatalf("cells: got %d, want 12 (3 header + 9 body, no duplicates)\n%v", len(cells), cells)
	}
	seen := map[lCell]int{}
	for _, cell := range cells {
		seen[cell]++
		if seen[cell] > 1 {
			t.Errorf("duplicate cell %+v appears %d times (double-recovery)", cell, seen[cell])
		}
	}
}
