package pdf

import "testing"

// TestFillStaircaseStepsSharedXSpan locks the false-positive discriminator that separates an
// EIA-style banded staircase (nested fill rects sharing one x-span, stepped tops) from a
// bar/column chart (rects with distinct x-spans). The chart shape otherwise satisfies the
// common-bottom + stepped-top staircase premise, so the shared-x-span test is the guard that
// keeps inferFillBandedRows from promoting a chart into a banded table.
func TestFillStaircaseStepsSharedXSpan(t *testing.T) {
	const tableTop, tableBot = -200.0, 0.0

	// Nested full-width bands: identical x-span [10,500], stepped tops at -40..-100. These are
	// the EIA geometry; fillStaircaseSteps must return the four distinct tops.
	bands := []Rect{
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 40}},
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 60}},
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 80}},
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 100}},
	}
	if got := fillStaircaseSteps(bands, tableTop, tableBot, 10, 500); len(got) != 4 {
		t.Errorf("nested shared-x bands: got %d steps %v, want 4", len(got), got)
	}

	// Bar/column chart inside [vMin,vMax]: same common baseline and stepped tops, but each bar
	// has its OWN x-span ([10,60], [70,120], ...). The x0 values form four clusters, so
	// fillStaircaseSteps must reject it (nil) — a chart is not a banded staircase.
	bars := []Rect{
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 60, Y: 40}},
		{Min: Point{X: 70, Y: 0}, Max: Point{X: 120, Y: 60}},
		{Min: Point{X: 130, Y: 0}, Max: Point{X: 180, Y: 80}},
		{Min: Point{X: 190, Y: 0}, Max: Point{X: 240, Y: 100}},
	}
	if got := fillStaircaseSteps(bars, tableTop, tableBot, 10, 240); got != nil {
		t.Errorf("bar chart accepted as staircase: got %v, want nil", got)
	}
}

// TestFillStaircaseStepsTableScoped verifies the shared-x test is scoped to the current
// table's [vMin,vMax]: a separate fill region elsewhere on the page (distinct x, same bottom,
// overlapping the table's vertical band) must NOT pollute the x-clusters and downgrade a real
// banded table. The off-table rects sit entirely right of vMax and must be ignored.
func TestFillStaircaseStepsTableScoped(t *testing.T) {
	const tableTop, tableBot = -200.0, 0.0
	rects := []Rect{
		// The table: nested bands sharing x-span [10,500].
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 40}},
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 60}},
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 80}},
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 500, Y: 100}},
		// A separate chart to the RIGHT of the table (x in [600..760]), same bottom.
		{Min: Point{X: 600, Y: 0}, Max: Point{X: 650, Y: 50}},
		{Min: Point{X: 700, Y: 0}, Max: Point{X: 760, Y: 90}},
	}
	// vMax = 500 excludes the off-table rects; the table's four bands must survive.
	if got := fillStaircaseSteps(rects, tableTop, tableBot, 10, 500); len(got) != 4 {
		t.Errorf("table-scoped staircase: got %d steps %v, want 4 (off-table rects must be ignored)", len(got), got)
	}
}

// TestInferFillBandedRowsDeclinesBarChart drives the full inferFillBandedRows entry point with
// a bar-chart c.Rect (distinct-x bars on a common baseline with stepped tops) and asserts it
// returns the input cells UNCHANGED — the end-to-end proof that a chart reaching the new pass
// is declined (the shared-x discriminator fails G1), not split into a fabricated grid.
func TestInferFillBandedRowsDeclinesBarChart(t *testing.T) {
	cells := []lCell{{x0: 0, top: -200, x1: 300, bottom: 0}}
	bars := []Rect{
		{Min: Point{X: 10, Y: 0}, Max: Point{X: 60, Y: 40}},
		{Min: Point{X: 70, Y: 0}, Max: Point{X: 120, Y: 60}},
		{Min: Point{X: 130, Y: 0}, Max: Point{X: 180, Y: 80}},
		{Min: Point{X: 190, Y: 0}, Max: Point{X: 240, Y: 100}},
		{Min: Point{X: 250, Y: 0}, Max: Point{X: 300, Y: 120}},
	}
	out := inferFillBandedRows(cells, nil, Content{Rect: bars}, nil)
	if len(out) != len(cells) || out[0] != cells[0] {
		t.Errorf("inferFillBandedRows split a bar chart: got %d cells %v, want input unchanged %v", len(out), out, cells)
	}
}

