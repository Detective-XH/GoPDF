// tables_rotated_text_test.go — unit tests for the skew-text drop filter and the
// re-scope contract that confines it to the table path only.
//
// dropSkewRotatedText removes diagonal/watermark glyphs from table-reconstruction
// word assembly while keeping all axis-aligned text (0°/90°/180°/270°). Public
// Words()/Lines()/Blocks() must return all glyphs unfiltered; only the table word
// source applies the filter (before word assembly). These tests verify both the
// filter's correctness and the scope boundary.
package pdf

import (
	"testing"
)

// TestDropSkewRotatedText verifies that dropSkewRotatedText keeps exactly the
// glyphs within skewAngleTolDeg of an axis (0°/90°/180°/270°) and drops the
// rest.
func TestDropSkewRotatedText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		rotation float64
		keep     bool
		label    string
	}{
		// ── Axis-aligned: must be kept ────────────────────────────────────────
		{0, true, "horizontal (0°)"},
		{5, true, "near horizontal (5°)"},
		{10, true, "boundary-keep (10° == skewAngleTolDeg)"},
		{80, true, "near 90° from below (80°)"},
		{85, true, "near 90° from below (85°)"},
		{90, true, "vertical (90°)"},
		{95, true, "near 90° from above (95°)"},
		{170, true, "near 180° from below (170°)"},
		{180, true, "horizontal flipped (180°)"},
		{265, true, "near 270° from below (265°)"},
		{270, true, "vertical flipped (270°)"},
		{355, true, "near 360°/0° from below (355°)"},
		{-90, true, "negative vertical (−90°)"},
		{-5, true, "small negative (−5°)"},
		// ── Skew/diagonal: must be dropped ───────────────────────────────────
		{11, false, "just outside tolerance (11°)"},
		{40, false, "shallow diagonal (40°)"},
		{45, false, "pure diagonal (45°) — watermark angle"},
		{135, false, "135° diagonal"},
		{225, false, "225° diagonal"},
		{315, false, "315° diagonal"},
		{-45, false, "negative diagonal (−45°)"},
		{30, false, "30° arc label"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			in := []Text{{S: "x", Rotation: tc.rotation}}
			out := dropSkewRotatedText(in)
			if tc.keep && len(out) == 0 {
				t.Errorf("rotation %.1f°: expected KEEP but got dropped", tc.rotation)
			}
			if !tc.keep && len(out) != 0 {
				t.Errorf("rotation %.1f°: expected DROP but got kept", tc.rotation)
			}
		})
	}
}

// TestDropSkewRotatedTextEmptyInput verifies that a nil/empty slice returns nil.
func TestDropSkewRotatedTextEmptyInput(t *testing.T) {
	t.Parallel()
	if out := dropSkewRotatedText(nil); out != nil {
		t.Errorf("nil input: expected nil output, got %v", out)
	}
	if out := dropSkewRotatedText([]Text{}); out != nil {
		t.Errorf("empty input: expected nil output, got %v", out)
	}
}

// TestDropSkewRotatedTextAllDropped verifies that an all-skew input returns nil
// (not an empty slice), preserving the nil-check contract of wordsFromContent.
func TestDropSkewRotatedTextAllDropped(t *testing.T) {
	t.Parallel()
	in := []Text{{S: "A", Rotation: 45}, {S: "B", Rotation: -45}, {S: "C", Rotation: 135}}
	out := dropSkewRotatedText(in)
	if out != nil {
		t.Errorf("all-dropped: expected nil output, got len=%d", len(out))
	}
}

// TestDropSkewRotatedTextMixed verifies that axis-aligned glyphs survive a
// mixed input that also contains diagonal ones.
func TestDropSkewRotatedTextMixed(t *testing.T) {
	t.Parallel()
	in := []Text{
		{S: "D", Rotation: 0},   // keep
		{S: "W", Rotation: 45},  // drop (watermark)
		{S: "a", Rotation: 90},  // keep (landscape)
		{S: "t", Rotation: 225}, // drop (diagonal)
	}
	out := dropSkewRotatedText(in)
	if len(out) != 2 {
		t.Fatalf("mixed: expected 2 kept glyphs, got %d: %v", len(out), out)
	}
	if out[0].S != "D" || out[1].S != "a" {
		t.Errorf("mixed: kept wrong glyphs: %v", out)
	}
}

// TestTableWordsFilterScope is the re-scope contract test. It verifies that the
// skew filter is confined to the table word-assembly path and does NOT suppress
// diagonal text from the public word-assembly path:
//
//	(a) wordsFromContent on an unfiltered Content KEEPS diagonal glyphs — this
//	    is the behaviour Words()/Lines()/Blocks() depend on.
//	(b) wordsFromContent on a dropSkewRotatedText-filtered Content DROPS diagonal
//	    glyphs — this is exactly what Tables() does internally before assembling
//	    words for grid reconstruction.
//
// A synthetic Content is used so the test is self-contained and fixture-free.
func TestTableWordsFilterScope(t *testing.T) {
	t.Parallel()

	const (
		y        = 100.0
		h        = 12.0
		fontSize = 12.0
	)
	// Two glyphs at the same Y level, well separated in X so they form distinct
	// words (144 pt gap far exceeds any word-gap threshold).
	dataGlyph := Text{S: "9", Rotation: 0, X: 50, Y: y, W: 6, H: h, FontSize: fontSize}
	skewGlyph := Text{S: "W", Rotation: 45, X: 200, Y: y, W: 8, H: h, FontSize: fontSize}

	c := Content{Text: []Text{dataGlyph, skewGlyph}}

	// ── (a) Public-API word-assembly path: unfiltered Content ─────────────────
	// wordsFromContent(c) is the same function Words() drives. After the re-scope
	// it must NOT call dropSkewRotatedText itself, so both glyphs appear in output.
	publicWords := wordsFromContent(c)
	var gotData, gotSkew bool
	for _, w := range publicWords {
		if w.S == "9" {
			gotData = true
		}
		if w.S == "W" {
			gotSkew = true
		}
	}
	if !gotData {
		t.Error("public path (a): axis-aligned glyph '9' missing from Words output — unexpected regression")
	}
	if !gotSkew {
		t.Error("public path (a): diagonal glyph 'W' was dropped from Words output — re-scope broke public API; Words() must return diagonal text unfiltered")
	}

	// ── (b) Table word-assembly path: skew-filtered Content ───────────────────
	// Tables() builds tableC := Content{Text: dropSkewRotatedText(c.Text), ...}
	// then calls wordsFromContent(tableC). The diagonal glyph must be absent.
	tableC := Content{Text: dropSkewRotatedText(c.Text), Rect: c.Rect, Stroke: c.Stroke}
	tableWords := wordsFromContent(tableC)
	gotData, gotSkew = false, false
	for _, w := range tableWords {
		if w.S == "9" {
			gotData = true
		}
		if w.S == "W" {
			gotSkew = true
		}
	}
	if !gotData {
		t.Error("table path (b): axis-aligned glyph '9' missing — skew filter must not drop 0° text")
	}
	if gotSkew {
		t.Error("table path (b): diagonal glyph 'W' survived in table words — skew filter must remove it before word assembly so watermarks do not contaminate cell values")
	}
}
