package pdf

import "testing"

// Per-cell-grid column-cut recovery unit tests (PR-1). They lock the synthetic mechanism of
// inferColumnCuts / splitWideBandCells and its false-positive guards: corroboration (a cut x must
// match a real column boundary of THIS table — the NIST p5 sibling-table guard) and content-straddle
// (the cell's words must land in ≥2 of the new sub-cells — the spanning-label/merged-header guard).
// Real-world cross-publisher coverage is the deferred de-bias corpus fixtures (PR-1 acceptance #4);
// these tests lock the data-structure invariant the mechanism rests on.
//
// Coordinate convention (tables_lattice.go:21-23): top <= bottom, "below = larger". The header band
// [-20,-15] sits above the data band [-15,-5].

func pcgCell(x0, x1, top, bottom float64) lCell {
	return lCell{x0: x0, top: top, x1: x1, bottom: bottom}
}

func pcgVEdge(x, top, bottom float64) lEdge {
	return lEdge{x0: x, x1: x, top: top, bottom: bottom, orient: 'v'}
}

// splitHeaderRow is three narrow header cells establishing column boundaries at x=100,200,300,400.
func splitHeaderRow() []lCell {
	return []lCell{
		pcgCell(100, 200, -20, -15),
		pcgCell(200, 300, -20, -15),
		pcgCell(300, 400, -20, -15),
	}
}

// dataWord returns a word centered at ax inside the data band (ay = -10 ∈ [-15,-5]).
func dataWord(ax float64) Word { return Word{S: "9", X: ax - 3, Y: 8, W: 6, H: 4} }

// TestSplitWideBandCellsVEdgeCorroborated: a fused wide data cell whose words populate each column,
// with corroborated interior v-edges, splits into its columns.
func TestSplitWideBandCellsVEdgeCorroborated(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(200, -20, -5), pcgVEdge(300, -20, -5)}
	words := []Word{dataWord(150), dataWord(250), dataWord(350)} // one per column
	out := splitWideBandCells(cells, words, vEdges)
	if len(out) != 6 { // 3 header + 3 split data
		t.Fatalf("corroborated v-edge split: got %d cells, want 6", len(out))
	}
	if dc := distinctCols(out); dc != 3 {
		t.Errorf("corroborated v-edge split: distinctCols=%d, want 3", dc)
	}
	for _, want := range [][2]float64{{100, 200}, {200, 300}, {300, 400}} {
		found := false
		for _, c := range out {
			if c.top == -15 && c.x0 == want[0] && c.x1 == want[1] {
				found = true
			}
		}
		if !found {
			t.Errorf("split data sub-cell [%v,%v] missing", want[0], want[1])
		}
	}
}

// TestSplitWideBandCellsRejectsUncorroboratedVEdge is the NIST p5 false-positive guard: a v-edge
// from another table (x=250, matching NO sibling-cell boundary) must NOT split the wide cell.
func TestSplitWideBandCellsRejectsUncorroboratedVEdge(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(250, -20, -5)}
	words := []Word{dataWord(150), dataWord(350)}
	assertWideCellIntact(t, splitWideBandCells(cells, words, vEdges), "uncorroborated v-edge")
}

// TestSplitWideBandCellsRejectsNonSpanningContent is the content-straddle guard (codex finding 1):
// corroborated, geometrically real cuts exist, but the cell's content sits in ONE column (a spanning
// label / merged-header title). The cell must be left intact rather than broken across columns.
func TestSplitWideBandCellsRejectsNonSpanningContent(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(200, -20, -5), pcgVEdge(300, -20, -5)} // real, corroborated cuts
	words := []Word{dataWord(150)}                                    // content only in the first column
	assertWideCellIntact(t, splitWideBandCells(cells, words, vEdges), "single-column content")
}

// TestSplitWideBandCellsBoundaryWordNotDoubleCounted guards the straddle gate against a single word
// whose anchor lands EXACTLY on a corroborated cut: half-open sub-cell membership must count it once
// (→ ≥2 fails → no split), not once per adjacent sub-cell. A centered single-word label must stay intact.
func TestSplitWideBandCellsBoundaryWordNotDoubleCounted(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(200, -20, -5), pcgVEdge(300, -20, -5)}
	words := []Word{dataWord(200)} // single word centered exactly on the x=200 cut
	assertWideCellIntact(t, splitWideBandCells(cells, words, vEdges), "boundary word double-count")
}

