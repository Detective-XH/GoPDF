// tables_quality_corpus_test.go — per-class table-extraction quality diagnostic for
// the public Page.Tables() surface.
//
// DIAGNOSTIC-ONLY: all ACCURACY output (content%/verbatim%) is t.Logf — there is no
// t.Errorf accuracy floor here (a blocking aggregate floor is future work). The only
// t.Errorf in this file are STRUCTURAL-INTEGRITY assertions (a held-out fixture must
// not be one of the 3 threshold-tuning sources, and must declare a class) — these
// gate corpus coherence, not extraction quality, so they do not violate the
// diagnostic-only contract.
//
// COVERAGE (>=2 held-out fixtures per in-scope class) is reported as a LOUD t.Logf
// "HELD-OUT CORPUS INCOMPLETE" diagnostic, not a hard gate: the held-out corpus is
// populated incrementally. The hard >=2 structural gate is added once it is complete.
//
// Scoring contract (the .cellgrid.tsv golden format):
//   - cell-level match over DATA cells (golden rows after header_rows);
//   - ROW ALIGNMENT, not a naive rows[header_rows:] slice — each golden data row is
//     anchored to the detector grid by its row-label column (looseCell match); a
//     not-found OR duplicate-ambiguous anchor scores the whole row as a miss;
//   - verbatim = strict == (exact-match %); content = looseCell
//     (whitespace/superscript-folded), a supplementary diagnostic;
//   - "# known-ceiling:" cells are EXCLUDED from the denominator and counted separately.
//
// Does NOT duplicate TestPublicAccuracyEPA/IRS/NIST (tables_public_accuracy_test.go) —
// qualityFixtures is derived by filtering cellgridFixtures for heldOut==true, which
// EXCLUDES the 3 tuned fixtures (they carry heldOut=false).
//
// Reuses package-level helpers: looseCell, pct (tables_lattice_accuracy_test.go,
// tables_lattice_irs_test.go), parseCellGrid/cellGrid (corpus_cellgrid_test.go),
// publicLargestTable (tables_public_accuracy_test.go), corpusPath (corpus_test.go).
package pdf

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// inScopeQualityClasses are the taxonomy classes currently in scope for the held-out
// quality corpus. group-ruled+banded, single-axis-ruled, and borderless are not yet
// in scope (no accuracy consumer / measured FP-unsafe).
var inScopeQualityClasses = []string{"fully-ruled", "rect-bordered"}

// tunedFixturePaths are the 3 threshold-tuning sources. They must NEVER appear in the
// held-out quality set — they are gated exclusively by TestPublicAccuracyEPA/IRS/NIST.
// (NIST's golden is inline in tables_lattice_accuracy_test.go, not in cellgridFixtures;
// it is listed here as a belt-and-suspenders denylist entry.)
var tunedFixturePaths = map[string]bool{
	"tables/epa-egrid2022-t1.cellgrid.tsv":      true,
	"tables/irs-soi-inpre-t1-2022.cellgrid.tsv": true,
	"tables/nist-hb44-appc-2026.cellgrid.tsv":   true,
}

// qualityHeldOut returns the held-out quality fixtures — cellgridFixtures filtered for
// heldOut==true. Deriving the set by filter (rather than a second registry) makes an
// orphaned quality entry impossible by construction.
func qualityHeldOut() []cellgridFixture {
	var out []cellgridFixture
	for _, f := range cellgridFixtures {
		if f.heldOut {
			out = append(out, f)
		}
	}
	return out
}

// perClassQuality accumulates cell-level accuracy for one taxonomy class.
//
// The SUBSTANTIVE tallies (subst*) are the load-bearing measurement: they exclude
// both the row-anchor column (matched by construction — that is how anchorRow finds
// the row) and empty cells (empty==empty is a trivial match). content/verbatim over
// ALL data cells stay reported for continuity but are diluted by those two classes;
// substantive content/verbatim are what actually test value placement.
type perClassQuality struct {
	contentHit, verbatimHit, total                int
	substContentHit, substVerbatimHit, substTotal int
	anchorCount, emptyCount, ceilingCount         int
	fixtureCount                                  int
}

// anchorRow finds the grid row whose anchorCol cell looseCell-matches want. It returns
// (rowIdx, true) only on a UNIQUE match; a not-found OR duplicate-ambiguous anchor
// returns (-1, false) so the caller scores the whole golden row as a miss.
func anchorRow(grid [][]string, anchorCol int, want string) (int, bool) {
	w := looseCell(want)
	found := -1
	for ri, row := range grid {
		if anchorCol < len(row) && looseCell(row[anchorCol]) == w {
			if found >= 0 {
				return -1, false // ambiguous: anchor value is not unique
			}
			found = ri
		}
	}
	return found, found >= 0
}

