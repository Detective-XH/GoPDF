package pdf

// tables_lattice.go — internal lattice (ruled-table) detection engine.
//
// This is a spec-port of pdfplumber's "lines" strategy (snap=join=intersection=3 pt)
// extended with a structural open-column recovery gate. The pipeline is:
//   edges (strokes + rects) → merge (snap + join) → intersections → cells → tables
// plus an optional per-table open-column recovery step (latticeTablesOpen).

import (
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// --- geometric types ---

// lEdge is a snapped ruling-line edge in a TOP-ORIGIN frame (topY = -Y), so the ported
// cell-walk (below = larger top, right = larger x) matches pdfplumber verbatim.
// h-edge: top == bottom (the constant y), x0 <= x1 (the span).
// v-edge: x0 == x1 (the constant x), top <= bottom (the span).
type lEdge struct {
	orient      byte // 'h' or 'v'
	x0, x1      float64
	top, bottom float64
}

func (e lEdge) length() float64 {
	if e.orient == 'h' {
		return e.x1 - e.x0
	}
	return e.bottom - e.top
}

// lCell is one closed cell in top-origin coordinates.
//
// open marks provenance: true for a cell synthesized by recoverOpenColumns/synthOpenColumns
// (an open/recovered column living outside the table's own rule-bounded core — e.g. a
// footnote-marker or overhang label column). These cells have no real governing rule of their
// own: their narrow width makes them prone to being pulled onto a neighboring REAL column's
// rule if snapped like an ordinary rule-bounded cell — the single-axis-ruled Item 1 fix's
// defect (1); see plans/decisions/SINGLE-AXIS-RULED-ITEM1-SPIKE-VERDICT-2026-07-01.md.
// snapToGoverningRule excludes open cells from rule-snapping unconditionally. Every other
// construction site (closed lattice cells and the other recovery passes: rect-bordered,
// fill-banded, comb-body, header-ruled-data) leaves this at its zero value (false).
type lCell struct {
	x0, top, x1, bottom float64
	open                bool
}

// pointKey is a quantized intersection point (0.01 pt) used as a float-map key.
type pointKey struct{ x, y float64 }

// inter records which v- and h-edge indices contribute to an intersection point.
type inter struct {
	vIdx, hIdx []int
}

// q quantizes v to 0.01 pt for stable float map keys.
func q(v float64) float64 { return math.Round(v*100) / 100 }

// --- edge pipeline ---

// edgesFromContent builds the raw edge pool from strokes + rects, normalized to top-origin,
// pre-filtered to length >= 1pt (edge_min_length_prefilter). The `lines` strategy edge pool.
// All Content.Rect entries are included verbatim; thin rects (< 3 pt wide/tall) collapse to
// a single edge under snap — the mechanism that handles thin filled-rect ruling lines.
func edgesFromContent(c Content) []lEdge {
	var es []lEdge
	for _, s := range c.Stroke {
		es = append(es, strokeToEdge(s))
	}
	for _, r := range c.Rect {
		es = append(es, rectToEdges(r)...)
	}
	var out []lEdge
	for _, e := range es {
		if e.orient == 0 {
			continue // degenerate (zero-length both axes)
		}
		if e.length() >= 1.0 {
			out = append(out, e)
		}
	}
	return out
}

func strokeToEdge(s Stroke) lEdge {
	x0, x1 := s.From.X, s.To.X
	y0, y1 := -s.From.Y, -s.To.Y // top-origin: negate Y
	dx, dy := math.Abs(x1-x0), math.Abs(y1-y0)
	switch {
	case dx == 0 && dy == 0:
		return lEdge{} // orient 0 -> dropped
	case dx >= dy: // horizontal
		yMid := (y0 + y1) / 2
		return lEdge{orient: 'h', x0: min(x0, x1), x1: max(x0, x1), top: yMid, bottom: yMid}
	default: // vertical
		xMid := (x0 + x1) / 2
		return lEdge{orient: 'v', x0: xMid, x1: xMid, top: min(y0, y1), bottom: max(y0, y1)}
	}
}

// rectToEdges turns one rect into its 4 borders. A thin rect (one axis < ~3 pt) still yields 4
// edges; the two near-coincident long edges collapse to one under snap (the thin-filled-rect
// ruling-line mechanism used by documents like the NIST HB44 tables).
func rectToEdges(r Rect) []lEdge {
	left, right := min(r.Min.X, r.Max.X), max(r.Min.X, r.Max.X)
	top, bottom := -max(r.Min.Y, r.Max.Y), -min(r.Min.Y, r.Max.Y) // top-origin
	return []lEdge{
		{orient: 'h', x0: left, x1: right, top: top, bottom: top},       // top border
		{orient: 'h', x0: left, x1: right, top: bottom, bottom: bottom}, // bottom border
		{orient: 'v', x0: left, x1: left, top: top, bottom: bottom},     // left border
		{orient: 'v', x0: right, x1: right, top: top, bottom: bottom},   // right border
	}
}

func mergeEdges(edges []lEdge, snapTol, joinTol float64) []lEdge {
	edges = snapEdges(edges, snapTol)
	edges = joinEdges(edges, joinTol)
	var out []lEdge
	for _, e := range edges {
		if e.length() >= 3.0 { // edge_min_length (post-merge)
			out = append(out, e)
		}
	}
	return out
}

// snapEdges clusters v-edges by x0 (within tol) and h-edges by top (within tol), shifting each
// cluster member to the cluster mean. cluster_by = sort, single-linkage on consecutive gap.
func snapEdges(edges []lEdge, tol float64) []lEdge {
	snap := func(es []lEdge, get func(*lEdge) float64, set func(*lEdge, float64)) {
		sort.SliceStable(es, func(i, j int) bool { return get(&es[i]) < get(&es[j]) })
		i := 0
		for i < len(es) {
			j := i + 1
			sum, n := get(&es[i]), 1
			for j < len(es) && get(&es[j])-get(&es[j-1]) <= tol { // single-linkage chain
				sum += get(&es[j])
				n++
				j++
			}
			mean := sum / float64(n)
			for k := i; k < j; k++ {
				set(&es[k], mean)
			}
			i = j
		}
	}
	var v, h []lEdge
	for _, e := range edges {
		if e.orient == 'v' {
			v = append(v, e)
		} else {
			h = append(h, e)
		}
	}
	snap(v, func(e *lEdge) float64 { return e.x0 }, func(e *lEdge, x float64) { e.x0, e.x1 = x, x })
	snap(h, func(e *lEdge) float64 { return e.top }, func(e *lEdge, y float64) { e.top, e.bottom = y, y })
	return append(v, h...)
}

// joinEdges merges, within each snapped-coordinate group, collinear segments that overlap or
// lie within joinTol of each other.
func joinEdges(edges []lEdge, tol float64) []lEdge {
	groupKey := func(e lEdge) float64 {
		if e.orient == 'v' {
			return e.x0
		}
		return e.top
	}
	spanLo := func(e lEdge) float64 {
		if e.orient == 'v' {
			return e.top
		}
		return e.x0
	}
	spanHi := func(e *lEdge) *float64 {
		if e.orient == 'v' {
			return &e.bottom
		}
		return &e.x1
	}
	byKey := map[[2]float64][]lEdge{}
	for _, e := range edges {
		k := [2]float64{float64(e.orient), groupKey(e)}
		byKey[k] = append(byKey[k], e)
	}
	var out []lEdge
	for _, grp := range byKey {
		sort.SliceStable(grp, func(i, j int) bool { return spanLo(grp[i]) < spanLo(grp[j]) })
		cur := grp[0]
		for _, e := range grp[1:] {
			if spanLo(e) <= *spanHi(&cur)+tol { // overlap-or-within-tol -> extend
				if hi := spanLo(e) + e.length(); hi > *spanHi(&cur) {
					*spanHi(&cur) = hi
				}
			} else {
				out = append(out, cur)
				cur = e
			}
		}
		out = append(out, cur)
	}
	return out
}

// --- intersection → cell → table pipeline ---

func intersectIdx(a, b []int) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// edgesToIntersections finds every (v,h) crossing within tolerance; the vertex is (v.x0, h.top).
func edgesToIntersections(edges []lEdge, xTol, yTol float64) (map[pointKey]*inter, []lEdge, []lEdge) {
	var v, h []lEdge
	for _, e := range edges {
		if e.orient == 'v' {
			v = append(v, e)
		} else {
			h = append(h, e)
		}
	}
	out := map[pointKey]*inter{}
	for vi := range v {
		for hi := range h {
			ve, he := v[vi], h[hi]
			if ve.top <= he.top+yTol && ve.bottom >= he.top-yTol &&
				ve.x0 >= he.x0-xTol && ve.x0 <= he.x1+xTol {
				k := pointKey{q(ve.x0), q(he.top)}
				it := out[k]
				if it == nil {
					it = &inter{}
					out[k] = it
				}
				it.vIdx = append(it.vIdx, vi)
				it.hIdx = append(it.hIdx, hi)
			}
		}
	}
	return out, v, h
}

// interSharers holds the functions for checking shared edges between intersection points.
type interSharers struct {
	shareV func(a, b pointKey) bool
	shareH func(a, b pointKey) bool
}

// findClosingCell searches for the smallest closing rectangle (cell) for the given
// top-left point pt among the sorted candidate points. Returns (cell, true) if found.
func findClosingCell(pt pointKey, pts []pointKey, inters map[pointKey]*inter, s interSharers) (lCell, bool) {
	var below, right []pointKey
	for _, o := range pts {
		if o.x == pt.x && o.y > pt.y {
			below = append(below, o)
		}
		if o.y == pt.y && o.x > pt.x {
			right = append(right, o)
		}
	}
	for _, b := range below {
		if !s.shareV(pt, b) {
			continue
		}
		for _, r := range right {
			if !s.shareH(pt, r) {
				continue
			}
			br := pointKey{r.x, b.y}
			if _, ok := inters[br]; ok && s.shareV(br, r) && s.shareH(br, b) {
				return lCell{x0: pt.x, top: pt.y, x1: r.x, bottom: b.y}, true
			}
		}
	}
	return lCell{}, false
}

// intersectionsToCells finds, for each point (sorted top-left first), the smallest closing rectangle.
func intersectionsToCells(inters map[pointKey]*inter) []lCell {
	pts := make([]pointKey, 0, len(inters))
	for k := range inters {
		pts = append(pts, k)
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].x != pts[j].x {
			return pts[i].x < pts[j].x
		}
		return pts[i].y < pts[j].y
	})
	s := interSharers{
		shareV: func(a, b pointKey) bool { return a.x == b.x && intersectIdx(inters[a].vIdx, inters[b].vIdx) },
		shareH: func(a, b pointKey) bool { return a.y == b.y && intersectIdx(inters[a].hIdx, inters[b].hIdx) },
	}
	var cells []lCell
	for i, pt := range pts {
		if cell, ok := findClosingCell(pt, pts[i+1:], inters, s); ok {
			cells = append(cells, cell)
		}
	}
	return cells
}

// cellsToTables groups cells into tables by shared-corner connected components; drops singletons.
func cellsToTables(cells []lCell) [][]lCell {
	corners := func(c lCell) [4]pointKey {
		return [4]pointKey{{c.x0, c.top}, {c.x0, c.bottom}, {c.x1, c.top}, {c.x1, c.bottom}}
	}
	remaining := append([]lCell(nil), cells...)
	var tables [][]lCell
	for len(remaining) > 0 {
		curCorners := map[pointKey]bool{}
		var cur []lCell
		for {
			before := len(cur)
			var next []lCell
			for _, c := range remaining {
				cs := corners(c)
				touch := len(cur) == 0
				for _, cn := range cs {
					if curCorners[cn] {
						touch = true
					}
				}
				if touch {
					for _, cn := range cs {
						curCorners[cn] = true
					}
					cur = append(cur, c)
				} else {
					next = append(next, c)
				}
			}
			remaining = next
			if len(cur) == before {
				break
			}
		}
		tables = append(tables, cur)
	}
	var out [][]lCell
	for _, t := range tables {
		if len(t) > 1 { // a lone rectangle is not a table
			out = append(out, t)
		}
	}
	return out
}

// latticeTables is the end-to-end closed lattice driver: edges -> merge -> intersections -> cells -> tables.
func latticeTables(c Content) [][]lCell {
	edges := mergeEdges(edgesFromContent(c), 3, 3)
	inters, _, _ := edgesToIntersections(edges, 3, 3)
	return cellsToTables(intersectionsToCells(inters))
}

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
func latticeTablesOpen(c Content, words []Word, media [4]float64) [][]lCell {
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

	for i := range tables {
		tables[i] = append(tables[i], recoverOpenColumns(tables[i], words, hEdges, media)...)
		tables[i] = inferRectBorderedRows(tables[i], words, hEdges, media)
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

// inferHeaderRuledDataCells recovers the "headers-only / partial-table-miss" class: a closed
// lattice whose header is ruled into a multi-column row while the DATA body is ruled only down
// the label column (col0). The data values then fall outside every closed cell and reconstructGrid
// drops them (they survive only in Lines()) — the grid comes out with populated headers and empty
// data columns. This synthesizes the missing data cells at (header column cell x-range) × (col0
// data-row band), but ONLY where a word actually sits: a triple anchor (a real header x-cut, a
// real ruled row band, real content) that adds no phantom column and splits no existing cell (the
// data body has none to split — this is purely additive).
//
// Discriminator (0-FP, structural): a column-defining header row of >= headerRuledMinCols cells,
// a data body ruled in exactly one column aligned to that header's label column, and a word
// present in the target box. A normally-ruled table (data rows already carry their own column
// cells) fails the distinctCols == 1 test and is returned untouched; the other recovery passes
// run first, so a table they already reconstruct no longer presents a single-column data body.
func inferHeaderRuledDataCells(cells []lCell, words []Word) []lCell {
	cdCells, cdTop, ok := headerColumnDefiningRow(cells)
	if !ok {
		return nil
	}
	dataCells, ok := colOnlyDataBody(cells, cdTop, cdCells[0].x0)
	if !ok {
		return nil
	}
	// Trigger only on DROPPED words — those whose center fell inside no existing cell. A word
	// already correctly placed (a col0 label, a value an earlier pass recovered) is therefore
	// never re-grabbed or duplicated; only genuinely-missing data drives synthesis.
	return synthHeaderDataCells(dataCells, cdCells, cdCells[0].x1, unplacedWords(cells, words))
}

// unplacedWords returns the words whose center anchor (top-origin) falls inside none of cells —
// the words reconstructGrid would drop. Used as the sole trigger evidence for header-ruled data
// recovery so it can only resurrect dropped content, never re-claim an already-placed word.
func unplacedWords(cells []lCell, words []Word) []Word {
	var out []Word
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2)
		placed := false
		for _, c := range cells {
			if ax >= c.x0 && ax <= c.x1 && ay >= c.top && ay <= c.bottom {
				placed = true
				break
			}
		}
		if !placed {
			out = append(out, w)
		}
	}
	return out
}

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

// headerColumnDefiningRow returns the TOPMOST row band carrying the most cells — the header that
// defines the column structure — sorted left-to-right, provided it has >= headerRuledMinCols
// cells. The topmost-max choice is deterministic (map iteration order is randomized) and
// semantically correct: it elects the header, never an interior fully-ruled data row (which would
// leave a spurious single-row "data region" below it).
func headerColumnDefiningRow(cells []lCell) (cdCells []lCell, cdTop float64, ok bool) {
	if len(cells) < 2 {
		return nil, 0, false
	}
	tops := make([]float64, len(cells))
	for i, c := range cells {
		tops[i] = c.top
	}
	rowReps := cluster1D(tops, 4) // the row tolerance reconstructGrid also uses
	byBand := map[int][]lCell{}
	for _, c := range cells {
		byBand[nearestIdx(rowReps, c.top)] = append(byBand[nearestIdx(rowReps, c.top)], c)
	}
	bandKeys := make([]int, 0, len(byBand))
	for b := range byBand {
		bandKeys = append(bandKeys, b)
	}
	sort.Ints(bandKeys) // ascending band index == top-to-bottom (rowReps is sorted)
	cdBand, cdN := -1, 0
	for _, b := range bandKeys {
		if len(byBand[b]) > cdN {
			cdN, cdBand = len(byBand[b]), b
		}
	}
	if cdN < headerRuledMinCols {
		return nil, 0, false
	}
	cdCells = append([]lCell(nil), byBand[cdBand]...)
	sort.Slice(cdCells, func(i, j int) bool { return cdCells[i].x0 < cdCells[j].x0 })
	return cdCells, rowReps[cdBand], true
}

