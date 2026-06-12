package pdf

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestCachedEncoderParsesOnce proves (*Font).cachedEncoder memoizes the parsed
// encoder, so a ToUnicode CMap is parsed once rather than on every Tf. With the
// document-level encoder cache (Tier 2) getEncoder is itself memoized on the
// Reader, so the non-vacuity guard parses via raw readCmap (which always yields
// a fresh *cmap) rather than getEncoder.
func TestCachedEncoderParsesOnce(t *testing.T) {
	data, err := os.ReadFile("testdata/corpus/cjk/irs-p850-zh-hant.pdf")
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	var f Font
	found := false
	for i := 1; i <= r.NumPage() && !found; i++ {
		p := r.Page(i)
		for _, name := range p.Fonts() {
			if cand := p.Font(name); cand.V.Key("ToUnicode").Kind() == Stream {
				f, found = cand, true
				break
			}
		}
	}
	if !found {
		t.Skip("fixture has no /ToUnicode-stream font; cannot prove caching")
	}
	// Non-vacuity guard: the underlying parse yields DISTINCT instances (a
	// /ToUnicode stream, not a predefined-CMap singleton), so the assertions
	// below are not trivially true. readCmap bypasses both cache tiers.
	m1, m2 := readCmap(f.V.Key("ToUnicode")), readCmap(f.V.Key("ToUnicode"))
	if m1 == nil || m1 == m2 {
		t.Skip("font uses a singleton/unparseable encoder; caching test would be vacuous")
	}
	c1, _ := f.cachedEncoder()
	if f.enc == nil {
		t.Fatal("cachedEncoder() did not populate the L1 memo (f.enc)")
	}
	// Isolate L1 from the document-level (Tier 2) cache: clear the Reader's
	// encoder cache so that re-entering getEncoder would parse a DIFFERENT *cmap
	// pointer. A correct cachedEncoder returns the memoized f.enc WITHOUT
	// re-entering getEncoder, so the pointer is unchanged; a broken L1 that
	// re-parsed every call would return the new pointer and fail here.
	r.encoders.mu.Lock()
	r.encoders.m = nil
	r.encoders.bytes = 0
	r.encoders.mu.Unlock()
	c2, _ := f.cachedEncoder()
	if c1 != c2 {
		t.Fatal("cachedEncoder() re-parsed the CMap; L1 memo ineffective")
	}
}

// cjkFontsMap builds the page-1 font map of the Traditional Chinese fixture,
// shared by the read-only and concurrency regression tests below.
func cjkFontsMap(t *testing.T) (Page, map[string]*Font) {
	t.Helper()
	data, err := os.ReadFile("testdata/corpus/cjk/irs-p850-zh-hant.pdf")
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	p := r.Page(1)
	fonts := make(map[string]*Font)
	for _, name := range p.Fonts() {
		f := p.Font(name)
		fonts[name] = &f
	}
	if len(fonts) == 0 {
		t.Skip("fixture page 1 has no fonts")
	}
	return p, fonts
}

// TestGetPlainTextKeepsCallerFontsReadOnly verifies that GetPlainText does not
// cache encoders onto a caller-supplied fonts map: the map's *Font.enc fields
// must remain nil afterwards. This is the property that makes a shared map safe;
// before the copy-into-local fix, GetPlainText wrote enc on the caller's *Font.
func TestGetPlainTextKeepsCallerFontsReadOnly(t *testing.T) {
	p, fonts := cjkFontsMap(t)
	if _, err := p.GetPlainText(fonts); err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	for name, f := range fonts {
		if f.enc != nil {
			t.Errorf("GetPlainText mutated caller font %q (enc set); map is not read-only", name)
		}
	}
}

