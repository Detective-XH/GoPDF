// tables_confidence_test.go — tests for the per-table confidence / warnings / location API.
//
// Gate responsibilities:
//   - TestTablePhantomDetector: pure unit test for the D1 blank-column detector
//   - TestTableConfidenceRollup: Confidence = Low iff len(Warnings) > 0
//   - TestTableRegionsLength: 1:1 correspondence between Tables() and TableRegions()
//   - TestTableRegionsNonRotated: page-space coords for a non-rotated fixture
//   - TestTableRegionsRotated: page-space coords for a synthetic 90°-CCW table
//   - TestTablesCellsNoHarm: byte-identical Table.Cells gate (golden hash capture)
//
// Inner-loop commands:
//
//	go test -run TestTablePhantomDetector .         # pure unit; no PDF I/O
//	go test -run TestTableConfidenceRollup .        # pure unit; no PDF I/O
//	go test -run TestTableRegions .                 # coord checks
//	go test -run TestTablesCellsNoHarm -update .    # capture golden (run once after tables.go edit)
//	go test -run TestTablesCellsNoHarm .            # verify byte-identical
package pdf

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

// ── D1 phantom detector ───────────────────────────────────────────────────────

// TestTablePhantomDetector is the unit gate for detectTableWarnings (D1: blankCol >= 0.6).
// It exercises the threshold boundary, the zero-column guard, and the Detail format.
// No PDF I/O — pure function.
func TestTablePhantomDetector(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		grid     [][]string
		wantWarn bool    // true → phantom warning expected
		wantFrac float64 // expected blank_col_fraction (only checked when wantWarn)
	}{
		{
			name: "all_columns_blank",
			grid: [][]string{
				{"", "", ""},
				{"", "", ""},
			},
			wantWarn: true, wantFrac: 1.0,
		},
		{
			name: "one_of_five_has_content_80pct_blank",
			grid: [][]string{
				{"a", "", "", "", ""},
				{"b", "", "", "", ""},
			},
			wantWarn: true, wantFrac: 0.80,
		},
		{
			name: "exactly_06_threshold_3_of_5_blank",
			grid: [][]string{
				{"a", "b", "", "", ""},
				{"c", "d", "", "", ""},
			},
			wantWarn: true, wantFrac: 0.60,
		},
		{
			name: "just_below_06_2_of_4_blank_50pct",
			grid: [][]string{
				{"a", "b", "", ""},
				{"c", "d", "", ""},
			},
			wantWarn: false,
		},
		{
			name: "normal_table_no_blank_cols",
			grid: [][]string{
				{"a", "b", "c"},
				{"1", "2", "3"},
			},
			wantWarn: false,
		},
		{
			name:     "empty_grid",
			grid:     [][]string{},
			wantWarn: false,
		},
		{
			name:     "zero_columns",
			grid:     [][]string{{}},
			wantWarn: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := detectTableWarnings(tc.grid)
			if tc.wantWarn {
				if len(got) == 0 {
					t.Fatalf("want phantom warning, got none")
				}
				if got[0].Code != TableWarningPhantom {
					t.Errorf("Code: got %q, want %q", got[0].Code, TableWarningPhantom)
				}
				if got[0].Message == "" {
					t.Error("Message must be non-empty")
				}
				wantDetail := fmt.Sprintf("blank_col_fraction=%.2f", tc.wantFrac)
				if got[0].Detail != wantDetail {
					t.Errorf("Detail: got %q, want %q", got[0].Detail, wantDetail)
				}
			} else {
				if len(got) != 0 {
					t.Errorf("want no warning, got %+v", got)
				}
			}
		})
	}
}

// ── D2 legacy-font-text detector ───────────────────────────────────────────────

// legacyCellWord builds a Word whose center anchor lands inside the single test cell
// [x0=0,x1=100] × top-origin [top=-20,bottom=0] (display Y around 10). Used to feed
// detectLegacyFontText synthetic in-cell words without PDF I/O.
func legacyCellWord(s, font string) Word {
	return Word{S: s, X: 10, Y: 8, W: 12, H: 8, Font: font}
}

