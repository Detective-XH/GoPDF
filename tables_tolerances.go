package pdf

// Calibrated tolerances for the tables pipeline, collected in one place (C3 Stage 2). Values
// are load-bearing calibration data — never retune without the full extraction-changing gate
// (plans/VERIFICATION.md §5 row 1). New predicates corroborate against these, never fresh
// epsilons (§7).

// --- Stage 1: closed-lattice core (tables_lattice_core.go) ---

// latticeSnapTol and latticeJoinTol are the pdfplumber "lines"-strategy spec-port constants
// (snap=join=3 pt) — a DIFFERENT calibration lineage from rectRowSnapTol's 3.0; deliberately
// NOT unified. They feed mergeEdges(edgesFromContent(c), latticeSnapTol, latticeJoinTol) and
// edgesToIntersections's xTol/yTol in latticeTables, verticalRules (tables_lattice_core.go),
// and latticeTablesOpen (tables_recover_open.go).
const (
	latticeSnapTol = 3.0
	latticeJoinTol = 3.0
)

// --- Stage 2 pass: open-column recovery (tables_recover_open.go) ---

// Constants for open-column recovery (the only tunables; locked by the synthetic guard tests).
//
//	overhangTol — minimum rule extension past vMin/vMax to count as a real overhang.
//	              = 2×rectRowSnapTol = 6 pt (now compiler-enforced: overhangTol's declaration
//	              is the computed form 2*rectRowSnapTol, so the two constants can never drift
//	              apart silently). Filters snap-noise artifacts (NIST ~1 pt < 6 pt) while
//	              admitting genuine overhangs (IRS ~258 pt >> 6 pt).
//	              calibrated: NIST (~1pt snap noise, rejected) vs IRS (~258pt genuine overhang,
//	              admitted).
//	minOpenRows — open-side words must span >= this many admitted bands (rejects a
//	              single marginal note outside the outer rule).
//	              provenance: pre-split tables_lattice.go
//	minOpenColW — reject a sub-rule sliver narrower than 2× rectRowSnapTol (6 pt).
//	              provenance: pre-split tables_lattice.go
//	openPad     — outward pad so near-boundary word anchors are fully contained; the
//	              INNER side gets NO pad — it is exactly vMin/vMax so the synthesized cell
//	              never overlaps any interior cell.
//	              provenance: pre-split tables_lattice.go
//
// minOpenColW and openPad are also read by synthOpenColumns (tables_recover_rows.go), the
// rect-bordered-rows sibling recovery path. Defined here because recoverOpenColumns/
// recoverOneSide are the primary/eponymous owner of this constant group.
const (
	overhangTol = 2 * rectRowSnapTol
	minOpenRows = 2
	minOpenColW = 6.0
	openPad     = 2.0
)

// --- Stage 2 pass: rect-bordered / shaded-sibling / fill-banded / header-ruled row recovery
// (tables_recover_rows.go) ---

