package pdf

import (
	"os"
	"strings"
	"testing"
)

// TestLatticeAccuracyBEA scores the B2 lattice on the held-out BEA per-cell-grid table
// (Survey of Current Business GDP Table 1, 2024) against its companion-authored
// .cellgrid.tsv golden. DATA cells (golden rows after header_rows=3) are scored; rows
// are anchored on the col-0 line-number label ("1".."26" clean; "27".."32" are Addenda
// rows whose col-0 is empty in the detector due to word-segmentation fusing — documented
// limitation, not a geometry failure). The mechanism assertion confirms the BEA phantom-
// clamp fired: open grid must have FEWER rows than the closed grid (clamp removes
// title+footnote phantom rows, 40 closed → 36 open).
//
// BLOCKING accuracy gates: dataCells, content, and verbatim floors measured live
// 2026-06-20 after the BEA branch landed.
func TestLatticeAccuracyBEA(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/bea-scb-gdp-2024-t1.pdf")
	if err != nil {
		t.Fatalf("open BEA: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	p := r.Page(1)
	c := p.Content()
	words, err := p.Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}

	// --- OPEN table (BEA phantom-clamp via inferFillBandedRows BEA branch) ---
	openTables := latticeTablesOpen(c, words, p.MediaBox())
	if len(openTables) == 0 {
		t.Fatalf("latticeTablesOpen found NO table on BEA p1")
	}
	largestOpen := openTables[0]
	for _, tb := range openTables[1:] {
		if len(tb) > len(largestOpen) {
			largestOpen = tb
		}
	}
	grid := reconstructGrid(largestOpen, words)
	openRows := len(grid)
	openCols := 0
	if openRows > 0 {
		openCols = len(grid[0])
	}
	t.Logf("latticeTablesOpen: %d tables on p1; largest=%d cells; grid=%dx%d",
		len(openTables), len(largestOpen), openRows, openCols)
	for ri := range openRows {
		c0, c1, c2 := "", "", ""
		if len(grid[ri]) > 0 {
			c0 = grid[ri][0]
		}
		if len(grid[ri]) > 1 {
			c1 = grid[ri][1]
		}
		if len(grid[ri]) > 2 {
			c2 = grid[ri][2]
		}
		t.Logf("  row %2d: c0=%q c1=%q c2=%q", ri, c0, c1, c2)
	}

	// --- CLOSED table (for mechanism assertion only) ---
	closedTables := latticeTables(c)
	var closedRows int
	if len(closedTables) > 0 {
		largestClosed := closedTables[0]
		for _, tb := range closedTables[1:] {
			if len(tb) > len(largestClosed) {
				largestClosed = tb
			}
		}
		closedGrid := reconstructGrid(largestClosed, words)
		closedRows = len(closedGrid)
	}
	t.Logf("latticeTables (closed): %d tables; largest closed grid rows=%d", len(closedTables), closedRows)

	// Load the golden.
	gdata, err := os.ReadFile("testdata/corpus/tables/bea-scb-gdp-2024-t1.cellgrid.tsv")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	g, err := parseCellGrid(gdata)
	if err != nil {
		t.Fatalf("parseCellGrid: %v", err)
	}
	t.Logf("golden: %dx%d header_rows=%d", g.rows, g.cols, g.headerRows)

	// Anchor each golden DATA row on col[0] only — the unique line label ("1".."26").
	// Rows whose col[0] anchor is empty ("Addenda:" section-label) are skipped; rows
	// with anchor "27".."32" miss (detector col[0] empty due to word-seg fusing).
	findRow := func(lineNum string) int {
		want := looseCell(lineNum)
		for ri := range grid {
			if len(grid[ri]) > 0 && looseCell(grid[ri][0]) == want {
				return ri
			}
		}
		return -1
	}

	var content, verbatim, total, missingRows int
	for gr := g.headerRows; gr < g.rows; gr++ {
		gd := g.cells[gr]
		line := gd[0]
		if strings.TrimSpace(line) == "" {
			continue // section-label row (e.g. "Addenda:") — not a scored data row
		}
		rr := findRow(line)
		if rr < 0 {
			missingRows++
			total += g.cols
			t.Logf("MISSING data row line=%q (all %d cols miss)", line, g.cols)
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
		if rowContent < g.cols {
			var gotRow []string
			if rr < len(grid) {
				gotRow = grid[rr]
			}
			t.Logf("row line=%-4q content=%2d/%d  GOT=%v", line, rowContent, g.cols, strings.Join(gotRow, "|"))
		}
	}
	dataCells := total
	t.Logf("=== BEA DATA-CELL accuracy (%d missing line rows) ===", missingRows)
	t.Logf("dataCells=%d  CONTENT=%d (%.1f%%)  VERBATIM=%d (%.1f%%)",
		dataCells, content, 100*float64(content)/float64(dataCells),
		verbatim, 100*float64(verbatim)/float64(dataCells))

	// --- BLOCKING accuracy gates (floors measured live 2026-06-20) ---
	// 32 scored rows × 11 cols = 352; 26 matched rows anchor-clean (lines 1–26); 6 Addenda
	// rows miss (word-seg fusing of line# + series token, documented limitation). Content
	// is limited by sub-item rows where the word segmenter fuses adjacent numeric cells
	// (e.g. "21.4−1.23.05.6") — a pre-existing word-spacing issue, not a geometry failure.
	if dataCells != 352 {
		t.Errorf("BEA denominator drift: got %d data cells, want 352 (32 scored rows x 11 cols)", dataCells)
	}
	if content < 129 {
		t.Errorf("BEA content regressed: %d/352 (want >=129)", content)
	}
	if verbatim < 129 {
		t.Errorf("BEA verbatim regressed: %d/352 (want >=129)", verbatim)
	}

	// --- Mechanism-fired positive assertion ---
	// BEA phantom-clamp REDUCES rows: open must be STRICTLY FEWER than closed.
	// Expected: open=36 rows, closed=40 rows.
	if openRows >= closedRows {
		t.Errorf("BEA phantom-clamp did not fire: open=%d closed=%d (expected open < closed)",
			openRows, closedRows)
	} else {
		t.Logf("BEA phantom-clamp confirmed: open=%d rows < closed=%d rows", openRows, closedRows)
	}
}
