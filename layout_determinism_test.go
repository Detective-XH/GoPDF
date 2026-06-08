// layout_determinism_test.go — determinism characterization for the legacy
// column/row path (GetTextByColumn / GetTextByRow / walkTextBlocks).
//
// These tests augment the existing behavioral coverage in page_test.go and
// layout_test.go by locking the three dimensions those tests do not assert:
// the full-grid (Position, []S) snapshot, the equal-(X,Y) tie ordering, and
// cross-Reader determinism. They pin current behavior; the equal-(X,Y) tie
// case in particular intentionally locks the float-equality ordering in
// TextVertical.Less / TextHorizontal.Less — that must NOT change here (the
// epsilon fix belongs to a separate maintenance task, not this path).
package pdf

import (
	"reflect"
	"testing"
)

// posContent is a comparable snapshot of one Column/Row: its integer Position and
// the ordered S values of its Content. A named type (not a repeated anonymous struct)
// keeps reflect.DeepEqual robust against field-order drift across call sites.
type posContent struct {
	Pos int64
	S   []string
}

// gridStream lays out a deterministic 2x2 grid of single-glyph runs:
//
//	X=100      X=300
//	A          B        (Y=700, top row)
//	C          D        (Y=500, bottom row)
//
// No font resource is declared, so walkTextBlocks decodes with nopEncoder and the
// glyph bytes survive verbatim — every Tj yields exactly one Text at a known (X,Y).
const gridStream = "BT\n100 700 Td\n(A) Tj\nET\n" +
	"BT\n300 700 Td\n(B) Tj\nET\n" +
	"BT\n100 500 Td\n(C) Tj\nET\n" +
	"BT\n300 500 Td\n(D) Tj\nET"

// columnSlice flattens Columns into a comparable shape for snapshot assertions.
func columnSlice(cols Columns) []posContent {
	out := make([]posContent, 0, len(cols))
	for _, c := range cols {
		var ss []string
		for _, t := range c.Content {
			ss = append(ss, t.S)
		}
		out = append(out, posContent{c.Position, ss})
	}
	return out
}

func rowSlice(rows Rows) []posContent {
	out := make([]posContent, 0, len(rows))
	for _, r := range rows {
		var ss []string
		for _, t := range r.Content {
			ss = append(ss, t.S)
		}
		out = append(out, posContent{r.Position, ss})
	}
	return out
}

// TestGetTextByColumnGridSnapshot locks the exact (Position, []S) sequence for a
// 2x2 grid: column order (Position ascending) AND within-column order (Y descending)
// in one assertion. This is the golden the sort change must preserve.
func TestGetTextByColumnGridSnapshot(t *testing.T) {
	r, err := OpenBytes(buildSinglePagePDF(gridStream))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	cols, err := r.Page(1).GetTextByColumn()
	if err != nil {
		t.Fatalf("GetTextByColumn: %v", err)
	}
	want := []posContent{
		{100, []string{"A", "C"}},
		{300, []string{"B", "D"}},
	}
	if got := columnSlice(cols); !reflect.DeepEqual(got, want) {
		t.Errorf("GetTextByColumn grid = %+v, want %+v", got, want)
	}
}

// TestGetTextByRowGridSnapshot is the symmetric golden for rows: row order
// (Position descending) AND within-row order (X ascending).
func TestGetTextByRowGridSnapshot(t *testing.T) {
	r, err := OpenBytes(buildSinglePagePDF(gridStream))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	rows, err := r.Page(1).GetTextByRow()
	if err != nil {
		t.Fatalf("GetTextByRow: %v", err)
	}
	want := []posContent{
		{700, []string{"A", "B"}},
		{500, []string{"C", "D"}},
	}
	if got := rowSlice(rows); !reflect.DeepEqual(got, want) {
		t.Errorf("GetTextByRow grid = %+v, want %+v", got, want)
	}
}

// TestGetTextByColumnEqualXYTieStable locks the tie case the existing tests do not
// cover: two runs at the SAME X and SAME Y land in one column and, because
// TextVertical.Less treats them as equal, keep content-stream (insertion) order.
// This intentionally pins the current float-equality behavior; it must NOT change
// (the epsilon fix belongs to a separate maintenance task, not here).
func TestGetTextByColumnEqualXYTieStable(t *testing.T) {
	stream := "BT\n100 700 Td\n(First) Tj\nET\nBT\n100 700 Td\n(Second) Tj\nET"
	r, err := OpenBytes(buildSinglePagePDF(stream))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	cols, err := r.Page(1).GetTextByColumn()
	if err != nil {
		t.Fatalf("GetTextByColumn: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("want 1 column, got %d", len(cols))
	}
	got := []string{}
	for _, c := range cols[0].Content {
		got = append(got, c.S)
	}
	if want := []string{"First", "Second"}; !reflect.DeepEqual(got, want) {
		t.Errorf("equal-(X,Y) column order = %v, want %v (insertion order)", got, want)
	}
}

// TestGetTextByRowEqualXYTieStable is the symmetric tie lock for rows.
func TestGetTextByRowEqualXYTieStable(t *testing.T) {
	stream := "BT\n100 700 Td\n(First) Tj\nET\nBT\n100 700 Td\n(Second) Tj\nET"
	r, err := OpenBytes(buildSinglePagePDF(stream))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	rows, err := r.Page(1).GetTextByRow()
	if err != nil {
		t.Fatalf("GetTextByRow: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	got := []string{}
	for _, c := range rows[0].Content {
		got = append(got, c.S)
	}
	if want := []string{"First", "Second"}; !reflect.DeepEqual(got, want) {
		t.Errorf("equal-(X,Y) row order = %v, want %v (insertion order)", got, want)
	}
}

// TestGetTextByColumnDeterministicAcrossReaders parses the same bytes through two
// independent Readers and asserts the Columns are deeply equal. reflect.DeepEqual
// follows the *Column pointers and compares Column values (we never compare the
// pointers themselves). This is the determinism contract the SliceStable change
// guards.
func TestGetTextByColumnDeterministicAcrossReaders(t *testing.T) {
	data := buildSinglePagePDF(gridStream)
	r1, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes r1: %v", err)
	}
	r2, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes r2: %v", err)
	}
	c1, err := r1.Page(1).GetTextByColumn()
	if err != nil {
		t.Fatalf("GetTextByColumn r1: %v", err)
	}
	c2, err := r2.Page(1).GetTextByColumn()
	if err != nil {
		t.Fatalf("GetTextByColumn r2: %v", err)
	}
	if !reflect.DeepEqual(c1, c2) {
		t.Errorf("GetTextByColumn not deterministic across readers:\n r1=%+v\n r2=%+v",
			columnSlice(c1), columnSlice(c2))
	}
}

// TestGetTextByRowDeterministicAcrossReaders is the symmetric determinism lock.
func TestGetTextByRowDeterministicAcrossReaders(t *testing.T) {
	data := buildSinglePagePDF(gridStream)
	r1, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes r1: %v", err)
	}
	r2, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes r2: %v", err)
	}
	rw1, err := r1.Page(1).GetTextByRow()
	if err != nil {
		t.Fatalf("GetTextByRow r1: %v", err)
	}
	rw2, err := r2.Page(1).GetTextByRow()
	if err != nil {
		t.Fatalf("GetTextByRow r2: %v", err)
	}
	if !reflect.DeepEqual(rw1, rw2) {
		t.Errorf("GetTextByRow not deterministic across readers:\n r1=%+v\n r2=%+v",
			rowSlice(rw1), rowSlice(rw2))
	}
}
