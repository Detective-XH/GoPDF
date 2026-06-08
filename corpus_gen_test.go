package pdf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorpusRegenerate materialises the synthetic .pdf fixtures from the in-process
// builders. Run once with -update to commit them; thereafter they are static. It is
// skipped on normal runs so a missing -update never rewrites committed bytes.
//
//	go test -run TestCorpusRegenerate -update .
func TestCorpusRegenerate(t *testing.T) {
	if !*updateGolden {
		t.Skip("run with -update to regenerate synthetic fixtures")
	}
	// Ordered list so iteration is deterministic (no map ranging in serialised bytes).
	type synthEntry struct {
		rel  string
		data []byte
	}
	synth := []synthEntry{
		{"plaintext/hello-ascii.pdf", buildTextPDF("BT /F1 12 Tf (Hello, Corpus.) Tj ET")},
		{"styled/multifont.pdf", buildStyledCorpusPDF()},
		{"bench/synthetic-multipage.pdf", buildBenchCorpusPDF()},
		// Extraction-readiness signal fixtures (consumers: quality score, image/scanned classifier).
		{"signals/image-full-bleed.pdf", buildImageFullBleedPDF()},
		{"signals/image-thumbnail.pdf", buildImageThumbnailPDF()},
		{"signals/image-thumbnail-text.pdf", buildImageThumbnailTextPDF()},
		{"signals/text-artifact-only.pdf", buildArtifactOnlyPDF()},
		{"signals/text-numeric-center.pdf", buildNumericCenterPDF()},
		{"signals/malformed-unclosed-bt.pdf", buildMalformedUnclosedBTPDF()},
		{"signals/malformed-mismatched-qq.pdf", buildMalformedMismatchedQQPDF()},
		{"signals/malformed-truncated.pdf", buildMalformedTruncatedPDF()},
		// Fallback decode-path fixtures (consumer: the fallback encoding framework).
		{"encoding/predefined-identity.pdf", buildPredefinedIdentityPDF()},
		{"encoding/charset-shiftjis.pdf", buildCharsetShiftJISPDF()},
		{"encoding/ucs2-be.pdf", buildUCS2BEPDF()},
		{"encoding/differences-partial.pdf", buildDifferencesPartialPDF()},
		{"encoding/unknown-name.pdf", buildUnknownEncodingPDF()},
		{"encoding/unmapped-glyph.pdf", buildUnmappedGlyphPDF()},
		// Rotated + vertical warning fixtures (consumer: fallback-encoding risk
		// warnings; also the fixture half of the rotated/vertical geometry gate).
		{"geometry/rotated-90.pdf", buildRotated90PDF()},
		{"geometry/vertical-cmap.pdf", buildVerticalCMapPDF()},
	}
	for _, e := range synth {
		p := corpusPath(e.rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", e.rel, err)
		}
		if err := os.WriteFile(p, e.data, 0o600); err != nil {
			t.Fatalf("write %s: %v", e.rel, err)
		}
	}
}

// buildStyledCorpusPDF builds a deterministic multi-font styled-text fixture.
// Single page, two fonts (F1 = Helvetica 18pt title, F2 = Times-Roman 10pt body).
// Two text runs at different sizes — mirrors buildWidthsFontPDF object assembly.
// Byte-deterministic: no time, no map iteration in serialised output.
func buildStyledCorpusPDF() []byte {
	// Two separate BT/ET blocks so the PDF text-extraction engine sees two runs.
	titleRun := "BT /F1 18 Tf 72 700 Td (GoPDF Corpus) Tj ET"
	bodyRun := "BT /F2 10 Tf 72 680 Td (styled body text) Tj ET"
	stream := titleRun + "\n" + bodyRun

	return buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page — two fonts registered: F1 and F2
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /Font << /F1 5 0 R /F2 6 0 R >> >> >>",
		// 4: Content stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		// 5: F1 — Helvetica (title font)
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		// 6: F2 — Times-Roman (body font)
		"<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman >>",
	})
}

