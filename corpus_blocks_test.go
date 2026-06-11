// corpus_blocks_test.go — runs Page.Blocks() over the multicolumn (FR) and CJK
// (UDHR) corpus fixtures and locks the column-major reading-order signature.
//
// On a multi-column page the column-major block sequence makes a large upward Y
// "reset" at each column boundary (read one column top-to-bottom, then jump back up
// to the top of the next column), whereas the row-major Page.Lines() sequence stays
// Y-non-increasing. On a single-column page the block sequence is strictly
// top-to-bottom, so it never resets upward. That asymmetry is the external proof
// that the reorder actually moved a lower-left line ahead of a higher-right one —
// the definition of column-major.
//
// All thresholds were filled EMPIRICALLY from actual Blocks()/Lines() output
// (plans-conventions Honesty Rule): observed FR block reset 595–705 pt at the
// column boundaries vs a line reset <= 4.5 pt on the same pages, and an observed
// single-column CJK reset of exactly 0.
package pdf

import (
	"strings"
	"testing"
)

// maxBlocksCorpusPages caps how many pages per fixture TestCorpusBlocks visits.
// Each page is interpreted for both Blocks() and Lines(); the first pages already
// carry the column-major signal and the masthead/CJK sentinels. Mirrors
// maxLinesCorpusPages.
const maxBlocksCorpusPages = 4

// blockResetFloor is the minimum upward Y-reset (points) a multi-column fixture's
// column-major block sequence must exhibit on its best page. Calibrated from
// observed output: the FR fixtures reset 595–705 pt at the column boundaries, while
// the row-major Lines() sequence and the single-column CJK block sequence never
// reset above ~4.5 pt. 200 sits far above that jitter floor and far below the
// observed resets, so it rejects a non-reordered (row-major) result without being
// brittle to small layout changes.
const blockResetFloor = 200

// blockExpect is the behavioural snapshot a fixture's Blocks() output must
// reproduce. multiColumn selects which signature is asserted (a large column-major
// reset vs strict top-to-bottom monotonicity); wantBlockSubstr locks that real
// text survives the grouping into some Block.S. Both filled EMPIRICALLY.
type blockExpect struct {
	multiColumn     bool
	wantBlockSubstr []string
	desc            string
}

// blockExpectations is the single source of truth for the Blocks() behavioural
// sentinels, keyed by fixture Path; it also selects WHICH fixtures TestCorpusBlocks
// exercises. A corpusManifest entry with no key here is skipped.
var blockExpectations = map[string]blockExpect{
	// FR 3-column notices: the masthead survives as a one-line block at the top of
	// the leftmost column, then the body is read column-major — each column
	// top-to-bottom in full, with a ~600–700 pt upward reset at each column edge.
	"multicolumn/fr-2024-06543.pdf": {
		multiColumn:     true,
		wantBlockSubstr: []string{"Federal Register / Vol. 89, No. 61 / Thursday, March 28, 2024 / Notices"},
		desc:            "dense 3-column body: column-major blocks reset upward at each column boundary",
	},
	"multicolumn/fr-2024-01353.pdf": {
		multiColumn:     true,
		wantBlockSubstr: []string{"Federal Register / Vol. 89, No. 16 / Wednesday, January 24, 2024 / Notices"},
		desc:            "dense 3-column body: column-major blocks reset upward at each column boundary",
	},
	// Single-column CJK: no column gutter is detected, so every line is column 0 and
	// the blocks stay strictly top-to-bottom (reset 0). The sentinels also confirm
	// the CJK no-space join survives into Block.S.
	"cjk/udhr-zh-hans.pdf": {
		multiColumn:     false,
		wantBlockSubstr: []string{"世界人权宣言"},
		desc:            "single-column Simplified Chinese: blocks strictly top-to-bottom, no column reset",
	},
	"cjk/udhr-ja.pdf": {
		multiColumn:     false,
		wantBlockSubstr: []string{"『世界人権宣言』"},
		desc:            "single-column Japanese: blocks strictly top-to-bottom, no column reset",
	},
}

