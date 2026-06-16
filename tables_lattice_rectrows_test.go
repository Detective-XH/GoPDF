package pdf

// tables_lattice_rectrows_test.go — content-validation for rect-bordered row inference
// (inferRectBorderedRows + its guards). Every fixture is built directly as []lCell + []lEdge +
// []Word with CLEAN strings, so it validates the row-inference ALGORITHM decoupled from any
// PDF/font parsing — there is no text-decode step in the loop. The
// coordinate convention is TOP-ORIGIN throughout — see tables_lattice_opensynth_test.go, whose
// wordAtBand helper this file reuses.

import "testing"

// rectBorderedFixture builds a clean-font rect-bordered table: 3 full-height data columns (each
// one collapsed cell, NO interior horizontal rules), an open Year label column to the left, and
// a frame whose top+bottom horizontals overhang left past vMin. Four data rows sit at band
// centres 110/130/150/170 (spacing 20 ≫ rectRowGapTol). It mirrors the ERP B-1 geometry the
// detector must recover, without ERP's undecodable glyphs.
func rectBorderedFixture() (cells []lCell, hEdges []lEdge, words []Word, media [4]float64) {
	const dataTop, tableBot = 100.0, 200.0
	const vMin, vMax = 200.0, 350.0
	x0s := []float64{200, 250, 300}
	x1s := []float64{250, 300, 350}
	for i := range x0s {
		cells = append(cells, lCell{x0: x0s[i], top: dataTop, x1: x1s[i], bottom: tableBot})
	}
	// Frame top + bottom overhang left to x0=150 (past vMin-overhangTol) and reach vMax.
	hEdges = []lEdge{
		{orient: 'h', x0: 150, x1: vMax, top: dataTop, bottom: dataTop},
		{orient: 'h', x0: 150, x1: vMax, top: tableBot, bottom: tableBot},
	}
	bands := [][2]float64{{100, 120}, {120, 140}, {140, 160}, {160, 180}}
	years := []string{"2001", "2002", "2003", "2004"}
	col1 := []string{"1.1", "2.2", "3.3", "4.4"}
	col2 := []string{"5.5", "6.6", "7.7", "8.8"}
	col3 := []string{"9.0", "10.1", "11.2", "12.3"}
	for r, b := range bands {
		words = append(words,
			wordAtBand(years[r], 160, 20, b[0], b[1]), // ax=170 < vMin → open Year column
			wordAtBand(col1[r], 210, 20, b[0], b[1]),  // ax=220 → data col 1
			wordAtBand(col2[r], 260, 20, b[0], b[1]),  // ax=270 → data col 2
			wordAtBand(col3[r], 310, 20, b[0], b[1]),  // ax=320 → data col 3
		)
	}
	media = [4]float64{0, 0, 612, 792}
	return cells, hEdges, words, media
}

// TestInferRectBorderedRowsCleanFont is the headline content check: the open Year column is
// recovered as col0 and every data value lands on its own row, aligned across columns. This is
// the correctness evidence the ERP corpus fixture cannot supply (font-broken).
func TestInferRectBorderedRowsCleanFont(t *testing.T) {
	cells, hEdges, words, media := rectBorderedFixture()
	out := inferRectBorderedRows(cells, words, hEdges, media)
	grid := reconstructGrid(out, words)

	if len(grid) != 4 {
		t.Fatalf("rows: got %d, want 4\ngrid=%v", len(grid), grid)
	}
	if len(grid[0]) != 4 {
		t.Fatalf("cols: got %d, want 4 (recovered Year + 3 data)\ngrid=%v", len(grid[0]), grid)
	}
	wantCol0 := []string{"2001", "2002", "2003", "2004"}
	wantCol1 := []string{"1.1", "2.2", "3.3", "4.4"}
	wantCol3 := []string{"9.0", "10.1", "11.2", "12.3"}
	for r := range grid {
		if grid[r][0] != wantCol0[r] {
			t.Errorf("grid[%d][0] = %q, want %q (recovered open Year column)", r, grid[r][0], wantCol0[r])
		}
		if grid[r][1] != wantCol1[r] {
			t.Errorf("grid[%d][1] = %q, want %q (data aligned to its inferred row)", r, grid[r][1], wantCol1[r])
		}
		if grid[r][3] != wantCol3[r] {
			t.Errorf("grid[%d][3] = %q, want %q (rightmost data column intact)", r, grid[r][3], wantCol3[r])
		}
	}
}

