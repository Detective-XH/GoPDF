package pdf

import "testing"

// Per-cell-grid column-cut recovery unit tests (PR-1). They lock the synthetic mechanism of
// inferColumnCuts / splitWideBandCells and its false-positive guards: corroboration (a cut x must
// match a real column boundary of THIS table — the NIST p5 sibling-table guard) and content-straddle
// (the cell's words must land in ≥2 of the new sub-cells — the spanning-label/merged-header guard).
// Real-world cross-publisher coverage is the deferred de-bias corpus fixtures (PR-1 acceptance #4);
// these tests lock the data-structure invariant the mechanism rests on.
//
// Coordinate convention (tables_lattice.go:21-23): top <= bottom, "below = larger". The header band
// [-20,-15] sits above the data band [-15,-5].

func pcgCell(x0, x1, top, bottom float64) lCell {
	return lCell{x0: x0, top: top, x1: x1, bottom: bottom}
}

func pcgVEdge(x, top, bottom float64) lEdge {
	return lEdge{x0: x, x1: x, top: top, bottom: bottom, orient: 'v'}
}

// splitHeaderRow is three narrow header cells establishing column boundaries at x=100,200,300,400.
func splitHeaderRow() []lCell {
	return []lCell{
		pcgCell(100, 200, -20, -15),
		pcgCell(200, 300, -20, -15),
		pcgCell(300, 400, -20, -15),
	}
}

// dataWord returns a word centered at ax inside the data band (ay = -10 ∈ [-15,-5]).
func dataWord(ax float64) Word { return Word{S: "9", X: ax - 3, Y: 8, W: 6, H: 4} }

// TestSplitWideBandCellsVEdgeCorroborated: a fused wide data cell whose words populate each column,
// with corroborated interior v-edges, splits into its columns.
func TestSplitWideBandCellsVEdgeCorroborated(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(200, -20, -5), pcgVEdge(300, -20, -5)}
	words := []Word{dataWord(150), dataWord(250), dataWord(350)} // one per column
	out := splitWideBandCells(cells, words, vEdges)
	if len(out) != 6 { // 3 header + 3 split data
		t.Fatalf("corroborated v-edge split: got %d cells, want 6", len(out))
	}
	if dc := distinctCols(out); dc != 3 {
		t.Errorf("corroborated v-edge split: distinctCols=%d, want 3", dc)
	}
	for _, want := range [][2]float64{{100, 200}, {200, 300}, {300, 400}} {
		found := false
		for _, c := range out {
			if c.top == -15 && c.x0 == want[0] && c.x1 == want[1] {
				found = true
			}
		}
		if !found {
			t.Errorf("split data sub-cell [%v,%v] missing", want[0], want[1])
		}
	}
}

// TestSplitWideBandCellsRejectsUncorroboratedVEdge is the NIST p5 false-positive guard: a v-edge
// from another table (x=250, matching NO sibling-cell boundary) must NOT split the wide cell.
func TestSplitWideBandCellsRejectsUncorroboratedVEdge(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(250, -20, -5)}
	words := []Word{dataWord(150), dataWord(350)}
	assertWideCellIntact(t, splitWideBandCells(cells, words, vEdges), "uncorroborated v-edge")
}

// TestSplitWideBandCellsRejectsNonSpanningContent is the content-straddle guard (codex finding 1):
// corroborated, geometrically real cuts exist, but the cell's content sits in ONE column (a spanning
// label / merged-header title). The cell must be left intact rather than broken across columns.
func TestSplitWideBandCellsRejectsNonSpanningContent(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(200, -20, -5), pcgVEdge(300, -20, -5)} // real, corroborated cuts
	words := []Word{dataWord(150)}                                    // content only in the first column
	assertWideCellIntact(t, splitWideBandCells(cells, words, vEdges), "single-column content")
}

// TestSplitWideBandCellsBoundaryWordNotDoubleCounted guards the straddle gate against a single word
// whose anchor lands EXACTLY on a corroborated cut: half-open sub-cell membership must count it once
// (→ ≥2 fails → no split), not once per adjacent sub-cell. A centered single-word label must stay intact.
func TestSplitWideBandCellsBoundaryWordNotDoubleCounted(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	vEdges := []lEdge{pcgVEdge(200, -20, -5), pcgVEdge(300, -20, -5)}
	words := []Word{dataWord(200)} // single word centered exactly on the x=200 cut
	assertWideCellIntact(t, splitWideBandCells(cells, words, vEdges), "boundary word double-count")
}

// TestSplitWideBandCellsNoWordXFallback documents that the word-X (G4) path is deferred: with no
// v-edges, a wide cell is NOT split on word spacing (a high-FP path; G4 lands in a later PR).
func TestSplitWideBandCellsNoWordXFallback(t *testing.T) {
	cells := append(splitHeaderRow(), pcgCell(100, 400, -15, -5))
	words := []Word{dataWord(150), dataWord(250), dataWord(350)}
	assertWideCellIntact(t, splitWideBandCells(cells, words, nil), "no-vedge word-X")
}

// assertWideCellIntact fails unless the [100,400] wide cell survived splitWideBandCells unchanged.
func assertWideCellIntact(t *testing.T, out []lCell, ctx string) {
	t.Helper()
	for _, c := range out {
		if c.x0 == 100 && c.x1 == 400 {
			return
		}
	}
	t.Errorf("%s: wide cell [100,400] was split (false positive not guarded)", ctx)
}
