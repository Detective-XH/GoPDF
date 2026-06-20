package pdf

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Store unit tests
// ---------------------------------------------------------------------------

// TestWarningStoreDedupAndOrder — set semantics and the (Page, Code, Detail)
// total order; an empty store snapshots to nil, not an empty slice.
func TestWarningStoreDedupAndOrder(t *testing.T) {
	var w warningStore
	if got := w.snapshot(); got != nil {
		t.Fatalf("empty store: want nil, got %v", got)
	}
	dup := ExtractionWarning{Code: WarningFallbackEncoding, Detail: "b"}
	w.add(dup)
	w.add(dup) // duplicate: must not grow the set
	w.add(ExtractionWarning{Code: WarningFallbackEncoding, Detail: "a"})
	w.add(ExtractionWarning{Page: 2, Code: WarningMissingToUnicode, Detail: "x"})
	w.add(ExtractionWarning{Code: WarningMissingToUnicode, Detail: "y"})

	got := w.snapshot()
	want := []ExtractionWarning{
		{Code: WarningFallbackEncoding, Detail: "a"},
		{Code: WarningFallbackEncoding, Detail: "b"},
		{Code: WarningMissingToUnicode, Detail: "y"},
		{Page: 2, Code: WarningMissingToUnicode, Detail: "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot order/dedup:\n got %v\nwant %v", got, want)
	}
}

// TestWarningStoreCap — the retained total is EXACTLY maxStoredWarnings
// (4095 normal + the warnings_truncated sentinel), never cap+1; duplicates of
// retained warnings stay no-ops after truncation; further distinct adds drop.
func TestWarningStoreCap(t *testing.T) {
	var w warningStore
	for i := 0; i < maxStoredWarnings+10; i++ {
		w.add(ExtractionWarning{Code: WarningUnsupportedEncoding, Detail: strconv.Itoa(i)})
	}
	got := w.snapshot()
	if len(got) != maxStoredWarnings {
		t.Fatalf("store size after overflow: got %d, want exactly %d", len(got), maxStoredWarnings)
	}
	sentinels := 0
	for _, warn := range got {
		if warn.Code == WarningTruncated {
			sentinels++
		}
	}
	if sentinels != 1 {
		t.Errorf("sentinel count: got %d, want 1", sentinels)
	}
	// A duplicate of an already-retained warning is still a no-op.
	w.add(ExtractionWarning{Code: WarningUnsupportedEncoding, Detail: "0"})
	if n := len(w.snapshot()); n != maxStoredWarnings {
		t.Errorf("size after retained-duplicate add: got %d, want %d", n, maxStoredWarnings)
	}
	// A NEW distinct warning past truncation does not grow the store.
	w.add(ExtractionWarning{Code: WarningUnsupportedEncoding, Detail: "brand-new"})
	if n := len(w.snapshot()); n != maxStoredWarnings {
		t.Errorf("size after post-truncation distinct add: got %d, want %d", n, maxStoredWarnings)
	}
}

// TestWarningStoreCapConcurrent — the invariants that hold past the cap
// REGARDLESS of interleaving: exact size, exactly one sentinel, every
// retained entry from the emitted universe, no growth on a second round.
// Run under -race.
func TestWarningStoreCapConcurrent(t *testing.T) {
	var w warningStore
	workers := runtime.GOMAXPROCS(0)
	total := 3 * maxStoredWarnings
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < total; i += workers {
				w.add(ExtractionWarning{Code: WarningUnsupportedEncoding, Detail: strconv.Itoa(i)})
			}
		}(g)
	}
	wg.Wait()

	got := w.snapshot()
	if len(got) != maxStoredWarnings {
		t.Fatalf("concurrent overflow size: got %d, want exactly %d", len(got), maxStoredWarnings)
	}
	sentinels := 0
	for _, warn := range got {
		if warn.Code == WarningTruncated {
			sentinels++
			continue
		}
		n, err := strconv.Atoi(warn.Detail)
		if err != nil || n < 0 || n >= total {
			t.Errorf("retained warning outside emitted universe: %+v", warn)
		}
	}
	if sentinels != 1 {
		t.Errorf("sentinel count: got %d, want 1", sentinels)
	}
	w.add(ExtractionWarning{Code: WarningUnsupportedEncoding, Detail: "second-round"})
	if n := len(w.snapshot()); n != maxStoredWarnings {
		t.Errorf("second round grew the store: got %d, want %d", n, maxStoredWarnings)
	}
}

