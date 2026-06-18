// Package pdf — corpus integrity validator for .cellgrid.tsv fixtures.
//
// This file validates the structural integrity of the cell-grid corpus:
// it parses each .cellgrid.tsv file, checks its declared
// dims/header_rows match reality, and confirms that every grid's source PDF
// is present in corpusManifest and openable. It is NOT an accuracy scorer —
// no table-detector API exists yet, so no extraction-vs-ground-truth
// comparison is performed. The accuracy scorer is a follow-up blocked on
// the table-detector API.
//
// The parser (parseCellGrid and helpers) is test-only; it has no public API.
package pdf

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cellGrid holds a parsed .cellgrid.tsv v1 file.
// cells contains all rows (header + data), each with exactly cols fields.
// knownCeiling is populated from optional "# known-ceiling:" lines.
type cellGrid struct {
	rows, cols, headerRows, pdfPage int
	cells                           [][]string
	knownCeiling                    []ceilingMark
}

// ceilingMark records a single "# known-ceiling:" annotation (1-based coords).
type ceilingMark struct {
	row, col int
	reason   string
}

// reCeiling matches "known-ceiling: <reason> @ r<N>c<M>".
var reCeiling = regexp.MustCompile(`^known-ceiling:\s*(.+?)\s*@\s*r(\d+)c(\d+)\s*$`)

// parseCeilingMark parses the text after "# " for a known-ceiling line.
// Returns an error if the format doesn't match.
func parseCeilingMark(text string) (ceilingMark, error) {
	m := reCeiling.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return ceilingMark{}, fmt.Errorf("cellgrid: malformed known-ceiling line: %q", text)
	}
	row, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])
	return ceilingMark{row: row, col: col, reason: m[1]}, nil
}

// parseCellGridMeta extracts the key=value metadata from comment tokens.
// Lines starting with "#" are processed here: "known-ceiling:" lines are
// dispatched to parseCeilingMark; other lines are split on "|" and each
// token on the first "=", storing into meta. A token with no "=" is skipped
// (handles "# cellgrid v1" style version lines).
func parseCellGridMeta(lines []string) (meta map[string]string, ceilings []ceilingMark, err error) {
	meta = make(map[string]string)
	for _, line := range lines {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		// Strip leading "# " or "#"
		body := strings.TrimPrefix(line, "#")
		body = strings.TrimPrefix(body, " ")
		if strings.HasPrefix(body, "known-ceiling:") {
			cm, parseErr := parseCeilingMark(body)
			if parseErr != nil {
				return nil, nil, parseErr
			}
			ceilings = append(ceilings, cm)
			continue
		}
		for token := range strings.SplitSeq(body, "|") {
			token = strings.TrimSpace(token)
			parts := strings.SplitN(token, "=", 2)
			if len(parts) != 2 {
				continue // e.g. "cellgrid v1" version marker
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key != "" {
				meta[key] = val
			}
		}
	}
	return meta, ceilings, nil
}

// validateCellGridDims checks that cells matches declared row/col counts
// and that headerRows is in range. Returned errors identify the violation.
func validateCellGridDims(cells [][]string, rows, cols, headerRows int) error {
	if len(cells) != rows {
		return fmt.Errorf("cellgrid: dims declares %d rows but file has %d data rows", rows, len(cells))
	}
	for i, row := range cells {
		if len(row) != cols {
			return fmt.Errorf("cellgrid: row %d has %d fields, want %d (cols)", i+1, len(row), cols)
		}
	}
	if headerRows < 0 || headerRows >= rows {
		return fmt.Errorf("cellgrid: header_rows=%d out of range [0, %d)", headerRows, rows)
	}
	return nil
}

// validateCeilingMarks checks each ceilingMark against the declared grid bounds.
func validateCeilingMarks(marks []ceilingMark, rows, cols int) error {
	for _, cm := range marks {
		if cm.row < 1 || cm.row > rows {
			return fmt.Errorf("cellgrid: known-ceiling row %d out of range [1, %d]", cm.row, rows)
		}
		if cm.col < 1 || cm.col > cols {
			return fmt.Errorf("cellgrid: known-ceiling col %d out of range [1, %d]", cm.col, cols)
		}
	}
	return nil
}

// parseDims parses a "NxM" dims string into (rows, cols).
func parseDims(s string) (rows, cols int, err error) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("cellgrid: malformed dims %q, want NxM", s)
	}
	rows, errR := strconv.Atoi(parts[0])
	cols, errC := strconv.Atoi(parts[1])
	if errR != nil || errC != nil {
		return 0, 0, fmt.Errorf("cellgrid: malformed dims %q, want NxM", s)
	}
	return rows, cols, nil
}

