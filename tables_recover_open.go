package pdf

// Open-column recovery + the Stage-2 pass sequencer (see plans/ARCHITECTURE.md §2).
// CONTRACT (latticeTablesOpen): page-wide pools (hEdges/vEdges/rectH) are computed ONCE here
// and passed into every table's passes; a PRISTINE deep copy of the closed-lattice output is
// snapshotted BEFORE any recovery pass mutates the per-table slices (comb-body preconditions
// and its cross-table guard read pristine; all other passes read the augmented tables[i]).
// The pass ORDER is a contract: recoverOpenColumns → inferRectBorderedRows →
// inferShadedSiblingRows → inferFillBandedRows → inferHeaderRuledDataCells → inferCombBodyRows.
// recoverOpenColumns synthesizes half-open edge cells from h-edge overhang evidence only.

import (
	"math"
	"sort"
)

// --- open-column recovery ---

// Constants for open-column recovery (the only tunables; locked by the synthetic guard tests).
//
//	overhangTol — minimum rule extension past vMin/vMax to count as a real overhang.
//	              = 2×snapTol = 6 pt. Filters snap-noise artifacts (NIST ~1 pt < 6 pt)
//	              while admitting genuine overhangs (IRS ~258 pt >> 6 pt).
//	minOpenRows — open-side words must span >= this many admitted bands (rejects a
//	              single marginal note outside the outer rule).
//	minOpenColW — reject a sub-rule sliver narrower than 2× snapTol (6 pt).
//	openPad     — outward pad so near-boundary word anchors are fully contained; the
//	              INNER side gets NO pad — it is exactly vMin/vMax so the synthesized cell
//	              never overlaps any interior cell.
//
// minOpenColW and openPad are also read by synthOpenColumns (tables_lattice.go), the
// rect-bordered-rows sibling recovery path. Defined here because recoverOpenColumns/
// recoverOneSide are the primary/eponymous owner of this constant group.
const (
	overhangTol = 6.0
	minOpenRows = 2
	minOpenColW = 6.0
	openPad     = 2.0
)

// latticeTablesOpen is latticeTables plus open edge-column recovery per table.
//
// It calls latticeTables (unchanged — closed-only) then appends any recovered half-open
// edge cells for each table. latticeTables is retained verbatim so the locked closed-cell
// goldens (EPA, NIST) keep asserting against the unchanged function independently.
//
// media is the page MediaBox [llx, lly, urx, ury] in PDF coordinates; only llx (media[0])
// and urx (media[2]) are used as the outer clamps.
//
// fillRects is the fill-provenance-verified subset of c.Rect (only re rects actually closed
// by a fill-class paint op — see contentState.fillRect); inferShadedSiblingRows uses it, never
// c.Rect directly, since c.Rect alone cannot distinguish a genuine shading fill from a
// stroked-only or unpainted re rectangle. Callers with no fill-provenance available (most
// existing tests) may pass nil — inferShadedSiblingRows degrades to a no-op, matching prior
// behavior before this pass existed.
func latticeTablesOpen(c Content, words []Word, media [4]float64, fillRects []Rect) [][]lCell {
	edges := mergeEdges(edgesFromContent(c), 3, 3) // same pool latticeTables uses
	var hEdges, vEdges []lEdge
	for _, e := range edges {
		if e.orient == 'h' {
			hEdges = append(hEdges, e)
		} else {
			vEdges = append(vEdges, e)
		}
	}
	tables := latticeTables(c) // closed-only, unchanged

	// Pristine snapshot: a deep copy of the latticeTables output before any recovery
	// pass mutates the per-table slices. inferCombBodyRows checks body-occupancy
	// against the PRISTINE cells — inferRectBorderedRows can split some header cells
	// into fake body rows (e.g. the employees-table header → ~10 fake cells), which
	// would corrupt bodyClosedCount and cause inferCombBodyRows to fire incorrectly.
	// All other passes read tables[i] after prior mutations, unchanged.
	pristine := make([][]lCell, len(tables))
	for i, tbl := range tables {
		pristine[i] = append([]lCell(nil), tbl...)
	}

	// rectOnlyHEdges: the shading-provenance pool inferShadedSiblingRows needs to tell a
	// row split derived from an alternating-row-shading fill-rect apart from one derived from
	// an ordinary ruling stroke (or, sharing Content.Rect's own ambiguity, a stroked-only re
	// rectangle — hence fillRects, not c.Rect, as the input). Computed once here (page-wide,
	// like hEdges/vEdges) and passed into every table's pass rather than recomputed per table.
	rectH := rectOnlyHEdges(fillRects)

	for i := range tables {
		tables[i] = append(tables[i], recoverOpenColumns(tables[i], words, hEdges, media)...)
		tables[i] = inferRectBorderedRows(tables[i], words, hEdges, media)
		tables[i] = inferShadedSiblingRows(tables[i], words, rectH)
		tables[i] = inferFillBandedRows(tables[i], words, c, vEdges)
		// Last: recover data cells for the headers-only class the passes above cannot form
		// (header ruled into columns, data body ruled only down the label column). Runs on
		// their output so it engages only when the data grid is still otherwise empty.
		tables[i] = append(tables[i], inferHeaderRuledDataCells(tables[i], words)...)
		// Comb-body recovery (Variant 3): synthesizes data rows for a closed-header table
		// whose body has only vertical rules (comb teeth) and no horizontal row rules.
		// Structural preconditions run on the PRISTINE snapshot (pristine[i]/pristine) so
		// their geometry is not corrupted by the earlier recovery passes; the body-WORD
		// selection runs on the AUGMENTED tables[i] so words an earlier pass already placed
		// are not re-synthesized into overlapping cells.
		tables[i] = append(tables[i], inferCombBodyRows(pristine[i], tables[i], i, pristine, words, hEdges, vEdges)...)
	}
	return tables
}

