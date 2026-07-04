package pdf

// Row-recovery passes (Stage 2 passes 2-5 — see plans/ARCHITECTURE.md §2): rect-bordered,
// shaded-sibling, fill-banded (incl. BEA staircase), and header-ruled-data synthesis.
// CONTRACT: each pass is additive on []lCell and reads the AUGMENTED tables[i] (post prior
// passes); inferShadedSiblingRows takes its donor-provenance pool from rectOnlyHEdges(fillRects)
// — Y-snapped-only fill-paint evidence, never X-joined edges and never raw Content.Rect
// (fill/stroke/clip conflation; PR #112). KNOWN latent gap (BACKLOG.md): inferFillBandedRows/BEA
// still consume raw c.Rect (paint-provenance-blind) — an upgrade to fillRects is
// extraction-changing and gated. Pass order is owned by latticeTablesOpen
// (tables_recover_open.go).

import (
	"math"
	"slices"
	"sort"
)

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
	rowReps := cluster1D(tops, rectRowGapTol) // the row tolerance reconstructGrid also uses
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
// The tolerances (rectRowSnapTol, rectMinRowClusters, rectMinRowSplit, rectRowGapTol,
// rectMinBodyFrac, headerRuledMinCols, headerRuledMinColBands) live in tables_tolerances.go.

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

// --- shaded-sibling row inference (column-selective alternating-row shading) ---
//
// The problem: alternating-row shading applied to only SOME conceptual columns of a table
// causes those columns' fill-rects to become real per-row lattice cells, while sibling columns
// with no shading arrive as one tall cell spanning the whole shaded span — inferRectBorderedRows
// never engages because its own guards (splitFullHeight's whole-table-bottom predicate,
// interiorHRuleCount seeing the shaded siblings' own rules as "this table is already ruled")
// are not column-aware. This pass is additive and narrowly scoped: it borrows an already
// row-closed sibling column's row Y-boundaries onto a same-table, same-y-span tall cell,
// gated by a per-instance consequence invariant (never a shape/content discriminator) plus a
// donor-provenance requirement described below.
//
// A first version of this pass (reverted, see the spike-verdict doc) accepted ANY column with
// >= rectMinRowClusters contiguous already-split same-x0 cells as a valid donor, regardless of
// WHY those cells were already split. That regressed a real table: a wrapped 3-line column
// HEADER, sitting beside an unrelated header sub-column that happened to already be split into
// the same row count for its own (stroke-ruled) reasons, got torn into per-line row-cells. The
// donor-provenance gate below closes exactly that gap: a stack only qualifies as a donor when
// EVERY one of its cells' top AND bottom boundaries coincides with an edge that itself derives
// from a Content.Rect FILL operation (rectOnlyHEdges) — never from the general merged
// stroke+rect edge pool, and never from an ordinary ruling stroke. A stroke-ruled header split
// fails this gate; a genuine alternating-shading-derived stack passes it.
//
// Known, NOT-yet-closed residual risk (see the spike-verdict doc's follow-on note): a
// genuinely shading-derived donor stack paired with a RECIPIENT that is a real, legitimate
// row-spanning label/footnote cell whose wrapped text happens to fall one line per shaded band
// would pass both this gate and the consequence invariant, and get wrongly split. No known fix
// exists for that case yet; each round that changes this pass must explicitly sweep for it.
// The tolerances (shadedSiblingColTol, shadedSiblingRectSpanTol) live in tables_tolerances.go.

