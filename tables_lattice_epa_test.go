package pdf

import (
	"os"
	"strings"
	"testing"
)

// TestLatticeAccuracyEPA scores the B2 lattice on the held-out STROKE-bordered
// EPA eGRID Table 1 (p.2) against its companion+render-verified .cellgrid.tsv
// golden. DATA cells (golden rows after header_rows) are the primary gate; the
// grid is anchored row-by-row on the leftmost acronym cell, then columns are
// compared positionally. Diagnostic-only (never t.Errorf) — a measurement.
func TestLatticeAccuracyEPA(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/epa-egrid2022-t1.pdf")
	if err != nil {
		t.Fatalf("open EPA: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	p := r.Page(2)
	c := p.Content()
	words, err := p.Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}

	tables := latticeTables(c)
	if len(tables) == 0 {
		t.Fatalf("lattice found NO table on EPA p2 (edges=%d)", len(mergeEdges(edgesFromContent(c), 3, 3)))
	}
	largest := tables[0]
	for _, tb := range tables[1:] {
		if len(tb) > len(largest) {
			largest = tb
		}
	}
	grid := reconstructGrid(largest, words)
	rR := len(grid)
	cR := 0
	if rR > 0 {
		cR = len(grid[0])
	}
	t.Logf("lattice: %d tables on p2; largest=%d cells; reconstructed grid=%dx%d", len(tables), len(largest), rR, cR)
	for ri := 0; ri < rR; ri++ {
		c0, c2, c16 := "", "", ""
		if len(grid[ri]) > 0 {
			c0 = grid[ri][0]
		}
		if len(grid[ri]) > 2 {
			c2 = grid[ri][2]
		}
		if len(grid[ri]) > 16 {
			c16 = grid[ri][16]
		}
		t.Logf("  Rrow %2d: c0=%q c2=%q c16=%q", ri, c0, c2, c16)
	}

	// Load the golden.
	gdata, err := os.ReadFile("testdata/corpus/tables/epa-egrid2022-t1.cellgrid.tsv")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	g, err := parseCellGrid(gdata)
	if err != nil {
		t.Fatalf("parseCellGrid: %v", err)
	}
	t.Logf("golden: %dx%d header_rows=%d", g.rows, g.cols, g.headerRows)

	// Anchor each golden DATA row on its acronym (col 0) within the reconstructed grid.
	findRow := func(acr string) int {
		want := looseCell(acr)
		for ri := range grid {
			for ci := 0; ci < len(grid[ri]) && ci < 3; ci++ { // acronym is leftmost
				if looseCell(grid[ri][ci]) == want {
					return ri
				}
			}
		}
		return -1
	}

	var content, verbatim, total, missingRows int
	for gr := g.headerRows; gr < g.rows; gr++ {
		gd := g.cells[gr]
		acr := gd[0]
		rr := findRow(acr)
		if rr < 0 {
			missingRows++
			total += g.cols
			t.Logf("MISSING data row acronym=%q (all %d cols miss)", acr, g.cols)
			continue
		}
		rowContent := 0
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
				rowContent++
			}
		}
		if rowContent < g.cols { // log imperfect rows for diagnosis
			var gotRow []string
			if rr < len(grid) {
				gotRow = grid[rr]
			}
			t.Logf("row acr=%-5q content=%2d/%d  GOT=%v", acr, rowContent, g.cols, strings.Join(gotRow, "|"))
		}
	}
	dataCells := total
	t.Logf("=== EPA DATA-CELL accuracy (28 rows x %d cols, %d missing rows) ===", g.cols, missingRows)
	t.Logf("CONTENT (space-insensitive) = %d/%d (%.1f%%)  <- the gate metric", content, dataCells, 100*float64(content)/float64(dataCells))
	t.Logf("VERBATIM (strict)           = %d/%d (%.1f%%)", verbatim, dataCells, 100*float64(verbatim)/float64(dataCells))

	// --- Task B: open-recovery non-regression ---
	// latticeTablesOpen must produce an identical grid to the closed-only latticeTables on
	// a fully-bordered table (open-edge recovery is a structural no-op when there are no
	// words outside [vMin,vMax] within the table Y-span that span >= minOpenRows bands).
	openTablesEPA := latticeTablesOpen(c, words, p.MediaBox())
	if len(openTablesEPA) == 0 {
		t.Errorf("EPA open: latticeTablesOpen found NO tables")
	} else {
		largestOpenEPA := openTablesEPA[0]
		for _, tb := range openTablesEPA[1:] {
			if len(tb) > len(largestOpenEPA) {
				largestOpenEPA = tb
			}
		}
		gridOpen := reconstructGrid(largestOpenEPA, words)
		openR := len(gridOpen)
		openC := 0
		if openR > 0 {
			openC = len(gridOpen[0])
		}
		t.Logf("EPA dual-run: open grid=%dx%d  closed grid=%dx%d", openR, openC, rR, cR)

		identical := true
		if openR != rR {
			identical = false
			t.Errorf("EPA dual-run: row count mismatch: open=%d closed=%d", openR, rR)
		}
		for ri := 0; ri < openR && ri < rR; ri++ {
			if len(gridOpen[ri]) != len(grid[ri]) {
				identical = false
				t.Errorf("EPA dual-run: row %d col count mismatch: open=%d closed=%d", ri, len(gridOpen[ri]), len(grid[ri]))
				continue
			}
			for ci := range gridOpen[ri] {
				if gridOpen[ri][ci] != grid[ri][ci] {
					identical = false
					t.Errorf("EPA dual-run: cell [%d][%d] open=%q closed=%q", ri, ci, gridOpen[ri][ci], grid[ri][ci])
				}
			}
		}
		if identical {
			t.Logf("EPA dual-run identical: YES")
		} else {
			t.Logf("EPA dual-run identical: NO")
		}

		// Confirm 476/476 still holds on the open grid.
		var openContent, openTotal int
		for gr := g.headerRows; gr < g.rows; gr++ {
			gd := g.cells[gr]
			acr := gd[0]
			rr2 := findRow(acr)
			if rr2 < 0 {
				openTotal += g.cols
				continue
			}
			for ci := 0; ci < g.cols; ci++ {
				openTotal++
				var got string
				if ci < len(gridOpen[rr2]) {
					got = gridOpen[rr2][ci]
				}
				if looseCell(got) == looseCell(gd[ci]) {
					openContent++
				}
			}
		}
		t.Logf("EPA open grid accuracy: CONTENT = %d/%d (%.1f%%)", openContent, openTotal, 100*float64(openContent)/float64(openTotal))
		if openContent != content || openTotal != dataCells {
			t.Errorf("EPA open accuracy mismatch: open=%d/%d closed=%d/%d", openContent, openTotal, content, dataCells)
		}
	}
}
