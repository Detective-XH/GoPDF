// extraction_correctness_test.go — regression suite for two extraction-correctness
// bugs in the Content()/Words() interpreter (content.go):
//
//   - The q/Q operators saved and restored the current font (g.Tf) but not the
//     text encoder, so a glyph shown after a Q decoded through the inner block's
//     encoder while still reporting the outer font. Fixed by moving enc into the
//     graphics state so q/Q save and restore it together with Tf.
//   - Form XObjects were interpreted with CTM = identity, discarding both the CTM
//     in effect at the Do operator and the form's /Matrix, so XObject text
//     coordinates came out form-local instead of page-space. Fixed by setting the
//     form sub-state CTM to formMatrix · parentCTM.
//
// Each fixture was verified to fail before the fix (post-Q S="€" not "Ä";
// XObject X/Y/FontSize = 0/0/12 not 110/220/24). buildPDFFromObjects is defined
// in page_test.go (same package).
package pdf

import (
	"fmt"
	"testing"
)

// TestEncoderRestoredAfterQ verifies that the q/Q graphics-state stack restores
// the text encoder together with the font. F1 (Helvetica/MacRoman) is selected,
// a q-block switches to F2 (Times-Roman/WinAnsi) and shows byte 0x80, then Q
// restores F1 and byte 0x80 is shown again. Byte 0x80 decodes to '€' under
// WinAnsi and to 'Ä' under MacRoman, so the post-Q glyph must come out as 'Ä'
// (F1's encoder), not '€' (the stale inner encoder).
//
// \\200 in Go source is the 4-char PDF octal escape for byte 0x80, exactly as
// buildCrossPageFontCachePDF writes it; /Length is computed from the same string.
func TestEncoderRestoredAfterQ(t *testing.T) {
	stream := "BT /F1 12 Tf q /F2 12 Tf (\\200) Tj Q (\\200) Tj ET"
	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page — two fonts: F1 (MacRoman) and F2 (WinAnsi)
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R /F2 6 0 R >> >> /Contents 4 0 R >>",
		// 4: Content stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		// 5: F1 — Helvetica with MacRomanEncoding (byte 0x80 -> 'Ä')
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /MacRomanEncoding >>",
		// 6: F2 — Times-Roman with WinAnsiEncoding (byte 0x80 -> '€')
		"<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman /Encoding /WinAnsiEncoding >>",
	})

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	texts := r.Page(1).Content().Text
	if len(texts) != 2 {
		t.Fatalf("Content().Text = %d entries, want 2: %+v", len(texts), texts)
	}

	// First glyph: shown inside the q-block under F2/WinAnsi -> '€'.
	if texts[0].S != "€" {
		t.Errorf("in-block glyph S = %q, want %q (WinAnsi 0x80)", texts[0].S, "€")
	}
	// Second glyph: shown after Q. Tf is restored to F1 (so Font is Helvetica),
	// and the encoder must follow it back to MacRoman -> 'Ä'. The Font check
	// proves Q restores Tf (it always did), isolating the bug to the encoder:
	// pre-fix this glyph reported Font=Helvetica but S="€".
	if texts[1].Font != "Helvetica" {
		t.Errorf("post-Q glyph Font = %q, want %q (Q restores Tf)", texts[1].Font, "Helvetica")
	}
	if texts[1].S != "Ä" {
		t.Errorf("post-Q glyph S = %q, want %q (MacRoman 0x80 — encoder must be restored by Q)", texts[1].S, "Ä")
	}
}

