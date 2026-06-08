// Per-page extraction-readiness signals: Page.ExtractionSummary classifies a
// page for ingestion pipelines (index it, skip it, or route it to OCR)
// without OCR, image decoding, rendering, or layout reconstruction.

package pdf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// pageMapCache lazily memoizes buildPageMap's objptr.id -> 1-based page
// number map for one Reader. PDFs are immutable post-open, so the map never
// invalidates; once built it is read-only, which is what lets every reader
// of the map skip locking (the objCache precedent: only the build itself
// synchronizes). Size is bounded by the page objects the tree actually
// yields: buildPageMap's consecutive-null bail stops a bogus /Count from
// driving a long scan, and NumPage clamps at maxPageCount.
type pageMapCache struct {
	once sync.Once
	m    map[uint32]int
}

// cachedPageMap returns the page-number map, building it on first use. Only
// the summary path uses it: whole-document summarization is O(pages) once
// per Reader instead of O(pages) per page. The metadata APIs (Annotations,
// Outline, Dest) deliberately keep their transient per-call builds — caching
// under them would silently turn a one-off metadata lookup on a long-lived
// Reader into lifetime retention.
func (r *Reader) cachedPageMap() map[uint32]int {
	r.pageNums.once.Do(func() { r.pageNums.m = r.buildPageMap() })
	return r.pageNums.m
}

// PageExtractionSummary reports cheap extraction-readiness signals for one
// page: whether the page yields extractable text, how many words, how many
// image draw operations, and the page-scoped warnings attributed to it.
// It involves no OCR, no image decoding, no rendering, and no layout
// reconstruction. The struct is not comparable (Warnings is a slice) and
// carries no comparability promise; new fields may be added in minor
// versions.
type PageExtractionSummary struct {
	// Page is the 1-based page number, or 0 when the page cannot be
	// located in this Reader's page tree (null pages; Page values not
	// produced by this Reader).
	Page int
	// HasText reports whether the text layer yields at least one word
	// (WordCount > 0). It reflects extractable text only — a scanned page
	// with no text layer reports false regardless of visual content.
	HasText bool
	// WordCount is the number of words Page.Words reports.
	WordCount int
	// ImageCount is the number of image DRAW OPERATIONS the page's content
	// stream performs: Do of an Image XObject (including inside Form
	// XObjects, depth-capped) plus inline images counted as BI..EI pairs
	// (a stray EI without a BI is ignored). It counts operations, not
	// distinct images — a tiled image drawn N times counts N. Image streams
	// are never opened; only operator names and header dictionaries are
	// read. Undrawn resource-dictionary entries do not count.
	ImageCount int
	// ImageCoverage is the fraction of the page (MediaBox) area covered by drawn
	// image bounding boxes, clamped to [0,1]; 0 when the page has no images or no
	// positive MediaBox area. It distinguishes a full-bleed scan (near 1.0) from
	// an incidental thumbnail (well under 1.0) so an OCR router can tell a scanned
	// page from a logo. Coarse by design: overlapping or partly off-page images
	// are summed naively before clamping, so it is a density signal, not exact
	// coverage.
	ImageCoverage float64
	// Warnings holds this page's page-scoped warnings (ExtractionWarning
	// with Page equal to this page's number) observed so far, sorted by
	// (Page, Code, Detail); nil when none or when Page is 0.
	// Document-scoped warnings are reported by Reader.Warnings only.
	Warnings []ExtractionWarning
}

// ExtractionSummary classifies one page for ingestion pipelines.
// When the page draws images but yields no extractable text — confirmed by
// an error-free plain-text pass — it records an image_only_page warning on
// the Reader (visible in both the returned Warnings field and
// Reader.Warnings) — without attempting OCR. Pages that cannot be located
// in the page tree (Page == 0) are classified in the return value only,
// never in Reader.Warnings.
//
// A null Page returns the zero summary and nil error. On error, Page and
// ImageCount are populated when determinable; HasText and WordCount are
// zero; no warning is emitted — a page whose content stream fails to
// interpret reports an error rather than a guessed classification.
//
// The result is deterministic: the same call on the same page yields the
// same summary, including under repeated or concurrent use (warnings
// deduplicate; the page map is immutable once built). Safe for concurrent
// use. Running the summary executes the content interpreter, so
// document-scoped warnings (fonts, encodings) may be newly observed as a
// side effect and appear in Reader.Warnings — never in the returned
// Warnings field.
func (p Page) ExtractionSummary() (s PageExtractionSummary, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			// Keep the already-attributed page number for failure routing;
			// everything else is unreliable after a panic.
			s = PageExtractionSummary{Page: s.Page}
			err = errors.New(fmt.Sprint(rec))
		}
	}()
	if p.V.IsNull() {
		return PageExtractionSummary{}, nil
	}
	r := p.V.r
	if r != nil {
		s.Page = r.cachedPageMap()[p.V.ptr.id]
	}
	box := p.MediaBox()
	// One count-only image scan yields both the draw count and the summed image
	// area for coverage, retaining no per-image refs - so a tiled or adversarial
	// content stream that draws an image many times cannot inflate memory here
	// (Page.Images is the only path that retains refs). Like the previous
	// countDrawnImages call it uses the scanner without an internal recover, so a
	// malformed content stream panics through to the deferred handler above.
	_, cnt, areaSum := scanPageImages(p, true)
	s.ImageCount = cnt
	s.ImageCoverage = imageCoverage(areaSum, box)
	words, werr := p.Words()
	if werr != nil {
		// Text extraction failed: report the failure rather than guess.
		// Page and ImageCount stay populated for routing.
		return s, werr
	}
	s.WordCount = len(words)
	s.HasText = s.WordCount > 0
	if rerr := emitRoutingWarning(r, p, s, words, box); rerr != nil {
		return s, rerr
	}
	if r != nil && s.Page != 0 {
		s.Warnings = pageWarnings(r, s.Page)
	}
	return s, nil
}