// TestSplitWideBandCellsNoWordXFallback documents that the word-X (G4) path is deferred: with no
// v-edges, a wide cell is NOT split on word spacing (a high-FP path; G4 lands in a later PR).
func TestSplitWideBandCellsNoWordXFallback(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	words := []Word{dataWord(150), dataWord(250), dataWord(350)}
	assertWideCellIntact(t, splitWideBandCells(cells, words, nil), "no-vedge word-X")
}

// assertWideCellIntact fails unless the [100,400] wide cell survived splitWideBandCells unchanged.
func assertWideCellIntact(t *testing.T, out []lCell, ctx string) {
	t.Helper()
	for _, c := range out {
		if c.x0 == 100 && c.x1 == 400 {
			return
		}
	}
	t.Errorf("%s: wide cell [100,400] was split (false positive not guarded)", ctx)
}

// --- PR-2: row-split (splitTallBandCells) unit tests ---
//
// Coordinate convention: top <= bottom, "below = larger" (top-origin). The data body spans
// [bodyTop, bodyBot] = [10, 200]. Columns are at x=100,200,300,400.
// Word anchor: ax = w.X+w.W/2, ay = -(w.Y+w.H/2).
//
// tallWord builds a word with a numeric token at the given (ax, ay) position, placed in
// column col (0-based, width 100) and at the specified body ay. Since ay = -(Y + H/2), to
// produce ay we set Y = -(ay + H/2). H=2, W=6: ax = X+3 → X = ax-3; Y = -(ay+1).
func tallWord(ax, ay float64) Word {
	return Word{S: "42", X: ax - 3, Y: -(ay + 1), W: 6, H: 2}
}

// tallTextWord is the same geometry but with a non-numeric token (header/label text).
func tallTextWord(ax, ay float64) Word {
	return Word{S: "Total", X: ax - 3, Y: -(ay + 1), W: 6, H: 2}
}

// tallCells builds a two-column setup: one header row and one tall fused data cell spanning
// the full data body, representing a collapsed table column (both columns share one tall cell
// spanning [bodyTop, bodyBot]).
func tallCells() []lCell {
	return []lCell{
		pcgCell(100, 200, 10, 30),  // col-A header
		pcgCell(200, 300, 10, 30),  // col-B header
		pcgCell(100, 200, 30, 200), // col-A tall data cell (collapsed)
		pcgCell(200, 300, 30, 200), // col-B tall data cell (collapsed)
	}
}

// tallWordsAt generates n evenly-spaced numeric cross-column bands inside (30, 200).
// Each band has one word in col-A (ax=150) and one in col-B (ax=250).
func tallWordsAt(n int) []Word {
	var ws []Word
	step := (200.0 - 30.0) / float64(n+1)
	for i := 1; i <= n; i++ {
		ay := 30.0 + step*float64(i)
		ws = append(ws, tallWord(150, ay), tallWord(250, ay))
	}
	return ws
}

// TestSplitTallBandCellsSplitsCollapsedRows: a tall data cell with ≥3 numeric cross-column
// bands is split into rows (the FT-900 analog). Four bands → four sub-rows per column.
func TestSplitTallBandCellsSplitsCollapsedRows(t *testing.T) {
	cells := tallCells()
	words := tallWordsAt(4) // 4 numeric cross-column bands
	out := splitTallBandCells(cells, words, 10, 200)
	// 2 header cells + 4 sub-rows × 2 columns = 10 cells total
	if len(out) != 10 {
		t.Fatalf("split collapsed rows: got %d cells, want 10 (2 header + 4×2 data)", len(out))
	}
	// All output cells must have the same x0/x1 as their column.
	for _, c := range out {
		if c.x0 != 100 && c.x0 != 200 {
			t.Errorf("unexpected x0=%v in output", c.x0)
		}
	}
	// The tall data cells must have been replaced by sub-rows: no cell should span [30,200].
	for _, c := range out {
		if c.top == 30 && c.bottom == 200 {
			t.Errorf("tall cell [30,200] was not split")
		}
	}
}