// inferShadedSiblingRows borrows an already row-closed sibling column's row boundaries onto a
// same-table, same-y-span tall cell that a shading-column-selective layout left uncollapsed.
// Returns cells unchanged when no donor stack qualifies for any candidate recipient.
func inferShadedSiblingRows(cells []lCell, words []Word, rectH []lEdge) []lCell {
	// A qualifying donor run needs >= rectMinRowClusters stacked cells, which needs
	// >= rectMinRowClusters+1 distinct rect-derived h-edge Y-positions (N cells share N+1
	// boundaries). Below that, no run can possibly qualify — skip the O(cells log cells)
	// column-grouping pass entirely rather than paying it just to find nothing. This keeps
	// the common case (a page with a handful of unrelated fill-rects, no real shading donor)
	// cheap: measured to remove most of this pass's added cost on BenchmarkTables' stroke-
	// bordered fixture, which has no shading donor but was still paying the full scan.
	if len(cells) == 0 || len(rectH) < rectMinRowClusters+1 {
		return cells
	}
	var donors [][]lCell
	for _, g := range shadedSiblingColumnGroups(cells) {
		donors = append(donors, shadedSiblingStackRuns(g, rectH)...)
	}
	if len(donors) == 0 {
		return cells
	}
	donorCells := make(map[lCell]bool)
	for _, run := range donors {
		for _, c := range run {
			donorCells[c] = true
		}
	}
	out := make([]lCell, 0, len(cells))
	for _, cell := range cells {
		if cell.open || donorCells[cell] {
			out = append(out, cell)
			continue
		}
		if split, ok := shadedSiblingBestSplit(cell, donors, words); ok {
			out = append(out, split...)
			continue
		}
		out = append(out, cell)
	}
	return out
}

// shadedSiblingColumnGroups clusters cells into column groups by x0 (tol shadedSiblingColTol),
// each group sorted top-to-bottom, ready for shadedSiblingStackRuns to scan for donor stacks.
func shadedSiblingColumnGroups(cells []lCell) [][]lCell {
	xs := make([]float64, len(cells))
	for i, c := range cells {
		xs[i] = c.x0
	}
	reps := cluster1D(xs, shadedSiblingColTol)
	groups := make([][]lCell, len(reps))
	for _, c := range cells {
		idx := nearestIdx(reps, c.x0)
		groups[idx] = append(groups[idx], c)
	}
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool { return g[i].top < g[j].top })
	}
	return groups
}

// shadedSiblingStackRuns scans one x0-clustered, top-sorted column group for maximal
// contiguous runs of >= rectMinRowClusters vertically-abutting cells (bottom[i] within
// rectRowSnapTol of top[i+1]) whose every cell's top AND bottom boundary is donor-provenance
// verified (shadedSiblingRunIsRectDerived). Non-qualifying runs (too short, or provenance
// fails) contribute no donor.
func shadedSiblingStackRuns(group []lCell, rectH []lEdge) [][]lCell {
	var runs [][]lCell
	i := 0
	for i < len(group) {
		j := i + 1
		for j < len(group) && math.Abs(group[j].top-group[j-1].bottom) <= rectRowSnapTol {
			j++
		}
		if j-i >= rectMinRowClusters && shadedSiblingRunIsRectDerived(group[i:j], rectH) {
			runs = append(runs, group[i:j])
		}
		i = j
	}
	return runs
}

// shadedSiblingRunIsRectDerived is the donor-provenance gate: every cell in run must have both
// its top and its bottom boundary trace back to a Content.Rect fill operation, not merely to
// the general merged stroke+rect edge pool (which any ordinary ruling stroke also populates).
func shadedSiblingRunIsRectDerived(run []lCell, rectH []lEdge) bool {
	for _, c := range run {
		if !shadedSiblingBoundaryIsRectDerived(c.top, c.x0, c.x1, rectH) ||
			!shadedSiblingBoundaryIsRectDerived(c.bottom, c.x0, c.x1, rectH) {
			return false
		}
	}
	return true
}

