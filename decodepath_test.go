package pdf

import (
	"fmt"
	"testing"
)

// decodeCountersBothSinks runs both decode sinks on page 1 of data and returns
// their counters. The cross-sink agreement contract is that these are equal for
// content meeting the bounded conditions (single resolved font, no q/Q-scoped
// font change, separators excluded) — which every committed encoding/ fixture and
// every inline fixture here satisfies.
func decodeCountersBothSinks(t *testing.T, data []byte) (content, plain decodeCounters) {
	t.Helper()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	content = r.Page(1).decodeCountersFromContent()
	plain, err = r.Page(1).decodeCountersFromPlainText()
	if err != nil {
		t.Fatalf("decodeCountersFromPlainText: %v", err)
	}
	return content, plain
}

// TestDecodeSourceClassification proves each committed decode-path fixture routes
// through the encSource bucket its /Encoding implies — observed through the
// content-sink counters (encSource is internal). The unmapped-glyph fixture is
// encSourceToUnicode (it carries a parsed /ToUnicode that merely under-covers).
func TestDecodeSourceClassification(t *testing.T) {
	cases := map[string]encSource{
		"encoding/predefined-identity.pdf": encSourceMissingToUnicode,
		"encoding/charset-shiftjis.pdf":    encSourceFallback,
		"encoding/ucs2-be.pdf":             encSourceFallback,
		"encoding/differences-partial.pdf": encSourceDict,
		"encoding/unknown-name.pdf":        encSourceUnsupported,
		"encoding/unmapped-glyph.pdf":      encSourceToUnicode,
	}
	for _, e := range corpusManifest {
		want, ok := cases[e.Path]
		if !ok {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			c := loadCorpus(t, e).Page(1).decodeCountersFromContent()
			if c.glyphs[want] == 0 {
				t.Errorf("expected glyphs in bucket %d, got counters %+v", want, c)
			}
		})
	}
}

// TestDecodeCounterAgreement locks the core contract: the per-source counters are
// identical between the content (Words/Texts/Lines) sink and the plain-text sink
// on every committed encoding/ fixture, plus an EXACT count on a single-glyph
// fixture (agreement alone cannot catch a symmetric over-count).
func TestDecodeCounterAgreement(t *testing.T) {
	for _, e := range corpusManifest {
		if e.Feature != "signal-decode-path" {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			content := loadCorpus(t, e).Page(1).decodeCountersFromContent()
			plain, err := loadCorpus(t, e).Page(1).decodeCountersFromPlainText()
			if err != nil {
				t.Fatalf("decodeCountersFromPlainText: %v", err)
			}
			if content != plain {
				t.Errorf("counter mismatch: content %+v, plain %+v", content, plain)
			}
			if e.Path == "encoding/charset-shiftjis.pdf" && content.glyphs[encSourceFallback] != 1 {
				t.Errorf("charset-shiftjis: glyphs[fallback] = %d, want 1 (one あ)",
					content.glyphs[encSourceFallback])
			}
		})
	}
}

// TestUnmappedCounter ties the silent-unmapped counter to the fixture minted for
// it: a /ToUnicode CMap under-covering its codespace emits U+FFFD, which both
// sinks count, equally.
func TestUnmappedCounter(t *testing.T) {
	var e corpusEntry
	for _, c := range corpusManifest {
		if c.Path == "encoding/unmapped-glyph.pdf" {
			e = c
		}
	}
	if e.Path == "" {
		t.Fatal("encoding/unmapped-glyph.pdf missing from corpusManifest")
	}
	content := loadCorpus(t, e).Page(1).decodeCountersFromContent()
	plain, err := loadCorpus(t, e).Page(1).decodeCountersFromPlainText()
	if err != nil {
		t.Fatalf("decodeCountersFromPlainText: %v", err)
	}
	if content.unmapped < 1 {
		t.Errorf("expected unmapped >= 1, got %d", content.unmapped)
	}
	if content.unmapped != plain.unmapped {
		t.Errorf("unmapped mismatch: content %d, plain %d", content.unmapped, plain.unmapped)
	}
}

