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
type lCell struct{ x0, top, x1, bottom float64 }

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
				return lCell{pt.x, pt.y, r.x, b.y}, true
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
	var hEdges []lEdge
	for _, e := range edges {
		if e.orient == 'h' {
			hEdges = append(hEdges, e)
		}
	}
	tables := latticeTables(c) // closed-only, unchanged
	for i := range tables {
		tables[i] = append(tables[i], recoverOpenColumns(tables[i], words, hEdges, media)...)
		tables[i] = inferRectBorderedRows(tables[i], words, hEdges, media)
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
		admitted = append(admitted, lCell{x0: x0, top: rowTops[i], x1: x1, bottom: rowTops[i+1]})
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
			out = append(out, lCell{x0: x0, top: dataTop, x1: vMin, bottom: tableBot})
		}
	}
	rightWords := decodableWords(openSideWords(words, dataTop, tableBot, func(ax float64) bool { return ax > vMax }))
	if len(rightWords) > 0 && edgeOverhangsRight(hEdges, dataTop, vMax) && edgeOverhangsRight(hEdges, tableBot, vMax) {
		if x1 := clampHi(maxWordRight(rightWords)+openPad, urx); x1-vMax >= minOpenColW {
			out = append(out, lCell{x0: vMax, top: dataTop, x1: x1, bottom: tableBot})
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
// sub-cell inherits c's x-extent, so columns are preserved and the shared cut lines keep tops
// aligned across columns for reconstructGrid's row clustering.
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
		out = append(out, lCell{x0: c.x0, top: top, x1: c.x1, bottom: bottom})
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

// reconstructGrid bands a table's cells into (row,col) by their own bbox geometry (top->row,
// x0->col — the banding IS the geometric mapping) and fills each cell with the reading-order
// join of the words geometrically contained in it. vRules are the page's vertical rules (empty
// in unit tests that build cells directly); mergeAbuttingWords uses them to avoid welding across
// a real column rule.
func reconstructGrid(cells []lCell, words []Word, vRules ...lEdge) [][]string {
	words = mergeAbuttingWords(cells, words, vRules) // re-join zero-advance-space-fragmented tokens
	tops := make([]float64, len(cells))
	x0s := make([]float64, len(cells))
	for i, c := range cells {
		tops[i] = c.top
		x0s[i] = c.x0
	}
	rowReps := cluster1D(tops, 4)
	colReps := cluster1D(x0s, 4)
	grid := make([][]string, len(rowReps))
	for i := range grid {
		grid[i] = make([]string, len(colReps))
	}
	bucket := map[[2]int][]Word{}
	for _, w := range words {
		ax := w.X + w.W/2
		ay := -(w.Y + w.H/2) // top-origin anchor
		for _, c := range cells {
			if ax >= c.x0 && ax <= c.x1 && ay >= c.top && ay <= c.bottom {
				r := nearestIdx(rowReps, c.top)
				cc := nearestIdx(colReps, c.x0)
				key := [2]int{r, cc}
				bucket[key] = append(bucket[key], w)
				break
			}
		}
	}
	for key, ws := range bucket {
		grid[key[0]][key[1]] = joinReading(trimDotLeaders(ws))
	}
	return dropGutterColumns(grid, cells, colReps)
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

// dropGutterColumns removes columns that are entirely empty AND thin by BOTH the relative
// (gutterFraction × median data-column width) and absolute (absoluteGutterCap) gates — a
// gutter cell from a double-wall decorative border rect, e.g. on a report cover or nav
// frame. A legitimately empty data column has normal width (relative to its neighbours and
// in absolute terms) and is preserved. The grid is returned unchanged when no column
// qualifies, or when dropping would leave no columns (degenerate frame guard).
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
	keep := make([]bool, len(colReps))
	nKeep := 0
	for cc := range colReps {
		// Drop only if empty AND positive-width AND thin by BOTH the absolute cap
		// and the relative (median-fraction) gate.
		gutter := empty[cc] && colW[cc] > 0 && colW[cc] < absoluteGutterCap && colW[cc] < threshold
		keep[cc] = !gutter
		if keep[cc] {
			nKeep++
		}
	}
	if nKeep == len(colReps) || nKeep == 0 {
		return grid // nothing to drop, or a drop would empty the grid
	}
	return compactColumns(grid, keep, nKeep)
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
