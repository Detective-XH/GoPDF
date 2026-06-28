package pdf

// tables_lattice_headerruled_test.go — content-validation for inferHeaderRuledDataCells, the
// "headers-only / partial-table-miss" recovery: a closed lattice whose header is ruled into
// columns while the data body is ruled only down the label column (col0). Fixtures are built
// directly as []lCell + []Word (TOP-ORIGIN, the wordAtBand convention from
// tables_lattice_opensynth_test.go), so they validate the synthesis algorithm decoupled from any
// PDF/font parsing. Mirrors the th-nso-yearbook quarterly geometry (census T080).

import "testing"

// headerRuledFixture builds a headers-only table: a 4-cell header row (label + 3 data columns)
// over a data body ruled only down the label column. The data values exist as words but fall
// outside every closed cell, so without recovery they are dropped from the grid.
func headerRuledFixture() (cells []lCell, words []Word) {
	// Header column-defining row (top band [10,20]): label + 3 data columns.
	cells = append(cells,
		lCell{x0: 0, top: 10, x1: 50, bottom: 20},   // label column
		lCell{x0: 50, top: 10, x1: 100, bottom: 20}, // data col A
		lCell{x0: 100, top: 10, x1: 150, bottom: 20},
		lCell{x0: 150, top: 10, x1: 200, bottom: 20},
	)
	// Data rows: col0-only cells (the row-label column) at three bands below the header.
	dataBands := [][2]float64{{30, 40}, {50, 60}, {70, 80}}
	for _, b := range dataBands {
		cells = append(cells, lCell{x0: 0, top: b[0], x1: 50, bottom: b[1]})
	}
	// Header label words.
	words = append(words,
		wordAtBand("Region", 5, 30, 10, 20),
		wordAtBand("A", 70, 10, 10, 20),
		wordAtBand("B", 120, 10, 10, 20),
		wordAtBand("C", 170, 10, 10, 20),
	)
	// Row labels (land in the existing col0 cells).
	labels := []string{"North", "South", "East"}
	// Data values per row × column (ax = x+w/2 must fall in the header column x-range).
	colA := []string{"1.1", "2.2", "3.3"}
	colB := []string{"4.4", "", "6.6"} // South colB intentionally EMPTY (0-FP sparse check)
	colC := []string{"7.7", "8.8", "9.9"}
	for r, b := range dataBands {
		words = append(words, wordAtBand(labels[r], 5, 30, b[0], b[1])) // ax=20 → col0
		words = append(words, wordAtBand(colA[r], 70, 10, b[0], b[1]))  // ax=75 → col A
		if colB[r] != "" {
			words = append(words, wordAtBand(colB[r], 120, 10, b[0], b[1])) // ax=125 → col B
		}
		words = append(words, wordAtBand(colC[r], 170, 10, b[0], b[1])) // ax=175 → col C
	}
	return cells, words
}

// TestInferHeaderRuledDataCellsRecovers is the headline check: the missing data cells are
// synthesized, every data value lands in its header-defined column, and the EMPTY South/colB
// cell stays empty (no fabricated phantom — synthesis only where a word sits).
func TestInferHeaderRuledDataCellsRecovers(t *testing.T) {
	cells, words := headerRuledFixture()
	synth := inferHeaderRuledDataCells(cells, words)
	// 3 rows × 3 data columns − 1 empty (South/colB) = 8 synthesized cells.
	if len(synth) != 8 {
		t.Fatalf("synthesized cells: got %d, want 8 (3x3 data minus 1 empty)", len(synth))
	}
	grid := reconstructGrid(append(cells, synth...), words)
	if len(grid) != 4 { // 1 header band + 3 data bands
		t.Fatalf("rows: got %d, want 4\ngrid=%v", len(grid), grid)
	}
	if len(grid[0]) != 4 { // label + 3 data columns
		t.Fatalf("cols: got %d, want 4\ngrid=%v", len(grid[0]), grid)
	}
	// Data rows are grid[1..3]; col0 holds the row label, cols 1..3 the recovered values.
	wantCol0 := []string{"North", "South", "East"}
	wantColA := []string{"1.1", "2.2", "3.3"}
	wantColB := []string{"4.4", "", "6.6"}
	wantColC := []string{"7.7", "8.8", "9.9"}
	for i := range 3 {
		row := grid[i+1]
		if row[0] != wantCol0[i] {
			t.Errorf("row %d col0 = %q, want %q", i, row[0], wantCol0[i])
		}
		if row[1] != wantColA[i] {
			t.Errorf("row %d colA = %q, want %q", i, row[1], wantColA[i])
		}
		if row[2] != wantColB[i] {
			t.Errorf("row %d colB = %q, want %q (empty cell must NOT be fabricated)", i, row[2], wantColB[i])
		}
		if row[3] != wantColC[i] {
			t.Errorf("row %d colC = %q, want %q", i, row[3], wantColC[i])
		}
	}
}

