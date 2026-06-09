// corpus_lines_test.go — runs Page.Lines() over the committed multicolumn (FR)
// and CJK (UDHR) corpus fixtures and locks today's reading-order behaviour: the
// character-conservation invariant Lines() must satisfy on any PDF, plus the
// current column-interleaving (multi-column) and intra-line CJK spacing that the
// reading-order stabilisation work will later change. Characterization only — no
// production code ships with this file; when the stabilisation work alters
// Lines(), a sentinel fails ON PURPOSE and is updated there.
package pdf

import (
	"strings"
	"testing"
	"unicode"
)

// maxLinesCorpusPages caps how many pages per fixture the Lines() corpus check
// visits. Each page is interpreted twice (once inside Lines(), once for the
// Content() conservation comparison), which is slow under -race on the multi-page
// CJK fixtures; the first pages already carry every sentinel and exercise the
// banding/joining path. Mirrors maxWordsCorpusPages (layout_words_corpus_test.go).
const maxLinesCorpusPages = 4

// linesExpect is the behavioural snapshot a fixture's Lines() output must still
// reproduce today. Two complementary locks, both filled EMPIRICALLY at
// implementation (plans-conventions Honesty Rule) from actual Lines() output — the
// existing GetPlainText goldens use a different interpreter and are only a starting
// hypothesis:
//
//   - wantLineSubstr: each substring MUST appear verbatim in some Line.S among the
//     first maxLinesCorpusPages pages. Used for the CJK intra-line spacing lock
//     (zh-hans/ko space-separated Han/Hangul; ja contiguous run) — a single
//     baseline, so X-order is deterministic.
//   - wantSameLine: ALL of these tokens MUST co-occur in ONE Line.S — the
//     column-merge tripwire for the multi-column (FR) fixtures. Tokens are chosen
//     from physically separate columns; their co-occurrence proves Lines() collapsed
//     two columns into one visual row. This needs only that the tokens land in the
//     same Y-band (tol = FontSize*0.5, far larger than any FP epsilon) — it does NOT
//     depend on within-band ordering, so the float-equality Y-sort (latent bug #1)
//     cannot perturb it. When the reading-order stabilisation work splits columns the
//     tokens fall to different Line.S and the co-occurrence breaks ON PURPOSE.
type linesExpect struct {
	wantLineSubstr []string
	wantSameLine   []string
	desc           string
}

// linesExpectations is the single source of truth for the slice-3 Lines()
// behavioural sentinels, keyed by fixture Path. It also selects WHICH fixtures
// TestCorpusLines exercises (FR multicolumn + UDHR CJK) — a corpusManifest entry
// with no key here is skipped. The reading-order stabilisation work updates these
// when it intentionally changes line grouping/joining.
var linesExpectations = map[string]linesExpect{
	// FR 3-column pages: left col X~45-215, middle X~222-385, right X~399-565
	// (probed). Each pair is one left-column + one middle-column token that land in
	// the SAME Line.S today — a genuine column merge. The reading-order stabilisation
	// work splitting columns later moves them to different lines and the co-occurrence
	// breaks ON PURPOSE.
	"multicolumn/fr-2024-06543.pdf": {
		wantSameLine: []string{"Participation", "www.regulations.gov"}, // X~74 (left) + X~222 (middle), page-1 row Y=725
		desc:         "dense 3-column body: tokens from separate columns co-occur in one Line.S (columns merge)",
	},
	"multicolumn/fr-2024-01353.pdf": {
		wantSameLine: []string{"supplement", "adams.html."}, // X~45 (left) + X~222 (middle), page-1 row Y=725
		desc:         "dense 3-column body: tokens from separate columns co-occur in one Line.S (columns merge)",
	},
	// CJK spacing locks (real Lines() output, NOT the GetPlainText golden — different
	// interpreter). ja body runs are contiguous; zh-hans/ko split per glyph/syllable
	// into space-joined Line.S. A future CJK no-space join drops the spaced variants.
	"cjk/udhr-ja.pdf": {
		wantLineSubstr: []string{"世界人権宣言"}, // contiguous run (title L00 『世界人権宣言』); ja stays one token
		desc:           "Japanese: tightly-set run stays one token (no intra-run spaces)",
	},
	"cjk/udhr-zh-hans.pdf": {
		wantLineSubstr: []string{"联 合 国 大 会"}, // per-glyph X-gaps split into space-joined Line.S (page-1 body)
		desc:           "Simplified Chinese: per-glyph X-gaps split into space-joined Line.S",
	},
	"cjk/udhr-ko.pdf": {
		wantLineSubstr: []string{"세 계 인 권 선 언"}, // per-syllable X-gaps split into space-joined Line.S (title L00)
		desc:           "Korean: per-syllable X-gaps split into space-joined Line.S",
	},
}

