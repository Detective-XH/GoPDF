package pdf

import (
	"math"
	"sort"
)

// Table is one reconstructed ruled ("lattice") table on a page: a rectangular grid
// of cell strings. Cells[r][c] holds the text of the cell in row r, column c; an empty
// cell is the empty string. Rows run top-to-bottom, columns left-to-right in page order.
//
// Experimental: the API is additive-evolving (see API-STABILITY.md). Cells is the stable
// core; Table may gain fields in a future minor release, and the detection output may still
// change as extraction quality stabilizes. See EXAMPLES.md.
type Table struct {
	// Cells is the reconstructed grid indexed Cells[row][col]. Every row has equal length.
	// FROZEN: semantics and field type will not change in the v0.x line (TABLES-API-SHAPE.md).
	Cells [][]string

	// Confidence is a coarse, detection-relative quality level for this table.
	// Low if at least one Warning fired; High otherwise.
	// High means "nothing flagged it" — NOT "verified correct". See TableConfidence.
	Confidence TableConfidence

	// Warnings lists the quality flags detected for this table, in detection order.
	// Callers must tolerate unknown TableWarningCode values. An empty slice means no
	// quality problem was detected by the current detector set (not that the table is correct).
	Warnings []TableWarning
}

// Tables reconstructs the ruled ("lattice") tables on the page as grids of cell strings.
//
// It builds a grid from the page's ruling lines — both stroked lines (Content.Stroke)
// and thin filled rectangles (Content.Rect) — recovers half-open edge columns whose outer
// vertical rule is absent but whose row rules overhang into them (the row-label and last
// data columns common in statistical tables), and assigns each word to its cell by
// geometric containment.
//
// Documented scope: ruled lattices — interior cells closed by a visible rule between
// adjacent rows AND adjacent columns, plus the half-open edge columns recovered below (a
// row-label or last-data column whose outer vertical rule is absent but whose row rules
// overhang the inner vertical). On that scope reconstruction is locked against regression
// by corpus accuracy gates (the determinism promise in API-STABILITY.md). Outside it the
// result is best-effort, not a contract:
//
//   - A borderless table (no closing rules) yields no Table.
//   - A partially-ruled or banded table — ruled only at group boundaries, with rows
//     separated by shading rather than rules (common in statistical tables) — may yield
//     no Table or a structurally incomplete or merged grid. Treat such a grid as advisory.
//
// An open edge column is recovered only where the table's row rules overhang into it; a
// column whose rules stop at the inner vertical is not recovered (a safe omission).
//
// Phantom columns introduced by decorative or banded ruling are removed so they do not surface
// as spurious empty columns. Three structural drops apply: (1) a thin, entirely empty column
// from a decorative double-wall border rule (the two parallel walls of a frame, common on report
// cover and navigation pages) is dropped, gated on column width — both relative to the table's
// median data column and an absolute ceiling; (2) a normal-width all-empty column whose drawn
// span encloses another column's representative position is dropped as a mis-split spanning cell,
// regardless of width; (3) in a banded table whose header background is two side-by-side filled
// rectangles, the seam between them is not treated as a column rule, so it neither splits a value
// nor adds a phantom column. A genuine empty data column, and a genuine grouped header carrying
// real per-column sub-labels, are left intact; the documented limit is that a real data column
// that is both narrower than the width ceiling and entirely blank on the page may be dropped by (1).
//
// A space-separated thousands group whose trailing part overflows its ruled column — the value
// typeset slightly wider than the column, so the trailing group straddles the column's right rule —
// is re-attached to the number it continues, so the cell keeps the whole value instead of truncating
// it. Only an all-digit trailing group is recovered; non-numeric text is never pulled across a rule.
//
// Verbatim caveat: a superscript renders at a distinct vertical position and font size, so
// it extracts as a spaced token (for example "cm²" becomes "cm 2"). This is specific to
// Y-offset glyph transitions, not a general spacing artifact; cell content — the right
// value in the right cell — is unaffected. A run of four or more leader dots ('.') that
// visually connects a row label to its value is filler, not data, and is dropped from cell text;
// a cell whose only content is such a run is preserved as-is. When a dot leader's glyphs are
// interleaved with the label's own letters — so the leader fuses into the label token rather than
// forming a separate run, as in some statistical row labels — the fused filler dots are likewise
// stripped from that cell, recovering the label. Only this fused signature (a run of three or more
// dots flanked by a letter on BOTH sides) triggers the strip, so legitimate data text is left
// verbatim: decimal points, abbreviations ("U.S."), a trailing ellipsis ("continued..."), and a
// dot-separated range ("1...3") are all preserved.
//
// Diagonal text — a watermark or rotated decoration whose baseline is more than ~10° off an
// axis — is excluded from cell content: such glyphs overlap data cells spatially and would
// otherwise fuse into cell values. Axis-aligned text is always kept, including vertical and
// landscape text at 90°/270°. This filtering is specific to Tables; Page.Words, Page.Lines, and
// Page.Blocks call Page.Content independently and return every glyph unfiltered.
//
// A landscape table embedded on a portrait page via a 90°-CCW text matrix — common in
// statistical yearbooks — is remapped to reading orientation before reconstruction, so its
// cells come out de-reversed and in the correct row/column order rather than character-reversed
// and transposed. This remap is internal to Tables: Page.Words, Page.Lines, and Page.Blocks
// still report such text in its raw rotated page geometry.
//
// Experimental: the detection geometry and the Table type are additive-evolving, and the
// reconstruction output may still change as extraction quality is stabilized across the
// real-world table distribution (see API-STABILITY.md). Tables returns the same error as
// Words. See EXAMPLES.md for usage.
func (p Page) Tables() ([]Table, error) {
	results, err := p.reconstructTables()
	if err != nil {
		return nil, err
	}
	return tableResultsToTables(results), nil
}

