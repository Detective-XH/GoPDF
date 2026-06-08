// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"unicode"
)

// A Text represents a single piece of text drawn on a page.
type Text struct {
	Font     string  // the font used
	FontSize float64 // the font size, in points (1/72 of an inch)
	X        float64 // the X coordinate, in points, increasing left to right
	Y        float64 // the Y coordinate, in points, increasing bottom to top
	W        float64 // the width of the text, in points
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

// Column represents the contents of a column
type Column struct {
	Position int64
	Content  TextVertical
}

// Columns is a list of column
type Columns []*Column

// GetTextByColumn returns the page's all text grouped by column.
// Returned Text.S values are verbatim UTF-8; see Text.S for the escaping contract.
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

// Row represents the contents of a row
type Row struct {
	Position int64
	Content  TextHorizontal
}

// Rows is a list of rows
type Rows []*Row

// GetTextByRow returns the page's all text grouped by rows.
// Returned Text.S values are verbatim UTF-8; see Text.S for the escaping contract.
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
type Word struct {
	S    string
	X, Y float64
	W, H float64
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
func (p Page) Words() (words []Word, err error) {
	defer func() {
		if r := recover(); r != nil {
			words = nil
			err = errors.New(fmt.Sprint(r))
		}
	}()

	texts := p.Content().Text
	if len(texts) == 0 {
		return nil, nil
	}

	for _, band := range bandsByY(texts) {
		words = append(words, wordsFromBand(band)...)
	}
	return words, nil
}

// bandsByY sorts texts top-to-bottom (Y descending then X ascending) and
// groups them into y-bands: a new band starts when the Y-distance from the
// first glyph of the current band exceeds max(band[0].FontSize*0.5, 1).
// Each band is re-sorted X-ascending before appending, satisfying
// wordsFromBand's left-to-right precondition and handling sub/superscript Y-shift.
func bandsByY(texts []Text) [][]Text {
	sort.SliceStable(texts, func(i, j int) bool {
		if texts[i].Y != texts[j].Y {
			return texts[i].Y > texts[j].Y
		}
		return texts[i].X < texts[j].X
	})

	var bands [][]Text
	var band []Text
	flush := func() {
		sort.SliceStable(band, func(i, j int) bool {
			return band[i].X < band[j].X
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
			cur = &Word{S: t.S, X: t.X, Y: t.Y, W: t.W, H: t.FontSize}
			continue
		}

		gap := t.X - (cur.X + cur.W)
		threshold := t.W * 0.3
		if alt := t.FontSize * 0.15; alt > threshold {
			threshold = alt
		}
		if gap > threshold {
			emit()
			cur = &Word{S: t.S, X: t.X, Y: t.Y, W: t.W, H: t.FontSize}
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
		if tTop := t.Y + t.FontSize; tTop > curTop {
			curTop = tTop
		}
		cur.H = curTop - cur.Y
	}
	emit()
	return words
}

// Line is a reading-order group of words that share a visual line.
// X and Y are the bottom-left origin in PDF coordinate space (Y increases upward).
// W and H are the bounding box of the entire line in points.
// S is the words joined by a single space.
// Words preserves the left-to-right order of the constituent Word values.
//
// Lines groups by the same y-band criterion as Page.Words(): two words are on
// the same line when they share a y-band. Multi-column pages with columns at
// the same Y will be collapsed into one Line per visual row.
type Line struct {
	S     string
	X, Y  float64
	W, H  float64
	Words []Word
}

// Lines returns visual text lines on the page in reading order (top-to-bottom,
// left-to-right). Each Line corresponds to one y-band — the same grouping
// criterion as Page.Words(). Words within a line are in left-to-right order;
// lines are top-to-bottom.
//
// Returns (nil, nil) for pages with no extractable text. Panics during content
// parsing are recovered and returned as errors, matching Words() semantics.
func (p Page) Lines() (lines []Line, err error) {
	defer func() {
		if r := recover(); r != nil {
			lines = nil
			err = errors.New(fmt.Sprint(r))
		}
	}()

	texts := p.Content().Text
	if len(texts) == 0 {
		return nil, nil
	}

	for _, band := range bandsByY(texts) {
		ws := wordsFromBand(band)
		if len(ws) == 0 {
			continue
		}
		l := Line{
			S:     ws[0].S,
			X:     ws[0].X,
			Y:     ws[0].Y,
			W:     ws[0].W,
			H:     ws[0].H,
			Words: ws,
		}
		top := l.Y + l.H
		for _, w := range ws[1:] {
			l.S += " " + w.S
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
		lines = append(lines, l)
	}
	return lines, nil
}