// TestDetectLegacyFontText is the unit gate for D2: a table whose in-cell text comes from a
// legacy non-Unicode Indic font and decoded to non-Devanagari gibberish is flagged
// legacy_font_text. It locks the font-family match, the no-Devanagari script-mismatch
// corroboration (incl. the Latin-1 case), the High/clean cases, the threshold, and the
// in-cell restriction (a garbled caption OUTSIDE the cells must not flag). Pure function.
func TestDetectLegacyFontText(t *testing.T) {
	t.Parallel()

	// One cell covering x∈[0,100], top-origin y∈[-20,0]; legacyCellWord lands inside it.
	cells := []lCell{{x0: 0, top: -20, x1: 100, bottom: 0}}

	cases := []struct {
		name     string
		words    []Word
		wantWarn bool
		wantFont string // expected legacy_font= prefix in Detail (only when wantWarn)
	}{
		{
			name: "kruti_dev_ascii_gibberish",
			words: []Word{
				legacyCellWord("rkfydk", "KrutiDev010"),
				legacyCellWord("jktLFkku", "KrutiDev010"),
				legacyCellWord("ljdkj", "Kruti-Dev680"),
			},
			wantWarn: true, wantFont: "KrutiDev010",
		},
		{
			name: "walkman_chanakya_latin1_gibberish", // è = U+00E8 (Latin-1), not ASCII — no-Indic-script still fires
			words: []Word{
				legacyCellWord("vfèkdkfj;ksa", "Walkman-Chanakya905Bold"),
				legacyCellWord("leh{kk", "Vivek-NormalA"),
				legacyCellWord("jk\"Vªh;", "Walkman-Chanakya905Bold"),
			},
			wantWarn: true, wantFont: "Walkman-Chanakya905Bold",
		},
		{
			name: "legacy_font_but_decoded_to_real_devanagari", // G3 separation: legacy NAME but correct decode → silent
			words: []Word{
				legacyCellWord("तालिका", "KrutiDev010"),
				legacyCellWord("राष्ट्रीय", "KrutiDev010"),
				legacyCellWord("निवल", "KrutiDev010"),
			},
			wantWarn: false,
		},
		{
			name: "legacy_font_decoded_to_real_gujarati", // hasIndicScript covers ALL Indic blocks, not just Devanagari
			words: []Word{
				legacyCellWord("ગુજરાતી", "Walkman-Chanakya905Bold"),
				legacyCellWord("વસ્તી", "Walkman-Chanakya905Bold"),
				legacyCellWord("કુલ", "Walkman-Chanakya905Bold"),
			},
			wantWarn: false,
		},
		{
			name: "two_garbled_below_count_gate", // frac=1.0 but only 2 garbled words < minLegacyGarbledWords
			words: []Word{
				legacyCellWord("rkfydk", "KrutiDev010"),
				legacyCellWord("ljdkj", "KrutiDev010"),
			},
			wantWarn: false,
		},
		{
			name: "ambiguous_family_name_dropped_from_list", // "shree"/"akshar" were removed — a legit Latin font must not match
			words: []Word{
				legacyCellWord("Total", "Shree-Regular"),
				legacyCellWord("Population", "Akshar-Bold"),
				legacyCellWord("Growth", "Akruti-Italic"),
			},
			wantWarn: false,
		},
		{
			name: "normal_latin_font_english",
			words: []Word{
				legacyCellWord("Total", "TimesNewRomanPSMT"),
				legacyCellWord("Population", "Arial-BoldMT"),
			},
			wantWarn: false,
		},
		{
			name: "below_threshold_mostly_english", // 1 legacy of 5 alpha words = 0.2 < 0.3
			words: []Word{
				legacyCellWord("vkfFkZd", "KrutiDev010"),
				legacyCellWord("Year", "Arial"),
				legacyCellWord("Total", "Arial"),
				legacyCellWord("Male", "Arial"),
				legacyCellWord("Female", "Arial"),
			},
			wantWarn: false,
		},
		{
			name: "legacy_word_outside_cells_not_flagged", // garbled caption above the grid → center anchor outside cells
			words: []Word{
				{S: "vkfFkZd", X: 10, Y: 200, W: 12, H: 8, Font: "KrutiDev010"}, // ay = -(204) ∉ [-20,0]
				{S: "leh{kk", X: 30, Y: 200, W: 12, H: 8, Font: "KrutiDev010"},
			},
			wantWarn: false,
		},
		{
			name:     "no_alpha_words_numeric_only",
			words:    []Word{legacyCellWord("12,493", "TimesNewRomanPSMT"), legacyCellWord("265", "TimesNewRomanPSMT")},
			wantWarn: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := detectLegacyFontText(cells, tc.words)
			if tc.wantWarn {
				if len(got) == 0 {
					t.Fatalf("want legacy_font_text warning, got none")
				}
				if got[0].Code != TableWarningLegacyFont {
					t.Errorf("Code: got %q, want %q", got[0].Code, TableWarningLegacyFont)
				}
				if got[0].Message == "" {
					t.Error("Message must be non-empty")
				}
				if !strings.HasPrefix(got[0].Detail, "legacy_font="+tc.wantFont+";") {
					t.Errorf("Detail: got %q, want prefix %q", got[0].Detail, "legacy_font="+tc.wantFont+";")
				}
			} else if len(got) != 0 {
				t.Errorf("want no warning, got %+v", got)
			}
		})
	}

	// Empty cells → no warning (degenerate guard).
	if got := detectLegacyFontText(nil, []Word{legacyCellWord("rkfydk", "KrutiDev010")}); len(got) != 0 {
		t.Errorf("nil cells: want no warning, got %+v", got)
	}
}

