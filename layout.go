// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"unicode"
	"unicode/utf8"
)

// A Text represents a single piece of text drawn on a page.
type Text struct {
	Font     string  // the font used
	FontSize float64 // the font size, in points (1/72 of an inch)
	X        float64 // the X coordinate, in points, increasing left to right
	Y        float64 // the Y coordinate, in points, increasing bottom to top
	W        float64 // the width of the text, in points
	// H is the nominal font-box height in points: the magnitude of the text-space
	// up-vector. It equals the font size for ordinary horizontal text and stays
	// positive and rotation-invariant for rotated runs, where FontSize (taken from
	// the matrix x-scale) collapses toward zero. Always >= 0.
	H float64
	// Rotation is the text baseline's angle in degrees, counter-clockwise-positive
	// from the positive x-axis; 0 for ordinary horizontal text. It is the baseline
	// angle of the text rendering matrix in the page's display space: the page
	// /Rotate attribute (clockwise) is composed into the coordinate system, so on a
	// rotated page Rotation reflects the combined text-matrix and page rotation.
	// /Rotate is opposite-signed (clockwise) to this counter-clockwise angle; read
	// the applied page rotation directly via Page.Rotate.
	Rotation float64
	// S is the extracted UTF-8 text, returned verbatim with no escaping applied.
	// Callers must escape S before embedding it in HTML, shell commands, SQL, or
	// any other context-sensitive sink (e.g. html.EscapeString for HTML output).
	S string
}

// A Rect represents a rectangle.
type Rect struct {
	Min, Max Point
}

// A Stroke is a straight line segment drawn by a stroke-painting operator
// (S, s, B, B*, b, b*) in the content stream — a table ruling line, cell border,
// underline, or other vector rule. From and To are its endpoints in the page's
// upright display space (points, after page /Rotate and any cm), the same
// coordinate space as Rect and Text.
//
// Only straight stroked segments appear: a Bézier curve (c/v/y) breaks the run
// rather than contributing a chord, and fill-only (f/F) and clip (W/W*) paths
// are excluded. Rectangles drawn with the re operator are reported in Rect (even
// when stroked), not here; a lattice/table consumer reads both Rect and Stroke.
// Segments are verbatim from the stream and may include zero-length runs; callers
// that want only ruling lines should filter as needed.
//
// Experimental: the Go type and the Content.Stroke field are additive-stable, but
// which segments are emitted (the re-vs-stroke boundary, degenerate and closed-path
// handling) may be refined in a minor release as ruling-line/table support matures.
type Stroke struct {
	From, To Point
}

// A Point represents an X, Y pair.
type Point struct {
	X float64
	Y float64
}

// Content describes the basic content on a page: the text, any drawn rectangles,
// and any stroked line segments.
// All string fields within Text elements are verbatim UTF-8; see Text.S for the
// escaping contract that callers must honour.
type Content struct {
	Text   []Text
	Rect   []Rect
	Stroke []Stroke
}

// Column represents the contents of a column.
//
// Deprecated: backing type for the deprecated Page.GetTextByColumn; prefer Page.Lines / Page.Words.
type Column struct {
	Position int64
	Content  TextVertical
}

// Columns is a list of column.
//
// Deprecated: returned by the deprecated Page.GetTextByColumn; prefer Page.Lines / Page.Words.
type Columns []*Column

