// Document- and page-level extraction-readiness signals: a deterministic
// routing hint (index / OCR / flag) for LLM/RAG ingestion pipelines, derived
// only from existing extraction diagnostics. No ratios, no ML in this
// baseline; decode-path and coverage ratios are pre-announced additive
// follow-ups.

package pdf

import "strings"

// ExtractionSignal classifies a page's extraction readiness so an ingestion
// pipeline can route it without parsing logs: extractable text (index as-is),
// image-only (send to OCR), empty (flag for review), or degraded (flag; do not
// index). It is a best-effort routing hint, not a quality guarantee.
//
// The value set is additive (API-STABILITY.md, Additive-evolving): a later
// release may add values — for example a low-confidence signal once per-page
// decode-path ratios exist. Callers MUST tolerate unknown values:
// treat any unrecognized ExtractionSignal as "needs review", never panic.
type ExtractionSignal string

const (
	// SignalText: the page yields extractable text. Route fast (index as-is).
	SignalText ExtractionSignal = "text"
	// SignalImageOnly: the page draws images but yields no extractable text —
	// a scanned or image-only page. Route to OCR.
	SignalImageOnly ExtractionSignal = "image_only"
	// SignalEmpty: the page yields neither extractable text nor drawn images
	// (a blank page, or one whose only content is unsupported). Route to review.
	SignalEmpty ExtractionSignal = "empty"
	// SignalDegraded: text extraction failed on the page — a malformed or
	// truncated content stream surfaced through the strict text path. The page
	// is at best a partial result; route to review and never index as-is.
	SignalDegraded ExtractionSignal = "degraded"
)

// classifyPageSignal returns the page's routing signal and its image-draw count
// in a single classification, shared by Page.ExtractionSignal and
// Reader.DocumentSummary so both stay byte-identical.
//
// The text authority is the STRICT path, Page.GetPlainText: it surfaces a broken
// content stream as an error where the word/summary path (Page.Words, used by
// ExtractionSummary) silently recovers to a clean-looking partial. Using it here
// is what keeps a truncated stream from being mis-signalled as healthy text (the
// "silent-ok" gap). Cost: one interpreter pass for text plus one image-scan pass
// — comparable to ExtractionSummary; intended for ingestion-time routing, not
// hot loops.
//
// Determinism: GetPlainText, countDrawnImages, and IsNull are pure functions of
// the immutable post-open document, so the result is identical on every call and
// safe for concurrent use. No-panic is total: on the no-text path an image scan
// that panics (the scanner runs an interpreter without its own recover) degrades
// to SignalDegraded; on the text path the supplementary image count is guarded
// so a malformed image stream cannot downgrade a text-bearing page.
func classifyPageSignal(p Page) (sig ExtractionSignal, imageCount int) {
	if p.V.IsNull() {
		return SignalEmpty, 0
	}
	text, err := p.GetPlainText(nil)
	if err != nil {
		return SignalDegraded, 0
	}
	if strings.TrimSpace(text) != "" {
		// Text extracts cleanly: that wins. The image count is supplementary
		// here (a text+image page may still warrant hi_res routing), so a panic
		// in the image scan must not downgrade a text-bearing page — guard it.
		return SignalText, guardedImageCount(p)
	}
	// No extractable text: the image count is now the classifier. An image scan
	// that panics means a malformed stream the text path happened to tolerate —
	// surface it as degraded rather than a silent "empty".
	defer func() {
		if recover() != nil {
			sig, imageCount = SignalDegraded, 0
		}
	}()
	if imageCount = countDrawnImages(p); imageCount > 0 {
		return SignalImageOnly, imageCount
	}
	return SignalEmpty, 0
}

// guardedImageCount returns countDrawnImages but never panics: on a malformed
// image stream it returns 0. Used on the text-bearing path where the count is
// supplementary and must not downgrade the page's signal.
func guardedImageCount(p Page) (n int) {
	defer func() { _ = recover() }()
	return countDrawnImages(p)
}

// ExtractionSignal classifies this page for ingestion routing (see the
// ExtractionSignal type). It is deterministic, safe for repeated and concurrent
// use, and never panics — a malformed content stream is reported as
// SignalDegraded. A null or absent page (for example Page(num) out of range)
// reports SignalEmpty.
//
// It runs the extraction interpreter; document-scoped font/encoding warnings may
// be observed as a side effect and appear in Reader.Warnings (the same contract
// as ExtractionSummary).
func (p Page) ExtractionSignal() ExtractionSignal {
	sig, _ := classifyPageSignal(p)
	return sig
}

