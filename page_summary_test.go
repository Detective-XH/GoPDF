package pdf

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// imageOnlyPDF builds a one-page document whose content draws a single image
// XObject and nothing else. The image stream carries /Filter /DCTDecode with
// an empty body: if any code path ever opened the image stream, the
// unsupported-filter warning (or a decode error) would surface — its absence
// is evidence the classification never touches image payloads.
func imageOnlyPDF() []byte {
	pageContent := "/Img0 Do"
	return buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page — image XObject only
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		// 4: Image XObject
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Filter /DCTDecode /Length 0 >>\nstream\nendstream",
		// 5: Page content
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
	})
}

// twoPageTextImagePDF builds a two-page document: page 1 shows text through
// /F1 (fontBody), page 2 draws one image XObject and nothing else.
func twoPageTextImagePDF(fontBody string) []byte {
	text := "BT /F1 12 Tf (Hi) Tj ET"
	img := "/Img0 Do"
	return buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		// 3: Page 1 — text via /F1
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 8 0 R >> >> /Contents 5 0 R >>",
		// 4: Page 2 — image only
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 7 0 R >> >> /Contents 6 0 R >>",
		// 5: Page 1 content
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(text), text),
		// 6: Page 2 content
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(img), img),
		// 7: Image XObject
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
		// 8: Font /F1
		fontBody,
	})
}

// plainExtract runs reader-level text plus first-page Content — the plain
// extraction surface WITHOUT ExtractionSummary, for asserting that plain
// extraction never classifies.
func plainExtract(t *testing.T, r *Reader) {
	t.Helper()
	rd, err := r.GetPlainText(context.Background())
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	_, _ = io.Copy(io.Discard, rd)
	_ = r.Page(1).Content()
}

// assertImageOnlyWarning asserts ws is exactly one image_only_page warning
// attributed to page n.
func assertImageOnlyWarning(t *testing.T, ws []ExtractionWarning, n int) {
	t.Helper()
	if len(ws) != 1 {
		t.Fatalf("warnings: got %d entries (%v), want 1", len(ws), ws)
	}
	w := ws[0]
	if w.Code != WarningImageOnlyPage || w.Page != n {
		t.Errorf("warning: got {Page:%d Code:%s}, want {Page:%d Code:%s}", w.Page, w.Code, n, WarningImageOnlyPage)
	}
	if w.Message != warningMessages[WarningImageOnlyPage] {
		t.Errorf("warning Message: got %q, want the fixed table entry", w.Message)
	}
	if !strings.Contains(w.Detail, "image draw operation") {
		t.Errorf("warning Detail: got %q, want it to name image draw operations", w.Detail)
	}
}

// TestExtractionSummaryInlineImageOnly — a page whose only content is an
// inline image (BI/ID/EI), with NO XObject resources at all: the EI the
// lexer dispatches after skipInlineImage is the counted evidence.
func TestExtractionSummaryInlineImageOnly(t *testing.T) {
	content := "BI /W 1 /H 1 ID A EI"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}))
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	if sum.Page != 1 || sum.HasText || sum.WordCount != 0 || sum.ImageCount != 1 {
		t.Errorf("summary: got %+v, want {Page:1 HasText:false WordCount:0 ImageCount:1}", sum)
	}
	assertImageOnlyWarning(t, sum.Warnings, 1)
}

