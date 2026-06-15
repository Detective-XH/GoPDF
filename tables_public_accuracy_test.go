// tables_public_accuracy_test.go — MEASURE-FIRST diagnostic for the public
// p.Tables() surface (v0.9.0 graduation gate).
//
// This file scores the PUBLIC Table.Cells [][]string API against ground truth
// for all three tuned fixtures. It uses t.Logf throughout (NO t.Errorf for
// accuracy); t.Fatalf is used only for setup failures. The purpose is to
// confirm that the frozen public surface matches the internal accuracy numbers
// captured in tables_lattice_accuracy_test.go, tables_lattice_epa_test.go, and
// tables_lattice_irs_test.go before flipping any blocking accuracy gate.
//
// Reuses package-level symbols from the existing test files:
//   - looseCell, looseIRS, normCell, pct (tables_lattice_accuracy_test.go,
//     tables_lattice_irs_test.go)
//   - parseCellGrid, cellGrid (corpus_cellgrid_test.go)
//   - nistAreaGolden (tables_lattice_accuracy_test.go)
//
// Local helper names are deliberately distinct so there is no redeclaration.
package pdf

import (
	"os"
	"strings"
	"testing"
)

// publicGridTotalCells returns the total cell count for a Table by summing row
// lengths. Rows are always equal length after reconstructGrid, so this is
// rows*cols — but the sum form is robust even for a ragged result.
func publicGridTotalCells(t Table) int {
	n := 0
	for _, row := range t.Cells {
		n += len(row)
	}
	return n
}

// publicLargestTable returns the Table from tables with the most cells total.
// Returns the zero Table (empty Cells) if tables is empty.
func publicLargestTable(tables []Table) Table {
	if len(tables) == 0 {
		return Table{}
	}
	best := tables[0]
	for _, tb := range tables[1:] {
		if publicGridTotalCells(tb) > publicGridTotalCells(best) {
			best = tb
		}
	}
	return best
}

// ---- EPA fixture ----

// publicEPAFindRow scans the leftmost <=3 columns of each grid row for a
// looseCell match of acr. Returns the row index or -1 if not found.
func publicEPAFindRow(grid [][]string, acr string) int {
	want := looseCell(acr)
	for ri, row := range grid {
		lim := min(len(row), 3)
		for ci := range lim {
			if looseCell(row[ci]) == want {
				return ri
			}
		}
	}
	return -1
}

// scoreEPAPublic scores the public grid for the EPA fixture against golden g.
// It anchors each golden DATA row on its col0 acronym, then compares
// positionally. Counts content (looseCell) and verbatim (strict).
func scoreEPAPublic(t *testing.T, grid [][]string, g cellGrid) (content, verbatim, total int) {
	for gr := g.headerRows; gr < g.rows; gr++ {
		gd := g.cells[gr]
		acr := gd[0]
		rr := publicEPAFindRow(grid, acr)
		if rr < 0 {
			total += g.cols
			t.Logf("EPA PUBLIC: MISSING data row acronym=%q (all %d cols miss)", acr, g.cols)
			continue
		}
		for ci := 0; ci < g.cols; ci++ {
			total++
			var got string
			if ci < len(grid[rr]) {
				got = grid[rr][ci]
			}
			if got == gd[ci] {
				verbatim++
			}
			if looseCell(got) == looseCell(gd[ci]) {
				content++
			}
		}
	}
	return content, verbatim, total
}

// TestPublicAccuracyEPA scores p.Tables() on the EPA eGRID2022 p2 fixture.
func TestPublicAccuracyEPA(t *testing.T) {
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
	p := r.Page(2)

	t.Run("epa", func(t *testing.T) {
		tables, err := p.Tables()
		if err != nil {
			t.Fatalf("p.Tables() error: %v", err)
		}
		if len(tables) == 0 {
			t.Fatalf("p.Tables() returned no tables on EPA p2")
		}
		largest := publicLargestTable(tables)
		grid := largest.Cells
		nRows := len(grid)
		nCols := 0
		if nRows > 0 {
			nCols = len(grid[0])
		}
		t.Logf("EPA PUBLIC: %d tables; largest grid=%dx%d total=%d cells",
			len(tables), nRows, nCols, publicGridTotalCells(largest))

		gdata, err := os.ReadFile("testdata/corpus/tables/epa-egrid2022-t1.cellgrid.tsv")
		if err != nil {
			t.Fatalf("read EPA golden: %v", err)
		}
		g, err := parseCellGrid(gdata)
		if err != nil {
			t.Fatalf("parseCellGrid EPA: %v", err)
		}
		t.Logf("EPA PUBLIC: golden=%dx%d header_rows=%d", g.rows, g.cols, g.headerRows)

		content, verbatim, total := scoreEPAPublic(t, grid, g)
		t.Logf("EPA PUBLIC content=%d/%d (%.1f%%) verbatim=%d/%d (%.1f%%)",
			content, total, pct(content, total),
			verbatim, total, pct(verbatim, total))

		// --- BLOCKING gates (v0.9.0 floor = measured live 2026-06-15) ---
		if total != 476 {
			t.Errorf("EPA PUBLIC denominator drift: got %d data cells, want 476 (28 rows x 17 cols)", total)
		}
		if content < 476 {
			t.Errorf("EPA PUBLIC content regressed: %d/476", content)
		}
		if verbatim < 476 {
			t.Errorf("EPA PUBLIC verbatim regressed: %d/476", verbatim)
		}
	})
}