// buildBenchCorpusPDF builds a deterministic synthetic multi-page PDF suitable
// for extraction benchmarks. 24 pages, each with an identical text run drawn
// from a shared Helvetica font (obj 5). Uses buildPDFFromObjects so xref and
// object numbering are consistent across runs.
//
// Object layout:
//
//	1: Catalog
//	2: Pages (Kids = [3 0 R, 8 0 R, 13 0 R, ...])
//	3+(i*5): Page i
//	4+(i*5): Content stream for page i
//	5: Shared font /F1
//
// To avoid map ranging, pages are built in a fixed ascending order.
func buildBenchCorpusPDF() []byte {
	const numPages = 24

	// We'll collect all objects in fixed order, then call buildPDFFromObjects.
	// Object numbering (1-based, matching their position in the slice):
	//   obj 1 = Catalog
	//   obj 2 = Pages
	//   obj 3+(i*2) = Page i  (i=0..numPages-1)
	//   obj 4+(i*2) = Content stream for page i
	//   obj 3+(numPages*2) = shared /F1 font
	//
	// Kids refs: 3 0 R, 5 0 R, 7 0 R ... (every odd obj starting at 3)

	fontObjNum := 3 + numPages*2 // e.g. 3+48=51

	// Build Kids array string
	var kidsArr strings.Builder
	kidsArr.WriteString("[")
	for i := 0; i < numPages; i++ {
		pageObjNum := 3 + i*2
		if i > 0 {
			kidsArr.WriteString(" ")
		}
		fmt.Fprintf(&kidsArr, "%d 0 R", pageObjNum)
	}
	kidsArr.WriteString("]")

	objs := make([]string, 0, 2+numPages*2+1)

	// obj 1: Catalog
	objs = append(objs, "<< /Type /Catalog /Pages 2 0 R >>")

	// obj 2: Pages
	objs = append(objs,
		fmt.Sprintf("<< /Type /Pages /Kids %s /Count %d >>", kidsArr.String(), numPages))

	// objs 3..3+numPages*2-1: alternating Page + Content pairs
	for i := 0; i < numPages; i++ {
		contentObjNum := 4 + i*2 // e.g. 4,6,8,...
		text := fmt.Sprintf("Bench page %d of %d.", i+1, numPages)
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)

		// Page dict
		objs = append(objs,
			fmt.Sprintf(
				"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]"+
					" /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>",
				contentObjNum, fontObjNum,
			),
		)
		// Content stream
		objs = append(objs,
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		)
	}

	// Last object: shared /F1 font
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	return buildPDFFromObjects(objs)
}

// --- Extraction-readiness signal fixtures -----------------------------------
// All byte-deterministic (no time, no map iteration). q…Q isolates each image's
// scaling CTM from the text matrix. imageStream (images_test.go) is a never-decoded
// stub (/Length 0); the /Filter name is metadata only — no image stream is opened.

// buildImageFullBleedPDF builds a one-page fixture whose only content is a single
// image scaled to fill the MediaBox, with no text. Coverage ratio (image bbox /
// page area) ≈ 1.0 — the full-bleed scan signal for the image/scanned classifier.
func buildImageFullBleedPDF() []byte {
	content := "q 612 0 0 792 0 0 cm /Img0 Do Q"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /XObject << /Img0 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		imageStream(1200, 1600, "DCTDecode"),
	})
}

// buildImageThumbnailPDF builds a one-page fixture with a 60x60 image and no text.
// Coverage ratio 3600/484704 ≈ 0.0074 — same v1 signal as full-bleed today (locks
// the thumbnail-vs-full-bleed gap the classifier closes).
func buildImageThumbnailPDF() []byte {
	content := "q 60 0 0 60 0 0 cm /Img0 Do Q"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /XObject << /Img0 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		imageStream(64, 64, "DCTDecode"),
	})
}

// buildImageThumbnailTextPDF builds a one-page fixture with BOTH a small image and
// a body-text run. Resources carries /Font AND /XObject (template: page_test.go's
// TestGetPlainTextXObjectNestedFont). Mixed page — must NOT be classified image-only.
func buildImageThumbnailTextPDF() []byte {
	content := "q 60 0 0 60 0 0 cm /Img0 Do Q BT /F1 12 Tf 72 700 Td (body text run) Tj ET"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /Font << /F1 5 0 R >> /XObject << /Img0 6 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		imageStream(64, 64, "DCTDecode"),
	})
}

// buildArtifactOnlyPDF builds a one-page fixture whose only text is a page-number-like
// token at the bottom extremity (Td 300 24). v1 reports HasText=true (the sparse-text
// false-negative the classifier closes). Feeds the classifier's "short tokens at page extremities".
func buildArtifactOnlyPDF() []byte {
	return buildTextPDF("BT /F1 8 Tf 300 24 Td (12) Tj ET")
}

// buildNumericCenterPDF builds a one-page fixture whose only text is the same
// page-number-like token as buildArtifactOnlyPDF but drawn at the page CENTRE
// (Td 300 400), not the margin. Same token, different position: it proves the
// sparse-text margin band is load-bearing — this page must NOT fire WarningSparseText.
func buildNumericCenterPDF() []byte {
	return buildTextPDF("BT /F1 8 Tf 300 400 Td (12) Tj ET")
}

// buildMalformedUnclosedBTPDF builds a fixture whose content opens BT but never
// closes it with ET.
func buildMalformedUnclosedBTPDF() []byte {
	return buildTextPDF("BT /F1 12 Tf (alpha beta) Tj")
}

// buildMalformedMismatchedQQPDF builds a fixture with more Q than q (excess restores).
func buildMalformedMismatchedQQPDF() []byte {
	return buildTextPDF("q BT /F1 12 Tf (gamma) Tj ET Q Q Q")
}

// buildMalformedTruncatedPDF builds a fixture with content-level truncation — TJ with
// no array operand (NOT file/xref truncation; that surface is TestRedteamP2TruncatedXref).
func buildMalformedTruncatedPDF() []byte {
	return buildTextPDF("BT /F1 12 Tf (delta) Tj TJ")
}