// TestInferFillBandedRowsDeclinesSparseFillGrid drives the BEA per-cell-grid branch
// (inferFillBandedRowsBEA, reached after the staircase signature fails) with a fill-rect grid
// that has only TWO multi-column rect-rows. Each row spans >=2 distinct x0 columns, so it would
// pass the distinctX0>=2 per-band discriminator — but two such rows is below rectMinRowClusters
// (3), so beaDataBodyBBox returns ok=false and the pass declines (input cells unchanged). This
// locks the lower bound of the subtractive phantom-clamp: a sparse fill grid is never clamped
// into a fabricated table, the analogue of TestInferFillBandedRowsDeclinesBarChart for BEA.
func TestInferFillBandedRowsDeclinesSparseFillGrid(t *testing.T) {
	cells := []lCell{{x0: 0, top: -100, x1: 300, bottom: 0}}
	// Two rect-rows (tops -30 and -60), each two cells at distinct x0 (10 and 160). Bottoms sit
	// far from tableBot (0), so the staircase common-bottom test fails and the BEA branch runs.
	rects := []Rect{
		{Min: Point{X: 10, Y: 20}, Max: Point{X: 140, Y: 30}},  // row 1, col a
		{Min: Point{X: 160, Y: 20}, Max: Point{X: 290, Y: 30}}, // row 1, col b
		{Min: Point{X: 10, Y: 50}, Max: Point{X: 140, Y: 60}},  // row 2, col a
		{Min: Point{X: 160, Y: 50}, Max: Point{X: 290, Y: 60}}, // row 2, col b
	}
	out := inferFillBandedRows(cells, nil, Content{Rect: rects}, nil)
	if len(out) != len(cells) || out[0] != cells[0] {
		t.Errorf("inferFillBandedRows clamped a 2-row sparse fill grid: got %d cells %v, want input unchanged %v", len(out), out, cells)
	}
}

// TestInferFillBandedRowsNoOpOnAllMultiColumnGrid locks the body==extent no-op guard in the BEA
// branch. A stroke-free per-cell grid whose rows are ALL multi-column has a data-body bbox equal
// to the table extent — no single-column title/footnote banner lies outside it — so the
// subtractive phantom-clamp has nothing to remove and must return the cells untouched. Without
// the guard, dropTrailingEmptyBands would trim a sparse real row, the regression first caught on
// EPA p1's stroke-free 7×3 gutter frame (which collapsed to 6×2). beaDataBodyBBox returns ok here
// (3 multi-column rect-rows >= rectMinRowClusters), so this exercises the clamp-removed-nothing
// path specifically, not an earlier decline.
func TestInferFillBandedRowsNoOpOnAllMultiColumnGrid(t *testing.T) {
	cells := []lCell{
		{x0: 10, top: -30, x1: 140, bottom: -20}, {x0: 160, top: -30, x1: 290, bottom: -20},
		{x0: 10, top: -60, x1: 140, bottom: -50}, {x0: 160, top: -60, x1: 290, bottom: -50},
		{x0: 10, top: -90, x1: 140, bottom: -80}, {x0: 160, top: -90, x1: 290, bottom: -80},
	}
	// One rect per cell: three rect-rows (tops -30/-60/-90), each two distinct x0 columns (10,160).
	// The body bbox spans all three rows ⇒ body == table extent ⇒ clamp removes nothing.
	rects := []Rect{
		{Min: Point{X: 10, Y: 20}, Max: Point{X: 140, Y: 30}}, {Min: Point{X: 160, Y: 20}, Max: Point{X: 290, Y: 30}},
		{Min: Point{X: 10, Y: 50}, Max: Point{X: 140, Y: 60}}, {Min: Point{X: 160, Y: 50}, Max: Point{X: 290, Y: 60}},
		{Min: Point{X: 10, Y: 80}, Max: Point{X: 140, Y: 90}}, {Min: Point{X: 160, Y: 80}, Max: Point{X: 290, Y: 90}},
	}
	out := inferFillBandedRows(append([]lCell(nil), cells...), nil, Content{Rect: rects}, nil)
	if len(out) != len(cells) {
		t.Fatalf("inferFillBandedRows changed cell count on an all-multi-column grid: got %d, want %d", len(out), len(cells))
	}
	for i := range cells {
		if out[i] != cells[i] {
			t.Errorf("cell %d mutated by BEA branch on a no-phantom grid: got %v, want %v", i, out[i], cells[i])
		}
	}
}

