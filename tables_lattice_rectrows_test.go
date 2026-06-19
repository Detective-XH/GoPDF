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

// rectBorderedFixtureWithLeaders builds the same rect-bordered fixture as rectBorderedFixture but
// injects one dot-leader word per row into the open Year column's x-range, alongside the year
// word. The leader word (X=162, W=30 → ax=177 < vMin=200) lands in the same open-column cell as
// the year. Pre-fix, reconstructGrid emits "2001 .........." for row 0; post-fix it emits "2001".
func rectBorderedFixtureWithLeaders() (cells []lCell, hEdges []lEdge, words []Word, media [4]float64) {
	cells, hEdges, words, media = rectBorderedFixture()
	bands := [][2]float64{{100, 120}, {120, 140}, {140, 160}, {160, 180}}
	for _, b := range bands {
		// ax = 162 + 30/2 = 177 < vMin(200): lands in the open Year column cell.
		words = append(words, wordAtBand("..........", 162, 30, b[0], b[1]))
	}
	return cells, hEdges, words, media
}

// TestReconstructGridDropsDotLeaderInAnchor locks locus A of the dot-leader fix: when a
// tabular dot-leader word co-occupies the open Year column cell with the year token, reconstructGrid
// must drop the leader and emit bare year text, NOT "2001 ..........".
// The Year column must still be recovered (4 cols total), because the year word is decodable.
func TestReconstructGridDropsDotLeaderInAnchor(t *testing.T) {
	cells, hEdges, words, media := rectBorderedFixtureWithLeaders()
	out := inferRectBorderedRows(cells, words, hEdges, media)
	grid := reconstructGrid(out, words)

	if len(grid) == 0 {
		t.Fatal("empty grid")
	}
	if len(grid[0]) != 4 {
		t.Fatalf("cols: got %d, want 4 (Year column still recovered — year word is decodable)\ngrid=%v", len(grid[0]), grid)
	}
	wantCol0 := []string{"2001", "2002", "2003", "2004"}
	for r := range grid {
		if grid[r][0] != wantCol0[r] {
			t.Errorf("grid[%d][0] = %q, want %q (dot-leader must be trimmed, not joined into cell text)", r, grid[r][0], wantCol0[r])
		}
	}
}

// TestReconstructGridRejoinsCommaSplitAcrossBoundary locks the comma-bleed fix: a value a
// zero-advance space glyph fragmented into two OVERLAPPING words ("2,1"+"20", gap -1 mirroring the
// observed NASS -0.03) that straddle a column boundary must be re-joined whole by mergeAbuttingWords
// before assignment, landing in ONE cell as "2,120". Pre-fix it bleeds (A="2,1", B="20"). A vertical
// rule is placed AT the boundary to prove the rule-guard never blocks a genuine fragment pair: a
// rule cannot lie between two overlapping boxes, so the merge proceeds. No real fixture scores this
// (NASS's anchor labels block row alignment), so this synthetic test is the only guard on the fix.
func TestReconstructGridRejoinsCommaSplitAcrossBoundary(t *testing.T) {
	const top, bot = 100.0, 120.0
	cells := []lCell{
		{x0: 200, top: top, x1: 250, bottom: bot}, // cell A
		{x0: 250, top: top, x1: 300, bottom: bot}, // cell B
	}
	words := []Word{
		wordAtBand("2,1", 224, 24, top, bot), // right edge 248, center 236 -> A
		wordAtBand("20", 247, 16, top, bot),  // left 247 < 248 (overlap, gap -1), center 255 -> B
	}
	rule := lEdge{orient: 'v', x0: 248, x1: 248, top: top, bottom: bot} // at the overlap, cannot lie "between"
	grid := reconstructGrid(cells, words, rule)
	if len(grid) != 1 || len(grid[0]) != 2 {
		t.Fatalf("grid shape: got %d rows; want 1x2\ngrid=%v", len(grid), grid)
	}
	if grid[0][0] != "2,120" {
		t.Errorf("grid[0][0] = %q, want %q (overlapping fragments re-joined whole despite a coincident rule)", grid[0][0], "2,120")
	}
	if grid[0][1] != "" {
		t.Errorf("grid[0][1] = %q, want \"\" (right fragment must not bleed into the next cell)", grid[0][1])
	}
}

// TestReconstructGridKeepsRuledColumnsSeparate is the ruled-table FP-guard: two distinct values
// that abut at a cell border with ZERO whitespace padding ("100"|"200", gap 0) but are divided by a
// real vertical rule must NOT be welded. Whitespace is not the only visual separator — a column rule
// is — so mergeAbuttingWords forbids a merge across a rule (ruleBetween). Without the guard the gap-0
// pair would concatenate to "100200" in one cell; the rule keeps them in their own columns.
func TestReconstructGridKeepsRuledColumnsSeparate(t *testing.T) {
	const top, bot = 100.0, 120.0
	cells := []lCell{
		{x0: 200, top: top, x1: 250, bottom: bot},
		{x0: 250, top: top, x1: 300, bottom: bot},
	}
	words := []Word{
		wordAtBand("100", 230, 20, top, bot), // right edge 250, center 240 -> A
		wordAtBand("200", 250, 20, top, bot), // left 250, gap 0 (touches the border), center 260 -> B
	}
	rule := lEdge{orient: 'v', x0: 250, x1: 250, top: top, bottom: bot} // the column rule between A and B
	grid := reconstructGrid(cells, words, rule)
	if len(grid) != 1 || len(grid[0]) != 2 {
		t.Fatalf("grid shape: got %d rows; want 1x2\ngrid=%v", len(grid), grid)
	}
	if grid[0][0] != "100" || grid[0][1] != "200" {
		t.Errorf("grid = [%q %q], want [\"100\" \"200\"] (a real column rule must keep zero-padding values separate)", grid[0][0], grid[0][1])
	}
}