// TestInferRectBorderedRowsCalloutBoxGuard locks the A4 anti-fabrication guard: a single-cell
// framed region (a callout box) holding several text lines is NOT split into spurious rows,
// because distinctCols(full) < 2. This is the framed-non-data false-positive surface.
func TestInferRectBorderedRowsCalloutBoxGuard(t *testing.T) {
	cells := []lCell{{x0: 200, top: 100, x1: 350, bottom: 200}}
	hEdges := []lEdge{
		{orient: 'h', x0: 200, x1: 350, top: 100, bottom: 100},
		{orient: 'h', x0: 200, x1: 350, top: 200, bottom: 200},
	}
	bands := [][2]float64{{100, 120}, {120, 140}, {140, 160}, {160, 180}}
	lines := []string{"Note line one", "Note line two", "Note line three", "Note line four"}
	var words []Word
	for r, b := range bands {
		words = append(words, wordAtBand(lines[r], 220, 100, b[0], b[1]))
	}
	media := [4]float64{0, 0, 612, 792}

	out := inferRectBorderedRows(cells, words, hEdges, media)
	if len(out) != len(cells) {
		t.Errorf("callout box: got %d cells, want %d unchanged (a single-column frame must not be split)", len(out), len(cells))
	}
}

// TestInferRectBorderedRowsInteriorRuleGuard locks the rows-unruled guard: a table with an
// interior horizontal rule in its data body is a ruled table and must be left untouched.
func TestInferRectBorderedRowsInteriorRuleGuard(t *testing.T) {
	cells, hEdges, words, media := rectBorderedFixture()
	hEdges = append(hEdges, lEdge{orient: 'h', x0: 200, x1: 350, top: 150, bottom: 150})

	out := inferRectBorderedRows(cells, words, hEdges, media)
	if len(out) != len(cells) {
		t.Errorf("interior rule present: got %d cells, want %d unchanged (a ruled table must not be row-inferred)", len(out), len(cells))
	}
}

// TestInferRectBorderedRowsUndecodableOpenColumn locks the decodable-words guard: an open-side
// run that decodes to nothing but replacement characters (the ERP per-row leader) must NOT be
// fabricated into a column. The closed data columns still split; only the phantom open column
// is suppressed.
func TestInferRectBorderedRowsUndecodableOpenColumn(t *testing.T) {
	cells, hEdges, words, media := rectBorderedFixture()
	var garbled []Word
	for _, w := range words {
		if w.X+w.W/2 < 200 { // the open Year column words
			w.S = "���"
		}
		garbled = append(garbled, w)
	}

	out := inferRectBorderedRows(cells, garbled, hEdges, media)
	grid := reconstructGrid(out, garbled)
	if len(grid) == 0 {
		t.Fatal("empty grid")
	}
	if len(grid[0]) != 3 {
		t.Errorf("cols: got %d, want 3 (undecodable open column must not be fabricated)\ngrid=%v", len(grid[0]), grid)
	}
}

// TestInferRectBorderedRowsMultiColumnProseGuard locks the framed-multi-column-prose A4 guard:
// a boxed two-column sidebar (vertical divider, no interior rules) whose two columns carry
// INDEPENDENT prose lines at unaligned Y positions must NOT be split into spurious rows. The
// rowAligned check rejects it because few inferred bands are cross-column (each prose line sits
// in one column at its own Y, unlike a data row that spans columns at a shared Y).
func TestInferRectBorderedRowsMultiColumnProseGuard(t *testing.T) {
	const dataTop, tableBot = 100.0, 200.0
	cells := []lCell{
		{x0: 200, top: dataTop, x1: 275, bottom: tableBot},
		{x0: 275, top: dataTop, x1: 350, bottom: tableBot},
	}
	hEdges := []lEdge{
		{orient: 'h', x0: 200, x1: 350, top: dataTop, bottom: dataTop},
		{orient: 'h', x0: 200, x1: 350, top: tableBot, bottom: tableBot},
	}
	// Left column lines at ay 110/130/150/170; right column at 115/142/168 — independent flow.
	words := []Word{
		wordAtBand("Left one", 210, 60, 105, 115),
		wordAtBand("Left two", 210, 60, 125, 135),
		wordAtBand("Left three", 210, 60, 145, 155),
		wordAtBand("Left four", 210, 60, 165, 175),
		wordAtBand("Right A", 285, 60, 110, 120),
		wordAtBand("Right B", 285, 60, 137, 147),
		wordAtBand("Right C", 285, 60, 163, 173),
	}
	media := [4]float64{0, 0, 612, 792}

	out := inferRectBorderedRows(cells, words, hEdges, media)
	if len(out) != len(cells) {
		t.Errorf("multi-column prose: got %d cells, want %d unchanged (independent-column prose must not be row-split)", len(out), len(cells))
	}
}