// TestXObjectPageSpaceCoords verifies that text inside a Form XObject is
// reported in page space, concatenating the form's /Matrix with the CTM in
// effect at the Do operator. The page translates the CTM by (100,200) then
// invokes a form whose /Matrix scales by 2 and translates by (10,20). A glyph
// at the form origin must land at:
//
//	origin (0,0) --/Matrix--> (10,20) --CTM(translate 100,200)--> (110,220)
//
// with font size 12 × 2 = 24. Pre-fix the sub-state used CTM = identity, so the
// glyph came out form-local at (0,0) with size 12. The scale factor of 2 also
// distinguishes the correct concatenation order (110,220) from a transposed one
// (210,420). All the matrix entries are integers, so the products are exact and
// an == comparison is safe (matching the existing position tests).
func TestXObjectPageSpaceCoords(t *testing.T) {
	xobjBody := "BT /F1 12 Tf (Z) Tj ET"
	xobj := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Form /Matrix [2 0 0 2 10 20] "+
			"/Resources << /Font << /F1 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(xobjBody), xobjBody)
	pageContent := "1 0 0 1 100 200 cm /Fm0 Do"
	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page — invokes Form XObject /Fm0
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Fm0 5 0 R >> >> /Contents 4 0 R >>",
		// 4: Page content — translate the CTM, then Do the form
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
		// 5: Form XObject with /Matrix and its own font resources
		xobj,
		// 6: Font for the form's text
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	texts := r.Page(1).Content().Text
	if len(texts) != 1 {
		t.Fatalf("Content().Text = %d entries, want 1: %+v", len(texts), texts)
	}
	got := texts[0]
	if got.S != "Z" {
		t.Errorf("glyph S = %q, want %q", got.S, "Z")
	}
	const wantX, wantY, wantSize = 110.0, 220.0, 24.0
	if got.X != wantX || got.Y != wantY {
		t.Errorf("XObject glyph position = (%g, %g), want (%g, %g) — page space, not form-local",
			got.X, got.Y, wantX, wantY)
	}
	if got.FontSize != wantSize {
		t.Errorf("XObject glyph FontSize = %g, want %g (12 × form /Matrix scale 2)", got.FontSize, wantSize)
	}
}

// TestXObjectMalformedMatrixIsIdentity verifies that a length-6 but non-numeric
// /Matrix is treated as the identity, not as a degenerate transform that
// collapses coordinates — and that the safe identity, not a corrupt CTM,
// propagates into nested forms. An outer form carries /Matrix [/Bad 0 0 1 10 20]
// (element 0 is a Name, not a number) and invokes an inner form that draws a
// glyph at the origin. Without the numeric guard, Float64() would read /Bad as 0,
// yielding the matrix [[0 0 0][0 1 0][10 20 1]]; concatenated into the inner
// form's CTM that places the glyph at (10,20) with FontSize 0 (the scale a=0
// collapses). With the guard the matrix is identity, so the glyph stays at the
// form origin (0,0) with FontSize 12.
func TestXObjectMalformedMatrixIsIdentity(t *testing.T) {
	outerBody := "/Inner Do"
	outer := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Form /Matrix [/Bad 0 0 1 10 20] "+
			"/Resources << /XObject << /Inner 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(outerBody), outerBody)
	innerBody := "BT /F1 12 Tf (Z) Tj ET"
	inner := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Form /Resources << /Font << /F1 7 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(innerBody), innerBody)
	pageContent := "/Outer Do"
	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page — invokes the outer Form XObject
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Outer 4 0 R >> >> /Contents 5 0 R >>",
		// 4: Outer form — malformed /Matrix, references /Inner
		outer,
		// 5: Page content stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
		// 6: Inner form — draws the glyph at the origin
		inner,
		// 7: Font for the inner text
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	texts := r.Page(1).Content().Text
	if len(texts) != 1 {
		t.Fatalf("Content().Text = %d entries, want 1: %+v", len(texts), texts)
	}
	got := texts[0]
	if got.X != 0 || got.Y != 0 {
		t.Errorf("malformed /Matrix glyph position = (%g, %g), want (0, 0) — malformed matrix must degrade to identity",
			got.X, got.Y)
	}
	if got.FontSize != 12 {
		t.Errorf("malformed /Matrix glyph FontSize = %g, want 12 — a degenerate matrix would collapse it to 0", got.FontSize)
	}
}
