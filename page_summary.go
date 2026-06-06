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
	// The counting pass uses the image metadata scanner without internal
	// recover: a malformed content stream panics through to the deferred
	// handler above and becomes this summary's error.
	s.ImageCount = countDrawnImages(p)
	words, werr := p.Words()
	if werr != nil {
		// Text extraction failed: report the failure rather than guess.
		// Page and ImageCount stay populated for routing.
		return s, werr
	}
	s.WordCount = len(words)
	s.HasText = s.WordCount > 0
	if !s.HasText && s.ImageCount > 0 {
		// Confirmation pass: Words is shielded by Content's internal
		// recover and can present a failed stream as empty. GetPlainText
		// surfaces those panics as an error; classify only on a clean,
		// whitespace-empty pass. Runs only on this candidate path, where
		// there is no text to decode.
		text, terr := p.GetPlainText(nil)
		if terr != nil {
			return s, terr
		}
		if strings.TrimSpace(text) == "" && r != nil && s.Page != 0 {
			r.warn(s.Page, WarningImageOnlyPage,
				strconv.Itoa(s.ImageCount)+" image draw operation(s), no extractable text")
		}
	}
	if r != nil && s.Page != 0 {
		s.Warnings = pageWarnings(r, s.Page)
	}
	return s, nil
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
