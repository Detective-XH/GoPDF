package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// buildSimpleLegacyPDF assembles a minimal 1-page PDF whose only font is a SIMPLE Type1 font with the
// given BaseFont. When toUnicode is non-empty the font carries a /ToUnicode CMap (so its decode path is
// encSourceToUnicode, i.e. a re-encoded/subsetted font); otherwise it has no /Encoding (encSourceSimple,
// i.e. raw codes == canonical legacy codes). content is the page content stream.
func buildSimpleLegacyPDF(baseFont, content, toUnicode string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content)+1, content),
	}
	if toUnicode == "" {
		objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /"+baseFont+" >>")
	} else {
		objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /"+baseFont+" /ToUnicode 6 0 R >>")
		objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(toUnicode)+1, toUnicode))
	}

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xrefOff := b.Len()
	n := len(objs) + 1
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", n)
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", n, xrefOff)
	return []byte(b.String())
}

func plainTextOf(t *testing.T, pdf []byte) string {
	t.Helper()
	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	s, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	return s
}

// TestLegacyDevanagariRemapCanonical: a canonical-coded (encSourceSimple, no /ToUnicode) Kruti Dev 010
// font's keyboard codes ",u-;w-,y-,e-" are recovered as real Unicode एन.यू.एल.एम. (the abbreviation
// N.U.L.M.). Exercises the table + the whole content/plaintext pipeline end to end.
func TestLegacyDevanagariRemapCanonical(t *testing.T) {
	pdf := buildSimpleLegacyPDF("KrutiDev010", "BT /F1 12 Tf 72 700 Td (,u-;w-,y-,e-) Tj ET", "")
	got := plainTextOf(t, pdf)
	if !strings.Contains(got, "एन.यू.एल.एम.") {
		t.Errorf("canonical Kruti remap: got %q, want it to contain %q", got, "एन.यू.एल.एम.")
	}
}

// TestLegacyDevanagariRemapDeclinesSubsetted: the SAME Kruti-named simple font but carrying a /ToUnicode
// (a re-encoded/subsetted font, decode path encSourceToUnicode) must NOT be remapped — applying the
// canonical code table to remapped codes would corrupt. The canonical-coded gate declines it, so the
// byte decodes through its own ToUnicode (here 0x2C->'r'), never through the legacy table.
func TestLegacyDevanagariRemapDeclinesSubsetted(t *testing.T) {
	cmap := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange <00> <FF> endcodespacerange\n" +
		"1 beginbfchar <2C> <0072> endbfchar\n" +
		"endcmap CMapName currentdict /CMap defineresource pop end end"
	pdf := buildSimpleLegacyPDF("KrutiDev010", "BT /F1 12 Tf 72 700 Td (,) Tj ET", cmap)
	got := plainTextOf(t, pdf)
	if strings.Contains(got, "ए") {
		t.Errorf("subsetted (ToUnicode) Kruti font was WRONGLY remapped to Devanagari: got %q", got)
	}
	if !strings.Contains(got, "r") {
		t.Errorf("subsetted font should decode its byte through its own ToUnicode (0x2C->'r'): got %q", got)
	}
}

// TestLegacyDevanagariRemapDeclinesNonStrict: a non-legacy font name is never remapped.
func TestLegacyDevanagariRemapDeclinesNonStrict(t *testing.T) {
	pdf := buildSimpleLegacyPDF("Helvetica", "BT /F1 12 Tf 72 700 Td (,u-;w-,y-,e-) Tj ET", "")
	got := plainTextOf(t, pdf)
	if strings.ContainsAny(got, "एनयूलम") {
		t.Errorf("non-legacy font was wrongly remapped: got %q", got)
	}
}

// lastGlyphX extracts the X of the last decoded glyph from a single-line legacy PDF.
func lastGlyphX(t *testing.T, pdf []byte) float64 {
	t.Helper()
	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	c := r.Page(1).Content()
	if len(c.Text) == 0 {
		t.Fatalf("no glyphs")
	}
	return c.Text[len(c.Text)-1].X
}

// TestLegacyDevanagariRemapTJKerningPreserved locks the geometry fix for the legacy run-buffering. A
// sub-threshold TJ kerning between elements is buffered for decode (so a fragmented matra composes), but
// its pen advance is NOT discarded — it is carried in the run's budget and applied to the text matrix at
// flush, so the CUMULATIVE advance (and every following glyph/word/cell) matches the base interpreter. A
// regression would drop it, leaving downstream text shifted. Here a -100 kerning inside the first run
// (before a word-gap flush) must push the post-gap glyph forward by 100/1000*fontsize vs the un-kerned
// control. 'd'=क(100), 'u'=न(117), 'l'=स(108); -300 is the (flushing) word gap.
func TestLegacyDevanagariRemapTJKerningPreserved(t *testing.T) {
	noKern := buildSimpleLegacyPDF("KrutiDev010", "BT /F1 12 Tf 72 700 Td [(d) (u) -300 (l)] TJ ET", "")
	kerned := buildSimpleLegacyPDF("KrutiDev010", "BT /F1 12 Tf 72 700 Td [(d) -100 (u) -300 (l)] TJ ET", "")
	shift := lastGlyphX(t, kerned) - lastGlyphX(t, noKern)
	const want = 100.0 / 1000 * 12 // 1.2 pt
	if shift < want-0.05 || shift > want+0.05 {
		t.Errorf("sub-threshold TJ kerning not carried into the run advance: post-gap glyph shifted %.3f pt, want ~%.3f", shift, want)
	}
}

// TestLegacyDevanagariRemapTJLeadingKern locks that a LEADING sub-threshold adjustment (no run in
// progress) is applied BEFORE the next glyph, matching the base interpreter — not deferred to flush.
// `[-100 (d)]` must place क 1.2pt right of `[(d)]`.
func TestLegacyDevanagariRemapTJLeadingKern(t *testing.T) {
	plain := buildSimpleLegacyPDF("KrutiDev010", "BT /F1 12 Tf 72 700 Td [(d)] TJ ET", "")
	leading := buildSimpleLegacyPDF("KrutiDev010", "BT /F1 12 Tf 72 700 Td [-100 (d)] TJ ET", "")
	shift := lastGlyphX(t, leading) - lastGlyphX(t, plain)
	const want = 100.0 / 1000 * 12 // 1.2 pt
	if shift < want-0.05 || shift > want+0.05 {
		t.Errorf("leading sub-threshold TJ adjustment not applied before the glyph: क shifted %.3f pt, want ~%.3f", shift, want)
	}
}

// TestLegacyDevanagariRemapCrossTJ locks the cross-TJ-element matra composition. Real Kruti PDFs
// fragment one syllable across consecutive TJ array string elements: the o-matra ो is the font glyphs
// ा + े, emitted as two elements split by a small kerning. The bytes 'd'=क(100), 'k'=ा/VERTBAR(107),
// 's'=े(115) compose ो only when the run is accumulated and decoded as a unit (interpretTJArrayLegacy /
// showArrayLegacy). Without the run-buffering the elements decode separately to the split form काे.
func TestLegacyDevanagariRemapCrossTJ(t *testing.T) {
	pdf := buildSimpleLegacyPDF("KrutiDev010", "BT /F1 12 Tf 72 700 Td [(dk) -20 (s)] TJ ET", "")
	got := plainTextOf(t, pdf)
	if !strings.Contains(got, "को") {
		t.Errorf("cross-TJ matra not composed: got %q, want it to contain को", got)
	}
	if strings.Contains(got, "काे") {
		t.Errorf("cross-TJ matra left in the split form काे: %q", got)
	}
}
