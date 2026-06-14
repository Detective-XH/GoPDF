package pdf

// tables_lattice_opensynth_test.go — synthetic unit tests for open edge-column recovery
// with STRUCTURAL EVIDENCE gate.
//
// All fixtures are built directly as []lCell + []lEdge + []Word + media [4]float64.
// No PDF files are opened; recoverOpenColumns and reconstructGrid are called directly.
//
// Coordinate convention (TOP-ORIGIN throughout):
//   Cells: top < bottom (e.g. top=100, bottom=120 means the cell spans that vertical band).
//   Words: w.Y is PDF bottom-origin (positive = up from page bottom), w.H is height.
//         The top-origin anchor is ay = -(w.Y + w.H/2).
//         To place a word anchor at top-origin position T, set: w.Y = -T - w.H/2.
//         For a word of height h in a band [top, bottom], midpoint = (top+bottom)/2:
//           w.Y = -(top+bottom)/2 - h/2   →  ay = (top+bottom)/2  ✓
//
// Closed-cell geometry for most tests:
//   v-rules at x∈{289, 347, 405, 464, 522}
//   h-rules at y∈{100, 120, 140, 160, 180}  (4 row bands)
//   Cells: columns at x0∈{289,347,405,464}, x1∈{347,405,464,522}  → 4×4 = 16 closed cells
//   vMin = 289, vMax = 522

import (
	"math"
	"testing"
)

// makeClosedCells builds the 16-cell 4-row × 4-col closed grid used by most tests.
// x0s and x1s define the column boundaries; rowTops defines the row boundaries.
func makeClosedCells(x0s, x1s, rowTops []float64) []lCell {
	var cells []lCell
	for r := 0; r+1 < len(rowTops); r++ {
		for c := range len(x0s) {
			cells = append(cells, lCell{
				x0:     x0s[c],
				x1:     x1s[c],
				top:    rowTops[r],
				bottom: rowTops[r+1],
			})
		}
	}
	return cells
}

// wordAtBand builds a Word with the given X, W, and a vertical anchor at the midpoint
// of the row band [bandTop, bandBot]. S is the word string.
//
// The anchor formula: ay = -(w.Y + w.H/2) must equal (bandTop+bandBot)/2.
// So: w.Y = -(bandTop+bandBot)/2 - w.H/2.
func wordAtBand(s string, x, w, bandTop, bandBot float64) Word {
	h := 10.0 // arbitrary glyph height; only matters for the anchor formula
	mid := (bandTop + bandBot) / 2
	return Word{
		S: s,
		X: x,
		W: w,
		H: h,
		Y: -mid - h/2,
	}
}

// standardGeom returns the shared closed-cell geometry used by most tests.
func standardGeom() (x0s, x1s, rowTops []float64) {
	x0s = []float64{289, 347, 405, 464}
	x1s = []float64{347, 405, 464, 522}
	rowTops = []float64{100, 120, 140, 160, 180}
	return
}

// makeOverhangingHEdges builds h-edges at each rowTop with the given x0, x1.
// These represent row rules that overhang the specified range.
func makeOverhangingHEdges(rowTops []float64, x0, x1 float64) []lEdge {
	var edges []lEdge
	for _, y := range rowTops {
		edges = append(edges, lEdge{orient: 'h', x0: x0, x1: x1, top: y, bottom: y})
	}
	return edges
}

