// layout_words_test.go — tests for Page.Words() word-boundary extraction.
package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// buildWordsPDF creates a minimal one-page PDF with a monospace font (/F1,
// 600-unit glyph width for every character code 0–255, similar to Courier).
// Use "/F1 <size> Tf" in the content stream to activate it. With size=12:
// each glyph has W = 600/1000 * 12 = 7.2 pt and the text matrix advances
// 7.2 pt after every character.
func buildWordsPDF(streamBody string) []byte {
	// Build Widths array: 256 entries of 600 (monospace).
	var wb strings.Builder
	for i := 0; i < 256; i++ {
		if i > 0 {
			wb.WriteByte(' ')
		}
		wb.WriteString("600")
	}
	fontBody := fmt.Sprintf(
		`<< /Type /Font /Subtype /Type1 /BaseFont /TestMono `+
			`/FirstChar 0 /LastChar 255 /Widths [%s] >>`,
		wb.String(),
	)
	return buildOneFontPDF(streamBody, fontBody)
}

// buildCJKWordsPDF creates a minimal one-page PDF whose /F1 font uses
// /Encoding /UniGB-UCS2-H, triggering ucs2BEEncoder for the content stream.
// The Tj string must contain raw UCS-2 BE bytes (use PDF octal escapes).
// No /Widths are declared: glyph advances are 0, so all chars land at the same
// X position and merge into one Word per contiguous no-whitespace sequence.
func buildCJKWordsPDF(streamBody string) []byte {
	fontBody := `<< /Type /Font /Subtype /Type1 /Encoding /UniGB-UCS2-H >>`
	return buildOneFontPDF(streamBody, fontBody)
}

// buildOneFontPDF assembles a 5-object PDF: catalog, pages tree, page dict,
// content stream, and one font dict declared as /F1. It is the shared template
// for buildWordsPDF and buildCJKWordsPDF.
func buildOneFontPDF(streamBody, fontBody string) []byte {
	streamLen := len(streamBody)
	objs := []string{
		`<< /Type /Catalog /Pages 2 0 R >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] ` +
			`/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>`,
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", streamLen, streamBody),
		fontBody,
	}

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	off := make([]int, len(objs)+1)
	for i, body := range objs {
		off[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefOff := b.Len()
	n := len(objs) + 1
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", n)
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", off[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		n, xrefOff)
	return []byte(b.String())
}

// TestWordsBasic verifies that a single-line "Hello World" string produces two
// words with correct text, non-zero bounding boxes, and reading-order positions.
func TestWordsBasic(t *testing.T) {
	// "Hello World" with monospace 12pt font.
	// Each char: W = 600/1000 * 12 = 7.2 pt; advance = 7.2 pt per char.
	// "Hello" → 5 chars → word W = 36 pt
	// " "     → whitespace → word boundary
	// "World" → 5 chars → starts at X = 72 + 6*7.2 = 115.2 (after space advance)
	stream := "BT\n/F1 12 Tf\n72 720 Td\n(Hello World) Tj\nET"
	data := buildWordsPDF(stream)

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words(): %v", err)
	}
	if len(words) != 2 {
		t.Fatalf("Words() returned %d words, want 2; words=%v", len(words), words)
	}
	if words[0].S != "Hello" {
		t.Errorf("words[0].S = %q, want \"Hello\"", words[0].S)
	}
	if words[1].S != "World" {
		t.Errorf("words[1].S = %q, want \"World\"", words[1].S)
	}
	for i, w := range words {
		if w.W <= 0 {
			t.Errorf("words[%d].W = %v, want > 0", i, w.W)
		}
		if w.H <= 0 {
			t.Errorf("words[%d].H = %v, want > 0", i, w.H)
		}
	}
	// Reading order: Hello before World in X.
	if words[0].X >= words[1].X {
		t.Errorf("words[0].X (%v) >= words[1].X (%v): not in reading order",
			words[0].X, words[1].X)
	}
}

// TestWordsMultiStyle verifies that a word split across two Tf font-size changes
// is merged correctly by the gap-based algorithm, and that the following word is
// also correctly identified.
func TestWordsMultiStyle(t *testing.T) {
	// "Hel" at 12pt then "lo" at 10pt (style change mid-word, no space).
	// After "Hel" Tj: cursor X = 72 + 3*7.2 = 93.6.
	// Tf 10pt (W per char = 6.0 pt): cursor stays at 93.6 (Tf does not move Tm).
	// gap between l(12pt, end=93.6) and l(10pt, X=93.6) = 0 ≤ threshold → merged.
	// " World" at 10pt: space → word boundary; "World" at X=105.6+6.0=111.6.
	stream := "BT\n/F1 12 Tf\n72 720 Td\n(Hel) Tj\n/F1 10 Tf\n(lo World) Tj\nET"
	data := buildWordsPDF(stream)

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words(): %v", err)
	}
	if len(words) != 2 {
		t.Fatalf("Words() returned %d words, want 2; words=%v", len(words), words)
	}
	if words[0].S != "Hello" {
		t.Errorf("words[0].S = %q, want \"Hello\" (spans 12pt+10pt style run)", words[0].S)
	}
	if words[1].S != "World" {
		t.Errorf("words[1].S = %q, want \"World\"", words[1].S)
	}
	// "Hello" spans two font sizes; H must reflect the maximum (12 pt).
	if words[0].H != 12 {
		t.Errorf("words[0].H = %v, want 12 (max of 12pt and 10pt)", words[0].H)
	}
}

