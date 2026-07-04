package pdf

// Grid assembly (Stage 3 — see plans/ARCHITECTURE.md §2): turns a table's lCells + page words
// into the final [][]string grid. CONTRACT: word placement is center-anchor into snapped cells;
// column identity resolves via resolveGridColumns (closed/open clustered separately; sparse+
// has-content clusters snap to governing rules; open cells never snap); weldStraddlingDigits
// runs after placement and only re-attaches all-digit straddles. Consumes page-wide pools
// (vRules) computed by the caller; produces the grid the cleanup passes (tables_grid_cleanup.go)
// then prune. Order within reconstructGrid is a contract.

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

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