// TestReconstructGridKeepsDistinctValuesSeparate is the FP-guard for the comma-bleed fix: two
// genuinely separate column values must NOT be welded. The gap here is 2.0pt — a REALISTIC minimal
// inter-value separation (a single word space advances the pen ~this far, and real columns are a
// whole column's padding apart), well above wordJoinGapTol(0.25) yet far below it. This is the
// load-bearing case (a 30pt gap would be a happy path); it proves the threshold sits below real
// spacing, so only fragments — which abut at gap <= 0 — are re-joined. The residual: two distinct
// values rendered with < tol visual separation (a degenerate zero-padding layout that renders as
// one visual run) would weld; faithful to the page, no corpus instance.
func TestReconstructGridKeepsDistinctValuesSeparate(t *testing.T) {
	const top, bot = 100.0, 120.0
	cells := []lCell{
		{x0: 200, top: top, x1: 250, bottom: bot},
		{x0: 250, top: top, x1: 300, bottom: bot},
	}
	words := []Word{
		wordAtBand("100", 228, 20, top, bot), // right edge 248, center 238 -> A
		wordAtBand("200", 250, 20, top, bot), // left edge 250, gap 2.0 > tol -> NOT merged, center 260 -> B
	}
	grid := reconstructGrid(cells, words)
	if len(grid) != 1 || len(grid[0]) != 2 {
		t.Fatalf("grid shape: got %d rows; want 1x2\ngrid=%v", len(grid), grid)
	}
	if grid[0][0] != "100" || grid[0][1] != "200" {
		t.Errorf("grid = [%q %q], want [\"100\" \"200\"] (a real 2pt inter-value gap must stay separate, not over-merged)", grid[0][0], grid[0][1])
	}
}

// TestSynthOpenColumnDropsPureDotLeader locks locus B of the dot-leader fix: when every word in
// the open Year column is a pure dot-leader (decodable dots, but no year data), synthOpenColumns
// must NOT fabricate a column from those leaders. This is the decodable-leader analog of
// TestInferRectBorderedRowsUndecodableOpenColumn (which tests U+FFFD leaders).
func TestSynthOpenColumnDropsPureDotLeader(t *testing.T) {
	cells, hEdges, words, media := rectBorderedFixture()
	// Replace every open-Year-column word with a pure dot-leader.
	var modified []Word
	for _, w := range words {
		if w.X+w.W/2 < 200 { // open Year column: ax < vMin=200
			w.S = "......"
		}
		modified = append(modified, w)
	}

	out := inferRectBorderedRows(cells, modified, hEdges, media)
	grid := reconstructGrid(out, modified)
	if len(grid) == 0 {
		t.Fatal("empty grid")
	}
	if len(grid[0]) != 3 {
		t.Errorf("cols: got %d, want 3 (pure dot-leader open column must not be fabricated)\ngrid=%v", len(grid[0]), grid)
	}
}

// TestIsDotLeader is a table-driven unit test for the isDotLeader predicate.
// True: a token of >=4 consecutive '.' and nothing else.
// False: <4 dots, empty, mixed chars, a space anywhere, single U+2026 ellipsis rune.
func TestIsDotLeader(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"....", true},
		{"....................", true},   // 20 dots
		{"......................", true}, // 22 dots
		{"...", false},                   // only 3 dots — below the >=4 floor
		{"", false},
		{".", false},
		{"4.0", false},    // decimal: mixed chars
		{"U.S.A.", false}, // abbreviation: mixed chars
		{". ...", false},  // space breaks the all-'.' invariant
		{"…", false},      // U+2026 HORIZONTAL ELLIPSIS — not U+002E
		{"..a..", false},  // non-dot rune in the middle
	}
	for _, tc := range cases {
		got := isDotLeader(tc.s)
		if got != tc.want {
			t.Errorf("isDotLeader(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// TestReconstructGridKeepsDotOnlyCell locks the contextual-trim guard (codex finding): the
// dot-leader trim fires only when a cell ALSO holds real (non-leader) content. A cell whose
// ENTIRE content is a dot run is preserved verbatim, never silently erased to empty. col0 here
// carries a real label and must keep it; col1 holds only a dot run and must survive intact.
func TestReconstructGridKeepsDotOnlyCell(t *testing.T) {
	cells := []lCell{
		{x0: 200, top: 100, x1: 250, bottom: 120},
		{x0: 250, top: 100, x1: 300, bottom: 120},
	}
	words := []Word{
		wordAtBand("Label", 210, 30, 100, 120),  // ax=225 -> col0 (real content)
		wordAtBand("......", 260, 30, 100, 120), // ax=275 -> col1 (pure dot-only cell)
	}
	grid := reconstructGrid(cells, words)
	if len(grid) == 0 || len(grid[0]) < 2 {
		t.Fatalf("unexpected grid shape: %v", grid)
	}
	if grid[0][0] != "Label" {
		t.Errorf("grid[0][0] = %q, want %q (real content unaffected)", grid[0][0], "Label")
	}
	if grid[0][1] != "......" {
		t.Errorf("grid[0][1] = %q, want %q (a dot-only cell must be preserved, not erased)", grid[0][1], "......")
	}
}
