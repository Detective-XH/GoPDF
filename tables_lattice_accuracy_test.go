package pdf

import (
	"os"
	"strings"
	"testing"
)

// nistAreaGolden — lattice accuracy ground truth, authored BLIND to lattice output
// from the Read-rendered NIST HB44 p.23 "Units of Area" table image + the authoritative
// published NIST Appendix C standard conversion constants (independent of GoPDF extraction).
// 17 rows x 2 cols; row 0 is the spanning title (col1 empty). Value form: AS PRINTED
// (space thousands separators + space-grouped decimals kept; multi-line value cells joined
// top-to-bottom with single spaces; superscripts as U+00B2).
var nistAreaGolden = [][]string{
	{"Units of Area (All underlined figures are exact.)", ""},
	{"1 acre (ac)", "43 560 square feet (exactly) 0.404 685 642 24 hectare (exactly)"},
	{"1 are (a)", "100 square meters (exactly) 119.599 square yards 0.025 acre"},
	{"1 hectare (ha)", "10 000 square meters (exactly) 0.01 square kilometer (exactly) 2.471 acres"},
	{"[1 section (of land)]", "[1 mile square] (approximate)"},
	{"[1 square (building)]", "100 square feet"},
	{"1 square centimeter (cm²)", "0.000 1 square meter (exactly) 0.155 square inch"},
	{"1 square decimeter (dm²)", "0.01 square meter (exactly) 15.500 square inches"},
	{"1 square foot (ft²)", "144 square inches (exactly) 929.030 4 square centimeters (exactly)"},
	{"1 square inch (in²)", "0.006 944 444 square feet 6.451 6 square centimeters (exactly)"},
	{"1 square kilometer (km²)", "1 000 000 square meters (exactly) 247.104 acres 0.386 square mile"},
	{"1 square meter (m²)", "0.000 001 square kilometer (exactly) 1 000 000 square millimeters (exactly) 1.196 square yards 10.764 square feet"},
	{"1 square mile (mi²)", "2.589 99 square kilometers 258.999 hectares"},
	{"1 square millimeter (mm²)", "0.000 001 square meter (exactly) 0.002 square inch"},
	{"1 square rod (rd²), square pole, or square perch", "25.292 852 64 square meters (exactly)"},
	{"1 square yard (yd²)", "0.836 127 36 square meter (exactly) 9 square feet (exactly) 1 296 square inches (exactly)"},
	{"[1 township]", "[6 miles square] (approximate) [36 sections (of land)] 36 square miles (approximate)"},
}

func normCell(s string) string {
	s = strings.ReplaceAll(s, "²", "2") // fold superscript-two
	return strings.Join(strings.Fields(s), " ")
}

// looseCell strips ALL whitespace and folds superscript-two — isolates "right content in the
// right cell" (the lattice + assignment result) from spacing/superscript text-extraction
// quirks (GoPDF renders a superscript "2" as a spaced separate token " 2 ").
func looseCell(s string) string {
	s = strings.ReplaceAll(s, "²", "2")
	return strings.ReplaceAll(s, " ", "")
}