// TestGetPlainTextSharedFontsMapConcurrent stresses GetPlainText with a single
// caller-supplied fonts map shared across goroutines (each with its own Page).
// Run under -race: before the copy-into-local fix this wrote Font.enc on the
// shared *Font concurrently and tripped the race detector.
func TestGetPlainTextSharedFontsMapConcurrent(t *testing.T) {
	_, shared := cjkFontsMap(t)
	data, err := os.ReadFile("testdata/corpus/cjk/irs-p850-zh-hant.pdf")
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Page(1).GetPlainText(shared); err != nil {
				t.Errorf("GetPlainText: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestEncoderCacheSharesAcrossFontValues proves the Tier 2 mechanism: two
// DISTINCT *Font values over the same ToUnicode stream (as different pages
// produce) share ONE parsed *cmap via the Reader's encoder cache. L1 cannot
// explain this — each Font value has its own nil enc field — so an equal pointer
// isolates the document-level (L2) collapse that removes the per-page re-parse.
func TestEncoderCacheSharesAcrossFontValues(t *testing.T) {
	data, err := os.ReadFile("testdata/corpus/cjk/irs-p850-zh-hant.pdf")
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	var p Page
	var name string
	found := false
	for i := 1; i <= r.NumPage() && !found; i++ {
		p = r.Page(i)
		for _, n := range p.Fonts() {
			if p.Font(n).V.Key("ToUnicode").Kind() == Stream {
				name, found = n, true
				break
			}
		}
	}
	if !found {
		t.Skip("fixture has no /ToUnicode-stream font")
	}
	// Non-vacuity: raw readCmap yields distinct instances, so an equal pointer
	// below can only come from the shared cache, not a singleton encoder.
	if m1, m2 := readCmap(p.Font(name).V.Key("ToUnicode")), readCmap(p.Font(name).V.Key("ToUnicode")); m1 == nil || m1 == m2 {
		t.Skip("font uses a singleton/unparseable encoder; sharing test would be vacuous")
	}
	f1, f2 := p.Font(name), p.Font(name) // two distinct Font values, both enc==nil
	e1, _ := f1.getEncoder()
	e2, _ := f2.getEncoder()
	if e1 != e2 {
		t.Fatalf("encoder cache did not share the CMap across Font values: %p != %p", e1, e2)
	}
}

// cmapProbeCodes returns every source code a cmap maps (each bfchar orig and
// each bfrange lo), so decode equivalence is checked over the CMap's own domain.
func cmapProbeCodes(m *cmap) []string {
	var out []string
	for _, bc := range m.bfchar {
		out = append(out, bc.orig)
	}
	for n := range m.bfrange {
		for _, br := range m.bfrange[n] {
			out = append(out, br.lo)
		}
	}
	return out
}

// TestEncoderCacheMatchesUncached locks the Tier 2 invariant: the cache changes
// WHEN a ToUnicode CMap is parsed, never WHAT it decodes to. For every
// /ToUnicode-stream font across the whole golden corpus, the *cmap the Reader's
// encoder cache returns must decode byte-for-byte identically to a fresh,
// uncached readCmap, and a second lookup must return the SAME pointer (a hit,
// not a re-parse).
func TestEncoderCacheMatchesUncached(t *testing.T) {
	for _, e := range corpusManifest {
		t.Run(e.Path, func(t *testing.T) {
			r := loadCorpus(t, e)
			seen := map[objptr]bool{}
			probed := 0
			for i := 1; i <= r.NumPage(); i++ {
				p := r.Page(i)
				for _, name := range p.Fonts() {
					tu := p.Font(name).V.Key("ToUnicode")
					if tu.Kind() != Stream || tu.ptr == (objptr{}) || seen[tu.ptr] {
						continue
					}
					seen[tu.ptr] = true
					fresh := readCmap(tu) // uncached reference parse
					if fresh == nil {
						continue
					}
					cached := r.encoders.lookup(tu.ptr, tu)
					if cached == nil {
						t.Fatalf("page %d font %s: cache returned nil for a parseable ToUnicode", i, name)
					}
					if again := r.encoders.lookup(tu.ptr, tu); again != cached {
						t.Errorf("page %d font %s: second lookup re-parsed (%p != %p)", i, name, again, cached)
					}
					for _, probe := range cmapProbeCodes(fresh) {
						if got, want := cached.Decode(probe), fresh.Decode(probe); got != want {
							t.Errorf("page %d font %s code %x: cached %q != uncached %q", i, name, probe, got, want)
						}
					}
					probed++
				}
			}
			// Surface the multi-key reality: if a corpus entry has only ONE
			// distinct ToUnicode stream, the cache holds one entry and the
			// multi-key/eviction paths ride on the synthetic test below instead.
			t.Logf("%s: %d distinct ToUnicode stream(s) probed", e.Path, len(seen))
			if probed == 0 {
				t.Skip("no /ToUnicode-stream fonts in this corpus entry")
			}
		})
	}
}

// buildInlineFontAliasPDF builds a one-page PDF whose two fonts are INLINE dicts
// in the page Resources — so both inherit the page object's ptr and ALIAS on one
// objptr — yet reference DISTINCT indirect ToUnicode streams mapping the same
// code <0001> to different runes (U+4E00 vs U+4E8C). Font-dict-ptr keying
// collides them; ToUnicode-stream-ptr keying keeps them apart.
func buildInlineFontAliasPDF() []byte {
	cmap := func(hexRune string) string {
		body := "1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n" +
			"1 beginbfchar\n<0001> <" + hexRune + ">\nendbfchar"
		return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body)
	}
	content := "BT /F1 12 Tf <0001> Tj /F2 12 Tf <0001> Tj ET"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << " +
			"/F1 << /Type /Font /Subtype /Type0 /BaseFont /Foo /Encoding /Identity-H /ToUnicode 5 0 R >> " +
			"/F2 << /Type /Font /Subtype /Type0 /BaseFont /Bar /Encoding /Identity-H /ToUnicode 6 0 R >> " +
			">> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		cmap("4E00"), // object 5 — F1's ToUnicode: code 0001 -> U+4E00
		cmap("4E8C"), // object 6 — F2's ToUnicode: code 0001 -> U+4E8C
	})
}