// parseCellGrid parses a .cellgrid.tsv v1 byte slice into a cellGrid.
// It validates structural integrity: dims must match actual row/col counts,
// header_rows must be in range, and any known-ceiling coords must be in bounds.
// An error is returned for any violation; these errors are exercised by
// TestParseCellGrid (every branch is reachable from a unit test).
func parseCellGrid(data []byte) (cellGrid, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	meta, ceilings, err := parseCellGridMeta(lines)
	if err != nil {
		return cellGrid{}, err
	}

	// Parse dims — required.
	dimsStr, ok := meta["dims"]
	if !ok {
		return cellGrid{}, fmt.Errorf("cellgrid: missing required key 'dims'")
	}
	rows, cols, err := parseDims(dimsStr)
	if err != nil {
		return cellGrid{}, err
	}

	// Parse header_rows — required.
	hrStr, ok := meta["header_rows"]
	if !ok {
		return cellGrid{}, fmt.Errorf("cellgrid: missing required key 'header_rows'")
	}
	headerRows, err := strconv.Atoi(strings.TrimSpace(hrStr))
	if err != nil {
		return cellGrid{}, fmt.Errorf("cellgrid: invalid header_rows %q: %v", hrStr, err)
	}

	// Parse pdf_page — optional.
	var pdfPage int
	if ppStr, ok := meta["pdf_page"]; ok {
		pdfPage, err = strconv.Atoi(strings.TrimSpace(ppStr))
		if err != nil {
			return cellGrid{}, fmt.Errorf("cellgrid: invalid pdf_page %q: %v", ppStr, err)
		}
	}

	// Collect grid rows: non-comment, non-blank lines. Do NOT trim — leading/
	// trailing tabs encode empty cells in spanning-header rows (EIA fixture row 6).
	var cells [][]string
	for _, line := range lines {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		cells = append(cells, strings.Split(line, "\t"))
	}

	if err := validateCellGridDims(cells, rows, cols, headerRows); err != nil {
		return cellGrid{}, err
	}
	if err := validateCeilingMarks(ceilings, rows, cols); err != nil {
		return cellGrid{}, err
	}

	return cellGrid{
		rows:         rows,
		cols:         cols,
		headerRows:   headerRows,
		pdfPage:      pdfPage,
		cells:        cells,
		knownCeiling: ceilings,
	}, nil
}

// cellgridFixture is the registry entry for one .cellgrid.tsv fixture.
// path is relative to corpusRoot. sourcePDF is the corpusManifest Path the
// grid was authored against. rows/cols/headerRows are the declared dims —
// TestCorpusCellGridFixtures asserts the parsed values match these (locking
// the file against silent drift).
//
// class is the table-type taxonomy class ("fully-ruled", "rect-bordered",
// "group-ruled+banded", "borderless"). heldOut marks a fixture that is NOT a
// threshold-tuning source: the held-out set is the per-class quality corpus
// scored by TestPublicTablesQualityCorpus. The 3 tuned fixtures
// (epa-egrid2022-t1, irs-soi-inpre-t1-2022, nist-hb44-appc-2026) and the three
// integrity-only fixtures (irs-db, eia-aer, bea-scb-gdp) carry heldOut=false; deriving
// qualityFixtures by filtering heldOut makes orphan held-out entries
// impossible by construction.
type cellgridFixture struct {
	path                   string // .cellgrid.tsv path relative to corpusRoot
	sourcePDF              string // corpusManifest Path the grid was authored against
	rows, cols, headerRows int
	class                  string // table-type taxonomy class
	heldOut                bool   // true = held-out quality fixture (not a tuning source)
	anchorCol              int    // 0-based row-label column used to align golden rows to the grid (default 0)
	// bonus marks a held-out fixture that is SCORED + logged but does NOT count toward its
	// class's coverage gate (e.g. a gate-independent CJK diagnostic). The hard/held coverage
	// decision uses only non-bonus ("gate-bearing") fixtures, so a partial-extraction bonus
	// fixture can never substitute for a genuinely-extracting gate-bearing one.
	bonus bool
}

