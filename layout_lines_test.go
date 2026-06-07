// layout_lines_test.go — tests for Page.Lines() line-level grouping.
package pdf

import (
	"strings"
	"testing"
)

// TestLinesSingleColumn verifies that a two-line single-column text produces
// two Lines in top-to-bottom reading order.
func TestLinesSingleColumn(t *testing.T) {
	// Monospace 12pt: each char W=7.2pt, line-height 14pt.
	// Line 1: "Hello World" at y=700 → words ["Hello","World"]
	// Line 2: "Foo Bar"     at y=686 → words ["Foo","Bar"]
	// Y-gap = 14pt; tol = 12*0.5 = 6pt → separate bands. ✓
	stream := "BT\n/F1 12 Tf\n100 700 Td\n(Hello World) Tj\n0 -14 Td\n(Foo Bar) Tj\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	page := r.Page(1)
	lines, err := page.Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0].S != "Hello World" {
		t.Errorf("line[0].S = %q, want %q", lines[0].S, "Hello World")
	}
	if lines[1].S != "Foo Bar" {
		t.Errorf("line[1].S = %q, want %q", lines[1].S, "Foo Bar")
	}
	if lines[0].Y <= lines[1].Y {
		t.Errorf("reading order wrong: line[0].Y=%v <= line[1].Y=%v", lines[0].Y, lines[1].Y)
	}
	if len(lines[0].Words) != 2 {
		t.Errorf("line[0] has %d words, want 2", len(lines[0].Words))
	}
	if len(lines[1].Words) != 2 {
		t.Errorf("line[1] has %d words, want 2", len(lines[1].Words))
	}
}

// TestLinesMultiStyle verifies that glyphs with different font sizes on the
// same visual baseline are merged into one Line.
func TestLinesMultiStyle(t *testing.T) {
	// "Hello" at 12pt then " World" at 6pt, same Y=700.
	// Both land in the same y-band → Words() yields ["Hello","World"] on one band.
	// Lines() must merge them into one Line.
	stream := "BT\n/F1 12 Tf\n100 700 Td\n(Hello) Tj\n/F1 6 Tf\n( World) Tj\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := r.Page(1).Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0].S, "Hello") || !strings.Contains(lines[0].S, "World") {
		t.Errorf("line[0].S = %q, want both Hello and World", lines[0].S)
	}
}

// TestLinesCJK verifies that a CJK line returns sensible (non-empty) text.
func TestLinesCJK(t *testing.T) {
	// UniGB-UCS2-H encoding; UCS-2 BE bytes for U+4E2D U+6587 ("中文").
	// Zero-advance glyphs merge into one Word; Lines() wraps it into one Line.
	stream := "BT\n/F1 12 Tf\n100 700 Td\n(\x4e\x2d\x65\x87) Tj\nET"
	r, err := OpenBytes(buildCJKWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := r.Page(1).Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("got 0 lines, want at least 1")
	}
	if lines[0].S == "" {
		t.Error("line[0].S is empty, want non-empty CJK text")
	}
}

// TestLinesEmpty verifies that a page with no text returns (nil, nil).
func TestLinesEmpty(t *testing.T) {
	stream := "BT\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := r.Page(1).Lines()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lines != nil {
		t.Errorf("got %v, want nil", lines)
	}
}

// TestLinesWordsNoRegression verifies that calling Lines() does not affect the
// output of a subsequent Words() call on the same page.
func TestLinesWordsNoRegression(t *testing.T) {
	stream := "BT\n/F1 12 Tf\n100 700 Td\n(Hello World) Tj\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	page := r.Page(1)
	if _, err = page.Lines(); err != nil {
		t.Fatal(err)
	}
	ws, err := page.Words()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 {
		t.Errorf("Words() after Lines() returned %d words, want 2", len(ws))
	}
}