// TestExtractionSummaryStrayEINotCounted — a bare EI token (no BI), and an
// ID..EI without BI, are not draw evidence: a malformed empty page must not
// classify as image-only (Codex gate finding G1).
func TestExtractionSummaryStrayEINotCounted(t *testing.T) {
	for _, content := range []string{"EI", "ID A EI"} {
		r := mustOpen(t, buildPDFFromObjects([]string{
			"<< /Type /Catalog /Pages 2 0 R >>",
			"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
			"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		}))
		sum, err := r.Page(1).ExtractionSummary()
		if err != nil {
			t.Fatalf("%q: ExtractionSummary: %v", content, err)
		}
		if sum.ImageCount != 0 {
			t.Errorf("%q: ImageCount = %d, want 0 (stray EI is not draw evidence)", content, sum.ImageCount)
		}
		if ws := r.Warnings(); ws != nil {
			t.Errorf("%q: want no warnings, got %v", content, ws)
		}
	}
}

// TestExtractionSummaryTextOnly — a clean text page: words counted, no
// images, no warnings anywhere (the unit-level noise gate).
func TestExtractionSummaryTextOnly(t *testing.T) {
	r := mustOpen(t, singleFontPDF(
		"BT /F1 12 Tf (Hello world) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	))
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	want := PageExtractionSummary{Page: 1, HasText: true, WordCount: 2}
	if !reflect.DeepEqual(sum, want) {
		t.Errorf("summary: got %+v, want %+v", sum, want)
	}
	if ws := r.Warnings(); ws != nil {
		t.Errorf("Warnings: want nil, got %v", ws)
	}
}

// TestExtractionSummaryImageOnly — the acceptance criterion moved here from
// the diagnostics feature: an image-only page emits an image-only warning
// without attempting OCR, and plain extraction alone emits nothing.
func TestExtractionSummaryImageOnly(t *testing.T) {
	r := mustOpen(t, imageOnlyPDF())
	plainExtract(t, r)
	if ws := r.Warnings(); ws != nil {
		t.Fatalf("plain extraction: want no warnings, got %v", ws)
	}
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	if sum.Page != 1 || sum.HasText || sum.WordCount != 0 || sum.ImageCount != 1 {
		t.Errorf("summary: got %+v, want {Page:1 HasText:false WordCount:0 ImageCount:1}", sum)
	}
	assertImageOnlyWarning(t, sum.Warnings, 1)
	if !reflect.DeepEqual(r.Warnings(), sum.Warnings) {
		t.Errorf("Reader.Warnings %v != summary.Warnings %v", r.Warnings(), sum.Warnings)
	}
	// Repeat: dedup keeps one entry and the summary is identical.
	sum2, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("second ExtractionSummary: %v", err)
	}
	if !reflect.DeepEqual(sum, sum2) {
		t.Errorf("repeat summary diverged:\n first %+v\nsecond %+v", sum, sum2)
	}
	if ws := r.Warnings(); len(ws) != 1 {
		t.Errorf("after repeat: got %d warnings, want 1 (dedup)", len(ws))
	}
}

// TestExtractionSummaryMixed — text + drawn image: has text, not classified
// image-only.
func TestExtractionSummaryMixed(t *testing.T) {
	content := "BT /F1 12 Tf (Hi) Tj ET /Img0 Do"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 6 0 R >> /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}))
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	want := PageExtractionSummary{Page: 1, HasText: true, WordCount: 1, ImageCount: 1}
	if !reflect.DeepEqual(sum, want) {
		t.Errorf("summary: got %+v, want %+v", sum, want)
	}
	if ws := r.Warnings(); ws != nil {
		t.Errorf("Warnings: want nil, got %v", ws)
	}
}

// TestExtractionSummaryUndrawnImageResource — a resource-declared image the
// content never draws must not count and must not classify (drawn-evidence
// semantics).
func TestExtractionSummaryUndrawnImageResource(t *testing.T) {
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
		"<< /Length 0 >>\nstream\nendstream",
	}))
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	want := PageExtractionSummary{Page: 1}
	if !reflect.DeepEqual(sum, want) {
		t.Errorf("summary: got %+v, want %+v (undrawn resource must not count)", sum, want)
	}
	if ws := r.Warnings(); ws != nil {
		t.Errorf("Warnings: want nil, got %v", ws)
	}
}

// TestExtractionSummaryFormNestedImage — an image drawn inside a Form
// XObject counts through the depth-capped recursion.
func TestExtractionSummaryFormNestedImage(t *testing.T) {
	pageContent := "/Fm0 Do"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Fm0 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
		formXObjStream("/Img0 Do", "<< /XObject << /Img0 6 0 R >> >>"),
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
	}))
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	if sum.ImageCount != 1 {
		t.Errorf("ImageCount: got %d, want 1 (Form-nested draw)", sum.ImageCount)
	}
	assertImageOnlyWarning(t, sum.Warnings, 1)
}

// TestExtractionSummaryMalformedContent — an unsupported /Crypt filter on
// the content stream makes the summary error out; extraction failure is
// never classified as image-only (the document-scoped unsupported_filter
// from Value.Reader is the only warning).
func TestExtractionSummaryMalformedContent(t *testing.T) {
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
		"<< /Length 4 /Filter /Crypt >>\nstream\nabcd\nendstream",
	}))
	sum, err := r.Page(1).ExtractionSummary()
	if err == nil {
		t.Fatalf("ExtractionSummary: want error for unsupported content filter, got %+v", sum)
	}
	if sum.Page != 1 {
		t.Errorf("summary.Page: got %d, want 1 (preserved for routing)", sum.Page)
	}
	for _, w := range r.Warnings() {
		if w.Code == WarningImageOnlyPage {
			t.Errorf("image_only_page emitted on extraction failure: %v", w)
		}
	}
	assertOneWarning(t, r, WarningUnsupportedFilter, "Crypt")
}