// GetTextByColumn returns the page's all text grouped by column.
// Returned Text.S values are verbatim UTF-8; see Text.S for the escaping contract.
//
// Deprecated: prefer Page.Lines, which groups text into column-aware visual lines
// through the shared extraction interpreter, so it carries per-word font metadata and
// feeds the decode-path quality signals this legacy path does not. Page.Words gives the
// per-word reading order. GetTextByColumn remains functional and is not scheduled for
// removal before a v2 module path.
func (p Page) GetTextByColumn() (Columns, error) {
	result := Columns{}
	var err error

	defer func() {
		if r := recover(); r != nil {
			result = Columns{}
			err = errors.New(fmt.Sprint(r))
		}
	}()

	showText := func(enc TextEncoding, currentX, currentY float64, s string) {
		var textBuilder bytes.Buffer

		for _, ch := range enc.Decode(s) {
			_, err := textBuilder.WriteRune(ch)
			if err != nil {
				panic(err)
			}
		}
		text := Text{
			S: textBuilder.String(),
			X: currentX,
			Y: currentY,
		}

		var currentColumn *Column
		columnFound := false
		for _, column := range result {
			if int64(currentX) == column.Position {
				currentColumn = column
				columnFound = true
				break
			}
		}

		if !columnFound {
			currentColumn = &Column{
				Position: int64(currentX),
				Content:  TextVertical{},
			}
			result = append(result, currentColumn)
		}

		currentColumn.Content = append(currentColumn.Content, text)
	}

	p.walkTextBlocks(showText)

	for _, column := range result {
		sort.Stable(column.Content)
	}

	// Columns have unique Position values: showText appends a new *Column only when
	// no existing column matches int64(currentX), so this ordering is already total.
	// SliceStable keeps it deterministic if that dedup invariant ever changes.
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Position < result[j].Position
	})

	return result, err
}

// Row represents the contents of a row.
//
// Deprecated: backing type for the deprecated Page.GetTextByRow; prefer Page.Lines / Page.Words.
type Row struct {
	Position int64
	Content  TextHorizontal
}

// Rows is a list of rows.
//
// Deprecated: returned by the deprecated Page.GetTextByRow; prefer Page.Lines / Page.Words.
type Rows []*Row

// GetTextByRow returns the page's all text grouped by rows.
// Returned Text.S values are verbatim UTF-8; see Text.S for the escaping contract.
//
// Deprecated: prefer Page.Lines for column-aware visual lines (with per-word font
// metadata and the decode-path quality signals this legacy path does not feed) and
// Page.Words for per-word reading order; both run the shared extraction interpreter
// this method bypasses. GetTextByRow remains functional and is not scheduled for
// removal before a v2 module path.
func (p Page) GetTextByRow() (Rows, error) {
	result := Rows{}
	var err error

	defer func() {
		if r := recover(); r != nil {
			result = Rows{}
			err = errors.New(fmt.Sprint(r))
		}
	}()

	showText := func(enc TextEncoding, currentX, currentY float64, s string) {
		var textBuilder bytes.Buffer
		for _, ch := range enc.Decode(s) {
			_, err := textBuilder.WriteRune(ch)
			if err != nil {
				panic(err)
			}
		}

		text := Text{
			S: textBuilder.String(),
			X: currentX,
			Y: currentY,
		}

		var currentRow *Row
		rowFound := false
		for _, row := range result {
			if int64(currentY) == row.Position {
				currentRow = row
				rowFound = true
				break
			}
		}

		if !rowFound {
			currentRow = &Row{
				Position: int64(currentY),
				Content:  TextHorizontal{},
			}
			result = append(result, currentRow)
		}

		currentRow.Content = append(currentRow.Content, text)
	}

	p.walkTextBlocks(showText)

	for _, row := range result {
		sort.Stable(row.Content)
	}

	// Rows have unique Position values (int64(currentY)); see GetTextByColumn.
	// SliceStable keeps the ordering deterministic under future invariant changes.
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Position > result[j].Position
	})

	return result, err
}

// TextVertical implements sort.Interface for sorting
// a slice of Text values in vertical order, top to bottom,
// and then left to right within a line.
type TextVertical []Text

func (x TextVertical) Len() int      { return len(x) }
func (x TextVertical) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x TextVertical) Less(i, j int) bool {
	if x[i].Y != x[j].Y {
		return x[i].Y > x[j].Y
	}
	return x[i].X < x[j].X
}

// TextHorizontal implements sort.Interface for sorting
// a slice of Text values in horizontal order, left to right,
// and then top to bottom within a column.
type TextHorizontal []Text

func (x TextHorizontal) Len() int      { return len(x) }
func (x TextHorizontal) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x TextHorizontal) Less(i, j int) bool {
	if x[i].X != x[j].X {
		return x[i].X < x[j].X
	}
	return x[i].Y > x[j].Y
}

