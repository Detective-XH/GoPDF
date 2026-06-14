package pdf

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
)

// looseIRS is looseCell plus stripping U+FFFD — the IRS data fonts append zero-width
// glyphs whose own ToUnicode maps them to U+FFFD (source-marked "no meaning"); they are
// visual noise, so the cell-CONTENT comparison ignores them. Reported separately.
func looseIRS(s string) string {
	return strings.ReplaceAll(looseCell(s), "�", "")
}

// irsOpenPage returns the Content, Words, and MediaBox for IRS p1 (used by open scorer).
func irsOpenPage(t *testing.T) (Content, []Word, [4]float64) {
	f, err := os.Open("testdata/corpus/tables/irs-soi-inpre-t1-2022.pdf")
	if err != nil {
		t.Fatalf("open IRS (open): %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, _ := f.Stat()
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader (open): %v", err)
	}
	p := r.Page(1)
	c := p.Content()
	words, err := p.Words()
	if err != nil {
		t.Fatalf("Words (open): %v", err)
	}
	return c, words, p.MediaBox()
}

// TestLatticeAccuracyIRS scores the B2 lattice + open-column recovery on the held-out
// RECT-bordered, split-column IRS SOI Table 1 page-face against its companion+render-
// verified golden.  Uses a GEOMETRY-DERIVED column map (not hard-coded identity) so
// recovered vs dropped columns are reflected correctly in per-golden-col tally.
// Diagnostic-only (t.Logf throughout), except a hard t.Errorf if interior cols 1-4
// regress below 128/128 (that would be a functional regression against the closed lattice).
func TestLatticeAccuracyIRS(t *testing.T) {
	c, words, media := irsOpenPage(t)

	// --- CLOSED table (4 interior columns only) ---
	closedTables := latticeTables(c)
	if len(closedTables) == 0 {
		t.Fatalf("closed lattice found NO table on IRS p1")
	}
	largestClosed := closedTables[0]
	for _, tb := range closedTables[1:] {
		if len(tb) > len(largestClosed) {
			largestClosed = tb
		}
	}

	// --- OPEN table (closed + recovered edge columns) ---
	openTables := latticeTablesOpen(c, words, media)
	if len(openTables) == 0 {
		t.Fatalf("open lattice found NO table on IRS p1")
	}
	largestOpen := openTables[0]
	for _, tb := range openTables[1:] {
		if len(tb) > len(largestOpen) {
			largestOpen = tb
		}
	}

	grid := reconstructGrid(largestOpen, words)
	rR := len(grid)
	cR := 0
	if rR > 0 {
		cR = len(grid[0])
	}
	t.Logf("open lattice: %d tables; open grid = %dx%d (golden is 51x6)", len(openTables), rR, cR)
	t.Logf("closed lattice: %d tables; closed cells = %d; open cells = %d",
		len(closedTables), len(largestClosed), len(largestOpen))

	// --- Load golden ---
	gdata, err := os.ReadFile("testdata/corpus/tables/irs-soi-inpre-t1-2022.cellgrid.tsv")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	g, err := parseCellGrid(gdata)
	if err != nil {
		t.Fatalf("parseCellGrid: %v", err)
	}

	// --- Build GEOMETRY-DERIVED goldToRecon map ---
	//
	// fullReps: sorted cluster representatives of ALL open-cell x0s.
	//   grid column j <-> fullReps[j]  (same tol=4 used by reconstructGrid).
	// closedReps: sorted cluster representatives of closed-cell x0s.
	//   These represent the 4 interior numeric columns (golden cols 1-4) in x-order.
	//
	// vMin, vMax: the inner bounds of the closed lattice, used to locate the two
	//   open edge columns in fullReps.

	openX0s := make([]float64, 0, len(largestOpen))
	for _, lc := range largestOpen {
		openX0s = append(openX0s, lc.x0)
	}
	fullReps := cluster1D(openX0s, 4)
	sort.Float64s(fullReps)

	closedX0s := make([]float64, 0, len(largestClosed))
	for _, lc := range largestClosed {
		closedX0s = append(closedX0s, lc.x0)
	}
	closedReps := cluster1D(closedX0s, 4)
	sort.Float64s(closedReps)

	// Compute vMin / vMax from closed cells.
	vMin, vMax := colBounds(largestClosed)

	t.Logf("fullReps  = %v", fullReps)
	t.Logf("closedReps= %v", closedReps)
	t.Logf("vMin=%.2f  vMax=%.2f", vMin, vMax)

	// Count col5 bands admitted: open cells with x0 ≈ vMax (within tol=4).
	// Each admitted right-band emits exactly one cell at x0=vMax; counting them
	// confirms all 32 numeric row bands were structurally admitted (plan §2.1 concern C).
	col5AdmittedBands := 0
	for _, lc := range largestOpen {
		if math.Abs(lc.x0-vMax) <= 4 {
			col5AdmittedBands++
		}
	}
	t.Logf("col5 bands admitted by structural gate: %d (all right-side bands; 32 of these are the numeric-row bands scored above)", col5AdmittedBands)

	if len(closedReps) < 4 {
		t.Fatalf("expected >=4 closed columns, got %d (closedReps=%v)", len(closedReps), closedReps)
	}

	goldToRecon := map[int]int{}

	// Interior golden cols 1-4: map to nearest fullReps entry from each closedRep.
	for gc := 1; gc <= 4; gc++ {
		idx := nearestIdx(fullReps, closedReps[gc-1])
		goldToRecon[gc] = idx
	}

	// col5 (right open edge): open cell x0 = vMax. Guard: only map if a fullReps entry
	// is within 4pt of vMax (i.e. col5 was actually recovered).
	col5idx := nearestIdx(fullReps, vMax)
	if math.Abs(fullReps[col5idx]-vMax) <= 4 {
		goldToRecon[5] = col5idx
		t.Logf("col5 RECOVERED: fullReps[%d]=%.2f vMax=%.2f", col5idx, fullReps[col5idx], vMax)
	} else {
		goldToRecon[5] = -1
		t.Logf("col5 NOT recovered: nearest fullReps[%d]=%.2f dist=%.2f from vMax=%.2f",
			col5idx, fullReps[col5idx], math.Abs(fullReps[col5idx]-vMax), vMax)
	}

	// col0 (left open edge): open cell x0 < vMin. A recovered left column has a
	// fullReps entry clearly to the left of vMin (gap > 4pt).
	col0Recon := -1
	if len(fullReps) > 0 && fullReps[0] < vMin-4 {
		col0Recon = 0 // leftmost column in fullReps
		t.Logf("col0 RECOVERED: fullReps[0]=%.2f vMin=%.2f (gap=%.2f)",
			fullReps[0], vMin, vMin-fullReps[0])
	} else {
		t.Logf("col0 NOT recovered (fullReps[0]=%.2f vMin=%.2f)", fullReps[0], vMin)
	}

	// Log final map (sorted keys).
	mapKeys := []int{1, 2, 3, 4, 5}
	mapParts := make([]string, 0, len(mapKeys))
	for _, k := range mapKeys {
		mapParts = append(mapParts, fmt.Sprintf("%d->%d", k, goldToRecon[k]))
	}
	t.Logf("goldToRecon = {%s}  col0Recon=%d", strings.Join(mapParts, " "), col0Recon)

	// --- Anchor rows by TY2021 value (golden col1), scanning all recon columns ---
	findRow := func(ty2021 string) int {
		want := looseIRS(ty2021)
		for ri := range grid {
			for ci := range grid[ri] {
				if looseIRS(grid[ri][ci]) == want {
					return ri
				}
			}
		}
		return -1
	}

	// --- Score 160-cell gate: 32 numeric rows x golden cols 1..5 ---
	var (
		gateContent, gateVerbatim int // full 160-cell totals
		col5Content, col5Verbatim int // col5-only tally (32 cells)
		col14Content              int // interior cols 1-4 only (128 cells)
		gateTotal                 int
		col5Total                 int
		col14Total                int
		missingRows               int
		numericRows               int
	)

	for gr := g.headerRows; gr < g.rows; gr++ {
		gd := g.cells[gr]
		if strings.TrimSpace(gd[1]) == "" { // section-label row: skip
			continue
		}
		numericRows++
		rr := findRow(gd[1])
		if rr < 0 {
			missingRows++
			t.Logf("MISSING numeric row TY2021=%q (label=%q)", gd[1], gd[0])
			continue
		}
		var rowMiss []string
		for gc := 1; gc <= 5; gc++ {
			rc := goldToRecon[gc]
			gateTotal++
			if gc == 5 {
				col5Total++
			} else {
				col14Total++
			}
			if rc < 0 {
				// not recovered
				rowMiss = append(rowMiss, fmt.Sprintf("gc%d=DROPPED", gc))
				continue
			}
			var got string
			if rc < len(grid[rr]) {
				got = grid[rr][rc]
			}
			if got == gd[gc] {
				gateVerbatim++
				if gc == 5 {
					col5Verbatim++
				}
			}
			if looseIRS(got) == looseIRS(gd[gc]) {
				gateContent++
				if gc == 5 {
					col5Content++
				} else {
					col14Content++
				}
			} else {
				rowMiss = append(rowMiss, fmt.Sprintf("gc%d: %q!=%q", gc, gd[gc], got))
			}
		}
		if len(rowMiss) > 0 {
			t.Logf("row TY2021=%-12q miss: %s", gd[1], strings.Join(rowMiss, "  "))
		}
	}

	// --- col0 informational (labels, all rows including section-label rows) ---
	var col0Content, col0Verbatim, col0Total int
	if col0Recon >= 0 {
		for gr := g.headerRows; gr < g.rows; gr++ {
			gd := g.cells[gr]
			col0Total++
			if strings.TrimSpace(gd[1]) == "" {
				// section-label row: scan by label text (approximate anchor)
				want := looseIRS(gd[0])
				found := false
				for ri := range grid {
					if col0Recon < len(grid[ri]) {
						if looseIRS(grid[ri][col0Recon]) == want {
							if gd[0] == grid[ri][col0Recon] {
								col0Verbatim++
							}
							col0Content++
							found = true
							break
						}
					}
				}
				if !found {
					t.Logf("col0 INFO: section-label row gr=%d %q not found in open grid", gr, gd[0])
				}
				continue
			}
			// numeric row: anchor by TY2021
			rr := findRow(gd[1])
			if rr < 0 {
				t.Logf("col0 INFO: numeric row gr=%d TY2021=%q not anchored", gr, gd[1])
				continue
			}
			var got string
			if col0Recon < len(grid[rr]) {
				got = grid[rr][col0Recon]
			}
			if got == gd[0] {
				col0Verbatim++
			}
			if looseIRS(got) == looseIRS(gd[0]) {
				col0Content++
			}
		}
	}

	// --- Log summary ---
	t.Logf("=== IRS SOI open-column re-measure (rect-bordered, split-column) ===")
	t.Logf("numeric rows found: %d/%d  missing: %d", numericRows-missingRows, numericRows, missingRows)
	t.Logf("GATE (160-cell = 32 rows x 5 numeric cols):")
	t.Logf("  CONTENT  (looseIRS) = %d/%d (%.1f%%)", gateContent, gateTotal, pct(gateContent, gateTotal))
	t.Logf("  VERBATIM (strict)   = %d/%d (%.1f%%)", gateVerbatim, gateTotal, pct(gateVerbatim, gateTotal))
	t.Logf("col5 ($15,000-$30,000) [THE GATE]:")
	t.Logf("  CONTENT  = %d/%d (%.1f%%)", col5Content, col5Total, pct(col5Content, col5Total))
	t.Logf("  VERBATIM = %d/%d (%.1f%%)", col5Verbatim, col5Total, pct(col5Verbatim, col5Total))
	t.Logf("  bands admitted by structural gate: %d (all row bands; 32 numeric-row bands scored above)", col5AdmittedBands)
	t.Logf("interior cols 1-4 (regression check, must be 128/128):")
	t.Logf("  CONTENT  = %d/%d (%.1f%%)", col14Content, col14Total, pct(col14Content, col14Total))
	t.Logf("col0 (label) INFORMATIONAL, not part of the gate:")
	if col0Recon >= 0 {
		t.Logf("  CONTENT  = %d/%d  VERBATIM = %d/%d", col0Content, col0Total, col0Verbatim, col0Total)
	} else {
		t.Logf("  col0 not recovered (skipped)")
	}

	// Hard regression gate: interior cols 1-4 must not drop.
	if col14Content < 128 {
		t.Errorf("REGRESSION: interior cols 1-4 content = %d/128, want >=128 (was 128/128 in closed lattice)", col14Content)
	}
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}