// TestAnchorRow locks the row-alignment detection: a unique match returns its index,
// a not-found OR duplicate-ambiguous anchor returns (-1, false) so the caller scores
// the whole row as a miss (the branch codex caught inflating empty-cell hits). looseCell
// folding is exercised so a char-spaced grid cell still anchors on the printed value.
func TestAnchorRow(t *testing.T) {
	grid := [][]string{
		{"Alabama", "1"},
		{"Alaska", "2"},
		{"4 0 , 6 9 8", "x"}, // looseCell folds the char-spacing to "40,698"
		{"Dup", "a"},
		{"Dup", "b"},
	}
	cases := []struct {
		name    string
		want    string
		wantIdx int
		wantOK  bool
	}{
		{"unique match", "Alabama", 0, true},
		{"looseCell-folded match", "40,698", 2, true},
		{"not found", "Wyoming", -1, false},
		{"duplicate ambiguous", "Dup", -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, ok := anchorRow(grid, 0, tc.want)
			if idx != tc.wantIdx || ok != tc.wantOK {
				t.Errorf("anchorRow(%q) = (%d, %v), want (%d, %v)", tc.want, idx, ok, tc.wantIdx, tc.wantOK)
			}
		})
	}
}

// openQualityGrid opens the fixture's source PDF, parses its golden .cellgrid.tsv, and
// returns the detector's largest-table grid plus the parsed golden. The page is taken
// from the golden's pdf_page (single source of truth — no registry/TSV divergence).
// Setup failures are t.Fatalf (a fixture that cannot be scored is a hard error, never a
// silent skip). Split out of scoreQualityFixture to keep gocyclo below threshold.
func openQualityGrid(t *testing.T, f cellgridFixture) ([][]string, cellGrid) {
	t.Helper()
	//nolint:gosec // G304: fixed corpus path, not user input
	raw, err := os.ReadFile(corpusPath(f.path))
	if err != nil {
		t.Fatalf("read golden %s: %v", f.path, err)
	}
	g, err := parseCellGrid(raw)
	if err != nil {
		t.Fatalf("parseCellGrid %s: %v", f.path, err)
	}
	if g.pdfPage <= 0 {
		t.Fatalf("golden %s declares no pdf_page (needed to select the page)", f.path)
	}

	fh, err := os.Open(corpusPath(f.sourcePDF))
	if err != nil {
		t.Fatalf("open %s: %v", f.sourcePDF, err)
	}
	defer func() { _ = fh.Close() }()
	fi, err := fh.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", f.sourcePDF, err)
	}
	r, err := NewReader(fh, fi.Size())
	if err != nil {
		t.Fatalf("NewReader %s: %v", f.sourcePDF, err)
	}
	tables, err := r.Page(g.pdfPage).Tables()
	if err != nil {
		t.Fatalf("Page(%d).Tables() %s: %v", g.pdfPage, f.path, err)
	}
	if len(tables) == 0 {
		t.Fatalf("Page(%d).Tables() returned no tables for %s", g.pdfPage, f.path)
	}
	return publicLargestTable(tables).Cells, g
}

// scoreQualityFixture row-aligns the detector grid to the golden and scores DATA cells.
func scoreQualityFixture(t *testing.T, f cellgridFixture) perClassQuality {
	grid, g := openQualityGrid(t, f)

	// known-ceiling set keyed by 1-based (row, col); excluded from the denominator.
	ceil := make(map[[2]int]string, len(g.knownCeiling))
	for _, cm := range g.knownCeiling {
		ceil[[2]int{cm.row, cm.col}] = cm.reason
	}

	var res perClassQuality
	for gr := g.headerRows; gr < g.rows; gr++ {
		anchor := g.cells[gr][f.anchorCol]
		if strings.TrimSpace(anchor) == "" {
			continue // section-label row (empty anchor) — not a scored data row
		}
		ri, ok := anchorRow(grid, f.anchorCol, anchor)
		for gc := 0; gc < g.cols; gc++ {
			if reason, isCeiling := ceil[[2]int{gr + 1, gc + 1}]; isCeiling {
				res.ceilingCount++
				t.Logf("CEILING EXCLUDED [%s] r%dc%d: %s", f.path, gr+1, gc+1, reason)
				continue
			}
			res.total++
			want := g.cells[gr][gc]
			var got string
			if ok && gc < len(grid[ri]) {
				got = grid[ri][gc]
			}
			// A row whose anchor was not found (or was ambiguous) is a FULL-ROW miss
			// per the alignment contract: gating both matches on ok forces every cell
			// (including empty golden cells, which would otherwise match "" == "")
			// to a miss, so alignment breakage cannot vacuously inflate the score.
			// TestAnchorRow locks the not-found/duplicate detection feeding ok.
			// TODO: add a synthetic fixture with a non-aligning row to lock this
			// full-row-miss scoring path end-to-end (only the detection is unit-tested).
			cMatch := ok && looseCell(got) == looseCell(want)
			vMatch := ok && got == want
			if vMatch {
				res.verbatimHit++
			}
			if cMatch {
				res.contentHit++
			}
			classifyQualityCell(&res, gc == f.anchorCol, want == "", cMatch, vMatch)
		}
	}
	logFixtureQuality(t, f, res)
	return res
}

