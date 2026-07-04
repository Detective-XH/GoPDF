package pdf

// tables_lattice_core.go — closed-lattice detection core (Stage 1 — see plans/ARCHITECTURE.md §2).
//
// This is a spec-port of pdfplumber's "lines" strategy (snap=join=intersection=3 pt).
// The pipeline: edges (strokes + rects) → merge (snap + join) → intersections → cells →
// tables. Only fully rule-closed cells exist after this stage.
//
// CONTRACT: this file owns the geometric types (lEdge/lCell/pointKey/inter) and the edge/
// intersection/cell pipeline, plus the small helpers genuinely shared across stages
// (openSideWords/minWordLeft/maxWordRight/clampLo/clampHi/unplacedWords/isDotLeader/
// hasDecodableRune/decodableWords/verticalRules). The recovery passes live in
// tables_recover_open.go (sequencer + open columns), tables_recover_rows.go (rect-bordered/
// shaded-sibling/fill-banded/header-ruled), tables_recover_comb.go (comb body); grid assembly
// in tables_grid.go; cleanup in tables_grid_cleanup.go. Same-package cross-file calls are
// free; a symbol's file states its stage ownership, not its visibility.

import (
	"math"
	"slices"
	"sort"
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