// Word is a sequence of non-whitespace characters with a merged bounding box.
// X and Y are the bottom-left origin in PDF coordinate space (Y increases upward).
// W and H are the bounding box width and height in points.
// Font and FontSize describe the typeface: for a word built from glyphs in more
// than one font or size the first glyph wins. Font is the empty string when the
// glyph carried no font name.
type Word struct {
	S        string
	X, Y     float64
	W, H     float64
	Font     string
	FontSize float64
}

// Words returns the words on the page in reading order (left-to-right, top-to-bottom).
// Word boundaries come from space glyphs and inter-glyph X-gaps: adjacent
// non-whitespace glyphs within the same Y-band merge into one Word when the gap
// between them is within charWidth * 0.3, and glyphs in different Y-bands are
// never merged. Zero-width newline glyphs — the synthetic terminator the content
// interpreter appends after a TJ operator, and any literal LF byte in a show
// string — carry no visual advance and so are treated as non-breaking; a word
// split across a TJ boundary is therefore not falsely segmented. Bounding boxes
// are best-effort. Returns (nil, nil) for pages with no extractable text.
func (p Page) Words() ([]Word, error) {
	return wordsFromContentRecovered(p.layoutContent())
}

// layoutContent returns the page Content used by the reading-order PROSE surfaces — Page.Words(),
// Page.Lines(), and Page.Blocks(). When the document carries a cross-page diagonal WATERMARK (the
// same skew stamp printed on ~every page, e.g. an agency mark), its glyphs are dropped here so a
// watermark glyph cannot fuse into an adjacent value during geometry-based assembly (e.g. "11144"
// → "111Ê44"). A document WITHOUT a watermark keeps its diagonal text untouched — page-specific
// rotated content such as a chart-axis label is preserved on these prose surfaces (it is not a
// watermark, and a reader may want it).
//
// NOTE: Page.Tables() does NOT go through layoutContent — its grid path drops skew text
// UNCONDITIONALLY (reconstructTablesFromContent), because a grid cell never legitimately holds
// diagonal text. The two surfaces have genuinely different requirements: a table cell must never
// contain a rotated label; reading-order prose sometimes should keep one. The raw page geometry,
// including any watermark glyphs, always stays available on Content() / Texts() / DebugJSON. See
// Reader.hasWatermark for the cross-page detection.
func (p Page) layoutContent() Content {
	c := p.Content()
	// Fast path: a page with NO diagonal text has nothing a watermark could pollute, so skip the
	// (document-level, page-sampling) watermark detection entirely. This keeps the overwhelmingly
	// common clean page free — the cross-page scan is paid only when this page actually carries
	// skew glyphs (i.e. when a watermark could be present), and is cached once per Reader.
	if !contentHasSkew(c.Text) {
		return c
	}
	if r := p.V.r; r != nil && r.hasWatermark() {
		return layoutFilter(c)
	}
	return c
}

// contentHasSkew reports whether any glyph is diagonal (skew-rotated). Cheap, allocation-free.
func contentHasSkew(texts []Text) bool {
	for i := range texts {
		if isSkewRotated(texts[i].Rotation) {
			return true
		}
	}
	return false
}

// layoutFilter is the pure projection layoutContent applies on a watermarked document
// (factored out so it is unit testable without a Page). It drops skew glyphs from c.Text and
// leaves Rect/Stroke intact.
func layoutFilter(c Content) Content {
	return Content{Text: dropSkewRotatedText(c.Text), Rect: c.Rect, Stroke: c.Stroke}
}

// watermarkCache lazily memoizes the document-level watermark verdict (one detection per
// Reader). Mirrors the pageMapCache sync.Once idiom.
type watermarkCache struct {
	once    sync.Once
	present bool
}