// Rect-bordered tables (e.g. the Economic Report of the President B-1/B-2) have full column
// verticals and an outer frame but ZERO interior horizontal rules in the data body, so
// latticeTables collapses every data column into one full-height cell and the data rows are
// lost. The constants below recover them by inferring row bands from the data-body word
// Y-centers.
const (
	// rectRowSnapTol matches a cell bottom to the table bottom and bounds the interior-rule
	// exclusion window around the data-body extremes.
	// calibrated: rect-bordered regression corpus — verified BYTE-IDENTICAL on NICS, HHS ASPE,
	// IRS P17, EPA, and IRS-SOI (34 interior data-body rules, 0 collapsed cells).
	rectRowSnapTol = 3.0
	// rectMinRowClusters is the minimum word-Y-rows a full-height cell must hold to count as a
	// collapsed multi-row data column. It doubles as the multi-row guard: a one- or two-line
	// framed box (a callout) never reaches it.
	// calibrated: rect-bordered regression corpus (ERP B-1/B-2, NICS, HHS ASPE, IRS P17, EPA,
	// IRS-SOI).
	rectMinRowClusters = 3
	// rectMinRowSplit is the minimum number of numeric cross-column record bands a BEA-branch cell
	// must contain before splitTallBandCells (PR-2) splits it into rows. It is deliberately HIGHER
	// than rectMinRowClusters: a corpus sweep of the shipped mechanism showed the exactly-3-band
	// zone is false-positive-dense (blank-row insertion at group separators, displaced multi-line
	// headers), while every genuine collapsed data table (FT-900 months, German census waves, MHLW
	// wage rows) carries far more (10–46) record bands. It is a HEURISTIC boundary, NOT a complete
	// false-positive guard: a count threshold cannot by itself separate a 4-row numeric HEADER /
	// annotation block (each row carrying year-like tokens in ≥2 columns) from a 4-row collapsed
	// data block — that residual, and prose/cover/TOC blocks that reach the BEA branch at all, are
	// documented known limitations (a structural body-vs-header discriminator is a follow-on).
	// calibrated: FT-900/German-census/MHLW-wage corpus sweep (splitTallBandCells, PR-2).
	rectMinRowSplit = 4
	// rectRowGapTol clusters word Y-centers into row bands. Kept equal to reconstructGrid's row
	// clustering tol (tables_grid.go's cluster1D(tops, rectRowGapTol) call in reconstructGrid,
	// tables_recover_rows.go's headerColumnDefiningRow, and tables_recover_comb.go's
	// combBodyBodyWords all reference this SAME constant, compiler-enforced) so the synthesized
	// tops re-cluster consistently downstream.
	rectRowGapTol = 4.0
	// rectMinBodyFrac is the minimum fraction of the table's vertical extent that the full-height
	// candidate cells must span. It separates a genuinely collapsed data body (which dominates
	// the table — ERP B-1 spans ~85%) from a multi-line BOTTOM ROW of an otherwise fully-ruled
	// table (whose cells also reach the table bottom and can hold >=3 wrapped lines, but span only
	// a small fraction). Without it, a multi-row ruled table whose last row wraps in >=2 aligned
	// columns would be wrongly row-split. It does NOT catch a ruled table that is geometrically
	// rect-bordered (a short header over one tall wrapped row dominating the body) — that input is
	// indistinguishable from the target; see the rect-bordered decision record.
	// calibrated: ERP B-1 (~85% span observed for a genuinely collapsed body).
	rectMinBodyFrac = 0.6
	// headerRuledMinCols is the minimum cell count of the column-defining header row for the
	// header-ruled data-cell recovery (inferHeaderRuledDataCells) to engage. >=3 ruled columns
	// is a genuine multi-column header, not a 1-2 cell caption/frame band.
	// provenance: pre-split tables_lattice.go
	headerRuledMinCols = 3
	// headerRuledMinColBands is the minimum number of distinct data-row bands a header data-column
	// must receive dropped words in before that column is synthesized. >=2 demands column-level
	// cluster evidence so a lone in-envelope word (geometrically indistinguishable from a single
	// real value — e.g. a footnote inside the data rectangle) is never promoted to table data.
	// provenance: pre-split tables_lattice.go
	headerRuledMinColBands = 2
)

// The problem: alternating-row shading applied to only SOME conceptual columns of a table
// causes those columns' fill-rects to become real per-row lattice cells, while sibling columns
// with no shading arrive as one tall cell spanning the whole shaded span. The constants below
// gate the pass that borrows an already row-closed sibling column's row Y-boundaries onto such
// a same-table, same-y-span tall cell.
const (
	// shadedSiblingColTol clusters cell left edges (x0) into column groups when locating a
	// candidate donor stack. Tighter than rectRowSnapTol because two genuinely distinct
	// columns can sit within a few points of each other in a dense grid.
	// provenance: pre-split tables_lattice.go
	shadedSiblingColTol = 2.0
	// shadedSiblingRectSpanTol bounds how closely a candidate rect-derived edge's OWN x-span
	// must match the donor cell's x-span for the boundary to count as shading-derived. An
	// overlap-only test (any rect edge whose x-range intersects the cell's) is NOT enough: a
	// real-world page's table-wide background/frame fill (e.g. x=[43,784] spanning the whole
	// table) trivially overlaps every narrow column's x-range at the table's own top/bottom Y,
	// so an overlap test cannot tell "this fill rect IS this cell's own per-row shading" apart
	// from "some unrelated, much wider fill rect happens to pass through this Y".
	// calibrated: irs-p1040 shaded columns (genuine per-row shading edge x-span matches the
	// cell's own x-span to within 0.01-0.08pt) vs the destatis regression's donor (wide-frame
	// false match has an x-span mismatch of hundreds of points) — tol=3.0 (matching
	// rectRowSnapTol's scale) cleanly separates the two without being a corpus-frequency bet.
	shadedSiblingRectSpanTol = 3.0
)

