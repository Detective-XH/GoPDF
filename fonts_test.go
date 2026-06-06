// Tests for fonts.go: document-level font inventory.

package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// testFontSpec describes a font resource to embed in a test PDF.
type testFontSpec struct {
	name     string // /BaseFont value (no slash)
	subtype  string // /Subtype value (no slash): Type1, TrueType, etc.
	embedded bool   // whether to include a /FontDescriptor with a /FontFile stream
}

// buildFontPDF builds a minimal single-page PDF with the given font resources.
// Object numbers are pre-computed so that forward references (e.g. /Parent) are
// correct without a two-pass writer.
func buildFontPDF(specs []testFontSpec) []byte {
	// Phase 1: pre-compute object numbers.
	type fontNums struct {
		fontNum int
		descNum int // 0 if not embedded
		fileNum int // 0 if not embedded
	}
	nextObj := 1
	fnums := make([]fontNums, len(specs))
	for i, spec := range specs {
		fn := fontNums{}
		if spec.embedded {
			fn.fileNum = nextObj
			nextObj++
			fn.descNum = nextObj
			nextObj++
		}
		fn.fontNum = nextObj
		nextObj++
		fnums[i] = fn
	}
	pageNum := nextObj
	nextObj++
	pagesNum := nextObj
	nextObj++
	catalogNum := nextObj
	nextObj++
	total := nextObj // one past last object number

	// Phase 2: build object bodies using the pre-computed numbers.
	bodies := make([]string, total) // 1-indexed; bodies[0] unused
	for i, spec := range specs {
		fn := fnums[i]
		if spec.embedded {
			const fakeFont = "fake"
			bodies[fn.fileNum] = fmt.Sprintf(
				"<< /Length %d >>\nstream\n%s\nendstream",
				len(fakeFont), fakeFont,
			)
			bodies[fn.descNum] = fmt.Sprintf(
				"<< /Type /FontDescriptor /FontName /%s /FontFile %d 0 R >>",
				spec.name, fn.fileNum,
			)
			bodies[fn.fontNum] = fmt.Sprintf(
				"<< /Type /Font /Subtype /%s /BaseFont /%s /FontDescriptor %d 0 R >>",
				spec.subtype, spec.name, fn.descNum,
			)
		} else {
			bodies[fn.fontNum] = fmt.Sprintf(
				"<< /Type /Font /Subtype /%s /BaseFont /%s >>",
				spec.subtype, spec.name,
			)
		}
	}

	var fontDict strings.Builder
	for i, fn := range fnums {
		fmt.Fprintf(&fontDict, " /F%d %d 0 R", i+1, fn.fontNum)
	}
	if len(specs) == 0 {
		bodies[pageNum] = fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] >>",
			pagesNum,
		)
	} else {
		bodies[pageNum] = fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font <<%s>> >> >>",
			pagesNum, fontDict.String(),
		)
	}
	bodies[pagesNum] = fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", pageNum)
	bodies[catalogNum] = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesNum)

	// Phase 3: write objects, track byte offsets, emit xref + trailer.
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, total)
	for i := 1; i < total; i++ {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i, bodies[i])
	}
	xrefOff := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", total)
	for i := 1; i < total; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		total, catalogNum, xrefOff)
	return []byte(b.String())
}