// cellYSpan returns the top-to-bottom extent of a cell slice.
func cellYSpan(cells []lCell) (yTop, yBot float64) {
	yTop, yBot = cells[0].top, cells[0].bottom
	for _, c := range cells[1:] {
		if c.top < yTop {
			yTop = c.top
		}
		if c.bottom > yBot {
			yBot = c.bottom
		}
	}
	return yTop, yBot
}

// tableHEdges selects the h-edges within the table's Y-span (± snapTol) and returns
// them along with the sorted, clustered row-top representatives.
func tableHEdges(cells []lCell, hEdges []lEdge) (tableH []lEdge, rowTops []float64) {
	const snapTol = 3.0
	yTop, yBot := cellYSpan(cells)
	for _, e := range hEdges {
		if e.top >= yTop-snapTol && e.top <= yBot+snapTol {
			tableH = append(tableH, e)
		}
	}
	if len(tableH) == 0 {
		return nil, nil
	}
	var htops []float64
	for _, e := range tableH {
		htops = append(htops, e.top)
	}
	rowTops = cluster1D(htops, snapTol)
	sort.Float64s(rowTops)
	return tableH, rowTops
}

// edgeOverhangsLeft reports whether a row rule of THIS table overhangs left into the open
// region at row y: an h-edge that (a) extends left past vMin-overhangTol AND (b) reaches the
// inner boundary vMin (x1 >= vMin) — one continuous rule running from the open region into
// the table body. The reach-inner-boundary requirement binds the structural evidence to this
// table: a foreign margin-local rule that merely shares the row Y (a neighbouring table, a
// title box, or a sidebar rule) has x1 < vMin and is rejected, so unrelated page geometry
// cannot fabricate a phantom column.
func edgeOverhangsLeft(tableH []lEdge, y, vMin float64) bool {
	const snapTol = 3.0
	for _, e := range tableH {
		if math.Abs(e.top-y) <= snapTol && e.x0 <= vMin-overhangTol && e.x1 >= vMin {
			return true
		}
	}
	return false
}

// edgeOverhangsRight is the symmetric right-side test: an h-edge that extends right past
// vMax+overhangTol AND reaches the inner boundary vMax (x0 <= vMax). See edgeOverhangsLeft
// for why the reach-inner-boundary requirement binds the evidence to this table.
func edgeOverhangsRight(tableH []lEdge, y, vMax float64) bool {
	const snapTol = 3.0
	for _, e := range tableH {
		if math.Abs(e.top-y) <= snapTol && e.x1 >= vMax+overhangTol && e.x0 <= vMax {
			return true
		}
	}
	return false
}