// colOnlyDataBody returns the cells strictly below the column-defining row, requiring that they
// are ruled in exactly one column aligned to the header's label column (the headers-only
// signature). It reports false for a normally/partially ruled table, which is then left untouched.
func colOnlyDataBody(cells []lCell, cdTop, labelX0 float64) ([]lCell, bool) {
	var dataCells []lCell
	for _, c := range cells {
		if c.top > cdTop+rectRowSnapTol {
			dataCells = append(dataCells, c)
		}
	}
	if len(dataCells) == 0 || distinctCols(dataCells) != 1 ||
		math.Abs(dataCells[0].x0-labelX0) > colClusterTol {
		return nil, false
	}
	return dataCells, true
}

// synthHeaderDataCells synthesizes data cells from the dropped words, but only for a header
// data-column that receives dropped words in >= headerRuledMinColBands distinct data-row bands —
// COLUMN-LEVEL cluster evidence, not a lone word. A single unplaced word geometrically inside one
// (header-col × col0-band) box is indistinguishable from a real single value, so a lone hit (e.g.
// a footnote/overprint word that happens to fall in the data rectangle) is NOT promoted to a cell;
// a genuine data column reappears across multiple rows and clears the bar. The trigger is the
// triple anchor (real header x-cut, real ruled row band, real dropped content) plus this cluster.
func synthHeaderDataCells(dataCells, cdCells []lCell, labelX1 float64, dropped []Word) []lCell {
	type hit struct{ di, hi int }
	var hits []hit
	colBands := map[int]int{} // header-column index → number of distinct data bands it hits
	for di, dc := range dataCells {
		for hi, hc := range cdCells {
			if hc.x0 < labelX1 {
				continue // skip the label column itself
			}
			if wordInBox(dropped, hc.x0, hc.x1, dc.top, dc.bottom) {
				hits = append(hits, hit{di, hi})
				colBands[hi]++
			}
		}
	}
	var out []lCell
	for _, h := range hits {
		if colBands[h.hi] < headerRuledMinColBands {
			continue // lone in-envelope word — not a data column
		}
		dc, hc := dataCells[h.di], cdCells[h.hi]
		// Defensive provenance carry: dc/hc are ordinarily real closed cells (this pass requires
		// a ruled header row and a ruled label column), but cells is the AUGMENTED table set
		// (recoverOpenColumns already ran), so either could in principle be an open/recovered
		// cell — propagate rather than assume.
		out = append(out, lCell{x0: hc.x0, top: dc.top, x1: hc.x1, bottom: dc.bottom, open: hc.open || dc.open})
	}
	return out
}

// wordInBox reports whether any word's center anchor (top-origin) falls inside the box.
func wordInBox(words []Word, x0, x1, top, bottom float64) bool {
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2)
		if ax >= x0 && ax <= x1 && ay >= top && ay <= bottom {
			return true
		}
	}
	return false
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

// openSideWords returns the subset of words whose top-origin vertical anchor ay falls
// inside [yTop,yBot] AND whose horizontal anchor ax satisfies pred.
//
// Anchor convention (matches reconstructGrid exactly):
//
//	ax = w.X + w.W/2
//	ay = -(w.Y + w.H/2)   (top-origin: more-positive ay = further down the page)
func openSideWords(words []Word, yTop, yBot float64, pred func(ax float64) bool) []Word {
	var out []Word
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2)
		if ay >= yTop && ay <= yBot && pred(ax) {
			out = append(out, w)
		}
	}
	return out
}

// minWordLeft returns the minimum w.X (left bbox edge) over all words in ws.
func minWordLeft(ws []Word) float64 {
	m := ws[0].X
	for _, w := range ws[1:] {
		if w.X < m {
			m = w.X
		}
	}
	return m
}

// maxWordRight returns the maximum w.X+w.W (right bbox edge) over all words in ws.
func maxWordRight(ws []Word) float64 {
	m := ws[0].X + ws[0].W
	for _, w := range ws[1:] {
		if r := w.X + w.W; r > m {
			m = r
		}
	}
	return m
}

// clampLo returns the larger of v and lo (i.e. max(v, lo)).
func clampLo(v, lo float64) float64 {
	if v < lo {
		return lo
	}
	return v
}

// clampHi returns the smaller of v and hi (i.e. min(v, hi)).
func clampHi(v, hi float64) float64 {
	if v > hi {
		return hi
	}
	return v
}

// --- rect-bordered row inference (columns-ruled, rows-unruled tables) ---

// Rect-bordered tables (e.g. the Economic Report of the President B-1/B-2) have full column
// verticals and an outer frame but ZERO interior horizontal rules in the data body, so
// latticeTables collapses every data column into one full-height cell and the data rows are
// lost. The pass below recovers them by inferring row bands from the data-body word Y-centers.
//
// Scope / regression: a normally-ruled table is left unchanged because its cells terminate at
// interior rules (so they do not reach the bottom as a tall multi-row band), and the guards
// below — multi-column, body-fraction dominance, interior-rule count, row alignment — reject the
// rest. This is verified BYTE-IDENTICAL on the corpus: NICS, HHS ASPE, IRS P17, EPA, and IRS-SOI
// (34 interior data-body rules, 0 collapsed cells) all early-return. It is NOT an absolute
// guarantee for every conceivable ruled table: one whose GEOMETRY is itself rect-bordered — a
// short header over a single tall multi-line band that is one wrapped row, not many rows — is
// indistinguishable from the target and will be row-split. That ambiguity is inherent (the
// 1-wrapped-row and N-row inputs are byte-identical); see the rect-bordered decision record.
const (
	// rectRowSnapTol matches a cell bottom to the table bottom and bounds the interior-rule
	// exclusion window around the data-body extremes.
	rectRowSnapTol = 3.0
	// rectMinRowClusters is the minimum word-Y-rows a full-height cell must hold to count as a
	// collapsed multi-row data column. It doubles as the multi-row guard: a one- or two-line
	// framed box (a callout) never reaches it.
	rectMinRowClusters = 3
	// rectMinRowSplit is the minimum number of numeric cross-column record bands a BEA-branch cell
	// must contain before splitTallBandCells (PR-2) splits it into rows. It is deliberately HIGHER
	// than rectMinRowClusters: a corpus sweep of the shipped mechanism showed the exactly-3-band
	// zone is false-positive-dense (blank-row insertion at group separators, displaced multi-line
	// headers), while every genuine collapsed data table (FT-900 months, German census waves, MHLW
	// wage rows) carries far more (10–46) record bands. It is a HEURISTIC boundary, NOT a complete
	// false-positive guard: a count threshold cannot by itself separate a 4-row numeric HEADER /
	// annotation block (each row carrying year-like tokens in ≥2 columns) from a 4-row collapsed
	// data block — that residual, and prose/cover/TOC blocks that reach the BEA branch at all, are
	// documented known limitations (a structural body-vs-header discriminator is a follow-on).
	rectMinRowSplit = 4
	// rectRowGapTol clusters word Y-centers into row bands. Kept equal to reconstructGrid's row
	// clustering tol so the synthesized tops re-cluster consistently downstream.
	rectRowGapTol = 4.0
	// rectMinBodyFrac is the minimum fraction of the table's vertical extent that the full-height
	// candidate cells must span. It separates a genuinely collapsed data body (which dominates
	// the table — ERP B-1 spans ~85%) from a multi-line BOTTOM ROW of an otherwise fully-ruled
	// table (whose cells also reach the table bottom and can hold >=3 wrapped lines, but span only
	// a small fraction). Without it, a multi-row ruled table whose last row wraps in >=2 aligned
	// columns would be wrongly row-split. It does NOT catch a ruled table that is geometrically
	// rect-bordered (a short header over one tall wrapped row dominating the body) — that input is
	// indistinguishable from the target; see the rect-bordered decision record.
	rectMinBodyFrac = 0.6
	// headerRuledMinCols is the minimum cell count of the column-defining header row for the
	// header-ruled data-cell recovery (inferHeaderRuledDataCells) to engage. >=3 ruled columns
	// is a genuine multi-column header, not a 1-2 cell caption/frame band.
	headerRuledMinCols = 3
	// headerRuledMinColBands is the minimum number of distinct data-row bands a header data-column
	// must receive dropped words in before that column is synthesized. >=2 demands column-level
	// cluster evidence so a lone in-envelope word (geometrically indistinguishable from a single
	// real value — e.g. a footnote inside the data rectangle) is never promoted to table data.
	headerRuledMinColBands = 2
)

// inferRectBorderedRows splits each full-height data column (plus a synthesized open anchor
// column) of a rect-bordered table into rows inferred from word Y-centers. It returns cells
// unchanged unless the table is genuinely columns-ruled / rows-unruled — the three guards
// (multi-column, no interior data-body rules, multi-row) make fully-ruled and single-cell
// framed regions early-return untouched.
func inferRectBorderedRows(cells []lCell, words []Word, hEdges []lEdge, media [4]float64) []lCell {
	if len(cells) == 0 {
		return cells
	}
	yTop, tableBot := cellYSpan(cells)
	full, others := splitFullHeight(cells, words, tableBot)
	if distinctCols(full) < 2 {
		return cells // single-cell frame (callout box) or a normally-ruled table — leave it
	}
	dataTop := minTop(full)
	if tableBot-dataTop < rectMinBodyFrac*(tableBot-yTop) {
		return cells // `full` is a multi-line bottom ROW of a ruled table, not a collapsed body
	}
	vMin, vMax := colBounds(full)
	if interiorHRuleCount(hEdges, dataTop, tableBot, vMin, vMax) > 0 {
		return cells // interior horizontal rules ⇒ a ruled table; never touch it
	}
	bands := rowBands(full, words)
	if len(bands) < rectMinRowClusters || !rowAligned(full, words, bands) {
		return cells // too few rows, or independent per-column prose (not a row-aligned grid)
	}
	full = append(full, synthOpenColumns(full, words, hEdges, media, dataTop, tableBot)...)
	out := append([]lCell(nil), others...)
	for _, c := range full {
		out = append(out, splitCellAtBands(c, bands)...)
	}
	return out
}

// --- fill-banded staircase row inference (group-ruled + fill-banded tables) ---

// Fill-banded staircase tables (e.g. EIA Annual Energy Review Table 3.1) use alternating
// fill rects whose bottoms all converge at the table's base and whose tops form a staircase
// of distinct monotonic steps — one step per data row. The lattice engine sees only 4 wide
// column cells (the long vertical group rules) and misses the row structure. This pass
// recovers rows by projecting word Y-centers onto a clustering grid and splitting each
// staircase data-body cell both vertically (row bands) and horizontally (column cuts from
// the v-edge pool).
//
// FP safety: the staircase signature (common-bottom, varying-tops, regular spacing, fraction
// of body height, >= rectMinRowClusters distinct tops) is shared by no other table class in
// the corpus: fully-ruled tables have interior h-rules (gate G5 rejects them); rect-bordered
// tables have rects that share common tops not common bottoms, or share neither (ERP); BEA
// alternating-fill has 46 distinct bottoms, not 1 (gate G1 rejects it); single-shaded callout
// boxes have only 1 or 2 distinct top steps (gate G3 rejects them); framed prose boxes fail
// the row-aligned gate (G6).

// fillStaircaseSteps returns the top-origin tops of rects whose bottom clusters within
// rectRowSnapTol of tableBot AND whose top is strictly inside the table body (not a full-
// height spanning banner). Full-height banner rects (top within rectRowSnapTol of tableTop)
// are excluded so they don't distort the regularity check.
//
// Banded fill rects are NESTED bands that share one common x-span (the same left and right
// edge), differing only in their stepped top — so the qualifying rects must collapse to a
// single x0 cluster and a single x1 cluster. A bar/column chart, by contrast, is made of
// rects with DISTINCT x-spans (one per bar), which yields multiple x clusters and is rejected
// here before the regularity check. (The shared-x test is independent of the table's column
// bounds, which recoverOpenColumns may have widened with a recovered label column.)
//
// rects are scoped to the current table by [vMin,vMax]: a fill region elsewhere on the page
// (another figure/chart) must not pollute the shared-x test, so rects whose x-span does not
// overlap [vMin,vMax] are skipped. (vMin/vMax come from the table's cells and may have been
// widened by recoverOpenColumns; the overlap test — not a full-span requirement — keeps the
// banded rects regardless of that widening.)
//
// Returns nil when fewer than rectMinRowClusters distinct tops are found or the qualifying
// rects do not share a single x-span.
func fillStaircaseSteps(rects []Rect, tableTop, tableBot, vMin, vMax float64) []float64 {
	var tops, x0s, x1s []float64
	for _, r := range rects {
		top := -max(r.Min.Y, r.Max.Y) // top-origin: negate max PDF Y
		bot := -min(r.Min.Y, r.Max.Y) // top-origin: negate min PDF Y
		rx0 := min(r.Min.X, r.Max.X)
		rx1 := max(r.Min.X, r.Max.X)
		// Scope to the current table: skip rects whose x-span lies entirely outside [vMin,vMax].
		if rx1 < vMin-rectRowSnapTol || rx0 > vMax+rectRowSnapTol {
			continue
		}
		// Exclude rects that don't share the common bottom or span the full table height.
		if math.Abs(bot-tableBot) > rectRowSnapTol || top < tableTop+rectRowSnapTol {
			continue
		}
		if top < tableBot-rectRowSnapTol {
			tops = append(tops, top)
			x0s = append(x0s, rx0)
			x1s = append(x1s, rx1)
		}
	}
	if len(tops) == 0 {
		return nil
	}
	// Nested bands share one x-span; bars (distinct x per rect) do not.
	if len(cluster1D(x0s, rectRowSnapTol)) != 1 || len(cluster1D(x1s, rectRowSnapTol)) != 1 {
		return nil
	}
	return cluster1D(tops, rectRowSnapTol)
}

// staircaseSignatureHolds checks that tops form a genuine staircase: distinct steps that are
// monotonic, regularly-spaced (max gap <= 2× median gap), and span >= rectMinBodyFrac of the
// table body (tableTop to tableBot).
func staircaseSignatureHolds(tops []float64, tableTop, tableBot float64) bool {
	if len(tops) < rectMinRowClusters {
		return false
	}
	// tops are sorted ascending by cluster1D (smallest = highest on page).
	span := tops[len(tops)-1] - tops[0]
	bodyH := tableBot - tableTop
	if bodyH <= 0 || span < rectMinBodyFrac*bodyH {
		return false
	}
	// Check regular spacing: max consecutive gap <= 2 × median gap.
	gaps := make([]float64, len(tops)-1)
	for i := range gaps {
		gaps[i] = tops[i+1] - tops[i]
	}
	sort.Float64s(gaps)
	median := gaps[len(gaps)/2]
	if median <= 0 {
		return false
	}
	maxGap := gaps[len(gaps)-1]
	return maxGap <= 2*median
}

// fillBandedDataRegion returns the top-origin [dataTop, dataBot] of the staircase data body.
//
// dataTop is derived from STROKE-ONLY h-edges: the last full-span stroke that lies strictly
// above the staircase top (steps[0]) is the column-header separator — the boundary between
// the sub-header row and the first data row (which may start above the first fill band). This
// captures data cells that precede the staircase fill bands. Falls back to steps[0] if no
// such stroke exists.
//
// dataBot is the full-span stroke immediately below the lowest staircase step, separating the
// data body from the footnote zone — the MOST-negative (nearest-staircase) qualifying stroke,
// which excludes the footer. Falls back to tableBot when no such stroke exists. This assumes a
// single data/footer frame below the staircase (the validated banded geometry); if several
// full-span strokes sit below the lowest step, the one nearest the staircase wins.
//
// strokeHEdges must be derived from c.Stroke only (not c.Rect) to avoid confusion with fill
// rect borders.
func fillBandedDataRegion(tops []float64, tableBot, vMin, vMax float64, strokeHEdges []lEdge) (dataTop, dataBot float64) {
	// NOTE: in top-origin coordinates, more-negative = higher on page.
	// staircaseHigh = tops[0] = most negative = topmost step.
	// staircaseLow  = tops[last] = least negative = bottommost step.
	staircaseHigh := tops[0]
	staircaseLow := tops[len(tops)-1]
	dataTop = staircaseHigh
	dataBot = tableBot

	// bestHeaderSep tracks the HIGHEST (most negative) top among candidate header-separator
	// strokes. It starts at math.Inf(-1) so any real stroke supersedes it.
	bestHeaderSep := math.Inf(-1)

	for _, e := range strokeHEdges {
		if e.x0 > vMin+rectRowSnapTol || e.x1 < vMax-rectRowSnapTol {
			continue // not a full-span edge
		}
		// Header-separator: a full-span stroke strictly ABOVE the staircase top
		// (e.top < staircaseHigh, i.e. more negative). Among all such strokes, take
		// the one CLOSEST to staircaseHigh (least negative = largest value).
		if e.top < staircaseHigh && e.top > bestHeaderSep {
			bestHeaderSep = e.top
		}
		// Bottom frame: the data/footer boundary is the full-span stroke at or just below the
		// lowest staircase step (for the banded geometry the bottom frame sits right at the
		// last data row, coincident with the lowest step within rectRowSnapTol). In top-origin
		// coords the footer sits below (less negative than) the data, so the boundary is the
		// MOST-negative qualifying stroke; taking it (the e.top < dataBot min) excludes the
		// footer. The window admits a stroke within rectRowSnapTol of the lowest step because
		// the bottom frame can be coincident with it.
		if e.top >= staircaseLow-rectRowSnapTol && e.top < tableBot-rectRowSnapTol {
			if e.top < dataBot {
				dataBot = e.top
			}
		}
	}
	if !math.IsInf(bestHeaderSep, -1) {
		dataTop = bestHeaderSep
	}
	return dataTop, dataBot
}