// TestCorpusLines locks today's Page.Lines() reading-order behaviour over the
// committed multicolumn (FR) and CJK (UDHR) fixtures — the corpus coverage Lines()
// shipped without (audit: zero real-fixture coverage). Per page it asserts the
// character-conservation invariant; per fixture it asserts the column-interleaving
// / CJK-spacing sentinels. The reading-order stabilisation work extends/updates
// linesExpectations when it intentionally changes line grouping.
func TestCorpusLines(t *testing.T) {
	for _, e := range corpusManifest {
		exp, ok := linesExpectations[e.Path]
		if !ok {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			r := loadCorpus(t, e)
			pages := min(r.NumPage(), maxLinesCorpusPages)
			var lineS []string
			for i := 1; i <= pages; i++ {
				for _, l := range checkLinesPage(t, e.Path, i, r.Page(i)) {
					lineS = append(lineS, l.S)
				}
			}
			assertLineSentinels(t, e.Path, lineS, exp)
		})
	}
}

// checkLinesPage asserts the PDF-agnostic Lines() invariants for one page and
// returns the page's lines for sentinel gathering. Mirrors checkWordsPage but for
// the Lines() path: every Line.S is non-empty, carries no synthetic TJ newline, is
// not whitespace-only, and has at least one Word; and the non-space runes across all
// Line.S equal the page's non-space glyph runes. This is NOT redundant with
// TestWordsCorpus — it validates the Lines()-specific assembly: that the band->line
// grouping and the " "-join into Line.S (layout.go) neither drop nor invent a glyph
// (the joining spaces are stripped on both sides, so the non-space multiset must
// match Content() exactly).
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

// assertLineSentinels locks the fixture's current CJK spacing (wantLineSubstr) and
// column merging (wantSameLine). An entry that locks nothing — no wantLineSubstr and
// fewer than 2 co-occurrence tokens — is rejected so a fixture cannot pass vacuously
// (a single token "co-occurring" with itself is just Contains, not a column merge).
func assertLineSentinels(t *testing.T, path string, lineS []string, exp linesExpect) {
	t.Helper()
	if len(exp.wantLineSubstr) == 0 && len(exp.wantSameLine) < 2 {
		t.Fatalf("%s: linesExpectations entry locks nothing (need a wantLineSubstr or a >=2-token wantSameLine) — refusing to pass vacuously", path)
	}
	// CJK / spacing lock: each substring must appear in some Line.S.
	for _, substr := range exp.wantLineSubstr {
		if !anyLineContains(lineS, substr) {
			t.Errorf("%s: no Line.S contains spacing sentinel %q (%s)\nall lines: %q",
				path, substr, exp.desc, lineS)
		}
	}
	// Column-merge tripwire: all wantSameLine tokens must co-occur in ONE Line.S.
	// splitting columns later drops one token to a different line -> this breaks.
	if len(exp.wantSameLine) >= 2 && !anyLineContainsAll(lineS, exp.wantSameLine) {
		t.Errorf("%s: no single Line.S contains all column-merge tokens %q (%s) — columns no longer interleave?\nall lines: %q",
			path, exp.wantSameLine, exp.desc, lineS)
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

// anyLineContainsAll reports whether some single Line.S in lineS contains every
// token in subs (their co-occurrence in one line is the column-merge signal).
func anyLineContainsAll(lineS, subs []string) bool {
	for _, s := range lineS {
		all := true
		for _, sub := range subs {
			if !strings.Contains(s, sub) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
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