// --- Stage 2 pass: comb-body row recovery (tables_recover_comb.go) ---

// Comb-body tables have a fully-closed header band over a data body that carries
// only vertical rules (column cuts, like comb teeth) and no horizontal row rules.
const (
	// combBodyMinCols is the minimum number of interior crossing V-edges required
	// for the comb-body data-row recovery (inferCombBodyRows) to engage. >=2 interior
	// crossing verticals represent at least two column divisions (a genuine multi-column
	// comb body), not a single split or a page-margin border that slipped through.
	// provenance: pre-split tables_lattice.go
	combBodyMinCols = 2
	// combBodyStraddleTol is the touch-margin (points) for the anti-straddle gate: a
	// column cut falls "strictly inside" a word when it lies within
	// [w.X+combBodyStraddleTol, w.X+w.W-combBodyStraddleTol]. The 0.5 pt margin
	// absorbs sub-pixel PDF coordinate rounding without masking a genuine misalignment —
	// a word that truly straddles a rule will lie several points inside the word's
	// x-span, not 0.5 pt.
	// provenance: pre-split tables_lattice.go
	combBodyStraddleTol = 0.5
	// combBodyMinDataRows is the minimum number of row-bands that must each fill >=2
	// NON-LABEL columns (a genuine multi-column data row) for the body to qualify as a
	// real comb-body data grid. A per-COLUMN occupancy test is too weak: scattered
	// infographic/map labels can occupy two columns in DISJOINT bands with no actual
	// multi-column row. Requiring >=2 co-occupied non-label columns IN THE SAME band
	// rejects scattered map labels, table-of-contents lists, formula/prose, and charts.
	// provenance: pre-split tables_lattice.go
	combBodyMinDataRows = 2
	// combBodyForeignXPad is the x-axis padding (points) combBodyForeignInBody applies when
	// testing whether a foreign (other-table) closed cell's center falls inside this table's
	// body x-span [tableLeft-combBodyForeignXPad, tableRight+combBodyForeignXPad]. Absorbs
	// sub-point rounding at the table's own left/right bounds without widening the foreign-
	// occupancy test enough to catch a genuinely separate neighboring table.
	// provenance: pre-split tables_lattice.go
	combBodyForeignXPad = 2.0
)

// --- Stage 3: grid assembly (tables_grid.go) ---

// wordJoinGapTol (points) bounds the inter-word horizontal gap below which two same-row words
// are treated as one fragmented token. The true discriminator is gap <= 0 (the runs touch or
// overlap — the fragmented NASS values overlap at gap -0.03); the small positive margin only
// absorbs float noise. A genuine word space advances the pen well past this, and distinct column
// values are a whole column's padding apart, so the re-join is a no-op on correct cells: two
// words can only reach this gap when they are fragments of one token (or a degenerate zero-padding
// layout that renders as a single visual run anyway).
// calibrated: NASS value-fragment case ("2,1"/"20" pair, observed gap -0.03pt).
const wordJoinGapTol = 0.25

// ruleSpanFrac is the minimum fraction of a table's own vertical extent that a page vertical
// rule must overlap before reconstructGrid treats it as a genuine column-boundary rule (a
// "governing" rule) rather than a decorative header-only box line. Real column dividers in
// single-axis-ruled tables (full-height verticals, no horizontal rules between data rows)
// overlap most of the table's height even when they stop short of a merged multi-tier header;
// decorative per-cell header box lines cover only the header band. See
// plans/decisions/SINGLE-AXIS-RULED-TRIAGE-2026-07-01.md and the Item 1 spike verdict.
// calibrated: single-axis-ruled spike verdict (plans/decisions/SINGLE-AXIS-RULED-TRIAGE-2026-07-01.md).
const ruleSpanFrac = 0.5