// --- Fallback decode-path + rotated/vertical fixtures ------------------------
// All byte-deterministic. Each font omits /ToUnicode (except unmapped-glyph,
// whose /ToUnicode deliberately under-covers its codespace) so getEncoder takes
// the /Encoding branch and the matching document-scoped warning fires. The
// decoded text is the golden authority (GetPlainText); positions are irrelevant.

// encodingPagePDF assembles a one-page, one-font, one-content-stream PDF whose
// font object body is fontBody (raw, e.g. with a custom /Encoding). Shared shape
// for the decode-path fixtures so each generator states only what differs.
func encodingPagePDF(content, fontBody string) []byte {
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		fontBody,
	})
}

// buildPredefinedIdentityPDF: /Encoding /Identity-H, no /ToUnicode → byteEncoder
// over pdfDocEncoding; fires WarningMissingToUnicode (Identity CMap, no ToUnicode).
func buildPredefinedIdentityPDF() []byte {
	return encodingPagePDF(
		"BT /F1 12 Tf (identity) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /Encoding /Identity-H >>",
	)
}

// buildCharsetShiftJISPDF: /Encoding /90ms-RKSJ-H, no /ToUnicode → multibyte
// charset fallback; fires WarningFallbackEncoding. Content bytes 0x82 0xA0 decode
// to あ via Shift-JIS.
func buildCharsetShiftJISPDF() []byte {
	return encodingPagePDF(
		"BT /F1 12 Tf (\x82\xa0) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /Encoding /90ms-RKSJ-H >>",
	)
}

// buildUCS2BEPDF: /Encoding /UniGB-UCS2-H, no /ToUnicode → ucs2BEEncoder; fires
// WarningFallbackEncoding. Content bytes 0x4E 0x2D (ASCII "N-") decode to 中 as a
// single UCS-2 BE code unit.
func buildUCS2BEPDF() []byte {
	return encodingPagePDF(
		"BT /F1 12 Tf (N-) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /Encoding /UniGB-UCS2-H >>",
	)
}

// buildDifferencesPartialPDF: /Encoding dict with a /Differences entry whose glyph
// name is absent from the AGL table → applyDifferences counts 1 lost mapping and
// fires WarningMissingGlyphMapping. Content "differ" decodes via the WinAnsi base
// table (the lost entry is counted, NOT rendered as U+FFFD).
func buildDifferencesPartialPDF() []byte {
	return encodingPagePDF(
		"BT /F1 12 Tf (differ) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /Encoding"+
			" << /Type /Encoding /BaseEncoding /WinAnsiEncoding"+
			" /Differences [65 /A 66 /nonexistentglyph] >> >>",
	)
}

// buildUnknownEncodingPDF: /Encoding names an encoding absent from cmapEncoderTable
// → encoderForCMapName default (pdfDocEncoding); fires WarningUnsupportedEncoding.
func buildUnknownEncodingPDF() []byte {
	return encodingPagePDF(
		"BT /F1 12 Tf (unknown) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /Encoding /NonexistentEncoding >>",
	)
}

// unmappedToUnicodeCMap is a minimal /ToUnicode program that defines a 1-byte
// codespace <00>..<FF> but maps ONLY <41> ('A'). Any other in-range byte matches
// the codespace yet has no bfchar/bfrange, so cmap.decodeOne returns U+FFFD.
const unmappedToUnicodeCMap = "/CIDInit /ProcSet findresource begin\n" +
	"12 dict begin\nbegincmap\n/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n" +
	"1 begincodespacerange\n<00> <FF>\nendcodespacerange\n" +
	"1 beginbfchar\n<41> <0041>\nendbfchar\n" +
	"endcmap\nend\nend"

// buildUnmappedGlyphPDF: a font with a /ToUnicode CMap that under-covers its
// codespace. Content "AB" → 'A' maps to A; 'B' is in-codespace but unmapped →
// U+FFFD in the output. Silent today (no unmapped-glyph counter); the gap the
// fallback encoding framework closes. NOTE: highest byte-level risk (embedded
// CMap stream) — build/test FIRST.
func buildUnmappedGlyphPDF() []byte {
	content := "BT /F1 12 Tf (AB) Tj ET"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /ToUnicode 6 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream",
			len(unmappedToUnicodeCMap), unmappedToUnicodeCMap),
	})
}

// buildRotated90PDF: a 90°-rotated text run (Tm = [0 1 -1 0 ...]). FontSize =
// Trm[0][0] = 0 today, yet GetPlainText returns the text → SignalText, no warning.
// Locks the "looks healthy" state the rotated-text risk warning will flag. No golden.
func buildRotated90PDF() []byte {
	return encodingPagePDF(
		"BT /F1 12 Tf 0 1 -1 0 72 400 Tm (Rotated) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	)
}

// buildVerticalCMapPDF: /Encoding /UniJIS-UCS2-V (a vertical -V CMap). WMode is
// never read (cmap.go), so the glyph decodes and advances horizontally; today
// SignalText + WarningFallbackEncoding (ucs2), no vertical warning. Locks the gap
// the vertical-writing-mode risk warning closes. No golden.
func buildVerticalCMapPDF() []byte {
	return encodingPagePDF(
		"BT /F1 12 Tf (N-) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /Encoding /UniJIS-UCS2-V >>",
	)
}
