package pdf

import (
	"os"
	"strings"
	"testing"
)

// TestPageTablesIRSOpenColumn exercises the public Page.Tables() wrapper end to end on the
// real IRS SOI fixture. The internal accuracy gate (TestLatticeAccuracyIRS) locks the
// cell-level numbers against latticeTablesOpen directly; this test locks the public surface:
// that p.Words()/p.Content()/p.MediaBox() flow through correctly and that the structural
// open-column recovery surfaces the right-edge column (col5) whose value only appears once
// the half-open column is recovered.
func TestPageTablesIRSOpenColumn(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/irs-soi-inpre-t1-2022.pdf")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	tables, err := r.Page(1).Tables()
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("Tables() returned 0 tables; want >= 1 (the bordered IRS data grid)")
	}

	// Largest table = the data grid. Open recovery adds the left label column (col0) and the
	// right AGI-bracket column (col5), so the grid is the full 6 columns, not the 4 interior.
	largest := tables[0]
	for _, tb := range tables[1:] {
		if len(tb.Cells) > len(largest.Cells) {
			largest = tb
		}
	}
	cols := 0
	if len(largest.Cells) > 0 {
		cols = len(largest.Cells[0])
	}
	if cols < 6 {
		t.Errorf("largest table has %d columns; want >= 6 (col0 label + 4 interior + col5 recovered)", cols)
	}
	if len(largest.Cells) < 30 {
		t.Errorf("largest table has %d rows; want >= 30 numeric data rows", len(largest.Cells))
	}

	// col5 sentinel: "495,215" is a distinct value of the recovered right-edge column.
	// Its presence proves the open-column recovery flowed through the public Page.Tables().
	var flat strings.Builder
	for _, row := range largest.Cells {
		for _, cell := range row {
			flat.WriteString(cell)
			flat.WriteByte('\n')
		}
	}
	if !strings.Contains(flat.String(), "495,215") {
		t.Errorf("recovered grid missing col5 sentinel %q; open-column recovery did not flow through Page.Tables()", "495,215")
	}
}

// TestPageTablesNoFalsePositive verifies the public Page.Tables() returns no tables on a
// prose discriminator (the same 0-FP contract the internal FP gate locks), exercising the
// wrapper's empty-result path end to end.
func TestPageTablesNoFalsePositive(t *testing.T) {
	f, err := os.Open("testdata/corpus/multicolumn/fr-2024-06543.pdf")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		tables, err := p.Tables()
		if err != nil {
			t.Fatalf("Tables p%d: %v", i, err)
		}
		if len(tables) != 0 {
			t.Errorf("Tables() on prose page %d returned %d tables; want 0 (false positive)", i, len(tables))
		}
	}
}
