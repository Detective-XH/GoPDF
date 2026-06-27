// tables_gutter_test.go — tests for dropGutterColumns (the thin all-empty column drop).
//
// TestDropGutterColumns is the PRIMARY lock: synthetic unit test for the FP-safety
// invariant (wide+empty is kept; thin+empty is dropped; thin+non-empty is kept; etc.).
// TestPublicTablesGutterColumnsDropped is the real-fixture consumer lock: EPA eGRID p1
// cover frames are double-wall border rects → thin gutter columns; the test pins the
// exact post-fix table[0] dims.
package pdf

import (
	"os"
	"reflect"
	"slices"
	"testing"
)

// TestDropGutterColumns exercises the FP-safety invariant of dropGutterColumns.
// The relative gate (gutterFraction × median data-column width) must:
//   - drop thin+empty columns (the gutter case)
//   - KEEP wide+empty columns (a legitimately empty data column — the FP guard)
//   - keep thin+non-empty columns (content wins over width)
//   - return the grid unchanged when no column qualifies
//   - return the grid unchanged when the drop would empty the grid
func TestDropGutterColumns(t *testing.T) {
	t.Run("thin_empty_dropped", func(t *testing.T) {
		// Col0: wide (100 pt), has data.
		// Col1: thin (5 pt), all-empty — should be DROPPED.
		// Col2: wide (120 pt), all-empty — should be KEPT (FP guard).
		cells := []lCell{
			{x0: 100, x1: 200, top: 0, bottom: 10}, // col0 wide, data
			{x0: 205, x1: 210, top: 0, bottom: 10}, // col1 thin gutter (5 pt)
			{x0: 300, x1: 420, top: 0, bottom: 10}, // col2 wide (120 pt)
		}
		colReps := []float64{100, 205, 300}
		grid := [][]string{
			{"a", "", ""},
			{"b", "", ""},
		}
		got := dropGutterColumns(grid, cells, colReps)
		// col0 kept (data), col1 dropped (thin+empty), col2 kept (wide+empty).
		want := [][]string{{"a", ""}, {"b", ""}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("thin_empty_dropped: got %v, want %v", got, want)
		}
	})

	t.Run("wide_empty_kept", func(t *testing.T) {
		// Col0: wide (100 pt), has data.
		// Col1: wide (90 pt), all-empty — should be KEPT (normal empty data col).
		cells := []lCell{
			{x0: 0, x1: 100, top: 0, bottom: 10},
			{x0: 110, x1: 200, top: 0, bottom: 10},
		}
		colReps := []float64{0, 110}
		grid := [][]string{
			{"hello", ""},
			{"world", ""},
		}
		got := dropGutterColumns(grid, cells, colReps)
		// Both columns kept (col1 is wide relative to col0 median).
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("wide_empty_kept: got %v, want %v (unchanged)", got, grid)
		}
	})

	t.Run("thin_nonempty_kept", func(t *testing.T) {
		// Col0: wide (200 pt), has data.
		// Col1: thin (6 pt), has data — must NOT be dropped (content present).
		cells := []lCell{
			{x0: 0, x1: 200, top: 0, bottom: 10},
			{x0: 205, x1: 211, top: 0, bottom: 10}, // 6 pt, has text
		}
		colReps := []float64{0, 205}
		grid := [][]string{
			{"alpha", "x"},
		}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("thin_nonempty_kept: got %v, want %v (unchanged)", got, grid)
		}
	})

	t.Run("no_qualifying_column", func(t *testing.T) {
		// All columns are wide and have data — nothing to drop.
		cells := []lCell{
			{x0: 0, x1: 100, top: 0, bottom: 10},
			{x0: 110, x1: 210, top: 0, bottom: 10},
		}
		colReps := []float64{0, 110}
		grid := [][]string{
			{"foo", "bar"},
			{"baz", "qux"},
		}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("no_qualifying: got %v, want unchanged", got)
		}
	})

	t.Run("would_empty_grid", func(t *testing.T) {
		// Only column is thin and empty — dropping it would leave an empty grid.
		// Guard: nKeep == 0 → return unchanged.
		cells := []lCell{
			{x0: 0, x1: 5, top: 0, bottom: 10}, // 5 pt thin
		}
		colReps := []float64{0}
		grid := [][]string{
			{""},
			{""},
		}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("would_empty_grid: got %v, want unchanged", got)
		}
	})

	t.Run("empty_grid_input", func(t *testing.T) {
		// Edge: empty grid — must return immediately.
		got := dropGutterColumns([][]string{}, []lCell{}, []float64{})
		if len(got) != 0 {
			t.Errorf("empty_grid_input: got %v, want []", got)
		}
	})

	t.Run("all_data_cols_empty_no_median", func(t *testing.T) {
		// All columns are all-empty — no data columns to derive a median from.
		// Guard: len(dataWidths) == 0 → return unchanged.
		cells := []lCell{
			{x0: 0, x1: 5, top: 0, bottom: 10},   // 5 pt
			{x0: 10, x1: 15, top: 0, bottom: 10}, // 5 pt
		}
		colReps := []float64{0, 10}
		grid := [][]string{{"", ""}}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("all_empty_no_median: got %v, want unchanged", got)
		}
	})

	t.Run("multiple_thin_gutters_dropped", func(t *testing.T) {
		// Three thin empty gutters mixed among data columns; all three must be dropped.
		// Mirrors the EPA p1 situation (8 thin empty cols around 3 wide data cols).
		// Data cols: 200, 220, 500 pt → median 220, threshold = 0.25 × 220 = 55 pt.
		// Gutter widths: 5, 7, 11 pt — all below 55 pt threshold.
		cells := []lCell{
			// wide data
			{x0: 0, x1: 200, top: 0, bottom: 10},
			// thin gutter
			{x0: 205, x1: 210, top: 0, bottom: 10}, // 5 pt
			// wide data
			{x0: 220, x1: 440, top: 0, bottom: 10}, // 220 pt
			// thin gutter
			{x0: 445, x1: 452, top: 0, bottom: 10}, // 7 pt
			// wide data
			{x0: 460, x1: 960, top: 0, bottom: 10}, // 500 pt
			// thin gutter
			{x0: 965, x1: 976, top: 0, bottom: 10}, // 11 pt
		}
		colReps := []float64{0, 205, 220, 445, 460, 965}
		grid := [][]string{
			{"A", "", "B", "", "C", ""},
		}
		got := dropGutterColumns(grid, cells, colReps)
		want := [][]string{{"A", "B", "C"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("multiple_gutters: got %v, want %v", got, want)
		}
	})

	t.Run("real_narrow_empty_kept", func(t *testing.T) {
		// Codex adversarial-review scenario: a LEGITIMATE all-empty narrow data column
		// (20 pt — e.g. a flag/status/reserved column blank on this page) among wide data
		// columns must NOT be dropped. Relative-gate alone would drop it (20 < 0.25×200=50);
		// the absolute cap (absoluteGutterCap=16) preserves it (20 > 16).
		cells := []lCell{
			{x0: 0, x1: 200, top: 0, bottom: 10},   // wide data (200 pt)
			{x0: 205, x1: 225, top: 0, bottom: 10}, // 20 pt — real narrow EMPTY column
			{x0: 230, x1: 430, top: 0, bottom: 10}, // wide data (200 pt)
		}
		colReps := []float64{0, 205, 230}
		grid := [][]string{{"A", "", "B"}}
		got := dropGutterColumns(grid, cells, colReps)
		// median data-col = 200, relative threshold = 50; 20 < 50 but 20 > 16 cap → KEPT.
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("real_narrow_empty_kept: got %v, want %v (unchanged — 20pt > absoluteGutterCap)", got, grid)
		}
	})
}

