package pdf

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// maxStoredWarnings and maxWarningDetailLen together bound the warning
// store's retained heap (dual bound, following the package's DoS-bound
// convention — compare the objCache entry+byte budgets): an adversarial
// document must not drive unbounded growth through thousands of distinct
// font names, nor through a single attacker-sized name — the parser's
// readName has no length cap, so every document-derived Detail component is
// clamped BEFORE string concatenation (construction cost) and the assembled
// Detail is clamped again at add() (retained size).
const (
	maxStoredWarnings   = 4096
	maxWarningDetailLen = 256
	// maxFilterDetailNames caps how many /Filter array elements are joined
	// into one Detail: an adversarial array must not buy an expensive join.
	maxFilterDetailNames = 8
)

// clampDetail bounds one document-derived Detail component (or a final
// assembled Detail). The truncated slice is cloned so a retained warning key
// cannot pin a large parser string's backing array in memory.
func clampDetail(s string) string {
	if len(s) <= maxWarningDetailLen {
		return s
	}
	cut := maxWarningDetailLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.Clone(s[:cut]) + "..."
}

// warningMessages maps each code to its fixed human-readable message.
// Messages are constant per code BY CONSTRUCTION — warn() has no message
// parameter — so a formatted message can never fragment deduplication or
// break the determinism contract. Per-occurrence variability belongs in
// ExtractionWarning.Detail and is limited to enumerable document facts
// (font, CMap, and filter names; deterministic counts).
var warningMessages = map[ExtractionWarningCode]string{
	WarningMissingToUnicode:    "font lacks a usable ToUnicode CMap; extracted text may not be accurate Unicode",
	WarningMalformedToUnicode:  "font ToUnicode CMap parse panicked; CMap discarded, fell back to missing-ToUnicode path",
	WarningFallbackEncoding:    "text decoded via an approximate fallback encoding",
	WarningUnsupportedEncoding: "font encoding is unsupported; bytes decoded as PDFDocEncoding",
	WarningMissingGlyphMapping: "some glyphs cannot be mapped to Unicode",
	WarningLegacyFont:          "a legacy non-Unicode Indic font was used; its text decodes to Latin gibberish rather than the intended script (numeric data may be intact)",
	WarningUnsupportedFilter:   "stream filter is unsupported; the stream's contents were skipped",
	WarningTruncated:           "warning storage limit reached; further distinct warnings were dropped",
	WarningImageOnlyPage:       "page declares image content but yields no extractable text; OCR is not attempted",
	WarningNullPageSlot:        "page slot is null and was skipped during extraction",
	WarningRotatedText:         "a text run has a rotated (non-horizontal) baseline; geometry-based layout for it is unreliable",
	WarningVerticalWritingMode: "a vertical writing-mode CMap was selected; glyphs advance vertically using a default one-em displacement (per-glyph vertical metrics are not applied)",
	WarningSparseText:          "page text is only sparse page furniture (e.g. a page number) at the margin; it may be a scanned page",
	WarningNonFiniteGeometry:   "geometry held a non-finite coordinate that DebugJSON sanitized to zero",
}

// warningStore accumulates deduplicated extraction warnings for one Reader.
//
// Set semantics is the determinism mechanism: extraction operations re-emit
// the same warnings on every run (encoders are re-selected per page per
// operation), dedup absorbs the repeats, and below the cap the same SET of
// operations therefore yields the same warning set regardless of page order,
// repetition, or concurrent interleaving. That order-independence is what lets
// Warnings() join the Reader's blanket concurrency contract. The one exception
// is overflow: past maxStoredWarnings the retained subset is bounded and may
// depend on operation order, flagged by the WarningTruncated sentinel (see
// Reader.Warnings). Concurrent use stays safe either way.
//
// Locking: mu guards the map only; no extraction work ever runs under it.
type warningStore struct {
	mu        sync.Mutex
	set       map[ExtractionWarning]struct{}
	truncated bool
}

func (w *warningStore) add(warn ExtractionWarning) {
	warn.Detail = clampDetail(warn.Detail) // last-line size guard for the key
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.set == nil {
		w.set = make(map[ExtractionWarning]struct{})
	}
	if _, ok := w.set[warn]; ok {
		return // duplicates stay cheap no-ops, also after truncation
	}
	if w.truncated {
		return
	}
	// One slot is reserved for the sentinel, so the retained total is
	// exactly maxStoredWarnings — never maxStoredWarnings+1.
	if len(w.set) >= maxStoredWarnings-1 {
		w.truncated = true
		w.set[ExtractionWarning{
			Code:    WarningTruncated,
			Message: warningMessages[WarningTruncated],
		}] = struct{}{}
		return
	}
	w.set[warn] = struct{}{}
}

// snapshot returns the warnings sorted by (Page, Code, Detail). Message is
// excluded from the sort key: it is constant per Code (see warningMessages).
func (w *warningStore) snapshot() []ExtractionWarning {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.set) == 0 {
		return nil
	}
	out := make([]ExtractionWarning, 0, len(w.set))
	for warn := range w.set {
		out = append(out, warn)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Page != b.Page {
			return a.Page < b.Page
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Detail < b.Detail
	})
	return out
}