// cellgridFixtures is the single source of truth for all .cellgrid.tsv
// fixtures. Every .cellgrid.tsv file under testdata/corpus/ must have an
// entry here (enforced by TestCorpusCellGridComplete), and every entry's
// sourcePDF must be in corpusManifest (enforced by
// TestCorpusCellGridFixtures). This is the cross-reference that makes both
// data sets coherent: a grid without a manifest PDF would silently lose its
// extraction anchor.
var cellgridFixtures = []cellgridFixture{
	{
		path:      "tables/irs-db-t4-3-2025.cellgrid.tsv",
		sourcePDF: "tables/irs-db-t4-3-2025.pdf",
		rows:      10, cols: 4, headerRows: 1,
		class: "borderless", heldOut: false, // integrity-only (no accuracy consumer)
	},
	{
		path:      "tables/eia-aer-t3-1-2011.cellgrid.tsv",
		sourcePDF: "tables/eia-aer-t3-1-2011.pdf",
		rows:      45, cols: 10, headerRows: 2,
		class: "group-ruled+banded", heldOut: false, // integrity-only (banded; no accuracy consumer yet)
	},
	{
		path:      "tables/bea-scb-gdp-2024-t1.cellgrid.tsv",
		sourcePDF: "tables/bea-scb-gdp-2024-t1.pdf",
		rows:      36, cols: 11, headerRows: 3,
		class: "group-ruled+banded", heldOut: false, // 2nd banded fixture, cross-publisher (BEA, not EIA); integrity-only held-out target, no accuracy consumer yet
	},
	{
		path:      "tables/epa-egrid2022-t1.cellgrid.tsv",
		sourcePDF: "tables/epa-egrid2022-t1.pdf",
		rows:      31, cols: 17, headerRows: 3,
		class: "fully-ruled", heldOut: false, // tuning source — gated by TestPublicAccuracyEPA
	},
	{
		path:      "tables/irs-soi-inpre-t1-2022.cellgrid.tsv",
		sourcePDF: "tables/irs-soi-inpre-t1-2022.pdf",
		rows:      51, cols: 6, headerRows: 3,
		class: "rect-bordered", heldOut: false, // tuning source — gated by TestPublicAccuracyIRS
	},
	{
		// Held-out quality fixture #1 (fully-ruled, threshold-naïve).
		path:      "tables/fbi-nics-by-state-2026.cellgrid.tsv",
		sourcePDF: "tables/fbi-nics-by-state-2026.pdf",
		rows:      56, cols: 14, headerRows: 1,
		class: "fully-ruled", heldOut: true, anchorCol: 0, // col0 = State/Territory (unique)
	},
	{
		// Held-out quality fixture #2 (fully-ruled) — second gate-bearing fully-ruled
		// fixture; the fully-ruled coverage gate flips hard on NICS + this one.
		path:      "tables/hhs-aspe-vsl-2024.cellgrid.tsv",
		sourcePDF: "tables/hhs-aspe-vsl-2024.pdf",
		rows:      12, cols: 4, headerRows: 1,
		class: "fully-ruled", heldOut: true, anchorCol: 0, // col0 = Year (unique numeric)
	},
	{
		// Held-out quality fixture (fully-ruled, CJK bonus) — Simplified-Chinese rate
		// schedule; class confirmed by rendered rule coverage (interior verticals + per-cell
		// rects), gate-independent (the gate rests on NICS + HHS ASPE).
		path:      "tables/irs-p17zhs-rate-sched-2025.cellgrid.tsv",
		sourcePDF: "tables/irs-p17zhs-rate-sched-2025.pdf",
		rows:      8, cols: 4, headerRows: 1,
		class: "fully-ruled", heldOut: true, anchorCol: 0, // col0 = $-bracket lower bound (unique)
		bonus: true, // CJK diagnostic — scored, but NOT counted toward the fully-ruled coverage gate
	},
	{
		// Held-out quality fixture (rect-bordered) — minimal golden proving the detector
		// gap: the detector drops the open Year column and collapses the rows -> ~0%.
		path:      "tables/erp-2024-tb1-gdp-pctchg.cellgrid.tsv",
		sourcePDF: "tables/erp-2024-tb1-gdp-pctchg.pdf",
		rows:      11, cols: 2, headerRows: 1,
		class: "rect-bordered", heldOut: true, anchorCol: 0, // col0 = Year (unique); detector drops it
	},
	{
		// Held-out quality fixture (rect-bordered) — second ERP table, same gap. Both
		// fixtures are ERP/CEA (single publisher); cross-publisher generalization NOT proven.
		path:      "tables/erp-2024-tb2-gdp-contrib.cellgrid.tsv",
		sourcePDF: "tables/erp-2024-tb2-gdp-contrib.pdf",
		rows:      11, cols: 2, headerRows: 1,
		class: "rect-bordered", heldOut: true, anchorCol: 0, // col0 = Year (unique); detector drops it
	},
	{
		path:      "tables/nass-cropan-2024-planted-harvested.cellgrid.tsv",
		sourcePDF: "tables/nass-cropan-2024-planted-harvested.pdf",
		rows:      53, cols: 7, headerRows: 3,
		class: "rect-bordered", heldOut: true, anchorCol: 0, // col0 = State (unique); cross-publisher (USDA) generalization fixture for the rect-bordered gate
	},
}

