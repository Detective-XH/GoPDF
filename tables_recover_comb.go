package pdf

// Comb-body recovery (Stage 2 pass 6 — see plans/ARCHITECTURE.md §2): synthesizes data rows
// for a closed-header table whose body has only vertical rules (comb teeth) and no horizontal
// row rules. CONTRACT: structural preconditions read the PRISTINE snapshot (the pre-recovery
// deep copy taken in latticeTablesOpen) so earlier passes cannot corrupt its occupancy checks;
// body-word selection reads the AUGMENTED cells so already-placed words are not re-synthesized.
// The cross-table foreign-occupancy guard (combBodyForeignInBody) deliberately reads the
// PRISTINE snapshot for ALL sibling tables: this keeps the guard order-independent (reading
// mutated siblings would be asymmetric in j<i vs j>i) at the cost of blindness to
// recovery-synthesized sibling cells (zero observed corpus instances; see
// plans/C3-LATTICE-CLARIFICATION-PLAN.md).

import (
	"math"
	"sort"
)

// --- comb-body data-row recovery ---

// Comb-body tables have a fully-closed header band over a data body that carries
// only vertical rules (column cuts, like comb teeth) and no horizontal row rules.
// The data cells never close, so latticeTables produces only the header cells and
// every data value is dropped from the grid (present in Page.Lines() but absent
// from Tables()). inferCombBodyRows recovers the missing rows by synthesizing one
// cell per (word-Y-cluster × column-interval).
const (
	// combBodyMinCols is the minimum number of interior crossing V-edges required
	// for the comb-body data-row recovery (inferCombBodyRows) to engage. >=2 interior
	// crossing verticals represent at least two column divisions (a genuine multi-column
	// comb body), not a single split or a page-margin border that slipped through.
	combBodyMinCols = 2
	// combBodyStraddleTol is the touch-margin (points) for the anti-straddle gate: a
	// column cut falls "strictly inside" a word when it lies within
	// [w.X+combBodyStraddleTol, w.X+w.W-combBodyStraddleTol]. The 0.5 pt margin
	// absorbs sub-pixel PDF coordinate rounding without masking a genuine misalignment —
	// a word that truly straddles a rule will lie several points inside the word's
	// x-span, not 0.5 pt.
	combBodyStraddleTol = 0.5
	// combBodyMinDataRows is the minimum number of row-bands that must each fill >=2
	// NON-LABEL columns (a genuine multi-column data row) for the body to qualify as a
	// real comb-body data grid. A per-COLUMN occupancy test is too weak: scattered
	// infographic/map labels can occupy two columns in DISJOINT bands with no actual
	// multi-column row. Requiring >=2 co-occupied non-label columns IN THE SAME band
	// rejects scattered map labels, table-of-contents lists, formula/prose, and charts.
	combBodyMinDataRows = 2
)

// combCol is a recovered comb-body column interval [x0, x1].
type combCol struct{ x0, x1 float64 }