// hasWatermark reports whether the document carries a cross-page diagonal watermark — the same
// skew stamp printed across the document (distinct from page-specific rotated content like a
// chart-axis label, which appears on only a few pages). Computed once per Reader by sampling
// pages (detectWatermark) and memoized. Concurrency-safe; nil-safe.
func (r *Reader) hasWatermark() bool {
	if r == nil {
		return false
	}
	r.watermark.once.Do(func() { r.watermark.present = r.detectWatermark() })
	return r.watermark.present
}

const (
	// watermarkSampleCap bounds how many pages detectWatermark interprets. A watermark is by
	// definition repeated across the document, so an evenly-spaced sample detects it without
	// scanning every page of a large file (the cost stays bounded for a single-page extraction).
	watermarkSampleCap = 16
	// watermarkPageFrac: the document is watermarked when the SAME diagonal signature recurs on
	// at least this fraction of sampled pages. A cross-page watermark prints on ~every page
	// (e.g. 1235/1236 ≈ 100%); page-specific diagonal chart-axis labels appear on a small
	// minority and DIFFER per page, so 0.5 separates the two with a wide margin.
	watermarkPageFrac = 0.5
	// minWatermarkRunes: the recurring signature must be at least this many (non-space) runes — a
	// watermark is a phrase/stamp ("TỔNG CỤC THỐNG KÊ", 14 runes), not a short rotated axis unit
	// ("kWh"/"CO2"/"GDP", 3 runes) that a chart might repeat on every page. 5 excludes those short
	// units with margin to the validated watermark; the trade-off is that a sub-5-rune watermark is
	// not detected (a false negative, acceptable for this detection-relative flag).
	minWatermarkRunes = 5
)

// detectWatermark samples up to watermarkSampleCap EVENLY-SPACED pages and reports whether the
// SAME diagonal-text signature recurs across the document (cross-page continuity). Keying on the
// recurring SIGNATURE — not merely "some skew on many pages" — is what separates a real watermark
// (the identical stamp on every page) from page-specific rotated content whose text differs page
// to page (chart-axis labels, section markers). Per-page panics are contained so a malformed page
// cannot abort detection.
//
// Known limits (detection-relative — both bias toward NOT filtering, i.e. toward keeping text):
//   - A watermark whose per-page signature VARIES (because a page also carries page-specific
//     diagonal text that pollutes the whole-page rune set) lowers the dominant fraction and may go
//     undetected (a false negative): the watermark then stays in Words/Lines, which is honest — no
//     legitimate text is dropped.
//   - A long diagonal label IDENTICALLY repeated on most pages (a fixed repeated chart) would match
//     the recurring-signature test; this is rare (no instance in the measured corpus) and is exactly
//     the "same text on every page" a watermark is, so treating it as one is defensible. The
//     minWatermarkRunes floor keeps a SHORT repeated rotated unit (kWh/CO2/GDP) from tripping it.
func (r *Reader) detectWatermark() bool {
	n := r.NumPage()
	if n <= 0 {
		return false
	}
	idxs := samplePageIndices(n, watermarkSampleCap)
	sigCount := map[string]int{}
	for _, pg := range idxs {
		if sig, ok := pageSkewSignature(r, pg); ok {
			sigCount[sig]++
		}
	}
	domSig, domCount := "", 0
	for sig, c := range sigCount {
		if c > domCount {
			domSig, domCount = sig, c
		}
	}
	return watermarkVerdict(domSig, domCount, len(idxs))
}

// samplePageIndices returns up to cap 1-based page indices spread EVENLY across [1,n], always
// including the first and last page (so a watermark concentrated anywhere in the document is
// sampled, not just a prefix). For n<=cap it returns every page.
func samplePageIndices(n, cap int) []int {
	if n <= cap {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i + 1
		}
		return idx
	}
	idx := make([]int, cap)
	for i := range idx {
		idx[i] = 1 + int(math.Round(float64(i)*float64(n-1)/float64(cap-1)))
	}
	return idx
}