// TestSplitTallBandCellsNumericGate: bands with only text tokens (no numeric word) must NOT
// be split. This is the header-wrap guard: a header cell wrapping over multiple lines keeps
// all lines in one column of text, so no record band is created and the cell is left intact.
func TestSplitTallBandCellsNumericGate(t *testing.T) {
	cells := tallCells()
	// 4 text-only bands, cross-column — should NOT fire because none are numeric.
	step := (200.0 - 30.0) / 5.0
	var words []Word
	for i := 1; i <= 4; i++ {
		ay := 30.0 + step*float64(i)
		words = append(words, tallTextWord(150, ay), tallTextWord(250, ay))
	}
	out := splitTallBandCells(cells, words, 10, 200)
	if len(out) != len(cells) {
		t.Fatalf("numeric gate: got %d cells, want %d (no split on text-only bands)", len(out), len(cells))
	}
	// The tall cells must be unchanged.
	for _, c := range out {
		if c.top == 30 && c.bottom == 200 {
			return // found untouched tall cell — OK
		}
	}
	t.Error("numeric gate: tall cell [30,200] unexpectedly missing from output")
}

// TestSplitTallBandCellsCrossColumnGate: bands are numeric but all words in ONE column — the
// single-column wrapped label guard. The cell must be left intact.
func TestSplitTallBandCellsCrossColumnGate(t *testing.T) {
	cells := tallCells()
	// 4 numeric bands but all words in col-A (ax=150) only.
	step := (200.0 - 30.0) / 5.0
	var words []Word
	for i := 1; i <= 4; i++ {
		ay := 30.0 + step*float64(i)
		words = append(words, tallWord(150, ay)) // single column only
	}
	out := splitTallBandCells(cells, words, 10, 200)
	if len(out) != len(cells) {
		t.Fatalf("cross-column gate: got %d cells, want %d (no split on single-column bands)", len(out), len(cells))
	}
}

// TestSplitTallBandCellsThresholdGate: 2 numeric cross-column bands (< rectMinRowSplit=4) must NOT
// trigger a split. A 2-line multi-column value should be left intact.
func TestSplitTallBandCellsThresholdGate(t *testing.T) {
	cells := tallCells()
	words := tallWordsAt(2) // 2 numeric cross-column bands
	out := splitTallBandCells(cells, words, 10, 200)
	if len(out) != len(cells) {
		t.Fatalf("threshold gate: got %d cells, want %d (2 bands < threshold=%d)", len(out), len(cells), rectMinRowSplit)
	}
}

// TestSplitTallBandCellsThreeBandNoSplit: exactly 3 numeric cross-column bands must NOT split — the
// rectMinRowSplit=4 tightening. A corpus sweep of the shipped mechanism showed the exactly-3-band
// zone is false-positive-dense (blank-row insertion at group separators, displaced multi-line
// headers, TOC/cover pages), while every genuine collapsed data table carries far more bands.
func TestSplitTallBandCellsThreeBandNoSplit(t *testing.T) {
	cells := tallCells()
	words := tallWordsAt(3) // 3 numeric cross-column bands — below the >=4 split threshold
	out := splitTallBandCells(cells, words, 10, 200)
	if len(out) != len(cells) {
		t.Fatalf("three-band gate: got %d cells, want %d (3 bands < threshold=%d)", len(out), len(cells), rectMinRowSplit)
	}
}