// shadedSiblingBoundaryIsRectDerived reports whether some rect-only h-edge sits within
// rectRowSnapTol of y AND whose own x-span matches [x0,x1] within shadedSiblingRectSpanTol —
// i.e. the fill rect actually BOUNDS this specific cell (a genuine per-row shading fill), not
// merely a wider unrelated rect (a table frame/background) that happens to overlap this
// column's x-range at this Y. A plain overlap test is deliberately NOT used here; see
// shadedSiblingRectSpanTol's doc comment for the real-world false-match this rejects.
//
// Known limitation (false-negative only, no observed case in either committed corpus): this is
// an exact single-edge span match, not a coverage match. If a PDF producer emits one donor
// cell's own boundary as several adjacent same-Y fill rects (e.g. [0,10]+[10,20] for a [0,20]
// cell) instead of one rect spanning the full cell, none of the segments matches [x0,x1] alone
// and the donor is missed — never wrongly split, since a narrower segment can only fail the
// match, not spuriously satisfy a wider one. Coverage-matching (accept several same-Y edges
// whose union covers [x0,x1]) would recover this but needs its own gap/overhang tolerance,
// calibrated against a real example this codebase has not yet seen — deferred rather than
// adding an uncalibrated threshold ahead of evidence.
func shadedSiblingBoundaryIsRectDerived(y, x0, x1 float64, rectH []lEdge) bool {
	for _, e := range rectH {
		if math.Abs(e.top-y) <= rectRowSnapTol &&
			math.Abs(e.x0-x0) <= shadedSiblingRectSpanTol && math.Abs(e.x1-x1) <= shadedSiblingRectSpanTol {
			return true
		}
	}
	return false
}

// shadedSiblingBoundaries returns the run's N+1 row-boundary Y positions (donor cell tops
// followed by every donor cell's bottom), ready to cut a same-span recipient into N sub-cells
// at the EXACT donor boundaries (no clustering/midpoint reconstruction needed — the donor
// stack's own cell edges already ARE the boundaries).
func shadedSiblingBoundaries(run []lCell) []float64 {
	b := make([]float64, 0, len(run)+1)
	b = append(b, run[0].top)
	for _, c := range run {
		b = append(b, c.bottom)
	}
	return b
}

// shadedSiblingSpanMatches reports whether cell's own [top,bottom] matches the donor run's
// overall span within rectRowSnapTol — the recipient must be a genuine same-table-section
// sibling of the donor, not merely some other tall cell elsewhere on the page.
func shadedSiblingSpanMatches(cell lCell, run []lCell) bool {
	top, bot := run[0].top, run[len(run)-1].bottom
	return math.Abs(cell.top-top) <= rectRowSnapTol && math.Abs(cell.bottom-bot) <= rectRowSnapTol
}

// shadedSiblingBestSplit tries every donor run whose span matches cell, in stable column/top
// order, and returns the first one whose consequence invariant (shadedSiblingTrySplit) commits.
func shadedSiblingBestSplit(cell lCell, donors [][]lCell, words []Word) ([]lCell, bool) {
	for _, run := range donors {
		if !shadedSiblingSpanMatches(cell, run) {
			continue
		}
		if split, ok := shadedSiblingTrySplit(cell, shadedSiblingBoundaries(run), words); ok {
			return split, true
		}
	}
	return nil, false
}

// shadedSiblingTrySplit is the per-instance consequence invariant (unchanged from the prior,
// independently-verified spike — see the spike-verdict doc's Task 1): commit a candidate split
// of cell at boundaries only if (a) every resulting band holds >=1 word (lossless) and (b) no
// natural row-of-data — cell's own words, single-linkage clustered by rectRowGapTol, the same
// tolerance/algorithm cellRowClusters/rowBands use elsewhere in this file — has its members
// land in more than one borrowed band (non-destructive).
func shadedSiblingTrySplit(cell lCell, boundaries []float64, words []Word) ([]lCell, bool) {
	if len(boundaries) < rectMinRowClusters+1 {
		return nil, false
	}
	ys := shadedSiblingCellWordYs(words, cell)
	if len(ys) == 0 {
		return nil, false
	}
	counts, ok := shadedSiblingBandCounts(ys, boundaries)
	if !ok {
		return nil, false
	}
	if slices.Contains(counts, 0) {
		return nil, false // every band must hold >=1 word (lossless)
	}
	out := make([]lCell, 0, len(boundaries)-1)
	for i := 0; i < len(boundaries)-1; i++ {
		out = append(out, lCell{x0: cell.x0, top: boundaries[i], x1: cell.x1, bottom: boundaries[i+1], open: cell.open})
	}
	return out, true
}

