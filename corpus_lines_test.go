// corpus_lines_test.go — runs Page.Lines() over the committed multicolumn (FR)
// and CJK (UDHR) corpus fixtures and locks the stabilised reading-order
// behaviour: the character-conservation invariant Lines() must satisfy on any
// PDF, the per-word/-line font fields, the CJK no-space join, and the
// per-band column split (a full-width masthead stays one line while dense
// multi-column body rows break into single-column lines). All sentinel values
// were filled EMPIRICALLY from actual Lines() output (plans-conventions Honesty
// Rule); the GetPlainText goldens use a different interpreter and only seeded
// the hypotheses.
package pdf

import (
	"sort"
	"strings"
	"testing"
	"unicode"
)

// maxLinesCorpusPages caps how many pages per fixture the Lines() corpus check
// visits. Each page is interpreted twice (once inside Lines(), once for the
// Content() conservation comparison), which is slow under -race on the multi-page
// CJK fixtures; the first pages already carry every sentinel and exercise the
// banding/splitting/joining path. Mirrors maxWordsCorpusPages.
const maxLinesCorpusPages = 4

// linesExpect is the behavioural snapshot a fixture's Lines() output must
// reproduce. Both locks are filled EMPIRICALLY from actual Lines() output:
//
//   - wantLineSubstr: each substring MUST appear verbatim in some Line.S among
//     the first maxLinesCorpusPages pages. Used two ways: the CJK no-space join
//     (a contiguous Han/Hangul run that, before the join, was space-separated),
//     and the full-width masthead lock (the whole masthead line must survive the
//     per-band column split intact — it spans the gutter x-positions but carries
//     no inter-column gap, so it must NOT fragment).
//   - wantMedianWBelow: the median Line.W across the gathered pages must be below
//     this width — the column-split signal for the dense multi-column (FR)
//     fixtures. Before the split a 3-column body row merged into one full-width
//     (~340 pt) line; after it, most lines are a single column (~155 pt), so the
//     median collapses. Skipped (0) for the single-column CJK fixtures, whose
//     lines legitimately span the full text column.
type linesExpect struct {
	wantLineSubstr   []string
	wantMedianWBelow float64
	desc             string
}

// linesExpectations is the single source of truth for the Lines() behavioural
// sentinels, keyed by fixture Path. It also selects WHICH fixtures
// TestCorpusLines exercises (FR multicolumn + UDHR CJK) — a corpusManifest entry
// with no key here is skipped.
var linesExpectations = map[string]linesExpect{
	// FR 3-column notices: the full-width masthead must survive the per-band
	// split as one Line.S (it crosses the gutter x-positions but flows with
	// ordinary word spacing, so there is no inter-column gap to split at), while
	// the dense body rows split into single-column lines — the median line width
	// collapses from ~340 pt (merged rows) to ~155 pt (one column).
	"multicolumn/fr-2024-06543.pdf": {
		wantLineSubstr:   []string{"Federal Register / Vol. 89, No. 61 / Thursday, March 28, 2024 / Notices"},
		wantMedianWBelow: 250,
		desc:             "dense 3-column body: full-width masthead stays one line; body rows split to single-column width",
	},
	"multicolumn/fr-2024-01353.pdf": {
		wantLineSubstr:   []string{"Federal Register / Vol. 89, No. 16 / Wednesday, January 24, 2024 / Notices"},
		wantMedianWBelow: 250,
		desc:             "dense 3-column body: full-width masthead stays one line; body rows split to single-column width",
	},
	// CJK no-space join: per-glyph/-syllable words rejoin without the inter-word
	// space because both boundary runes are CJK. ja was already contiguous; the
	// zh-hans/ko runs were space-separated before the join.
	"cjk/udhr-ja.pdf": {
		wantLineSubstr: []string{"世界人権宣言"}, // contiguous run (title 『世界人権宣言』)
		desc:           "Japanese: tightly-set run stays one contiguous token",
	},
	"cjk/udhr-zh-hans.pdf": {
		wantLineSubstr: []string{"联合国大会"}, // was "联 合 国 大 会"; Han is space-less, so the join closes the per-glyph gaps
		desc:           "Simplified Chinese: Han no-space join yields a contiguous run",
	},
	"cjk/udhr-ko.pdf": {
		// Korean uses real inter-word spaces, so Hangul is NOT space-suppressed:
		// this body phrase keeps its word spaces (it would read "모든인류구성원의"
		// if the join wrongly closed Hangul gaps — the Codex-flagged corruption).
		wantLineSubstr: []string{"모든 인류 구성원의"},
		desc:           "Korean: Hangul inter-word spaces are preserved (not space-suppressed)",
	},
}