// TestDropTrailingEmptyBandsKeepsMultiColumnRow locks the phantom-vs-real discriminator the
// codex review flagged: dropTrailingEmptyBands must NOT delete a trailing band just because it
// holds no words. Word-emptiness alone is not a phantom signal — a real blank/sparse data row
// spans >=2 columns, so only a SINGLE-column banner (title/footnote) may be trimmed. Here all
// four bands are multi-column and word-free; the trailing real row must be preserved (the prior
// word-only rule would have dropped it down to rectMinRowClusters).
func TestDropTrailingEmptyBandsKeepsMultiColumnRow(t *testing.T) {
	cells := []lCell{
		{x0: 10, top: -10, x1: 90, bottom: -2}, {x0: 110, top: -10, x1: 190, bottom: -2},
		{x0: 10, top: -30, x1: 90, bottom: -22}, {x0: 110, top: -30, x1: 190, bottom: -22},
		{x0: 10, top: -50, x1: 90, bottom: -42}, {x0: 110, top: -50, x1: 190, bottom: -42},
		{x0: 10, top: -70, x1: 90, bottom: -62}, {x0: 110, top: -70, x1: 190, bottom: -62}, // blank multi-col row
	}
	out := dropTrailingEmptyBands(cells, nil) // no words anywhere → every band is word-empty
	if len(out) != len(cells) {
		t.Errorf("dropped a blank MULTI-column trailing row: got %d cells, want %d (a multi-column row is never a banner phantom)", len(out), len(cells))
	}
}

// TestDropTrailingEmptyBandsDropsSingleColumnBanner is the positive half: a word-empty
// SINGLE-column full-width banner (the BEA footnote-phantom shape) below three multi-column data
// bands IS trimmed, so the phantom-clamp still reaches the 36-row BEA target.
func TestDropTrailingEmptyBandsDropsSingleColumnBanner(t *testing.T) {
	// Top-origin coords: a band's top = -y, so the TRAILING (bottom-most) band is the one with
	// the LEAST-negative top. The single-column banner therefore sits at top=-10 (below the three
	// multi-column data rows at -30/-50/-70), matching the BEA footnote-phantom position.
	cells := []lCell{
		{x0: 10, top: -70, x1: 90, bottom: -62}, {x0: 110, top: -70, x1: 190, bottom: -62},
		{x0: 10, top: -50, x1: 90, bottom: -42}, {x0: 110, top: -50, x1: 190, bottom: -42},
		{x0: 10, top: -30, x1: 90, bottom: -22}, {x0: 110, top: -30, x1: 190, bottom: -22},
		{x0: 10, top: -10, x1: 190, bottom: -2}, // single full-width trailing banner: distinctCols == 1
	}
	out := dropTrailingEmptyBands(cells, nil)
	if len(out) != 6 {
		t.Errorf("kept a word-empty single-column banner: got %d cells, want 6 (the banner row must be trimmed)", len(out))
	}
}