// TestLatticeOpenRight: closed grid (4 cols, 4 rows), h-edges overhang to x1≈580,
// words at X≈540–565 in all 4 bands → exactly 1 right column emitted.
// (Replaces the prior text-bbox-only TestLatticeOpenRight; now requires structural evidence.)
func TestLatticeOpenRight(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// vMax = 522; h-rules overhang to x1=580 (58 pt right of vMax >> overhangTol=6)
	hEdges := makeOverhangingHEdges(rowTops, 289, 580)
	// words at X=540, W=25 → anchor ax=552.5 > vMax ✓
	words := []Word{
		wordAtBand("495,215", 540, 25, rowTops[0], rowTops[1]),
		wordAtBand("786,764", 540, 25, rowTops[1], rowTops[2]),
		wordAtBand("5,812,502", 540, 25, rowTops[2], rowTops[3]),
		wordAtBand("1,234,567", 540, 25, rowTops[3], rowTops[4]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	if len(extra) == 0 {
		t.Fatalf("recoverOpenColumns: got 0 cells; want right-column cells (4 row bands)")
	}

	// Verify: exactly 4 extra cells (one per row band), all with x0=vMax=522.
	vMax := 522.0
	if len(extra) != 4 {
		t.Errorf("extra cells: got %d; want 4 (one per row band)", len(extra))
	}
	for i, c := range extra {
		if math.Abs(c.x0-vMax) > 0.5 {
			t.Errorf("extra[%d].x0 = %.2f; want %.2f (vMax)", i, c.x0, vMax)
		}
		if c.x1 <= c.x0 {
			t.Errorf("extra[%d]: x1=%.2f <= x0=%.2f (degenerate cell)", i, c.x1, c.x0)
		}
	}

	// Use reconstructGrid to verify words land in the rightmost column.
	allCells := append(cells, extra...)
	grid := reconstructGrid(allCells, words)
	nRows := len(grid)
	if nRows == 0 {
		t.Fatalf("reconstructGrid returned empty grid")
	}
	nCols := len(grid[0])
	// Expect 5 columns: 4 closed + 1 recovered.
	if nCols != 5 {
		t.Errorf("grid columns: got %d; want 5 (4 closed + 1 open right)", nCols)
	}
	// Words should appear in the last column.
	wordStrings := map[string]bool{"495,215": true, "786,764": true, "5,812,502": true, "1,234,567": true}
	for ri := range nRows {
		cell := grid[ri][nCols-1]
		if cell == "" {
			t.Errorf("grid[%d][%d] (right open col): empty; want one of the open-side words", ri, nCols-1)
		} else if !wordStrings[cell] {
			t.Errorf("grid[%d][%d] (right open col): got %q; not a recognized word", ri, nCols-1, cell)
		}
	}
}

// TestLatticeOpenBoth: closed grid + h-edges overhang on BOTH sides + words on both sides.
// Assert: recoverOpenColumns adds two columns (one left, one right).
func TestLatticeOpenBoth(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// vMin=289, vMax=522; h-rules span x0=30 to x1=580 (both sides overhang >> 6 pt)
	hEdges := makeOverhangingHEdges(rowTops, 30, 580)
	// vMin = 289: left words at X=100, W=43 → anchor ax=121.5 < 289 ✓
	// vMax = 522: right words at X=540, W=25 → anchor ax=552.5 > 522 ✓
	words := []Word{
		// Left column — all 4 bands
		wordAtBand("Number of returns", 100, 43, rowTops[0], rowTops[1]),
		wordAtBand("Amount", 100, 43, rowTops[1], rowTops[2]),
		wordAtBand("Net income", 100, 43, rowTops[2], rowTops[3]),
		wordAtBand("Total", 100, 43, rowTops[3], rowTops[4]),
		// Right column — all 4 bands
		wordAtBand("495,215", 540, 25, rowTops[0], rowTops[1]),
		wordAtBand("786,764", 540, 25, rowTops[1], rowTops[2]),
		wordAtBand("5,812,502", 540, 25, rowTops[2], rowTops[3]),
		wordAtBand("1,234,567", 540, 25, rowTops[3], rowTops[4]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)

	// Expect 8 extra cells: 4 left + 4 right.
	if len(extra) != 8 {
		t.Errorf("extra cells: got %d; want 8 (4 left + 4 right)", len(extra))
	}

	// Verify grid has 6 columns.
	allCells := append(cells, extra...)
	grid := reconstructGrid(allCells, words)
	if len(grid) == 0 {
		t.Fatalf("reconstructGrid returned empty grid")
	}
	nCols := len(grid[0])
	if nCols != 6 {
		t.Errorf("grid columns: got %d; want 6 (1 left + 4 closed + 1 right)", nCols)
	}

	// Left column (index 0) and right column (index nCols-1) must be non-empty.
	for ri := range grid {
		if grid[ri][0] == "" {
			t.Errorf("grid[%d][0] (left open col): empty; want a left-column word", ri)
		}
		if grid[ri][nCols-1] == "" {
			t.Errorf("grid[%d][%d] (right open col): empty; want a right-column word", ri, nCols-1)
		}
	}
}

// TestLatticeOpenClosedControl: closed grid, NO words outside [vMin,vMax].
// Assert: recoverOpenColumns returns zero cells (no-op for fully-closed tables).
func TestLatticeOpenClosedControl(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// H-edges only span [289, 522] — no overhang (fully closed)
	hEdges := makeOverhangingHEdges(rowTops, 289, 522)
	// Words inside the closed region: ax∈[289,522] → neither side fires.
	words := []Word{
		wordAtBand("interior", 310, 20, rowTops[0], rowTops[1]),
		wordAtBand("data", 370, 20, rowTops[1], rowTops[2]),
		wordAtBand("values", 420, 20, rowTops[2], rowTops[3]),
		wordAtBand("here", 480, 20, rowTops[3], rowTops[4]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	if len(extra) != 0 {
		t.Errorf("closed control: got %d extra cells; want 0 (no-op for fully-closed table)", len(extra))
	}
}

// TestLatticeOpenSingleRowNote: closed grid + h-edges overhang right + exactly ONE word
// outside vMax, in ONE band. The row-span guard (minOpenRows=2) must reject this.
func TestLatticeOpenSingleRowNote(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// H-edges overhang right (rule evidence present).
	hEdges := makeOverhangingHEdges(rowTops, 289, 580)
	// One word outside vMax, placed at the MIDPOINT of band 0 only.
	words := []Word{
		wordAtBand("footnote", 540, 25, rowTops[0], rowTops[1]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	if len(extra) != 0 {
		t.Errorf("single-row note: got %d extra cells; want 0 (row-span guard must reject)", len(extra))
	}
}

// TestLatticeOpenMediaBoxClamp: right words extend beyond urx. rightExt must be clamped to urx.
func TestLatticeOpenMediaBoxClamp(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// H-edges overhang right past urx=620 (they reach x1=640, but cells will be clamped).
	hEdges := makeOverhangingHEdges(rowTops, 289, 640)
	// Words at X=600, W=30 → maxWordRight=630; urx=620. rightExt clamped to 620.
	words := []Word{
		wordAtBand("far-right-1", 600, 30, rowTops[0], rowTops[1]),
		wordAtBand("far-right-2", 600, 30, rowTops[1], rowTops[2]),
		wordAtBand("far-right-3", 600, 30, rowTops[2], rowTops[3]),
		wordAtBand("far-right-4", 600, 30, rowTops[3], rowTops[4]),
	}
	urx := 620.0
	media := [4]float64{0, 0, urx, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	if len(extra) == 0 {
		t.Fatalf("mediabox-clamp: got 0 extra cells; want 4 (right col should be created)")
	}

	// Every extra cell must have x1 <= urx.
	for i, c := range extra {
		if c.x1 > urx+1e-9 {
			t.Errorf("extra[%d].x1 = %.4f > urx=%.4f (cell extends past page boundary)", i, c.x1, urx)
		}
		if math.Abs(c.x1-urx) > 0.5 {
			t.Errorf("extra[%d].x1 = %.4f; want %.4f (clamped to urx)", i, c.x1, urx)
		}
	}
}

// TestLatticeOpenSidebarNoOverhang (NIST p23 shape): closed cells; words outside vMin
// across many bands BUT h-edges stop at vMin (min h x0 = vMin-1, inside snap band).
// Structural gate must reject: 0 cells emitted.
// This is the regression test for the prior text-bbox-only false-positive.
func TestLatticeOpenSidebarNoOverhang(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// vMin = 289. h-edges reach x0=288 (1 pt left of vMin = snap noise, < overhangTol=6).
	// This mirrors NIST p23: min(x0)=78.5, vMin=79.51, gap=~1pt.
	hEdges := makeOverhangingHEdges(rowTops, 288, 522) // x0=288, NOT overhanging
	// Words outside vMin=289 across all 4 bands.
	words := []Word{
		wordAtBand("sidebar-1", 100, 43, rowTops[0], rowTops[1]),
		wordAtBand("sidebar-2", 100, 43, rowTops[1], rowTops[2]),
		wordAtBand("sidebar-3", 100, 43, rowTops[2], rowTops[3]),
		wordAtBand("sidebar-4", 100, 43, rowTops[3], rowTops[4]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	if len(extra) != 0 {
		t.Errorf("sidebar-no-overhang (NIST shape): got %d extra cells; want 0 (structural gate must reject)", len(extra))
	}
}

// TestLatticeOpenStrayBorderOverhang: only the top+bottom border h-edges overhang to the margin;
// interior row-line h-edges stop at vMin. A 3-band caption of words outside vMin → 0 cells.
// Per-band confirmation fails on interior bands: overhangsLeft(rowTops[i+1]) is false for
// i=0 (middle row top is not a border).
func TestLatticeOpenStrayBorderOverhang(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)

	// Top border (y=rowTops[0]=100) and bottom border (y=rowTops[4]=180) overhang to x0=30.
	// Interior row edges (y=120,140,160) stop at vMin=289 (x0=289, not overhanging).
	var hEdges []lEdge
	for i, y := range rowTops {
		if i == 0 || i == len(rowTops)-1 {
			// Border: overhang left
			hEdges = append(hEdges, lEdge{orient: 'h', x0: 30, x1: 522, top: y, bottom: y})
		} else {
			// Interior: stop at vMin
			hEdges = append(hEdges, lEdge{orient: 'h', x0: 289, x1: 522, top: y, bottom: y})
		}
	}

	// 3-line caption outside vMin in bands 0..3.
	words := []Word{
		wordAtBand("caption-line-1", 100, 43, rowTops[0], rowTops[1]),
		wordAtBand("caption-line-2", 100, 43, rowTops[1], rowTops[2]),
		wordAtBand("caption-line-3", 100, 43, rowTops[2], rowTops[3]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	// Band [100,120]: overhangsLeft(100)=T (border), overhangsLeft(120)=F (interior) → not admitted.
	// Band [120,140]: overhangsLeft(120)=F → not admitted.
	// Band [140,160]: overhangsLeft(140)=F → not admitted.
	// No band admits → 0 cells.
	if len(extra) != 0 {
		t.Errorf("stray-border-overhang: got %d extra cells; want 0 (interior bands fail per-band check)", len(extra))
	}
}

// TestLatticeOpenSnapNoise: h-edge x0 = vMin-(overhangTol-0.5) = 289-5.5 = 283.5
// This is just inside the snap band but < overhangTol from vMin → rejected.
// overhangTol=6: need x0 ≤ vMin-6=283 to qualify. x0=283.5 > 283 → does NOT qualify.
func TestLatticeOpenSnapNoise(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// vMin=289, overhangTol=6 → threshold = 289-6 = 283.
	// x0=283.5: does NOT overhang (283.5 > 283).
	hEdges := makeOverhangingHEdges(rowTops, 283.5, 522)
	words := []Word{
		wordAtBand("noise-1", 100, 43, rowTops[0], rowTops[1]),
		wordAtBand("noise-2", 100, 43, rowTops[1], rowTops[2]),
		wordAtBand("noise-3", 100, 43, rowTops[2], rowTops[3]),
		wordAtBand("noise-4", 100, 43, rowTops[3], rowTops[4]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	if len(extra) != 0 {
		t.Errorf("snap-noise: got %d extra cells; want 0 (overhangTol filters sub-band noise)", len(extra))
	}
}

// TestLatticeOpenForeignHEdgeNoLeak: structural evidence must be a row rule of THIS table —
// a rule that reaches the inner vertical boundary, not one that merely shares the row Y.
// This table's own row rules stop at vMin=289 (no overhang). A SEPARATE margin-local rule
// sits at the same row Ys (x0=30, x1=200; x1 < vMin so it does not reach the inner boundary),
// as a neighbouring table, title box, or sidebar rule on the same page would. Margin words
// span every band. Recovery must still emit 0 cells: a foreign rule that does not reach vMin
// cannot supply overhang evidence for this table. (Regression for the cross-table h-edge leak.)
func TestLatticeOpenForeignHEdgeNoLeak(t *testing.T) {
	x0s, x1s, rowTops := standardGeom()
	cells := makeClosedCells(x0s, x1s, rowTops)
	// Own row rules span exactly [vMin,vMax]=[289,522] (no overhang); plus a foreign margin
	// rule [30,200] at each row Y (x1=200 < vMin=289 → does not reach the inner boundary).
	var hEdges []lEdge
	for _, y := range rowTops {
		hEdges = append(hEdges, lEdge{orient: 'h', x0: 289, x1: 522, top: y, bottom: y})
		hEdges = append(hEdges, lEdge{orient: 'h', x0: 30, x1: 200, top: y, bottom: y})
	}
	words := []Word{
		wordAtBand("foreign-1", 100, 43, rowTops[0], rowTops[1]),
		wordAtBand("foreign-2", 100, 43, rowTops[1], rowTops[2]),
		wordAtBand("foreign-3", 100, 43, rowTops[2], rowTops[3]),
		wordAtBand("foreign-4", 100, 43, rowTops[3], rowTops[4]),
	}
	media := [4]float64{0, 0, 612, 792}

	extra := recoverOpenColumns(cells, words, hEdges, media)
	if len(extra) != 0 {
		t.Errorf("foreign-h-edge: got %d extra cells; want 0 (a rule not reaching vMin is not this table's own row rule)", len(extra))
	}
}
