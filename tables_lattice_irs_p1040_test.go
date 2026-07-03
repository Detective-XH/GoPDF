package pdf

import "testing"

// TestShadedSiblingRowsIRSP1040RowRecovery locks the row-collapse fix: alternating-row shading
// applied to only some conceptual columns of a table left the unshaded sibling columns as one
// tall, collapsed cell instead of one cell per row. inferShadedSiblingRows recovers this by
// borrowing the shaded siblings' own row Y-boundaries, gated by a donor-provenance check
// (rectOnlyHEdges) plus a per-instance consequence invariant. Values below are render-truth
// (direct PDF read), not derived from the extractor's own output.
func TestShadedSiblingRowsIRSP1040RowRecovery(t *testing.T) {
	_, r, err := Open("testdata/corpus/hard/irs-p1040-tax-tables-excerpt.pdf")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tables, err := r.Page(1).Tables()
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	if len(tables) != 3 {
		t.Fatalf("got %d tables, want 3 (one per side-by-side $1,000/$3,000/$6,000/$9,000-block set)", len(tables))
	}
	// Each block is a distinct table containing 3 stacked $1,000-wide sections (20 data rows +
	// 3 header/section-label rows each = 65 total incl. the 2 shared column-header rows... the
	// exact row layout is asserted by content, not recomputed here).
	cases := []struct {
		table   int
		row     int
		want    string
		wantCol int
	}{
		// Block 0 ("3,000"-"12,000" range): section "3,000", row 3 = first data row, row 22 =
		// last data row of that section.
		{0, 3, "3,000 3,050 303", 0},
		{0, 3, "303", 2},
		{0, 3, "303", 3},
		{0, 3, "303", 4},
		{0, 22, "3,950 4,000 398", 0},
		{0, 22, "398", 2},
		{0, 22, "398", 3},
		{0, 22, "398", 4},
		// Block 1 ("6,000"-"15,000" range).
		{1, 3, "6,000 6,050 603", 0},
		{1, 22, "6,950 7,000 698", 0},
		// Block 2 ("9,000"-"18,000" range).
		{2, 3, "9,000 9,050 903", 0},
		{2, 22, "9,950 10,000 998", 0},
	}
	for _, c := range cases {
		tb := tables[c.table]
		if c.row >= len(tb.Cells) {
			t.Fatalf("table[%d]: row %d out of range (have %d rows)", c.table, c.row, len(tb.Cells))
		}
		row := tb.Cells[c.row]
		if c.wantCol >= len(row) {
			t.Fatalf("table[%d] row[%d]: col %d out of range (have %d cols): %q", c.table, c.row, c.wantCol, len(row), row)
		}
		if got := row[c.wantCol]; got != c.want {
			t.Errorf("table[%d] row[%d] col[%d] = %q, want %q (full row: %q)", c.table, c.row, c.wantCol, got, c.want, row)
		}
	}
}