// TestExtractionSummaryBadOperatorNoClassify — the residual corner: a
// malformed operator panics the text callbacks (Content recovers internally,
// so Words reports empty with nil error) while the counting pass completes.
// The confirmation pass must surface the failure as an error, not classify.
func TestExtractionSummaryBadOperatorNoClassify(t *testing.T) {
	content := "BT Tj ET /Img0 Do"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}))
	sum, err := r.Page(1).ExtractionSummary()
	if err == nil {
		t.Fatalf("ExtractionSummary: want error from the confirmation pass, got %+v", sum)
	}
	for _, w := range r.Warnings() {
		if w.Code == WarningImageOnlyPage {
			t.Errorf("image_only_page emitted despite failed text extraction: %v", w)
		}
	}
}

// TestExtractionSummaryEmptyPage — no text, no images: empty is not
// image-only.
func TestExtractionSummaryEmptyPage(t *testing.T) {
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		"<< /Length 0 >>\nstream\nendstream",
	}))
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	want := PageExtractionSummary{Page: 1}
	if !reflect.DeepEqual(sum, want) {
		t.Errorf("summary: got %+v, want %+v", sum, want)
	}
	if ws := r.Warnings(); ws != nil {
		t.Errorf("Warnings: want nil, got %v", ws)
	}
}

// TestExtractionSummaryNullPage — a page number past the tree returns the
// zero summary with no error and no warnings from the summary itself.
func TestExtractionSummaryNullPage(t *testing.T) {
	r := mustOpen(t, imageOnlyPDF())
	sum, err := r.Page(99).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	if !reflect.DeepEqual(sum, PageExtractionSummary{}) {
		t.Errorf("summary: got %+v, want zero value", sum)
	}
	if ws := r.Warnings(); ws != nil {
		t.Errorf("Warnings: want nil, got %v", ws)
	}
}

// TestExtractionSummaryUnlocatablePage — a page object outside the /Pages
// tree classifies in the return value only: page-scoped codes are never
// emitted without attribution.
func TestExtractionSummaryUnlocatablePage(t *testing.T) {
	orphanContent := "/Img0 Do"
	r := mustOpen(t, buildPDFFromObjects([]string{
		// 1: Catalog — /Orphan dangles outside the page tree
		"<< /Type /Catalog /Pages 2 0 R /Orphan 6 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: the real (empty) page
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		// 4: empty content
		"<< /Length 0 >>\nstream\nendstream",
		// 5: Image XObject
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
		// 6: orphan page drawing the image
		"<< /Type /Page /Resources << /XObject << /Img0 5 0 R >> >> /Contents 7 0 R >>",
		// 7: orphan content
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(orphanContent), orphanContent),
	}))
	p := Page{r.Trailer().Key("Root").Key("Orphan")}
	sum, err := p.ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	if sum.Page != 0 || sum.HasText || sum.ImageCount != 1 || sum.Warnings != nil {
		t.Errorf("summary: got %+v, want {Page:0 HasText:false ImageCount:1 Warnings:nil}", sum)
	}
	for _, w := range r.Warnings() {
		if w.Code == WarningImageOnlyPage {
			t.Errorf("image_only_page emitted for an unlocatable page: %v", w)
		}
	}
}

