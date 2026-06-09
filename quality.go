// Document- and page-level extraction-readiness signals: a deterministic
// routing hint (index / OCR / flag) for LLM/RAG ingestion pipelines, derived
// only from existing extraction diagnostics. Per-page and document decode-path
// quality ratios (DecodeRatios) accompany the signal; no ML, no rendering.

package pdf

import "strings"

// ExtractionSignal classifies a page's extraction readiness so an ingestion
// pipeline can route it without parsing logs: extractable text (index as-is),
// image-only (send to OCR), empty (flag for review), or degraded (flag; do not
// index). It is a best-effort routing hint, not a quality guarantee.
//
// The value set is additive (API-STABILITY.md, Additive-evolving): a later
// release may add values. Per-page decode-path quality is now exposed directly as
// DecodeRatios (on PageSignal and DocumentSummary); a derived low-confidence
// signal VALUE remains deferred — the ratios are stable facts, while a value would
// bake in a confidence threshold for which there is no corpus evidence yet.
// Callers MUST tolerate unknown values: treat any unrecognized ExtractionSignal as
// "needs review", never panic.
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

// classifyPageSignal returns the page's routing signal, its image-draw count, and
// the page's per-decode-path counters (the source for DocumentSummary's quality
// ratios) in a single classification, shared by Page.ExtractionSignal and
// Reader.DocumentSummary so both stay byte-identical.
//
// The text authority is the STRICT path: plainTextAndCounters (GetPlainText's
// core) surfaces a broken content stream as an error where the word/summary path
// (Page.Words, used by ExtractionSummary) silently recovers to a clean-looking
// partial. Using it here is what keeps a truncated stream from being mis-signalled
// as healthy text (the "silent-ok" gap), and the same pass yields the decode-path
// counters, so the ratios cost no extra interpreter run. Cost: one interpreter
// pass for text plus one image-scan pass — comparable to ExtractionSummary;
// intended for ingestion-time routing, not hot loops.
//
// Determinism: plainTextAndCounters, countDrawnImages, and IsNull are pure
// functions of the immutable post-open document, so the result is identical on
// every call and safe for concurrent use. No-panic is total: on the no-text path
// an image scan that panics (the scanner runs an interpreter without its own
// recover) degrades to SignalDegraded; on the text path the supplementary image
// count is guarded so a malformed image stream cannot downgrade a text-bearing
// page. Only a text-classified page contributes counters; every other branch
// returns a zero decodeCounters, so image-only/empty/degraded pages add no glyphs
// to the ratios.
func classifyPageSignal(p Page) (sig ExtractionSignal, imageCount int, counters decodeCounters) {
	if p.V.IsNull() {
		return SignalEmpty, 0, decodeCounters{}
	}
	text, dc, err := p.plainTextAndCounters(nil)
	if err != nil {
		// A degraded page decoded only a partial, unreliable stream; report it as
		// degraded and contribute no glyphs to the ratios (it is already flagged
		// for review). Discarding dc keeps the rollup describing clean text only.
		return SignalDegraded, 0, decodeCounters{}
	}
	if strings.TrimSpace(text) != "" {
		// Text extracts cleanly: that wins. The image count is supplementary
		// here (a text+image page may still warrant hi_res routing), so a panic
		// in the image scan must not downgrade a text-bearing page — guard it.
		return SignalText, guardedImageCount(p), dc
	}
	// No extractable text: the image count is now the classifier. An image scan
	// that panics means a malformed stream the text path happened to tolerate —
	// surface it as degraded rather than a silent "empty". A whitespace-only page
	// contributes no glyphs (counters stay zero) since it is not text-classified.
	defer func() {
		if recover() != nil {
			sig, imageCount, counters = SignalDegraded, 0, decodeCounters{}
		}
	}()
	if imageCount = countDrawnImages(p); imageCount > 0 {
		return SignalImageOnly, imageCount, decodeCounters{}
	}
	return SignalEmpty, 0, decodeCounters{}
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
	sig, _, _ := classifyPageSignal(p)
	return sig
}