// TestSplitTallBandCellsNoOpAlreadySplit: a set already containing one band per cell (each cell
// spans only its own band) is returned unchanged — no spurious extra splits.
func TestSplitTallBandCellsNoOpAlreadySplit(t *testing.T) {
	// Two columns, three rows already correctly split: 6 cells total.
	cells := []lCell{
		pcgCell(100, 200, 10, 70),
		pcgCell(200, 300, 10, 70),
		pcgCell(100, 200, 70, 130),
		pcgCell(200, 300, 70, 130),
		pcgCell(100, 200, 130, 200),
		pcgCell(200, 300, 130, 200),
	}
	// One numeric word per cell (centered in each row band, both columns).
	words := []Word{
		tallWord(150, 40), tallWord(250, 40),
		tallWord(150, 100), tallWord(250, 100),
		tallWord(150, 165), tallWord(250, 165),
	}
	out := splitTallBandCells(cells, words, 10, 200)
	if len(out) != len(cells) {
		t.Fatalf("no-op already-split: got %d cells, want %d", len(out), len(cells))
	}
	// Verify tops are unchanged.
	wantTops := []float64{10, 10, 70, 70, 130, 130}
	for i, c := range out {
		if c.top != wantTops[i] {
			t.Errorf("no-op already-split: cell %d top=%v, want %v", i, c.top, wantTops[i])
		}
	}
}

// TestSplitTallBandCellsPoisonWordsOutsideBody: out-of-table page words (a numeric page number / date
// in the margin, neighbouring-layout text) at the SAME Y as text-only in-table bands must NOT make
// those bands count as numeric cross-column record bands. The in-table content here is TEXT only; the
// only numeric, multi-column words sit OUTSIDE [vMin,vMax] (ax=50 < vMin=100 and ax=350 > vMax=300).
// Because every gate is scoped to the in-body words (tallBandBodyWords), the poison words are filtered
// out and the cell is left intact. (Codex adversarial-review finding, 2026-06-20.)
func TestSplitTallBandCellsPoisonWordsOutsideBody(t *testing.T) {
	cells := tallCells() // cols 100-200 / 200-300 → vMin=100, vMax=300; tall data cells [30,200]
	step := (200.0 - 30.0) / 5.0
	var words []Word
	for i := 1; i <= 4; i++ {
		ay := 30.0 + step*float64(i)
		words = append(words, tallTextWord(150, ay), tallTextWord(250, ay)) // in-table: TEXT only
		words = append(words, tallWord(50, ay), tallWord(350, ay))          // OUTSIDE body: numeric, 2 cols
	}
	out := splitTallBandCells(cells, words, 10, 200)
	if len(out) != len(cells) {
		t.Fatalf("poison-word guard: got %d cells, want %d (out-of-body numeric words must not satisfy the gates)", len(out), len(cells))
	}
}

// annotWord is an in-body word whose token is numeric-LOOKING (a date / footnote marker) but is NOT
// table data — numericTokenWord("2024") is true.
func annotWord(ax, ay float64) Word { return Word{S: "2024", X: ax - 3, Y: -(ay + 1), W: 6, H: 2} }

// TestSplitTallBandCellsInBodyAnnotationNoSplit: four IN-BODY text bands, each with a numeric-looking
// marker (a date "2024") in ONE column and ordinary text in another, must NOT split. The numeric and
// cross-column tests are coupled (numeric tokens must appear in >=2 columns), so a single-column
// marker plus text elsewhere is not a record band — genuine data carries numeric values across >=2
// columns. (Codex adversarial-review round-2 finding, 2026-06-20.)
func TestSplitTallBandCellsInBodyAnnotationNoSplit(t *testing.T) {
	cells := tallCells()
	step := (200.0 - 30.0) / 5.0
	var words []Word
	for i := 1; i <= 4; i++ {
		ay := 30.0 + step*float64(i)
		words = append(words, annotWord(150, ay))    // col A: numeric-looking marker, ONE column only
		words = append(words, tallTextWord(250, ay)) // col B: ordinary text
	}
	out := splitTallBandCells(cells, words, 10, 200)
	if len(out) != len(cells) {
		t.Fatalf("in-body annotation guard: got %d cells, want %d (single-column numeric marker must not promote a text band)", len(out), len(cells))
	}
}