// tableResultsToTables projects the internal shared reconstruction results into the public
// []Table: each result's grid, its rolled-up Confidence, and its detected Warnings. This is
// the exact field-population wiring Tables() exposes, factored out so it is testable end to
// end (a synthetic phantom result must surface Warnings + Confidence Low through this path).
func tableResultsToTables(results []tableResult) []Table {
	tables := make([]Table, len(results))
	for i, r := range results {
		tables[i] = Table{
			Cells:      r.grid,
			Confidence: rollupConfidence(r.warnings),
			Warnings:   r.warnings,
		}
	}
	return tables
}

// rollupConfidence derives a table's Confidence from its detected Warnings: Low if any
// Warning fired, High otherwise. This is the single definition of the roll-up rule —
// Tables() projects through it so the rule is testable in isolation and cannot drift from
// what the public API reports. High is detection-relative ("nothing flagged it"), not a
// correctness guarantee; see TableConfidence.
func rollupConfidence(warnings []TableWarning) TableConfidence {
	if len(warnings) > 0 {
		return TableConfidenceLow
	}
	return TableConfidenceHigh
}

// rotIdx90 records a ~90°-CCW glyph's index into the working texts slice
// and its post-transform coordinates, used in the ΔX-advance reconstruction pass.
type rotIdx90 struct {
	i    int     // index into working texts slice
	newX float64 // landscape X = portrait Y − lly
	newY float64 // landscape Y = urx − portrait X
}

// detectPredominantCCWRotation reports whether texts is overwhelmingly ~90°-CCW
// rotated: more than half of the real (non-empty, non-newline) glyphs are within
// skewAngleTolDeg of 90°, with a minimum count of 3.
//
// The minimum-3 guard prevents false positives from pages with a handful of
// rotated decorations alongside a mostly-horizontal body.
func detectPredominantCCWRotation(texts []Text) bool {
	nRot, nAll := 0, 0
	for _, t := range texts {
		if t.S == "" || t.S == "\n" {
			continue
		}
		nAll++
		if math.Abs(t.Rotation-90) <= skewAngleTolDeg {
			nRot++
		}
	}
	// Strict majority (more than half), matching the doc above: an exact 50/50
	// split does NOT fire, so a small portrait table carrying a few vertical
	// column-header labels is never wholesale-rotated.
	return nAll >= 1 && nRot >= 3 && nRot*2 > nAll
}

// rotPoint90CCW maps a point (x, y) in the portrait PDF frame to the landscape
// frame produced by a 90°-CCW page rotation:
//
//	newX = y - lly   (portrait Y → landscape X)
//	newY = urx - x   (portrait X → landscape Y, inverted)
//
// lly and urx are the bottom-left and top-right coordinates of the page MediaBox.
func rotPoint90CCW(x, y, lly, urx float64) (float64, float64) {
	return y - lly, urx - x
}