// fillBandedVCuts collects distinct interior vertical-edge x-positions inside (vMin, vMax),
// clustered within 2 pt. These are the column separators for the fill-banded table.
func fillBandedVCuts(vEdges []lEdge, vMin, vMax float64) []float64 {
	const vCutClusterTol = 2.0
	var xs []float64
	for _, e := range vEdges {
		if e.x0 > vMin+rectRowSnapTol && e.x0 < vMax-rectRowSnapTol {
			xs = append(xs, e.x0)
		}
	}
	return cluster1D(xs, vCutClusterTol)
}

// inferColumnCuts returns interior x-cut positions for a wide data cell from the rect-backed
// vertical column rules (the G1 path). A v-edge yields a cut only when BOTH hold:
//
//   - (x-interior) fillBandedVCuts admits it strictly inside (cell.x0, cell.x1);
//   - (corroboration) its x matches a column boundary already present in THIS table (knownXs),
//     within rectRowSnapTol — a real separator coincides with the x0/x1 of narrower sibling cells
//     (e.g. the split header that defines the columns); a page-global edge from another element does
//     not. NOTE: a y-overlap requirement is deliberately NOT used — in a per-cell-grid the columns
//     are defined by the header/top rules while the data rows themselves are unruled, so a column
//     rule legitimately does not cross the data cell it bounds (verified on DE insolvenzen p6:
//     requiring y-overlap removes every real cut). Corroboration is the geometric guard instead.
//
// The caller (splitWideBandCells) then applies a content-straddle gate so a cell whose words do not
// span the cuts (a merged/spanning label) is left intact — that is the false-positive guard for a
// cut that is geometrically real but should not break a single-column cell.
//
// The word-X (G4) fallback is intentionally NOT implemented here: clustering word mid-X invents
// columns from ordinary word spacing on a wide label/callout/prose cell (a high false-positive
// path — see PR-1 risk R1). G4 is deferred to a later PR with a multi-row-alignment guard.
func inferColumnCuts(cell lCell, vEdges []lEdge, knownXs []float64) []float64 {
	rawCuts := fillBandedVCuts(vEdges, cell.x0, cell.x1)
	cuts := rawCuts[:0]
	for _, cx := range rawCuts {
		if xCorroborated(cx, knownXs) {
			cuts = append(cuts, cx)
		}
	}
	return cuts
}

// xCorroborated reports whether x is within rectRowSnapTol of any value in knownXs.
// Used to filter v-edge cuts that don't correspond to a cell boundary already present
// in the table, rejecting spurious cuts from page-level edges outside this table.
func xCorroborated(x float64, knownXs []float64) bool {
	for _, kx := range knownXs {
		if math.Abs(x-kx) <= rectRowSnapTol {
			return true
		}
	}
	return false
}

// splitWideBandCells applies column cuts (via inferColumnCuts) to any cell whose own content
// genuinely spans multiple columns, after the BEA phantom-clamp and dropTrailingEmptyBands passes
// are already done. A split fires only when (a) it yields ≥2 distinct columns AND (b) the cell's
// words land in ≥2 of the resulting sub-cells (the content-straddle gate). Gate (b) is the
// false-positive guard against splitting a cell that holds a single spanning label or merged-header
// title: a geometrically real column boundary that the cell's content does not cross leaves the
// cell intact. Extracted as a helper to keep inferFillBandedRowsBEA's cyclomatic complexity in check.
func splitWideBandCells(cells []lCell, words []Word, vEdges []lEdge) []lCell {
	if len(cells) == 0 {
		return cells
	}
	// Collect the set of column-boundary x-positions already present in the cell set.
	// Used by inferColumnCuts to corroborate v-edge cuts (reject spurious page-global edges).
	knownXs := make([]float64, 0, len(cells)*2)
	for _, c := range cells {
		knownXs = append(knownXs, c.x0, c.x1)
	}

	any := false
	expanded := make([]lCell, 0, len(cells))
	for _, c := range cells {
		cuts := inferColumnCuts(c, vEdges, knownXs)
		splits := splitCellAtXs(c, cuts)
		if len(splits) > 1 && distinctCols(splits) >= 2 && populatedSplits(splits, words) >= 2 {
			expanded = append(expanded, splits...)
			any = true
		} else {
			expanded = append(expanded, c)
		}
	}
	if !any {
		return cells // avoid replacing with an equal-length new slice unnecessarily
	}
	return expanded
}

// populatedSplits counts how many of the ordered, contiguous sub-cells contain at least one word,
// assigning each word to EXACTLY ONE sub-cell. Adjacent sub-cells from splitCellAtXs share the cut
// x-coordinate, so a half-open x-interval [x0,x1) (the last sub-cell closed) is used: a word whose
// anchor lands exactly on a shared cut boundary is counted once, not in both neighbours. Without
// this, a single boundary/centered word would falsely satisfy the ≥2 straddle gate and split a
// genuine single-column label cell.
func populatedSplits(splits []lCell, words []Word) int {
	seen := make([]bool, len(splits))
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2) // top-origin anchor
		for i, c := range splits {
			if ay < c.top || ay > c.bottom || ax < c.x0 {
				continue
			}
			last := i == len(splits)-1
			if ax < c.x1 || (last && ax <= c.x1) {
				seen[i] = true
				break
			}
		}
	}
	n := 0
	for _, s := range seen {
		if s {
			n++
		}
	}
	return n
}

// splitCellAtXs splits cell c horizontally at each x-cut inside (c.x0, c.x1). The leftmost
// piece keeps c.x0 so left-margin labels stay anchored; the rightmost piece keeps c.x1. Every
// sub-piece inherits c.open: a split of an open/recovered cell (recoverOpenColumns/
// synthOpenColumns) is still an open/recovered cell — losing that provenance here would let
// snapToGoverningRule treat a synthesized marker column like an ordinary closed cell after any
// pass that splits it (e.g. inferRectBorderedRows splits synthOpenColumns output via
// splitCellAtBands/splitCellAtXs before reconstructGrid ever sees it).
// Returns []lCell{c} when no cuts fall inside (c.x0, c.x1).
func splitCellAtXs(c lCell, xcuts []float64) []lCell {
	var cuts []float64
	for _, x := range xcuts {
		if x > c.x0+rectRowSnapTol && x < c.x1-rectRowSnapTol {
			cuts = append(cuts, x)
		}
	}
	if len(cuts) == 0 {
		return []lCell{c}
	}
	sort.Float64s(cuts)
	out := make([]lCell, 0, len(cuts)+1)
	x0 := c.x0
	for _, x := range cuts {
		out = append(out, lCell{x0: x0, top: c.top, x1: x, bottom: c.bottom, open: c.open})
		x0 = x
	}
	out = append(out, lCell{x0: x0, top: c.top, x1: c.x1, bottom: c.bottom, open: c.open})
	return out
}

// fillBandedSplitCell splits one data-region cell first by row bands (vertical split) then
// by x-cuts (horizontal split), returning all resulting sub-cells.
// It filters bands to those strictly inside (c.top, c.bottom) before calling splitCellAtBands
// to prevent a 1-band cell from exploding into 43 sub-cells.
func fillBandedSplitCell(c lCell, bands []float64, xcuts []float64) []lCell {
	// Filter bands to those strictly inside this cell's vertical extent.
	var localBands []float64
	for _, b := range bands {
		if b > c.top && b < c.bottom {
			localBands = append(localBands, b)
		}
	}
	// Split vertically into row bands.
	var rowCells []lCell
	if len(localBands) < 2 {
		rowCells = []lCell{c}
	} else {
		rowCells = splitCellAtBands(c, localBands)
	}
	// Split each row band horizontally at x-cuts.
	out := make([]lCell, 0, len(rowCells)*(len(xcuts)+1))
	for _, rc := range rowCells {
		out = append(out, splitCellAtXs(rc, xcuts)...)
	}
	return out
}

// inferFillBandedRows recovers the row and column structure of fill-banded tables. It
// handles two opposite geometries:
//
//   - EIA staircase (e.g. EIA AER Table 3.1): alternating fill rects converge at a
//     common bottom with stepped tops; latticeTables produces wide multi-row cells that
//     this pass splits via G1–G6 and the downstream cell splitter.
//
//   - BEA per-cell-grid (e.g. BEA Survey of Current Business GDP Table 1): one fill rect
//     per cell, closing natively into a lattice that includes phantom title/footnote rows;
//     the BEA branch applies a subtractive phantom-clamp (never adds cells).
//
// Returns cells unchanged when neither signature holds.
func inferFillBandedRows(cells []lCell, words []Word, c Content, vEdges []lEdge) []lCell {
	if len(cells) == 0 {
		return cells
	}
	tableTop, tableBot := cellYSpan(cells)
	vMin, vMax := colBounds(cells)

	// G1: staircase signature — common-bottom fill rects with distinct, regular tops.
	steps := fillStaircaseSteps(c.Rect, tableTop, tableBot, vMin, vMax)
	if !staircaseSignatureHolds(steps, tableTop, tableBot) {
		// Try BEA per-cell-grid branch (subtractive phantom-clamp + column-cut recovery).
		return inferFillBandedRowsBEA(cells, words, c, vEdges)
	}

	// Pre-compute stroke-only h-edges (needed for both data-region and G5 checks).
	strokeH := strokeOnlyHEdges(c)

	// G2: derive data region using stroke-only edges to find the bottom frame rule.
	dataTop, dataBot := fillBandedDataRegion(steps, tableBot, vMin, vMax, strokeH)

	// G3: cluster word Y-centers in the data region into row bands.
	dataCell := lCell{x0: vMin, top: dataTop, x1: vMax, bottom: dataBot}
	allY := wordYCentersIn(words, dataCell)
	bands := cluster1D(allY, rectRowGapTol)
	if len(bands) < rectMinRowClusters {
		return cells
	}

	// G4: collect interior v-edge column cuts.
	xcuts := fillBandedVCuts(vEdges, vMin, vMax)

	// G5: no interior horizontal stroke rules inside the data body.
	if interiorHRuleCount(strokeH, dataTop, dataBot, vMin, vMax) > 0 {
		return cells
	}

	// G6: row-alignment cross-column check — bands must be shared across data cells.
	// Build a temporary set of full-height data cells (spanning the full data region)
	// to reuse rowAligned's multi-column cross-check.
	dataCells := cellsInDataRegion(cells, dataTop, dataBot)
	if len(dataCells) < 2 || !rowAligned(dataCells, words, bands) {
		return cells
	}

	// All gates passed: split every cell in the data region.
	out := make([]lCell, 0, len(cells)*len(bands))
	for _, cell := range cells {
		if cellInDataRegion(cell, dataTop, dataBot, vMin, vMax) {
			out = append(out, fillBandedSplitCell(cell, bands, xcuts)...)
		} else {
			out = append(out, cell)
		}
	}
	return out
}

// inferFillBandedRowsBEA handles the dense per-cell-grid variant of fill-banded tables
// (e.g. BEA Survey of Current Business GDP). Unlike the EIA staircase, BEA has one fill
// rect per cell; the native lattice closes into a grid that includes phantom title/footer
// rows. The mechanism is SUBTRACTIVE (phantom-clamp): retain only cells whose center falls
// within the data-body bbox (rows with ≥2 distinct rect x0 columns) and drop any trailing
// row band that contains no words. Never adds cells.
//
// It acts ONLY when the data-body bbox is strictly smaller than the table extent — i.e. a
// single-column banner row (title/footnote) lies outside the multi-column body. In a
// rect-derived grid every multi-column row falls inside that body by construction, so a real
// stroke-free grid whose rows are all multi-column (e.g. an EPA framed cover box) has
// body == extent, removes nothing, and is returned untouched. Without this no-op guard the
// downstream dropTrailingEmptyBands would trim a sparse real row off such a table (regression
// caught on EPA p1's 7×3 gutter frame).
func inferFillBandedRowsBEA(cells []lCell, words []Word, c Content, vEdges []lEdge) []lCell {
	if len(c.Stroke) > 0 {
		return cells // BEA signature requires zero stroke paths on the page
	}
	tableTop, tableBot := cellYSpan(cells)
	vMin, vMax := colBounds(cells)
	bodyTop, bodyBot, ok := beaDataBodyBBox(c.Rect, tableTop, tableBot, vMin, vMax)
	if !ok {
		return cells
	}
	clamped := clampCellsToBody(cells, bodyTop, bodyBot)
	if len(clamped) == len(cells) {
		return cells // body == extent: no out-of-body phantom rows to subtract — leave untouched
	}
	if distinctCols(clamped) < 2 {
		return cells
	}
	// Phantom-clamp done; drop trailing empty bands, then apply column-cut recovery followed
	// by row-split recovery (PR-2: numeric+cross-column-gated word-Y row clustering).
	dropped := dropTrailingEmptyBands(clamped, words)
	// Guard against phantom columns seeded by side-by-side header background fill rects.
	// When two fill rects share a midpoint boundary in the header band, the closed lattice
	// forms a short v-edge at that boundary, creating header cells with a spurious interior
	// x-boundary that (a) corrupts the knownXs set inside splitWideBandCells (making
	// xCorroborated pass for the phantom x) and (b) inflates reconstructGrid's column count.
	// Fix: when the header band is a seam header whose cell boundaries are absent from the
	// data rows, MERGE the header band into one full-width cell (text-preserving) so the
	// phantom boundary is gone but no header glyph is orphaned; splitWideBandCells then
	// re-derives the header columns from data-corroborated v-edge cuts.
	if firstDataRowTop, ok2 := beaFirstDataRowTop(c.Rect, tableTop, tableBot, vMin, vMax, bodyTop); ok2 {
		dropped = mergePhantomHeaderBand(dropped, firstDataRowTop)
	}
	colSplit := splitWideBandCells(dropped, words, vEdges)
	return splitTallBandCells(colSplit, words, bodyTop, bodyBot)
}

// splitTallBandCells splits any cell whose vertical extent contains ≥rectMinRowClusters "record
// bands" — word-Y clusters that (a) carry a numeric token and (b) straddle ≥2 columns — into
// those rows. It is the row-axis analog of splitWideBandCells, applied after the column-split pass
// so each column cell is independently tested for row collapse.
//
// The numeric + cross-column corroboration is the false-positive guard: a wrapped multi-line text
// header produces only text bands (rejected by the numeric gate) and a single-column label wraps
// within one column (rejected by the cross-column gate). The ≥3 threshold is a final backstop
// against 2-line multi-column values being mis-split.
func splitTallBandCells(cells []lCell, words []Word, bodyTop, bodyBot float64) []lCell {
	if len(cells) == 0 {
		return cells
	}
	// Build the column lattice from x0 positions — same tolerance as distinctCols.
	cols := tallBandColumnReps(cells)
	// Collect the words strictly inside the data body across all table columns. EVERY downstream gate
	// (band Y-clustering, numeric, cross-column) operates on THIS set only — scoping them to the body
	// is the false-positive guard against out-of-table page words (a margin page number, a date, a
	// footnote marker, or neighbouring-layout text at a matching Y) poisoning a text-only in-table
	// band into a spurious "numeric cross-column record band".
	vMin, vMax := colBounds(cells)
	bodyWords := tallBandBodyWords(words, bodyTop, bodyBot, vMin, vMax)
	allY := make([]float64, len(bodyWords))
	for i, w := range bodyWords {
		allY[i] = -(w.Y + w.H/2)
	}
	bands := cluster1D(allY, rectRowGapTol)
	// Keep only bands that are both numeric and cross-column (judged on the in-body words only).
	record := tallBandRecordBands(bands, bodyWords, cols)
	if len(record) < rectMinRowSplit {
		return cells // fewer than rectMinRowSplit record bands — nothing to recover
	}
	any := false
	out := make([]lCell, 0, len(cells))
	for _, c := range cells {
		inside := tallBandStrictlyInside(record, c.top, c.bottom)
		if len(inside) >= rectMinRowSplit {
			// This cell spans ≥3 record rows — it is a collapsed data column; split it.
			out = append(out, splitCellAtBands(c, inside)...)
			any = true
		} else {
			out = append(out, c)
		}
	}
	if !any {
		return cells
	}
	return out
}