// --- PR-3: mergeNestedColumns unit tests ---
//
// These tests lock the three-gate predicate of mergeNestedColumns:
//   (1) SHARED WALL — |x1[i] − x1[i+1]| ≤ 3 pt (right-aligned nested sub-cell signature)
//   (2) ROW-COMPLEMENTARY — no row has both columns non-empty
//   (3) ONE-COLUMN-SPARSE — min(nonEmpty[i], nonEmpty[i+1]) ≤ phantomMaxSparseCells (=2)
//
// Geometry convention: x0/x1 are page-space horizontal coords (pt). The cells below
// reproduce the DESTATIS nested-sub-cell signature: a WIDE outer cell (phantom header)
// shares x1 with a NARROW inner cell (data column). cluster1D keeps both x0s (they are
// far enough apart to not merge), yielding two adjacent grid-columns. Each test builds its
// cells + grid inline.

// TestPR3MergeFiresSharedWallComplementarySparse: canonical phantom-pair merge.
// Two adjacent grid columns share x1 (right wall, dist=0), are row-complementary
// (header text in wide col row0, data text in narrow col row1), and sparse (1 cell each).
// Merge must fire; header text must be preserved in the merged column.
func TestPR3MergeFiresSharedWallComplementarySparse(t *testing.T) {
	// Wide col: x0=100, x1=200, w=100. Narrow col: x0=150, x1=200, w=50.
	// They share x1=200 (dist=0 < nestedWallTol=3).
	cells := []lCell{
		pcgCell(100, 200, 0, 10),  // wide — row 0 (header band)
		pcgCell(150, 200, 10, 20), // narrow — row 1 (data band)
	}
	x0s := []float64{100, 150}
	colReps := cluster1D(x0s, 4) // should be two reps: 100 and 150
	if len(colReps) != 2 {
		t.Fatalf("expected 2 colReps, got %d", len(colReps))
	}

	// Build a grid: 1 header row (wide col, sparse) + 3 data rows (narrow col, dense ≥
	// phantomMinDataCells=3 so the dense-partner gate is satisfied):
	//   row 0: ["Revenue", ""]    (header in wide col)
	//   rows 1-3: ["", "<n>"]     (data in narrow col)
	grid := [][]string{
		{"Revenue", ""},
		{"", "42"},
		{"", "43"},
		{"", "44"},
	}

	out := mergeNestedColumns(grid, cells, colReps)
	if len(out) == 0 {
		t.Fatal("mergeNestedColumns returned empty grid")
	}
	nCols := len(out[0])
	if nCols != 1 {
		t.Fatalf("expected 1 column after merge, got %d", nCols)
	}
	// Row 0: must carry "Revenue" (the header).
	if out[0][0] != "Revenue" {
		t.Errorf("row 0 merged cell = %q; want %q", out[0][0], "Revenue")
	}
	// Row 1: must carry "42" (the data value).
	if out[1][0] != "42" {
		t.Errorf("row 1 merged cell = %q; want %q", out[1][0], "42")
	}
}

// TestPR3NoMergeNonSharedWall: adjacent columns sharing a boundary (x1[i] == x0[i+1]),
// NOT the same x1. Shared-wall gate must fail → no merge.
func TestPR3NoMergeNonSharedWall(t *testing.T) {
	// Col A: x0=100, x1=150. Col B: x0=150, x1=250.
	// They share a BOUNDARY (x1[A]==x0[B]=150), not a shared x1 wall.
	// x1 values differ by 100 pt >> nestedWallTol=3.
	cells := []lCell{
		pcgCell(100, 150, 0, 10),
		pcgCell(150, 250, 0, 10),
	}
	colReps := cluster1D([]float64{100, 150}, 4)
	if len(colReps) != 2 {
		t.Fatalf("expected 2 colReps, got %d", len(colReps))
	}
	// Row-complementary and sparse — but shared-wall fails.
	grid := [][]string{
		{"Header", ""},
	}
	out := mergeNestedColumns(grid, cells, colReps)
	if len(out[0]) != 2 {
		t.Errorf("non-shared-wall: expected 2 cols (no merge), got %d", len(out[0]))
	}
}