// TestMergePhantomHeaderBandTwoBandsPhantomAndReal locks the per-band fix for multi-row
// seam headers. The header has TWO stacked row-bands:
//   - Band A (top=-50 to -40): two cells split at x=150, which is absent from the data
//     x-set {0,100,200,300} — a phantom seam. Band A must be merged into one full-width cell.
//   - Band B (top=-40 to -30): three cells whose boundaries {0,100,200,300} ARE all in the
//     data x-set — a genuine grouped header. Band B must be left intact.
//
// FP-safety invariant: the per-band phantom check is the sole protection for band B — a
// data-corroborated band must never be merged regardless of what happens to a sibling band.
func TestMergePhantomHeaderBandTwoBandsPhantomAndReal(t *testing.T) {
	// Coordinate convention: top-origin negated. top=-50 is higher on the page than top=-40.
	// firstDataRowTop=-20; header cells have bottom <= -20+3 = -17.
	// Band A (phantom): x boundary at 150, absent from data columns.
	// Band B (real):    x boundaries at 0,100,200,300, all present in data.
	// Data:             three columns at x=[0,100,200,300], two rows.
	firstDataRowTop := -20.0

	bandA := []lCell{
		{x0: 0, top: -50, x1: 150, bottom: -40},
		{x0: 150, top: -50, x1: 300, bottom: -40},
	}
	bandB := []lCell{
		{x0: 0, top: -40, x1: 100, bottom: -30},
		{x0: 100, top: -40, x1: 200, bottom: -30},
		{x0: 200, top: -40, x1: 300, bottom: -30},
	}
	dataRows := []lCell{
		{x0: 0, top: -20, x1: 100, bottom: -10},
		{x0: 100, top: -20, x1: 200, bottom: -10},
		{x0: 200, top: -20, x1: 300, bottom: -10},
		{x0: 0, top: -10, x1: 100, bottom: 0},
		{x0: 100, top: -10, x1: 200, bottom: 0},
		{x0: 200, top: -10, x1: 300, bottom: 0},
	}

	var cells []lCell
	cells = append(cells, bandA...)
	cells = append(cells, bandB...)
	cells = append(cells, dataRows...)

	out := mergePhantomHeaderBand(cells, firstDataRowTop)

	// Band A: 2 cells → 1 merged (phantom removed). Band B: 3 cells kept. Data: 6 cells.
	wantLen := 1 + len(bandB) + len(dataRows) // 10
	if len(out) != wantLen {
		t.Fatalf("want %d cells (band A merged + band B kept + data), got %d: %v", wantLen, len(out), out)
	}
	// First cell is the merged band A spanning full width.
	if out[0].x0 != 0 || out[0].x1 != 300 || out[0].top != -50 || out[0].bottom != -40 {
		t.Errorf("merged band A = %+v, want {x0:0 top:-50 x1:300 bottom:-40}", out[0])
	}
	// Band B cells (indices 1-3) must be unchanged.
	for i, want := range bandB {
		if out[i+1] != want {
			t.Errorf("band B cell %d = %+v, want %+v", i, out[i+1], want)
		}
	}
}