// ---- IRS fixture ----

// publicIRSFindRow searches ALL cells in the grid for a looseIRS match of
// ty2021 (the TY2021 value in golden col1). Returns the row index or -1.
func publicIRSFindRow(grid [][]string, ty2021 string) int {
	want := looseIRS(ty2021)
	for ri, row := range grid {
		for _, cell := range row {
			if looseIRS(cell) == want {
				return ri
			}
		}
	}
	return -1
}

// scoreIRSPositional scores 32 numeric rows x golden cols 1..5 using positional
// column mapping (golden col j → grid col j). Returns (content, verbatim, total)
// and also fills col5 and col14 sub-tallies.
func scoreIRSPositional(t *testing.T, grid [][]string, g cellGrid) (
	content, verbatim, total int,
	col5Content, col5Verbatim, col5Total int,
	col14Content, col14Total int,
) {
	for gr := g.headerRows; gr < g.rows; gr++ {
		gd := g.cells[gr]
		if strings.TrimSpace(gd[1]) == "" {
			continue // section-label row
		}
		rr := publicIRSFindRow(grid, gd[1])
		if rr < 0 {
			total += 5
			col5Total++
			col14Total += 4
			t.Logf("IRS POSITIONAL: MISSING numeric row TY2021=%q", gd[1])
			continue
		}
		for gc := 1; gc <= 5; gc++ {
			total++
			if gc == 5 {
				col5Total++
			} else {
				col14Total++
			}
			var got string
			if gc < len(grid[rr]) {
				got = grid[rr][gc]
			}
			if got == gd[gc] {
				verbatim++
				if gc == 5 {
					col5Verbatim++
				}
			}
			if looseIRS(got) == looseIRS(gd[gc]) {
				content++
				if gc == 5 {
					col5Content++
				} else {
					col14Content++
				}
			}
		}
	}
	return
}