// TestPR3NoMergeComplementaryViolation: two adjacent columns with shared x1 wall AND
// sparse, but the same row has content in BOTH → row-complementary gate must fail → no merge.
func TestPR3NoMergeComplementaryViolation(t *testing.T) {
	// Wide col: x0=100, x1=200. Narrow col: x0=160, x1=200. Shared x1=200 (dist=0).
	cells := []lCell{
		pcgCell(100, 200, 0, 10),
		pcgCell(160, 200, 0, 10),
	}
	colReps := cluster1D([]float64{100, 160}, 4)
	if len(colReps) != 2 {
		t.Fatalf("expected 2 colReps, got %d", len(colReps))
	}
	// Same row has content in both columns → not complementary.
	grid := [][]string{
		{"Header", "Value"},
	}
	out := mergeNestedColumns(grid, cells, colReps)
	if len(out[0]) != 2 {
		t.Errorf("complementary-violation: expected 2 cols (no merge), got %d", len(out[0]))
	}
}

// TestPR3NoMergeSparseViolation: adjacent columns with shared x1 wall AND row-complementary,
// but BOTH columns have more than phantomMaxSparseCells (=2) non-empty cells. This is the
// DESTATIS p5 col19+col20 over-merge guard: two data-rich complementary columns must NOT merge.
func TestPR3NoMergeSparseViolation(t *testing.T) {
	// Col A: x0=100, x1=200. Col B: x0=160, x1=200. Shared x1=200 (dist=0 pt).
	cells := []lCell{
		pcgCell(100, 200, 0, 100),
		pcgCell(160, 200, 0, 100),
	}
	colReps := cluster1D([]float64{100, 160}, 4)
	if len(colReps) != 2 {
		t.Fatalf("expected 2 colReps, got %d", len(colReps))
	}
	// Build a 6-row grid with 3 non-empty cells in each column, alternating (complementary).
	// min(3, 3) = 3 > phantomMaxSparseCells=2 → sparse gate fails.
	grid := [][]string{
		{"A1", ""},
		{"", "B2"},
		{"A3", ""},
		{"", "B4"},
		{"A5", ""},
		{"", "B6"},
	}
	out := mergeNestedColumns(grid, cells, colReps)
	if len(out[0]) != 2 {
		t.Errorf("sparse-violation: expected 2 cols (no merge), got %d (should not merge data-rich pair)", len(out[0]))
	}
}

// TestPR3LossFreeMerge: verifies that no non-empty cell is lost after a merge fires.
// The merged output must contain every non-empty string from the original grid.
func TestPR3LossFreeMerge(t *testing.T) {
	// Wide phantom col (x0=100, x1=200) with a 2-line header (sparse: 2 cells); narrow data col
	// (x0=155, x1=200) with 3 data values (dense ≥ phantomMinDataCells=3). Complementary.
	cells := []lCell{
		pcgCell(100, 200, 0, 10),
		pcgCell(155, 200, 10, 40),
	}
	colReps := cluster1D([]float64{100, 155}, 4)
	if len(colReps) != 2 {
		t.Fatalf("expected 2 colReps, got %d", len(colReps))
	}
	grid := [][]string{
		{"Revenue", ""}, // header in wide (row 0) — wide col: 2 cells (sparse)
		{"(USD)", ""},   // header line 2 in wide (row 1)
		{"", "1,234"},   // data in narrow (row 2)
		{"", "5,678"},   // data in narrow (row 3)
		{"", "9,012"},   // data in narrow (row 4) — narrow: 3 cells ≥ phantomMinDataCells
	}
	// Count non-empty strings before merge.
	want := map[string]bool{"Revenue": true, "(USD)": true, "1,234": true, "5,678": true, "9,012": true}

	out := mergeNestedColumns(grid, cells, colReps)

	got := map[string]bool{}
	for _, row := range out {
		for _, cell := range row {
			if cell != "" {
				got[cell] = true
			}
		}
	}
	for s := range want {
		if !got[s] {
			t.Errorf("loss-free violation: %q disappeared after merge", s)
		}
	}
	// Column count must have shrunk by 1.
	if len(out[0]) != 1 {
		t.Errorf("loss-free: expected 1 column after merge, got %d", len(out[0]))
	}
}