// TestEncoderCacheInlineFontDistinctToUnicode locks the cache-key choice. The two
// inline font dicts alias on one objptr (keying on it WOULD serve one font the
// other's CMap — silent wrong glyphs), while their ToUnicode streams keep
// distinct ptrs. A correct key decodes each font to its OWN rune.
func TestEncoderCacheInlineFontDistinctToUnicode(t *testing.T) {
	r, err := OpenBytes(buildInlineFontAliasPDF())
	if err != nil {
		t.Fatal(err)
	}
	p := r.Page(1)
	f1, f2 := p.Font("F1"), p.Font("F2")
	if f1.V.ptr != f2.V.ptr {
		t.Fatalf("inline font dicts have distinct ptrs (%v vs %v); test no longer exercises aliasing", f1.V.ptr, f2.V.ptr)
	}
	if tu1, tu2 := f1.V.Key("ToUnicode"), f2.V.Key("ToUnicode"); tu1.ptr == tu2.ptr {
		t.Fatalf("ToUnicode streams share ptr %v; fixture is wrong", tu1.ptr)
	}
	e1, _ := f1.getEncoder()
	e2, _ := f2.getEncoder()
	if e1 == nil || e2 == nil {
		t.Fatal("ToUnicode CMap failed to parse; fixture is wrong")
	}
	if got := e1.Decode("\x00\x01"); got != "一" {
		t.Errorf("F1 decoded %q, want U+4E00 — wrong CMap served (key aliased?)", got)
	}
	if got := e2.Decode("\x00\x01"); got != "二" {
		t.Errorf("F2 decoded %q, want U+4E8C — wrong CMap served (key aliased?)", got)
	}
}

// TestEncoderCacheBytesBoundsRetention proves the cache bounds RETAINED BYTES,
// not just entry count. A single ToUnicode stream can accumulate arbitrarily
// many bfchar sections (interpretCmapRanges bounds one section, not the stream),
// so a parsed *cmap is not size-bounded. A CMap whose parsed size exceeds
// maxEncoderEntryBytes must be served uncached (never retained), so an
// adversarial document cannot pin unbounded heap on the Reader for its lifetime.
func TestEncoderCacheBytesBoundsRetention(t *testing.T) {
	// One bfchar section large enough that the parsed *cmap exceeds
	// maxEncoderEntryBytes (n stays under maxCmapEntries = 65536).
	var sb strings.Builder
	const n = 64000
	sb.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	fmt.Fprintf(&sb, "%d beginbfchar\n", n)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "<%04X> <%04X>\n", i, 0x4E00+(i%0x4000))
	}
	sb.WriteString("endbfchar")
	body := sb.String()
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 << /Type /Font /Subtype /Type0 /BaseFont /Big /Encoding /Identity-H /ToUnicode 5 0 R >> >> >> /Contents 4 0 R >>",
		"<< /Length 0 >>\nstream\n\nendstream",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body),
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	tu := r.Page(1).Font("F1").V.Key("ToUnicode")
	if tu.Kind() != Stream {
		t.Fatal("ToUnicode not a stream; fixture wrong")
	}
	m := r.encoders.lookup(tu.ptr, tu)
	if m == nil {
		t.Fatal("oversized ToUnicode failed to parse; fixture wrong")
	}
	if sz := approxCmapSize(m); sz <= maxEncoderEntryBytes {
		t.Fatalf("test CMap (%d bytes) did not exceed maxEncoderEntryBytes (%d); raise n", sz, maxEncoderEntryBytes)
	}
	r.encoders.mu.RLock()
	defer r.encoders.mu.RUnlock()
	if _, ok := r.encoders.m[tu.ptr]; ok {
		t.Error("oversized CMap was retained; cache must serve it uncached")
	}
	if r.encoders.bytes != 0 {
		t.Errorf("oversized CMap accounted %d bytes; want 0 (not retained)", r.encoders.bytes)
	}
}