// inferCombBodyRows recovers the data rows of a "comb-body" table: a closed lattice
// whose closed cells are ALL in a top header band, below which vertical rules extend
// down like comb teeth into a data body that has NO horizontal rules (no interior row
// rules, no closing bottom rule). Without this pass every data value is dropped from
// the grid (present in Page.Lines() but absent from Tables()).
//
// Normally-closed tables are excluded by two guards: crossingV (their V-edges terminate
// at the bottom row rule and do not extend below it, so no comb teeth are found) and
// foreignInBody (their data is itself a separate closed table sitting in the body
// region). A per-table body-cell count is NOT used — it is structurally always 0 here,
// because headerBot = cellYSpan's max(bottom) and every closed cell satisfies
// top < bottom ≤ headerBot, so no cell can sit below the header band.
//
// Anti-straddle discriminator: if any body word's glyph box is split by a column cut
// (the cut falls strictly inside the word's x-span using the combBodyStraddleTol
// touch-margin), the data does not align to the V-rules and synthesizing cells would
// scramble values into wrong columns — bail. The gate is measured across ALL body
// words (not just the first band) so a later misaligned row cannot slip through.
// On cz-czso p753 the unemployment table scores 0 straddles (fires, recovering 12
// data rows × 7 columns) while the employees table scores several straddles in its
// first row band alone (bails).
//
// allClosed carries the PRISTINE closed cells for every table on the page (before any
// recovery pass augments them). tableIdx identifies this table in allClosed so the
// cross-table body-occupancy guard can exclude it and avoid the "normal page" false-
// positive (a normally-closed data table sitting inside this header's body region would
// otherwise appear to be an empty body).
//
// closed is the PRISTINE snapshot (pre-augmentation geometry) used for the structural
// preconditions; placed is the AUGMENTED table cells as they stand when this pass runs
// (the closed cells plus everything the earlier recovery passes added), used ONLY to
// filter the body-word selection. Selecting body words against placed (not closed)
// prevents re-synthesizing cells over words an earlier pass already recovered — it only
// EXCLUDES already-placed words, never drops a genuinely-missing value.
func inferCombBodyRows(closed, placed []lCell, tableIdx int, allClosed [][]lCell, words []Word, hEdges, vEdges []lEdge) []lCell {
	if len(closed) == 0 {
		return nil
	}
	_, headerBot := cellYSpan(closed)

	tableLeft, tableRight, gotBound := combBodyBounds(headerBot, hEdges)
	crossingV, bodyBot, cuts := combBodyCrossingV(vEdges, headerBot, tableLeft, tableRight)
	foreignInBody := combBodyForeignInBody(allClosed, tableIdx, headerBot, bodyBot, tableLeft, tableRight)

	if !gotBound || foreignInBody != 0 || len(crossingV) < combBodyMinCols {
		return nil
	}

	cols := combBodyColumns(cuts, tableRight)
	bodyWords, bands := combBodyBodyWords(placed, words, headerBot, bodyBot, tableLeft, tableRight)
	if len(bodyWords) == 0 {
		return nil
	}

	// Anti-straddle gate: bail if ANY body word is split by a column cut. Measured
	// across all body words — not just the first band — so a well-aligned first row
	// followed by misaligned later rows cannot pass the gate.
	if combBodyStraddles(bodyWords, cuts) > 0 {
		return nil
	}

	// Row co-occupancy gate: a real comb-body data grid has >= combBodyMinDataRows
	// row-bands that EACH fill >= 2 non-label columns (a genuine multi-column data row).
	// Scattered map/infographic labels (two columns occupied in disjoint bands), TOC
	// lists, formula/prose, and charts fall short and are rejected here.
	if combBodyDataRows(bodyWords, bands, cols) < combBodyMinDataRows {
		return nil
	}

	// Synthesize one cell per (band × column). The ±6 pt height matches the cluster1D
	// tolerance (4 pt) used to derive the bands, so reconstructGrid re-clusters the
	// synth-cell tops consistently and assigns each word to the correct row.
	synth := make([]lCell, 0, len(bands)*len(cols))
	for _, b := range bands {
		for _, cc := range cols {
			synth = append(synth, lCell{x0: cc.x0, top: b - 6, x1: cc.x1, bottom: b + 6})
		}
	}
	return synth
}

// combBodyBounds returns the table's left/right x-bounds from the widest H-edge snapped
// to headerBot — the header-bottom rule whose x-extent defines the table boundaries.
// gotBound is false when no H-edge sits at the header bottom.
func combBodyBounds(headerBot float64, hEdges []lEdge) (tableLeft, tableRight float64, gotBound bool) {
	bestLen := 0.0
	for _, e := range hEdges {
		if math.Abs(e.top-headerBot) <= rectRowSnapTol && (e.x1-e.x0) > bestLen {
			tableLeft, tableRight, bestLen, gotBound = e.x0, e.x1, e.x1-e.x0, true
		}
	}
	return tableLeft, tableRight, gotBound
}

// combBodyCrossingV returns the V-edges that cross the header-bottom rule (start at/above
// headerBot, extend below) AND are strictly interior to the table x-bounds (excluding
// page-margin verticals that run the full page height). It also returns bodyBot (the
// lowest crossing-V bottom) and the sorted column cut points {tableLeft} ∪ crossing-V
// x-positions.
func combBodyCrossingV(vEdges []lEdge, headerBot, tableLeft, tableRight float64) (crossingV []lEdge, bodyBot float64, cuts []float64) {
	bodyBot = headerBot
	xset := map[float64]bool{}
	for _, e := range vEdges {
		if e.top <= headerBot+rectRowSnapTol && e.bottom > headerBot+rectRowSnapTol &&
			e.x0 > tableLeft+1 && e.x0 < tableRight-1 {
			crossingV = append(crossingV, e)
			if e.bottom > bodyBot {
				bodyBot = e.bottom
			}
			xset[e.x0] = true
		}
	}
	cuts = make([]float64, 0, len(xset)+1)
	cuts = append(cuts, tableLeft)
	for x := range xset {
		cuts = append(cuts, x)
	}
	sort.Float64s(cuts)
	return crossingV, bodyBot, cuts
}