// tallBandColumnReps returns the column representative x0 values by clustering all cell x0
// positions at tolerance 1.0 — the same tolerance distinctCols uses.
func tallBandColumnReps(cells []lCell) []float64 {
	xs := make([]float64, len(cells))
	for i, c := range cells {
		xs[i] = c.x0
	}
	return cluster1D(xs, 1.0)
}

// tallBandBodyWords returns the words whose anchor (ax,ay) falls inside the data body
// [bodyTop,bodyBot] × [vMin,vMax]. splitTallBandCells scopes every band / numeric / cross-column gate
// to this set so that out-of-table page words (margin page numbers, dates, footnote markers, or
// neighbouring-layout text at a matching Y) cannot poison a text-only in-table band into a spurious
// numeric cross-column record band.
func tallBandBodyWords(words []Word, bodyTop, bodyBot, vMin, vMax float64) []Word {
	var in []Word
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2)
		if ax >= vMin && ax <= vMax && ay >= bodyTop && ay <= bodyBot {
			in = append(in, w)
		}
	}
	return in
}

// tallBandRecordBands filters bands to "record bands" — those carrying NUMERIC tokens in ≥2 distinct
// columns. The numeric and cross-column tests are COUPLED (not two independent gates): the numeric
// evidence must itself be cross-column. This is the false-positive guard against a header / footnote /
// annotation band that merely has a numeric marker (a date "2024", a footnote "(1)") in ONE column
// and ordinary text in another — genuine stacked data records carry numeric VALUES across ≥2 columns,
// an annotation row does not. bodyWords MUST already be restricted to the data body (see
// tallBandBodyWords) so the gate judges the table's own content, never coincidental page words at a
// matching Y.
func tallBandRecordBands(bands []float64, bodyWords []Word, cols []float64) []float64 {
	var record []float64
	for _, b := range bands {
		if tallBandNumericColCount(b, bodyWords, cols) >= 2 {
			record = append(record, b)
		}
	}
	return record
}

// tallBandNumericColCount counts the distinct column buckets that contain at least one NUMERIC in-body
// word at band center b. A word is "in the band" when its Y-anchor is within rectRowGapTol of b; its
// column bucket is the nearest representative in cols. Coupling numeric-ness to the column count (vs
// counting any-word columns separately) is what stops a one-column numeric marker plus text elsewhere
// from promoting a non-data band — see tallBandRecordBands.
func tallBandNumericColCount(b float64, bodyWords []Word, cols []float64) int {
	seen := make(map[int]struct{})
	for _, w := range bodyWords {
		ay := -(w.Y + w.H/2)
		if math.Abs(ay-b) > rectRowGapTol || !numericTokenWord(w.S) {
			continue
		}
		ax := w.X + w.W/2
		seen[tallBandNearestCol(ax, cols)] = struct{}{}
	}
	return len(seen)
}

// tallBandNearestCol returns the index in cols of the representative closest to ax.
func tallBandNearestCol(ax float64, cols []float64) int {
	best, bestDist := 0, math.Abs(ax-cols[0])
	for i := 1; i < len(cols); i++ {
		if d := math.Abs(ax - cols[i]); d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// tallBandStrictlyInside returns the subset of record band-centers strictly inside (top, bottom)
// — mirroring fillBandedSplitCell's local-band filter (tables_lattice.go:1004-1010).
func tallBandStrictlyInside(record []float64, top, bottom float64) []float64 {
	var inside []float64
	for _, b := range record {
		if b > top && b < bottom {
			inside = append(inside, b)
		}
	}
	return inside
}

// numericTokenWord reports whether s (trimmed) is non-empty, contains at least one ASCII digit,
// and consists only of digits and the characters: , . - – ( ) % space. CJK/Cyrillic data tables
// still use ASCII digits so this rune-level check is sufficient. It is the numeric-gate predicate
// for splitTallBandCells.
func numericTokenWord(s string) bool {
	hasDigit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == ',' || r == '.' || r == '-' || r == '–' ||
			r == '(' || r == ')' || r == '%' || r == ' ':
			// allowed punctuation / separator
		default:
			return false
		}
	}
	return hasDigit
}

// beaDataBodyBBox derives the data-body bounding box from the fill rects that form real
// multi-column rows (≥2 distinct x0 positions within rectRowSnapTol). Thin separator
// rects (h<2) and rects outside the table extent are excluded. Returns (0,0,false) when
// fewer than rectMinRowClusters multi-column rows are found.
func beaDataBodyBBox(rects []Rect, tableTop, tableBot, vMin, vMax float64) (bodyTop, bodyBot float64, ok bool) {
	tops := beaBodyRectTops(rects, tableTop, tableBot, vMin, vMax)
	if len(tops) == 0 {
		return 0, 0, false
	}
	bandTops := cluster1D(tops, rectRowSnapTol)
	sort.Float64s(bandTops)

	kept := 0
	bodyTop, bodyBot = math.Inf(1), math.Inf(-1)
	for _, bandTop := range bandTops {
		cols, bBot := beaBandColumnsBottom(rects, bandTop)
		if cols < 2 {
			continue // single-column banner: title or footnote strip
		}
		kept++
		if bandTop < bodyTop {
			bodyTop = bandTop
		}
		if bBot > bodyBot {
			bodyBot = bBot
		}
	}
	if kept < rectMinRowClusters {
		return 0, 0, false
	}
	return bodyTop, bodyBot, true
}

// beaFirstDataRowTop returns the boundary that separates the table's fill-banded header from
// its first data row, for use by dropPhantomHeaderCols. It returns (boundary, true) when the
// band at bodyTop looks like a seam header (far fewer fill-rect columns than the next band
// below it — e.g. the header is painted with 2 wide background rects while data has 5 columns),
// setting boundary = first-data-row top. When the band at bodyTop already looks like a data row
// (comparable column count to the band below), it returns (bodyTop, true) so dropPhantomHeaderCols
// finds no "header cells" and becomes a no-op. Returns (0, false) when no qualifying next band
// exists.
func beaFirstDataRowTop(rects []Rect, tableTop, tableBot, vMin, vMax, bodyTop float64) (float64, bool) {
	tops := beaBodyRectTops(rects, tableTop, tableBot, vMin, vMax)
	if len(tops) == 0 {
		return 0, false
	}
	bandTops := cluster1D(tops, rectRowSnapTol)
	sort.Float64s(bandTops)
	// Find the first multi-column band strictly below bodyTop.
	nextBandTop := 0.0
	nextBandFound := false
	for _, bt := range bandTops {
		if bt <= bodyTop+rectRowSnapTol {
			continue
		}
		if beaBandColumnsScoped(rects, bt, vMin, vMax) >= 2 {
			nextBandTop = bt
			nextBandFound = true
			break
		}
	}
	if !nextBandFound {
		return 0, false
	}
	// Compare fill-rect column counts (scoped to the table x-extent so out-of-table rects —
	// sidebars, legends, neighbouring tables — cannot inflate the count): if bodyTop has far
	// fewer columns than the next band, bodyTop is a seam header (e.g. 2 wide header rects vs
	// 5 data-column rects) and the first real data row starts at nextBandTop. Otherwise bodyTop
	// is already a data row (the single-column title above it was excluded from beaDataBodyBBox),
	// so returning bodyTop makes mergePhantomHeaderBand a no-op — all cells span beyond
	// bodyTop+snap and are classified as data cells.
	colsAtBody := beaBandColumnsScoped(rects, bodyTop, vMin, vMax)
	colsAtNext := beaBandColumnsScoped(rects, nextBandTop, vMin, vMax)
	if colsAtNext > 0 && colsAtBody*2 <= colsAtNext {
		return nextBandTop, true // seam header: next band is first data row
	}
	return bodyTop, true // bodyTop is already a data row: fix is a no-op
}

// rowBandsByExtent clusters cells into row-bands: each band is a maximal run (in top order)
// whose tops AND bottoms EACH span at most rectRowSnapTol — one visual row. It applies the
// original single-row guard's "shared top AND bottom = one row" rule per band, so a genuine
// multi-row header yields one band per row. Clustering on top alone (the prior chain) would
// chain two rows whose tops happen to fall within tol but whose bottoms differ materially,
// flattening a real header row. Sorting is stable for deterministic intra-band order.
func rowBandsByExtent(cells []lCell) [][]lCell {
	sorted := append([]lCell(nil), cells...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].top < sorted[j].top })
	var bands [][]lCell
	for i := 0; i < len(sorted); {
		minTop, maxTop := sorted[i].top, sorted[i].top
		minBot, maxBot := sorted[i].bottom, sorted[i].bottom
		j := i + 1
		for j < len(sorted) {
			nt, nb := sorted[j].top, sorted[j].bottom
			if max(maxTop, nt)-min(minTop, nt) > rectRowSnapTol ||
				max(maxBot, nb)-min(minBot, nb) > rectRowSnapTol {
				break // adding this cell would break the shared-row-extent invariant
			}
			minTop, maxTop = min(minTop, nt), max(maxTop, nt)
			minBot, maxBot = min(minBot, nb), max(maxBot, nb)
			j++
		}
		bands = append(bands, sorted[i:j])
		i = j
	}
	return bands
}

// bandHasPhantomSeam reports whether any INTERIOR boundary of the band — a cell edge that is
// not the band's outer left (bandMinX) or right (bandMaxX) extent — is NOT data-corroborated
// AND lies strictly inside the data x-range (dataMin, dataMax). That is the phantom-seam
// signature: a fill-rect split with no data-column counterpart. Corroboration uses the shared
// xCorroborated helper (within rectRowSnapTol of any data x), the SAME test inferColumnCuts
// uses to accept real column boundaries — so a boundary jittered sub-pixel-to-a-few-tenths off a
// data column (e.g. 100.5 vs 100.0) is treated as corroborated, not a phantom. The outer edges
// are excluded so a header frame / fill-rect that overhangs the data extent (e.g. -1 / 301 over
// data 0..300) cannot trigger a merge, and the strict-within-range test is belt-and-suspenders
// against a near-edge boundary masquerading as an interior seam.
func bandHasPhantomSeam(band []lCell, dataXs []float64, dataMin, dataMax, bandMinX, bandMaxX float64) bool {
	for _, c := range band {
		for _, x := range [2]float64{c.x0, c.x1} {
			if x == bandMinX || x == bandMaxX {
				continue // outer edge of the band — never a seam between adjacent cells
			}
			if xCorroborated(x, dataXs) {
				continue // data-corroborated interior boundary (genuine sub-column edge)
			}
			if x > dataMin && x < dataMax {
				return true // interior boundary uncorroborated and inside the data span ⇒ phantom
			}
		}
	}
	return false
}

// mergeBandIfPhantom merges a single header row-band into one full-width cell when it carries
// a phantom interior seam (bandHasPhantomSeam), removing that boundary from the knownXs set
// fed to splitWideBandCells without orphaning any header glyph. A data-corroborated band
// (genuine grouped/spanning header whose interior seams align with real data columns) and a
// band with fewer than 2 cells (no interior boundary to test) are returned unchanged.
func mergeBandIfPhantom(band []lCell, dataXs []float64, dataMin, dataMax float64) []lCell {
	if len(band) < 2 {
		return band
	}
	bandMinX, bandMaxX := band[0].x0, band[0].x1
	for _, c := range band {
		bandMinX = min(bandMinX, c.x0)
		bandMaxX = max(bandMaxX, c.x1)
	}
	if !bandHasPhantomSeam(band, dataXs, dataMin, dataMax, bandMinX, bandMaxX) {
		return band
	}
	merged := band[0]
	for _, c := range band[1:] {
		merged.x0 = min(merged.x0, c.x0)
		merged.top = min(merged.top, c.top)
		merged.x1 = max(merged.x1, c.x1)
		merged.bottom = max(merged.bottom, c.bottom)
	}
	return []lCell{merged}
}

// mergePhantomHeaderBand collapses phantom-seam column boundaries from the header region
// (cells whose bottom sits at or before firstDataRowTop) of a seam-header BEA table.
//
// Header cells are clustered into row-bands by rowBandsByExtent (shared top AND bottom = one
// row). Each band is tested independently by mergeBandIfPhantom: a band carrying a phantom
// INTERIOR seam — an interior cell boundary not corroborated by any data x and strictly inside
// the data x-range — is merged into one full-width cell, removing the phantom boundary without
// orphaning any header glyph. Bands whose interior boundaries ARE data-corroborated (genuine
// grouped/spanning headers, e.g. "2020 | 2021" over sub-columns at real data-column x-positions)
// are left intact; the per-band interior-seam check IS the false-positive protection, so a real
// grouped header — even one whose outer fill-rect overhangs the body — is never flattened.
//
// A single-row header yields exactly one band — the single-band path matches the original
// single-row-guard behaviour (one band, interior-seam phantom check).
func mergePhantomHeaderBand(cells []lCell, firstDataRowTop float64) []lCell {
	var header, data []lCell
	for _, c := range cells {
		if c.bottom <= firstDataRowTop+rectRowSnapTol {
			header = append(header, c)
		} else {
			data = append(data, c)
		}
	}
	if len(header) < 2 || len(data) == 0 {
		return cells
	}
	// Collect the data-row x-positions (left+right of each cell) plus the data x-range. A header
	// interior boundary not within rectRowSnapTol of any of these (via xCorroborated) and strictly
	// inside the range is a phantom seam.
	dataXs := make([]float64, 0, len(data)*2)
	dataMin, dataMax := math.Inf(1), math.Inf(-1)
	for _, c := range data {
		dataXs = append(dataXs, c.x0, c.x1)
		dataMin = min(dataMin, c.x0)
		dataMax = max(dataMax, c.x1)
	}
	// Process each row-band independently so phantom bands are merged while data-corroborated
	// bands — genuine grouped headers — are kept intact.
	anyMerged := false
	var result []lCell
	for _, band := range rowBandsByExtent(header) {
		out := mergeBandIfPhantom(band, dataXs, dataMin, dataMax)
		if len(out) != len(band) {
			anyMerged = true
		}
		result = append(result, out...)
	}
	if !anyMerged {
		return cells
	}
	return append(result, data...)
}

// beaBodyRectTops returns the top (top-origin) of every cell-height fill rect (h>=2) that lies
// within the table extent — the candidate row tops the band clustering operates on. Thin
// separator strips and rects outside [vMin,vMax]×[tableTop,tableBot] are dropped.
func beaBodyRectTops(rects []Rect, tableTop, tableBot, vMin, vMax float64) []float64 {
	var tops []float64
	for _, r := range rects {
		top := -max(r.Min.Y, r.Max.Y)
		bot := -min(r.Min.Y, r.Max.Y)
		rx0 := min(r.Min.X, r.Max.X)
		rx1 := max(r.Min.X, r.Max.X)
		if bot-top < 2.0 {
			continue
		}
		if rx1 < vMin-rectRowSnapTol || rx0 > vMax+rectRowSnapTol {
			continue
		}
		if top < tableTop-rectRowSnapTol || bot > tableBot+rectRowSnapTol {
			continue
		}
		tops = append(tops, top)
	}
	return tops
}

// beaBandColumnsBottom returns, for the rect-row band at bandTop, the number of distinct rect-x0
// columns it spans and the lowest rect bottom in the band. A band with ≥2 distinct x0 columns is
// a real grid row; a single-column band is a full-width title/footnote banner.
func beaBandColumnsBottom(rects []Rect, bandTop float64) (cols int, bBot float64) {
	var x0s []float64
	bBot = math.Inf(-1)
	for _, r := range rects {
		top := -max(r.Min.Y, r.Max.Y)
		bot := -min(r.Min.Y, r.Max.Y)
		if bot-top < 2.0 || math.Abs(top-bandTop) > rectRowSnapTol {
			continue
		}
		x0s = append(x0s, min(r.Min.X, r.Max.X))
		if bot > bBot {
			bBot = bot
		}
	}
	return len(cluster1D(x0s, rectRowSnapTol)), bBot
}

