package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// TestPlainTextQQEncoderRestore verifies that the q/Q graphics-state save/restore
// correctly preserves the active text encoder. Before the fix, a Tf inside a q…Q
// block bled the inner encoder past the closing Q, so text shown afterwards (with
// no fresh Tf, relying on the restored outer font) was decoded through the wrong
// encoder → U+FFFD garble.
//
// Synthetic PDF layout:
//
//	F1 (outer font): ToUnicode maps <42>→'B'. Byte 0x42 decodes correctly to 'B'.
//	F0 (inner font): ToUnicode maps <00>..<FF> codespace but only <41>→'A'; byte
//	                 0x42 is in-codespace but unmapped → U+FFFD.
//
// Content stream:
//
//	BT /F1 12 Tf (\x42) Tj ET          ← outer: should show 'B'
//	q
//	BT /F0 12 Tf (\x41) Tj ET          ← inside q…Q: shows 'A' via F0
//	Q
//	BT (\x42) Tj ET                     ← restored outer F1: should show 'B' again
//	                                      without fix: still F0 → U+FFFD
func TestPlainTextQQEncoderRestore(t *testing.T) {
	// F1 ToUnicode: maps ONLY byte 0x42 → 'B'.
	f1CMap := standardCmapHeader +
		"1 begincodespacerange\n<42> <42>\nendcodespacerange\n" +
		"1 beginbfchar\n<42> <0042>\nendbfchar\n" +
		standardCmapFooter

	// F0 ToUnicode: full codespace <00>..<FF> but only <41>→'A'; <42> → U+FFFD.
	// (Re-uses the same constant as buildUnmappedGlyphPDF from corpus_gen_test.go.)
	f0CMap := unmappedToUnicodeCMap

	content := "BT /F1 12 Tf (\x42) Tj ET\n" +
		"q\n" +
		"BT /F0 12 Tf (\x41) Tj ET\n" +
		"Q\n" +
		"BT (\x42) Tj ET"

	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page — both fonts in Resources
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /Font << /F1 5 0 R /F0 7 0 R >> >> >>",
		// 4: Content stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		// 5: Font F1 (outer font — maps 0x42→'B')
		"<< /Type /Font /Subtype /Type1 /BaseFont /SynOuter /ToUnicode 6 0 R >>",
		// 6: F1's ToUnicode CMap stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(f1CMap), f1CMap),
		// 7: Font F0 (inner font — maps <00>..<FF> codespace, only 0x41→'A'; 0x42→FFFD)
		"<< /Type /Font /Subtype /Type1 /BaseFont /SynInner /ToUnicode 8 0 R >>",
		// 8: F0's ToUnicode CMap stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(f0CMap), f0CMap),
	})

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}

	// The output must contain 'B' twice (outer font before and after Q) and no U+FFFD.
	// Without the q/Q fix the third show-string (\x42 after Q) used the inner F0
	// encoder, decoding 0x42 as U+FFFD instead of 'B'.
	if strings.Contains(got, "�") {
		t.Errorf("GetPlainText = %q; contains U+FFFD — q/Q encoder-bleed bug not fixed", got)
	}
	if strings.Count(got, "B") < 2 {
		t.Errorf("GetPlainText = %q; want at least 2 'B' runes (before and after Q)", got)
	}
}