// shadedSiblingCellWordYs returns the sorted Y-centers of words anchored inside cell c.
func shadedSiblingCellWordYs(words []Word, c lCell) []float64 {
	ys := wordYCentersIn(words, c)
	sort.Float64s(ys)
	return ys
}

// shadedSiblingBandOf returns the index of the band in boundaries (len(boundaries)-1 bands,
// band i spanning [boundaries[i],boundaries[i+1])) that y falls into, clamping to the nearest
// end band for a y that lands fractionally outside due to float rounding.
func shadedSiblingBandOf(y float64, boundaries []float64) int {
	if y < boundaries[0] {
		return 0
	}
	for i := 0; i < len(boundaries)-1; i++ {
		if y < boundaries[i+1] {
			return i
		}
	}
	return len(boundaries) - 2
}

// shadedSiblingBandCounts single-linkage clusters ys by rectRowGapTol (matching cellRowClusters'
// own algorithm) and returns, per band, how many clusters landed SOLELY within that band. ok is
// false the moment any single cluster's members span more than one band (a natural row-of-data
// would be torn by the borrowed boundaries — the non-destructive half of the invariant).
func shadedSiblingBandCounts(ys []float64, boundaries []float64) (counts []int, ok bool) {
	counts = make([]int, len(boundaries)-1)
	ok = true
	i := 0
	for i < len(ys) {
		j := i + 1
		for j < len(ys) && ys[j]-ys[j-1] <= rectRowGapTol {
			j++
		}
		b0 := shadedSiblingBandOf(ys[i], boundaries)
		for k := i + 1; k < j; k++ {
			if shadedSiblingBandOf(ys[k], boundaries) != b0 {
				ok = false
			}
		}
		counts[b0]++
		i = j
	}
	return counts, ok
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

// rectOnlyHEdges returns h-edges derived from fillRects — the fill-provenance-verified subset of
// Content.Rect (see latticeTablesOpen's fillRects param / contentState.fillRect), never from
// Content.Stroke line segments and never from a merely-constructed-but-unfilled re rectangle.
// The general hEdges pool (mergeEdges(edgesFromContent(c), ...)) merges Rect and Stroke
// together indiscriminately — exactly right for detecting a table's structural rules but wrong
// for inferShadedSiblingRows's donor-provenance gate: that gate must tell "this sibling
// column's row split comes from a real alternating-row SHADING fill" apart from "this sibling
// column's row split comes from an ordinary ruling stroke, or a stroked-only/unpainted re
// rectangle that Content.Rect alone cannot distinguish from a fill" (see the shaded-sibling doc
// comment above inferShadedSiblingRows for the real-world regression this distinguishes).
//
// Deliberately snaps Y (snapEdges) but does NOT join X-spans (joinEdges) the way the general
// hEdges pool does: joinEdges extends a fill edge's x-span across any OTHER collinear,
// touching-or-near fill edge at the same Y — exactly wrong for this gate, whose whole signal IS
// each individual donor cell's own x-span. Two adjacent genuinely-shaded sibling columns sharing
// a row boundary would otherwise have their edges joined into one wide edge before
// shadedSiblingBoundaryIsRectDerived ever sees them, so BOTH donors would fail their own x-span
// match — a false negative (a real fix silently not firing), never a false positive (joining can
// only WIDEN a span, which makes the x-span-match check strictly harder to satisfy, never easier
// — so this cannot manufacture a spurious donor match either way). rectOnlyHEdges is this gate's
// only consumer; the shared structural hEdges/vEdges pool (which DOES need joining, e.g. for
// thin-rect ruling lines) is computed separately in latticeTablesOpen and is unaffected.
func rectOnlyHEdges(fillRects []Rect) []lEdge {
	snapped := snapEdges(edgesFromContent(Content{Rect: fillRects}), 3)
	var h []lEdge
	for _, e := range snapped {
		if e.orient == 'h' && e.length() >= 3.0 { // edge_min_length, matching mergeEdges' own filter
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