// TestCorpusCellGridFixtures is the primary integrity gate for the cell-grid
// corpus. For each registry entry it:
//
//  1. Parses the .cellgrid.tsv and asserts dims/headerRows match the registry
//     (locks the file against silent drift — if someone edits the TSV without
//     updating the registry, this test fails).
//  2. Asserts the grid's sourcePDF is present in corpusManifest (the
//     cross-reference lock: a grid pointing at a missing PDF would silently
//     lose its extraction anchor).
//  3. Opens the sourcePDF via loadCorpus/OpenBytes and asserts NumPage() >=
//     pdfPage when pdfPage > 0 (the page reference must be in range).
//
// This is a corpus INTEGRITY validator, not an accuracy scorer. No
// table-detector API exists yet; extraction-vs-ground-truth comparison is
// a follow-up task.
func TestCorpusCellGridFixtures(t *testing.T) {
	// Build a set of manifest Paths for the cross-reference check.
	manifestPaths := make(map[string]corpusEntry, len(corpusManifest))
	for _, e := range corpusManifest {
		manifestPaths[e.Path] = e
	}

	for _, f := range cellgridFixtures {
		t.Run(f.path, func(t *testing.T) {
			// 1. Parse and validate structural integrity.
			//nolint:gosec // G304: fixed corpus path, not user input
			data, err := os.ReadFile(corpusPath(f.path))
			if err != nil {
				t.Fatalf("read %s: %v", f.path, err)
			}
			g, err := parseCellGrid(data)
			if err != nil {
				t.Fatalf("parseCellGrid(%s): %v", f.path, err)
			}
			if g.rows != f.rows {
				t.Errorf("rows: got %d, want %d", g.rows, f.rows)
			}
			if g.cols != f.cols {
				t.Errorf("cols: got %d, want %d", g.cols, f.cols)
			}
			if g.headerRows != f.headerRows {
				t.Errorf("headerRows: got %d, want %d", g.headerRows, f.headerRows)
			}

			// 1b. Held-out quality fixtures must have a UNIQUE anchor on covered data rows.
			// The quality scorer (scoreQualityFixture) aligns each golden data row to the
			// detector grid by its anchorCol value (anchorRow); a duplicate anchor is scored
			// as a full-row miss, so a non-unique anchor would silently deflate the
			// measurement. Lock it here (A6).
			if f.heldOut {
				assertAnchorUnique(t, f, g)
			}

			// 2. Cross-reference: sourcePDF must be in corpusManifest.
			entry, inManifest := manifestPaths[f.sourcePDF]
			if !inManifest {
				t.Fatalf("sourcePDF %q not found in corpusManifest — add it before the cellgrid fixture", f.sourcePDF)
			}

			// 3. Open the PDF and assert page range.
			r := loadCorpus(t, entry)
			if g.pdfPage > 0 && r.NumPage() < g.pdfPage {
				t.Errorf("NumPage() = %d, want >= pdfPage %d", r.NumPage(), g.pdfPage)
			}
		})
	}
}

