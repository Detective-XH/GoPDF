package pdf

// tables_lattice.go — internal lattice (ruled-table) detection engine.
//
// This is a spec-port of pdfplumber's "lines" strategy (snap=join=intersection=3 pt)
// extended with a structural open-column recovery gate. The pipeline is:
//   edges (strokes + rects) → merge (snap + join) → intersections → cells → tables
// plus an optional per-table open-column recovery step (latticeTablesOpen).

import (
	"math"
	"sort"
	"strings"
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
		for _, y := range b {
			if x == y {
				return true
			}
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

// reconstructGrid bands a table's cells into (row,col) by their own bbox geometry (top->row,
// x0->col — the banding IS the geometric mapping) and fills each cell with the reading-order
// join of the words geometrically contained in it.
func reconstructGrid(cells []lCell, words []Word) [][]string {
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
		grid[key[0]][key[1]] = joinReading(ws)
	}
	return grid
}