// TestMergePhantomHeaderBandTwoBandsBothPhantom locks the case where BOTH stacked header
// bands carry phantom seams — the geometry seen in real stat-pocketbook tables where each
// header row is painted with two wide fill rects (left/right of the phantom seam). Both
// bands must be merged independently into full-width cells; neither merge must suppress
// the other; data cells must be unchanged.
func TestMergePhantomHeaderBandTwoBandsBothPhantom(t *testing.T) {
	// x=150 is the phantom boundary, absent from data columns {0,100,200,300}.
	firstDataRowTop := -20.0
	cells := []lCell{
		// Band A (top=-50 to -40): phantom split at x=150.
		{x0: 0, top: -50, x1: 150, bottom: -40},
		{x0: 150, top: -50, x1: 300, bottom: -40},
		// Band B (top=-40 to -30): same phantom split.
		{x0: 0, top: -40, x1: 150, bottom: -30},
		{x0: 150, top: -40, x1: 300, bottom: -30},
		// Data: three columns, two rows.
		{x0: 0, top: -20, x1: 100, bottom: -10},
		{x0: 100, top: -20, x1: 200, bottom: -10},
		{x0: 200, top: -20, x1: 300, bottom: -10},
		{x0: 0, top: -10, x1: 100, bottom: 0},
		{x0: 100, top: -10, x1: 200, bottom: 0},
		{x0: 200, top: -10, x1: 300, bottom: 0},
	}

	out := mergePhantomHeaderBand(cells, firstDataRowTop)

	// 2 merged header bands (1 cell each) + 6 data cells = 8.
	if len(out) != 8 {
		t.Fatalf("want 8 cells (2 merged bands + 6 data), got %d: %v", len(out), out)
	}
	// Band A merged to full width.
	if out[0].x0 != 0 || out[0].x1 != 300 || out[0].top != -50 || out[0].bottom != -40 {
		t.Errorf("merged band A = %+v, want {x0:0 top:-50 x1:300 bottom:-40}", out[0])
	}
	// Band B merged to full width.
	if out[1].x0 != 0 || out[1].x1 != 300 || out[1].top != -40 || out[1].bottom != -30 {
		t.Errorf("merged band B = %+v, want {x0:0 top:-40 x1:300 bottom:-30}", out[1])
	}
}

// TestMergePhantomHeaderBandDataCorroboratedNotMerged locks the FP-safety invariant:
// a two-band header whose every cell boundary is present in the data x-set is returned
// unchanged. This is the "genuine grouped header" case — e.g. "2020 | 2021" spanning
// real data-column positions — where no phantom seam exists and no merge should occur.
func TestMergePhantomHeaderBandDataCorroboratedNotMerged(t *testing.T) {
	// All header cell boundaries lie at data positions {0,100,200,300}: data-corroborated.
	firstDataRowTop := -20.0
	cells := []lCell{
		// Band A: boundaries at real data x-positions.
		{x0: 0, top: -50, x1: 100, bottom: -40},
		{x0: 100, top: -50, x1: 200, bottom: -40},
		{x0: 200, top: -50, x1: 300, bottom: -40},
		// Band B: same real positions.
		{x0: 0, top: -40, x1: 100, bottom: -30},
		{x0: 100, top: -40, x1: 200, bottom: -30},
		{x0: 200, top: -40, x1: 300, bottom: -30},
		// Data: three columns, one row.
		{x0: 0, top: -20, x1: 100, bottom: -10},
		{x0: 100, top: -20, x1: 200, bottom: -10},
		{x0: 200, top: -20, x1: 300, bottom: -10},
	}

	out := mergePhantomHeaderBand(cells, firstDataRowTop)
	if len(out) != len(cells) {
		t.Fatalf("data-corroborated header: want %d cells unchanged, got %d", len(cells), len(out))
	}
	for i := range cells {
		if out[i] != cells[i] {
			t.Errorf("cell %d changed: got %+v, want %+v", i, out[i], cells[i])
		}
	}
}

// cellSetContains reports whether out holds a cell equal to want (order-independent).
func cellSetContains(out []lCell, want lCell) bool {
	for _, c := range out {
		if c == want {
			return true
		}
	}
	return false
}