// emitRoutingWarning records the page-scoped routing warning implied by the
// summary so far: image_only_page for an image-bearing page with no text, or
// sparse_text for a text-bearing page whose whole text layer is page furniture
// (a page number/folio at the margin) that the binary HasText signal misses. The
// image-only branch runs a confirmation pass that can fail; that error is
// returned and becomes the summary's error.
func emitRoutingWarning(r *Reader, p Page, s PageExtractionSummary, words []Word, box [4]float64) error {
	switch {
	case !s.HasText && s.ImageCount > 0:
		// Confirmation pass: Words is shielded by Content's internal recover and
		// can present a failed stream as empty. GetPlainText surfaces those
		// panics as an error; classify only on a clean, whitespace-empty pass.
		text, terr := p.GetPlainText(nil)
		if terr != nil {
			return terr
		}
		if strings.TrimSpace(text) == "" {
			warnPageScoped(r, s.Page, WarningImageOnlyPage,
				strconv.Itoa(s.ImageCount)+" image draw operation(s), no extractable text")
		}
	case s.HasText && isSparseArtifactText(words, box):
		warnPageScoped(r, s.Page, WarningSparseText,
			strconv.Itoa(s.WordCount)+" page-furniture token(s) at the margin, no body text")
	}
	return nil
}

// warnPageScoped emits a page-scoped warning when the page is locatable in a
// present Reader, and is a no-op otherwise. It centralises the
// (r != nil && page != 0) guard the page-scoped emitters share.
func warnPageScoped(r *Reader, page int, code ExtractionWarningCode, detail string) {
	if r != nil && page != 0 {
		r.warn(page, code, detail)
	}
}

// Sparse-text (page-furniture) detection thresholds. A text-bearing page whose
// entire text layer is a few short page-number-like tokens at the top/bottom
// margin is page furniture (a folio), not body text - the artifact-only
// false-positive an OCR router must still see as scan-like.
const (
	sparseTextMaxWords       = 3    // at most this many words on the page
	sparseTextMaxTokenRunes  = 4    // each furniture token at most this many runes
	sparseTextMarginFraction = 0.10 // top/bottom band as a fraction of page height
)

// isSparseArtifactText reports whether words are nothing but page furniture: at
// most sparseTextMaxWords short page-number-like tokens (Unicode decimal digits
// and page-number punctuation, at least one digit overall), every one inside the
// top or bottom margin band of box. It reads only word geometry and code points,
// so it is deterministic and script-independent (fullwidth and other Unicode
// decimal digits count; letters of any script, including a lone CJK glyph, do
// not). Returns false when box has no positive height (furniture cannot then be
// localised).
func isSparseArtifactText(words []Word, box [4]float64) bool {
	if len(words) == 0 || len(words) > sparseTextMaxWords {
		return false
	}
	bottom, top := box[1], box[3]
	if top <= bottom {
		return false
	}
	margin := (top - bottom) * sparseTextMarginFraction
	sawDigit := false
	for _, w := range words {
		hasDigit, ok := furnitureToken(w.S)
		if !ok || !inMarginBand(w.Y, bottom, top, margin) {
			return false
		}
		sawDigit = sawDigit || hasDigit
	}
	return sawDigit
}

// furnitureToken reports whether s is a page-number-like token - non-empty, at
// most sparseTextMaxTokenRunes runes, every rune a Unicode decimal digit or
// page-number punctuation - and whether it contains at least one digit. Using
// unicode.IsDigit (category Nd) catches fullwidth and Arabic-Indic page numbers;
// any letter (any script, so a lone CJK glyph is real content) disqualifies it.
func furnitureToken(s string) (hasDigit, ok bool) {
	n := 0
	for _, r := range s {
		if n++; n > sparseTextMaxTokenRunes {
			return false, false
		}
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case isPageNumberPunct(r):
			// allowed, not a digit
		default:
			return false, false
		}
	}
	return hasDigit, n > 0
}

// isPageNumberPunct reports whether r is punctuation that can appear in a page
// number or folio (ASCII punctuation plus the en dash and em dash).
func isPageNumberPunct(r rune) bool {
	switch r {
	case '.', '-', '/', '(', ')', '[', ']', '\u2013', '\u2014':
		return true
	}
	return false
}

// inMarginBand reports whether y (a word's bottom edge) sits within margin of the
// page's bottom or top edge.
func inMarginBand(y, bottom, top, margin float64) bool {
	return y <= bottom+margin || y >= top-margin
}

// pageWarnings filters the Reader's warning snapshot to the entries
// attributed to page n. The snapshot is already sorted by (Page, Code,
// Detail), so the filtered slice keeps that order. Returns nil when none.
func pageWarnings(r *Reader, n int) []ExtractionWarning {
	var out []ExtractionWarning
	for _, w := range r.Warnings() {
		if w.Page == n {
			out = append(out, w)
		}
	}
	return out
}