// TestPublicAccuracyIRS scores p.Tables() on the IRS SOI Table 1 p1 fixture.
func TestPublicAccuracyIRS(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/irs-soi-inpre-t1-2022.pdf")
	if err != nil {
		t.Fatalf("open IRS: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat IRS: %v", err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader IRS: %v", err)
	}
	p := r.Page(1)

	t.Run("irs", func(t *testing.T) {
		tables, err := p.Tables()
		if err != nil {
			t.Fatalf("p.Tables() error: %v", err)
		}
		if len(tables) == 0 {
			t.Fatalf("p.Tables() returned no tables on IRS p1")
		}
		largest := publicLargestTable(tables)
		grid := largest.Cells
		nRows := len(grid)
		nCols := 0
		if nRows > 0 {
			nCols = len(grid[0])
		}
		t.Logf("IRS PUBLIC: %d tables; largest grid=%dx%d total=%d cells",
			len(tables), nRows, nCols, publicGridTotalCells(largest))

		gdata, err := os.ReadFile("testdata/corpus/tables/irs-soi-inpre-t1-2022.cellgrid.tsv")
		if err != nil {
			t.Fatalf("read IRS golden: %v", err)
		}
		g, err := parseCellGrid(gdata)
		if err != nil {
			t.Fatalf("parseCellGrid IRS: %v", err)
		}
		t.Logf("IRS PUBLIC: golden=%dx%d header_rows=%d", g.rows, g.cols, g.headerRows)

		// --- POSITIONAL strategy (golden col j -> grid col j) ---
		// Gate is POSITIONAL only: the IRS grid is [label,1,2,3,4,5]; positional mapping
		// was measured safe (160/160 content+verbatim) at v0.9.0.
		posContent, posVerbatim, posTotal,
			pos5C, pos5V, pos5T,
			pos14C, pos14T := scoreIRSPositional(t, grid, g)

		t.Logf("IRS PUBLIC POSITIONAL gate content=%d/%d (%.1f%%) verbatim=%d/%d (%.1f%%)",
			posContent, posTotal, pct(posContent, posTotal),
			posVerbatim, posTotal, pct(posVerbatim, posTotal))
		t.Logf("IRS PUBLIC POSITIONAL col5 content=%d/%d (%.1f%%) verbatim=%d/%d (%.1f%%)",
			pos5C, pos5T, pct(pos5C, pos5T),
			pos5V, pos5T, pct(pos5V, pos5T))
		t.Logf("IRS PUBLIC POSITIONAL interior cols1-4 content=%d/%d (%.1f%%)",
			pos14C, pos14T, pct(pos14C, pos14T))

		// --- BLOCKING gates (v0.9.0 floor = measured live 2026-06-15) ---
		if posTotal != 160 {
			t.Errorf("IRS PUBLIC denominator drift: got %d positional cells, want 160 (32 rows x 5 cols)", posTotal)
		}
		if posContent < 160 {
			t.Errorf("IRS PUBLIC content regressed: %d/160", posContent)
		}
		if posVerbatim < 160 {
			t.Errorf("IRS PUBLIC verbatim regressed: %d/160", posVerbatim)
		}
		if pos5C < 32 {
			t.Errorf("IRS PUBLIC col5 content regressed: %d/32", pos5C)
		}
		if pos5V < 32 {
			t.Errorf("IRS PUBLIC col5 verbatim regressed: %d/32", pos5V)
		}
		if pos14C < 128 {
			t.Errorf("IRS PUBLIC interior cols1-4 content regressed: %d/128", pos14C)
		}
	})
}

// ---- NIST fixture ----

// scoreNISTPublic scores the public grid against nistAreaGolden over the
// overlapping region (min rows, 2 cols). Returns verbatim and content (looseCell).
func scoreNISTPublic(grid [][]string) (verbatim, content, total int) {
	// Iterate the FULL golden region (17x2) so a missing or ragged grid cell is scored as a
	// MISS, never dropped from the denominator. Scoring only the min-overlap would let the
	// content gate pass vacuously on a shrunken total if a later row lost a cell (the shape
	// check reads only grid[0]'s width). total is therefore structurally fixed at 34.
	for ri := range nistAreaGolden {
		for ci := range 2 {
			total++
			var got string
			if ri < len(grid) && ci < len(grid[ri]) {
				got = grid[ri][ci]
			}
			want := nistAreaGolden[ri][ci]
			if got == want {
				verbatim++
			}
			if looseCell(got) == looseCell(want) {
				content++
			}
		}
	}
	return verbatim, content, total
}

// TestPublicAccuracyNIST scores p.Tables() on the NIST HB44 p23 fixture.
func TestPublicAccuracyNIST(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/nist-hb44-appc-2026.pdf")
	if err != nil {
		t.Fatalf("open NIST: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat NIST: %v", err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader NIST: %v", err)
	}
	p := r.Page(23)

	t.Run("nist", func(t *testing.T) {
		tables, err := p.Tables()
		if err != nil {
			t.Fatalf("p.Tables() error: %v", err)
		}
		if len(tables) == 0 {
			t.Fatalf("p.Tables() returned no tables on NIST p23")
		}
		largest := publicLargestTable(tables)
		grid := largest.Cells
		nRows := len(grid)
		nCols := 0
		if nRows > 0 {
			nCols = len(grid[0])
		}
		t.Logf("NIST PUBLIC: %d tables; largest grid=%dx%d total=%d cells",
			len(tables), nRows, nCols, publicGridTotalCells(largest))

		verbatim, content, total := scoreNISTPublic(grid)
		t.Logf("NIST PUBLIC shape=%dx%d verbatim=%d/%d (%.1f%%) content=%d/%d (%.1f%%)",
			nRows, nCols,
			verbatim, total, pct(verbatim, total),
			content, total, pct(content, total))

		// --- BLOCKING gates (v0.9.0 floor = measured live 2026-06-15) ---
		if nRows != 17 || nCols != 2 {
			t.Errorf("NIST PUBLIC shape regressed: got %dx%d want 17x2", nRows, nCols)
		}
		if total != 34 {
			t.Errorf("NIST PUBLIC denominator drift: got %d cells, want 34 (17 rows x 2 cols)", total)
		}
		if content < 33 {
			t.Errorf("NIST PUBLIC content regressed: %d/%d want >=33/34", content, total)
		}
		// INTENTIONALLY diagnostic (not a gate): verbatim has 11 misses due to the
		// superscript-rendering quirk — GoPDF renders cm² as "cm 2" (spaced separate token)
		// rather than "cm²". This is a font-extraction limit, NOT a lattice error. Gating on
		// verbatim here would penalise Page.Tables() for a text-layer issue; it must NOT gate
		// Page.Tables() stability. Advisor-confirmed as threshold T for the v0.9.0 graduation.
		t.Logf("NIST PUBLIC verbatim=%d/%d (diagnostic only — superscript quirk, not gated)", verbatim, total)
	})
}