// pageSkewSignature returns an order-independent signature (sorted, space-stripped runes) of
// page pg's diagonal glyph text, or ok=false when the page has no skew text. A genuine cross-page
// watermark yields the SAME signature on every page (the same stamp); page-specific rotated
// labels (chart axes, section markers) yield a DIFFERENT signature per page.
func pageSkewSignature(r *Reader, pg int) (sig string, ok bool) {
	defer func() { _ = recover() }()
	p := r.Page(pg)
	if p.V.IsNull() {
		return "", false
	}
	var runes []rune
	for _, t := range p.Content().Text {
		if !isSkewRotated(t.Rotation) {
			continue
		}
		for _, ru := range t.S {
			if !unicode.IsSpace(ru) {
				runes = append(runes, ru)
			}
		}
	}
	if len(runes) == 0 {
		return "", false
	}
	slices.Sort(runes)
	return string(runes), true
}

// watermarkVerdict reports a watermark when the dominant diagonal signature recurs on at least
// watermarkPageFrac of the sampled pages (cross-page continuity) AND is a phrase of at least
// minWatermarkRunes runes. Factored out so the threshold is unit testable.
func watermarkVerdict(dominantSig string, dominant, sampled int) bool {
	if sampled == 0 || float64(dominant)/float64(sampled) < watermarkPageFrac {
		return false
	}
	return len([]rune(dominantSig)) >= minWatermarkRunes
}

// skewAngleTolDeg: text whose rotation is within this many degrees of an axis
// (0°/90°/180°/270°) is kept. Diagonal / skew text — watermarks, arc-decoration labels — is
// dropped from the assembled-text surfaces so it cannot fuse into a value: UNCONDITIONALLY in the
// table grid path (reconstructTablesFromContent — a cell never holds diagonal text), and for a
// detected cross-page watermark in the reading-order prose path (layoutContent → Words/Lines/Blocks,
// which otherwise keep a page-specific rotated label). The raw glyphs (incl. skew) remain on
// Content()/Texts()/DebugJSON.
const skewAngleTolDeg = 10.0