// TestMergePhantomHeaderBandOuterEdgeOverhangNotMerged locks Fix 1 (interior-only seam test):
// a genuine grouped header whose INTERIOR boundaries are all data-corroborated, but whose outer
// left/right fill-rect edge overhangs the data body (-1 / 301 vs data 0..300), must NOT be
// merged. The outer edges are excluded from the phantom test, and the overhang values fall
// outside the strict (dataMin,dataMax) range, so the band is preserved verbatim. Without the
// interior-only fix the absent outer edge (-1 or 301) would falsely flag the band as phantom
// and flatten the grouped header.
func TestMergePhantomHeaderBandOuterEdgeOverhangNotMerged(t *testing.T) {
	firstDataRowTop := -20.0
	cells := []lCell{
		// Single-band grouped header; interior seams at 100,200 (data-corroborated), but the
		// outer edges overhang: left edge -1, right edge 301.
		{x0: -1, top: -40, x1: 100, bottom: -30},
		{x0: 100, top: -40, x1: 200, bottom: -30},
		{x0: 200, top: -40, x1: 301, bottom: -30},
		// Data: three columns spanning 0..300.
		{x0: 0, top: -20, x1: 100, bottom: -10},
		{x0: 100, top: -20, x1: 200, bottom: -10},
		{x0: 200, top: -20, x1: 300, bottom: -10},
	}

	out := mergePhantomHeaderBand(cells, firstDataRowTop)
	if len(out) != len(cells) {
		t.Fatalf("outer-edge overhang: want %d cells unchanged, got %d: %v", len(cells), len(out), out)
	}
	for i := range cells {
		if out[i] != cells[i] {
			t.Errorf("cell %d changed: got %+v, want %+v", i, out[i], cells[i])
		}
	}
}

// TestMergePhantomHeaderBandChainedTopsDifferentBottomsNotFlattened locks Fix 2 (cluster by
// SHARED ROW EXTENT, top AND bottom). Two header rows whose tops fall within rectRowSnapTol
// but whose bottoms differ by more than it must NOT be clustered into one band and flattened.
//   - Row 1 (top=-50, bottom=-44): a genuine 3-cell grouped header, all seams data-corroborated.
//   - Row 2 (top=-48, bottom=-30): a phantom-seam row (split at x=150, absent from data).
//
// Under the prior top-only clustering both rows chain into ONE band; the combined band's
// interior set then includes the phantom 150, flattening ALL five header cells into one and
// destroying row 1's grouped structure. With extent clustering they form two bands: row 1
// (corroborated) is kept as its three cells, row 2 (phantom) is merged to one cell.
func TestMergePhantomHeaderBandChainedTopsDifferentBottomsNotFlattened(t *testing.T) {
	firstDataRowTop := -20.0
	row1 := []lCell{
		{x0: 0, top: -50, x1: 100, bottom: -44},
		{x0: 100, top: -50, x1: 200, bottom: -44},
		{x0: 200, top: -50, x1: 300, bottom: -44},
	}
	row2 := []lCell{ // tops within 2pt of row1 but bottoms 14pt apart; phantom seam at 150
		{x0: 0, top: -48, x1: 150, bottom: -30},
		{x0: 150, top: -48, x1: 300, bottom: -30},
	}
	data := []lCell{
		{x0: 0, top: -20, x1: 100, bottom: -10},
		{x0: 100, top: -20, x1: 200, bottom: -10},
		{x0: 200, top: -20, x1: 300, bottom: -10},
	}
	var cells []lCell
	cells = append(cells, row1...)
	cells = append(cells, row2...)
	cells = append(cells, data...)

	out := mergePhantomHeaderBand(cells, firstDataRowTop)

	// Header part = cells with bottom <= firstDataRowTop+rectRowSnapTol (= -17).
	var header []lCell
	for _, c := range out {
		if c.bottom <= firstDataRowTop+3.0 {
			header = append(header, c)
		}
	}
	// Row 1's three cells must survive intact (NOT flattened), and row 2 merges to one cell:
	// 3 + 1 = 4 header cells.
	if len(header) != 4 {
		t.Fatalf("want 4 header cells (row1 kept as 3 + row2 merged to 1), got %d: %v", len(header), header)
	}
	for i, want := range row1 {
		if !cellSetContains(header, want) {
			t.Errorf("row1 cell %d %+v missing — grouped header row was flattened", i, want)
		}
	}
	// Row 2 merged to one full-width cell spanning only its own row extent.
	if !cellSetContains(header, lCell{x0: 0, top: -48, x1: 300, bottom: -30}) {
		t.Errorf("row2 phantom band not merged to its own full-width cell: %v", header)
	}
	// No header cell may span BOTH row extents (the flattened-into-one-band failure).
	for _, c := range header {
		if c.top <= -49 && c.bottom >= -31 {
			t.Errorf("a header cell spans both rows (flattened across differing bottoms): %+v", c)
		}
	}
}