// combBodyForeignInBody counts closed cells from any OTHER table (j != tableIdx) whose
// center falls inside this table's body region. On a normal page the data lives in a
// SEPARATE closed table sitting inside this header's body region; a non-zero count vetoes
// recovery (the body is not actually empty).
func combBodyForeignInBody(allClosed [][]lCell, tableIdx int, headerBot, bodyBot, tableLeft, tableRight float64) int {
	foreign := 0
	for j, other := range allClosed {
		if j == tableIdx {
			continue
		}
		for _, c := range other {
			cx := (c.x0 + c.x1) / 2
			cy := (c.top + c.bottom) / 2
			if cy > headerBot+rectRowSnapTol && cy < bodyBot+rectRowSnapTol &&
				cx > tableLeft-2 && cx < tableRight+2 {
				foreign++
			}
		}
	}
	return foreign
}

// combBodyColumns turns the sorted cut points into consecutive column intervals,
// terminating the last interval at tableRight and dropping degenerate (<1 pt) intervals.
func combBodyColumns(cuts []float64, tableRight float64) []combCol {
	var cols []combCol
	for i, x := range cuts {
		right := tableRight
		if i+1 < len(cuts) {
			right = cuts[i+1]
		}
		if right > x+1 { // drop degenerate (<1 pt) intervals
			cols = append(cols, combCol{x, right})
		}
	}
	return cols
}

// combBodyBodyWords selects the unplaced words whose vertical anchor falls in
// (headerBot, bodyBot+6) and horizontal anchor in [tableLeft-2, tableRight+2], and
// returns them with their cluster1D row bands (sorted). bodyWords is nil when none qualify.
// placed is the AUGMENTED table cells (closed + earlier-recovery output); filtering against
// it excludes words an earlier pass already placed, so the body words are only the ones
// still genuinely missing from the grid.
func combBodyBodyWords(placed []lCell, words []Word, headerBot, bodyBot, tableLeft, tableRight float64) (bodyWords []Word, bands []float64) {
	dropped := unplacedWords(placed, words)
	var bays []float64
	for _, w := range dropped {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2)
		if ay > headerBot+1 && ay < bodyBot+6 && ax >= tableLeft-2 && ax <= tableRight+2 {
			bodyWords = append(bodyWords, w)
			bays = append(bays, ay)
		}
	}
	if len(bodyWords) == 0 {
		return nil, nil
	}
	bands = cluster1D(bays, 4)
	sort.Float64s(bands)
	return bodyWords, bands
}

// combBodyStraddles counts body words that are split by a column cut: a word is
// considered straddled when a cut falls strictly inside
// [w.X+combBodyStraddleTol, w.X+w.W-combBodyStraddleTol]. At most one straddle
// is counted per word regardless of how many cuts cross its glyph box.
func combBodyStraddles(bodyWords []Word, cuts []float64) int {
	n := 0
	for _, w := range bodyWords {
		for _, cut := range cuts {
			if cut > w.X+combBodyStraddleTol && cut < w.X+w.W-combBodyStraddleTol {
				n++
				break // one straddle per word is enough
			}
		}
	}
	return n
}

// combBodyDataRows counts how many DISTINCT row-bands each fill >=2 NON-LABEL columns —
// a genuine multi-column data row. Each body word is assigned to its (band, column) by
// center anchor: band via nearestIdx(bands, ay), column via the combCol whose [x0,x1]
// contains ax. cols[0] (leftmost) is the LABEL column and is excluded. A real comb-body
// data grid has many such rows; scattered map/infographic labels (two columns occupied in
// disjoint bands), a TOC list, a formula/prose block, or a chart do not, so this separates
// them where a per-column occupancy count cannot.
func combBodyDataRows(bodyWords []Word, bands []float64, cols []combCol) int {
	if len(cols) < 2 || len(bands) == 0 {
		return 0
	}
	bandCols := make([]map[int]bool, len(bands))
	for _, w := range bodyWords {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2)
		ci := -1
		for k, cc := range cols {
			if ax >= cc.x0 && ax <= cc.x1 {
				ci = k
				break
			}
		}
		if ci <= 0 { // outside every column, or in the label column (col0) → skip
			continue
		}
		bi := nearestIdx(bands, ay)
		if bandCols[bi] == nil {
			bandCols[bi] = map[int]bool{}
		}
		bandCols[bi][ci] = true
	}
	n := 0
	for _, set := range bandCols {
		if len(set) >= 2 {
			n++
		}
	}
	return n
}
