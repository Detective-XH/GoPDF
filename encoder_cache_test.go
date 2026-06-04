package pdf

import (
	"os"
	"sync"
	"testing"
)

// TestCachedEncoderParsesOnce proves (*Font).cachedEncoder memoizes the parsed
// encoder, so a ToUnicode CMap is parsed once rather than on every Tf. It uses a
// font backed by a /ToUnicode stream (readCmap returns a fresh *cmap per parse);
// predefined-CMap fonts return singletons and would make this test vacuous.
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
	// Non-vacuity guard: uncached parsing yields DISTINCT instances (not a
	// singleton), else the assertion below would be trivially true.
	if f.getEncoder() == f.getEncoder() {
		t.Skip("font uses a singleton encoder; caching test would be vacuous")
	}
	// The fix: repeated cachedEncoder() returns the SAME instance.
	if f.cachedEncoder() != f.cachedEncoder() {
		t.Fatal("cachedEncoder() re-parsed the CMap; cache ineffective")
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
