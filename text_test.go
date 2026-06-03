// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"testing"
)

// textMakeSurrogate encodes a Unicode code point above U+FFFF as a pair of
// UTF-16 big-endian surrogate units and returns the raw 4-byte string.
func textMakeSurrogate(r rune) string {
	r -= 0x10000
	hi := uint16(0xD800 + (r>>10)&0x3FF)
	lo := uint16(0xDC00 + r&0x3FF)
	return string([]byte{
		byte(hi >> 8), byte(hi),
		byte(lo >> 8), byte(lo),
	})
}

// TestUTF16Decode verifies that utf16Decode correctly decodes a surrogate pair
// (a code point above U+FFFF, e.g. U+1F600 GRINNING FACE) into UTF-8.
func TestUTF16Decode(t *testing.T) {
	// U+1F600 GRINNING FACE: surrogate pair is 0xD83D 0xDE00
	textInput := textMakeSurrogate(0x1F600)
	got := utf16Decode(textInput)
	want := "\U0001F600"
	if got != want {
		t.Errorf("utf16Decode(%q) = %q; want %q", textInput, got, want)
	}
}

// textPDFDocByte returns the expected rune for a pdfDocEncoding byte,
// looked up directly from the package-level table.
func textPDFDocByte(b byte) rune {
	return pdfDocEncoding[b]
}

// TestPDFDocDecodeHighBytes verifies that pdfDocDecode translates bytes in the
// 0x80–0xFF range to the spec-mandated Unicode code points rather than treating
// them as Latin-1. We test a representative selection of bytes whose pdfDocEncoding
// mapping differs from their Latin-1 value.
func TestPDFDocDecodeHighBytes(t *testing.T) {
	cases := []struct {
		b    byte
		want rune
	}{
		// 0x80 → U+2022 BULLET (Latin-1 would be 0x0080 control)
		{0x80, 0x2022},
		// 0x81 → U+2020 DAGGER
		{0x81, 0x2020},
		// 0x82 → U+2021 DOUBLE DAGGER
		{0x82, 0x2021},
		// 0x83 → U+2026 HORIZONTAL ELLIPSIS
		{0x83, 0x2026},
		// 0x84 → U+2014 EM DASH
		{0x84, 0x2014},
		// 0x85 → U+2013 EN DASH
		{0x85, 0x2013},
		// 0x92 → U+2122 TRADE MARK SIGN
		{0x92, 0x2122},
		// 0xa0 → U+20AC EURO SIGN (Latin-1 would be U+00A0 NBSP)
		{0xa0, 0x20ac},
	}

	for _, tc := range cases {
		// Sanity-check the table matches our expectation.
		tableRune := textPDFDocByte(tc.b)
		if tableRune != tc.want {
			t.Errorf("pdfDocEncoding[0x%02x] = U+%04X; want U+%04X (spec)", tc.b, tableRune, tc.want)
		}

		// Now exercise pdfDocDecode end-to-end.
		input := string([]byte{tc.b})
		got := pdfDocDecode(input)
		wantStr := string(tc.want)
		if got != wantStr {
			t.Errorf("pdfDocDecode(%q) = %q; want %q (U+%04X)", input, got, wantStr, tc.want)
		}
	}
}

// textMakeText is a convenience constructor for Text values used in
// IsSameSentence tests.
func textMakeText(font string, fontSize, y float64, s string) Text {
	return Text{
		Font:     font,
		FontSize: fontSize,
		Y:        y,
		S:        s,
	}
}

// TestIsSameSentenceFalse checks that IsSameSentence returns false when:
//  1. The font name differs between segments.
//  2. The vertical (Y) delta is >= 5 points.
func TestIsSameSentenceFalse(t *testing.T) {
	t.Run("DifferentFont", func(t *testing.T) {
		last := textMakeText("Helvetica", 12, 100, "Hello ")
		current := textMakeText("Times-Roman", 12, 100, "world")
		if IsSameSentence(last, current) {
			t.Error("IsSameSentence returned true for different fonts; want false")
		}
	})

	t.Run("YDeltaAtBoundary", func(t *testing.T) {
		// Exactly 5.0 — NOT less than 5, so must be false.
		last := textMakeText("Helvetica", 12, 100, "Hello ")
		current := textMakeText("Helvetica", 12, 95, "world")
		if IsSameSentence(last, current) {
			t.Error("IsSameSentence returned true for Y-delta == 5; want false")
		}
	})

	t.Run("YDeltaAboveBoundary", func(t *testing.T) {
		// Y delta of 10 — well above threshold.
		last := textMakeText("Helvetica", 12, 200, "First line ")
		current := textMakeText("Helvetica", 12, 190, "Second line")
		if IsSameSentence(last, current) {
			t.Error("IsSameSentence returned true for Y-delta == 10; want false")
		}
	})

	t.Run("EmptyLastString", func(t *testing.T) {
		// last.S == "" — the condition requires last.S != "".
		last := textMakeText("Helvetica", 12, 100, "")
		current := textMakeText("Helvetica", 12, 100, "world")
		if IsSameSentence(last, current) {
			t.Error("IsSameSentence returned true for empty last.S; want false")
		}
	})
}