// DecodeRatios reports the fraction of decoded glyphs (on a page, or rolled up
// across a document) that came through a lower-confidence decode path, so an
// ingestion pipeline can flag or re-route text that is present but unreliable.
//
// The fields are stable extraction FACTS, not a single opaque score: the caller
// sets its own thresholds. Each ratio is in [0,1] over the shared denominator
// Glyphs. The three ratios are NOT a disjoint partition and MUST NOT be summed
// into one "total unreliable" fraction: a U+FFFD glyph is also counted in its
// decode-source bucket, so when that bucket is missing-/ToUnicode or fallback the
// same glyph lands in two of the three ratios — a sum can then double-count and
// exceed 1. Threshold each ratio independently (or take the max).
//
// DecodeRatios is computed purely from per-decode-path glyph counts
// accumulated during text extraction — the same counts the content (Words) sink
// and the plain-text (GetPlainText) sink agree on for the committed encoding
// fixtures (the agreement is bounded to a single resolved font, no q/Q-scoped
// font change, separators excluded). It NEVER reads the warning store, so it is
// fully deterministic and concurrency-safe — strictly more so than
// DocumentSummary.Warnings, whose snapshot is bounded and scheduler-dependent
// under warning-store overflow.
//
// A page whose entire text decodes through an unknown /Encoding shows all three
// ratios at 0 (that path is not one of the three named ratios); it is not
// invisible — it always fires the document-scoped WarningUnsupportedEncoding.
type DecodeRatios struct {
	// Glyphs is the total decoded runes the ratios are computed over (the shared
	// denominator). 0 means no text was decoded on a text-bearing path — every
	// ratio is then 0. Only text-classified pages contribute glyphs; image-only,
	// empty, and degraded pages report a zero-value DecodeRatios.
	Glyphs int
	// MissingToUnicodeRatio is the fraction of Glyphs decoded through an Identity
	// or byte-table path with no usable /ToUnicode: the code points may be wrong.
	MissingToUnicodeRatio float64
	// FallbackRatio is the fraction of Glyphs decoded through a predefined-CMap
	// charset approximation rather than the font's own /ToUnicode.
	FallbackRatio float64
	// UnmappedRatio is the fraction of Glyphs that decoded to the Unicode
	// replacement character U+FFFD (a glyph the decode path could not map).
	UnmappedRatio float64
}

// decodeRatiosFrom derives the public ratios from one run's internal decode
// counters. The denominator is the total decoded runes across EVERY encSource;
// the total==0 guard keeps an empty page's ratios at 0 (no NaN — so DecodeRatios
// stays DeepEqual-stable across repeated and concurrent calls). The document
// rollup calls this over merged counters, so the document ratio is the weighted
// (sum-of-numerators / sum-of-denominators) ratio, never a mean of per-page
// ratios that would mis-weight short pages.
func decodeRatiosFrom(c decodeCounters) DecodeRatios {
	total := 0
	for _, n := range c.glyphs {
		total += n
	}
	dr := DecodeRatios{Glyphs: total}
	if total == 0 {
		return dr
	}
	denom := float64(total)
	dr.MissingToUnicodeRatio = float64(c.glyphs[encSourceMissingToUnicode]) / denom
	dr.FallbackRatio = float64(c.glyphs[encSourceFallback]) / denom
	dr.UnmappedRatio = float64(c.unmapped) / denom
	return dr
}

// PageSignal is one page's routing classification inside a DocumentSummary: the
// lean record (signal, locatable page number, image-draw count, decode ratios).
// Callers that also need word counts or per-page warnings call
// Page.ExtractionSummary.
type PageSignal struct {
	// Page is the 1-based page number.
	Page int
	// Signal is the page's routing classification.
	Signal ExtractionSignal
	// ImageCount is the number of image draw operations on the page
	// (countDrawnImages); 0 for degraded or null pages.
	ImageCount int
	// DecodeRatios reports this page's decode-path quality ratios (lower-confidence
	// decode fractions). Zero-valued for image-only, empty, and degraded pages.
	DecodeRatios DecodeRatios
}

// DocumentSummary aggregates per-page extraction-readiness signals across a
// document so an ingestion pipeline can make a single routing decision without
// iterating pages by hand. It also carries the document-level decode-path quality
// ratios (DecodeRatios), rolled up from the per-page counters by summing glyph
// counts (weighted), not by averaging per-page ratios.
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
	// DecodeRatios rolls the per-page decode ratios up across the document by
	// summing glyph counts (weighted), not by averaging per-page ratios. Only
	// text-classified pages contribute glyphs. Computed purely from decode
	// counters (never the warning store), so it is deterministic and
	// concurrency-safe.
	DecodeRatios DecodeRatios
}

// DocumentSummary classifies every page for routing and rolls the per-page
// signals up to the document level. See DocumentSummary (the type) for the
// determinism/concurrency contract and null-page-slot handling.
//
// Experimental: this API is additive-evolving; see the DocumentSummary type
// for the stability contract.
//
// It runs the extraction interpreter once per page; on a large document this is
// ingestion-time work, not a hot-loop call.
func (r *Reader) DocumentSummary() DocumentSummary {
	ds := DocumentSummary{TotalPages: r.NumPage()}
	var total decodeCounters
	for i, p := range r.Pages() {
		sig, imgs, counters := classifyPageSignal(p)
		total.merge(counters)
		ds.Pages = append(ds.Pages, PageSignal{
			Page:         i,
			Signal:       sig,
			ImageCount:   imgs,
			DecodeRatios: decodeRatiosFrom(counters),
		})
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
	ds.DecodeRatios = decodeRatiosFrom(total)
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
	return filterWarnings(ws, func(w ExtractionWarning) bool { return w.Page == 0 })
}
