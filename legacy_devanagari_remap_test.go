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
