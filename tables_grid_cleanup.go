package pdf

// Grid cleanup passes (Stage 3 tail — see plans/ARCHITECTURE.md §2): post-assembly column
// hygiene on the reconstructed grid. CONTRACT: these passes consume the assembled grid +
// snapped cells + colReps from reconstructGrid and only ever DROP or MERGE columns/cells —
// they never synthesize content. Order (dropGutterColumns → mergeNestedColumns) is a contract.
// Tolerances here are calibrated constants (see plans/C3-LATTICE-CLARIFICATION-PLAN.md Stage 2);
// new predicates corroborate against existing tolerance helpers (xCorroborated), never fresh
// epsilons (VERIFICATION.md §7).

import (
	"math"
	"slices"
	"sort"
	"strings"
)

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