// beaBandColumnsScoped counts the distinct rect-x0 columns in the rect-row band at bandTop,
// restricted to rects within the table's horizontal extent [vMin,vMax]. Unlike
// beaBandColumnsBottom it excludes out-of-table fill rects (sidebars, legends, neighbouring
// tables, page-header backgrounds) that happen to share a top within rectRowSnapTol — those
// would otherwise inflate the seam-header column comparison in beaFirstDataRowTop.
func beaBandColumnsScoped(rects []Rect, bandTop, vMin, vMax float64) int {
	var x0s []float64
	for _, r := range rects {
		top := -max(r.Min.Y, r.Max.Y)
		bot := -min(r.Min.Y, r.Max.Y)
		if bot-top < 2.0 || math.Abs(top-bandTop) > rectRowSnapTol {
			continue
		}
		rx0 := min(r.Min.X, r.Max.X)
		rx1 := max(r.Min.X, r.Max.X)
		if rx1 < vMin-rectRowSnapTol || rx0 > vMax+rectRowSnapTol {
			continue // outside the table x-extent
		}
		x0s = append(x0s, rx0)
	}
	return len(cluster1D(x0s, rectRowSnapTol))
}

// clampCellsToBody retains cells whose center falls within [bodyTop, bodyBot] (with a
// small snap tolerance for floating-point boundaries).
func clampCellsToBody(cells []lCell, bodyTop, bodyBot float64) []lCell {
	var out []lCell
	for _, c := range cells {
		cen := (c.top + c.bottom) / 2
		if cen >= bodyTop-rectRowSnapTol && cen <= bodyBot+rectRowSnapTol {
			out = append(out, c)
		}
	}
	return out
}

// dropTrailingEmptyBands removes trailing row bands (from the bottom up) that are BOTH
// word-empty AND single-column — i.e. full-width title/footnote banner phantoms the lattice
// swept into the clamp tolerance. It stops at the first trailing band that holds words OR spans
// >=2 columns, so a real multi-column row is never dropped even when blank (word-emptiness alone
// is not a phantom signal). This trims BEA's one phantom footnote row (37 -> 36 rows) while
// leaving a legitimate sparse data row intact.
func dropTrailingEmptyBands(cells []lCell, words []Word) []lCell {
	for len(cells) > 0 {
		tops := make([]float64, len(cells))
		for i, c := range cells {
			tops[i] = c.top
		}
		bands := cluster1D(tops, rectRowGapTol)
		if len(bands) <= rectMinRowClusters {
			return cells
		}
		sort.Float64s(bands)
		lastBand := bands[len(bands)-1]
		var band, rest []lCell
		bandTop, bandBot := math.Inf(1), math.Inf(-1)
		for _, cell := range cells {
			if math.Abs(cell.top-lastBand) <= rectRowGapTol {
				band = append(band, cell)
				bandTop = math.Min(bandTop, cell.top)
				bandBot = math.Max(bandBot, cell.bottom)
			} else {
				rest = append(rest, cell)
			}
		}
		probe := lCell{x0: -1e9, top: bandTop, x1: 1e9, bottom: bandBot}
		if len(wordYCentersIn(words, probe)) > 0 || distinctCols(band) != 1 {
			return cells // real row (has words, or spans >=2 columns) — never a banner phantom
		}
		cells = rest
	}
	return cells
}

// cellsInDataRegion returns cells whose vertical extent overlaps [dataTop, dataBot], for use
// in the rowAligned cross-column check. The input cells are already the current table's cells
// (the lattice scopes them), so no x-bound filter is needed here.
func cellsInDataRegion(cells []lCell, dataTop, dataBot float64) []lCell {
	var out []lCell
	for _, c := range cells {
		if c.bottom > dataTop && c.top < dataBot {
			out = append(out, c)
		}
	}
	return out
}

// cellInDataRegion reports whether cell c lies fully within the data region.
func cellInDataRegion(c lCell, dataTop, dataBot, vMin, vMax float64) bool {
	return c.bottom > dataTop+rectRowSnapTol && c.top < dataBot-rectRowSnapTol &&
		c.x0 >= vMin-rectRowSnapTol && c.x1 <= vMax+rectRowSnapTol
}

// strokeOnlyHEdges returns the merged horizontal edges derived from c.Stroke only
// (not from c.Rect), so fill rect borders are excluded. Used to detect real frame
// rules without being confused by the alternating-band rect borders.
func strokeOnlyHEdges(c Content) []lEdge {
	raw := mergeEdges(edgesFromContent(Content{Stroke: c.Stroke}), 3, 3)
	var h []lEdge
	for _, e := range raw {
		if e.orient == 'h' {
			h = append(h, e)
		}
	}
	return h
}

// splitFullHeight partitions cells into the full-height collapsed data columns (bottom at the
// table bottom AND holding >=rectMinRowClusters word rows) and everything else (header cells,
// which terminate above the data body). The cluster test is the recon-blessed full-height
// predicate — a top≈tableTop key is BROKEN because data cells start at the header-band bottom,
// not the table top.
func splitFullHeight(cells []lCell, words []Word, tableBot float64) (full, others []lCell) {
	for _, c := range cells {
		if math.Abs(c.bottom-tableBot) <= rectRowSnapTol && cellRowClusters(c, words) >= rectMinRowClusters {
			full = append(full, c)
		} else {
			others = append(others, c)
		}
	}
	return full, others
}

// cellRowClusters counts the distinct word-Y-rows whose anchor falls inside cell c.
func cellRowClusters(c lCell, words []Word) int {
	ys := wordYCentersIn(words, c)
	if len(ys) == 0 {
		return 0
	}
	return len(cluster1D(ys, rectRowGapTol))
}

// wordYCentersIn returns the top-origin Y-centers of words whose anchor falls inside cell c.
func wordYCentersIn(words []Word, c lCell) []float64 {
	var ys []float64
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2)
		if ax >= c.x0 && ax <= c.x1 && ay >= c.top && ay <= c.bottom {
			ys = append(ys, ay)
		}
	}
	return ys
}

// distinctCols counts the distinct column x-positions among cells (near-equal x0 within 1pt
// collapse). >=2 marks a genuine multi-column table, not a single-cell framed box.
func distinctCols(cells []lCell) int {
	if len(cells) == 0 {
		return 0
	}
	xs := make([]float64, len(cells))
	for i, c := range cells {
		xs[i] = c.x0
	}
	return len(cluster1D(xs, 1.0))
}

// minTop returns the smallest (highest) top over cells. Callers guarantee len(cells) > 0.
func minTop(cells []lCell) float64 {
	m := cells[0].top
	for _, c := range cells[1:] {
		if c.top < m {
			m = c.top
		}
	}
	return m
}

// interiorHRuleCount counts horizontal edges strictly inside the data body (between dataTop
// and tableBot) that horizontally overlap the column span [vMin,vMax]. The overlap test binds
// the count to THIS table, so a neighbouring table's rule at the same Y cannot make a
// rows-unruled table look ruled. Zero ⇒ rows-unruled (the rect-bordered case).
func interiorHRuleCount(hEdges []lEdge, dataTop, tableBot, vMin, vMax float64) int {
	n := 0
	for _, e := range hEdges {
		if e.top > dataTop+rectRowSnapTol && e.top < tableBot-rectRowSnapTol &&
			e.x0 <= vMax && e.x1 >= vMin {
			n++
		}
	}
	return n
}

// hasDecodableRune reports whether s carries at least one rune that is neither the Unicode
// replacement character (U+FFFD, an undecodable glyph) nor whitespace.
func hasDecodableRune(s string) bool {
	for _, r := range s {
		if r != '�' && !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// isDotLeader reports whether s is a tabular dot-leader run — a token of >=4
// consecutive '.' (U+002E) glyphs and nothing else (the filler that visually
// connects a row label to its value). It carries no data, so the table path
// drops it. The all-runes-are-'.' test rejects decimals ("4.0"), abbreviations
// ("U.S.A."), and 3-dot ellipses; the >=4 floor rejects a lone period.
func isDotLeader(s string) bool {
	n := 0
	for _, r := range s {
		if r != '.' {
			return false
		}
		n++
	}
	return n >= 4
}

// leaderDotRunFloor is the minimum length of a consecutive '.' (U+002E) run that, when flanked by
// letters on BOTH sides (hasInterleavedLeaderRun), marks a table cell as carrying a dot-leader
// fused INTO a label rather than isolated decimal points or an abbreviation. Measured against the
// USDA NASS crop table, where a State label's trailing dot leader is rendered with its glyphs
// interleaved into the label and so fuses into the label token: "Iowa" arrives as "Iow...a..."
// (3-dot runs), "Alabama" as "Alaba...m...a...." (4-dot runs), while "U.S." carries only single-dot
// runs. A floor of 3 fires on every fused State label (lifting the held-out fixture 0/300 ->
// 294/300) yet never on "U.S." (run length 1); a floor of 4 misses the exact-3-run states
// (Iowa/Utah/Ohio), dropping the score to 276/300.
const leaderDotRunFloor = 3

// hasInterleavedLeaderRun reports whether s contains a dot-leader run — n or more consecutive '.'
// (U+002E) — immediately flanked by a Unicode letter on BOTH sides. This is the signature of a dot
// leader whose glyphs fused INTO a row label ("Alaba...m...a...." for "Alabama"), as distinct from
// a TRAILING ellipsis ("continued...", no letter after the run), a digit-flanked range ("1...3",
// not letters), an abbreviation ("U.S.", runs shorter than n), or a space-separated ellipsis
// ("see ... below", the run abuts a space). It is the gate for stripLeaderDots, so the repair
// fires only on genuinely leader-contaminated label cells and leaves legitimate data text alone.
func hasInterleavedLeaderRun(s string, n int) bool {
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if rs[i] != '.' {
			i++
			continue
		}
		j := i
		for j < len(rs) && rs[j] == '.' {
			j++
		}
		if j-i >= n && i > 0 && unicode.IsLetter(rs[i-1]) && j < len(rs) && unicode.IsLetter(rs[j]) {
			return true
		}
		i = j
	}
	return false
}

// asciiDigitAt reports whether rs[i] is an ASCII digit, with bounds checking.
func asciiDigitAt(rs []rune, i int) bool {
	return i >= 0 && i < len(rs) && rs[i] >= '0' && rs[i] <= '9'
}

// stripLeaderDots removes dot-leader filler that a glyph-interleaved leader fused into a
// reconstructed table cell's text. Some producers (the USDA NASS crop tables) render a row label
// and its trailing dot leader as interleaved glyphs, so wordsFromBand + mergeAbuttingWords weld
// them into one token ("Alaba...m...a...." for "Alabama") that trimDotLeaders cannot drop (it is
// not a pure-dot word). The repair fires ONLY when the cell carries the fused-leader signature —
// a >= leaderDotRunFloor dot run flanked by letters on both sides (hasInterleavedLeaderRun) — then
// drops every '.' that is not a decimal point (flanked by an ASCII digit on BOTH sides), so the
// fused leader dots vanish while legitimate data text is left verbatim: "U.S." and "823.1" (no
// qualifying run), a TRAILING ellipsis "continued...", a digit range "1...3", and a version
// "1.2.3" all fail the gate and pass through unchanged. A cell that fails the gate returns with no
// allocation, and a cell that is ENTIRELY dot-leader is preserved as-is (matching trimDotLeaders's
// dot-only contract — no irreversible erasure). This is table-path only (reconstructGrid);
// Words()/Lines() text extraction is untouched, so every corpus .golden.txt stays byte-identical.
func stripLeaderDots(s string) string {
	if !hasInterleavedLeaderRun(s, leaderDotRunFloor) {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		if r == '.' && (!asciiDigitAt(rs, i-1) || !asciiDigitAt(rs, i+1)) {
			continue // fused dot-leader filler (not a decimal point) — drop
		}
		b.WriteRune(r)
	}
	// The gate guarantees a letter flanks the run on both sides, so the label letters always
	// survive the strip — the result is never empty (a fully-dot cell fails the gate above).
	return b.String()
}

// decodableWords keeps only words carrying decodable text that are not dot-leader filler.
// A word that is entirely replacement characters (e.g. an undecodable per-row leader run set
// in a symbol font) anchors no extractable content, and a tabular dot-leader run (>=4 consecutive
// '.' and nothing else) is page typography, not data — both must not seed or fabricate a
// synthesized open column.
func decodableWords(ws []Word) []Word {
	var out []Word
	for _, w := range ws {
		if hasDecodableRune(w.S) && !isDotLeader(w.S) {
			out = append(out, w)
		}
	}
	return out
}

// synthOpenColumns synthesizes a full-height open anchor column on each side whose outer words
// carry decodable text AND whose frame horizontals overhang the inner boundary at BOTH
// data-body extremes. Two anti-fabrication guards bind it to a real column: the overhang test
// (edgeOverhangsLeft/Right) — a page number, footnote, or margin rule cannot fabricate a
// phantom column — and the decodable-words filter — an undecodable per-row leader run (all
// U+FFFD) cannot either. Each synthesized column is later split into rows by the shared band set
// exactly like the closed columns, so it clears the minOpenRows hurdle that rejected it as a
// single unruled band.
func synthOpenColumns(full []lCell, words []Word, hEdges []lEdge, media [4]float64, dataTop, tableBot float64) []lCell {
	vMin, vMax := colBounds(full)
	llx, urx := media[0], media[2]
	var out []lCell
	leftWords := decodableWords(openSideWords(words, dataTop, tableBot, func(ax float64) bool { return ax < vMin }))
	if len(leftWords) > 0 && edgeOverhangsLeft(hEdges, dataTop, vMin) && edgeOverhangsLeft(hEdges, tableBot, vMin) {
		if x0 := clampLo(minWordLeft(leftWords)-openPad, llx); vMin-x0 >= minOpenColW {
			out = append(out, lCell{x0: x0, top: dataTop, x1: vMin, bottom: tableBot, open: true})
		}
	}
	rightWords := decodableWords(openSideWords(words, dataTop, tableBot, func(ax float64) bool { return ax > vMax }))
	if len(rightWords) > 0 && edgeOverhangsRight(hEdges, dataTop, vMax) && edgeOverhangsRight(hEdges, tableBot, vMax) {
		if x1 := clampHi(maxWordRight(rightWords)+openPad, urx); x1-vMax >= minOpenColW {
			out = append(out, lCell{x0: vMax, top: dataTop, x1: x1, bottom: tableBot, open: true})
		}
	}
	return out
}

// rowBands clusters the data-body word Y-centers across ALL full-height columns into one shared
// set of row centers, so every column splits at the same rows (an aligned grid).
func rowBands(full []lCell, words []Word) []float64 {
	var ys []float64
	for _, c := range full {
		ys = append(ys, wordYCentersIn(words, c)...)
	}
	return cluster1D(ys, rectRowGapTol)
}

// rowAligned reports whether the inferred row bands are shared ACROSS the full-height columns
// (a genuine data grid) rather than independent per-column text flow (framed multi-column
// prose — a banded notice or boxed two-column sidebar). It requires a MAJORITY of bands to
// carry words in >=2 columns: in a data table every row spans columns at the same Y; in a
// two-column prose box each line sits in one column at its own Y, so few bands are cross-column.
// This is the framed-multi-column-prose A4 false-positive guard.
//
// It counts ONLY words whose anchor lies inside a full-height cell on BOTH axes (wordYCentersIn
// — the same set that produced the bands). Binding to in-cell words is essential: a plain
// nearest-band assignment over every page word would let body text above/below the data body —
// or a neighbouring region sharing an X range — be mapped onto a band by nearestIdx and fake
// cross-column alignment, defeating the guard.
func rowAligned(full []lCell, words []Word, bands []float64) bool {
	if len(bands) == 0 {
		return false
	}
	cols := make([]map[int]struct{}, len(bands))
	for i := range cols {
		cols[i] = map[int]struct{}{}
	}
	for ci, c := range full {
		for _, ay := range wordYCentersIn(words, c) {
			cols[nearestIdx(bands, ay)][ci] = struct{}{}
		}
	}
	crossed := 0
	for _, set := range cols {
		if len(set) >= 2 {
			crossed++
		}
	}
	return crossed*2 >= len(bands)
}

// splitCellAtBands tiles cell c into one sub-cell per row band, cutting at the midpoints between
// consecutive band centers (the first sub-cell starts at c.top, the last ends at c.bottom). Each
// sub-cell inherits c's x-extent AND c.open, so columns (and their open/recovered provenance) are
// preserved and the shared cut lines keep tops aligned across columns for reconstructGrid's row
// clustering. Provenance matters here: inferRectBorderedRows appends synthOpenColumns' open
// cells to `full` and then runs every cell in `full` (including those) through this splitter —
// dropping c.open here silently re-exposed the already-fixed open cells to rule-snapping.
func splitCellAtBands(c lCell, bands []float64) []lCell {
	if len(bands) < 2 {
		return []lCell{c}
	}
	out := make([]lCell, 0, len(bands))
	top := c.top
	for i, center := range bands {
		bottom := c.bottom
		if i < len(bands)-1 {
			bottom = (center + bands[i+1]) / 2
		}
		out = append(out, lCell{x0: c.x0, top: top, x1: c.x1, bottom: bottom, open: c.open})
		top = bottom
	}
	return out
}

// --- grid reconstruction ---

// cluster1D returns the sorted cluster-mean representatives of vals (single-linkage on
// consecutive gap <= tol), mirroring snapEdges' clustering.
func cluster1D(vals []float64, tol float64) []float64 {
	if len(vals) == 0 {
		return nil
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	var reps []float64
	i := 0
	for i < len(s) {
		j := i + 1
		sum, n := s[i], 1
		for j < len(s) && s[j]-s[j-1] <= tol {
			sum += s[j]
			n++
			j++
		}
		reps = append(reps, sum/float64(n))
		i = j
	}
	return reps
}

func nearestIdx(reps []float64, v float64) int {
	best, bi := math.Abs(reps[0]-v), 0
	for i, r := range reps {
		if d := math.Abs(r - v); d < best {
			best, bi = d, i
		}
	}
	return bi
}

// joinReading joins words in reading order: cluster into line-bands by top (top-origin),
// order bands top-to-bottom, within a band order by x, join with single spaces.
func joinReading(ws []Word) string {
	type lw struct {
		top, x float64
		s      string
	}
	items := make([]lw, len(ws))
	for i, w := range ws {
		items[i] = lw{-(w.Y + w.H/2), w.X, w.S}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].top < items[j].top })
	var lines [][]lw
	for _, it := range items {
		if len(lines) == 0 {
			lines = append(lines, []lw{it})
			continue
		}
		last := lines[len(lines)-1]
		if it.top-last[len(last)-1].top > 5 {
			lines = append(lines, []lw{it})
		} else {
			lines[len(lines)-1] = append(last, it)
		}
	}
	var parts []string
	for _, ln := range lines {
		sort.SliceStable(ln, func(i, j int) bool { return ln[i].x < ln[j].x })
		for _, it := range ln {
			parts = append(parts, it.s)
		}
	}
	return strings.Join(parts, " ")
}