// TestWarningMessagesFixed — the structural lock for the dedup-key rule:
// every exported code has a fixed non-empty message, and warn() fills Message
// from the table (it accepts no message parameter by design).
func TestWarningMessagesFixed(t *testing.T) {
	codes := []ExtractionWarningCode{
		WarningMissingToUnicode,
		WarningFallbackEncoding,
		WarningUnsupportedEncoding,
		WarningMissingGlyphMapping,
		WarningUnsupportedFilter,
		WarningTruncated,
	}
	for _, c := range codes {
		if warningMessages[c] == "" {
			t.Errorf("code %q has no fixed message", c)
		}
	}
	r, err := OpenBytes(buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	}))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	r.warn(3, WarningUnsupportedFilter, "d")
	ws := r.Warnings()
	if len(ws) != 1 {
		t.Fatalf("Warnings: got %d entries, want 1", len(ws))
	}
	want := ExtractionWarning{
		Page:    3,
		Code:    WarningUnsupportedFilter,
		Message: warningMessages[WarningUnsupportedFilter],
		Detail:  "d",
	}
	if ws[0] != want {
		t.Errorf("warn() result: got %+v, want %+v", ws[0], want)
	}
}

// ---------------------------------------------------------------------------
// Emission tests (synthetic fixtures pin exact codes)
// ---------------------------------------------------------------------------

// singleFontPDF builds a one-page document with content and object 5 as the
// /F1 font body (additional objects appended after the font).
func singleFontPDF(content, fontBody string, extra ...string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		fontBody,
	}
	return append([]byte(nil), buildPDFFromObjects(append(objs, extra...))...)
}

func mustOpen(t *testing.T, data []byte) *Reader {
	t.Helper()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	return r
}

func pageText(t *testing.T, r *Reader) string {
	t.Helper()
	text, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	return text
}

// assertOneWarning asserts the Reader holds exactly one warning with the
// given code whose Detail contains detailSub.
func assertOneWarning(t *testing.T, r *Reader, code ExtractionWarningCode, detailSub string) {
	t.Helper()
	ws := r.Warnings()
	if len(ws) != 1 {
		t.Fatalf("Warnings: got %d entries (%v), want 1", len(ws), ws)
	}
	w := ws[0]
	if w.Code != code || !strings.Contains(w.Detail, detailSub) {
		t.Errorf("warning: got {Code:%s Detail:%q}, want Code %s with Detail containing %q",
			w.Code, w.Detail, code, detailSub)
	}
	if w.Page != 0 {
		t.Errorf("warning Page: got %d, want 0 (document-scoped)", w.Page)
	}
	if w.Message != warningMessages[code] {
		t.Errorf("warning Message: got %q, want the fixed table entry %q", w.Message, warningMessages[code])
	}
}

// TestWarningsMissingToUnicodeParseFail — a present-but-unparseable ToUnicode
// CMap ("endbfchar" with no begin sets ok=false; token garbage would instead
// yield an empty cmap) warns AND text still extracts via the fallback.
func TestWarningsMissingToUnicodeParseFail(t *testing.T) {
	badTU := "endbfchar"
	r := mustOpen(t, singleFontPDF(
		"BT /F1 12 Tf (Hi) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /BadTU /ToUnicode 6 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(badTU), badTU),
	))
	if got := pageText(t, r); got != "Hi" {
		t.Errorf("text: got %q, want %q (extraction must continue)", got, "Hi")
	}
	assertOneWarning(t, r, WarningMissingToUnicode, "BadTU")
}

// TestWarningsIdentityH — an Identity CMap with no usable ToUnicode is the
// classic missing-ToUnicode case. NOTE: a fixture combining a BROKEN
// ToUnicode with an Identity encoding legitimately emits TWO entries with
// distinct Details — never assert "exactly one" on such a combination.
func TestWarningsIdentityH(t *testing.T) {
	r := mustOpen(t, singleFontPDF(
		"BT /F1 12 Tf (abc) Tj ET",
		"<< /Type /Font /Subtype /Type0 /BaseFont /IdentityFont /Encoding /Identity-H >>",
	))
	_ = pageText(t, r) // must not panic; byte-level output is not asserted
	assertOneWarning(t, r, WarningMissingToUnicode, "Identity CMap")
}

