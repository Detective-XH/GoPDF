// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
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
	// from the positive x-axis; 0 for ordinary horizontal text. This is the text
	// rendering matrix's baseline angle — distinct from, and opposite-signed to,
	// the page /Rotate attribute (which is clockwise-positive and is not applied
	// here).
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

// A Point represents an X, Y pair.
type Point struct {
	X float64
	Y float64
}

// Content describes the basic content on a page: the text and any drawn rectangles.
// All string fields within Text elements are verbatim UTF-8; see Text.S for the
// escaping contract that callers must honour.
type Content struct {
	Text []Text
	Rect []Rect
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
	return wordsFromContentRecovered(p.Content())
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
		words = append(words, wordsFromBand(band)...)
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

// bandsByY sorts texts top-to-bottom (Y descending then X ascending) and
// groups them into y-bands: a new band starts when the Y-distance from the
// first glyph of the current band exceeds max(band[0].FontSize*0.5, 1).
// Each band is re-sorted X-ascending before appending, satisfying
// wordsFromBand's left-to-right precondition and handling sub/superscript Y-shift.
func bandsByY(texts []Text) [][]Text {
	// Sort a copy: the in-place sort below must not mutate the caller's slice
	// (one page's Content.Text may be shared between the words and lines
	// derivations). Bands are built by appending Text values into fresh slices,
	// so they never alias the input.
	texts = append([]Text(nil), texts...)
	sort.SliceStable(texts, func(i, j int) bool {
		// Quantise Y to a fine grid (far finer than line spacing, coarser than
		// floating-point noise) so two glyphs meant for the same visual line
		// cannot be reordered by sub-point baseline jitter; co-linear glyphs and
		// ties fall through to left-to-right X order. Distinct lines, spaced by
		// whole points, never share a grid cell.
		if qi, qj := quantize(texts[i].Y), quantize(texts[j].Y); qi != qj {
			return qi > qj
		}
		return texts[i].X < texts[j].X
	})

	var bands [][]Text
	var band []Text
	flush := func() {
		sort.SliceStable(band, func(i, j int) bool {
			if quantize(band[i].X) != quantize(band[j].X) {
				return band[i].X < band[j].X
			}
			return band[i].Y > band[j].Y // deterministic tie-break for stacked glyphs
		})
		bands = append(bands, band)
		band = nil
	}

	for _, t := range texts {
		if len(band) == 0 {
			band = append(band, t)
			continue
		}
		tol := band[0].FontSize * 0.5
		if tol < 1 {
			tol = 1
		}
		if band[0].Y-t.Y > tol {
			flush()
		}
		band = append(band, t)
	}
	if len(band) > 0 {
		flush()
	}
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

// wordsFromBand groups per-glyph Text entries that share a y-band into Word values.
// band must be sorted left-to-right (X ascending).
func wordsFromBand(band []Text) []Word {
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

	for _, t := range band {
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
	return linesFromContentRecovered(p.Content())
}

// linesFromContent assembles lines (reading order, column-split) from an
// already-interpreted Content. It may panic on a pathological segment; callers
// needing the Lines() degrade-to-empty contract use linesFromContentRecovered.
func linesFromContent(c Content) []Line {
	lines, _ := linesAndGutters(c)
	return lines
}

// linesAndGutters assembles lines (reading order, column-split) from an
// already-interpreted Content and also returns the detected column gutters, so a
// caller can re-derive each line's column (columnOf) without recomputing the band
// and gutter geometry. It backs both linesFromContent and blocksFromContent. It may
// panic on a pathological segment; callers wrap it (linesFromContentRecovered /
// blocksFromContentRecovered) for the degrade-to-empty contract.
func linesAndGutters(c Content) ([]Line, []float64) {
	texts := c.Text
	if len(texts) == 0 {
		return nil, nil
	}
	var rows [][]Word
	for _, band := range bandsByY(texts) {
		if ws := wordsFromBand(band); len(ws) > 0 {
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
	return lines, gutters
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