// assertAnchorUnique checks that a held-out fixture's anchor column carries a unique
// looseCell value on every covered data row. Header rows, empty-anchor section rows, and
// known-ceiling anchor cells are excluded, mirroring scoreQualityFixture's row loop. A
// duplicate anchor makes anchorRow drop the row (a full-row miss), so this locks the
// fixture's scorability (A6). looseCell is the scorer's own folding, so uniqueness here
// means uniqueness as the scorer sees it.
func assertAnchorUnique(t *testing.T, f cellgridFixture, g cellGrid) {
	t.Helper()
	ceil := make(map[[2]int]bool, len(g.knownCeiling))
	for _, cm := range g.knownCeiling {
		ceil[[2]int{cm.row, cm.col}] = true
	}
	seen := make(map[string]bool)
	for gr := g.headerRows; gr < g.rows; gr++ {
		if f.anchorCol >= len(g.cells[gr]) {
			continue
		}
		raw := strings.TrimSpace(g.cells[gr][f.anchorCol])
		if raw == "" || ceil[[2]int{gr + 1, f.anchorCol + 1}] {
			continue
		}
		key := looseCell(raw)
		if seen[key] {
			t.Errorf("%s: anchor value %q (col %d) is not unique on covered rows — anchorRow would drop it",
				f.path, raw, f.anchorCol)
		}
		seen[key] = true
	}
}