// TestCorpusLines locks Page.Lines() reading-order behaviour over the committed
// multicolumn (FR) and CJK (UDHR) fixtures. Per page it asserts the
// character-conservation invariant and the per-line font-aggregation rule; per
// fixture it asserts the CJK-join / masthead-whole substrings, the multi-column
// width-split signal, and that the additive font fields are populated.
func TestCorpusLines(t *testing.T) {
	t.Parallel()
	for _, e := range corpusManifest {
		exp, ok := linesExpectations[e.Path]
		if !ok {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			t.Parallel()
			r := loadCorpus(t, e)
			pages := min(r.NumPage(), maxLinesCorpusPages)
			var lineS []string
			var widths []float64
			sawFont := false
			for i := 1; i <= pages; i++ {
				for _, l := range checkLinesPage(t, e.Path, i, r.Page(i)) {
					lineS = append(lineS, l.S)
					widths = append(widths, l.W)
					if l.Font != "" && l.FontSize > 0 {
						sawFont = true
					}
				}
			}
			if !sawFont {
				t.Errorf("%s: no Line carried a populated Font/FontSize — additive font fields not flowing through", e.Path)
			}
			assertLineSentinels(t, e.Path, lineS, widths, exp)
		})
	}
}

// checkLinesPage asserts the PDF-agnostic Lines() invariants for one page and
// returns the page's lines for sentinel gathering. Mirrors checkWordsPage but
// for the Lines() path: every Line.S is non-empty, carries no synthetic TJ
// newline, is not whitespace-only, and has at least one Word; the per-line font
// fields equal the first word's (the documented first-word-wins aggregation);
// and the non-space runes across all Line.S equal the page's non-space glyph
// runes. The conservation check is NOT redundant with TestWordsCorpus — it
// validates the Lines()-specific assembly (the band->column-segment->line
// grouping and the CJK-aware join) neither drops nor invents a glyph.
func checkLinesPage(t *testing.T, path string, page int, p Page) []Line {
	t.Helper()
	lines, err := p.Lines()
	if err != nil {
		t.Fatalf("%s p%d: Lines(): %v", path, page, err)
	}
	for _, l := range lines {
		switch {
		case l.S == "":
			t.Errorf("%s p%d: empty Line.S", path, page)
		case strings.ContainsRune(l.S, '\n'):
			t.Errorf("%s p%d: Line %q carries synthetic TJ newline", path, page, l.S)
		case strings.TrimSpace(l.S) == "":
			t.Errorf("%s p%d: whitespace-only Line %q", path, page, l.S)
		case len(l.Words) == 0:
			t.Errorf("%s p%d: Line %q has no Words", path, page, l.S)
		case l.Font != l.Words[0].Font || l.FontSize != l.Words[0].FontSize:
			t.Errorf("%s p%d: Line %q font aggregation broken: line=(%q,%.1f) first word=(%q,%.1f)",
				path, page, l.S, l.Font, l.FontSize, l.Words[0].Font, l.Words[0].FontSize)
		}
	}
	got := sortedRunes(lineNonSpaceRunes(lines))
	want := sortedRunes(nonSpaceGlyphRunes(p.Content().Text))
	if got != want {
		t.Errorf("%s p%d: character conservation broken\n lines(non-ws): %q\nglyphs(non-ws): %q",
			path, page, got, want)
	}
	return lines
}

// assertLineSentinels locks the fixture's CJK-join / masthead substrings and the
// multi-column width-split signal. An entry that locks nothing is rejected so a
// fixture cannot pass vacuously.
func assertLineSentinels(t *testing.T, path string, lineS []string, widths []float64, exp linesExpect) {
	t.Helper()
	if len(exp.wantLineSubstr) == 0 && exp.wantMedianWBelow == 0 {
		t.Fatalf("%s: linesExpectations entry locks nothing (need a wantLineSubstr or wantMedianWBelow) — refusing to pass vacuously", path)
	}
	for _, substr := range exp.wantLineSubstr {
		if !anyLineContains(lineS, substr) {
			t.Errorf("%s: no Line.S contains sentinel %q (%s)\nall lines: %q",
				path, substr, exp.desc, lineS)
		}
	}
	if exp.wantMedianWBelow > 0 {
		if m := medianFloat(widths); m >= exp.wantMedianWBelow {
			t.Errorf("%s: median Line.W = %.0f, want < %.0f (%s) — multi-column rows not split?",
				path, m, exp.wantMedianWBelow, exp.desc)
		}
	}
}

// anyLineContains reports whether some Line.S in lineS contains substr.
func anyLineContains(lineS []string, substr string) bool {
	for _, s := range lineS {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// medianFloat returns the median of xs (0 for empty); xs is copied before sorting.
func medianFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// lineNonSpaceRunes returns the non-whitespace runes across every Line.S — the
// single spaces Lines() inserts between words (and any intra-line space) are
// dropped so the result is a clean multiset comparable to nonSpaceGlyphRunes.
func lineNonSpaceRunes(lines []Line) []rune {
	var rs []rune
	for _, l := range lines {
		for _, r := range l.S {
			if !unicode.IsSpace(r) {
				rs = append(rs, r)
			}
		}
	}
	return rs
}