// TestWarningsFallbackCJK — a predefined CJK CMap decoded via charset
// approximation warns; the SAME encoding with a valid ToUnicode emits
// NOTHING (ToUnicode governs — the noise gate for well-formed CJK).
func TestWarningsFallbackCJK(t *testing.T) {
	// テスト in Shift-JIS via 90ms-RKSJ-H.
	r := mustOpen(t, singleFontPDF(
		"BT /F1 12 Tf <836583588367> Tj ET",
		"<< /Type /Font /Subtype /Type0 /BaseFont /JPTest /Encoding /90ms-RKSJ-H >>",
	))
	if got := pageText(t, r); got != "テスト" {
		t.Errorf("ShiftJIS text: got %q, want %q", got, "テスト")
	}
	assertOneWarning(t, r, WarningFallbackEncoding, "90ms-RKSJ-H")

	// Control: valid ToUnicode mapping 0x41 → あ; no warnings at all.
	cm := buildBfcharCmap(0x41, 'あ')
	rc := mustOpen(t, singleFontPDF(
		"BT /F1 12 Tf <41> Tj ET",
		"<< /Type /Font /Subtype /Type0 /BaseFont /JPTest2 /Encoding /90ms-RKSJ-H /ToUnicode 6 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cm), cm),
	))
	if got := pageText(t, rc); got != "あ" {
		t.Errorf("ToUnicode text: got %q, want %q", got, "あ")
	}
	if ws := rc.Warnings(); ws != nil {
		t.Errorf("valid-ToUnicode control: want no warnings, got %v", ws)
	}
}

// TestWarningsUnsupportedEncodingName — unknown encoding name falls back to
// PDFDocEncoding (text still extracts) and warns.
func TestWarningsUnsupportedEncodingName(t *testing.T) {
	r := mustOpen(t, singleFontPDF(
		"BT /F1 12 Tf (Hello) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /BogusEnc /Encoding /Bogus-Enc >>",
	))
	if got := pageText(t, r); got != "Hello" {
		t.Errorf("text: got %q, want %q", got, "Hello")
	}
	assertOneWarning(t, r, WarningUnsupportedEncoding, "Bogus-Enc")
}

// TestWarningsDifferencesUnknownGlyph — unmappable /Differences entries
// (unknown glyph names AND out-of-range code slots) warn with a
// deterministic count; a known in-range name is silent.
func TestWarningsDifferencesUnknownGlyph(t *testing.T) {
	t.Run("unknown_name", func(t *testing.T) {
		r := mustOpen(t, singleFontPDF(
			"BT /F1 12 Tf (A) Tj ET",
			"<< /Type /Font /Subtype /Type1 /BaseFont /DiffFont /Encoding << /Differences [65 /nosuchglyphname] >> >>",
		))
		if got := pageText(t, r); got != "A" {
			t.Errorf("text: got %q, want %q (base table entry must survive)", got, "A")
		}
		assertOneWarning(t, r, WarningMissingGlyphMapping, "1 unmappable glyph entries")
	})
	t.Run("out_of_range_code", func(t *testing.T) {
		r := mustOpen(t, singleFontPDF(
			"BT /F1 12 Tf (A) Tj ET",
			"<< /Type /Font /Subtype /Type1 /BaseFont /DiffFont2 /Encoding << /Differences [300 /sterling] >> >>",
		))
		_ = pageText(t, r)
		assertOneWarning(t, r, WarningMissingGlyphMapping, "1 unmappable glyph entries")
	})
	t.Run("known_name_control", func(t *testing.T) {
		r := mustOpen(t, singleFontPDF(
			"BT /F1 12 Tf (B) Tj ET",
			"<< /Type /Font /Subtype /Type1 /BaseFont /DiffFont3 /Encoding << /Differences [66 /sterling] >> >>",
		))
		if got := pageText(t, r); got != "£" {
			t.Errorf("text: got %q, want %q (Differences must still apply)", got, "£")
		}
		if ws := r.Warnings(); ws != nil {
			t.Errorf("known-name control: want no warnings, got %v", ws)
		}
	})
}

// TestWarningsMissingFontResource — a Tf naming a font absent from the page
// resources warns once (all three walkers emit the same warning; dedup
// collapses them) and extraction continues via the nopEncoder/PDFDoc path.
func TestWarningsMissingFontResource(t *testing.T) {
	r := mustOpen(t, singleFontPDF(
		"BT /F9 12 Tf (x) Tj ET",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	))
	p := r.Page(1)
	_ = p.Content()                                // content.go walker
	if _, err := p.GetPlainText(nil); err != nil { // plaintext.go walker
		t.Fatalf("GetPlainText: %v", err)
	}
	if _, err := p.GetTextByRow(); err != nil { // walk.go walker
		t.Fatalf("GetTextByRow: %v", err)
	}
	assertOneWarning(t, r, WarningMissingGlyphMapping, "F9")
}

