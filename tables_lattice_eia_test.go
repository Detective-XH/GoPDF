package pdf

import (
	"os"
	"strings"
	"testing"
)

// TestLatticeAccuracyEIA scores the B2 lattice on the held-out FILL-BANDED EIA AER
// Table 3.1 (2011 edition, p.1) against its companion+render-verified .cellgrid.tsv
// golden. DATA cells (golden rows after header_rows=2) are the primary gate; the grid
// is anchored row-by-row on the leftmost year label (col 0 only), then columns are
// compared positionally. The scored grid comes from latticeTablesOpen because
// inferFillBandedRows runs ONLY inside that path (NOT in latticeTables closed-only).
// A second mechanism assertion confirms the fill-banded split fired: open grid must
// have strictly more data rows than the closed grid.
// BLOCKING accuracy gates: dataCells, content, and verbatim floors measured live 2026-06-20.
func TestLatticeAccuracyEIA(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/eia-aer-t3-1-2011.pdf")
	if err != nil {
		t.Fatalf("open EIA: %v", err)
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

	// --- OPEN table (fill-banded split via inferFillBandedRows) ---
	// latticeTablesOpen is mandatory: latticeTables (closed-only) does NOT split
	// fill bands, so the EIA table appears as ~33 rows instead of ~45.
	openTables := latticeTablesOpen(c, words, p.MediaBox())
	if len(openTables) == 0 {
		t.Fatalf("latticeTablesOpen found NO table on EIA p1 (edges=%d)", len(mergeEdges(edgesFromContent(c), 3, 3)))
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
	t.Logf("latticeTablesOpen: %d tables on p1; largest=%d cells; reconstructed grid=%dx%d",
		len(openTables), len(largestOpen), openRows, openCols)
	for ri := range openRows {
		c0, c1, c9 := "", "", ""
		if len(grid[ri]) > 0 {
			c0 = grid[ri][0]
		}
		if len(grid[ri]) > 1 {
			c1 = grid[ri][1]
		}
		if len(grid[ri]) > 9 {
			c9 = grid[ri][9]
		}
		t.Logf("  row %2d: c0=%q c1=%q c9=%q", ri, c0, c1, c9)
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
	gdata, err := os.ReadFile("testdata/corpus/tables/eia-aer-t3-1-2011.cellgrid.tsv")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	g, err := parseCellGrid(gdata)
	if err != nil {
		t.Fatalf("parseCellGrid: %v", err)
	}
	t.Logf("golden: %dx%d header_rows=%d", g.rows, g.cols, g.headerRows)

	// Anchor each golden DATA row on col[0] only — the unique year label (1949..2011P).
	// EIA uses a single-column year anchor (not a multi-column acronym like EPA).
	findRow := func(year string) int {
		want := looseCell(year)
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
		year := gd[0]
		rr := findRow(year)
		if rr < 0 {
			missingRows++
			total += g.cols
			t.Logf("MISSING data row year=%q (all %d cols miss)", year, g.cols)
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
			t.Logf("row year=%-8q content=%2d/%d  GOT=%v", year, rowContent, g.cols, strings.Join(gotRow, "|"))
		}
	}
	dataCells := total
	t.Logf("=== EIA DATA-CELL accuracy (43 data rows x %d cols, %d missing rows) ===", g.cols, missingRows)
	t.Logf("CONTENT (space-insensitive) = %d/%d (%.1f%%)  <- the gate metric",
		content, dataCells, 100*float64(content)/float64(dataCells))
	t.Logf("VERBATIM (strict)           = %d/%d (%.1f%%)", verbatim, dataCells, 100*float64(verbatim)/float64(dataCells))

	// --- BLOCKING accuracy gates (floor measured live 2026-06-20) ---
	// Denominator guard: if the golden or parse structure changes, ratios are no longer comparable.
	if dataCells != 430 {
		t.Errorf("EIA denominator drift: got %d data cells, want 430 (43 rows x 10 cols)", dataCells)
	}
	if content < 430 {
		t.Errorf("EIA content regressed: %d/430", content)
	}
	if verbatim < 430 {
		t.Errorf("EIA verbatim regressed: %d/430", verbatim)
	}

	// --- Mechanism-fired positive assertion ---
	// latticeTablesOpen must produce STRICTLY MORE data rows than latticeTables (closed).
	// The fill-banded split (inferFillBandedRows) fires only on the open path.
	// Expected: open ~45 rows vs closed ~33 rows.
	if openRows <= closedRows {
		t.Errorf("EIA fill-banded split did not fire: open=%d closed=%d (expected open > closed)",
			openRows, closedRows)
	} else {
		t.Logf("fill-banded split confirmed: open=%d rows > closed=%d rows", openRows, closedRows)
	}
}