// PageSignal is one page's routing classification inside a DocumentSummary: the
// lean record (signal, locatable page number, image-draw count). Callers that
// also need word counts or per-page warnings call Page.ExtractionSummary.
type PageSignal struct {
	// Page is the 1-based page number.
	Page int
	// Signal is the page's routing classification.
	Signal ExtractionSignal
	// ImageCount is the number of image draw operations on the page
	// (countDrawnImages); 0 for degraded or null pages.
	ImageCount int
}

// DocumentSummary aggregates per-page extraction-readiness signals across a
// document so an ingestion pipeline can make a single routing decision without
// iterating pages by hand. In this baseline it carries no ratios; decode-path
// and coverage ratios are pre-announced additive fields.
//
// The classification and counts — TotalPages, Pages, and the four *Pages
// tallies — are deterministic and safe for concurrent use: they are pure
// functions of the immutable document (they never read the Reader's warning
// store), identical on every platform. The supplementary Warnings field is
// deterministic up to the warning-store cap and best-effort beyond it; see that
// field's note.
//
// This API is experimental and Additive-evolving (API-STABILITY.md): fields and
// ExtractionSignal values may be added. Compare defensively and tolerate unknown
// signal values.
type DocumentSummary struct {
	// TotalPages is the document's declared page count (Reader.NumPage()).
	TotalPages int
	// Pages holds one record per page reachable in page order via
	// Reader.Pages(). Null page slots are skipped (and reported as
	// WarningNullPageSlot), never recorded as SignalEmpty; a long run of null
	// slots stops traversal. Consequently len(Pages) may be less than
	// TotalPages on a malformed page tree — the difference is the skipped or
	// unreachable slots, observable through Reader.Warnings.
	Pages []PageSignal
	// TextPages, ImageOnlyPages, EmptyPages, DegradedPages are the per-signal
	// tallies over Pages (the per-signal fields routing logic keys on, not one
	// opaque score). They sum to len(Pages).
	TextPages      int
	ImageOnlyPages int
	EmptyPages     int
	DegradedPages  int
	// Warnings holds the document-scoped (Page == 0) warnings the Reader has
	// observed THROUGH THE END OF THIS CALL — the document-level confidence
	// reducers (font/encoding/filter issues). It is the deduplicated snapshot,
	// so it INCLUDES any document-scoped warnings emitted by earlier operations
	// on this Reader, not only those from this aggregation pass. Page-scoped
	// warnings (image_only, null_page_slot) are not duplicated here; read them
	// from each page's ExtractionSummary or Reader.Warnings. Sorted by
	// (Page, Code, Detail); nil when none.
	//
	// Determinism caveat: this is the deduplicated, bounded Reader.Warnings()
	// snapshot. If a document yields more than the warning-store cap of DISTINCT
	// warnings, the store truncates (emitting WarningTruncated) and the retained
	// subset is best-effort — under concurrent calls it may differ, because
	// which distinct warnings won the race to fill the store before truncation
	// is scheduler-dependent. The signal fields above are unaffected (they do
	// not read the store). This bound is the existing Reader.Warnings() behaviour
	// on adversarial inputs, not specific to DocumentSummary.
	Warnings []ExtractionWarning
}

// DocumentSummary classifies every page for routing and rolls the per-page
// signals up to the document level. See DocumentSummary (the type) for the
// determinism/concurrency contract and null-page-slot handling.
//
// It runs the extraction interpreter once per page; on a large document this is
// ingestion-time work, not a hot-loop call.
func (r *Reader) DocumentSummary() DocumentSummary {
	ds := DocumentSummary{TotalPages: r.NumPage()}
	for i, p := range r.Pages() {
		sig, imgs := classifyPageSignal(p)
		ds.Pages = append(ds.Pages, PageSignal{Page: i, Signal: sig, ImageCount: imgs})
		switch sig {
		case SignalText:
			ds.TextPages++
		case SignalImageOnly:
			ds.ImageOnlyPages++
		case SignalEmpty:
			ds.EmptyPages++
		case SignalDegraded:
			ds.DegradedPages++
		}
	}
	// Captured AFTER the full pass: this pass has emitted every document-scoped
	// warning the document can produce, so the deduplicated snapshot is complete
	// regardless of concurrent callers (which only add duplicates). This is the
	// same end-of-pass capture discipline ExtractionSummary and the concurrency
	// snapshot rely on.
	ds.Warnings = docScopedWarnings(r.Warnings())
	return ds
}

// docScopedWarnings returns the document-scoped (Page == 0) entries of a sorted
// warning snapshot, preserving order. Returns nil when none, so an all-clean
// document yields a nil DocumentSummary.Warnings rather than an empty slice.
func docScopedWarnings(ws []ExtractionWarning) []ExtractionWarning {
	var out []ExtractionWarning
	for _, w := range ws {
		if w.Page == 0 {
			out = append(out, w)
		}
	}
	return out
}
