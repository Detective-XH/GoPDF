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

	sort.Slice(result, func(i, j int) bool {
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

	sort.Slice(result, func(i, j int) bool {
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

	sort.SliceStable(texts, func(i, j int) bool {
		if texts[i].Y != texts[j].Y {
			return texts[i].Y > texts[j].Y
		}
		return texts[i].X < texts[j].X
	})

	var band []Text
	flush := func() {
		// The global sort keys on Y first, so a band spanning multiple Y values
		// (e.g. sub/superscripts within tolerance) is not guaranteed X-ascending.
		// Re-sort by X here to satisfy wordsFromBand's left-to-right precondition.
		sort.SliceStable(band, func(i, j int) bool {
			return band[i].X < band[j].X
		})
		words = append(words, wordsFromBand(band)...)
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
	flush()

	return words, nil
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
		isSpace := true
		for _, r := range t.S {
			if !unicode.IsSpace(r) {
				isSpace = false
				break
			}
		}
		if isSpace {
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
		if t.FontSize > cur.H {
			cur.H = t.FontSize
		}
	}
	emit()
	return words
}