// TestInferHeaderRuledDataCellsIgnoresOutsideText asserts the 0-FP envelope guard (codex
// adversarial finding): a stray non-table word in a data-row band but OUTSIDE the header column
// x-range (e.g. right-margin text / a neighbouring table) is NOT pulled into a synthesized cell.
func TestInferHeaderRuledDataCellsIgnoresOutsideText(t *testing.T) {
	cells, words := headerRuledFixture()
	// Stray word at ax=250 — right of the last header column (150..200) — in the first data band.
	stray := wordAtBand("STRAY", 245, 10, 30, 40) // ax=250 > vMax(200)
	words = append(words, stray)
	synth := inferHeaderRuledDataCells(cells, words)
	if len(synth) != 8 {
		t.Fatalf("synthesized cells: got %d, want 8 (stray out-of-envelope word must not add a cell)", len(synth))
	}
	grid := reconstructGrid(append(cells, synth...), words)
	for _, row := range grid {
		for c, v := range row {
			if v == "STRAY" {
				t.Errorf("stray out-of-envelope word was pulled into grid col %d", c)
			}
		}
	}
}

// TestInferHeaderRuledDataCellsRejectsLoneInEnvelopeWord asserts the column-cluster guard (codex
// adversarial round 2): a lone unplaced word geometrically INSIDE a (header-col × col0-band) box —
// indistinguishable from a single real value (a footnote/overprint word in the data rectangle) — is
// NOT promoted to a cell, while a genuine column that recurs across >=2 rows IS recovered.
func TestInferHeaderRuledDataCellsRejectsLoneInEnvelopeWord(t *testing.T) {
	var cells []lCell
	// 3-cell header: label[0..50] + colA[50..100] + colB[100..150].
	for _, x := range [][2]float64{{0, 50}, {50, 100}, {100, 150}} {
		cells = append(cells, lCell{x0: x[0], top: 10, x1: x[1], bottom: 20})
	}
	// 3 data rows, col0-only.
	dataBands := [][2]float64{{30, 40}, {50, 60}, {70, 80}}
	for _, b := range dataBands {
		cells = append(cells, lCell{x0: 0, top: b[0], x1: 50, bottom: b[1]})
	}
	var words []Word
	for r, b := range dataBands {
		words = append(words, wordAtBand("row", 5, 30, b[0], b[1])) // col0 label
		words = append(words, wordAtBand("a", 70, 10, b[0], b[1]))  // colA: all 3 bands → real column
		if r == 0 {
			words = append(words, wordAtBand("LONE", 120, 10, b[0], b[1])) // colB: only band 0 → lone stray
		}
	}
	synth := inferHeaderRuledDataCells(cells, words)
	// colA recovers 3 cells; the lone colB word recovers 0 (cluster < 2 bands).
	if len(synth) != 3 {
		t.Fatalf("synthesized cells: got %d, want 3 (colA only; lone colB word rejected)", len(synth))
	}
	grid := reconstructGrid(append(cells, synth...), words)
	for _, row := range grid {
		for c, v := range row {
			if v == "LONE" {
				t.Errorf("lone in-envelope word was promoted to grid col %d (cluster guard failed)", c)
			}
		}
	}
}

// TestInferHeaderRuledDataCellsLeavesNormalTable asserts the 0-FP guard: a normally-ruled table
// (every data row already carries its own column cells) is NOT touched — distinctCols(dataCells)
// != 1 short-circuits the recovery, so no phantom cells are added.
func TestInferHeaderRuledDataCellsLeavesNormalTable(t *testing.T) {
	var cells []lCell
	// Header row + two data rows, each fully ruled into 4 columns.
	bands := [][2]float64{{10, 20}, {30, 40}, {50, 60}}
	xs := [][2]float64{{0, 50}, {50, 100}, {100, 150}, {150, 200}}
	for _, b := range bands {
		for _, x := range xs {
			cells = append(cells, lCell{x0: x[0], top: b[0], x1: x[1], bottom: b[1]})
		}
	}
	if synth := inferHeaderRuledDataCells(cells, nil); synth != nil {
		t.Errorf("normally-ruled table: got %d synthesized cells, want 0 (must not fire)", len(synth))
	}
}

// TestInferHeaderRuledDataCellsRequiresMultiColHeader asserts the >=3-column header guard: a
// 2-column header (label + 1) never engages the recovery.
func TestInferHeaderRuledDataCellsRequiresMultiColHeader(t *testing.T) {
	cells := []lCell{
		{x0: 0, top: 10, x1: 50, bottom: 20},   // header label
		{x0: 50, top: 10, x1: 100, bottom: 20}, // header col (only one)
		{x0: 0, top: 30, x1: 50, bottom: 40},   // data col0
		{x0: 0, top: 50, x1: 50, bottom: 60},
	}
	words := []Word{wordAtBand("x", 70, 10, 30, 40)} // a value to the right of col0
	if synth := inferHeaderRuledDataCells(cells, words); synth != nil {
		t.Errorf("2-column header: got %d synthesized cells, want 0 (must not fire)", len(synth))
	}
}