// wordJoinGapTol (points) bounds the inter-word horizontal gap below which two same-row words
// are treated as one fragmented token. The true discriminator is gap <= 0 (the runs touch or
// overlap — the fragmented NASS values overlap at gap -0.03); the small positive margin only
// absorbs float noise. A genuine word space advances the pen well past this, and distinct column
// values are a whole column's padding apart, so the re-join is a no-op on correct cells: two
// words can only reach this gap when they are fragments of one token (or a degenerate zero-padding
// layout that renders as a single visual run anyway).
const wordJoinGapTol = 0.25

// mergeAbuttingWords re-joins words that a zero-advance space glyph fragmented mid-token before
// reconstructGrid assigns them to cells. wordsFromBand emits a new Word at every isAllSpace glyph;
// some producers lay a token out with an embedded space carrying zero horizontal advance — a
// comma value as "2,1"<sp>"20" (the motivating NASS case), or a char-spaced word as "addr"<sp>"ess"
// — so one token arrives as two abutting Words (the fragments overlap or touch). Assigned by center, the
// fragments fall into adjacent cells and bleed across the column boundary (and even within one
// cell joinReading would wedge a stray space into the number). Candidates are words whose row band
// lies within the table's vertical extent; X is intentionally unbounded so a right-aligned value
// in an OPEN edge column whose trailing fragment overflows the synthesized column boundary still
// re-joins (the gap condition, not an X bound, is what keeps the merge from reaching unrelated
// same-row text). Candidates are clustered into row bands (joinReading's 5pt tolerance), each band
// sorted by X, then consecutive words with gap <= wordJoinGapTol are concatenated WITHOUT a space,
// left-to-right — UNLESS a real vertical rule (vRules) separates them: in a ruled table two
// distinct column values can abut at the cell border with zero whitespace padding, so a rule
// between the words forbids the merge (ruleBetween). A genuine fragment pair overlaps (gap < 0),
// and a rule can never lie between two overlapping boxes, so this guard never blocks the bug it
// targets. The input slice is not mutated (Tables() reuses words across lattices);
// cells/colReps/rowReps are untouched, so on a no-op table the grid shape is byte-identical.
func mergeAbuttingWords(cells []lCell, words []Word, vRules []lEdge) []Word {
	if len(words) < 2 || len(cells) == 0 {
		return words
	}
	btop, bbot := cells[0].top, cells[0].bottom
	for _, c := range cells {
		btop, bbot = min(btop, c.top), max(bbot, c.bottom)
	}
	topOf := func(w Word) float64 { return -(w.Y + w.H/2) }
	var cand, out []Word
	for _, w := range words {
		// Words outside the table's vertical extent, and dot-leader fillers, pass through
		// untouched: a dot-leader must stay a separate word so trimDotLeaders can drop it
		// (TestReconstructGridDropsDotLeaderInAnchor) rather than be welded onto a label.
		if ay := topOf(w); ay >= btop && ay <= bbot && !isDotLeader(w.S) {
			cand = append(cand, w)
		} else {
			out = append(out, w)
		}
	}
	if len(cand) < 2 {
		return words
	}
	sort.SliceStable(cand, func(i, j int) bool { return topOf(cand[i]) < topOf(cand[j]) })
	for i := 0; i < len(cand); {
		j := i + 1
		for j < len(cand) && topOf(cand[j])-topOf(cand[i]) <= 5 { // row band
			j++
		}
		out = appendMergedBand(out, cand[i:j], vRules)
		i = j
	}
	return out
}

// appendMergedBand sorts one row band by X and appends its words to out, concatenating each run of
// consecutive abutting words (gap <= wordJoinGapTol, no real rule between) into a single token.
func appendMergedBand(out, band []Word, vRules []lEdge) []Word {
	sort.SliceStable(band, func(a, b int) bool { return band[a].X < band[b].X })
	cur := band[0]
	for k := 1; k < len(band); k++ {
		w := band[k]
		if w.X-(cur.X+cur.W) <= wordJoinGapTol && !ruleBetween(cur, w, vRules) {
			cur = unionWord(cur, w)
			continue
		}
		out = append(out, cur)
		cur = w
	}
	return append(out, cur)
}

// unionWord concatenates w's text onto cur (the band is X-ascending, so this is reading order) and
// widens cur's bounding box to cover both.
func unionWord(cur, w Word) Word {
	cur.S += w.S
	left := math.Min(cur.X, w.X)
	right := math.Max(cur.X+cur.W, w.X+w.W)
	bottom := math.Min(cur.Y, w.Y)
	top := math.Max(cur.Y+cur.H, w.Y+w.H)
	cur.X, cur.W = left, right-left
	cur.Y, cur.H = bottom, top-bottom
	return cur
}

// ruleBetween reports whether a real vertical rule separates left from right: a v-edge whose x
// lies in [left.right, right.left] and whose span covers left's row. Two abutting distinct column
// values in a ruled table are divided by such a rule, so the re-join must not cross it. Overlapping
// fragments (right.left < left.right) yield an empty x-interval, so a genuine split is never blocked.
func ruleBetween(left, right Word, vRules []lEdge) bool {
	lr := left.X + left.W
	ay := -(left.Y + left.H/2) // top-origin row of the left word
	for _, e := range vRules {
		if e.x0 >= lr && e.x0 <= right.X && e.top <= ay && ay <= e.bottom {
			return true
		}
	}
	return false
}

// verticalRules returns the stroked vertical rules of c (the same edge pool latticeTablesOpen
// builds), consumed by mergeAbuttingWords to forbid welding values a real column rule separates.
func verticalRules(c Content) []lEdge {
	var v []lEdge
	for _, e := range mergeEdges(edgesFromContent(c), 3, 3) {
		if e.orient == 'v' {
			v = append(v, e)
		}
	}
	return v
}

// ruleSpanFrac is the minimum fraction of a table's own vertical extent that a page vertical
// rule must overlap before reconstructGrid treats it as a genuine column-boundary rule (a
// "governing" rule) rather than a decorative header-only box line. Real column dividers in
// single-axis-ruled tables (full-height verticals, no horizontal rules between data rows)
// overlap most of the table's height even when they stop short of a merged multi-tier header;
// decorative per-cell header box lines cover only the header band. See
// plans/decisions/SINGLE-AXIS-RULED-TRIAGE-2026-07-01.md and the Item 1 spike verdict.
const ruleSpanFrac = 0.5

// ruleSnapTol is how close (pt) a cell's own x0 must sit to a table's governing rule x-position
// before reconstructGrid snaps that cell onto the rule for column-identity purposes. Wider than
// colClusterTol (4.0pt) because a header label's own content x0 can sit several points inside
// its true rule-bounded column. Calibrated against the single-axis-ruled fixtures (observed
// offsets ranged 5.3-10.3pt across publishers).
const ruleSnapTol = 12.0

// governingLeftSlack is the tolerance (pt) by which a candidate rule may sit to the RIGHT of a
// cell's own x0 and still be considered "at" that x0 rather than strictly to its right. It
// exists only to absorb sub-point rounding jitter (a cell's own x0 landing a hair left of its
// true governing rule due to float quantization) — it must stay much smaller than ruleSnapTol
// so it never lets a genuinely different (neighboring) rule qualify as this cell's own left
// boundary. See snapToGoverningRule's directional-boundary rationale (Item 1 fix, defect (2)).
const governingLeftSlack = 0.75

// governingVRules filters a page-wide vertical-rule pool (vRules is page-scoped: the same
// slice is passed to reconstructGrid for every table on the page) down to the x-positions that
// actually define THIS table's own column structure: within the table's own x-bounds (risk:
// vRules pollution from another table/page-border on the same page) and overlapping at least
// ruleSpanFrac of THIS table's own y-extent (risk: a rule whose vertical span is narrower than
// the table — e.g. present only in the data body under a merged multi-tier header, or a
// decorative box line confined to one header row — must be judged against the table's actual
// row geometry, not assumed full-height). Returns clustered representative x-positions, or nil
// if no rule qualifies (callers must leave column banding untouched in that case).
func governingVRules(vRules []lEdge, cells []lCell) []float64 {
	if len(vRules) == 0 || len(cells) == 0 {
		return nil
	}
	vMin, vMax := colBounds(cells)
	tableTop, tableBot := cellYSpan(cells)
	tableH := tableBot - tableTop
	if tableH <= 0 {
		return nil
	}
	const xTol = 2.0
	var xs []float64
	for _, e := range vRules {
		if e.orient != 'v' {
			continue
		}
		if e.x0 < vMin-xTol || e.x0 > vMax+xTol {
			continue // outside this table's own column bounds — a different table/page region
		}
		ovTop, ovBot := max(e.top, tableTop), min(e.bottom, tableBot)
		if ovBot-ovTop < ruleSpanFrac*tableH {
			continue // too short vertically for THIS table to be a governing column divider
		}
		xs = append(xs, e.x0)
	}
	if len(xs) == 0 {
		return nil
	}
	return cluster1D(xs, colClusterTol)
}

// snapToGoverningRule resolves x's actual governing LEFT boundary among ruleXs and returns
// that rule's x-position, or x unchanged if none governs it: the closest rule AT OR TO THE LEFT
// of x (its left wall) — never a rule that sits to the right, however close, because that rule
// belongs to the NEXT column (Defect (2) in the spike verdict doc's §5 adversarial-review
// findings — choosing by raw absolute distance, either side, is what let a single real column's
// cells flip between two adjacent rules and checkerboard-split, confirmed on
// destatis-erzeugerpreise-dez2022.pdf p19 table[64], case B9). governingLeftSlack absorbs only
// sub-point rounding, so a rule fractionally right of x from quantization still qualifies, but a
// rule genuinely to the right (the next column's own boundary) never does.
//
// Callers, not this function, decide WHETHER a given x is even eligible to be snapped at all:
// see reconstructGrid's per-RAW-CLUSTER sparse/dense gate (Defect found in the follow-up
// hkcsd-monthly-digest-2024-01.pdf p169 investigation: two independently real, 21/21-row-
// coverage columns sitting 9pt apart were both "close enough" to trigger a snap despite neither
// needing one) and the open/closed separation in mergeColumnReps (Defect (1): an open/recovered
// cell, from recoverOpenColumns/synthOpenColumns, is never passed to this function at all).
func snapToGoverningRule(x float64, ruleXs []float64) float64 {
	if len(ruleXs) == 0 {
		return x
	}
	best, found := math.Inf(-1), false
	for _, rx := range ruleXs {
		if rx > x+governingLeftSlack {
			continue // to the right of x — the NEXT column's boundary, never this one's
		}
		if rx > best {
			best, found = rx, true
		}
	}
	if found && x-best <= ruleSnapTol {
		return best
	}
	return x
}

// sparseRowCoverageFrac is the row-coverage fraction (of the table's total distinct rows) below
// which a raw (unsnapped) column cluster is treated as a sparse, possibly-anomalous artifact
// (e.g. a header/label row whose own cell x0 differs from the data body's established column)
// and is therefore eligible for governing-rule reconciliation. At or above this fraction, a
// cluster is treated as an independently-established real column and is NEVER snapped onto a
// nearby rule, however close. This closes the hkcsd-monthly-digest-2024-01.pdf p169 defect: two
// genuinely distinct columns, EACH with 21/21 (100%) row coverage, sat 9.12pt apart — well
// within ruleSnapTol(12) of the same governing rule — and a naive per-cell/per-cluster snap
// (with no coverage awareness) collapsed one into the other, gluing a footnote marker onto the
// wrong neighboring column's value. Calibrated against real data: the Jordan 14.7-shaped
// (jo-dos-health-2023.pdf p6/p7) header-artifact clusters needing reconciliation had 1-2 rows out
// of 12-17 (8-17%); the dense columns they reconcile onto had 11-17 rows (92-100%). 0.5 sits with
// a wide margin on both sides of that real-data gap.
const sparseRowCoverageFrac = 0.5

// clusterRowSets returns, for each representative in reps, the SET of distinct row bands
// (indices into rowReps, via nearestIdx) among the cells at the given indices that map to it —
// i.e. which of the table's rows this column cluster actually appears in. Used to gate
// governing-rule snapping to sparse clusters only (sparseRowCoverageFrac, via each set's size).
//
// NOTE on a discarded alternative: an earlier version of this fix also vetoed a snap whenever
// the sparse cluster and its snap target shared a row (reasoning: they must then be two
// genuinely distinct, simultaneous columns, not alternate different-row representations of one
// column). That is REFUTED by the Jordan 14.7-shaped header case (jo-dos-health-2023.pdf p6
// table[0]): the sparse header-label cluster and the dense data-column cluster it must merge
// into DO share row 0 (the header row itself splits into two adjacent raw cells — one empty,
// one holding the header text) — yet the merge is required to reach the correct 7-column result.
// Row co-occurrence alone cannot distinguish "a header row split into two adjacent pieces of the
// same column" from "two genuinely different side-by-side columns" — clusterHasContent (content-
// emptiness) is the guard that actually holds; see there.
func clusterRowSets(cells []lCell, idx []int, reps []float64, rowReps []float64) []map[int]bool {
	seen := make([]map[int]bool, len(reps))
	for i := range seen {
		seen[i] = map[int]bool{}
	}
	for _, ci := range idx {
		c := cells[ci]
		ri := nearestIdx(reps, c.x0)
		seen[ri][nearestIdx(rowReps, c.top)] = true
	}
	return seen
}

// clusterRowCoverage returns the size of each set from clusterRowSets — how many of the table's
// rows each column cluster appears in.
func clusterRowCoverage(rowSets []map[int]bool) []int {
	cov := make([]int, len(rowSets))
	for i, m := range rowSets {
		cov[i] = len(m)
	}
	return cov
}

// clusterHasContent reports, for each representative in reps, whether ANY cell (among the given
// indices) mapping to it contains at least one word — i.e. whether this raw column cluster
// carries real content anywhere, as opposed to being a purely geometric sliver with nothing in
// it. Governing-rule snapping is gated on this (alongside sparseRowCoverageFrac): a sparse
// cluster is only worth reconciling with a nearby rule when it actually holds text that needs to
// reunite with its true column (e.g. a header label whose own cell geometry differs from the
// data rows beneath it) — a purely EMPTY sparse sliver has nothing to reconcile, and forcing a
// snap on it can pull it out of an unrelated run of adjacent thin/empty columns that the
// pre-existing dropGutterColumns/mergeNestedColumns cascade (unrelated to this Item 1 fix) relies
// on to consolidate step-by-step. Confirmed on eGRID2022 p1's cover-frame box: snapping an empty
// geometric sliver onto an unrelated empty gutter cluster (both otherwise harmless in isolation)
// broke that cascade and left one extra phantom column behind (4×4 instead of the correct 4×3).
func clusterHasContent(cells []lCell, idx []int, reps []float64, words []Word) []bool {
	byCluster := make([][]lCell, len(reps))
	for _, ci := range idx {
		c := cells[ci]
		ri := nearestIdx(reps, c.x0)
		byCluster[ri] = append(byCluster[ri], c)
	}
	has := make([]bool, len(reps))
	for i, cs := range byCluster {
		for _, c := range cs {
			if wordInBox(words, c.x0, c.x1, c.top, c.bottom) {
				has[i] = true
				break
			}
		}
	}
	return has
}