// classifyQualityCell buckets one scored cell into anchor / empty / substantive and
// accumulates the substantive content/verbatim tallies (the load-bearing metric).
func classifyQualityCell(res *perClassQuality, isAnchor, isEmpty, cMatch, vMatch bool) {
	switch {
	case isAnchor:
		res.anchorCount++ // matched by construction (anchorRow found the row on it)
	case isEmpty:
		res.emptyCount++ // empty==empty is a trivial match
	default:
		res.substTotal++
		if cMatch {
			res.substContentHit++
		}
		if vMatch {
			res.substVerbatimHit++
		}
	}
}

// logFixtureQuality emits the per-fixture diagnostic line. SUBSTANTIVE is the headline
// (non-anchor, non-empty cells); the all-cell content/verbatim are reported alongside.
func logFixtureQuality(t *testing.T, f cellgridFixture, res perClassQuality) {
	t.Helper()
	t.Logf("QUALITY [%s] class=%s SUBSTANTIVE content=%d/%d (%.1f%%) verbatim=%d/%d (%.1f%%) | all-cells content=%d/%d (%.1f%%) verbatim=%d/%d (%.1f%%) | anchor=%d empty=%d known_ceiling_count=%d",
		f.path, f.class,
		res.substContentHit, res.substTotal, pct(res.substContentHit, res.substTotal),
		res.substVerbatimHit, res.substTotal, pct(res.substVerbatimHit, res.substTotal),
		res.contentHit, res.total, pct(res.contentHit, res.total),
		res.verbatimHit, res.total, pct(res.verbatimHit, res.total),
		res.anchorCount, res.emptyCount, res.ceilingCount)
}

// TestPublicTablesQualityCorpus scores Page.Tables() on every held-out fixture and logs
// per-class content%/verbatim% diagnostics. See the file header for the contract.
func TestPublicTablesQualityCorpus(t *testing.T) {
	held := qualityHeldOut()

	// Structural integrity (t.Errorf — NOT accuracy floors).
	for _, f := range held {
		if tunedFixturePaths[f.path] {
			t.Errorf("held-out quality set must EXCLUDE tuned fixture %s (gated by TestPublicAccuracy*)", f.path)
		}
		if f.class == "" {
			t.Errorf("held-out fixture %s has empty class", f.path)
		}
	}

	byClass := make(map[string]*perClassQuality)
	for _, f := range held {
		t.Run(f.path, func(t *testing.T) {
			res := scoreQualityFixture(t, f)
			if byClass[f.class] == nil {
				byClass[f.class] = &perClassQuality{}
			}
			cr := byClass[f.class]
			cr.contentHit += res.contentHit
			cr.verbatimHit += res.verbatimHit
			cr.total += res.total
			cr.substContentHit += res.substContentHit
			cr.substVerbatimHit += res.substVerbatimHit
			cr.substTotal += res.substTotal
			cr.anchorCount += res.anchorCount
			cr.emptyCount += res.emptyCount
			cr.ceilingCount += res.ceilingCount
			cr.fixtureCount++
		})
	}

	logQualitySummary(t, byClass)
}

// logQualitySummary emits the per-class diagnostic table. In-scope classes with fewer
// than 2 fixtures log a LOUD "HELD-OUT CORPUS INCOMPLETE" line (incremental, not a gate).
func logQualitySummary(t *testing.T, byClass map[string]*perClassQuality) {
	t.Helper()
	t.Logf("=== per-class quality summary (DIAGNOSTIC — accuracy not gated) ===")
	for _, cls := range inScopeQualityClasses {
		cr := byClass[cls]
		if cr == nil || cr.fixtureCount == 0 {
			t.Logf("class=%-18s *** 0 fixtures — HELD-OUT CORPUS INCOMPLETE for this class (need >=2) ***", cls)
			continue
		}
		t.Logf("class=%-18s fixtures=%d SUBSTANTIVE content=%.1f%% verbatim=%.1f%% (n=%d) | all-cells content=%.1f%% verbatim=%.1f%% (anchor=%d empty=%d ceiling=%d total=%d)",
			cls, cr.fixtureCount,
			pct(cr.substContentHit, cr.substTotal), pct(cr.substVerbatimHit, cr.substTotal), cr.substTotal,
			pct(cr.contentHit, cr.total), pct(cr.verbatimHit, cr.total),
			cr.anchorCount, cr.emptyCount, cr.ceilingCount, cr.total)
		if cr.fixtureCount < 2 {
			t.Logf("class=%-18s *** only %d/2 fixtures — HELD-OUT CORPUS INCOMPLETE for this class ***", cls, cr.fixtureCount)
		}
	}
	for cls, cr := range byClass {
		if isInScopeQualityClass(cls) {
			continue
		}
		t.Logf("class=%-18s fixtures=%d SUBSTANTIVE content=%.1f%% verbatim=%.1f%% (n=%d) (OUT-OF-SCOPE)",
			cls, cr.fixtureCount,
			pct(cr.substContentHit, cr.substTotal), pct(cr.substVerbatimHit, cr.substTotal), cr.substTotal)
	}
}

// isInScopeQualityClass reports whether cls is an in-scope taxonomy class.
func isInScopeQualityClass(cls string) bool {
	return slices.Contains(inScopeQualityClasses, cls)
}
