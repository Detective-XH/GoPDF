package pdf

import (
	"reflect"
	"strings"
	"testing"
)

// decodePathExpect is the deterministic decode-path / geometry signal a fallback
// decode-path fixture locks. Encoder-selection warnings are DOCUMENT-SCOPED (Page==0),
// so they surface in DocumentSummary().Warnings / Reader.Warnings(), NOT in
// PageExtractionSummary.Warnings (page_summary.go) — assert on the document axis.
type decodePathExpect struct {
	signal      ExtractionSignal        // expected page-1 routing signal
	docWarnings []ExtractionWarningCode // doc-scoped codes that MUST be present (subset)
	hasUnmapped bool                    // GetPlainText output must contain U+FFFD
	// detailSubstr, when non-empty, requires at least one captured warning whose
	// Detail contains it. This disambiguates a code another path can also emit:
	// WarningMissingGlyphMapping is ALSO raised at plaintext.go (handlePlainTf) for
	// a font resource missing from /Resources, so a bare code check would pass
	// vacuously on a resource-wiring bug. "Differences" pins the /Differences
	// source; for the vertical fixture it pins the -V CMap name (a -V→-H typo
	// fails it — fallback_encoding alone is shared with the horizontal CMaps).
	detailSubstr string
	// degenerateRun, when true, requires a page-1 text run whose FontSize collapsed
	// to 0 while still carrying glyphs — the rotated-Tm signature (FontSize =
	// Trm[0][0] = 0 for a 90° run; a normal page carries the Tf size). Without it a
	// rotated fixture asserts only SignalText, which every text page satisfies.
	degenerateRun bool
	// wantRotatedWarning requires WarningRotatedText to fire after a Content() pass.
	// Rotation is detectable ONLY on the content sink (Trm exists only there), so
	// DocumentSummary's GetPlainText pass never sees it — assert it via r.Warnings()
	// after an explicit Content() pass.
	wantRotatedWarning bool
	// notWarnings are codes that MUST be absent after the full extraction (the
	// cross-contamination guard: a Tm/encoder mixup would otherwise pass green —
	// e.g. the rotated fixture must not fire the vertical warning, and vice versa).
	notWarnings []ExtractionWarningCode
}

// decodePathExpectations is the single source of truth for slice-2 signal
// contracts, keyed by fixture Path. Consumed by TestCorpusDecodePathFixtures AND
// the geometry/ arm of anchorNoGoldenSignal (DRY). The fallback encoding framework
// extends this map (and assertDecodePath) when it adds per-page decode-path
// counters and the rotation/vertical risk warnings.
//
// Rows reflect the EMPIRICAL classification (probe run at implementation), not a
// hypothesis: every encoding/ fixture extracts deterministic text and fires the
// listed document-scoped warning; unmapped-glyph and rotated-90 are silent today.
var decodePathExpectations = map[string]decodePathExpect{
	"encoding/predefined-identity.pdf": {signal: SignalText, docWarnings: []ExtractionWarningCode{WarningMissingToUnicode}},
	"encoding/charset-shiftjis.pdf":    {signal: SignalText, docWarnings: []ExtractionWarningCode{WarningFallbackEncoding}},
	"encoding/ucs2-be.pdf":             {signal: SignalText, docWarnings: []ExtractionWarningCode{WarningFallbackEncoding}},
	"encoding/differences-partial.pdf": {signal: SignalText, docWarnings: []ExtractionWarningCode{WarningMissingGlyphMapping}, detailSubstr: "Differences"},
	"encoding/unknown-name.pdf":        {signal: SignalText, docWarnings: []ExtractionWarningCode{WarningUnsupportedEncoding}},
	"encoding/unmapped-glyph.pdf":      {signal: SignalText, hasUnmapped: true}, // silent today
	"geometry/rotated-90.pdf": {
		signal: SignalText, degenerateRun: true, wantRotatedWarning: true,
		notWarnings: []ExtractionWarningCode{WarningVerticalWritingMode},
	},
	"geometry/vertical-cmap.pdf": {
		signal:       SignalText,
		docWarnings:  []ExtractionWarningCode{WarningFallbackEncoding, WarningVerticalWritingMode},
		detailSubstr: "UniJIS-UCS2-V",
		notWarnings:  []ExtractionWarningCode{WarningRotatedText},
	},
	// Same content as rotated-90.pdf but with a page /Rotate 90 that cancels the
	// content rotation back to upright: honoring /Rotate removes the degenerate run
	// and the rotated-text warning. notWarnings[WarningRotatedText] is the contrast
	// lock — without honoring, the rotated content re-fires it and this entry fails.
	"geometry/page-rotate-90.pdf": {
		signal:      SignalText,
		notWarnings: []ExtractionWarningCode{WarningRotatedText, WarningVerticalWritingMode},
	},
}