// TestWarningsUnsupportedFilterCrypt — an unsupported stream filter on a
// content stream degrades to empty extraction exactly as before
// (regression-pinned) AND now warns. The array variant locks errors.Is
// surviving applyArrayFilters and filterDetail's array branch.
func TestWarningsUnsupportedFilterCrypt(t *testing.T) {
	build := func(filter string) []byte {
		return buildPDFFromObjects([]string{
			"<< /Type /Catalog /Pages 2 0 R >>",
			"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
			"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
			fmt.Sprintf("<< /Length 4 /Filter %s >>\nstream\nABCD\nendstream", filter),
		})
	}
	for _, tc := range []struct{ name, filter string }{
		{"name_filter", "/Crypt"},
		{"array_filter", "[/Crypt]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mustOpen(t, build(tc.filter))
			out := r.Page(1).Content()
			if len(out.Text) != 0 {
				t.Errorf("Content text: got %d runs, want 0 (existing degradation)", len(out.Text))
			}
			assertOneWarning(t, r, WarningUnsupportedFilter, "filter Crypt")
		})
	}
}

// TestClampDetail — the Detail size guard: short strings pass through
// untouched, long strings are cut at a UTF-8 rune boundary and cloned (the
// retained key must not pin the original backing array).
func TestClampDetail(t *testing.T) {
	if got := clampDetail("short"); got != "short" {
		t.Errorf("short passthrough: got %q", got)
	}
	long := strings.Repeat("a", maxWarningDetailLen+100)
	got := clampDetail(long)
	if len(got) != maxWarningDetailLen+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("ascii clamp: len=%d suffix=%q", len(got), got[len(got)-3:])
	}
	// Multi-byte boundary: fill so the cut lands mid-rune; the result must
	// remain valid UTF-8.
	cjk := strings.Repeat("中", maxWarningDetailLen) // 3 bytes per rune
	got = clampDetail(cjk)
	if len(got) > maxWarningDetailLen+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("cjk clamp: len=%d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("cjk clamp produced invalid UTF-8: %q", got)
	}
}

// TestWarningsAdversarialSizes — Codex gate finding: attacker-sized names
// must not produce attacker-sized warnings. A ~64 KiB /BaseFont and /Encoding
// name and a 1000-element /Filter array each yield a warning whose Detail is
// clamped; total retained bytes stay bounded.
func TestWarningsAdversarialSizes(t *testing.T) {
	t.Run("huge_basefont_and_encoding", func(t *testing.T) {
		huge := strings.Repeat("A", 64<<10)
		r := mustOpen(t, singleFontPDF(
			"BT /F1 12 Tf (x) Tj ET",
			"<< /Type /Font /Subtype /Type1 /BaseFont /"+huge+" /Encoding /"+huge+"Enc >>",
		))
		_ = pageText(t, r)
		ws := r.Warnings()
		if len(ws) != 1 {
			t.Fatalf("want 1 warning, got %d", len(ws))
		}
		if len(ws[0].Detail) > maxWarningDetailLen+len("...") {
			t.Errorf("Detail not clamped: %d bytes", len(ws[0].Detail))
		}
	})
	t.Run("huge_filter_array", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 1000; i++ {
			sb.WriteString("/Crypt ")
		}
		r := mustOpen(t, buildPDFFromObjects([]string{
			"<< /Type /Catalog /Pages 2 0 R >>",
			"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
			"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
			fmt.Sprintf("<< /Length 4 /Filter [%s] >>\nstream\nABCD\nendstream", sb.String()),
		}))
		_ = r.Page(1).Content()
		ws := r.Warnings()
		if len(ws) != 1 {
			t.Fatalf("want 1 warning, got %d", len(ws))
		}
		if len(ws[0].Detail) > maxWarningDetailLen+len("...") {
			t.Errorf("Detail not clamped: %d bytes (%q...)", len(ws[0].Detail), ws[0].Detail[:80])
		}
		if !strings.HasSuffix(ws[0].Detail, "+...") {
			t.Errorf("array truncation marker missing: %q", ws[0].Detail)
		}
	})
}

// ---------------------------------------------------------------------------
// Noise gate, determinism, concurrency
// ---------------------------------------------------------------------------