// TestPR3NoMergeSmallTable: a tiny (2-row) complementary table with a shared x1 wall whose columns
// are trivially sparse (≤2 cells only because the table has ≤2 rows) must NOT merge — the
// dense-partner gate (max nonEmpty ≥ phantomMinDataCells) protects it. This is the EPA p1 2x2
// false positive caught by the full -race suite (TestPublicTablesGutterColumnsDropped).
func TestPR3NoMergeSmallTable(t *testing.T) {
	cells := []lCell{
		pcgCell(100, 200, 0, 10),
		pcgCell(160, 200, 0, 10),
	}
	colReps := cluster1D([]float64{100, 160}, 4)
	if len(colReps) != 2 {
		t.Fatalf("expected 2 colReps, got %d", len(colReps))
	}
	// 2-row complementary table: each column has 1 non-empty cell. Both trivially sparse, but neither
	// is a dense data column (max nonEmpty = 1 < phantomMinDataCells=3) → must not merge.
	grid := [][]string{
		{"Header", ""},
		{"", "42"},
	}
	out := mergeNestedColumns(grid, cells, colReps)
	if len(out[0]) != 2 {
		t.Errorf("small-table: expected 2 cols (no merge), got %d (a tiny table must not over-merge)", len(out[0]))
	}
}

// TestPR3NoMergeSpanningParent is the codex spanning-header FP guard (PR-3 adversarial review).
//
// Scenario: a multi-tier table where col-i has BOTH a leaf data cell (x0=100,x1=150) AND a
// spanning parent header (x0=100,x1=200). The spanning header reaches col-j's right wall (x1=200),
// so under the old MAX-x1 gate columnX1Max would return x1max=[200,200], making |x1[0]-x1[1]|=0
// which forges a shared-wall signal and would incorrectly merge two DISTINCT data columns.
//
// With the leaf-x1 (MIN) fix in columnLeafX1: col-i's leaf right edge is x1=150 (the narrow data
// cell), so leafX1=[150,200], |150-200|=50 >> nestedWallTol=3 → shared-wall gate FAILS → NO merge.
//
// Grid is row-complementary and satisfies the sparse+dense cell-count gate, so without the
// leaf-x1 fix the merge WOULD fire. This test would have FAILED under the old MAX logic.
func TestPR3NoMergeSpanningParent(t *testing.T) {
	// Col 0 (x0=100): leaf data cell ending at x1=150, plus a spanning parent ending at x1=200.
	// Col 1 (x0=150): data cells ending at x1=200.
	// Both x0=100 cells → col 0 via nearestIdx([100,150], ·).
	// x0=150 cell → col 1.
	cells := []lCell{
		pcgCell(100, 150, 10, 20), // col-0 leaf data cell  (x1=150 — the true leaf boundary)
		pcgCell(100, 200, 0, 10),  // col-0 spanning parent (x1=200 — reaches col-1's wall)
		pcgCell(150, 200, 10, 20), // col-1 data row 1
		pcgCell(150, 200, 20, 30), // col-1 data row 2
		pcgCell(150, 200, 30, 40), // col-1 data row 3
	}
	// colReps: x0s are 100, 100, 150 → cluster1D([100,100,150], 4) → [100, 150]
	colReps := cluster1D([]float64{100, 100, 150}, 4)
	if len(colReps) != 2 {
		t.Fatalf("expected 2 colReps, got %d: %v", len(colReps), colReps)
	}

	// Grid: col-0 sparse (1 header row), col-1 dense (3 data rows); row-complementary.
	// This satisfies all three original gates EXCEPT the leaf-x1 shared-wall gate.
	grid := [][]string{
		{"Header", ""}, // spanning parent row — col-0 non-empty, col-1 empty
		{"", "10"},     // data row 1 — col-0 empty, col-1 non-empty
		{"", "20"},     // data row 2
		{"", "30"},     // data row 3
	}

	out := mergeNestedColumns(grid, cells, colReps)
	// With leaf-x1 fix: leafX1[0]=150, leafX1[1]=200; |150-200|=50 > nestedWallTol → NO merge.
	// Without fix (MAX): x1max[0]=200, x1max[1]=200; |200-200|=0 → would merge (FP).
	if len(out[0]) != 2 {
		t.Errorf("spanning-parent FP: expected 2 cols (no merge), got %d — spanning parent header forged a false shared-wall signal", len(out[0]))
	}
}