// mergeColumnReps merges SEPARATELY-clustered closed and open column representative
// x-positions into one sorted, reading-order column list, without ever letting an open
// (recovered/synthesized) cluster's identity collapse into — or absorb — a closed (rule-
// anchored) cluster's identity merely because their representative x-values land within
// colClusterTol of each other. Clustering the two provenances in one cluster1D call (as an
// earlier version of this fix did) reopens exactly the defect Fix (1)/(2) close at the cell
// level: a protected open cell's raw x0 could still be chained into a neighboring closed
// column's cluster by single-linkage proximity, silently undoing the protection one step later.
// Returns the merged, x-sorted representative list plus, for each input slice, the merged-list
// index its entry landed at: closedFinal[j] is the final column index for closedReps[j],
// openFinal[j] for openReps[j].
func mergeColumnReps(closedReps, openReps []float64) (colReps []float64, closedFinal, openFinal []int) {
	type repEntry struct {
		x    float64
		open bool
		idx  int
	}
	entries := make([]repEntry, 0, len(closedReps)+len(openReps))
	for i, x := range closedReps {
		entries = append(entries, repEntry{x, false, i})
	}
	for i, x := range openReps {
		entries = append(entries, repEntry{x, true, i})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].x < entries[j].x })
	colReps = make([]float64, len(entries))
	closedFinal = make([]int, len(closedReps))
	openFinal = make([]int, len(openReps))
	for fi, e := range entries {
		colReps[fi] = e.x
		if e.open {
			openFinal[e.idx] = fi
		} else {
			closedFinal[e.idx] = fi
		}
	}
	return colReps, closedFinal, openFinal
}

// reconstructGrid bands a table's cells into (row,col) by their own bbox geometry (top->row,
// x0->col — the banding IS the geometric mapping) and fills each cell with the reading-order
// join of the words geometrically contained in it. vRules are the page's vertical rules (empty
// in unit tests that build cells directly); mergeAbuttingWords uses them to avoid welding across
// a real column rule. Column identity (x0->col) is additionally anchored to this table's own
// governing rule x-positions (governingVRules/snapToGoverningRule) so a header label whose
// content x0 differs from the data value's x0 beneath it — but which shares the same
// rule-bounded column — is not split into a phantom extra column (single-axis-ruled fix).
//
// Three provenance/eligibility layers close the defects found across this fix's two rounds of
// verification (spike verdict doc §5 + the follow-up hkcsd investigation):
//
//   - Open (recovered/synthesized, c.open) cells are clustered SEPARATELY from closed cells and
//     never snapped (Fix 1/mergeColumnReps): an open cell's raw x0 could otherwise chain into a
//     neighboring closed column merely by falling within colClusterTol.
//   - Closed cells are clustered RAW (unsnapped) first — rawClosedReps, identical to pre-fix
//     behavior — and governing-rule reconciliation is decided ONCE PER RAW CLUSTER, never per
//     individual cell: this is what stops one real column's rows from flipping between two
//     nearby rules row-by-row (the destatis-erzeugerpreise-dez2022.pdf p19 table[64]
//     checkerboard, case B9).
//   - A raw closed cluster is only ELIGIBLE for that reconciliation when its row-coverage is
//     sparse relative to the table (sparseRowCoverageFrac): a dense, already-established column
//     is never snapped onto a nearby rule however close, which is what stops two independently
//     real, fully-populated columns from collapsing into each other (hkcsd-monthly-digest-
//     2024-01.pdf p169 table[0]: two 21/21-row columns 9pt apart, both "close enough" under a
//     naive per-cluster snap despite neither needing one).
//
// Every cell's stored x0 (in `snapped`) is the FINAL colReps value for its own column, so every
// downstream consumer that maps a cell back to a column via plain nearestIdx(colReps, cell.x0)
// (dropGutterColumns/mergeNestedColumns and the columnWidths/columnLeafX1 helpers they call)
// resolves to an EXACT (distance-0) match — Fix (3) — rather than re-deriving column membership
// by raw distance, which could re-admit the same cross-provenance/cross-cluster ambiguity.
// resolveGridColumns computes final column identity for every entry in cells: closed and open
// cells are clustered SEPARATELY (mergeColumnReps), and a raw closed cluster is reconciled onto
// governingVRules only when it is BOTH sparse (sparseRowCoverageFrac) AND carries real content
// (clusterHasContent) — see reconstructGrid's doc for what each of these three provenance/
// eligibility layers closes. Returns the final column x-representatives (colReps), each cell's
// column index by index into cells (cellCol), and cells with x0 snapped to their final column
// (snapped, consumed by dropGutterColumns/mergeNestedColumns per the Fix (3) note above).
func resolveGridColumns(cells []lCell, words []Word, ruleXs, rowReps []float64) (colReps []float64, cellCol []int, snapped []lCell) {
	nRows := len(rowReps)

	var closedIdx, openIdx []int
	var closedXs, openXs []float64
	for i, c := range cells {
		if c.open {
			openIdx = append(openIdx, i)
			openXs = append(openXs, c.x0)
		} else {
			closedIdx = append(closedIdx, i)
			closedXs = append(closedXs, c.x0)
		}
	}

	// Closed cells: raw cluster first, then a per-cluster (never per-cell) governing-rule
	// reconciliation pass, gated to clusters that are BOTH sparse AND carry real content — see
	// the function doc above and clusterHasContent's doc for what each guard closes.
	rawClosedReps := cluster1D(closedXs, colClusterTol)
	rowSets := clusterRowSets(cells, closedIdx, rawClosedReps, rowReps)
	rowCoverage := clusterRowCoverage(rowSets)
	hasContent := clusterHasContent(cells, closedIdx, rawClosedReps, words)
	resolvedClosed := make([]float64, len(rawClosedReps))
	for i, x := range rawClosedReps {
		resolvedClosed[i] = x // default: unchanged
		if nRows > 0 && float64(rowCoverage[i]) >= sparseRowCoverageFrac*float64(nRows) {
			continue // dense/established column — never snapped, however close a rule is
		}
		if !hasContent[i] {
			continue // purely empty geometric sliver — nothing to reconcile; leave it for
			// dropGutterColumns/mergeNestedColumns, which already consolidate pure-empty runs
			// on their own (clusterHasContent doc)
		}
		resolvedClosed[i] = snapToGoverningRule(x, ruleXs)
	}
	// Re-cluster the resolved values: a sparse cluster snapped onto a rule may now coincide
	// (exactly or near-exactly) with the dense cluster already anchored there — merge them.
	closedReps := cluster1D(resolvedClosed, colClusterTol)
	rawClosedToClosed := make([]int, len(rawClosedReps))
	for i, x := range resolvedClosed {
		rawClosedToClosed[i] = nearestIdx(closedReps, x)
	}

	// Open cells: clustered separately, never snapped (Fix 1).
	openReps := cluster1D(openXs, colClusterTol)

	colReps, closedFinal, openFinal := mergeColumnReps(closedReps, openReps)

	cellCol = make([]int, len(cells)) // final column index per cell, by index into `cells`
	snapped = make([]lCell, len(cells))
	for _, ci := range closedIdx {
		c := cells[ci]
		ri := nearestIdx(rawClosedReps, c.x0)
		fi := closedFinal[rawClosedToClosed[ri]]
		cellCol[ci] = fi
		snapped[ci] = c
		snapped[ci].x0 = colReps[fi] // exact colReps entry — see Fix (3) note above
	}
	for _, oi := range openIdx {
		c := cells[oi]
		ri := nearestIdx(openReps, c.x0)
		fi := openFinal[ri]
		cellCol[oi] = fi
		snapped[oi] = c
		snapped[oi].x0 = colReps[fi]
	}
	return colReps, cellCol, snapped
}

func reconstructGrid(cells []lCell, words []Word, vRules ...lEdge) [][]string {
	words = mergeAbuttingWords(cells, words, vRules) // re-join zero-advance-space-fragmented tokens
	ruleXs := governingVRules(vRules, cells)

	tops := make([]float64, len(cells))
	for i, c := range cells {
		tops[i] = c.top
	}
	rowReps := cluster1D(tops, 4)

	colReps, cellCol, snapped := resolveGridColumns(cells, words, ruleXs, rowReps)

	grid := make([][]string, len(rowReps))
	for i := range grid {
		grid[i] = make([]string, len(colReps))
	}
	bucket := map[[2]int][]Word{}
	var placed []placedWord // words mapped by the primary center anchor (for the weld pass)
	var misses []Word       // words whose center fell outside every cell
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2) // top-origin anchor
		matched := false
		for ci, c := range cells {
			if ax >= c.x0 && ax <= c.x1 && ay >= c.top && ay <= c.bottom {
				r := nearestIdx(rowReps, c.top)
				cc := cellCol[ci]
				key := [2]int{r, cc}
				bucket[key] = append(bucket[key], w)
				placed = append(placed, placedWord{w: w, key: key, cellX1: c.x1})
				matched = true
				break
			}
		}
		if !matched {
			misses = append(misses, w)
		}
	}
	weldStraddlingDigits(bucket, placed, misses, cells, rowReps)
	for key, ws := range bucket {
		grid[key[0]][key[1]] = stripLeaderDots(joinReading(trimDotLeaders(ws)))
	}
	grid = dropGutterColumns(grid, snapped, colReps)
	return mergeNestedColumns(grid, snapped, colReps)
}

// placedWord records a word the primary center anchor mapped, with its (row,col) cell key and that
// cell's right edge (cellX1), so the weld pass can find the nearest left neighbour already living in
// a cell and verify a candidate truly straddles that cell's right wall.
type placedWord struct {
	w      Word
	key    [2]int
	cellX1 float64
}

// weldStraddlingDigits re-attaches a center-miss DIGIT group to a numeric cell it overflows on the
// right. The motivating defect: when a table's data is typeset on a wider pitch than its ruled
// columns, a space-thousands number straddles its cell's right wall, so the trailing group's CENTER
// lands outside the cell and the primary pass drops it (cz-czso p477: "66 315" → "66"). The weld
// recovers such a group, but ONLY under a deliberately narrow predicate, because a corpus A/B sweep
// proved a permissive "any token within colClusterTol of the left neighbour" rule mass-produces
// false positives (label words fused into number cells, dot-leader periods, chart-axis fragments,
// character-level interleave). Every gate below was added to kill a measured FP class:
//
//   - w.S is ALL DIGITS — a numeric continuation, never a label/dot-leader/unit token.
//   - the LEFT neighbour (anchor) is itself an all-digit word — we only extend a number, and only
//     when the gap to it is within colClusterTol (an intra-number space, never a column boundary).
//   - w STRADDLES the anchor cell's right wall — left edge inside (w.X < cellX1), center outside —
//     so a digit lying WHOLLY outside the cell (a standalone count, footnote marker, or adjacent
//     narrow digit column) cannot weld merely for abutting the anchor word.
//   - w is the cell's NEW RIGHTMOST token — no word already in the cell lies to w's right — so a
//     group is only ever appended to a number's tail, never inserted mid-cell (the interleave class).
//
// Purely additive and structure-preserving: only center-misses are considered, a weld only grows a
// cell that already holds the all-digit anchor, no empty cell becomes non-empty, and the column set
// dropGutterColumns/mergeNestedColumns operate on is unchanged. Word bounding boxes are untouched
// (the weld is downstream of column derivation), so Words()/Lines() are unaffected — the very layer
// an earlier word-level weld attempt was rejected for perturbing. No chaining: a welded token never
// anchors another, so a contaminated fragment cannot cascade.
func weldStraddlingDigits(bucket map[[2]int][]Word, placed []placedWord, misses []Word, cells []lCell, rowReps []float64) {
	if len(misses) == 0 || len(placed) == 0 {
		return
	}
	for _, w := range misses {
		if !isAllDigits(w.S) {
			continue
		}
		r := rowOfCenter(cells, rowReps, -(w.Y + w.H/2))
		if r < 0 {
			continue
		}
		anchor, ok := nearestPlacedLeft(placed, r, w.X)
		if !ok || !isAllDigits(anchor.w.S) || w.X-(anchor.w.X+anchor.w.W) > colClusterTol {
			continue
		}
		// w must genuinely STRADDLE the anchor cell's right wall: its left edge inside the cell
		// (w.X < cellX1) and its center outside (w.X+w.W/2 > cellX1). Without this a digit lying
		// WHOLLY outside the cell — a standalone count, a footnote marker, or an adjacent narrow
		// digit column with a sub-colClusterTol gutter — would weld merely for abutting the anchor
		// word, corrupting a clean value. The straddle ties the weld to the actual overflow geometry.
		if w.X >= anchor.cellX1 || w.X+w.W/2 <= anchor.cellX1 {
			continue
		}
		// w must become the cell's rightmost token (append to the number's tail, never mid-cell).
		if cellHasWordRightOf(placed, anchor.key, w.X) {
			continue
		}
		bucket[anchor.key] = append(bucket[anchor.key], w)
	}
}

// isAllDigits reports whether s is non-empty and every rune is an ASCII digit.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// rowOfCenter returns the row index of the cell band whose vertical extent contains the top-origin
// y-center ay, or -1 if none — used to row-align a center-miss word with placed words.
func rowOfCenter(cells []lCell, rowReps []float64, ay float64) int {
	for _, c := range cells {
		if ay >= c.top && ay <= c.bottom {
			return nearestIdx(rowReps, c.top)
		}
	}
	return -1
}

// nearestPlacedLeft returns the placed word in row r whose right edge is closest to (and not past)
// x, plus whether one was found.
func nearestPlacedLeft(placed []placedWord, r int, x float64) (placedWord, bool) {
	bestRight := math.Inf(-1)
	var best placedWord
	ok := false
	for _, p := range placed {
		if p.key[0] != r {
			continue
		}
		if pr := p.w.X + p.w.W; pr <= x && pr > bestRight {
			bestRight, best, ok = pr, p, true
		}
	}
	return best, ok
}

// cellHasWordRightOf reports whether any placed word in cell key has a right edge past x — i.e.
// welding a token at x would land it mid-cell rather than at the cell's tail.
func cellHasWordRightOf(placed []placedWord, key [2]int, x float64) bool {
	for _, p := range placed {
		if p.key == key && p.w.X+p.w.W > x {
			return true
		}
	}
	return false
}

// A gutter column (a thin cell from a double-wall decorative border rect) is dropped only
// when it is all-empty AND below BOTH of these gates — a column failing either is preserved:
//
//   - gutterFraction (relative): widest cell < gutterFraction × the table's median
//     data-column width. Targets the actual gutter property — thin RELATIVE to siblings —
//     so it is scale-robust across document sizes.
//   - absoluteGutterCap (absolute ceiling): widest cell < absoluteGutterCap pt. A hard
//     bound so a real, merely-narrow data column is never dropped just because wide sibling
//     columns inflate the median (the relative-gate-only false-positive class).
//
// Measured on all committed fixtures (2026-06-18):
//   - True gutter widths (double-wall border rects): 4.99–13.02 pt (EPA p1)
//   - Narrowest real data columns: 24.65 pt (ERP tables), 28.44 pt (HHS-ASPE)
//   - Normal data columns: 34–647 pt range
//
// The cap (16 pt) sits between max-gutter (13.02) and narrowest-real-column (24.65) with
// margin on both sides; the relative gate (0.25) additionally guards uniformly-narrow tables.
// A column must fall under both to be a gutter — far outside any observed real layout.
const (
	gutterFraction    = 0.25
	absoluteGutterCap = 16.0
)

// colClusterTol is the single-linkage tolerance used to cluster cell x0 positions into column
// representatives (reconstructGrid: cluster1D(x0s, colClusterTol)). It doubles as the
// span-interior margin in spanContainsRealColumn. Because cluster1D is single-linkage gap-based,
// a legitimately edge-sharing column's cluster-mean colRep can drift INWARD from the shared rule
// by up to ~one tolerance (members chain across gaps ≤ tol). Requiring a non-empty rep to lie
// MORE than colClusterTol inside the empty column's span therefore distinguishes a genuinely
// distinct interior sub-column (the mis-split spanning-cell signature) from a mere edge-sharing
// neighbour whose mean jittered inward. Tying the margin to the column cluster tolerance keeps
// the drop predicate self-consistent with how the columns were clustered in the first place.
const colClusterTol = 4.0