// dropSkewRotatedText filters out glyphs whose baseline is diagonal (not
// axis-aligned to within skewAngleTolDeg degrees).  Axis-aligned text at 0°,
// 90°, 180°, and 270° (including landscape tables and vertical headers) is
// always kept.  Skew text (e.g. a 45° watermark) is dropped so it cannot
// contaminate word boundaries or table cells.
//
// The distance to the nearest axis multiple is math.Mod(|rotation|, 90):
//   - exactly 45 → d=45 > skewAngleTolDeg → dropped  (watermark ✓)
//   - 0° or 360° → d=0 ≤ skewAngleTolDeg → kept      (body text ✓)
//   - 90°/270° → d=0 ≤ skewAngleTolDeg → kept        (landscape / vertical headers ✓)
//
// It returns nil (not []Text{}) when the input is empty or all glyphs are
// filtered, so that downstream nil-checks behave correctly.
//
// Fast path: the common page has NO skew text, so a single scan that finds none
// returns the input slice unchanged — zero allocation, identical to the prior
// direct `texts := c.Text` behaviour. Only a page that actually carries skew
// glyphs pays for a filtered copy.
func dropSkewRotatedText(texts []Text) []Text {
	if len(texts) == 0 {
		return nil
	}
	hasSkew := false
	for _, t := range texts {
		if isSkewRotated(t.Rotation) {
			hasSkew = true
			break
		}
	}
	if !hasSkew {
		return texts // no diagonal text → no allocation, no behaviour change
	}
	out := make([]Text, 0, len(texts))
	for _, t := range texts {
		if !isSkewRotated(t.Rotation) {
			out = append(out, t) // axis-aligned (0/90/180/270) → keep
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isSkewRotated reports whether a glyph baseline is diagonal — more than
// skewAngleTolDeg degrees from an axis multiple of 90°.
func isSkewRotated(rotation float64) bool {
	d := math.Mod(math.Abs(rotation), 90) // distance to nearest 90° multiple
	if d > 45 {
		d = 90 - d
	}
	return d > skewAngleTolDeg
}

// wordsFromContent groups an already-interpreted Content's glyphs into words.
// It may panic on a pathological band; callers needing the page's
// degrade-to-empty contract use wordsFromContentRecovered.
func wordsFromContent(c Content) []Word {
	texts := c.Text
	if len(texts) == 0 {
		return nil
	}
	var words []Word
	for _, band := range bandsByY(texts) {
		words = append(words, wordsFromBand(texts, band)...)
	}
	return words
}

// wordsFromContentRecovered wraps wordsFromContent in the Words() panic contract:
// a malformed band is recovered into (nil, error) rather than propagating.
func wordsFromContentRecovered(c Content) (words []Word, err error) {
	defer func() {
		if r := recover(); r != nil {
			words = nil
			err = errors.New(fmt.Sprint(r))
		}
	}()
	return wordsFromContent(c), nil
}

// wordsFromLines flattens the words of already-assembled lines back into one
// slice, in line/column order. splitWordsByGutters partitions a band's words
// without dropping or duplicating any, and lineFromWords keeps Line.Words as
// that partition, so the result is the same multiset of words wordsFromContent
// would yield from the same Content — the invariant TestWordsLinesCountEquivalence
// locks. It copies only Word headers (the strings are shared), so it is far
// cheaper than a second bandsByY + wordsFromBand pass.
func wordsFromLines(lines []Line) []Word {
	n := 0
	for _, ln := range lines {
		n += len(ln.Words)
	}
	if n == 0 {
		return nil
	}
	out := make([]Word, 0, n)
	for _, ln := range lines {
		out = append(out, ln.Words...)
	}
	return out
}

// bandsByY sorts texts top-to-bottom (Y descending then X ascending) and groups
// them into y-bands: a new band starts when the Y-distance from the first glyph
// of the current band exceeds max(band[0].FontSize*0.5, 1). Within a band the
// glyphs are X-ascending (with a deterministic Y tie-break for stacked glyphs),
// satisfying wordsFromBand's left-to-right precondition.
//
// bandsByY does NOT mutate texts. It sorts a private []int permutation and
// returns bands as sub-slices of that permutation (indices into texts), so a
// 22pp CJK page no longer pays two whole-[]Text copies (the former defensive
// copy plus the per-band value copies); only one len(texts) index slice is
// allocated. Banding the same Content twice is therefore idempotent.
func bandsByY(texts []Text) [][]int {
	if len(texts) == 0 {
		return nil
	}
	idx := make([]int, len(texts))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ti, tj := texts[idx[a]], texts[idx[b]]
		// Quantise Y to a fine grid (far finer than line spacing, coarser than
		// floating-point noise) so two glyphs meant for the same visual line
		// cannot be reordered by sub-point baseline jitter; co-linear glyphs and
		// ties fall through to left-to-right X order. Distinct lines, spaced by
		// whole points, never share a grid cell.
		if qi, qj := quantize(ti.Y), quantize(tj.Y); qi != qj {
			return qi > qj
		}
		return ti.X < tj.X
	})

	var bands [][]int
	start := 0
	flush := func(end int) {
		band := idx[start:end:end] // cap-bounded sub-slice; a later append cannot bleed across bands
		sort.SliceStable(band, func(a, b int) bool {
			ta, tb := texts[band[a]], texts[band[b]]
			if quantize(ta.X) != quantize(tb.X) {
				return ta.X < tb.X
			}
			return ta.Y > tb.Y // deterministic tie-break for stacked glyphs
		})
		bands = append(bands, band)
		start = end
	}

	for i := 1; i < len(idx); i++ {
		anchor := texts[idx[start]] // the band's first glyph; start advances only at a flush
		tol := anchor.FontSize * 0.5
		if tol < 1 {
			tol = 1
		}
		if anchor.Y-texts[idx[i]].Y > tol {
			flush(i)
		}
	}
	flush(len(idx))
	return bands
}

// quantize snaps a coordinate to a fine grid so floating-point jitter cannot
// perturb sort order. The grid is far finer than any real line or glyph
// spacing, so it only ever collapses values that are equal up to rounding.
func quantize(v float64) float64 {
	const grid = 0.05 // points
	return math.Round(v / grid)
}

// isAllSpace reports whether every rune in s is a Unicode space character.
func isAllSpace(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// wordsFromBand groups the per-glyph Text entries named by band (indices into
// texts, in left-to-right X order — bandsByY's postcondition) into Word values.
func wordsFromBand(texts []Text, band []int) []Word {
	if len(band) == 0 {
		return nil
	}
	var words []Word
	var cur *Word

	emit := func() {
		if cur != nil {
			words = append(words, *cur)
			cur = nil
		}
	}

	for _, bi := range band {
		t := texts[bi]
		// Skip empty glyphs and the synthetic "\n" that content.go appends after
		// every TJ operator (content.go handleTextShow): that terminator is an
		// interpreter artifact, not a real word boundary. Genuine separations
		// still surface as a real space glyph or an X-gap, both handled below.
		if t.S == "" || t.S == "\n" {
			continue
		}
		if isAllSpace(t.S) {
			emit()
			continue
		}

		if cur == nil {
			// H is the up-vector nominal font height (Text.H: rotation-invariant,
			// always >= 0), NOT t.FontSize (the matrix x-scale, which collapses at
			// 90 deg, doubles under Tz, and goes negative under a horizontal flip).
			// For horizontal Th=1 text the two are identical, so the committed
			// corpus is byte-unchanged.
			cur = &Word{S: t.S, X: t.X, Y: t.Y, W: t.W, H: t.H, Font: t.Font, FontSize: t.FontSize}
			continue
		}

		gap := t.X - (cur.X + cur.W)
		// The gap threshold sizes a HORIZONTAL inter-glyph gap; it stays on
		// FontSize (the x-scale), not t.H — it is an advance heuristic, not a height.
		threshold := t.W * 0.3
		if alt := t.FontSize * 0.15; alt > threshold {
			threshold = alt
		}
		if gap > threshold {
			emit()
			cur = &Word{S: t.S, X: t.X, Y: t.Y, W: t.W, H: t.H, Font: t.Font, FontSize: t.FontSize}
			continue
		}

		cur.S += t.S
		if end := t.X + t.W; end > cur.X+cur.W {
			cur.W = end - cur.X
		}
		curTop := cur.Y + cur.H
		if t.Y < cur.Y {
			cur.Y = t.Y
		}
		if tTop := t.Y + t.H; tTop > curTop { // up-vector height, matching the seed
			curTop = tTop
		}
		cur.H = curTop - cur.Y
	}
	emit()
	return words
}

// Line is a reading-order group of words that share a visual line.
// X and Y are the bottom-left origin in PDF coordinate space (Y increases upward).
// W and H are the bounding box of the entire line in points; H is the up-vector
// nominal font height (the same Text.H basis as Word.H), rotation-invariant and
// >= 0, and equals the font size for ordinary horizontal text.
// S is the words joined by a single space, except no space is inserted between
// two glyphs of a space-less CJK script (Han, Hiragana, Katakana) so a per-glyph
// run rejoins seamlessly; Korean (Hangul) keeps its inter-word spaces.
// Words preserves the left-to-right order of the constituent Word values.
// Font and FontSize describe the typeface of the line; for a line spanning more
// than one font or size the first word wins (mirroring Word's first-glyph rule).
type Line struct {
	S        string
	X, Y     float64
	W, H     float64
	Font     string
	FontSize float64
	Words    []Word
}

// Lines returns visual text lines on the page. Words are grouped into y-bands
// (as Page.Words()); a band that a column gutter splits — a wide vertical gap
// recurring across the page — yields one Line per column instead of gluing the
// columns into a single Line.S, so a full-width row (a masthead or heading that
// flows across the gutters) stays whole while a genuine multi-column row breaks
// apart. Words within a Line are left-to-right.
//
// Lines are emitted in y-band order, and within a band left-to-right by column.
// This is a bounded per-band split, NOT full multi-column reading order: on a
// multi-column page the columns are still interleaved by row (left col row 1,
// right col row 1, left col row 2, ...) rather than read down each column in
// full. Column-major ordering is intentionally out of scope here.
//
// Returns (nil, nil) for pages with no extractable text. Panics during content
// parsing are recovered and returned as errors, matching Words() semantics.
func (p Page) Lines() ([]Line, error) {
	return linesFromContentRecovered(p.layoutContent())
}

// linesFromContent assembles lines (reading order, column-split) from an
// already-interpreted Content. It may panic on a pathological segment; callers
// needing the Lines() degrade-to-empty contract use linesFromContentRecovered.
func linesFromContent(c Content) []Line {
	lines, _, _ := linesAndGutters(c)
	return lines
}

// linesAndGutters assembles lines (reading order, column-split) from an
// already-interpreted Content and also returns the detected column gutters and
// the colGap width, so callers can re-derive each line's column (columnOf) with
// the same snapTol used during splitting. It backs both linesFromContent and
// blocksFromContent. It may panic on a pathological segment; callers wrap it
// (linesFromContentRecovered / blocksFromContentRecovered) for the
// degrade-to-empty contract.
func linesAndGutters(c Content) ([]Line, []float64, float64) {
	texts := c.Text
	if len(texts) == 0 {
		return nil, nil, 0
	}
	var rows [][]Word
	for _, band := range bandsByY(texts) {
		if ws := wordsFromBand(texts, band); len(ws) > 0 {
			rows = append(rows, ws)
		}
	}
	gutters, colGap := columnGutters(rows)
	var lines []Line
	for _, ws := range rows {
		for _, seg := range splitWordsByGutters(ws, gutters, colGap) {
			lines = append(lines, lineFromWords(seg))
		}
	}
	return lines, gutters, colGap
}

// linesFromContentRecovered wraps linesFromContent in the Lines() panic
// contract: a malformed segment is recovered into (nil, error).
func linesFromContentRecovered(c Content) (lines []Line, err error) {
	defer func() {
		if r := recover(); r != nil {
			lines = nil
			err = errors.New(fmt.Sprint(r))
		}
	}()
	return linesFromContent(c), nil
}

// lineFromWords assembles one Line from a left-to-right run of words. S is the
// words joined by a single space, except no space is inserted between two
// adjacent CJK glyphs (Han, Hiragana, Katakana, Hangul), which are set without
// inter-character spacing — so a per-glyph word split rejoins seamlessly. Font
// and FontSize come from the first word (first word wins for mixed-font lines).
func lineFromWords(ws []Word) Line {
	l := Line{
		S: ws[0].S, X: ws[0].X, Y: ws[0].Y, W: ws[0].W, H: ws[0].H,
		Font: ws[0].Font, FontSize: ws[0].FontSize, Words: ws,
	}
	top := l.Y + l.H
	for _, w := range ws[1:] {
		if joinNeedsSpace(l.S, w.S) {
			l.S += " "
		}
		l.S += w.S
		if end := w.X + w.W; end > l.X+l.W {
			l.W = end - l.X
		}
		if w.Y < l.Y {
			l.Y = w.Y
		}
		if wTop := w.Y + w.H; wTop > top {
			top = wTop
		}
	}
	l.H = top - l.Y
	return l
}

// joinNeedsSpace reports whether a single space should separate the
// already-assembled left text from the next word. It is suppressed only when
// the boundary sits between two glyphs of a space-less CJK script (see
// isSpacelessCJK), so a run that a per-glyph X-gap split into separate words
// rejoins seamlessly. Korean (Hangul) is excluded: it uses real inter-word
// spaces, so suppressing them would destroy word boundaries.
func joinNeedsSpace(left, right string) bool {
	lr, _ := utf8.DecodeLastRuneInString(left)
	rr, _ := utf8.DecodeRuneInString(right)
	return !isSpacelessCJK(lr) || !isSpacelessCJK(rr)
}

// isSpacelessCJK reports whether r belongs to a CJK script written without
// inter-word spaces — Han (Chinese / Japanese kanji), Hiragana, or Katakana.
// Hangul is deliberately NOT included: Korean separates words with spaces, so a
// space between two Hangul syllables is a genuine word boundary that must be
// preserved, not a per-glyph layout gap to be closed.
func isSpacelessCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r)
}