// extractAll runs the full extraction surface without failing the test from
// non-test goroutines: reader-level text plus per-page Content and
// ExtractionSummary (the summary is part of the charter — it may emit
// page-scoped warnings, which the noise gate below must also cover).
func extractAll(r *Reader) {
	rd, err := r.GetPlainText(context.Background())
	if err == nil {
		_, _ = io.Copy(io.Discard, rd)
	}
	n := r.NumPage()
	for i := 1; i <= n; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		_ = p.Content()
		_, _ = p.ExtractionSummary()
	}
}

// TestWarningsCleanDocsNone — THE NOISE GATE: clean corpus fixtures (simple
// fonts, Null encodings) and image-bearing pages produce ZERO warnings;
// extraction never reads image streams.
func TestWarningsCleanDocsNone(t *testing.T) {
	for _, rel := range []string{"plaintext/hello-ascii.pdf", "styled/multifont.pdf"} {
		data, err := os.ReadFile(corpusPath(rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		r := mustOpen(t, data)
		extractAll(r)
		if ws := r.Warnings(); ws != nil {
			t.Errorf("%s: want no warnings, got %v", rel, ws)
		}
	}
	// Image XObject page (DCT-free here, but the point holds for any image:
	// extraction skips image streams entirely, so no filter warnings fire).
	// PLAIN extraction — GetPlainText + Content, no summary — must stay
	// silent; the summary's image_only_page is the intended signal, not
	// noise, and is asserted separately.
	pageContent := "/Img0 Do"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Filter /DCTDecode /Length 0 >>\nstream\nendstream",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
	}))
	if rd, err := r.GetPlainText(context.Background()); err == nil {
		_, _ = io.Copy(io.Discard, rd)
	}
	_ = r.Page(1).Content()
	if ws := r.Warnings(); ws != nil {
		t.Errorf("image-only page, plain extraction: want no warnings, got %v", ws)
	}
	// The summary classifies it — exactly one image_only_page.
	sum, err := r.Page(1).ExtractionSummary()
	if err != nil {
		t.Fatalf("ExtractionSummary: %v", err)
	}
	if len(sum.Warnings) != 1 || sum.Warnings[0].Code != WarningImageOnlyPage {
		t.Errorf("image-only page, summary: got %v, want exactly one image_only_page", sum.Warnings)
	}
}

// TestWarningsDeterministic — for every corpus fixture: same-Reader repeat
// and fresh-Reader repeat yield identical warning snapshots. (Does not pin
// WHICH warnings real corpus files emit — synthetic tests above pin codes.)
func TestWarningsDeterministic(t *testing.T) {
	t.Parallel()
	for _, e := range corpusManifest {
		t.Run(e.Path, func(t *testing.T) {
			t.Parallel()
			r1 := loadCorpus(t, e)
			extractAll(r1)
			w1 := r1.Warnings()
			extractAll(r1)
			w2 := r1.Warnings()
			r2 := loadCorpus(t, e)
			extractAll(r2)
			w3 := r2.Warnings()
			if !reflect.DeepEqual(w1, w2) {
				t.Errorf("same-Reader repeat diverged:\n first %v\nsecond %v", w1, w2)
			}
			if !reflect.DeepEqual(w1, w3) {
				t.Errorf("fresh-Reader repeat diverged:\n first %v\n fresh %v", w1, w3)
			}
		})
	}
}

// TestWarningsConcurrent — the dedup-under-contention test on a fixture that
// emits 4 DISTINCT warnings: every goroutine's post-pass snapshot equals the
// single-goroutine baseline (its own full pass guarantees the complete set
// regardless of the other goroutines). Run under -race.
func TestWarningsConcurrent(t *testing.T) {
	t.Parallel()
	base := mustOpen(t, buildWarningEmittingBenchPDF())
	extractAll(base)
	want := base.Warnings()
	if len(want) < 4 {
		t.Fatalf("fixture must emit >= 4 distinct warnings, got %v", want)
	}

	shared := mustOpen(t, buildWarningEmittingBenchPDF())
	var wg sync.WaitGroup
	for g := 0; g < runtime.GOMAXPROCS(0); g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			extractAll(shared)
			if got := shared.Warnings(); !reflect.DeepEqual(got, want) {
				t.Errorf("worker %d: warnings diverged from baseline:\n got %v\nwant %v", id, got, want)
			}
		}(g)
	}
	wg.Wait()
}