const (
	// nestedWallTol is the maximum distance (pt) between the x1 right-edges of two adjacent
	// grid-columns that still qualifies as a shared wall. Normal adjacent columns share a
	// boundary (x1[i] == x0[i+1]); nested sub-cell pairs share the SAME x1 — this tolerance
	// distinguishes them.
	nestedWallTol = 3.0
	// phantomMaxSparseCells is the maximum number of non-empty cells (over ALL rows) that the
	// sparser member of a candidate merge-pair may hold. A phantom header column carries only
	// the header label — at most one line (or two for a two-line wrapped header), so ≤2 is
	// generous. A data-bearing column carries many more cells and is never merged away.
	// Documented reopen: a 3+-line wrapped-header phantom will NOT merge (sparse limit = 2);
	// this is loss-free (no worse than today) and the reopen trigger is a confirmed real-world
	// fixture with a triple-line header phantom. The x0/left-aligned shared-wall variant also
	// never fired on real data and is a documented reopen.
	phantomMaxSparseCells = 2
	// phantomMinDataCells is the minimum non-empty cell count (over ALL rows) the DENSE partner of
	// a sparse phantom must hold for a merge to fire. Without it, a SMALL table whose columns are
	// trivially "sparse" (≤2 cells just because the table has ≤2 data rows) would be over-merged —
	// the EPA p1 false positive (a 2-row complementary table, both columns ≤2 cells, no real
	// phantom). Requiring the data partner to carry ≥3 cells confirms a genuine (header + data)
	// doubling. Loss-free reopen: a real phantom whose data column has <3 rows will not merge.
	phantomMinDataCells = 3
)

// dropGutterColumns removes columns that are entirely empty and meet at least one of:
//
//  1. Thin by BOTH the relative (gutterFraction × median data-column width) and absolute
//     (absoluteGutterCap) gates — a gutter cell from a double-wall decorative border rect.
//
//  2. Structural spanning-cell phantom: the empty column's drawn x-span (colReps[cc],
//     leafX1[cc]) strictly contains a non-empty column's representative x. This fires when
//     a real vertical rule mis-splits a wide spanning cell, producing a phantom empty
//     "column" that is too wide for condition 1 but encloses real sub-columns inside it.
//
// A legitimately empty data column has normal width and no sub-column inside its span, so
// it passes neither condition. The grid is returned unchanged when no column qualifies, or
// when dropping would leave no columns (degenerate frame guard).
func dropGutterColumns(grid [][]string, cells []lCell, colReps []float64) [][]string {
	if len(grid) == 0 || len(colReps) == 0 {
		return grid
	}
	colW := columnWidths(cells, colReps)
	empty := emptyColumns(grid, len(colReps))
	median, ok := medianDataWidth(colW, empty)
	if !ok {
		return grid // no data columns to derive a threshold from — leave unchanged
	}
	threshold := gutterFraction * median
	leafX1 := columnLeafX1(cells, colReps)
	keep := make([]bool, len(colReps))
	nKeep := 0
	for cc := range colReps {
		// Condition 1: drop if empty AND positive-width AND thin by BOTH the absolute cap
		// and the relative (median-fraction) gate.
		thinGutter := empty[cc] && colW[cc] > 0 && colW[cc] < absoluteGutterCap && colW[cc] < threshold
		// Condition 2: drop if this empty column's drawn span strictly contains a
		// non-empty column's representative x — the signature of a mis-split spanning cell.
		spanPhantom := empty[cc] && spanContainsRealColumn(cc, empty, colReps, leafX1)
		keep[cc] = !thinGutter && !spanPhantom
		if keep[cc] {
			nKeep++
		}
	}
	if nKeep == len(colReps) || nKeep == 0 {
		return grid // nothing to drop, or a drop would empty the grid
	}
	return compactColumns(grid, keep, nKeep)
}

// spanContainsRealColumn reports whether the empty column cc's drawn x-span strictly contains
// some OTHER non-empty column's representative x MORE than one column cluster tolerance inside —
// the geometric signature of a real vertical rule that mis-split a wide spanning cell. The
// interval (colReps[cc]+colClusterTol, leafX1[cc]-colClusterTol) carries a colClusterTol margin
// on BOTH boundaries: an edge-sharing neighbour whose single-linkage cluster-mean drifted inward
// by up to ~one tolerance stays outside the interval, while a genuinely distinct interior
// sub-column (which sits well inside) qualifies. See colClusterTol for the full rationale.
// leafX1 is sized to colReps by columnLeafX1, so the length guard below is defensive.
func spanContainsRealColumn(cc int, empty []bool, colReps, leafX1 []float64) bool {
	n := len(colReps)
	if len(empty) < n || len(leafX1) < n {
		return false
	}
	lo := colReps[cc] + colClusterTol
	hi := leafX1[cc] - colClusterTol
	for j := range colReps {
		if j != cc && !empty[j] && colReps[j] > lo && colReps[j] < hi {
			return true
		}
	}
	return false
}

// columnWidths returns the widest cell width per column, keyed by nearestIdx into
// colReps. The widest-cell representative is conservative: a column counts as thin
// only when even its widest cell is thin.
func columnWidths(cells []lCell, colReps []float64) []float64 {
	colW := make([]float64, len(colReps))
	for _, c := range cells {
		cc := nearestIdx(colReps, c.x0)
		if w := c.x1 - c.x0; w > colW[cc] {
			colW[cc] = w
		}
	}
	return colW
}

// emptyColumns reports, per column index, whether every row is empty at that column.
func emptyColumns(grid [][]string, nCols int) []bool {
	empty := make([]bool, nCols)
	for cc := range empty {
		empty[cc] = true
	}
	for _, row := range grid {
		for cc, cell := range row {
			if cc < nCols && cell != "" {
				empty[cc] = false
			}
		}
	}
	return empty
}

// medianDataWidth returns the median width over the non-empty (data) columns and
// whether any data column exists.
func medianDataWidth(colW []float64, empty []bool) (float64, bool) {
	var dataWidths []float64
	for cc, w := range colW {
		if cc < len(empty) && !empty[cc] {
			dataWidths = append(dataWidths, w)
		}
	}
	if len(dataWidths) == 0 {
		return 0, false
	}
	slices.Sort(dataWidths)
	n := len(dataWidths)
	if n%2 == 0 {
		return (dataWidths[n/2-1] + dataWidths[n/2]) / 2, true
	}
	return dataWidths[n/2], true
}

// compactColumns rebuilds grid keeping only the columns marked keep[cc].
func compactColumns(grid [][]string, keep []bool, nKeep int) [][]string {
	out := make([][]string, len(grid))
	for r, row := range grid {
		kept := make([]string, 0, nKeep)
		for cc, cell := range row {
			if cc < len(keep) && keep[cc] {
				kept = append(kept, cell)
			}
		}
		out[r] = kept
	}
	return out
}

// mergeNestedColumns collapses phantom-doubled adjacent grid-column pairs that arise when a PDF
// producer subdivides each logical column into a WIDE outer cell and a NARROW inner cell sharing
// the same right wall (x1). The cluster step in reconstructGrid keeps both x0-origins (they are
// 10–50 pt apart and never merge under cluster1D), producing a grid where the header label sits in
// the wide phantom column and its numeric data in the adjacent narrow column — a misaligned,
// doubled layout that confuses any downstream consumer.
//
// The three-gate predicate (all required):
//
//  1. SHARED WALL — |x1rep[i] − x1rep[i+1]| ≤ nestedWallTol (right-aligned nested signature).
//     Normal adjacent columns share a BOUNDARY (x1[i] == x0[i+1]); nested sub-cell pairs share
//     the SAME x1. These are distinct: a column boundary is not a shared wall.
//  2. ROW-COMPLEMENTARY — no row r has BOTH grid[r][i] != "" AND grid[r][i+1] != "".
//     Guarantees loss-free merge: the two cells never compete, so no non-empty content (including
//     a header label) is ever dropped.
//  3. PHANTOM/DATA CELL-COUNT ASYMMETRY — min(nonEmpty) ≤ phantomMaxSparseCells (the phantom holds
//     only a ≤2-line header) AND max(nonEmpty) ≥ phantomMinDataCells (the partner is a real data
//     column). The sparse half defeats over-merging two data-rich complementary columns (DESTATIS
//     p5 col19+col20); the dense half defeats over-merging a small table whose columns are trivially
//     sparse (the EPA p1 2-row false positive).
//
// Merge rule: merged[r] = grid[r][i] if non-empty, else grid[r][i+1]. Column i+1 is dropped and
// i's x1rep expands to the union max — subsequent pair checks use the updated geometry.
// Processing is greedy left-to-right; after each merge the scan restarts from the beginning.
//
// Documented reopens:
//   - x0/left-aligned shared-wall variant: not implemented — it did not occur across the validation
//     corpus (all observed phantom pairs are right-aligned, sharing x1); a concrete follow-on.
//   - phantomMaxSparseCells = 2: a 3+-line wrapped-header phantom will not merge (loss-free;
//     no worse than today). Reopen trigger: a confirmed real-world fixture with a 3-line phantom.
//
// The helper is only called when len(grid[0]) == len(colReps) (enforced by the entry guard);
// this holds iff dropGutterColumns dropped no column, which is the case for every table that
// has any shared-wall phantom pair (gutters are thin by definition; phantoms are wide).
func mergeNestedColumns(grid [][]string, cells []lCell, colReps []float64) [][]string {
	if len(grid) == 0 || len(colReps) < 2 {
		return grid
	}
	// Entry guard: grid width must match colReps; if dropGutterColumns compacted the grid,
	// the nearestIdx mapping would desync — skip safely rather than panic.
	if len(grid[0]) != len(colReps) {
		return grid
	}

	// Build per-column leaf x1 (minimum / innermost right-edge) from cells keyed by colReps.
	// Using MIN guards against spanning parent cells forging a false shared-wall signal.
	curLeafX1 := columnLeafX1(cells, colReps)

	// Greedy: find the first mergeable adjacent pair, apply it, and restart the scan (a merge
	// shifts indices and updates geometry). Stop when a full scan finds no mergeable pair.
	curGrid := grid
	for {
		merged := false
		for i := 0; i+1 < len(curLeafX1); i++ {
			if nestedPairMergeable(curGrid, curLeafX1, i, i+1) {
				curGrid, curLeafX1 = applyNestedMerge(curGrid, curLeafX1, i, i+1)
				merged = true
				break
			}
		}
		if !merged {
			break
		}
	}
	return curGrid
}

// nestedPairMergeable reports whether adjacent grid columns i and j (j == i+1) satisfy ALL three
// merge gates: (1) shared LEAF x1 wall — |leafX1[i]−leafX1[j]| ≤ nestedWallTol; gate-1 uses the
// innermost (min) right edge so that a spanning parent cell cannot forge a shared wall over a real
// column boundary; (2) row-complementary (no row has both non-empty); (3) phantom/data cell-count
// asymmetry — one column is a sparse phantom (header only, ≤ phantomMaxSparseCells cells) AND its
// partner is a dense data column (≥ phantomMinDataCells). The sparse half rejects two data-rich
// complementary columns (DESTATIS p5 col19+col20); the dense half rejects a small table whose
// columns are trivially sparse (the EPA p1 2-row false positive).
func nestedPairMergeable(grid [][]string, x1max []float64, i, j int) bool {
	if math.Abs(x1max[i]-x1max[j]) > nestedWallTol {
		return false
	}
	if !nestedColumnsComplementary(grid, i, j) {
		return false
	}
	ni, nj := nestedNonEmpty(grid, i), nestedNonEmpty(grid, j)
	return min(ni, nj) <= phantomMaxSparseCells && max(ni, nj) >= phantomMinDataCells
}

// applyNestedMerge merges column j into column i (j == i+1): each grid row collapses via
// mergeColumnIntoRow (non-empty wins, so no content is lost) and the per-column leafX1 takes the
// union max of the two columns' leafX1 values (the merged column's effective right edge is the
// wider of the two). Returns the grid and leafX1 slice one column narrower.
func applyNestedMerge(grid [][]string, x1max []float64, i, j int) ([][]string, []float64) {
	newNC := len(x1max) - 1
	newGrid := make([][]string, len(grid))
	for r, row := range grid {
		newGrid[r] = mergeColumnIntoRow(row, i, j, newNC)
	}
	newX1max := make([]float64, newNC)
	dst := 0
	for cc := range x1max {
		if cc == j {
			continue // absorbed into i
		}
		newX1max[dst] = x1max[cc]
		dst++
	}
	if x1max[j] > newX1max[i] {
		newX1max[i] = x1max[j]
	}
	return newGrid, newX1max
}

// columnLeafX1 returns the MINIMUM x1 value for each grid column, keyed by nearestIdx(colReps,c.x0).
//
// We use the minimum (innermost / leaf right edge) rather than the maximum to distinguish two cases:
//   - PHANTOM column: its only cell is the wide outer cell that reaches the shared wall → min == max
//     == the shared wall → the shared-wall gate in nestedPairMergeable still fires (intended).
//   - REAL column with a spanning PARENT header: the parent cell has a wide x1 reaching the next
//     column's wall, but the column also has narrower LEAF data cells ending at its own boundary.
//     min x1 = the leaf boundary ≠ the adjacent column's wall → shared-wall gate fails → NO merge
//     (the spanning-header false positive is blocked).
//
// Entries for columns with no mapped cells remain +Inf, so gate-1 (|diff| ≤ nestedWallTol) safely
// fails for unmapped columns.
func columnLeafX1(cells []lCell, colReps []float64) []float64 {
	leafX1 := make([]float64, len(colReps))
	for i := range leafX1 {
		leafX1[i] = math.Inf(1)
	}
	for _, c := range cells {
		cc := nearestIdx(colReps, c.x0)
		if c.x1 < leafX1[cc] {
			leafX1[cc] = c.x1
		}
	}
	return leafX1
}

// nestedColumnsComplementary reports whether columns i and j in grid are row-complementary:
// no single row has both grid[r][i] and grid[r][j] non-empty (after TrimSpace).
// All rows are checked uniformly — no header/data boundary.
func nestedColumnsComplementary(grid [][]string, i, j int) bool {
	for _, row := range grid {
		ci := ""
		if i < len(row) {
			ci = strings.TrimSpace(row[i])
		}
		cj := ""
		if j < len(row) {
			cj = strings.TrimSpace(row[j])
		}
		if ci != "" && cj != "" {
			return false
		}
	}
	return true
}

// nestedNonEmpty counts rows where column cc has non-empty text (TrimSpace), over ALL rows.
func nestedNonEmpty(grid [][]string, cc int) int {
	n := 0
	for _, row := range grid {
		if cc < len(row) && strings.TrimSpace(row[cc]) != "" {
			n++
		}
	}
	return n
}

// mergeColumnIntoRow merges the source column j into column i within a single grid row,
// returning a new row of width newNC. Non-empty wins; if both are somehow non-empty
// (guarded against by row-complementary but kept for safety), they are space-joined.
func mergeColumnIntoRow(row []string, i, j, newNC int) []string {
	newRow := make([]string, newNC)
	for cc, cell := range row {
		if cc == j {
			// Merge j into i: already placed above (i < j, so i was already written).
			ci := strings.TrimSpace(newRow[i])
			cj := strings.TrimSpace(cell)
			switch {
			case ci == "":
				newRow[i] = cj
			case cj == "" || ci == cj:
				// keep newRow[i] as-is
			default:
				newRow[i] = ci + " " + cj
			}
			continue
		}
		d := cc
		if cc > j {
			d = cc - 1
		}
		newRow[d] = strings.TrimSpace(cell)
	}
	return newRow
}

// trimDotLeaders drops tabular dot-leader filler words from a cell's word set, but ONLY when the
// cell also holds real (non-leader) content. A leader visually connects a label to its value, so
// in a real leader cell the trim leaves the label/value behind; a cell whose ENTIRE content is a
// dot run is preserved verbatim rather than silently erased to empty (irreversible data loss).
// The common cases — a cell with no leader (every corpus table) or a dot-only cell — return the
// input slice unchanged with no allocation; only a genuine mixed leader+content cell allocates.
func trimDotLeaders(ws []Word) []Word {
	anyLeader, anyReal := false, false
	for _, w := range ws {
		if isDotLeader(w.S) {
			anyLeader = true
		} else {
			anyReal = true
		}
	}
	if !anyLeader || !anyReal {
		return ws // nothing to trim, or a dot-only cell to preserve — no allocation
	}
	out := make([]Word, 0, len(ws)-1)
	for _, w := range ws {
		if !isDotLeader(w.S) {
			out = append(out, w)
		}
	}
	return out
}
