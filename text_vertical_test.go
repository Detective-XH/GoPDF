// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import "testing"

// glyphsOf returns the non-blank Text entries on page 1 of a PDF built from a
// single content stream — the glyphs whose X/Y advance we assert. Separators
// (the synthetic "\n"/" ") and empties are skipped.
func glyphsOf(t *testing.T, content, fontBody string) []Text {
	t.Helper()
	r, err := OpenBytes(encodingPagePDF(content, fontBody))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	var out []Text
	for _, tx := range r.Page(1).Content().Text {
		if s := tx.S; s != "" && s != "\n" && s != " " {
			out = append(out, tx)
		}
	}
	return out
}

// verticalFontBody is a synthetic Type1 font with a vertical (-V) predefined CMap.
// UCS-2 BE bytes 0x4E 0x2D ("N-") decode to 中 (U+4E2D); it has no /Widths, so the
// horizontal advance is 0 — exactly the metric-less case WS2's default vertical
// displacement covers.
const verticalFontBody = "<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic" +
	" /Encoding /UniJIS-UCS2-V >>"

// shiftJISVerticalFontBody is a vertical (-V) font whose multibyte CMap decodes a
// lone "\n"/" " separator to a real rune (unlike the ucs2 -V fonts, which drop an
// odd trailing byte). It exercises the synthetic-separator advance path. Shift-JIS
// bytes 0x82 0xA0 decode to あ (U+3042).
const shiftJISVerticalFontBody = "<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic" +
	" /Encoding /90ms-RKSJ-V >>"

// TestVerticalWritingAdvance locks WS2: a -V CMap font advances glyphs DOWN the
// page (Y decreasing by one em) with X held constant, for a plain Tj run and a TJ
// run with numeric kerning; a non-V font keeps horizontal layout. Before WS2 every
// glyph overprinted at one point (horizontal advance is 0 for these fonts). The
// cases are named functions so the dispatcher stays under the gocyclo threshold.
func TestVerticalWritingAdvance(t *testing.T) {
	t.Run("plain-Tj-stacks-down", verticalPlainTjStacksDown)
	t.Run("TJ-kerning-stays-on-Y-axis", verticalTJKerningStaysOnYAxis)
	t.Run("horizontal-font-unaffected", verticalHorizontalFontUnaffected)
	t.Run("TJ-then-Tj-no-extra-em", verticalTJThenTjNoExtraEm)
}

func verticalPlainTjStacksDown(t *testing.T) {
	// Three 中 at absolute Tm (100,700), Tfs 12 → Y steps down by 12 each.
	g := glyphsOf(t, "BT /F1 12 Tf 1 0 0 1 100 700 Tm (N-N-N-) Tj ET", verticalFontBody)
	if len(g) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(g))
	}
	wantY := []float64{700, 688, 676}
	for i, tx := range g {
		if !approxEq(tx.X, 100) {
			t.Errorf("glyph %d X = %v, want 100 (no horizontal drift)", i, tx.X)
		}
		if !approxEq(tx.Y, wantY[i]) {
			t.Errorf("glyph %d Y = %v, want %v", i, tx.Y, wantY[i])
		}
	}
}

func verticalTJKerningStaysOnYAxis(t *testing.T) {
	// glyph0 (100,700) → advance -12 → (100,688) → kern 500 → ty=-500/1000*12
	// = -6 → glyph1 (100,682). The diagonal-smear bug lands glyph1 at X=94;
	// no-kern lands it at Y=688. Asserting (100,682) rules out both.
	g := glyphsOf(t, "BT /F1 12 Tf 1 0 0 1 100 700 Tm [(N-) 500 (N-)] TJ ET", verticalFontBody)
	if len(g) != 2 {
		t.Fatalf("got %d glyphs, want 2", len(g))
	}
	if !approxEq(g[0].X, 100) || !approxEq(g[0].Y, 700) {
		t.Errorf("glyph 0 = (%v,%v), want (100,700)", g[0].X, g[0].Y)
	}
	if !approxEq(g[1].X, 100) || !approxEq(g[1].Y, 682) {
		t.Errorf("glyph 1 = (%v,%v), want (100,682)", g[1].X, g[1].Y)
	}
}

func verticalHorizontalFontUnaffected(t *testing.T) {
	// An -H/none font with real widths advances along x, Y constant — proves
	// the s.g.vertical gate does not touch horizontal layout.
	g := glyphsOf(t, "BT /F1 12 Tf 1 0 0 1 100 700 Tm (AB) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica"+
			" /FirstChar 65 /LastChar 66 /Widths [500 500] >>")
	if len(g) != 2 {
		t.Fatalf("got %d glyphs, want 2", len(g))
	}
	for i, tx := range g {
		if !approxEq(tx.Y, 700) {
			t.Errorf("glyph %d Y = %v, want 700 (horizontal: Y constant)", i, tx.Y)
		}
	}
	if g[1].X <= g[0].X {
		t.Errorf("horizontal advance: glyph1 X %v not > glyph0 X %v", g[1].X, g[0].X)
	}
}

func verticalTJThenTjNoExtraEm(t *testing.T) {
	// A TJ array then a real Tj glyph in the SAME text object, multibyte -V so
	// the synthetic "\n" after the TJ decodes to a real rune. That separator
	// must not advance the matrix, or the trailing Tj glyph drops an extra em.
	// TJ shows ああ at (100,700)/(100,688); the Tj あ must continue at (100,676),
	// not (100,664). 0x82A0 = あ under Shift-JIS.
	g := glyphsOf(t, "BT /F1 12 Tf 1 0 0 1 100 700 Tm [(\x82\xa0\x82\xa0)] TJ (\x82\xa0) Tj ET",
		shiftJISVerticalFontBody)
	if len(g) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(g))
	}
	wantY := []float64{700, 688, 676}
	for i, tx := range g {
		if !approxEq(tx.X, 100) {
			t.Errorf("glyph %d X = %v, want 100", i, tx.X)
		}
		if !approxEq(tx.Y, wantY[i]) {
			t.Errorf("glyph %d Y = %v, want %v (separator must not advance)", i, tx.Y, wantY[i])
		}
	}
}