// ── Confidence roll-up ────────────────────────────────────────────────────────

// TestTableConfidenceRollup verifies the roll-up rule (Confidence = Low iff a Warning fired)
// on TWO surfaces: (1) the rollupConfidence helper in isolation — both branches; and (2) the
// REAL projection in Tables(), by asserting the invariant on actual extracted tables. (2) is
// the load-bearing check: it proves Tables() wires the rule into the public Table.Confidence
// field — not a re-implementation of the rule inside the test.
func TestTableConfidenceRollup(t *testing.T) {
	t.Parallel()

	// (1) Helper in isolation — both branches.
	if got := rollupConfidence(nil); got != TableConfidenceHigh {
		t.Errorf("rollupConfidence(nil): got %q, want %q", got, TableConfidenceHigh)
	}
	if got := rollupConfidence([]TableWarning{}); got != TableConfidenceHigh {
		t.Errorf("rollupConfidence(empty): got %q, want %q", got, TableConfidenceHigh)
	}
	if got := rollupConfidence([]TableWarning{{Code: TableWarningPhantom}}); got != TableConfidenceLow {
		t.Errorf("rollupConfidence(one warning): got %q, want %q", got, TableConfidenceLow)
	}

	// (2) Invariant on REAL Tables() output: every returned table must satisfy
	//     (len(Warnings) > 0) == (Confidence == Low), and Confidence is always one of
	//     {High, Low}. This exercises the actual projection in Tables() across fixtures.
	fixtures := []struct {
		pdf  string
		page int
	}{
		{"tables/epa-egrid2022-t1.pdf", 1},
		{"tables/irs-soi-inpre-t1-2022.pdf", 1},
		{"tables/fbi-nics-by-state-2026.pdf", 1},
		{"tables/bea-scb-gdp-2024-t1.pdf", 1},
	}
	for _, f := range fixtures {
		t.Run(f.pdf, func(t *testing.T) {
			t.Parallel()
			pg := openCorpusPage(t, f.pdf, f.page)
			tables, err := pg.Tables()
			if err != nil {
				t.Fatalf("Tables: %v", err)
			}
			for i, tbl := range tables {
				switch tbl.Confidence {
				case TableConfidenceHigh, TableConfidenceLow:
				default:
					t.Errorf("table %d: Confidence %q is neither High nor Low", i, tbl.Confidence)
				}
				wantLow := len(tbl.Warnings) > 0
				gotLow := tbl.Confidence == TableConfidenceLow
				if gotLow != wantLow {
					t.Errorf("table %d: Confidence=%q but len(Warnings)=%d — roll-up invariant violated",
						i, tbl.Confidence, len(tbl.Warnings))
				}
			}
		})
	}
}

// ── TableRegions length ───────────────────────────────────────────────────────

