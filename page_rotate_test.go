// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"fmt"
	"strconv"
	"testing"
)

// rotateTokenPagePDF builds a single-page PDF whose page dict carries
// "/Rotate <rotateToken>" verbatim, MediaBox [0 0 612 792], /F1 = Helvetica, and the
// content stream. The raw token lets a test inject a malformed value (e.g. the Real
// "90.0"); rotatedPagePDF wraps it for the integer cases.
func rotateTokenPagePDF(rotateToken, content string) []byte {
	page := "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Rotate " + rotateToken +
		" /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		page,
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

// rotatedPagePDF is rotateTokenPagePDF with an integer /Rotate. Object layout mirrors
// buildTextPDF so a /Rotate 0 page is byte-comparable to a no-/Rotate one.
func rotatedPagePDF(rotate int, content string) []byte {
	return rotateTokenPagePDF(strconv.Itoa(rotate), content)
}

// firstGlyphRotated returns the first non-blank Text on page 1 of pdf.
func firstGlyphRotated(t *testing.T, pdf []byte) Text {
	t.Helper()
	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	for _, tx := range r.Page(1).Content().Text {
		if s := tx.S; s != "" && s != "\n" && s != " " {
			return tx
		}
	}
	t.Fatalf("no glyph in %d-byte pdf", len(pdf))
	return Text{}
}

// TestPageRotateCoordinateTransform locks the three rotation matrices: an upright
// glyph whose baseline origin is user-space (100,200) lands at the deterministic
// display-space transform for each /Rotate, and FontSize/Rotation reflect the applied
// page rotation. This is the non-circular matrix lock (a wrong index/sign fails it).
func TestPageRotateCoordinateTransform(t *testing.T) {
	const content = "BT /F1 12 Tf 1 0 0 1 100 200 Tm (X) Tj ET" // MediaBox W=612 H=792
	cases := []struct {
		rotate                            int
		wantX, wantY, wantFontSize, wantR float64
	}{
		{0, 100, 200, 12, 0},      // identity
		{90, 200, 512, 0, -90},    // (y, W-x); x-scale collapses, baseline -90
		{180, 512, 592, -12, 180}, // (W-x, H-y); upside-down: FontSize flips negative
		{270, 592, 100, 0, 90},    // (H-y, x); baseline +90
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("rotate-%d", c.rotate), func(t *testing.T) {
			g := firstGlyphRotated(t, rotatedPagePDF(c.rotate, content))
			if !approxEq(g.X, c.wantX) || !approxEq(g.Y, c.wantY) {
				t.Errorf("pos = (%v,%v), want (%v,%v)", g.X, g.Y, c.wantX, c.wantY)
			}
			if !approxEq(g.FontSize, c.wantFontSize) {
				t.Errorf("FontSize = %v, want ~%v", g.FontSize, c.wantFontSize)
			}
			if !approxEq(g.Rotation, c.wantR) {
				t.Errorf("Rotation = %v, want ~%v", g.Rotation, c.wantR)
			}
			if g.H < 0 {
				t.Errorf("H = %v, must be non-negative", g.H)
			}
		})
	}
}

// TestPageRotateCancelsContentRotation locks the valuable case: content authored
// rotated (the rotated-90.pdf Tm "0 1 -1 0" — baseline up, FontSize collapses, fires
// WarningRotatedText without /Rotate) plus /Rotate 90 cancels back to a horizontal
// display-space baseline. FontSize recovers to 12, Rotation 0, glyph at (400,540).
func TestPageRotateCancelsContentRotation(t *testing.T) {
	g := firstGlyphRotated(t, rotatedPagePDF(90, "BT /F1 12 Tf 0 1 -1 0 72 400 Tm (R) Tj ET"))
	if !approxEq(g.FontSize, 12) {
		t.Errorf("FontSize = %v, want ~12 (recovered, not collapsed 0)", g.FontSize)
	}
	if !approxEq(g.Rotation, 0) {
		t.Errorf("Rotation = %v, want ~0 (horizontal in display space)", g.Rotation)
	}
	if !approxEq(g.X, 400) || !approxEq(g.Y, 540) {
		t.Errorf("pos = (%v,%v), want (400,540)", g.X, g.Y)
	}
	if !approxEq(g.H, 12) {
		t.Errorf("H = %v, want ~12", g.H)
	}
}

// TestPageRotateNormalization locks Page.Rotate()'s mod-360 + multiple-of-90 snap.
func TestPageRotateNormalization(t *testing.T) {
	cases := []struct{ raw, want int }{
		{0, 0}, {90, 90}, {180, 180}, {270, 270},
		{360, 0}, {450, 90}, {720, 0}, {-90, 270}, {-360, 0}, {45, 0},
		// Huge value (> 32-bit int range): reduced mod 360 in int64 first, so it is
		// architecture-independent. 4294967386 % 360 = 346 → not a multiple of 90 → 0.
		{4294967386, 0},
	}
	for _, c := range cases {
		r, err := OpenBytes(rotatedPagePDF(c.raw, "BT /F1 12 Tf 1 0 0 1 100 200 Tm (X) Tj ET"))
		if err != nil {
			t.Fatalf("raw %d: OpenBytes: %v", c.raw, err)
		}
		if got := r.Page(1).Rotate(); got != c.want {
			t.Errorf("Rotate() raw %d = %d, want %d", c.raw, got, c.want)
		}
	}
}

// TestPageRotateRealIsMalformed locks that a non-integer /Rotate (malformed — the spec
// mandates an integer multiple of 90) reads as 0: Int64() returns 0 for a non-integer,
// so /Rotate 90.0 is treated as unrotated, not 90.
func TestPageRotateRealIsMalformed(t *testing.T) {
	r, err := OpenBytes(rotateTokenPagePDF("90.0", "BT /F1 12 Tf 1 0 0 1 100 200 Tm (X) Tj ET"))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if got := r.Page(1).Rotate(); got != 0 {
		t.Errorf("Rotate() with /Rotate 90.0 = %d, want 0 (non-integer is malformed)", got)
	}
}

// TestPageRotateZeroByteIdentical locks that /Rotate 0 is the literal identity: a
// /Rotate 0 page extracts the same Text as a page with no /Rotate key (rotateMatrix
// returns the literal ident on both, no MediaBox read, no float noise).
func TestPageRotateZeroByteIdentical(t *testing.T) {
	const content = "BT /F1 12 Tf 1 0 0 1 100 200 Tm (Hello) Tj ET"
	rAbsent, err := OpenBytes(buildTextPDF(content)) // no /Rotate key
	if err != nil {
		t.Fatalf("absent: %v", err)
	}
	rZero, err := OpenBytes(rotatedPagePDF(0, content)) // /Rotate 0
	if err != nil {
		t.Fatalf("zero: %v", err)
	}
	// Direct literal-ident lock (independent of builder parity): both pages must map
	// through the literal identity matrix — no MediaBox read, no float noise.
	if m := rZero.Page(1).rotateMatrix(); m != ident {
		t.Errorf("/Rotate 0 rotateMatrix() = %v, want ident", m)
	}
	if m := rAbsent.Page(1).rotateMatrix(); m != ident {
		t.Errorf("absent /Rotate rotateMatrix() = %v, want ident", m)
	}
	a, z := rAbsent.Page(1).Content().Text, rZero.Page(1).Content().Text
	if len(a) != len(z) {
		t.Fatalf("len mismatch: absent %d, /Rotate 0 %d", len(a), len(z))
	}
	for i := range a {
		if a[i] != z[i] {
			t.Errorf("glyph %d: absent %+v != /Rotate 0 %+v", i, a[i], z[i])
		}
	}
}