// TestCorpusCellGridComplete is the anti-orphan gate for the cell-grid
// corpus. It mirrors TestCorpusManifestComplete:
//
//   - Every .cellgrid.tsv file discovered under corpusRoot must have a
//     registry entry in cellgridFixtures (no orphan files that are never
//     validated).
//   - Every registry entry's path must exist on disk (no stale entries
//     pointing at deleted files).
func TestCorpusCellGridComplete(t *testing.T) {
	// Build a set of registered paths for the orphan check.
	registered := make(map[string]bool, len(cellgridFixtures))
	for _, f := range cellgridFixtures {
		registered[filepath.ToSlash(f.path)] = true
		// Also assert the file exists on disk.
		if _, err := os.Stat(corpusPath(f.path)); err != nil {
			t.Errorf("registry entry path missing on disk: %s (%v)", f.path, err)
		}
	}

	// Walk corpus for .cellgrid.tsv files not in the registry.
	err := filepath.WalkDir(corpusRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".cellgrid.tsv") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, corpusRoot+string(filepath.Separator)))
		if !registered[rel] {
			t.Errorf(".cellgrid.tsv on disk has no registry entry: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
}

// parseCellGridCase is one row of the TestParseCellGrid table.
// Zero values for numeric want-fields mean "do not assert".
type parseCellGridCase struct {
	name    string
	input   string
	wantErr bool
	// Fields to assert on success (zero value = not asserted).
	wantRows, wantCols, wantHeaderRows, wantPDFPage int
	wantCeilings                                    int // len(knownCeiling)
	wantCeilingRow, wantCeilingCol                  int
	wantCeilingReason                               string
}

// assertParseCellGridOK asserts the scalar and ceiling fields of a successfully
// parsed grid against a test case. Split out of TestParseCellGrid to keep that
// function's cyclomatic complexity below the gocyclo 15 threshold.
func assertParseCellGridOK(t *testing.T, g cellGrid, tc parseCellGridCase) {
	t.Helper()
	if tc.wantRows != 0 && g.rows != tc.wantRows {
		t.Errorf("rows: got %d, want %d", g.rows, tc.wantRows)
	}
	if tc.wantCols != 0 && g.cols != tc.wantCols {
		t.Errorf("cols: got %d, want %d", g.cols, tc.wantCols)
	}
	if tc.wantHeaderRows != 0 && g.headerRows != tc.wantHeaderRows {
		t.Errorf("headerRows: got %d, want %d", g.headerRows, tc.wantHeaderRows)
	}
	if tc.wantPDFPage != 0 && g.pdfPage != tc.wantPDFPage {
		t.Errorf("pdfPage: got %d, want %d", g.pdfPage, tc.wantPDFPage)
	}
	if tc.wantCeilings == 0 {
		return
	}
	if len(g.knownCeiling) != tc.wantCeilings {
		t.Fatalf("knownCeiling len: got %d, want %d", len(g.knownCeiling), tc.wantCeilings)
	}
	cm := g.knownCeiling[0]
	if cm.row != tc.wantCeilingRow {
		t.Errorf("knownCeiling[0].row: got %d, want %d", cm.row, tc.wantCeilingRow)
	}
	if cm.col != tc.wantCeilingCol {
		t.Errorf("knownCeiling[0].col: got %d, want %d", cm.col, tc.wantCeilingCol)
	}
	if cm.reason != tc.wantCeilingReason {
		t.Errorf("knownCeiling[0].reason: got %q, want %q", cm.reason, tc.wantCeilingReason)
	}
}

// TestParseCellGrid is a table-driven unit test over synthetic in-memory
// inputs. It exercises every error branch of parseCellGrid and the happy
// path, without touching the filesystem. The error cases below are what make
// the parser's validation branches non-vacuous: each case is the minimal
// input that reaches exactly one error path.
func TestParseCellGrid(t *testing.T) {
	// happy is a minimal valid 2x2 grid used as the base for error cases.
	const happy = "# dims=2x2 | header_rows=1 | pdf_page=3\na\tb\nc\td\n"

	// happyWithCeiling adds a known-ceiling annotation to the happy base.
	const happyWithCeiling = "# dims=2x2 | header_rows=1\n# known-ceiling: test reason @ r1c2\na\tb\nc\td\n"

	cases := []parseCellGridCase{
		{
			name:           "valid minimal grid",
			input:          happy,
			wantRows:       2,
			wantCols:       2,
			wantHeaderRows: 1,
			wantPDFPage:    3,
		},
		{
			name:              "valid grid with known-ceiling",
			input:             happyWithCeiling,
			wantRows:          2,
			wantCols:          2,
			wantHeaderRows:    1,
			wantCeilings:      1,
			wantCeilingRow:    1,
			wantCeilingCol:    2,
			wantCeilingReason: "test reason",
		},
		{
			// Row has wrong number of fields — reaches field-count validation.
			// dims and header_rows are valid so parsing reaches the row check.
			name:    "wrong field count in a row",
			input:   "# dims=2x2 | header_rows=1\na\tb\tc\nd\te\n",
			wantErr: true,
		},
		{
			// Declares 3 rows but only 2 data rows present.
			name:    "dims row-count mismatch",
			input:   "# dims=3x2 | header_rows=1\na\tb\nc\td\n",
			wantErr: true,
		},
		{
			// header_rows >= rows (1 header_rows out of 1 total row).
			name:    "header_rows equals rows",
			input:   "# dims=1x2 | header_rows=1\na\tb\n",
			wantErr: true,
		},
		{
			// dims value is not in NxM form.
			name:    "malformed dims non-NxM",
			input:   "# dims=2 | header_rows=1\na\tb\nc\td\n",
			wantErr: true,
		},
		{
			// dims key is absent entirely.
			name:    "missing dims key",
			input:   "# header_rows=1\na\tb\nc\td\n",
			wantErr: true,
		},
		{
			// header_rows key is absent.
			name:    "missing header_rows key",
			input:   "# dims=2x2\na\tb\nc\td\n",
			wantErr: true,
		},
		{
			// known-ceiling row coordinate is out of range.
			name:    "known-ceiling row out of range",
			input:   "# dims=2x2 | header_rows=1\n# known-ceiling: bad @ r5c1\na\tb\nc\td\n",
			wantErr: true,
		},
		{
			// known-ceiling col coordinate is out of range.
			name:    "known-ceiling col out of range",
			input:   "# dims=2x2 | header_rows=1\n# known-ceiling: bad @ r1c9\na\tb\nc\td\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseCellGrid([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseCellGrid: want error, got nil (grid=%+v)", g)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCellGrid: unexpected error: %v", err)
			}
			assertParseCellGridOK(t, g, tc)
		})
	}
}