// ExtractionWarningCode classifies a non-fatal extraction issue. Codes are
// additive across minor versions; callers must tolerate values they do not
// recognize.
type ExtractionWarningCode string

// Extraction warning codes reported by this package. Most warnings are
// document-scoped (Page == 0); image_only_page, sparse_text, and null_page_slot
// are page-scoped. non_finite_geometry is page-scoped for page/text geometry and
// document-scoped (page in Detail) for link geometry.
const (
	// WarningMissingToUnicode: a font has no usable /ToUnicode CMap (absent
	// for an Identity CMap, or present but unparseable), so extracted bytes
	// may not be accurate Unicode.
	WarningMissingToUnicode ExtractionWarningCode = "missing_tounicode"
	// WarningMalformedToUnicode: a font's /ToUnicode CMap stream contains a
	// structural malformation (e.g. a stray PostScript keyword inside a dict
	// literal) that caused a parse panic. The CMap was discarded; extraction
	// fell back to the same path as a missing ToUnicode. Extracted text for
	// this font may not be accurate Unicode.
	WarningMalformedToUnicode ExtractionWarningCode = "malformed_tounicode"
	// WarningFallbackEncoding: a predefined CMap was decoded via a charset
	// approximation (e.g. 90ms-RKSJ-H via Shift-JIS) rather than the real
	// CMap program.
	WarningFallbackEncoding ExtractionWarningCode = "fallback_encoding"
	// WarningUnsupportedEncoding: an unknown encoding name or unexpected
	// /Encoding object; bytes were decoded as PDFDocEncoding.
	WarningUnsupportedEncoding ExtractionWarningCode = "unsupported_encoding"
	// WarningMissingGlyphMapping: glyphs that cannot be mapped to Unicode —
	// unknown glyph names in /Differences, or a font resource missing from
	// the page's resource dictionary.
	WarningMissingGlyphMapping ExtractionWarningCode = "missing_glyph_mapping"
	// WarningLegacyFont: a known legacy NON-Unicode Indic (Devanagari) font
	// (Kruti Dev, DevLys, Walkman-Chanakya, Vivek, …) was selected. Such fonts
	// carry no real Unicode mapping — their glyph codes follow a legacy keyboard
	// layout — so every text surface (Words/Lines/GetPlainText/Tables) decodes
	// the script to Latin gibberish (e.g. "rkfydk" for तालिका) though numeric data
	// is usually intact. Detection only: the original characters are unrecoverable
	// from a pure-text path (only OCR or a font-specific remap could recover them).
	// Document-scoped (fonts are document-level); emitted at encoder selection. The
	// per-table Tables() surface ALSO reports this as TableWarningLegacyFont.
	WarningLegacyFont ExtractionWarningCode = "legacy_font_text"
	// WarningUnsupportedFilter: a stream declares a filter this package
	// cannot decode (e.g. /Crypt); the stream's contents were skipped.
	WarningUnsupportedFilter ExtractionWarningCode = "unsupported_filter"
	// WarningTruncated: the bounded warning store overflowed; further
	// distinct warnings were dropped.
	WarningTruncated ExtractionWarningCode = "warnings_truncated"
	// WarningImageOnlyPage: a page draws images but yields no extractable
	// text — an image-only/scanned-page candidate for OCR routing. Emitted
	// only by Page.ExtractionSummary (never by plain extraction), and only
	// when the page is locatable in the page tree (Page > 0).
	WarningImageOnlyPage ExtractionWarningCode = "image_only_page"
	// WarningNullPageSlot: a page-tree slot resolved to null and was
	// skipped by reader-level extraction. Page is the 1-based index whose
	// lookup returned null (for a /Count overstating the real kids, that is
	// the trailing indices, not the gap's position in the tree).
	WarningNullPageSlot ExtractionWarningCode = "null_page_slot"
	// WarningRotatedText: a text run was drawn with a rotated baseline (the
	// writing direction has a vertical component, Trm[0][1] != 0), so its
	// FontSize = Trm[0][0] and X-advance degrade and geometry-based layout for
	// that run is unreliable. A horizontal-baseline shear (synthetic italic) is
	// NOT flagged — its baseline and FontSize stay correct. Detection only — no
	// geometry fix is attempted. Document-scoped: observed only on the
	// Content/Words/Lines/Texts path (the plain-text path tracks no geometry).
	WarningRotatedText ExtractionWarningCode = "rotated_text"
	// WarningVerticalWritingMode: a vertical (-V) writing-mode CMap was
	// selected. Glyphs decode correctly and advance down the page using the PDF
	// default one-em vertical displacement (no per-glyph /W2 metrics are read).
	// Document-scoped; emitted at encoder selection, so a vertical font that also
	// carries a usable /ToUnicode is not flagged here (the ToUnicode path wins
	// before the CMap name is examined) even though its advance is still vertical.
	WarningVerticalWritingMode ExtractionWarningCode = "vertical_writing_mode"
	// WarningSparseText: a page yields only a few short page-number-like tokens
	// at the top or bottom margin (page furniture) and no body text, so it reads
	// as text-bearing yet is an image-only/scanned-page candidate for OCR
	// routing. Emitted only by Page.ExtractionSummary; page-scoped (Page > 0).
	WarningSparseText ExtractionWarningCode = "sparse_text"
	// WarningNonFiniteGeometry: DebugJSON sanitized a non-finite geometry coordinate
	// (±Inf or NaN) to 0 — reachable when adversarial content-stream numbers overflow
	// the text-matrix multiplication, a page box overflows its width subtraction, or a
	// link rectangle overflows its per-page transform. The warning marks that a
	// coordinate was zeroed, so a sanitized 0 is not mistaken for real geometry at the
	// origin. Page text/box geometry is page-scoped (Page > 0, in the page dict); link
	// geometry, surfaced only by Reader.DebugJSON, is document-scoped (Page == 0, in the
	// envelope) with the affected page in Detail ("link on page N").
	WarningNonFiniteGeometry ExtractionWarningCode = "non_finite_geometry"
)