// TestTableRegionsLength verifies the 1:1 invariant: len(TableRegions()) == len(Tables())
// for every page of the three tuning fixtures.
func TestTableRegionsLength(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		pdf  string
		page int
	}{
		{"epa-egrid", "tables/epa-egrid2022-t1.pdf", 1},
		{"irs-soi", "tables/irs-soi-inpre-t1-2022.pdf", 1},
		{"fbi-nics", "tables/fbi-nics-by-state-2026.pdf", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pg := openCorpusPage(t, tc.pdf, tc.page)
			tables, err := pg.Tables()
			if err != nil {
				t.Fatalf("Tables: %v", err)
			}
			regions, err := pg.TableRegions()
			if err != nil {
				t.Fatalf("TableRegions: %v", err)
			}
			if len(regions) != len(tables) {
				t.Errorf("len(TableRegions)=%d, len(Tables)=%d — must be 1:1",
					len(regions), len(tables))
			}
		})
	}
}

// ── Non-rotated coordinate self-consistency ───────────────────────────────────

// TestTableRegionsNonRotated verifies that TableRegions() returns a non-vacuous,
// correctly-oriented Rect in portrait display space for a non-rotated fixture.
// It checks:
//
//	(a) Min.X < Max.X and Min.Y < Max.Y (well-formed rect)
//	(b) at least one word from Page.Words() has its center inside the region
//	    (the word assembler and the region share the same display-space frame)
func TestTableRegionsNonRotated(t *testing.T) {
	t.Parallel()

	// EPA egrid2022 t1: a fully-ruled non-rotated table.
	pg := openCorpusPage(t, "tables/epa-egrid2022-t1.pdf", 1)

	regions, err := pg.TableRegions()
	if err != nil {
		t.Fatalf("TableRegions: %v", err)
	}
	if len(regions) == 0 {
		t.Fatal("no regions — EPA fixture should yield at least one table")
	}

	// (a) Well-formed rects.
	for i, r := range regions {
		if r.Rect.Min.X >= r.Rect.Max.X {
			t.Errorf("region %d: Min.X=%.2f >= Max.X=%.2f — vacuous X range",
				i, r.Rect.Min.X, r.Rect.Max.X)
		}
		if r.Rect.Min.Y >= r.Rect.Max.Y {
			t.Errorf("region %d: Min.Y=%.2f >= Max.Y=%.2f — vacuous Y range",
				i, r.Rect.Min.Y, r.Rect.Max.Y)
		}
	}

	// (b) At least one word center inside at least one region.
	words, err := pg.Words()
	if err != nil {
		t.Fatalf("Words: %v", err)
	}
	found := false
outer:
	for _, w := range words {
		cx := w.X + w.W/2
		cy := w.Y + w.H/2
		for _, r := range regions {
			if cx >= r.Rect.Min.X && cx <= r.Rect.Max.X &&
				cy >= r.Rect.Min.Y && cy <= r.Rect.Max.Y {
				found = true
				break outer
			}
		}
	}
	if !found {
		t.Error("no word center falls inside any TableRegion — coordinate frame mismatch")
	}
}

// ── Rotated coordinate self-consistency ──────────────────────────────────────