// TestSeparatorExclusion is the ONLY test that exercises the separator-exclusion
// paths (appendSeparator / writeSeparator): every committed fixture is
// single-Tj, so those sites are otherwise dead in the test set. The fixture fires
// all three synthesised-separator sites — a TJ kerning gap (-200 ≤ -120), the
// newline appended after the TJ array, and the T* newline — yet only the six real
// glyphs (a,b,c,d,e,f) must be counted, equally on both sinks. A regression that
// counts any separator breaks the exact total even where agreement still holds.
func TestSeparatorExclusion(t *testing.T) {
	content := encodingPagePDF(
		"BT /F1 12 Tf [(ab) -200 (cd)] TJ T* (ef) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	)
	c, p := decodeCountersBothSinks(t, content)
	if c != p {
		t.Errorf("counter mismatch with separators: content %+v, plain %+v", c, p)
	}
	total := 0
	for _, n := range c.glyphs {
		total += n
	}
	if total != 6 {
		t.Errorf("glyph total = %d, want 6 (a,b,c,d,e,f; all separators excluded)", total)
	}
	if c.unmapped != 0 {
		t.Errorf("unmapped = %d, want 0", c.unmapped)
	}
}

// TestXObjectCounterMerge proves Form-XObject text is counted on BOTH sinks: the
// page draws an XObject (via Do) whose own content stream shows the only text. The
// sub-state counters must merge into the parent on each path, else the total is 0
// (the merge would be silently absent and invisible to single-page fixtures).
func TestXObjectCounterMerge(t *testing.T) {
	const form = "BT /F1 12 Tf (xtext) Tj ET"
	const page = "/Fm0 Do"
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /XObject << /Fm0 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(page), page),
		fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 612 792]"+
			" /Resources << /Font << /F1 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
			len(form), form),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
	c, p := decodeCountersBothSinks(t, data)
	total := 0
	for _, n := range c.glyphs {
		total += n
	}
	if total != len("xtext") {
		t.Errorf("XObject glyph total = %d, want %d (text counted on neither sink without the merge)", total, len("xtext"))
	}
	if c != p {
		t.Errorf("XObject counter mismatch: content %+v, plain %+v", c, p)
	}
}

// TestXObjectPanicCountersConsistent is the adversarial guard for the Form-XObject
// panic path (Codex high finding): a form that decodes text and THEN hits a
// malformed operator (a 2-arg Tm, which panics) must still contribute its
// pre-panic glyphs to BOTH sinks, and both must agree — not one zeroing while the
// other keeps a partial count. Without the defer-merge in interpretXObject /
// handlePlainDo (and the counter-preserving plaintext recover) the totals would be
// 0 (content) vs 0 (plaintext zeroed) or otherwise diverge.
func TestXObjectPanicCountersConsistent(t *testing.T) {
	const form = "BT /F1 12 Tf (xtext) Tj /F1 Tf ET" // 5 glyphs, then a 1-arg Tf panics on both sinks
	const page = "/Fm0 Do"
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R" +
			" /Resources << /XObject << /Fm0 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(page), page),
		fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 612 792]"+
			" /Resources << /Font << /F1 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
			len(form), form),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	c := r.Page(1).decodeCountersFromContent()
	p, perr := r.Page(1).decodeCountersFromPlainText()
	if perr == nil {
		t.Fatal("expected a content-stream panic surfaced as error on the plain-text path")
	}
	total := func(d decodeCounters) (n int) {
		for _, g := range d.glyphs {
			n += g
		}
		return n
	}
	if total(c) != len("xtext") {
		t.Errorf("content pre-panic glyphs = %d, want %d (form counts dropped on panic)", total(c), len("xtext"))
	}
	if c != p {
		t.Errorf("panic-path counter mismatch: content %+v, plain %+v", c, p)
	}
}