func TestFontsBasic(t *testing.T) {
	data := buildFontPDF([]testFontSpec{
		{name: "Helvetica", subtype: "Type1"},
		{name: "TimesNewRoman", subtype: "TrueType"},
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fonts := r.Fonts()
	if len(fonts) != 2 {
		t.Fatalf("Fonts() returned %d entries, want 2", len(fonts))
	}
	byName := make(map[string]FontInfo)
	for _, fi := range fonts {
		byName[fi.Name] = fi
	}
	for _, want := range []string{"Helvetica", "TimesNewRoman"} {
		fi, ok := byName[want]
		if !ok {
			t.Errorf("font %q missing from Fonts() result", want)
			continue
		}
		if fi.Embedded {
			t.Errorf("font %q: Embedded=true, want false", want)
		}
		if len(fi.Pages) == 0 {
			t.Errorf("font %q: Pages empty, want non-empty", want)
		}
	}
}

func TestFontsEmbedded(t *testing.T) {
	data := buildFontPDF([]testFontSpec{
		{name: "CustomFont", subtype: "TrueType", embedded: true},
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fonts := r.Fonts()
	if len(fonts) != 1 {
		t.Fatalf("Fonts() returned %d entries, want 1", len(fonts))
	}
	if fonts[0].Name != "CustomFont" {
		t.Errorf("Fonts()[0].Name = %q, want CustomFont", fonts[0].Name)
	}
	if !fonts[0].Embedded {
		t.Errorf("font %q: Embedded=false, want true", fonts[0].Name)
	}
}

// buildType0FontPDF builds a minimal PDF with a single Type0 (composite) font.
// The font's /DescendantFonts array contains an inline CIDFont with an inline
// /FontDescriptor pointing to a /FontFile2 stream. This exercises the
// fontIsEmbedded() DescendantFonts traversal path.
func buildType0FontPDF() []byte {
	const fakeFont = "fake"
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	// Object 1: FontFile2 stream
	off1 := b.Len()
	fmt.Fprintf(&b, "1 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(fakeFont), fakeFont)

	// Object 2: Type0 font with inline DescendantFonts, inline CIDFont,
	// and inline FontDescriptor referencing the FontFile2 stream.
	off2 := b.Len()
	b.WriteString("2 0 obj\n")
	b.WriteString("<< /Type /Font /Subtype /Type0 /BaseFont /ArialMT /Encoding /Identity-H\n")
	b.WriteString("   /DescendantFonts [ << /Type /Font /Subtype /CIDFontType2 /BaseFont /ArialMT\n")
	b.WriteString("      /FontDescriptor << /Type /FontDescriptor /FontName /ArialMT /FontFile2 1 0 R >> >> ] >>\n")
	b.WriteString("endobj\n")

	// Object 3: Page
	off3 := b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 4 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 2 0 R >> >> >>\nendobj\n")

	// Object 4: Pages
	off4 := b.Len()
	b.WriteString("4 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	// Object 5: Catalog
	off5 := b.Len()
	b.WriteString("5 0 obj\n<< /Type /Catalog /Pages 4 0 R >>\nendobj\n")

	xrefOff := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, off := range []int{off1, off2, off3, off4, off5} {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 5 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	return []byte(b.String())
}

func TestFontsType0(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("Fonts() panicked on Type0 fixture: %v", rec)
		}
	}()
	data := buildType0FontPDF()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fonts := r.Fonts()
	if len(fonts) != 1 {
		t.Fatalf("Fonts() returned %d entries, want 1", len(fonts))
	}
	if fonts[0].Name != "ArialMT" {
		t.Errorf("Fonts()[0].Name = %q, want ArialMT", fonts[0].Name)
	}
	if fonts[0].Subtype != "Type0" {
		t.Errorf("Fonts()[0].Subtype = %q, want Type0", fonts[0].Subtype)
	}
	if !fonts[0].Embedded {
		t.Errorf("Fonts()[0].Embedded = false, want true (DescendantFonts path)")
	}
}

// TestFontsConflictingAliases pins the metadata-aggregation contract: two
// resource aliases share one BaseFont but point at DIFFERENT font objects —
// the unembedded one sorts first (/F1), the embedded one second (/F2). The
// inventory must report a single entry with Embedded=true (any instance
// embedded ⇒ embedded), not the first-encountered instance's false, and
// Pages must list the page once.
func TestFontsConflictingAliases(t *testing.T) {
	data := buildFontPDF([]testFontSpec{
		{name: "DualFont", subtype: "TrueType"},                 // /F1 — unembedded
		{name: "DualFont", subtype: "TrueType", embedded: true}, // /F2 — embedded
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fonts := r.Fonts()
	if len(fonts) != 1 {
		t.Fatalf("Fonts() returned %d entries, want 1 (deduplicated by BaseFont)", len(fonts))
	}
	if !fonts[0].Embedded {
		t.Error("Fonts()[0].Embedded = false, want true: a later embedded instance must aggregate")
	}
	if len(fonts[0].Pages) != 1 || fonts[0].Pages[0] != 1 {
		t.Errorf("Fonts()[0].Pages = %v, want [1] (no duplicate page entries)", fonts[0].Pages)
	}
}

func TestFontsEmpty(t *testing.T) {
	data := buildFontPDF(nil)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fonts := r.Fonts()
	if fonts != nil {
		t.Errorf("Fonts() = %v, want nil for PDF with no font resources", fonts)
	}
}

func TestFontsMalformed(t *testing.T) {
	// Two fonts with valid /BaseFont but broken descriptor structures:
	//   obj 1: /FontDescriptor is a string literal (not a dict) — broken descriptor path
	//   obj 2: /DescendantFonts is an integer (not an array) — broken Type0 path
	// Fonts() must not panic (null-safety proven) and must return both with Embedded=false.
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("Fonts() panicked on malformed fixture: %v", rec)
		}
	}()
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	off1 := b.Len()
	b.WriteString("1 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /BrokenDesc /FontDescriptor (not-a-dict) >>\nendobj\n")

	off2 := b.Len()
	b.WriteString("2 0 obj\n<< /Type /Font /Subtype /Type0 /BaseFont /BrokenArr /Encoding /Identity-H /DescendantFonts 42 >>\nendobj\n")

	off3 := b.Len()
	b.WriteString("3 0 obj\n<< /Type /Catalog /Pages 4 0 R >>\nendobj\n")
	off4 := b.Len()
	b.WriteString("4 0 obj\n<< /Type /Pages /Kids [5 0 R] /Count 1 >>\nendobj\n")
	off5 := b.Len()
	b.WriteString("5 0 obj\n<< /Type /Page /Parent 4 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R /F2 2 0 R >> >> >>\nendobj\n")

	xrefOff := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, off := range []int{off1, off2, off3, off4, off5} {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 3 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)

	r, err := OpenBytes([]byte(b.String()))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fonts := r.Fonts()
	// Both fonts have valid BaseFont and must be returned with Embedded=false.
	if len(fonts) != 2 {
		t.Fatalf("Fonts() returned %d entries, want 2 (both fonts have valid BaseFont)", len(fonts))
	}
	for _, fi := range fonts {
		if fi.Embedded {
			t.Errorf("font %q: Embedded=true, want false (broken descriptor/array)", fi.Name)
		}
	}
}