// TestPublicTablesGutterColumnsDropped verifies that p.Tables() on EPA eGRID p1
// no longer emits the thin gutter columns produced by double-wall decorative border
// rects on the cover page.
//
// Measured post-fix (2026-06-18):
//   - BEFORE: 3 tables; table[0] was 4×11 (8 all-empty cols from double-wall gutters)
//   - AFTER:  3 tables; table[0] is 4×3 (8 thin gutter cols dropped, 3 data cols kept)
//
// The test pins the exact post-fix dims of table[0] as a determinism floor.
// It uses the table index (not the largest-by-cells selector) because the largest
// table after the fix (7×2 ToC box) is different from the one that carried the gutters.
func TestPublicTablesGutterColumnsDropped(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/epa-egrid2022-t1.pdf")
	if err != nil {
		t.Fatalf("open EPA: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat EPA: %v", err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader EPA: %v", err)
	}

	p := r.Page(1)
	tables, err := p.Tables()
	if err != nil {
		t.Fatalf("p.Tables() p1: %v", err)
	}

	// Order-independent assertion (Page.Tables enumeration order is not a public contract,
	// only cell content is — assert the MULTISET of post-fix dims, not tables[i]).
	//
	// EPA p1 has 3 framed cover boxes; post-fix dim multiset is {2×2, 4×3, 7×3}:
	//   - The "Feedback / Survey / Contact / Created" frame is a double-wall border rect:
	//     pre-fix 4×11 (8 all-empty gutter cols, widths 4.99–13.02 pt); post-fix 4×3 — all 8
	//     gutters are < absoluteGutterCap (16) AND < the relative threshold (median 231 ⇒
	//     0.25×231 ≈ 57.8), so all drop. The 3 kept columns all carry data.
	//   - A second frame is pre/post 2×2 (two wide data columns; nothing to drop).
	//   - The third frame is pre-fix 7×5; post-fix 7×3 — it drops two THIN gutters (7.22 /
	//     5.11 pt) but KEEPS a 40.06 pt all-empty column: 40 > absoluteGutterCap, so the cap
	//     preserves it even though the relative gate alone (< 0.25×median=118) would drop it.
	//     This is the dual-gate FP guard (Codex's flagged class) demonstrated on real data.
	if len(tables) != 3 {
		t.Fatalf("EPA p1: got %d tables, want 3", len(tables))
	}
	type dim struct{ rows, cols int }
	dims := make([]dim, len(tables))
	for i, tbl := range tables {
		rows := len(tbl.Cells)
		cols := 0
		if rows > 0 {
			cols = len(tbl.Cells[0])
		}
		dims[i] = dim{rows, cols}
		t.Logf("EPA p1 table[%d]: %dx%d", i, rows, cols)
	}
	slices.SortFunc(dims, func(a, b dim) int {
		if a.rows != b.rows {
			return a.rows - b.rows
		}
		return a.cols - b.cols
	})
	want := []dim{{2, 2}, {4, 3}, {7, 3}}
	if !reflect.DeepEqual(dims, want) {
		t.Errorf("EPA p1 post-fix dim multiset = %v, want %v (4×11 gutter frame → 4×3; 7×5 frame → 7×3 keeping its wide empty col)", dims, want)
	}

	// The collapsed gutter frame (4×3) must carry no residual all-empty column (all 8 thin
	// gutters removed). The 7×3 frame, by contrast, MUST retain its one wide all-empty column
	// (the absolute-cap FP guard): a positive lock that the fix preserves legitimate empties.
	for _, tbl := range tables {
		empties := countAllEmptyCols(tbl.Cells)
		switch {
		case len(tbl.Cells) == 4 && len(tbl.Cells[0]) == 3:
			if empties != 0 {
				t.Errorf("EPA p1 de-guttered 4×3 frame: %d residual all-empty cols, want 0", empties)
			}
		case len(tbl.Cells) == 7 && len(tbl.Cells[0]) == 3:
			if empties != 1 {
				t.Errorf("EPA p1 7×3 frame: %d all-empty cols, want 1 (the 40pt empty col preserved by the cap)", empties)
			}
		}
	}
}

// TestDropGutterSpanPhantom locks the structural span-containment drop predicate added to
// dropGutterColumns (condition 2): an empty column whose drawn x-span (colReps[cc],
// leafX1[cc]) strictly contains a non-empty column's representative x is dropped as a
// mis-split spanning-cell phantom, regardless of how wide the empty column is.
// A column whose span contains NO non-empty colRep, and whose width passes the cap, is kept.
func TestDropGutterSpanPhantom(t *testing.T) {
	t.Run("span_contains_real_col_dropped", func(t *testing.T) {
		// Col 0: data (50 pt), text "A".
		// Col 1: wide (140 pt), all-empty. colW=140 exceeds absoluteGutterCap (16) and the
		//        relative threshold (0.25×median≈0.25×60=15), so condition 1 would NOT fire.
		//        But colReps[2]=150 falls strictly inside (colReps[1]=60, leafX1[1]=200) →
		//        condition 2 fires → DROP.
		// Col 2: data (70 pt), text "B".
		cells := []lCell{
			{x0: 0, x1: 50, top: 0, bottom: 10},    // col 0, 50 pt, data
			{x0: 60, x1: 200, top: 0, bottom: 10},  // col 1, 140 pt, spanning phantom
			{x0: 150, x1: 220, top: 0, bottom: 10}, // col 2, 70 pt, data
		}
		colReps := []float64{0, 60, 150}
		grid := [][]string{{"A", "", "B"}}
		got := dropGutterColumns(grid, cells, colReps)
		want := [][]string{{"A", "B"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("span_contains_real_col_dropped: got %v, want %v", got, want)
		}
	})

	t.Run("span_no_real_col_kept", func(t *testing.T) {
		// Col 0: data (50 pt), text "A".
		// Col 1: wide (140 pt), all-empty. Exceeds both gates (condition 1 false).
		//        No non-empty column has colRep strictly inside (60, 200) → condition 2
		//        false → KEPT (legitimate empty column enclosing no sub-column).
		cells := []lCell{
			{x0: 0, x1: 50, top: 0, bottom: 10},   // col 0, 50 pt, data
			{x0: 60, x1: 200, top: 0, bottom: 10}, // col 1, 140 pt, empty
		}
		colReps := []float64{0, 60}
		grid := [][]string{{"A", ""}}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("span_no_real_col_kept: got %v, want %v (unchanged)", got, grid)
		}
	})

	t.Run("boundary_colrep_not_strict_kept", func(t *testing.T) {
		// Edge: non-empty column's colRep exactly equals leafX1 of the empty column.
		// The interval is OPEN (strict <), so the boundary must NOT qualify → KEPT.
		// Col 0: data (50 pt), text "A".
		// Col 1: wide (140 pt), empty. leafX1[1]=200.
		// Col 2: data. colReps[2]=200 == leafX1[1] (at the right boundary, not strictly inside).
		cells := []lCell{
			{x0: 0, x1: 50, top: 0, bottom: 10},    // col 0
			{x0: 60, x1: 200, top: 0, bottom: 10},  // col 1, leafX1=200
			{x0: 200, x1: 280, top: 0, bottom: 10}, // col 2, colRep=200 == leafX1[1]
		}
		colReps := []float64{0, 60, 200}
		grid := [][]string{{"A", "", "B"}}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("boundary_colrep_not_strict_kept: got %v, want %v (unchanged — open interval)", got, grid)
		}
	})

	t.Run("edge_sharing_ulp_drift_kept", func(t *testing.T) {
		// Regression for the EPA eGRID p1 7-row frame (TestPublicTablesGutterColumnsDropped):
		// a non-empty column shares the empty column's RIGHT v-rule, but leafX1 (raw min x1)
		// and the neighbour's colRep (a running cluster-mean of x0 for the same rule) differ
		// by a few ULP — there the observed drift was 7.1e-15 at magnitude 59. A bare strict <
		// fired on that noise and wrongly dropped the legitimate empty column. The colClusterTol
		// margin (4.0 pt ≫ the 5e-14 drift here) keeps the neighbour outside the interval → KEPT.
		// Col 0: data, text "A".
		// Col 1: wide empty. leafX1[1]=59.14 (exceeds both gates → condition 1 false).
		// Col 2: data. colReps[2]=59.14+5e-14 — physically the same right edge, +5e-14 of
		//        simulated cluster-mean drift (far under colClusterTol).
		const edge = 59.14
		cells := []lCell{
			{x0: 0, x1: 19.08, top: 0, bottom: 10},          // col 0, 19.08 pt, data
			{x0: 19.08, x1: edge, top: 0, bottom: 10},       // col 1, 40.06 pt, empty (matches EPA 40.06pt col)
			{x0: edge + 5e-14, x1: 120, top: 0, bottom: 10}, // col 2, data, colRep ~= leafX1[1] (drift)
		}
		colReps := []float64{0, 19.08, edge + 5e-14}
		grid := [][]string{{"A", "", "B"}}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("edge_sharing_ulp_drift_kept: got %v, want %v (unchanged — colClusterTol absorbs ULP drift)", got, grid)
		}
	})

	t.Run("near_boundary_within_cluster_tol_kept", func(t *testing.T) {
		// Regression lock for the cluster-jitter FP class: a legitimate edge-sharing column whose
		// single-linkage cluster-mean colRep drifted INWARD by ~2 pt (less than colClusterTol=4)
		// must NOT be mistaken for a distinct interior sub-column.
		// Col 0: data (50 pt), text "A".
		// Col 1: wide (140 pt) all-empty. colReps[1]=60, leafX1[1]=200. Exceeds both gates →
		//        condition 1 false.
		// Col 2: data. colReps[2]=198 → margin to the RIGHT boundary = 200-198 = 2 < colClusterTol
		//        → NOT strictly inside (lo,hi)=(64,196) → condition 2 false → KEPT.
		cells := []lCell{
			{x0: 0, x1: 50, top: 0, bottom: 10},    // col 0, 50 pt, data
			{x0: 60, x1: 200, top: 0, bottom: 10},  // col 1, 140 pt, empty, leafX1=200
			{x0: 198, x1: 260, top: 0, bottom: 10}, // col 2, data, colRep=198 (2 pt inside, < tol)
		}
		colReps := []float64{0, 60, 198}
		grid := [][]string{{"A", "", "B"}}
		got := dropGutterColumns(grid, cells, colReps)
		if !reflect.DeepEqual(got, grid) {
			t.Errorf("near_boundary_within_cluster_tol_kept: got %v, want %v (unchanged — within colClusterTol)", got, grid)
		}
	})
}

// countAllEmptyCols returns how many column indices are empty across every row of grid.
func countAllEmptyCols(grid [][]string) int {
	if len(grid) == 0 {
		return 0
	}
	n := 0
	for ci := range grid[0] {
		allEmpty := true
		for ri := range grid {
			if ci < len(grid[ri]) && grid[ri][ci] != "" {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			n++
		}
	}
	return n
}