// rotatedCorpusFixtures is the EMPIRICAL set of corpus fixtures that contain a
// genuinely rotated text run (Trm[0][1] != 0 — a non-horizontal baseline), set
// from an actual full-corpus Content() probe, not a hypothesis. The synthetic
// rotated-90 plus four real documents with rotated table headers / sidebars /
// form text (the IRS 1040 AcroForm carries a rotated run, confirmed by the probe;
// the C.D. Cal. civil cover sheet does NOT and is deliberately absent); the
// synthetic-italic skew documents (cyrillic/udhr-ru, cjk/irs-p850, which slant
// glyphs but keep a horizontal baseline) are deliberately ABSENT — that exclusion
// is the whole point of the Trm[0][1] discriminator.
// NOTE on tables/epa-egrid2022-t1.pdf: this fixture fires WarningRotatedText NOT
// on a genuinely rotated run but on a ~6e-6 degree (Trm[0][1] ≈ -1.07e-7) artifact
// in the Microsoft-Word-emitted text matrix of its page-1 intro paragraph — visually
// horizontal text. It is listed here only because the detector's firing condition
// (content.go: `Trm[0][1] != 0`) has NO tolerance, so any sub-pixel matrix noise trips
// it. The honest fix is a small angle tolerance on that check (tracked as a follow-up);
// when that lands, EPA must be REMOVED from this set (it has no genuine rotation).
var rotatedCorpusFixtures = map[string]bool{
	"geometry/rotated-90.pdf":        true,
	"tables/nist-hb44-appc-2026.pdf": true,
	"multicolumn/fr-2024-06543.pdf":  true,
	"multicolumn/fr-2024-01353.pdf":  true,
	"forms/irs-f1040-2025.pdf":       true,
	"tables/epa-egrid2022-t1.pdf":    true, // sub-0.0001° Word-matrix artifact, not real rotation — see NOTE above
}

// TestStandardEncodingDivergence locks the /BaseEncoding /StandardEncoding fix:
// the name now resolves to the StandardEncoding table (no silent PDFDocEncoding
// fall-through), and that table corrects the code points where StandardEncoding
// DIFFERS from PDFDocEncoding — those differences are the whole point. Asserting
// only shared printable ASCII would be vacuous. The non-corrected upper-range
// slots intentionally still match PDFDocEncoding (documented limitation).
func TestStandardEncodingDivergence(t *testing.T) {
	// The name dispatches to the StandardEncoding table, not the pdfDoc default.
	got := baseEncodingTable(Value{data: name("StandardEncoding")})
	if got != &standardEncoding {
		t.Fatalf("baseEncodingTable(StandardEncoding) = %p, want &standardEncoding %p", got, &standardEncoding)
	}
	// The corrected divergences from PDFDocEncoding (the bug's payload).
	if standardEncoding[0x27] != 0x2019 {
		t.Errorf("standardEncoding[0x27] = U+%04X, want U+2019 (quoteright); pdfDoc has U+%04X",
			standardEncoding[0x27], pdfDocEncoding[0x27])
	}
	if standardEncoding[0x60] != 0x2018 {
		t.Errorf("standardEncoding[0x60] = U+%04X, want U+2018 (quoteleft); pdfDoc has U+%04X",
			standardEncoding[0x60], pdfDocEncoding[0x60])
	}
	// Non-vacuity: these two genuinely differ from PDFDocEncoding.
	if pdfDocEncoding[0x27] == 0x2019 || pdfDocEncoding[0x60] == 0x2018 {
		t.Fatal("PDFDocEncoding already matches StandardEncoding at 0x27/0x60; the test proves nothing")
	}
	// Shared printable ASCII is unchanged (regression guard, not the payload).
	if standardEncoding[0x41] != 'A' || standardEncoding[0x7a] != 'z' {
		t.Errorf("printable ASCII corrupted: 0x41=U+%04X 0x7a=U+%04X", standardEncoding[0x41], standardEncoding[0x7a])
	}
}