// ExtractionWarning describes one non-fatal issue observed while reading or
// extracting. The struct is comparable and stays comparable — callers may
// use it as a map key; the field set is frozen for the v0.x line (new
// diagnostics arrive as new codes, not new fields).
type ExtractionWarning struct {
	// Page is the 1-based page number for page-scoped warnings
	// (image_only_page, null_page_slot), or 0 for document-scoped warnings —
	// font/encoding/filter warnings are document-scoped because fonts are
	// document-level objects shared across pages. Page == 0 is also used for a
	// geometry warning whose natural page has no dedicated output slot, e.g. a
	// non_finite_geometry on a link rectangle, which carries its page in Detail.
	Page int
	// Code classifies the issue.
	Code ExtractionWarningCode
	// Message is fixed human-readable text, constant per Code.
	Message string
	// Detail discriminates occurrences: font, CMap, or filter name.
	Detail string
}

// Warnings returns the non-fatal extraction warnings observed so far by this
// Reader. It reports problems noticed by reading and extraction operations
// already performed (text extraction, content interpretation, stream reads);
// it does not itself extract anything, so a freshly opened Reader typically
// reports none.
//
// The result is nil when no warnings have been observed. It is a freshly
// allocated copy, sorted by (Page, Code, Detail): safe to retain and modify,
// and deterministic — the same set of operations on the same document yields
// the same warnings, including under repeated or concurrent extraction
// (warnings deduplicate). Storage is bounded at 4096 retained warnings
// including a warnings_truncated sentinel that appears on overflow; past the
// cap the kept subset may depend on operation order. Warnings never mutates
// Reader state and is safe for concurrent use. ExtractionWarning is and
// stays comparable with a frozen field set; codes are additive across minor
// versions and callers must tolerate unknown values.
func (r *Reader) Warnings() []ExtractionWarning {
	return r.warnings.snapshot()
}

// warn records one warning on the Reader. The message is looked up from
// warningMessages so it is constant per code (see the table's comment).
// page is 0 for document-scoped warnings. Nil-safe.
func (r *Reader) warn(page int, code ExtractionWarningCode, detail string) {
	if r == nil {
		return
	}
	r.warnings.add(ExtractionWarning{
		Page:    page,
		Code:    code,
		Message: warningMessages[code],
		Detail:  detail,
	})
}

// warn records a document-scoped warning through the Reader this Value
// belongs to. A zero Value carries no Reader; the call is then a no-op,
// which keeps hook sites unconditional.
func (v Value) warn(code ExtractionWarningCode, detail string) {
	if v.r != nil {
		v.r.warn(0, code, detail)
	}
}

// fontRef builds the Detail discriminator for font-scoped warnings:
// "font <BaseFont> (obj <id>)". The object id disambiguates distinct font
// objects that share a BaseFont name (e.g. subset clones), so their warnings
// do not dedup-merge into one; both parts are document-derived and
// deterministic per document. BaseFont is clamped BEFORE concatenation:
// readName has no length cap, and an attacker-sized name must not buy a
// large copy on every interpreter run.
func fontRef(f Font) string {
	return "font " + clampDetail(f.BaseFont()) + " (obj " + strconv.Itoa(int(f.V.ptr.id)) + ")"
}

// filterDetail canonicalizes a /Filter value into a bounded Detail string:
// filter NAMES only — never a formatted Value or err.Error(), whose string
// forms can embed stream byte offsets and would fragment deduplication.
// At most maxFilterDetailNames array elements are joined, each clamped, so
// an adversarial /Filter array cannot buy an expensive join.
func filterDetail(filter Value) string {
	switch filter.Kind() {
	case Name:
		return "filter " + clampDetail(filter.Name())
	case Array:
		n := filter.Len()
		truncated := false
		if n > maxFilterDetailNames {
			n = maxFilterDetailNames
			truncated = true
		}
		names := make([]string, 0, n)
		for i := 0; i < n; i++ {
			names = append(names, clampDetail(filter.Index(i).Name()))
		}
		out := "filter " + strings.Join(names, "+")
		if truncated {
			out += "+..."
		}
		return out
	default:
		return "non-name /Filter entry"
	}
}