// buildRotTextIndex copies src into a new working slice, updates non-rotated
// glyph coordinates in-place (coordinate transform only; FontSize, W, H, and
// Rotation are left as-is), and returns a companion index of ~90°-CCW entries
// for the ΔX-advance reconstruction pass.
//
// lly and urx are the MediaBox bounds passed to rotPoint90CCW.
//
// The copy-not-alias discipline is critical: the no-op path in deRotateTableContent
// returns the original Content unchanged, so the rotated path must never mutate
// the caller's slice.
func buildRotTextIndex(src []Text, lly, urx float64) ([]Text, []rotIdx90) {
	out := make([]Text, len(src))
	copy(out, src)
	var idx []rotIdx90
	for i, t := range out {
		nx, ny := rotPoint90CCW(t.X, t.Y, lly, urx)
		if math.Abs(t.Rotation-90) <= skewAngleTolDeg {
			idx = append(idx, rotIdx90{i, nx, ny})
		} else {
			// Non-rotated glyph: update coordinates only; FontSize/W/H/Rotation intact.
			out[i].X = nx
			out[i].Y = ny
		}
	}
	return out, idx
}

// deRotateBandGlyphs sets the landscape-frame fields for one row band of
// ~90°-CCW glyphs. h is the band's reference height (the rotation-invariant
// font scale from Text.H), used as the default W and the W cap. texts is
// updated in-place via band indices.
//
// Root cause addressed: in a 90°-CCW-rotated PDF text matrix the rendered
// x-scale collapses toward zero, so every glyph arrives with FontSize≈0 and
// W≈0. The rotation-invariant H (the y-scale, preserved as the up-vector
// magnitude) carries the true font size. Within-band advance — the distance
// the word assembler uses for character spacing — is encoded as ΔnewX: the
// 90°-CCW rotation maps the original reading advance from the Y direction into
// the landscape X direction.
//
// Fields set per glyph:
//   - FontSize ← H  (rotation-invariant scale, recovered from the y-vector)
//   - W        ← ΔnewX to the next glyph, capped at 1.5×H so that large
//     inter-column gaps remain visible as a gap to the word assembler
//   - H        ← FontSize (consistent up-vector in the new frame)
//   - Rotation ← 0
func deRotateBandGlyphs(band []rotIdx90, texts []Text, h float64) {
	capW := h * 1.5
	prevW := h
	for k := range band {
		t := &texts[band[k].i]
		newFontSize := t.H // rotation-invariant height is the true font scale
		if newFontSize <= 0 {
			newFontSize = h
		}
		var newW float64
		if k+1 < len(band) {
			delta := band[k+1].newX - band[k].newX
			if delta > 0 && delta <= capW {
				newW = delta // normal within-word or near-word advance: use as-is
			} else if delta > capW {
				// Large gap signals a column/cell boundary: cap W so the word
				// assembler still sees a non-zero gap after this glyph.
				newW = capW
			} else {
				// delta ≤ 0: co-located or slightly overlapping glyphs.
				newW = prevW
			}
		} else {
			// Last glyph in band: carry the previous advance to avoid spanning
			// to the next band's first glyph (which would be an inter-row gap).
			newW = prevW
		}
		t.X = band[k].newX
		t.Y = band[k].newY
		t.FontSize = newFontSize
		t.W = newW
		t.H = newFontSize // consistent up-vector in the new frame
		t.Rotation = 0
		prevW = newW
	}
}

// deRotateBands groups the rot90 index into landscape-row bands and applies
// deRotateBandGlyphs to each band. texts is updated in-place.
//
// Scope: handles ~90°-CCW (+90°) embedded landscape tables only. CW (−90°/270°)
// tables are not detected here; that is a distinct case reserved for a future
// extension.
func deRotateBands(texts []Text, rot90 []rotIdx90) {
	// Primary sort: descending newY (top→bottom in landscape), ascending newX
	// for glyphs within the 1-pt same-row tolerance (left→right).
	sort.Slice(rot90, func(a, b int) bool {
		if math.Abs(rot90[a].newY-rot90[b].newY) > 1.0 {
			return rot90[a].newY > rot90[b].newY
		}
		return rot90[a].newX < rot90[b].newX
	})
	i := 0
	for i < len(rot90) {
		// Band height from the first glyph's H (rotation-invariant ≈ new FontSize).
		h := texts[rot90[i].i].H
		if h <= 0 {
			h = 8 // safe fallback when H is absent
		}
		bandTol := h * 0.5
		if bandTol < 1 {
			bandTol = 1
		}
		yRef := rot90[i].newY
		j := i + 1
		for j < len(rot90) && math.Abs(rot90[j].newY-yRef) <= bandTol {
			j++
		}
		band := rot90[i:j]
		// Re-sort band by newX — redundant after primary sort but guards against
		// nearly-identical newY pairs that the 1-pt tolerance can reorder.
		sort.Slice(band, func(a, b int) bool { return band[a].newX < band[b].newX })
		deRotateBandGlyphs(band, texts, h)
		i = j
	}
}