// TestMergePhantomHeaderBandSubPointJitterCorroborated locks the corroboration fix: the phantom
// test uses xCorroborated (within rectRowSnapTol of any data x), NOT a quantized-exact map
// lookup. A header interior seam jittered off a real data boundary by MORE than q's 0.01pt
// resolution but WITHIN rectRowSnapTol (here 100.5 vs data column boundary 100.0) is corroborated
// and the band must NOT be merged — it is a genuine sub-column edge with sub-point geometry jitter,
// not a phantom seam. The companion below proves a TRUE phantom (well beyond tol) is still merged.
func TestMergePhantomHeaderBandSubPointJitterCorroborated(t *testing.T) {
	firstDataRowTop := -20.0
	cells := []lCell{
		// Single-band grouped header; interior seam at 100.5, jittered 0.5pt off data x=100.0.
		{x0: 0, top: -40, x1: 100.5, bottom: -30},
		{x0: 100.5, top: -40, x1: 300, bottom: -30},
		// Data: two columns split at exactly 100.0.
		{x0: 0, top: -20, x1: 100, bottom: -10},
		{x0: 100, top: -20, x1: 300, bottom: -10},
	}

	out := mergePhantomHeaderBand(cells, firstDataRowTop)
	if len(out) != len(cells) {
		t.Fatalf("sub-point-jitter seam (100.5 vs data 100.0, within tol): want %d cells unchanged, got %d: %v",
			len(cells), len(out), out)
	}
	for i := range cells {
		if out[i] != cells[i] {
			t.Errorf("cell %d changed: got %+v, want %+v", i, out[i], cells[i])
		}
	}
}

// TestMergePhantomHeaderBandTruePhantomBeyondTolMerged is the companion to the jitter test: an
// interior seam MORE than rectRowSnapTol from any data boundary (x=150, with data columns split
// only at 100 and 200) is uncorroborated and strictly within the data x-range → a true phantom,
// still merged into one full-width cell. This proves the corroboration change widened the keep
// zone by exactly the tolerance, not unboundedly.
func TestMergePhantomHeaderBandTruePhantomBeyondTolMerged(t *testing.T) {
	firstDataRowTop := -20.0
	cells := []lCell{
		// Single-band header; interior seam at 150, 50pt from the nearest data boundary (100/200).
		{x0: 0, top: -40, x1: 150, bottom: -30},
		{x0: 150, top: -40, x1: 300, bottom: -30},
		// Data: three columns split at 100 and 200 (no boundary near 150).
		{x0: 0, top: -20, x1: 100, bottom: -10},
		{x0: 100, top: -20, x1: 200, bottom: -10},
		{x0: 200, top: -20, x1: 300, bottom: -10},
	}

	out := mergePhantomHeaderBand(cells, firstDataRowTop)
	// 1 merged header cell + 3 data cells = 4.
	if len(out) != 4 {
		t.Fatalf("true phantom seam (150, beyond tol): want 4 cells (header merged), got %d: %v", len(out), out)
	}
	if out[0].x0 != 0 || out[0].x1 != 300 || out[0].top != -40 || out[0].bottom != -30 {
		t.Errorf("merged header = %+v, want {x0:0 top:-40 x1:300 bottom:-30}", out[0])
	}
}