// TestWordsCJK exercises the ucs2BEEncoder code path through Words() using a font
// whose /Encoding is /UniGB-UCS2-H. The Tj string encodes 繁體中文 as four UCS-2 BE
// pairs. Without Unicode whitespace, all four characters merge into one Word.
func TestWordsCJK(t *testing.T) {
	// UCS-2 BE pairs for 繁體中文:
	//   繁 U+7E41 → \176\101   體 U+9AD4 → \232\324
	//   中 U+4E2D → \116\055   文 U+6587 → \145\207
	// The font has no /Widths, so W=0 and all glyphs land at X=72 (tx=0).
	// gap=0 ≤ threshold=0 → all four chars merge into one Word "繁體中文".
	stream := "BT\n/F1 12 Tf\n72 720 Td\n(\\176\\101\\232\\324\\116\\055\\145\\207) Tj\nET"
	data := buildCJKWordsPDF(stream)

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words(): %v", err)
	}
	if len(words) == 0 {
		t.Fatal("Words() returned no words for CJK text")
	}
	var all strings.Builder
	for _, w := range words {
		all.WriteString(w.S)
	}
	if all.String() != "繁體中文" {
		t.Errorf("Words() concatenated text = %q, want \"繁體中文\"", all.String())
	}
}

// TestWordsMultiLine verifies that Words() produces separate words across two
// Y-bands and returns them in top-to-bottom, left-to-right reading order.
// This exercises the y-band flush path (band[0].Y - t.Y > tol → flush).
func TestWordsMultiLine(t *testing.T) {
	// "Hello" at Y=720, "World" at Y=700 (0 -20 Td).
	// tol = 12*0.5 = 6; ΔY = 20 > 6 → two separate bands.
	// Expected: words[0]="Hello" (Y=720), words[1]="World" (Y=700).
	stream := "BT\n/F1 12 Tf\n72 720 Td\n(Hello) Tj\n0 -20 Td\n(World) Tj\nET"
	data := buildWordsPDF(stream)

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words(): %v", err)
	}
	if len(words) != 2 {
		t.Fatalf("Words() returned %d words, want 2; words=%v", len(words), words)
	}
	if words[0].S != "Hello" {
		t.Errorf("words[0].S = %q, want \"Hello\"", words[0].S)
	}
	if words[1].S != "World" {
		t.Errorf("words[1].S = %q, want \"World\"", words[1].S)
	}
	// Top-to-bottom order: Hello (Y=720) before World (Y=700).
	if words[0].Y <= words[1].Y {
		t.Errorf("words[0].Y (%v) <= words[1].Y (%v): not in top-to-bottom order",
			words[0].Y, words[1].Y)
	}
}

// TestWordsEmpty verifies that a page with no content stream returns (nil, nil).
func TestWordsEmpty(t *testing.T) {
	data := buildSinglePagePDF("")

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words() unexpected error: %v", err)
	}
	if words != nil {
		t.Errorf("Words() = %v, want nil for page with no content", words)
	}
}

// TestWordsBaselineShift verifies that a subscript glyph whose baseline is
// lowered (via the Ts text-rise operator) but stays within the y-band tolerance
// does not get reordered after its same-Y neighbours. "H", subscript "2"
// (Ts -4), then "O" share one band; the global (Y desc, X asc) sort would order
// them H, O, 2, but flush re-sorts the band by X so wordsFromBand sees H, 2, O
// and merges them into a single reading-order word "H2O".
func TestWordsBaselineShift(t *testing.T) {
	// H at (72,720); 2 at (79.2,716) via Ts -4; O at (86.4,720); 12pt monospace.
	stream := "BT\n/F1 12 Tf\n72 720 Td\n(H) Tj\n-4 Ts\n(2) Tj\n0 Ts\n(O) Tj\nET"
	data := buildWordsPDF(stream)

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words(): %v", err)
	}
	if len(words) != 1 {
		t.Fatalf("Words() returned %d words, want 1; words=%v", len(words), words)
	}
	if words[0].S != "H2O" {
		t.Errorf("words[0].S = %q, want \"H2O\" (subscript must not be reordered)", words[0].S)
	}
}

// buildTJWordsPDF is like buildWordsPDF but its monospace font declares
// /FirstChar 32 /LastChar 126 (printable ASCII only), so the control byte 0x0A
// has zero width — matching real fonts, which never glyph the synthetic "\n"
// that content.go appends after every TJ operator. This isolates word-boundary
// logic from the phantom-newline advance that buildWordsPDF's FirstChar-0 font
// would otherwise introduce.
func buildTJWordsPDF(streamBody string) []byte {
	var wb strings.Builder
	for i := 0; i < 95; i++ { // codes 32..126 inclusive
		if i > 0 {
			wb.WriteByte(' ')
		}
		wb.WriteString("600")
	}
	fontBody := fmt.Sprintf(
		`<< /Type /Font /Subtype /Type1 /BaseFont /TestMono `+
			`/FirstChar 32 /LastChar 126 /Widths [%s] >>`,
		wb.String(),
	)
	return buildOneFontPDF(streamBody, fontBody)
}

// TestWordsTJContinuation verifies that a single visual word split across a TJ
// operator and a following Tj is returned as one word. content.go appends a
// synthetic "\n" after every TJ; Words() must not treat that terminator as a
// word boundary. "[(Hel)] TJ (lo) Tj" must yield "Hello", not "Hel"+"lo".
func TestWordsTJContinuation(t *testing.T) {
	stream := "BT\n/F1 12 Tf\n72 720 Td\n[(Hel)] TJ\n(lo) Tj\nET"
	data := buildTJWordsPDF(stream)

	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	words, err := r.Page(1).Words()
	if err != nil {
		t.Fatalf("Words(): %v", err)
	}
	if len(words) != 1 {
		t.Fatalf("Words() returned %d words, want 1; words=%v", len(words), words)
	}
	if words[0].S != "Hello" {
		t.Errorf("words[0].S = %q, want \"Hello\" (synthetic TJ newline must not split the word)", words[0].S)
	}
}