// ruleSnapTol is how close (pt) a cell's own x0 must sit to a table's governing rule x-position
// before reconstructGrid snaps that cell onto the rule for column-identity purposes. Wider than
// colClusterTol (4.0pt) because a header label's own content x0 can sit several points inside
// its true rule-bounded column.
// calibrated: single-axis-ruled fixtures (observed offsets ranged 5.3-10.3pt across publishers).
const ruleSnapTol = 12.0

// governingLeftSlack is the tolerance (pt) by which a candidate rule may sit to the RIGHT of a
// cell's own x0 and still be considered "at" that x0 rather than strictly to its right. It
// exists only to absorb sub-point rounding jitter (a cell's own x0 landing a hair left of its
// true governing rule due to float quantization) — it must stay much smaller than ruleSnapTol
// so it never lets a genuinely different (neighboring) rule qualify as this cell's own left
// boundary. See snapToGoverningRule's directional-boundary rationale (Item 1 fix, defect (2)).
// calibrated: Item 1 fix, defect (2) (single-axis-ruled spike verdict).
const governingLeftSlack = 0.75

// sparseRowCoverageFrac is the row-coverage fraction (of the table's total distinct rows) below
// which a raw (unsnapped) column cluster is treated as a sparse, possibly-anomalous artifact
// (e.g. a header/label row whose own cell x0 differs from the data body's established column)
// and is therefore eligible for governing-rule reconciliation. At or above this fraction, a
// cluster is treated as an independently-established real column and is NEVER snapped onto a
// nearby rule, however close. This closes the hkcsd-monthly-digest-2024-01.pdf p169 defect: two
// genuinely distinct columns, EACH with 21/21 (100%) row coverage, sat 9.12pt apart — well
// within ruleSnapTol(12) of the same governing rule — and a naive per-cell/per-cluster snap
// (with no coverage awareness) collapsed one into the other, gluing a footnote marker onto the
// wrong neighboring column's value.
// calibrated: jo-dos-health-2023.pdf p6/p7 (Jordan 14.7-shaped header-artifact clusters had 1-2
// rows out of 12-17, 8-17%; the dense columns they reconcile onto had 11-17 rows, 92-100%). 0.5
// sits with a wide margin on both sides of that real-data gap.
const sparseRowCoverageFrac = 0.5

// colClusterTol is the single-linkage tolerance used to cluster cell x0 positions into column
// representatives (reconstructGrid: cluster1D(x0s, colClusterTol)). It doubles as the
// span-interior margin in spanContainsRealColumn. Because cluster1D is single-linkage gap-based,
// a legitimately edge-sharing column's cluster-mean colRep can drift INWARD from the shared rule
// by up to ~one tolerance (members chain across gaps ≤ tol). Requiring a non-empty rep to lie
// MORE than colClusterTol inside the empty column's span therefore distinguishes a genuinely
// distinct interior sub-column (the mis-split spanning-cell signature) from a mere edge-sharing
// neighbour whose mean jittered inward. Tying the margin to the column cluster tolerance keeps
// the drop predicate self-consistent with how the columns were clustered in the first place.
// provenance: pre-split tables_lattice.go
const colClusterTol = 4.0

// leaderDotRunFloor is the minimum length of a consecutive '.' (U+002E) run that, when flanked by
// letters on BOTH sides (hasInterleavedLeaderRun), marks a table cell as carrying a dot-leader
// fused INTO a label rather than isolated decimal points or an abbreviation. Measured against the
// USDA NASS crop table, where a State label's trailing dot leader is rendered with its glyphs
// interleaved into the label and so fuses into the label token: "Iowa" arrives as "Iow...a..."
// (3-dot runs), "Alabama" as "Alaba...m...a...." (4-dot runs), while "U.S." carries only single-dot
// runs. A floor of 3 fires on every fused State label (lifting the held-out fixture 0/300 ->
// 294/300) yet never on "U.S." (run length 1); a floor of 4 misses the exact-3-run states
// (Iowa/Utah/Ohio), dropping the score to 276/300.
// calibrated: USDA NASS crop table fixture (0/300 -> 294/300 lift).
const leaderDotRunFloor = 3

// --- Stage 3 tail: grid cleanup (tables_grid_cleanup.go) ---

