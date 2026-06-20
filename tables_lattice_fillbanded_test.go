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