// TestTableRegionsRotated verifies that TableRegions() returns a region in PORTRAIT
// display space when the table is embedded as a 90°-CCW landscape table on a portrait page.
//
// This is the critical frame-inversion test. A wrong-frame region (landscape display or
// landscape top-origin) would NOT contain the raw portrait glyph centers, so failing this
// test proves a coordinate bug without requiring a render or a real PDF.
//
// Synthetic setup (portrait MediaBox [0,0,612,792], lly=0, urx=612):
//
//	Portrait strokes that form a 2-row × 1-col lattice in the landscape frame:
//	  • Landscape H rules at Y=480, 430, 380 (top, mid, bottom) → portrait V strokes
//	  • Landscape V rules at X=70, X=150 (left, right) → portrait H strokes
//	Portrait strokes (pre-transformation):
//	  (132,70)-(132,150), (182,70)-(182,150), (232,70)-(232,150)  [portrait V = landscape H]
//	  (232,70)-(132,70),  (232,150)-(132,150)                     [portrait H = landscape V]
//
//	Portrait glyphs at Rotation=90 for cell [0] (landscape Y≈455) and cell [1] (landscape Y≈405):
//	  Cell [0]: portrait (157, Y) where Y ∈ {100,110,120}  → landscape (Y, 455)
//	  Cell [1]: portrait (207, Y) where Y ∈ {100,110,120}  → landscape (Y, 405)
//
// Expected portrait region (from cellsUnionRectRotated, lly=0, urx=612):
//
//	cells: minTop=−480, maxBottom=−380, minX0=70, maxX1=150
//	Min: {X: 612+(−480)=132, Y: 70+0=70}
//	Max: {X: 612+(−380)=232, Y: 150+0=150}
//
// Portrait glyph centers to check: X=157 or 207, Y=104..124 — all inside [132,232]×[70,150].
// Wrong-frame region (landscape display [70,380,150,480]) would fail for X=157 > 150.
func TestTableRegionsRotated(t *testing.T) {
	t.Parallel()

	const h float64 = 8
	media := [4]float64{0, 0, 612, 792}

	// Portrait strokes forming a 2-row × 1-col landscape lattice after de-rotation.
	// See function doc for derivation.
	strokes := []Stroke{
		{From: Point{X: 132, Y: 70}, To: Point{X: 132, Y: 150}},  // landscape H top
		{From: Point{X: 182, Y: 70}, To: Point{X: 182, Y: 150}},  // landscape H mid
		{From: Point{X: 232, Y: 70}, To: Point{X: 232, Y: 150}},  // landscape H bottom
		{From: Point{X: 232, Y: 70}, To: Point{X: 132, Y: 70}},   // landscape V left
		{From: Point{X: 232, Y: 150}, To: Point{X: 132, Y: 150}}, // landscape V right
	}

	// Six 90°-CCW glyphs — 3 per landscape cell, each at a different portrait Y so
	// they land in the correct landscape row after rotPoint90CCW.
	// Cell [0]: portrait X=157, Y=100..120 → landscape Y=455 (612−157=455)
	// Cell [1]: portrait X=207, Y=100..120 → landscape Y=405 (612−207=405)
	var texts []Text
	for _, portraitX := range []float64{157, 207} {
		for _, portraitY := range []float64{100, 110, 120} {
			texts = append(texts, Text{
				S:        "x",
				Rotation: 90,
				X:        portraitX,
				Y:        portraitY,
				W:        0,
				H:        h,
				FontSize: 0,
			})
		}
	}

	c := Content{Text: texts, Stroke: strokes}
	results, err := reconstructTablesFromContent(c, media)
	if err != nil {
		t.Fatalf("reconstructTablesFromContent: %v", err)
	}
	if len(results) == 0 {
		// HARD FAIL, never skip: this is a release gate for the rotated-frame coordinate
		// conversion (the #1 bug risk). A skip would silently drop that coverage if a future
		// change broke the synthetic lattice. The pure-input TestCellsUnionRectRotated below
		// guarantees the conversion is covered even independently of lattice reconstruction.
		t.Fatal("synthetic rotated lattice produced 0 tables — rotated-coordinate gate cannot run")
	}

	// Find the result with the largest grid (most cells) to test.
	best := results[0]
	for _, r := range results[1:] {
		if len(r.grid)*len(r.grid[0]) > len(best.grid)*len(best.grid[0]) {
			best = r
		}
	}

	// (a) Well-formed region.
	if best.region.Min.X >= best.region.Max.X {
		t.Errorf("region X vacuous: Min.X=%.2f >= Max.X=%.2f", best.region.Min.X, best.region.Max.X)
	}
	if best.region.Min.Y >= best.region.Max.Y {
		t.Errorf("region Y vacuous: Min.Y=%.2f >= Max.Y=%.2f", best.region.Min.Y, best.region.Max.Y)
	}

	// (b) Portrait glyph centers must be inside the portrait region.
	// Portrait center: (portraitX + W/2, portraitY + H/2) ≈ (157, 104) etc.
	// Wrong-frame landscape region [70,380,150,480] would fail for X=157>150.
	for _, portraitX := range []float64{157, 207} {
		for _, portraitY := range []float64{100, 110, 120} {
			cx := portraitX // W=0
			cy := portraitY + h/2
			if cx < best.region.Min.X || cx > best.region.Max.X ||
				cy < best.region.Min.Y || cy > best.region.Max.Y {
				t.Errorf("portrait glyph center (%.1f, %.1f) not inside region %v — coordinate frame mismatch (wrong frame would fail for X=%.1f > %.1f)",
					cx, cy, best.region, cx, best.region.Max.X)
			}
		}
	}

	// (c) Sanity: the region Max.X should be near 232 (urx + maxBottom = 612 + (−380) = 232).
	// Allow 3 pt merge tolerance from mergeEdges — the exact value may shift slightly.
	const tol = 5.0
	if math.Abs(best.region.Max.X-232) > tol {
		t.Logf("region.Max.X=%.2f (expected ≈232 ± %.0f)", best.region.Max.X, tol)
	}
}

