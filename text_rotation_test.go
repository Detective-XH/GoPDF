// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"math"
	"testing"
)

// firstGlyph returns the first non-blank Text on page 1 of a PDF built from a
// single content stream — the glyph whose H/Rotation we assert. Separators
// (spaces, the synthetic "\n") and any empty entries are skipped.
func firstGlyph(t *testing.T, contentStream string) Text {
	t.Helper()
	r, err := OpenBytes(buildTextPDF(contentStream))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	for _, tx := range r.Page(1).Content().Text {
		if s := tx.S; s != "" && s != "\n" && s != " " {
			return tx
		}
	}
	t.Fatalf("no glyph in content %q", contentStream)
	return Text{}
}

func approxEq(got, want float64) bool { return math.Abs(got-want) < 1e-2 }

// TestTextRotationAndHeight locks the two WS1 fields against the text rendering
// matrix. Tfs is 12 throughout; each case sets a known Trm via an absolute Tm
// (and Tz for horizontal scaling) so FontSize/H/Rotation are exactly predictable.
func TestTextRotationAndHeight(t *testing.T) {
	cases := []struct {
		name                       string
		content                    string
		wantFontSize, wantH, wantR float64
	}{
		{
			// Upright: FontSize == H == Tfs, Rotation 0.
			name:         "horizontal",
			content:      "BT /F1 12 Tf 1 0 0 1 100 200 Tm (X) Tj ET",
			wantFontSize: 12, wantH: 12, wantR: 0,
		},
		{
			// 90° CCW: x-scale collapses (FontSize 0) but H recovers the true 12,
			// Rotation 90. This is the case Text.H exists for.
			name:         "rotated-90-ccw",
			content:      "BT /F1 12 Tf 0 1 -1 0 72 400 Tm (X) Tj ET",
			wantFontSize: 0, wantH: 12, wantR: 90,
		},
		{
			// 45° CCW: FontSize is the x-projection (~8.485) while H is the full 12.
			name:         "rotated-45-ccw",
			content:      "BT /F1 12 Tf 0.70710678 0.70710678 -0.70710678 0.70710678 100 100 Tm (X) Tj ET",
			wantFontSize: 8.4853, wantH: 12, wantR: 45,
		},
		{
			// Vertical flip (Trm[1][1] = -12): H must be the POSITIVE 12, not -12.
			// Guards the non-negativity contract (a raw-Trm[1][1] impl fails here).
			name:         "vertical-flip",
			content:      "BT /F1 12 Tf 1 0 0 -1 100 100 Tm (X) Tj ET",
			wantFontSize: 12, wantH: 12, wantR: 0,
		},
		{
			// Horizontal scaling Th=2 (Tz 200): FontSize = Tfs·Th = 24, but H = Tfs
			// = 12. Proves H is intentionally NOT FontSize; guards a future "cleanup"
			// that would collapse the two.
			name:         "horizontal-scaling-th2",
			content:      "BT /F1 12 Tf 200 Tz 1 0 0 1 100 100 Tm (X) Tj ET",
			wantFontSize: 24, wantH: 12, wantR: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := firstGlyph(t, c.content)
			if !approxEq(g.FontSize, c.wantFontSize) {
				t.Errorf("FontSize = %v, want ~%v", g.FontSize, c.wantFontSize)
			}
			if !approxEq(g.H, c.wantH) {
				t.Errorf("H = %v, want ~%v", g.H, c.wantH)
			}
			if g.H < 0 {
				t.Errorf("H = %v, must be non-negative", g.H)
			}
			if !approxEq(g.Rotation, c.wantR) {
				t.Errorf("Rotation = %v, want ~%v", g.Rotation, c.wantR)
			}
		})
	}
}
