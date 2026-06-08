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
		{"signals/malformed-unclosed-bt.pdf", buildMalformedUnclosedBTPDF()},
		{"signals/malformed-mismatched-qq.pdf", buildMalformedMismatchedQQPDF()},
		{"signals/malformed-truncated.pdf", buildMalformedTruncatedPDF()},
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