// rotateRects90CCW transforms a slice of Rect endpoints with rotPoint90CCW,
// then normalizes each rectangle (Min.X ≤ Max.X, Min.Y ≤ Max.Y). Normalization
// is required because the 90°-CCW rotation inverts the Y axis, swapping the
// Min/Max roles of the original corners.
func rotateRects90CCW(rects []Rect, lly, urx float64) []Rect {
	out := make([]Rect, len(rects))
	for i, r := range rects {
		x1, y1 := rotPoint90CCW(r.Min.X, r.Min.Y, lly, urx)
		x2, y2 := rotPoint90CCW(r.Max.X, r.Max.Y, lly, urx)
		out[i] = Rect{
			Min: Point{X: math.Min(x1, x2), Y: math.Min(y1, y2)},
			Max: Point{X: math.Max(x1, x2), Y: math.Max(y1, y2)},
		}
	}
	return out
}

// rotateStrokes90CCW transforms Stroke endpoints with rotPoint90CCW.
func rotateStrokes90CCW(strokes []Stroke, lly, urx float64) []Stroke {
	out := make([]Stroke, len(strokes))
	for i, s := range strokes {
		fx, fy := rotPoint90CCW(s.From.X, s.From.Y, lly, urx)
		tx, ty := rotPoint90CCW(s.To.X, s.To.Y, lly, urx)
		out[i] = Stroke{From: Point{X: fx, Y: fy}, To: Point{X: tx, Y: ty}}
	}
	return out
}

// deRotateTableContent detects a page whose text is predominantly ~90°-CCW rotated
// (a landscape table embedded in a portrait page via a 90°-CCW text matrix) and,
// if detected, maps the entire Content into the landscape reading frame so the
// horizontal word-assembly pipeline reconstructs the table correctly.
//
// The transform used for a point (x, y) is:
//
//	newX = y - media[1]   (page-Y becomes landscape-X)
//	newY = media[2] - x   (page-X becomes inverted landscape-Y)
//
// This maps a portrait MediaBox [llx, lly, urx, ury] to landscape [0, 0, pageH, pageW].
//
// For ~90°-CCW glyphs:
//   - FontSize ← H  (rotation-invariant height is the true font scale)
//   - Rotation ← 0
//   - W ← Δnew_X to the next glyph in the same landscape row, capped at 1.5×H so
//     that large inter-column gaps remain detectable by the word assembler.
//
// For non-rotated glyphs: only X/Y are updated; FontSize, W, H are left as-is.
//
// Rect and Stroke endpoints undergo the same (x,y)→(newX,newY) transform.
// The returned MediaBox is [0, 0, pageH, pageW].
//
// Scope: handles ~90°-CCW (+90°) embedded landscape tables. CW (−90°/270°)
// is out of scope for this change and is a future extension.
//
// For the non-rotated case this is a true no-op: it returns the input Content
// and media unchanged (wasRotated=false) with no allocations.
func deRotateTableContent(c Content, media [4]float64) (out Content, outMedia [4]float64, wasRotated bool) {
	if !detectPredominantCCWRotation(c.Text) {
		return c, media, false // clean pages — zero allocations, byte-identical to prior behaviour
	}
	lly, urx := media[1], media[2]
	texts, rot90 := buildRotTextIndex(c.Text, lly, urx)
	deRotateBands(texts, rot90)
	rects := rotateRects90CCW(c.Rect, lly, urx)
	strokes := rotateStrokes90CCW(c.Stroke, lly, urx)
	pageH := media[3] - media[1]
	pageW := media[2] - media[0]
	return Content{Text: texts, Rect: rects, Stroke: strokes}, [4]float64{0, 0, pageH, pageW}, true
}