// ── Warnings end-to-end (the feature's headline path) ─────────────────────────

// TestTablesWarningsEndToEnd is the end-to-end gate for the Warnings-population path: a
// phantom grid must flow through reconstructTablesFromContent (where detectTableWarnings is
// called) AND through tableResultsToTables (the exact public projection Tables() uses) so that
// the resulting Table carries the phantom warning and Confidence Low. Without this, a regression
// to `Warnings: nil` or a dropped detector call would pass every other test silently (the corpus
// fixtures are all clean → High, empty Warnings).
//
// Synthetic input: a closed 2-row × 5-col lattice with text in ONLY column 0 → 4 of 5 columns
// entirely blank → blankCol 0.8 ≥ 0.6 → phantom_table.
func TestTablesWarningsEndToEnd(t *testing.T) {
	t.Parallel()

	media := [4]float64{0, 0, 612, 792}
	xs := []float64{50, 90, 130, 170, 210, 250} // 6 vertical rules → 5 columns
	ys := []float64{100, 130, 160}              // 3 horizontal rules → 2 rows
	var strokes []Stroke
	for _, x := range xs {
		strokes = append(strokes, Stroke{From: Point{X: x, Y: ys[0]}, To: Point{X: x, Y: ys[len(ys)-1]}})
	}
	for _, y := range ys {
		strokes = append(strokes, Stroke{From: Point{X: xs[0], Y: y}, To: Point{X: xs[len(xs)-1], Y: y}})
	}
	// Text only in column 0 (x∈[50,90]); the other 4 columns stay entirely blank.
	texts := []Text{
		{S: "A", X: 62, Y: 142, W: 8, H: 10, FontSize: 10},
		{S: "B", X: 62, Y: 112, W: 8, H: 10, FontSize: 10},
	}

	results, err := reconstructTablesFromContent(Content{Text: texts, Stroke: strokes}, media)
	if err != nil {
		t.Fatalf("reconstructTablesFromContent: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("synthetic phantom lattice produced 0 tables — geometry needs adjustment")
	}

	// reconstructTablesFromContent must have run the detector on the produced grid.
	phantomInResults := false
	for _, r := range results {
		for _, w := range r.warnings {
			if w.Code == TableWarningPhantom {
				phantomInResults = true
			}
		}
	}
	if !phantomInResults {
		dims := make([][2]int, len(results))
		for i, r := range results {
			cols := 0
			if len(r.grid) > 0 {
				cols = len(r.grid[0])
			}
			dims[i] = [2]int{len(r.grid), cols}
		}
		t.Fatalf("no phantom_table warning in reconstructTablesFromContent results — detector call-site not exercised (grid dims rows×cols: %v)", dims)
	}

	// The public projection must surface the phantom on the Table itself.
	tables := tableResultsToTables(results)
	surfaced := false
	for _, tbl := range tables {
		if len(tbl.Warnings) == 0 {
			continue
		}
		if tbl.Confidence != TableConfidenceLow {
			t.Errorf("table has Warnings %v but Confidence=%q (want Low)", tbl.Warnings, tbl.Confidence)
		}
		for _, w := range tbl.Warnings {
			if w.Code == TableWarningPhantom {
				surfaced = true
			}
		}
	}
	if !surfaced {
		t.Error("phantom_table warning did not surface on any Table — projection wiring (Warnings/Confidence) broken")
	}
}

// ── Pure coordinate-helper unit tests (never skip) ────────────────────────────

// TestCellsUnionRect pins the non-rotated top-origin → display-space (Y-up) conversion
// with explicit lCell inputs. This is robust coverage that does not depend on lattice
// reconstruction succeeding (unlike the integration tests), so the conversion can never
// go silently untested.
func TestCellsUnionRect(t *testing.T) {
	t.Parallel()

	// Two cells spanning x∈[10,90], top-origin top∈[-30], bottom∈[-20].
	// display Y = −top_origin: visual top −30 → Max.Y 30; visual bottom −20 → Min.Y 20.
	cells := []lCell{
		{x0: 10, top: -30, x1: 50, bottom: -20},
		{x0: 50, top: -25, x1: 90, bottom: -20},
	}
	got := cellsUnionRect(cells)
	want := Rect{Min: Point{X: 10, Y: 20}, Max: Point{X: 90, Y: 30}}
	if got != want {
		t.Errorf("cellsUnionRect: got %+v, want %+v", got, want)
	}

	// Empty input → zero Rect.
	if z := cellsUnionRect(nil); z != (Rect{}) {
		t.Errorf("cellsUnionRect(nil): got %+v, want zero Rect", z)
	}
}

// TestCellsUnionRectRotated pins the de-rotated (landscape top-origin) → portrait
// display-space inversion with explicit lCell inputs, exercising the ORIGINAL-MediaBox
// (lly, urx) terms directly. Derivation (function doc):
//
//	portrait_x = urx + top_origin ; portrait_y = landscape_x + lly
func TestCellsUnionRectRotated(t *testing.T) {
	t.Parallel()

	// lly=0, urx=612. cells: minTop=−480, maxBottom=−380, minX0=70, maxX1=150.
	// Min{X: 612+(−480)=132, Y: 70+0=70}; Max{X: 612+(−380)=232, Y: 150+0=150}.
	cells := []lCell{
		{x0: 70, top: -480, x1: 150, bottom: -430},
		{x0: 70, top: -430, x1: 150, bottom: -380},
	}
	got := cellsUnionRectRotated(cells, 0, 612)
	want := Rect{Min: Point{X: 132, Y: 70}, Max: Point{X: 232, Y: 150}}
	if got != want {
		t.Errorf("cellsUnionRectRotated(lly=0,urx=612): got %+v, want %+v", got, want)
	}

	// Non-zero lly and a different urx exercise both offset terms independently.
	// lly=10, urx=600, single cell top=−480 bottom=−380 x0=70 x1=150:
	// Min{X: 600−480=120, Y: 70+10=80}; Max{X: 600−380=220, Y: 150+10=160}.
	one := []lCell{{x0: 70, top: -480, x1: 150, bottom: -380}}
	got2 := cellsUnionRectRotated(one, 10, 600)
	want2 := Rect{Min: Point{X: 120, Y: 80}, Max: Point{X: 220, Y: 160}}
	if got2 != want2 {
		t.Errorf("cellsUnionRectRotated(lly=10,urx=600): got %+v, want %+v", got2, want2)
	}

	// Empty input → zero Rect.
	if z := cellsUnionRectRotated(nil, 0, 612); z != (Rect{}) {
		t.Errorf("cellsUnionRectRotated(nil): got %+v, want zero Rect", z)
	}
}

// ── No-harm byte-identical gate ───────────────────────────────────────────────

// TestTablesCellsNoHarm is the byte-identical gate for the reconstructTables() refactoring.
// It proves that Table.Cells is byte-identical across the tuning fixtures and three held-out
// fixtures after the Confidence/Warnings fields and the reconstructTables() helper are added.
//
// Mechanism: SHA-256 of json.Marshal of the FULL ordered projection of EVERY table's Cells —
// [][][]string{tables[0].Cells, tables[1].Cells, ...} — plus the table count, for each fixture.
// Any cell mutation, table drop/add/reorder/duplicate, or count change flips the hash (or the
// count) and fails the test. Hashing the whole ordered set (not just the largest table) closes
// the non-largest / order / count blind spot.
//
// The golden is generated from the PRE-refactor (master) Tables() output, so this is an
// INDEPENDENT no-harm proof (branch reproduces master byte-for-byte), not a self-referential
// determinism check. To regenerate from master: check out master in a worktree, run the
// equivalent all-tables hash, and write testdata/tables_confidence_cells.golden.json.
//
// To recapture on the current tree (regression-lock only, NOT a vs-master proof):
//
//	go test -run TestTablesCellsNoHarm -update .
func TestTablesCellsNoHarm(t *testing.T) {
	t.Parallel()

	type fixtureGolden struct {
		PDF     string `json:"pdf"`
		Page    int    `json:"page"`
		Hash    string `json:"hash"`     // hex SHA-256 of json.Marshal([][][]string{all tables' Cells, ordered})
		NTables int    `json:"n_tables"` // table count (explicit count-regression guard)
	}

	const goldenPath = "testdata/tables_confidence_cells.golden.json"

	// Fixtures: the 3 tuning sources + FBI NICS and HHS ASPE (held-out fully-ruled)
	// + BEA per-cell-grid (held-out group-ruled+banded).
	type tc struct {
		pdf  string
		page int
	}
	fixtures := []tc{
		{"tables/epa-egrid2022-t1.pdf", 1},
		{"tables/irs-soi-inpre-t1-2022.pdf", 1},
		{"tables/fbi-nics-by-state-2026.pdf", 1},
		{"tables/hhs-aspe-vsl-2024.pdf", 1},
		{"tables/bea-scb-gdp-2024-t1.pdf", 1},
	}

	// hashAllTables hashes the FULL ordered projection of every table's Cells on the page —
	// json.Marshal([][][]string{tables[0].Cells, tables[1].Cells, ...}) — so a dropped,
	// reordered, duplicated, or mutated table in ANY position changes the hash. It also
	// returns the table count for an explicit count-regression message. This is stronger than
	// hashing only the largest table (which would miss non-largest, count, and order changes).
	hashAllTables := func(t *testing.T, pdfRel string, page int) (string, int) {
		t.Helper()
		pg := openCorpusPage(t, pdfRel, page)
		tables, err := pg.Tables()
		if err != nil {
			t.Fatalf("Tables %s p%d: %v", pdfRel, page, err)
		}
		allCells := make([][][]string, len(tables))
		for i, tbl := range tables {
			allCells[i] = tbl.Cells
		}
		b, err := json.Marshal(allCells)
		if err != nil {
			t.Fatalf("json.Marshal Cells %s: %v", pdfRel, err)
		}
		sum := sha256.Sum256(b)
		return fmt.Sprintf("%x", sum), len(tables)
	}

	if *updateGolden {
		var goldens []fixtureGolden
		for _, f := range fixtures {
			h, n := hashAllTables(t, f.pdf, f.page)
			goldens = append(goldens, fixtureGolden{PDF: f.pdf, Page: f.page, Hash: h, NTables: n})
		}
		data, err := json.MarshalIndent(goldens, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		if err := os.WriteFile(goldenPath, data, 0o600); err != nil { //nolint:gosec // golden file, test-only
			t.Fatalf("write golden %s: %v", goldenPath, err)
		}
		t.Logf("wrote %s with %d fixture hashes", goldenPath, len(goldens))
		return
	}

	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s — run 'go test -run TestTablesCellsNoHarm -update .' first: %v",
			goldenPath, err)
	}
	var goldens []fixtureGolden
	if err := json.Unmarshal(data, &goldens); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}

	byKey := make(map[string]fixtureGolden, len(goldens))
	for _, g := range goldens {
		byKey[fmt.Sprintf("%s|p%d", g.PDF, g.Page)] = g
	}

	for _, f := range fixtures {
		t.Run(f.pdf, func(t *testing.T) {
			t.Parallel()
			key := fmt.Sprintf("%s|p%d", f.pdf, f.page)
			want, ok := byKey[key]
			if !ok {
				t.Fatalf("fixture %s not found in golden — re-run with -update", key)
			}
			got, n := hashAllTables(t, f.pdf, f.page)
			if n != want.NTables {
				t.Errorf("table COUNT changed: got %d, want %d — the refactor must not add/drop tables", n, want.NTables)
			}
			if got != want.Hash {
				t.Errorf("Table.Cells changed (all-tables ordered hash mismatch):\n  got  %s\n  want %s\nThe reconstructTables() refactoring must not change any cell string, table order, or count.",
					got, want.Hash)
			}
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// openCorpusPage opens a corpus PDF and returns the (1-based) Page.
// It calls t.Fatal on any error.
func openCorpusPage(t *testing.T, rel string, page int) Page {
	t.Helper()
	//nolint:gosec // G304: fixed corpus path, not user input
	fh, err := os.Open(corpusPath(rel))
	if err != nil {
		t.Fatalf("open %s: %v", rel, err)
	}
	t.Cleanup(func() { _ = fh.Close() })
	fi, err := fh.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	r, err := NewReader(fh, fi.Size())
	if err != nil {
		t.Fatalf("NewReader %s: %v", rel, err)
	}
	return r.Page(page)
}
