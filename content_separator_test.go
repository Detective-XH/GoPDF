// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"fmt"
	"testing"
)

// sepBuildTJPDF builds a one-page PDF whose content stream shows the given TJ
// array (e.g. "[(AB)]") under a simple Type1 font carrying the given ToUnicode
// CMap body. The interpreter synthesises a "\n" separator after every TJ array
// and a " " separator wherever a kerning adjustment crosses the word-gap
// threshold; this fixture lets a test assert those interpreter-injected
// separators decode correctly even when the font's ToUnicode has no entry for the
// 0x0A/0x20 separator byte.
func sepBuildTJPDF(tjArray, cmapBody string) []byte {
	content := "BT /F1 12 Tf " + tjArray + " TJ ET"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /FirstChar 65 /LastChar 66" +
			" /Widths [500 500] /ToUnicode 6 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cmapBody), cmapBody),
	})
}

func sepContentRunes(t *testing.T, data []byte) []string {
	t.Helper()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	var got []string
	for _, tx := range r.Page(1).Content().Text {
		got = append(got, tx.S)
	}
	return got
}

// TestTJSeparatorNotReplacementCharUnderSimpleFont locks the rule that the
// separators a TJ array synthesises — "\n" after the array, " " across a word-gap
// kerning adjustment — stay literal even when the content font is a simple font
// whose ToUnicode CMap has no entry for the 0x0A/0x20 separator byte.
//
// The separator is interpreter-chosen ASCII, not a font code byte, but it is
// still routed through the content font's encoder (so the -V CMaps keep their
// real-rune advance handling). A simpleCmapEncoder (simple font + ToUnicode) has
// no bfchar/bfrange entry for 0x0A/0x20, so it decoded each separator to U+FFFD —
// leaking a replacement glyph into Content().Text/Words for every TJ array (the
// trailing U+FFFD run observed on the IRS SOI Adobe-subset-font table). The fix
// falls back to the literal sep on an unmappable decode, so the exact-rune
// expectations below carry no U+FFFD.
func TestTJSeparatorNotReplacementCharUnderSimpleFont(t *testing.T) {
	// ToUnicode maps only the printed codes 0x41→'A', 0x42→'B' — nothing at the
	// 0x0A newline or 0x20 space the interpreter injects between/after TJ arrays.
	cmapAB := standardCmapHeader +
		"1 begincodespacerange\n<41> <42>\nendcodespacerange\n" +
		"2 beginbfchar\n<41> <0041>\n<42> <0042>\nendbfchar\n" +
		standardCmapFooter

	cases := []struct {
		name    string
		tjArray string
		want    []string
	}{
		// The "\n" appended after the array stays literal, not U+FFFD.
		{"trailing-newline", "[(AB)]", []string{"A", "B", "\n"}},
		// A kerning gap past the word threshold injects a " " separator; it and
		// the trailing "\n" both stay literal.
		{"word-gap-space", "[(A) -200 (B)]", []string{"A", " ", "B", "\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sepContentRunes(t, sepBuildTJPDF(tc.tjArray, cmapAB))
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("Content().Text runes = %q, want %q", got, tc.want)
			}
		})
	}
}
