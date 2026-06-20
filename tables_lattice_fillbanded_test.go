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
