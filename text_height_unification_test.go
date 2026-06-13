// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import "testing"

// wordsOf returns the words on page 1 of a PDF built from a single content stream.
func wordsOf(t *testing.T, contentStream string) []Word {
	t.Helper()
	r, err := OpenBytes(buildTextPDF(contentStream))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}
	return words
}

// TestWordHeightUnification locks WS3: Word.H is the up-vector nominal height
// (Text.H), NOT the matrix x-scale FontSize. Every case is a run where the two
// DIVERGE (a horizontal case where they coincide would be a paper gate), and the
// cases between them exercise all three height sites in wordsFromBand — the
// first-glyph seed, the word-split seed, and the bounding-box extension — so a
// revert of ANY single one fails the gate. Tfs is 12 throughout; /F1 is Helvetica
// with no /Widths, so every glyph has W=0.
func TestWordHeightUnification(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		wantWords int     // structural lock (coalesce vs gap-split)
		idx       int     // which Word to inspect
		wantH     float64 // unified Word.H (= Text.H)
		note      string  // pre-WS3 value + the edit it locks
	}{
		{
			name:      "horizontal-anchor",
			content:   "BT /F1 12 Tf 1 0 0 1 100 200 Tm (X) Tj ET",
			wantWords: 1, idx: 0, wantH: 12, note: "seed; FontSize=H=12 (sanity)",
		},
		{
			name:      "seed-tz-th2",
			content:   "BT /F1 12 Tf 200 Tz 1 0 0 1 100 100 Tm (X) Tj ET",
			wantWords: 1, idx: 0, wantH: 12, note: "first-glyph seed; old FontSize=24",
		},
		{
			name:      "seed-rotated-90",
			content:   "BT /F1 12 Tf 0 1 -1 0 72 400 Tm (X) Tj ET",
			wantWords: 1, idx: 0, wantH: 12, note: "first-glyph seed; old FontSize=0",
		},
		{
			name:      "seed-horizontal-flip",
			content:   "BT /F1 12 Tf -1 0 0 1 100 100 Tm (X) Tj ET",
			wantWords: 1, idx: 0, wantH: 12, note: "first-glyph seed; old FontSize=-12 (>=0 lock)",
		},
		{
			// Two words in one band (gap 100 >> threshold); the SECOND word is
			// Tz-scaled, so its seed comes from the word-split branch.
			name:      "gapsplit-seed-tz-th2",
			content:   "BT /F1 12 Tf 1 0 0 1 100 100 Tm (A) Tj 200 Tz 1 0 0 1 200 100 Tm (B) Tj ET",
			wantWords: 2, idx: 1, wantH: 12, note: "word-split seed; old FontSize=24",
		},
		{
			// Two glyphs coalesced into one word (same origin, gap 0); the SECOND
			// glyph is Tz-scaled and drives the bounding-box extension. Tz, not 90deg:
			// at 90deg FontSize=0 so the old extension is a no-op (would not discriminate).
			name:      "extension-tz-th2",
			content:   "BT /F1 12 Tf 1 0 0 1 100 100 Tm (A) Tj 200 Tz (B) Tj ET",
			wantWords: 1, idx: 0, wantH: 12, note: "bbox extension; old tTop=Y+24 -> H=24",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			words := wordsOf(t, c.content)
			if len(words) != c.wantWords {
				t.Fatalf("got %d words, want %d (%s)", len(words), c.wantWords, c.note)
			}
			w := words[c.idx]
			if !approxEq(w.H, c.wantH) {
				t.Errorf("Word.H = %v, want ~%v (%s)", w.H, c.wantH, c.note)
			}
			if w.H < 0 {
				t.Errorf("Word.H = %v, must be non-negative", w.H)
			}
		})
	}
}

// TestLineHeightUnification locks that Line.H inherits the unified Word.H: a 90deg
// rotated glyph yields Line.H == 12 (up-vector), not 0 (collapsed FontSize). This
// is the lineFromWords inheritance path (no independent FontSize height math).
func TestLineHeightUnification(t *testing.T) {
	r, err := OpenBytes(buildTextPDF("BT /F1 12 Tf 0 1 -1 0 72 400 Tm (X) Tj ET"))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	lines, err := r.Page(1).Lines()
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	if len(lines) == 0 {
		t.Fatalf("no lines")
	}
	if !approxEq(lines[0].H, 12) {
		t.Errorf("Line.H = %v, want ~12 (inherited up-vector height, not collapsed FontSize 0)", lines[0].H)
	}
	if lines[0].H < 0 {
		t.Errorf("Line.H = %v, must be non-negative", lines[0].H)
	}
}