// assertDecodePath runs DocumentSummary (which classifies and emits/captures the
// document-scoped encoder warnings) and locks the page-1 signal, the expected
// warning codes/Detail, U+FFFD output (if any), and the rotated-run signature (if
// any). It also asserts determinism: a second DocumentSummary on the same Reader
// is identical. The warning/run sub-checks live in helpers so this stays well
// within the gocyclo budget (assertNoGoldenGap's B1 lesson).
func assertDecodePath(t *testing.T, r *Reader, exp decodePathExpect) {
	t.Helper()
	ds1 := r.DocumentSummary()
	ds2 := r.DocumentSummary()
	if !reflect.DeepEqual(ds1, ds2) {
		t.Fatalf("DocumentSummary not deterministic across two calls")
	}
	if len(ds1.Pages) == 0 {
		t.Fatalf("DocumentSummary returned no pages")
	}
	if got := ds1.Pages[0].Signal; got != exp.signal {
		t.Errorf("page-1 signal = %q, want %q", got, exp.signal)
	}
	assertDocWarnings(t, ds1.Warnings, exp)
	if exp.hasUnmapped {
		text, err := r.Page(1).GetPlainText(nil)
		if err != nil {
			t.Fatalf("GetPlainText: %v", err)
		}
		if !strings.ContainsRune(text, '�') {
			t.Errorf("expected U+FFFD (unmapped glyph) in output, got %q", text)
		}
	}
	if exp.degenerateRun {
		assertDegenerateRun(t, r)
	}
	assertRiskWarnings(t, r, exp)
}

// assertRiskWarnings checks the rotation/vertical risk warnings. Rotation is
// detectable only on the content sink, so it runs a Content() pass (idempotent —
// warnings dedup) before reading the union r.Warnings(): wantRotatedWarning
// requires WarningRotatedText present, and every notWarnings code must be absent
// (the cross-contamination guard). It runs the pass whenever either expectation
// is set so the absence check is exercised on the real content path.
func assertRiskWarnings(t *testing.T, r *Reader, exp decodePathExpect) {
	t.Helper()
	if !exp.wantRotatedWarning && len(exp.notWarnings) == 0 {
		return
	}
	_ = r.Page(1).Content() // fire content-path (rotation) warnings into r.warnings
	have := map[ExtractionWarningCode]bool{}
	for _, w := range r.Warnings() {
		have[w.Code] = true
	}
	if exp.wantRotatedWarning && !have[WarningRotatedText] {
		t.Errorf("expected WarningRotatedText after a Content() pass; got %+v", r.Warnings())
	}
	for _, code := range exp.notWarnings {
		if have[code] {
			t.Errorf("unexpected warning %q (cross-contamination); got %+v", code, r.Warnings())
		}
	}
}

// assertDocWarnings checks that every code in exp.docWarnings is present in the
// captured document-scoped warnings, and (when exp.detailSubstr is set) that at
// least one warning's Detail contains it — pinning the SOURCE of a code that more
// than one path can emit.
func assertDocWarnings(t *testing.T, warns []ExtractionWarning, exp decodePathExpect) {
	t.Helper()
	have := map[ExtractionWarningCode]bool{}
	for _, w := range warns {
		have[w.Code] = true
	}
	for _, code := range exp.docWarnings {
		if !have[code] {
			t.Errorf("missing document-scoped warning %q; got %+v", code, warns)
		}
	}
	if exp.detailSubstr == "" {
		return
	}
	for _, w := range warns {
		if strings.Contains(w.Detail, exp.detailSubstr) {
			return
		}
	}
	t.Errorf("no document-scoped warning Detail contains %q; got %+v", exp.detailSubstr, warns)
}

// assertDegenerateRun requires a page-1 text run whose FontSize collapsed to 0
// while still carrying glyphs — the rotated-Tm signature. A normal page carries
// the Tf size (e.g. 12), so this distinguishes a genuinely rotated page from one
// where the Tm was mistyped to identity. Deterministic: Trm[0][0] = 12*0 = 0.
func assertDegenerateRun(t *testing.T, r *Reader) {
	t.Helper()
	for _, tx := range r.Page(1).Content().Text {
		if tx.FontSize == 0 && tx.S != "" {
			return
		}
	}
	t.Errorf("expected a page-1 text run with FontSize==0 (rotated-Tm signature); got %+v",
		r.Page(1).Content().Text)
}

// TestCorpusDecodePathFixtures is the consumer-facing characterization layer for
// the fallback decode-path + geometry fixtures. The fallback encoding framework
// extends decodePathExpectations + assertDecodePath with per-page counters and the
// rotation/vertical warnings.
func TestCorpusDecodePathFixtures(t *testing.T) {
	for _, e := range corpusManifest {
		exp, ok := decodePathExpectations[e.Path]
		if !ok {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			assertDecodePath(t, loadCorpus(t, e), exp)
		})
	}
}

// anchorNoGoldenSignal anchors a no-golden fixture whose page-1 signal is locked by
// a shared expectations map — signals/ → signalExpectations (slice 1), geometry/ →
// decodePathExpectations (slice 2) — so neither can pass vacuously. It fatals if a
// prefixed fixture lacks an entry. Keeping this dispatch out of assertNoGoldenGap
// holds that test's cyclomatic complexity within the gocyclo budget.
func anchorNoGoldenSignal(t *testing.T, e corpusEntry, r *Reader) {
	t.Helper()
	switch {
	case strings.HasPrefix(e.Path, "signals/"):
		exp, ok := signalExpectations[e.Path]
		if !ok {
			t.Fatalf("%s: signals/ no-golden fixture without a signalExpectations entry", e.Path)
		}
		assertPageSignal(t, r, exp)
	case strings.HasPrefix(e.Path, "geometry/"):
		exp, ok := decodePathExpectations[e.Path]
		if !ok {
			t.Fatalf("%s: geometry/ no-golden fixture without a decodePathExpectations entry", e.Path)
		}
		assertDecodePath(t, r, exp)
	}
}