// TestLinesBoundingBox verifies that Line.X, Y, W, H span the full line extent.
func TestLinesBoundingBox(t *testing.T) {
	// "Hello World" in monospace 12pt starting at x=100, y=700.
	// "Hello": x=100, W=36pt; " ": word boundary; "World": x≈143, W=36pt.
	// Line.X should be ~100; Line.W should cover both words (>36pt).
	stream := "BT\n/F1 12 Tf\n100 700 Td\n(Hello World) Tj\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := r.Page(1).Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	l := lines[0]
	if l.X < 99 || l.X > 101 {
		t.Errorf("line.X = %.1f, want ~100", l.X)
	}
	if l.W <= 36 {
		t.Errorf("line.W = %.1f, want > 36 (must span both words)", l.W)
	}
	if l.H <= 0 {
		t.Errorf("line.H = %.1f, want > 0", l.H)
	}
}

// TestWordsWithinBandBaselineShift guards the within-word Y-tracking fix in
// wordsFromBand. Before the fix, cur.Y was taken from the first (leftmost) glyph
// and H was max(FontSize), so a subscript glyph at lower Y was silently excluded
// from the word's bounding box. After the fix, cur.Y = min(glyph.Y) and
// H = max(glyph.Y+FontSize) - cur.Y across all glyphs in the word.
//
// Setup (12pt monospace, glyph W=7.2pt):
//
//	"H" at (100,706): occupies X=[100,107.2].
//	"2" at (107,700): gap = 107-107.2 = -0.2, threshold = 7.2*0.3 = 2.16 → same Word.
//	Before fix: Word.Y=706, H=12 → bbox [706,718]. Y=700 subscript excluded.
//	After  fix: Word.Y=700, H=18 → bbox [700,718]. Subscript correctly included.
func TestWordsWithinBandBaselineShift(t *testing.T) {
	stream := "BT\n/F1 12 Tf\n" +
		"1 0 0 1 100 706 Tm\n(H) Tj\n" +
		"1 0 0 1 107 700 Tm\n(2) Tj\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := r.Page(1).Words()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("got %d words, want 1 (glyphs should merge into one word)", len(ws))
	}
	if ws[0].Y > 700.5 {
		t.Errorf("Word.Y = %.1f, want ~700 (subscript glyph at Y=700 excluded from bbox)", ws[0].Y)
	}
	if ws[0].H < 17.5 {
		t.Errorf("Word.H = %.1f, want ~18 (must span Y=700 to top Y=718)", ws[0].H)
	}
}

// TestLinesBaselineShift is the regression test for the Option-B anchor-mismatch
// bug. The upper line has its leftmost word baseline-shifted below the band
// anchor; Option-B (re-deriving bands from Word.Y) would false-merge the two
// lines because Word.Y < band anchor — closing the gap to the lower line below
// the tolerance threshold. Option-A (bandsByY shared helper) is immune because
// it uses the original anchor.
//
// Setup (12pt, tol = FontSize*0.5 = 6):
//
//	Upper band: "H" at X=120,Y=706 (anchor); "x" at X=100,Y=700 (leftmost, subscript).
//	Words() bands: 706-694=12 > 6 → two separate bands. ✓
//	Option-B Lines(): cur.Y = Word.Y of leftmost word = 700; 700-694=6, not > 6 → FALSE MERGE.
//	Option-A Lines() (this impl): bandsByY anchors at 706; 706-694=12 > 6 → two Lines. ✓
func TestLinesBaselineShift(t *testing.T) {
	// Use Tm (absolute text matrix) to place glyphs at precise coordinates.
	// "H" at (120,706), "x" at (100,700) — same band (706-700=6, not > tol=6).
	// "F" at (100,694) — new band (706-694=12 > 6).
	stream := "BT\n/F1 12 Tf\n" +
		"1 0 0 1 120 706 Tm\n(H) Tj\n" +
		"1 0 0 1 100 700 Tm\n(x) Tj\n" +
		"1 0 0 1 100 694 Tm\n(F) Tj\nET"
	r, err := OpenBytes(buildWordsPDF(stream))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := r.Page(1).Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (baseline-shift / anchor-mismatch regression)", len(lines))
	}
	// Upper line contains "x" (Y=700) and "H" (Y=706, H=12pt); bbox must span
	// Y=700 to Y=718, so l.H >= 18. Catches the old under-count bug where l.H
	// was only max(word.H) = 12 instead of the true vertical span.
	if lines[0].H < 18 {
		t.Errorf("upper line H = %.1f, want >= 18 (mixed-baseline bbox under-count)", lines[0].H)
	}
}
