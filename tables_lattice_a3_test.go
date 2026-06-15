// Package pdf — A3 measure-first diagnostic test for Page.Tables().
//
// TestPageTablesA3MeasureFirst is a DIAGNOSTIC-ONLY measurement; it NEVER
// calls t.Errorf or t.Fatalf for accuracy. t.Fatalf is used ONLY for setup
// failures (file open / NewReader / parseCellGrid errors).
//
// Purpose: empirically score Page.Tables() on two corpus fixtures whose
// .cellgrid.tsv ground-truth has never been scored against any extractor,
// beyond the three tuned fixtures (EPA eGrid, IRS SOI, NIST HB44).
// Results are reported via t.Logf only and inform future gate decisions.
package pdf

import (
	"os"
	"testing"
)

// a3Fixture describes one PDF+TSV pair for the A3 measurement.
type a3Fixture struct {
	name string
	pdf  string
	tsv  string
	page int
}

func TestPageTablesA3MeasureFirst(t *testing.T) {
	fixtures := []a3Fixture{
		{
			name: "eia-aer-t3-1-2011",
			pdf:  "testdata/corpus/tables/eia-aer-t3-1-2011.pdf",
			tsv:  "testdata/corpus/tables/eia-aer-t3-1-2011.cellgrid.tsv",
			page: 1,
		},
		{
			name: "irs-db-t4-3-2025",
			pdf:  "testdata/corpus/tables/irs-db-t4-3-2025.pdf",
			tsv:  "testdata/corpus/tables/irs-db-t4-3-2025.cellgrid.tsv",
			page: 1,
		},
	}

	for i := range fixtures {
		fx := fixtures[i]
		t.Run(fx.name, func(t *testing.T) {
			scoreA3Fixture(t, fx)
		})
	}
}

// scoreA3Fixture performs the full A3 measurement for one fixture.
// Kept in a helper to stay within gocyclo threshold 15.
func scoreA3Fixture(t *testing.T, fx a3Fixture) {
	t.Helper()

	// --- Setup ---
	f, err := os.Open(fx.pdf)
	if err != nil {
		t.Fatalf("open %s: %v", fx.pdf, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", fx.pdf, err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader %s: %v", fx.pdf, err)
	}
	p := r.Page(fx.page)

	// --- WHY-ZERO INSTRUMENTATION (white-box) ---
	c := p.Content()
	edges := mergeEdges(edgesFromContent(c), 3, 3)
	t.Logf("edges (merged): %d", len(edges))

	closed := latticeTables(c)
	t.Logf("closed tables: %d", len(closed))
	largestClosedCells := 0
	for _, tb := range closed {
		if len(tb) > largestClosedCells {
			largestClosedCells = len(tb)
		}
	}
	t.Logf("largest closed table cells: %d", largestClosedCells)

	// --- PUBLIC SCORE ---
	tables, err := p.Tables()
	if err != nil {
		t.Fatalf("Tables(): %v", err)
	}
	t.Logf("public Tables(): %d table(s)", len(tables))

	// Pick the largest Table by total cell count.
	var grid [][]string
	largestPublicCells := 0
	for _, tb := range tables {
		count := 0
		for _, row := range tb.Cells {
			count += len(row)
		}
		if count > largestPublicCells {
			largestPublicCells = count
			grid = tb.Cells
		}
	}

	pubRows := len(grid)
	pubCols := 0
	if pubRows > 0 {
		pubCols = len(grid[0])
	}
	t.Logf("public grid dims: %dx%d", pubRows, pubCols)

	// --- Load golden ---
	tsvData, err := os.ReadFile(fx.tsv)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", fx.tsv, err)
	}
	g, err := parseCellGrid(tsvData)
	if err != nil {
		t.Fatalf("parseCellGrid %s: %v", fx.tsv, err)
	}
	t.Logf("golden dims: %dx%d  header_rows=%d", g.rows, g.cols, g.headerRows)

	// --- findRow: anchor a golden data row on its col0 value ---
	// Scans ALL columns of every grid row for a looseIRS match of the golden col0.
	findRow := func(col0 string) int {
		want := looseIRS(col0)
		if want == "" {
			return -1
		}
		for ri := range grid {
			for ci := range grid[ri] {
				if looseIRS(grid[ri][ci]) == want {
					return ri
				}
			}
		}
		return -1
	}

	// --- SCORE: iterate golden data rows ---
	var verbatim, content, anyCol, total, missingRows int

	for gr := g.headerRows; gr < g.rows; gr++ {
		rr := findRow(g.cells[gr][0])
		if rr < 0 {
			missingRows++
			total += g.cols
			continue
		}
		for ci := 0; ci < g.cols; ci++ {
			total++
			var got string
			if ci < len(grid[rr]) {
				got = grid[rr][ci]
			}
			golden := g.cells[gr][ci]

			if got == golden {
				verbatim++
			}
			if looseIRS(got) == looseIRS(golden) {
				content++
			}
			// any-column content: truth signal for spanning-header misalignment.
			// Skip golden cells whose looseIRS is "" so empty cells don't inflate.
			if looseIRS(golden) != "" {
				for _, cell := range grid[rr] {
					if looseIRS(cell) == looseIRS(golden) {
						anyCol++
						break
					}
				}
			}
		}
	}

	// --- Summary ---
	t.Logf("=== A3 summary: %s ===", fx.name)
	t.Logf("  edges (merged)      : %d", len(edges))
	t.Logf("  closed tables       : %d", len(closed))
	t.Logf("  public tables       : %d", len(tables))
	t.Logf("  public dims         : %dx%d", pubRows, pubCols)
	t.Logf("  golden dims         : %dx%d  header_rows=%d", g.rows, g.cols, g.headerRows)
	t.Logf("  missing rows        : %d", missingRows)
	t.Logf("  VERBATIM positional : %d/%d (%.1f%%)", verbatim, total, pct(verbatim, total))
	t.Logf("  CONTENT  positional : %d/%d (%.1f%%)", content, total, pct(content, total))
	t.Logf("  CONTENT  any-column : %d/%d (%.1f%%)", anyCol, total, pct(anyCol, total))
}
