package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// TestPlainTextSeparatorNoRuneGuard verifies that the writeSeparator guard
// prevents a T* operator from emitting U+FFFD when the active font uses a
// 2-byte-codespace CMap (a Type0 composite font). Without the guard,
// cmap.Decode("\n") fails — the 1-byte separator is not in any 2-byte
// codespace entry — and returns noRune (U+FFFD), corrupting the output.
//
// Synthetic PDF layout:
//
//	Font F1: ToUnicode with a 2-byte codespace <0000>..<FFFF>
//	         One bfchar maps <0003> → 'C' (so the encoder is a real cmap,
//	         not a simpleCmapEncoder).
//	Content stream:
//	  BT /F1 12 Tf <0003> Tj T* <0003> Tj ET
//
// Expected output: contains "\n" (from T*), contains "CC", NO U+FFFD.
func TestPlainTextSeparatorNoRuneGuard(t *testing.T) {
	// 2-byte codespace: <0000>..<FFFF>
	// One bfchar: <0003> → U+0043 ('C').
	// The 1-byte "\n" (0x0A) is NOT in this 2-byte codespace, so cmap.Decode
	// returns noRune for it — exactly the failure mode we are guarding.
	cmap2Byte := standardCmapHeader +
		"1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n" +
		"1 beginbfchar\n<0003> <0043>\nendbfchar\n" +
		standardCmapFooter

	// Content: show 'C' (<0003>), T* line-move, show 'C' again. Hex show
	// operands keep the byte codes unambiguous in the content stream.
	content := "BT /F1 12 Tf <0003> Tj T* <0003> Tj ET"

	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page with one composite (Type0) font
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /Font << /F1 5 0 R >> >> >>",
		// 4: Content stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		// 5: Font F1 — Type0 (composite) font with a 2-byte-codespace ToUnicode
		"<< /Type /Font /Subtype /Type0 /BaseFont /SynComposite" +
			" /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 7 0 R >>",
		// 6: CIDFont (required descendant for Type0)
		"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /SynComposite" +
			" /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >>" +
			" /DW 1000 >>",
		// 7: ToUnicode CMap with 2-byte codespace — the trigger for noRune on "\n"
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cmap2Byte), cmap2Byte),
	})

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}

	// Must contain a newline from T* (not U+FFFD).
	if !strings.Contains(got, "\n") {
		t.Errorf("GetPlainText = %q; T* separator missing — want a literal newline", got)
	}
	// Must contain no U+FFFD replacement characters.
	if strings.Contains(got, "�") {
		t.Errorf("GetPlainText = %q; contains U+FFFD — T* separator decoded via 2-byte CMap bug not fixed", got)
	}
	// Both shows of <0003> should decode to 'C'.
	if strings.Count(got, "C") < 2 {
		t.Errorf("GetPlainText = %q; want at least 2 'C' runes from <0003> bfchar mapping", got)
	}
}