// A gutter column (a thin cell from a double-wall decorative border rect) is dropped only
// when it is all-empty AND below BOTH of these gates — a column failing either is preserved:
//
//   - gutterFraction (relative): widest cell < gutterFraction × the table's median
//     data-column width. Targets the actual gutter property — thin RELATIVE to siblings —
//     so it is scale-robust across document sizes.
//   - absoluteGutterCap (absolute ceiling): widest cell < absoluteGutterCap pt. A hard
//     bound so a real, merely-narrow data column is never dropped just because wide sibling
//     columns inflate the median (the relative-gate-only false-positive class).
//
// Measured on all committed fixtures (2026-06-18):
//   - True gutter widths (double-wall border rects): 4.99–13.02 pt (EPA p1)
//   - Narrowest real data columns: 24.65 pt (ERP tables), 28.44 pt (HHS-ASPE)
//   - Normal data columns: 34–647 pt range
//
// The cap (16 pt) sits between max-gutter (13.02) and narrowest-real-column (24.65) with
// margin on both sides; the relative gate (0.25) additionally guards uniformly-narrow tables.
// A column must fall under both to be a gutter — far outside any observed real layout.
// calibrated: committed-fixture sweep 2026-06-18 (EPA p1, ERP tables, HHS-ASPE).
const (
	gutterFraction    = 0.25
	absoluteGutterCap = 16.0
)

const (
	// nestedWallTol is the maximum distance (pt) between the x1 right-edges of two adjacent
	// grid-columns that still qualifies as a shared wall. Normal adjacent columns share a
	// boundary (x1[i] == x0[i+1]); nested sub-cell pairs share the SAME x1 — this tolerance
	// distinguishes them.
	// provenance: pre-split tables_lattice.go
	nestedWallTol = 3.0
	// phantomMaxSparseCells is the maximum number of non-empty cells (over ALL rows) that the
	// sparser member of a candidate merge-pair may hold. A phantom header column carries only
	// the header label — at most one line (or two for a two-line wrapped header), so ≤2 is
	// generous. A data-bearing column carries many more cells and is never merged away.
	// Documented reopen: a 3+-line wrapped-header phantom will NOT merge (sparse limit = 2);
	// this is loss-free (no worse than today) and the reopen trigger is a confirmed real-world
	// fixture with a triple-line header phantom. The x0/left-aligned shared-wall variant also
	// never fired on real data and is a documented reopen.
	// provenance: pre-split tables_lattice.go
	phantomMaxSparseCells = 2
	// phantomMinDataCells is the minimum non-empty cell count (over ALL rows) the DENSE partner of
	// a sparse phantom must hold for a merge to fire. Without it, a SMALL table whose columns are
	// trivially "sparse" (≤2 cells just because the table has ≤2 data rows) would be over-merged —
	// the EPA p1 false positive (a 2-row complementary table, both columns ≤2 cells, no real
	// phantom). Requiring the data partner to carry ≥3 cells confirms a genuine (header + data)
	// doubling. Loss-free reopen: a real phantom whose data column has <3 rows will not merge.
	// calibrated: EPA p1 false positive (2-row complementary table, both columns ≤2 cells).
	phantomMinDataCells = 3
)

// --- Confidence / warnings API (tables_confidence.go) ---

// legacyFontGarbleFrac / minLegacyGarbledWords: flag when BOTH (a) at least legacyFontGarbleFrac
// of a table's in-cell alphabetic words are legacy-Indic-font glyphs that decoded to non-Indic
// (Latin) gibberish, AND (b) there are at least minLegacyGarbledWords such words. The fraction
// gate alone gives no protection for a 1–2-label table (a single coincidental match reaches
// frac=1.0); the absolute floor requires a substantial body of garbled text, which a genuinely
// garbled table always has (its row-label column alone is dozens of words). The signal SEPARATES
// sharply — measured 0 on every clean corpus PDF (47/48) and fired on three legacy-font fixtures
// across three publishers and two embedding styles (in-ecosurvey Walkman-Chanakya/Vivek;
// Rajasthan Kruti-Dev/DevLys CID; Himachal Pradesh Kruti Dev TrueType).
// calibrated: legacy-font detector (in-ecosurvey Walkman-Chanakya/Vivek; Rajasthan
// Kruti-Dev/DevLys CID; Himachal Pradesh Kruti Dev TrueType; 0/47 FPs on clean corpus).
const (
	legacyFontGarbleFrac  = 0.3
	minLegacyGarbledWords = 3
)