// TestCorpusBlocks locks Page.Blocks() column-major reading order over the
// committed multicolumn (FR) and CJK (UDHR) fixtures. Per page it asserts the
// per-block invariants and that the grouping conserves lines (no line dropped or
// invented); per fixture it asserts the column-major reset signature (or strict
// monotonicity for single-column) and that a real text sentinel survives grouping.
func TestCorpusBlocks(t *testing.T) {
	for _, e := range corpusManifest {
		exp, ok := blockExpectations[e.Path]
		if !ok {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			r := loadCorpus(t, e)
			pages := min(r.NumPage(), maxBlocksCorpusPages)
			var blockS []string
			bestBlockReset, peerLineReset := 0.0, 0.0
			for i := 1; i <= pages; i++ {
				blocks := checkBlocksPage(t, e.Path, i, r.Page(i))
				lines, err := r.Page(i).Lines()
				if err != nil {
					t.Fatalf("%s p%d: Lines(): %v", e.Path, i, err)
				}
				// Grouping conserves lines: every line lands in exactly one block.
				total := 0
				for _, b := range blocks {
					total += len(b.Lines)
				}
				if total != len(lines) {
					t.Errorf("%s p%d: blocks hold %d lines, page has %d — grouping dropped or invented a line",
						e.Path, i, total, len(lines))
				}
				br := maxUpwardReset(blockYs(blocks))
				if exp.multiColumn {
					if br > bestBlockReset {
						bestBlockReset = br
						peerLineReset = maxUpwardReset(lineYs(lines))
					}
				} else if br != 0 {
					t.Errorf("%s p%d: single-column block reset = %.1f, want 0 (blocks must be strictly top-to-bottom)",
						e.Path, i, br)
				}
				for _, b := range blocks {
					blockS = append(blockS, b.S)
				}
			}
			if exp.multiColumn {
				if bestBlockReset < blockResetFloor {
					t.Errorf("%s: best column-major block reset = %.1f, want >= %d (column-major reorder not happening?) (%s)",
						e.Path, bestBlockReset, blockResetFloor, exp.desc)
				}
				if bestBlockReset <= peerLineReset {
					t.Errorf("%s: block reset %.1f <= same-page line reset %.1f — blocks no more column-major than row-major Lines()",
						e.Path, bestBlockReset, peerLineReset)
				}
			}
			assertBlockSentinels(t, e.Path, blockS, exp)
		})
	}
}

// checkBlocksPage asserts the PDF-agnostic Blocks() invariants for one page and
// returns the page's blocks: every Block.S is non-empty and not whitespace-only and
// every Block carries at least one Line.
func checkBlocksPage(t *testing.T, path string, page int, p Page) []Block {
	t.Helper()
	blocks, err := p.Blocks()
	if err != nil {
		t.Fatalf("%s p%d: Blocks(): %v", path, page, err)
	}
	if len(blocks) == 0 {
		t.Errorf("%s p%d: no blocks", path, page)
	}
	for _, b := range blocks {
		switch {
		case b.S == "":
			t.Errorf("%s p%d: empty Block.S", path, page)
		case strings.TrimSpace(b.S) == "":
			t.Errorf("%s p%d: whitespace-only Block", path, page)
		case len(b.Lines) == 0:
			t.Errorf("%s p%d: Block %q has no Lines", path, page, b.S)
		}
	}
	return blocks
}

// assertBlockSentinels locks the fixture's masthead / CJK substrings. An entry that
// locks nothing is rejected so a fixture cannot pass vacuously.
func assertBlockSentinels(t *testing.T, path string, blockS []string, exp blockExpect) {
	t.Helper()
	if len(exp.wantBlockSubstr) == 0 {
		t.Fatalf("%s: blockExpectations entry locks no sentinel — refusing to pass vacuously", path)
	}
	for _, substr := range exp.wantBlockSubstr {
		if !anyLineContains(blockS, substr) {
			t.Errorf("%s: no Block.S contains sentinel %q (%s)", path, substr, exp.desc)
		}
	}
}

// blockYs / lineYs project the Y origin of each block/line in emission order.
func blockYs(bs []Block) []float64 {
	ys := make([]float64, len(bs))
	for i, b := range bs {
		ys[i] = b.Y
	}
	return ys
}

func lineYs(ls []Line) []float64 {
	ys := make([]float64, len(ls))
	for i, l := range ls {
		ys[i] = l.Y
	}
	return ys
}

// maxUpwardReset returns the largest upward step ys[i] - ys[i-1] (0 if none). For a
// strictly top-to-bottom (Y-descending) sequence it is 0; a column-major reorder
// makes it jump by ~a column height at each column boundary.
func maxUpwardReset(ys []float64) float64 {
	m := 0.0
	for i := 1; i < len(ys); i++ {
		if d := ys[i] - ys[i-1]; d > m {
			m = d
		}
	}
	return m
}