// recoverOneSide attempts to synthesize half-open cells for one side (left or right).
// sideWords are the words outside the inner boundary; x0/x1 are the cell x-extents;
// overhangCheck is the per-row-top test. Returns the admitted cells.
func recoverOneSide(sideWords []Word, x0, x1 float64, minW float64, rowTops []float64, overhangCheck func(float64) bool) []lCell {
	if x1-x0 < minW {
		return nil
	}
	admitted := admitOpenColumn(sideWords, x0, x1, rowTops, overhangCheck)
	if len(admitted) >= minOpenRows {
		return admitted
	}
	return nil
}

// recoverOpenColumns synthesizes half-open edge cells for one closed lattice table.
//
// STRUCTURAL EXISTENCE GATE: A half-open cell is admitted only when the table's horizontal
// row rules overhang past vMin/vMax by more than overhangTol (= 6 pt = 2×snapTol).
// rowTops is derived from the SAME merged table h-edges the overhang test reads
// (single source of truth), so band-bounding edges match by construction.
//
// TEXT-BBOX SETS WIDTH: The outer extent is sourced from the open-side words' bboxes
// (clamped to the MediaBox), so word anchors are guaranteed contained.
func recoverOpenColumns(cells []lCell, words []Word, hEdges []lEdge, media [4]float64) []lCell {
	if len(cells) == 0 {
		return nil
	}
	vMin, vMax := colBounds(cells)
	tableH, rowTops := tableHEdges(cells, hEdges)
	if len(tableH) == 0 || len(rowTops) < 2 {
		return nil
	}

	llx, urx := media[0], media[2]
	yTop, yBot := cellYSpan(cells)

	var out []lCell

	// LEFT open column: words whose anchor ax < vMin within the table Y-span.
	leftWords := openSideWords(words, yTop, yBot, func(ax float64) bool { return ax < vMin })
	if len(leftWords) > 0 {
		outerExt := clampLo(minWordLeft(leftWords)-openPad, llx)
		leftOverhang := func(y float64) bool { return edgeOverhangsLeft(tableH, y, vMin) }
		out = append(out, recoverOneSide(leftWords, outerExt, vMin, minOpenColW, rowTops, leftOverhang)...)
	}

	// RIGHT open column: words whose anchor ax > vMax within the table Y-span.
	rightWords := openSideWords(words, yTop, yBot, func(ax float64) bool { return ax > vMax })
	if len(rightWords) > 0 {
		rightExt := clampHi(maxWordRight(rightWords)+openPad, urx)
		rightOverhang := func(y float64) bool { return edgeOverhangsRight(tableH, y, vMax) }
		out = append(out, recoverOneSide(rightWords, vMax, rightExt, minOpenColW, rowTops, rightOverhang)...)
	}

	return out
}

// admitOpenColumn applies the per-band structural confirmation for one side (left or right).
//
// For each consecutive row band [rowTops[i], rowTops[i+1]], the band is admitted when:
//   - both bounding row tops overhang (overhangs returns true for each), AND
//   - the band contains at least one word from sideWords.
//
// x0/x1 define the cell x-extents (set by the caller for the specific side):
// left columns: x0=outerExt, x1=vMin; right columns: x0=vMax, x1=outerExt.
func admitOpenColumn(sideWords []Word, x0, x1 float64, rowTops []float64, overhangs func(float64) bool) []lCell {
	var admitted []lCell
	for i := 0; i+1 < len(rowTops); i++ {
		if !overhangs(rowTops[i]) || !overhangs(rowTops[i+1]) {
			continue
		}
		if !bandHasWord(sideWords, rowTops[i], rowTops[i+1]) {
			continue
		}
		admitted = append(admitted, lCell{x0: x0, top: rowTops[i], x1: x1, bottom: rowTops[i+1], open: true})
	}
	return admitted
}

// bandHasWord returns true when at least one word's top-origin anchor falls in [bandTop, bandBot].
func bandHasWord(words []Word, bandTop, bandBot float64) bool {
	for _, w := range words {
		ay := -(w.Y + w.H/2)
		if ay >= bandTop && ay <= bandBot {
			return true
		}
	}
	return false
}

// colBounds returns the minimum x0 and maximum x1 over all cells (the inner boundaries
// of any edge columns that exist as fully-closed cells).
func colBounds(cells []lCell) (vMin, vMax float64) {
	vMin, vMax = cells[0].x0, cells[0].x1
	for _, c := range cells[1:] {
		if c.x0 < vMin {
			vMin = c.x0
		}
		if c.x1 > vMax {
			vMax = c.x1
		}
	}
	return vMin, vMax
}