// TestInferRectBorderedRowsStrayAlignedTextGuard locks the rowAligned containment fix: a boxed
// two-column prose region with INDEPENDENT in-box lines must stay unsplit even when the page
// carries aligned body text ABOVE and BELOW the box in the same X columns. Such out-of-box text
// must not be mapped onto the box's row bands (the bug a plain nearest-band scan over all page
// words would introduce — page text faking cross-column alignment).
func TestInferRectBorderedRowsStrayAlignedTextGuard(t *testing.T) {
	const dataTop, tableBot = 100.0, 200.0
	cells := []lCell{
		{x0: 200, top: dataTop, x1: 275, bottom: tableBot},
		{x0: 275, top: dataTop, x1: 350, bottom: tableBot},
	}
	hEdges := []lEdge{
		{orient: 'h', x0: 200, x1: 350, top: dataTop, bottom: dataTop},
		{orient: 'h', x0: 200, x1: 350, top: tableBot, bottom: tableBot},
	}
	words := []Word{
		// In-box, independent (misaligned) prose lines.
		wordAtBand("Left one", 210, 60, 105, 115),
		wordAtBand("Left two", 210, 60, 125, 135),
		wordAtBand("Left three", 210, 60, 145, 155),
		wordAtBand("Left four", 210, 60, 165, 175),
		wordAtBand("Right A", 285, 60, 110, 120),
		wordAtBand("Right B", 285, 60, 137, 147),
		wordAtBand("Right C", 285, 60, 163, 173),
		// Aligned page body text ABOVE the box (ay~50) in BOTH columns — out of the data body.
		wordAtBand("above L", 210, 60, 45, 55),
		wordAtBand("above R", 285, 60, 45, 55),
		// Aligned page body text BELOW the box (ay~250) in BOTH columns — out of the data body.
		wordAtBand("below L", 210, 60, 245, 255),
		wordAtBand("below R", 285, 60, 245, 255),
	}
	media := [4]float64{0, 0, 612, 792}

	out := inferRectBorderedRows(cells, words, hEdges, media)
	if len(out) != len(cells) {
		t.Errorf("stray aligned page text: got %d cells, want %d unchanged (out-of-box words must not satisfy row alignment)", len(out), len(cells))
	}
}

// TestInferRectBorderedRowsFullyRuledWrappedLastRow locks the body-fraction guard: a fully-ruled
// table (interior h-rules between every row) whose LAST row wraps to several baseline-aligned
// lines per column must be left unchanged. Those last-row cells reach the table bottom and hold
// >=3 word clusters, and the data-body-interior rule count between them is 0 (no rule inside one
// row) — so only the body-fraction check (the wrapped row spans far less than rectMinBodyFrac of
// the table) prevents a 0-regression break on a ruled table.
func TestInferRectBorderedRowsFullyRuledWrappedLastRow(t *testing.T) {
	rowTops := []float64{100, 130, 160, 190} // 3 rows; table bottom = 190
	x0s := []float64{200, 275}
	x1s := []float64{275, 350}
	var cells []lCell
	for r := 0; r+1 < len(rowTops); r++ {
		for c := range x0s {
			cells = append(cells, lCell{x0: x0s[c], x1: x1s[c], top: rowTops[r], bottom: rowTops[r+1]})
		}
	}
	var hEdges []lEdge // fully ruled: an h-rule at every row boundary, spanning both columns
	for _, y := range rowTops {
		hEdges = append(hEdges, lEdge{orient: 'h', x0: 200, x1: 350, top: y, bottom: y})
	}
	words := []Word{
		wordAtBand("r1c1", 210, 40, 100, 130),
		wordAtBand("r1c2", 285, 40, 100, 130),
		wordAtBand("r2c1", 210, 40, 130, 160),
		wordAtBand("r2c2", 285, 40, 130, 160),
		// Last row [160,190]: 3 baseline-aligned wrapped lines per column.
		wordAtBand("wrap a1", 210, 40, 163, 167),
		wordAtBand("wrap a2", 210, 40, 170, 174),
		wordAtBand("wrap a3", 210, 40, 177, 181),
		wordAtBand("wrap b1", 285, 40, 163, 167),
		wordAtBand("wrap b2", 285, 40, 170, 174),
		wordAtBand("wrap b3", 285, 40, 177, 181),
	}
	media := [4]float64{0, 0, 612, 792}

	out := inferRectBorderedRows(cells, words, hEdges, media)
	if len(out) != len(cells) {
		t.Errorf("fully-ruled wrapped last row: got %d cells, want %d unchanged (a ruled table's bottom row must not be row-split)", len(out), len(cells))
	}
}
