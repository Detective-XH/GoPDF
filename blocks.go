// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// blockGapFactor sets the inter-line vertical gap — as a multiple of the taller of
// two stacked lines' heights — beyond which the lines belong to different blocks.
// Single-spaced body text leaves a gap near 0.2x the line height; a paragraph or
// block break opens a visibly larger gap. 0.6 sits between the two so ordinary
// leading keeps lines together while a blank-line-sized gap splits the block.
const blockGapFactor = 0.6

// Block is a column-major group of consecutive Lines separated by no more than a
// block-sized vertical gap — the visual chunking unit a RAG pipeline wants. On a
// multi-column page Blocks read down each detected column in full (column-major),
// unlike Page.Lines which stays row-major. X and Y are the bottom-left origin in
// PDF coordinate space (Y increases upward); W and H bound every constituent line.
// S is the lines' S joined by "\n". Font and FontSize come from the first (topmost)
// line (the same first-wins rule as Line). Lines preserves top-to-bottom order.
//
// Experimental: the grouping heuristic — line-to-block assignment, the gap
// threshold, and column-major ordering details — may change in a minor release as
// the segmentation is refined. The Go signature and the field set are additive-
// stable per API-STABILITY.md. Blocks are visual groupings only: no paragraph or
// section semantics, and reading order around full-width interruptions (a masthead
// or a mid-page heading spanning the gutters) is best-effort, not guaranteed.
type Block struct {
	S        string
	X, Y     float64
	W, H     float64
	Font     string
	FontSize float64
	Lines    []Line
}

// Blocks returns the page's text grouped into visual blocks in column-major reading
// order: lines are split into the same columns Page.Lines detects, each column is
// read top-to-bottom in full, and within a column consecutive lines separated by no
// more than a block-sized vertical gap are merged into one Block. On a single-column
// page every line is one column, so Blocks degrades to gap-based paragraph grouping.
// A full-width line spanning the gutters is assigned to the column its left edge
// falls in; reading order around such interruptions is best-effort.
//
// Returns (nil, nil) for pages with no extractable text. Panics during content
// parsing are recovered and returned as errors, matching Words() and Lines().
func (p Page) Blocks() ([]Block, error) {
	return blocksFromContentRecovered(p.layoutContent())
}

// blocksFromContent assembles column-major blocks from an already-interpreted
// Content. It may panic on a pathological segment; callers needing the Blocks()
// degrade-to-empty contract use blocksFromContentRecovered.
func blocksFromContent(c Content) []Block {
	lines, gutters, colGap := linesAndGutters(c)
	if len(lines) == 0 {
		return nil
	}
	return groupLinesIntoBlocks(lines, gutters, colGap)
}

// blocksFromContentRecovered wraps blocksFromContent in the Blocks() panic
// contract: a malformed segment is recovered into (nil, error).
func blocksFromContentRecovered(c Content) (blocks []Block, err error) {
	defer func() {
		if r := recover(); r != nil {
			blocks = nil
			err = errors.New(fmt.Sprint(r))
		}
	}()
	return blocksFromContent(c), nil
}

// groupLinesIntoBlocks reorders lines into column-major reading order and runs a
// gap-based grouper down each column. Lines are bucketed by the column their left
// edge falls in (columnOfLine over the same gutters Lines() used); columns are emitted
// left-to-right, and within a column lines are read top-to-bottom (Y descending).
// With no gutters every line is column 0, so the output is one gap-grouped stack.
// colGap derives the same snapTol Lines() uses, so a column-start line whose left
// edge sits fractionally left of the gutter mean buckets into the column it opens
// (Blocks stays column-major). columnOfLine snaps only when the line's body extends
// past the gutter, so a short indented/right-aligned LEFT-column line is NOT pulled
// forward (the snap has no gap>colGap gate here). Pass 0 for colGap = strict boundary.
func groupLinesIntoBlocks(lines []Line, gutters []float64, colGap float64) []Block {
	snapTol := (colMergeFactor / colGapFactor) * colGap
	byCol := map[int][]Line{}
	for _, l := range lines {
		c := columnOfLine(l.X, l.X+l.W, gutters, snapTol)
		byCol[c] = append(byCol[c], l)
	}
	cols := make([]int, 0, len(byCol))
	for c := range byCol {
		cols = append(cols, c)
	}
	sort.Ints(cols)
	var blocks []Block
	for _, c := range cols {
		col := byCol[c]
		sort.SliceStable(col, func(i, j int) bool { return col[i].Y > col[j].Y })
		blocks = append(blocks, groupColumnLines(col)...)
	}
	return blocks
}

// groupColumnLines splits one column's top-to-bottom lines into blocks: a new block
// starts when the vertical gap from the previous line exceeds blockGapFactor times
// the taller of the two lines' heights.
func groupColumnLines(lines []Line) []Block {
	var blocks []Block
	var cur []Line
	for _, l := range lines {
		if len(cur) == 0 {
			cur = []Line{l}
			continue
		}
		prev := cur[len(cur)-1]
		ref := prev.H
		if l.H > ref {
			ref = l.H
		}
		gap := prev.Y - (l.Y + l.H)
		if ref > 0 && gap > blockGapFactor*ref {
			blocks = append(blocks, blockFromLines(cur))
			cur = []Line{l}
			continue
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		blocks = append(blocks, blockFromLines(cur))
	}
	return blocks
}

// blockFromLines assembles one Block from a top-to-bottom run of lines. S is the
// lines' S joined by "\n"; the bounding box spans every line; Font and FontSize come
// from the first (topmost) line. S is built with a strings.Builder (linear, not the
// O(n^2) of repeated concatenation): a block may merge a whole column of lines, so a
// crafted page with one giant block must not amplify into quadratic cost.
func blockFromLines(ls []Line) Block {
	minX, maxX := ls[0].X, ls[0].X+ls[0].W
	minY, maxY := ls[0].Y, ls[0].Y+ls[0].H
	var sb strings.Builder
	sb.WriteString(ls[0].S)
	for _, l := range ls[1:] {
		sb.WriteByte('\n')
		sb.WriteString(l.S)
		if l.X < minX {
			minX = l.X
		}
		if right := l.X + l.W; right > maxX {
			maxX = right
		}
		if l.Y < minY {
			minY = l.Y
		}
		if top := l.Y + l.H; top > maxY {
			maxY = top
		}
	}
	return Block{
		S: sb.String(), X: minX, Y: minY, W: maxX - minX, H: maxY - minY,
		Font: ls[0].Font, FontSize: ls[0].FontSize, Lines: ls,
	}
}
