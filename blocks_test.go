package pdf

import (
	"strings"
	"testing"
)

// TestGroupColumnLinesGapSplit: a small gap keeps lines together; a large gap splits.
func TestGroupColumnLinesGapSplit(t *testing.T) {
	lines := []Line{
		{S: "a", X: 10, Y: 100, W: 20, H: 12, FontSize: 12},
		{S: "b", X: 10, Y: 88, W: 20, H: 12, FontSize: 12}, // gap = 100-(88+12)=0  -> same block
		{S: "c", X: 10, Y: 40, W: 20, H: 12, FontSize: 12}, // gap = 88-(40+12)=36 -> new block (>0.6*12=7.2)
	}
	blocks := groupColumnLines(lines)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	if blocks[0].S != "a\nb" {
		t.Errorf("block 0 S = %q, want %q", blocks[0].S, "a\nb")
	}
	if blocks[1].S != "c" {
		t.Errorf("block 1 S = %q, want %q", blocks[1].S, "c")
	}
}

// TestGroupLinesIntoBlocksColumnMajor: a 2-column band-order line slice is reordered
// column-major (all of col 0 before col 1), proving no row interleave.
func TestGroupLinesIntoBlocksColumnMajor(t *testing.T) {
	gutters := []float64{200}
	// Emission (band) order: col0-top, col1-top, col0-bottom, col1-bottom.
	lines := []Line{
		{S: "L1", X: 10, Y: 100, W: 50, H: 12, FontSize: 12},
		{S: "R1", X: 250, Y: 100, W: 50, H: 12, FontSize: 12},
		{S: "L2", X: 10, Y: 86, W: 50, H: 12, FontSize: 12},
		{S: "R2", X: 250, Y: 86, W: 50, H: 12, FontSize: 12},
	}
	blocks := groupLinesIntoBlocks(lines, gutters, 0)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (one per column)", len(blocks))
	}
	if blocks[0].S != "L1\nL2" {
		t.Errorf("block 0 (left column) S = %q, want %q", blocks[0].S, "L1\nL2")
	}
	if blocks[1].S != "R1\nR2" {
		t.Errorf("block 1 (right column) S = %q, want %q", blocks[1].S, "R1\nR2")
	}
}

// TestBlockBoundingBox: the block bbox spans every constituent line.
func TestBlockBoundingBox(t *testing.T) {
	lines := []Line{
		{S: "wide", X: 10, Y: 100, W: 80, H: 12, Font: "F1", FontSize: 12},
		{S: "x", X: 5, Y: 88, W: 20, H: 12, Font: "F2", FontSize: 10},
	}
	b := blockFromLines(lines)
	if b.X != 5 || b.W != 85 { // minX=5, maxX=max(90,25)=90 -> W=85
		t.Errorf("bbox X=%.0f W=%.0f, want X=5 W=85", b.X, b.W)
	}
	if b.Y != 88 || b.H != 24 { // minY=88, maxY=max(112,100)=112 -> H=24
		t.Errorf("bbox Y=%.0f H=%.0f, want Y=88 H=24", b.Y, b.H)
	}
	if b.Font != "F1" || b.FontSize != 12 {
		t.Errorf("Font=%q size=%.0f, want first-line F1/12", b.Font, b.FontSize)
	}
}

// TestBlocksEmptyContent: no text -> (nil, nil).
func TestBlocksEmptyContent(t *testing.T) {
	if got := blocksFromContent(Content{}); got != nil {
		t.Errorf("blocksFromContent(empty) = %v, want nil", got)
	}
}

// TestBlocksEndToEnd: two paragraphs separated by a large vertical gap become two
// blocks via the public Page.Blocks() path.
func TestBlocksEndToEnd(t *testing.T) {
	stream := "BT\n/F1 12 Tf\n100 700 Td\n(Para one line one) Tj\n" +
		"0 -14 Td\n(Para one line two) Tj\n0 -40 Td\n(Para two) Tj\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := r.Page(1).Blocks()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	if blocks[0].S != "Para one line one\nPara one line two" {
		t.Errorf("block 0 S = %q", blocks[0].S)
	}
	if blocks[1].S != "Para two" {
		t.Errorf("block 1 S = %q", blocks[1].S)
	}
	if blocks[0].Y <= blocks[1].Y {
		t.Errorf("blocks not top-to-bottom: b0.Y=%.0f b1.Y=%.0f", blocks[0].Y, blocks[1].Y)
	}
}

// TestBlockFromLinesManyLines exercises the many-lines-in-one-block path (a whole
// column merged into a single Block) that the linear strings.Builder construction
// is sized for: S must join every line with "\n" and conserve every line, with no
// quadratic-concatenation regression slipping back in unnoticed.
func TestBlockFromLinesManyLines(t *testing.T) {
	const n = 1000
	lines := make([]Line, n)
	for i := range lines {
		// Stacked top-to-bottom; H spans Y so blockFromLines' bbox tracks the run.
		lines[i] = Line{S: "ln", X: 10, Y: float64(2 * (n - i)), W: 20, H: 12, FontSize: 12}
	}
	b := blockFromLines(lines)
	if len(b.Lines) != n {
		t.Fatalf("Block.Lines = %d, want %d (lines dropped/duplicated)", len(b.Lines), n)
	}
	if got := strings.Count(b.S, "\n"); got != n-1 {
		t.Errorf("Block.S has %d newlines, want %d (one between each of %d lines)", got, n-1, n)
	}
	if b.X != 10 || b.W != 20 {
		t.Errorf("bbox X=%.0f W=%.0f, want X=10 W=20", b.X, b.W)
	}
}
