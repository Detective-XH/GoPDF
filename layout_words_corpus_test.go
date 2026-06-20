// layout_words_corpus_test.go — runs Page.Words() over the real + synthetic
// corpus fixtures and asserts PDF-agnostic invariants (no golden file needed).
package pdf

import (
	"sort"
	"strings"
	"testing"
	"unicode"
)

// TestWordsCorpus exercises Page.Words() against every fixture in corpusManifest
// (real public-domain multilingual PDFs and synthetic baselines). It asserts
// invariants that must hold for any PDF: no Word is empty, whitespace-only, or
// carries the synthetic "\n" that content.go appends after a TJ operator; and
// every non-whitespace glyph from Content().Text lands in exactly one Word
// (character conservation — Words neither drops nor invents characters).
// maxWordsCorpusPages caps how many pages per fixture the check visits. Each
// page is interpreted twice (once inside Words(), once for the comparison's
// Content()), which is slow under -race on the 22- and 24-page CJK fixtures.
// The first few pages already exercise every script, font, and TJ pattern in
// the corpus; run without -race to sweep all pages quickly.
const maxWordsCorpusPages = 4

func TestWordsCorpus(t *testing.T) {
	t.Parallel()
	for _, e := range corpusManifest {
		t.Run(e.Path, func(t *testing.T) {
			t.Parallel()
			r := loadCorpus(t, e)
			pages := min(r.NumPage(), maxWordsCorpusPages)
			for i := 1; i <= pages; i++ {
				checkWordsPage(t, e.Path, i, r.Page(i))
			}
		})
	}
}

// checkWordsPage asserts the Words() invariants for a single page.
func checkWordsPage(t *testing.T, path string, page int, p Page) {
	t.Helper()
	words, err := p.Words()
	if err != nil {
		t.Fatalf("%s p%d: Words(): %v", path, page, err)
	}
	for _, w := range words {
		switch {
		case w.S == "":
			t.Errorf("%s p%d: empty Word.S", path, page)
		case strings.ContainsRune(w.S, '\n'):
			t.Errorf("%s p%d: Word %q carries synthetic TJ newline", path, page, w.S)
		case strings.TrimSpace(w.S) == "":
			t.Errorf("%s p%d: whitespace-only Word %q", path, page, w.S)
		}
	}
	got := sortedRunes(wordRunes(words))
	want := sortedRunes(nonSpaceGlyphRunes(p.Content().Text))
	if got != want {
		t.Errorf("%s p%d: character conservation broken\n words(non-ws): %q\nglyphs(non-ws): %q",
			path, page, got, want)
	}
}

// wordRunes concatenates every rune across the given words.
func wordRunes(words []Word) []rune {
	var rs []rune
	for _, w := range words {
		rs = append(rs, []rune(w.S)...)
	}
	return rs
}

// nonSpaceGlyphRunes returns the non-whitespace runes of the per-glyph texts.
func nonSpaceGlyphRunes(texts []Text) []rune {
	var rs []rune
	for _, t := range texts {
		for _, r := range t.S {
			if !unicode.IsSpace(r) {
				rs = append(rs, r)
			}
		}
	}
	return rs
}

// sortedRunes returns rs sorted, so two rune slices can be compared as multisets.
func sortedRunes(rs []rune) string {
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	return string(rs)
}