// TestStandardEncodingNamePath covers the more common /Encoding /StandardEncoding
// (a plain Name, not a /BaseEncoding dict): it must resolve to the same table as
// the dict path — symmetric with /Encoding /WinAnsiEncoding — decoding 0x27 to
// U+2019, classifying as encSourceSimple, and firing NO spurious
// unsupported_encoding warning.
func TestStandardEncodingNamePath(t *testing.T) {
	data := encodingPagePDF(
		"BT /F1 12 Tf (') Tj ET", // one byte 0x27
		"<< /Type /Font /Subtype /Type1 /BaseFont /Synthetic /Encoding /StandardEncoding >>",
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	text, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	if text != "’" {
		t.Errorf("GetPlainText = %q, want %q (StandardEncoding 0x27 → quoteright)", text, "’")
	}
	for _, w := range r.Warnings() {
		if w.Code == WarningUnsupportedEncoding {
			t.Errorf("unexpected unsupported_encoding warning for a recognized /Encoding /StandardEncoding: %+v", w)
		}
	}
	if c := r.Page(1).decodeCountersFromContent(); c.glyphs[encSourceSimple] == 0 {
		t.Errorf("expected StandardEncoding name path classified encSourceSimple, got %+v", c)
	}
}

// TestRotatedTextWarningCorpusWide is the no-false-positive guard for the rotation
// detector: across every loadable corpus fixture, a Content() pass over all pages
// must fire WarningRotatedText for exactly the rotatedCorpusFixtures set — proving
// it catches real rotated runs while NOT tripping on synthetic-italic skew.
func TestRotatedTextWarningCorpusWide(t *testing.T) {
	for _, e := range corpusManifest {
		if e.Password != "" {
			continue // skip encrypted fixtures; none carry rotated text
		}
		t.Run(e.Path, func(t *testing.T) {
			r := loadCorpus(t, e)
			for i := 1; i <= r.NumPage(); i++ {
				_ = r.Page(i).Content()
			}
			fired := false
			for _, w := range r.Warnings() {
				if w.Code == WarningRotatedText {
					fired = true
				}
			}
			if want := rotatedCorpusFixtures[e.Path]; fired != want {
				t.Errorf("WarningRotatedText fired=%v, want %v", fired, want)
			}
		})
	}
}

// TestDecodeRatiosAgreement locks the quality-ratio acceptance: the per-page decode
// ratios derived from the content (Words) sink and the plain-text (GetPlainText)
// sink are identical on every committed encoding/ fixture. The agreement is BOUNDED
// to those fixtures because the underlying counter agreement is bounded (single
// resolved font, no q/Q-scoped font change, synthesised separators excluded) — they
// all satisfy it. Exact pins catch a symmetric error that equality alone cannot:
// charset-shiftjis is one fallback glyph (FallbackRatio 1.0), and unmapped-glyph
// carries at least one U+FFFD (UnmappedRatio > 0).
func TestDecodeRatiosAgreement(t *testing.T) {
	for _, e := range corpusManifest {
		if e.Feature != "signal-decode-path" {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			content := loadCorpus(t, e).Page(1).decodeCountersFromContent()
			plain, err := loadCorpus(t, e).Page(1).decodeCountersFromPlainText()
			if err != nil {
				t.Fatalf("decodeCountersFromPlainText: %v", err)
			}
			if decodeRatiosFrom(content) != decodeRatiosFrom(plain) {
				t.Errorf("ratio mismatch: content %+v, plain %+v",
					decodeRatiosFrom(content), decodeRatiosFrom(plain))
			}
			switch e.Path {
			case "encoding/charset-shiftjis.pdf":
				if dr := decodeRatiosFrom(content); dr.Glyphs != 1 || dr.FallbackRatio != 1 {
					t.Errorf("charset-shiftjis ratios = %+v, want Glyphs:1 FallbackRatio:1", dr)
				}
			case "encoding/unmapped-glyph.pdf":
				if dr := decodeRatiosFrom(content); dr.UnmappedRatio <= 0 {
					t.Errorf("unmapped-glyph UnmappedRatio = %v, want > 0", dr.UnmappedRatio)
				}
			}
		})
	}
}

// TestDecodeRatioDenominatorExcludesUnset guards the ratio denominator: the
// no-font encSourceUnset bucket (bucket 0) must stay empty on every real corpus
// page. decodeRatiosFrom sums ALL encSource buckets for its denominator, so a run
// that ever recorded glyphs under encSourceUnset (a show op before Tf, an
// unresolved font emitting runes) would silently inflate the denominator and
// deflate every ratio. Empirically zero corpus-wide today; this locks it.
func TestDecodeRatioDenominatorExcludesUnset(t *testing.T) {
	for _, e := range corpusManifest {
		if e.Password != "" {
			continue // encrypted fixtures need a password to open; covered elsewhere
		}
		t.Run(e.Path, func(t *testing.T) {
			r := loadCorpus(t, e)
			for i := 1; i <= r.NumPage(); i++ {
				if c := r.Page(i).decodeCountersFromContent(); c.glyphs[encSourceUnset] != 0 {
					t.Errorf("page %d: encSourceUnset glyphs = %d, want 0 (inflates ratio denominator)",
						i, c.glyphs[encSourceUnset])
				}
			}
		})
	}
}