func TestLatticeAccuracyNISTArea(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/nist-hb44-appc-2026.pdf")
	if err != nil {
		t.Fatalf("open NIST: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	p := r.Page(23)
	c := p.Content()
	words, err := p.Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}

	tables := latticeTables(c)
	if len(tables) == 0 {
		t.Fatalf("lattice found NO table on NIST p23 (edges=%d)", len(mergeEdges(edgesFromContent(c), 3, 3)))
	}
	// pick the largest table by cell count (the Units of Area table)
	largest := tables[0]
	for _, tb := range tables[1:] {
		if len(tb) > len(largest) {
			largest = tb
		}
	}
	t.Logf("lattice: %d tables on p23; largest=%d cells", len(tables), len(largest))

	grid := reconstructGrid(largest, words)
	nRows := len(grid)
	nCols := 0
	if nRows > 0 {
		nCols = len(grid[0])
	}

	// --- Tier 1: shape ---
	t.Logf("SHAPE: lattice=%dx%d  golden=%dx%d", nRows, nCols, len(nistAreaGolden), 2)
	shapeOK := nRows == len(nistAreaGolden) && nCols == 2
	if !shapeOK {
		t.Logf("SHAPE MISMATCH — lattice over/under-segmented the grid (headline finding)")
	}

	// --- dump reconstructed grid vs golden for diagnosis ---
	for ri := 0; ri < nRows; ri++ {
		for ci := 0; ci < nCols; ci++ {
			var want string
			if ri < len(nistAreaGolden) && ci < 2 {
				want = nistAreaGolden[ri][ci]
			}
			mark := "  "
			if normCell(grid[ri][ci]) == normCell(want) {
				mark = "ok"
			}
			t.Logf("[%s] r%dc%d GOT=%q WANT=%q", mark, ri, ci, grid[ri][ci], want)
		}
	}

	// --- Tier 2 (verbatim) + Tier 3 (normalized), over the overlapping region ---
	var verbatim, normalized, loose, total int
	rmax := nRows
	if len(nistAreaGolden) < rmax {
		rmax = len(nistAreaGolden)
	}
	for ri := 0; ri < rmax; ri++ {
		cmax := nCols
		if 2 < cmax {
			cmax = 2
		}
		for ci := 0; ci < cmax; ci++ {
			total++
			if grid[ri][ci] == nistAreaGolden[ri][ci] {
				verbatim++
			}
			if normCell(grid[ri][ci]) == normCell(nistAreaGolden[ri][ci]) {
				normalized++
			}
			if looseCell(grid[ri][ci]) == looseCell(nistAreaGolden[ri][ci]) {
				loose++
			}
		}
	}
	t.Logf("CELL-EXACT verbatim          = %d/%d (%.1f%%)", verbatim, total, 100*float64(verbatim)/float64(total))
	t.Logf("CELL-EXACT normalized(ws+sup)= %d/%d (%.1f%%)", normalized, total, 100*float64(normalized)/float64(total))
	t.Logf("CONTENT space-insensitive    = %d/%d (%.1f%%)  <- lattice+assignment correctness", loose, total, 100*float64(loose)/float64(total))
	t.Logf("(in-tree / necessary-not-sufficient; NIST is a genuine full lattice; stroked-carrier" +
		" end-to-end is IRS p55b — but IRS p55b is semi-bordered, see findings)")

	// --- Task C: open-recovery dual-run — HARD FP REGRESSION GATE ---
	//
	// The structural gate requires h-rule overhang
	// > overhangTol=6 pt past vMin/vMax. NIST p23's min h-edge x0=78.5, vMin=79.51 → gap=~1 pt
	// < 6 pt → no admitted bands → no phantom left column. This is now a hard t.Errorf.
	openTablesNIST := latticeTablesOpen(c, words, p.MediaBox())
	if len(openTablesNIST) == 0 {
		t.Logf("NIST p23 open: latticeTablesOpen found NO tables (unexpected)")
	} else {
		largestOpenNIST := openTablesNIST[0]
		for _, tb := range openTablesNIST[1:] {
			if len(tb) > len(largestOpenNIST) {
				largestOpenNIST = tb
			}
		}
		gridOpen := reconstructGrid(largestOpenNIST, words)
		openR := len(gridOpen)
		openC := 0
		if openR > 0 {
			openC = len(gridOpen[0])
		}
		t.Logf("NIST p23 dual-run: open grid=%dx%d  closed grid=%dx%d", openR, openC, nRows, nCols)

		identical := true
		if openR != nRows {
			identical = false
			t.Errorf("NIST p23 dual-run: row count mismatch: open=%d closed=%d (want identical)", openR, nRows)
		}
		for ri := 0; ri < openR && ri < nRows; ri++ {
			if len(gridOpen[ri]) != len(grid[ri]) {
				identical = false
				t.Errorf("NIST p23 dual-run: col count mismatch at row %d: open=%d closed=%d (no phantom column allowed)", ri, len(gridOpen[ri]), len(grid[ri]))
				break
			}
			for ci := range gridOpen[ri] {
				if gridOpen[ri][ci] != grid[ri][ci] {
					identical = false
					t.Errorf("NIST p23 dual-run: cell [%d][%d] open=%q closed=%q", ri, ci, gridOpen[ri][ci], grid[ri][ci])
				}
			}
		}
		if identical {
			t.Logf("NIST p23 dual-run identical: YES (FP regression gate: PASS)")
		} else {
			t.Logf("NIST p23 dual-run identical: NO (FP regression gate: FAIL)")
		}
	}
}

// TestLatticeSpotCheckNISTp15 is a diagnostic-only test (never t.Errorf) that checks
// whether the lattice resolves the documented DENSE NUMERIC FRAGMENTATION cases from
// NIST HB44 page 15 (Minims/Fluid Drams conversion matrix) as clean single cells.
// See TABLES-CELLSEG-MEASURE §1 for the motivating failure description.
func TestLatticeSpotCheckNISTp15(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/nist-hb44-appc-2026.pdf")
	if err != nil {
		t.Fatalf("open NIST: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	p := r.Page(15)
	c := p.Content()
	words, err := p.Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}

	tables := latticeTables(c)
	if len(tables) == 0 {
		t.Logf("MISS: lattice found NO table on NIST p15 (no edges?)")
		return
	}

	// pick the largest table by cell count
	largest := tables[0]
	for _, tb := range tables[1:] {
		if len(tb) > len(largest) {
			largest = tb
		}
	}

	grid := reconstructGrid(largest, words)
	nRows := len(grid)
	nCols := 0
	if nRows > 0 {
		nCols = len(grid[0])
	}
	t.Logf("dims: %dx%d  largest-table cells=%d", nRows, nCols, len(largest))

	targets := []string{
		"0.016 666 67",
		"0.002 083 333",
		"1 fluid dram (fl dr)",
		"1 fluid ounce (fl oz)",
		"1 minim",
	}

	for _, target := range targets {
		targetLoose := looseCell(target)
		// Extract the first "token" of target for contains-diagnostics
		targetFields := strings.Fields(target)
		firstToken := ""
		if len(targetFields) > 0 {
			firstToken = targetFields[0]
		}

		found := false
		foundR, foundC := -1, -1
		for ri := range grid {
			for ci := range grid[ri] {
				if looseCell(grid[ri][ci]) == targetLoose {
					found = true
					foundR, foundC = ri, ci
					break
				}
			}
			if found {
				break
			}
		}

		if found {
			t.Logf("CLEAN: %q -> r%dc%d", target, foundR, foundC)
		} else {
			t.Logf("MISS: %q", target)
			// Log up to 3 cells whose looseCell contains the first token
			count := 0
			for ri := range grid {
				for ci := range grid[ri] {
					cell := grid[ri][ci]
					if strings.Contains(looseCell(cell), looseCell(firstToken)) && count < 3 {
						t.Logf("  contains-diag r%dc%d: %q", ri, ci, cell)
						count++
					}
				}
			}
		}
	}

	// Second pass: confirm row-label cells include the "=" separator as a clean single cell.
	labelTargetsWithEq := []string{
		"1 minim =",
		"1 fluid dram (fl dr) =",
		"1 fluid ounce (fl oz) =",
	}

	for _, target := range labelTargetsWithEq {
		targetLoose := looseCell(target)
		found := false
		foundR, foundC := -1, -1
		for ri := range grid {
			for ci := range grid[ri] {
				if looseCell(grid[ri][ci]) == targetLoose {
					found = true
					foundR, foundC = ri, ci
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			t.Logf("CLEAN-EQ: %q -> r%dc%d", target, foundR, foundC)
		} else {
			t.Logf("MISS-EQ: %q", target)
		}
	}
}