// TestExtractionSummaryPageAttribution — page numbers and warning
// attribution on a two-page document.
func TestExtractionSummaryPageAttribution(t *testing.T) {
	r := mustOpen(t, twoPageTextImagePDF(
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"))
	sum1, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if sum1.Page != 1 || !sum1.HasText || sum1.Warnings != nil {
		t.Errorf("page 1 summary: got %+v, want {Page:1 HasText:true Warnings:nil}", sum1)
	}
	sum2, err := r.Page(2).ExtractionSummary()
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if sum2.Page != 2 || sum2.HasText || sum2.ImageCount != 1 {
		t.Errorf("page 2 summary: got %+v, want {Page:2 HasText:false ImageCount:1}", sum2)
	}
	assertImageOnlyWarning(t, sum2.Warnings, 2)
	if ws := r.Warnings(); len(ws) != 1 || ws[0].Page != 2 {
		t.Errorf("Reader.Warnings: got %v, want exactly the page-2 entry", ws)
	}
}

// TestExtractionSummaryWarningsFilter — document-scoped warnings observed
// during a summary never leak into the per-page Warnings field.
func TestExtractionSummaryWarningsFilter(t *testing.T) {
	r := mustOpen(t, twoPageTextImagePDF(
		"<< /Type /Font /Subtype /Type1 /BaseFont /Odd /Encoding /Bogus-Enc >>"))
	sum1, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if sum1.Warnings != nil {
		t.Errorf("page 1 Warnings: got %v, want nil (unsupported_encoding is document-scoped)", sum1.Warnings)
	}
	sum2, err := r.Page(2).ExtractionSummary()
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	assertImageOnlyWarning(t, sum2.Warnings, 2)
	ws := r.Warnings()
	if len(ws) != 2 {
		t.Fatalf("Reader.Warnings: got %d entries (%v), want 2", len(ws), ws)
	}
	if ws[0].Page != 0 || ws[0].Code != WarningUnsupportedEncoding || ws[1].Page != 2 || ws[1].Code != WarningImageOnlyPage {
		t.Errorf("Reader.Warnings: got %v, want [doc-scoped unsupported_encoding, page-2 image_only_page]", ws)
	}
}

// TestNullPageSlotWarning — /Count overstating the real kids: reader-level
// extraction warns with the skipped 1-based index; the Pages iterator
// deduplicates into the same entry.
func TestNullPageSlotWarning(t *testing.T) {
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 3 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>",
		"<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>",
		"<< /Length 0 >>\nstream\nendstream",
	}))
	rd, err := r.GetPlainText(context.Background())
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	_, _ = io.Copy(io.Discard, rd)
	ws := r.Warnings()
	if len(ws) != 1 {
		t.Fatalf("Warnings: got %d entries (%v), want 1", len(ws), ws)
	}
	want := ExtractionWarning{Page: 3, Code: WarningNullPageSlot, Message: warningMessages[WarningNullPageSlot]}
	if ws[0] != want {
		t.Errorf("warning: got %+v, want %+v", ws[0], want)
	}
	for range r.Pages() {
	}
	if ws := r.Warnings(); len(ws) != 1 {
		t.Errorf("after Pages(): got %d warnings, want 1 (dedup)", len(ws))
	}
}

// TestExtractionSummaryDeterministicConcurrent — GOMAXPROCS goroutines
// summarizing every page of a shared Reader agree with a fresh-Reader
// baseline (exercises the sync.Once map build and the filter-by-page
// determinism argument under -race).
func TestExtractionSummaryDeterministicConcurrent(t *testing.T) {
	data := twoPageTextImagePDF(
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	summarize := func(r *Reader) ([]PageExtractionSummary, error) {
		var out []PageExtractionSummary
		for i := 1; i <= r.NumPage(); i++ {
			s, err := r.Page(i).ExtractionSummary()
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	}
	rBase := mustOpen(t, data)
	want, err := summarize(rBase)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	rShared := mustOpen(t, data)
	var wg sync.WaitGroup
	for w := range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			got, err := summarize(rShared)
			if err != nil {
				t.Errorf("worker %d: %v", w, err)
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("worker %d: summaries diverged from baseline:\n got %+v\nwant %+v", w, got, want)
			}
		})
	}
	wg.Wait()
}

// TestCachedPageMapMetadataUnchanged — the metadata APIs keep their
// transient page maps (never fed by the summary's cache), and repeated
// summaries resolve stable page numbers from the once-built map.
func TestCachedPageMapMetadataUnchanged(t *testing.T) {
	r := mustOpen(t, twoPageTextImagePDF(
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"))
	outlineBefore := r.Outline()
	annotsBefore, errBefore := r.Page(1).Annotations()
	sumA, err := r.Page(2).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	sumB, err := r.Page(2).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary repeat: %v", err)
	}
	if sumA.Page != 2 || sumB.Page != 2 {
		t.Errorf("page numbers: got %d then %d, want 2 and 2", sumA.Page, sumB.Page)
	}
	outlineAfter := r.Outline()
	annotsAfter, errAfter := r.Page(1).Annotations()
	if !reflect.DeepEqual(outlineBefore, outlineAfter) {
		t.Errorf("Outline changed across summaries: %+v -> %+v", outlineBefore, outlineAfter)
	}
	if !reflect.DeepEqual(annotsBefore, annotsAfter) || (errBefore == nil) != (errAfter == nil) {
		t.Errorf("Annotations changed across summaries: %v/%v -> %v/%v", annotsBefore, errBefore, annotsAfter, errAfter)
	}
}
