package pdf

import (
	"fmt"
	"unicode"
)

// TableConfidence is a coarse, DETECTION-RELATIVE quality level for one reconstructed table.
// It is NOT a calibrated probability and NOT a correctness guarantee.
//
// Confidence is defined as the roll-up of detected Warnings: a table carries Low if it
// has at least one Warning, High if it has none. This is the "nothing flagged it" signal —
// not an affirmative quality certification. Callers MUST NOT treat High as "verified correct."
//
// Warnings are a NEGATIVE claim ("I detected problem X") — honest even at low recall: if the
// current detector set misses a defect the table gets High, which is truthful (no detector
// fired). As detectors are added across minor versions (PR2: reversed-text; PR3:
// watermark/glyph-overlap), tables previously High may drop to Low. That is the feature
// working correctly, not a breaking change.
//
// Callers MUST tolerate unknown values: treat any unrecognized TableConfidence as
// "needs review".
type TableConfidence string

const (
	// TableConfidenceHigh means no quality problem was DETECTED by the current detector set.
	// This is DETECTION-RELATIVE — "nothing flagged it", NOT "verified correct".
	// As new detectors are added (PR2 reversal, PR3 watermark, ...) tables currently High
	// may move to Low; that is the feature working, not a regression or a breaking API change.
	TableConfidenceHigh TableConfidence = "high"

	// TableConfidenceLow means at least one quality Warning fired for this table.
	// The specific problem is described in Warnings[*].
	TableConfidenceLow TableConfidence = "low"
)

// TableWarningCode classifies one per-table quality flag. Codes are additive across minor
// versions; callers must tolerate values they do not recognize — treat as "needs review",
// never panic or hard-code a closed switch. This mirrors the ExtractionWarningCode precedent
// in diagnostics.go.
type TableWarningCode string

const (
	// TableWarningPhantom signals that the table grid is likely a phantom — a bar chart or
	// infographic misread as a ruled grid. Detection: a high fraction of entirely-blank
	// columns across all rows signals a structurally empty grid. Threshold: blankCol >= 0.6.
	// (D1, PR1)
	TableWarningPhantom TableWarningCode = "phantom_table"

	// TableWarningLegacyFont signals that the table's text was rendered through a known
	// legacy NON-Unicode Indic font (e.g. Kruti Dev, DevLys, Walkman-Chanakya) whose glyphs
	// decode to Latin gibberish ("rkfydk" for तालिका) instead of the intended Devanagari
	// script. The NUMERIC data is usually intact, but the text labels are unreliable. The
	// PDF has no ToUnicode that would let any pure-text extractor recover the real characters
	// (only OCR or a font-specific remap could), so this is an honest "labels are garbled"
	// flag, not a recoverable defect. Detection: a table whose in-cell alphabetic text is
	// predominantly from a legacy-Indic font AND carries no Devanagari codepoints. (D2, PR2)
	TableWarningLegacyFont TableWarningCode = "legacy_font_text"
)

// TableWarning is one per-table quality flag. The field set is frozen for the v0.x line;
// new diagnostics arrive as new Codes, not new fields (mirrors ExtractionWarning in
// diagnostics.go). Callers may compare TableWarning values for equality — the struct is
// comparable.
type TableWarning struct {
	// Code classifies the quality issue. Treat unknown Codes as "needs review".
	Code TableWarningCode
	// Message is fixed, human-readable text that is constant per Code.
	Message string
	// Detail discriminates occurrences within the same Code, for example
	// "blank_col_fraction=0.71".
	Detail string
}

// TableRegion is the page display-space bounding box of one detected table. It is 1:1
// with Tables() by index: TableRegions()[i] is the spatial extent of Tables()[i].
//
// Quality information (Confidence, Warnings) lives on the Table, not here. Correlate
// by index: call both Tables() and TableRegions() on the same page, then zip by index.
//
// The Rect is in page display space — the same coordinate frame as Word, Stroke, and Text
// from Page.Words() and Page.Content() (Y-up, same as the rest of the PDF coordinate system).
// TableRegion is a 1-field struct (not a bare Rect) so it can grow additively in future
// releases (for example, render-fidelity flags for the image-fallback feature) without
// breaking callers that embed or compare it.
type TableRegion struct {
	// Rect is the page display-space bounding box of the table (same coordinate space as
	// Word/Stroke/Text from Page.Words()/Page.Content()).
	// Min is the bottom-left corner, Max is the top-right corner (Y-up, native PDF space).
	Rect Rect
}

// tableResult is the internal shared result of one reconstructed table. Both Tables() and
// TableRegions() project from a []tableResult returned by reconstructTablesFromContent — the
// single shared reconstruction path. This is the binding architectural constraint that
// guarantees both public methods skip the same empty-grid tables, keeping the 1:1
// index correspondence intact by construction.
type tableResult struct {
	grid     [][]string
	region   Rect
	warnings []TableWarning
}

// TableRegions returns the page display-space bounding boxes of the detected tables, in
// the same order and 1:1 with Tables(). The Rect of TableRegions()[i] is the spatial
// extent of Tables()[i] on the page.
//
// Confidence and Warnings are NOT duplicated on TableRegion — they live on the
// corresponding Table. Correlate by index.
//
// Coordinate space: the same Y-up display frame as Word, Stroke, and Text from
// Page.Words() and Page.Content() — Min is the bottom-left corner (smaller X, smaller Y),
// Max is the top-right corner.
//
// The 1:1 length correspondence with Tables() holds by construction: both methods call
// reconstructTables() and project from its output. TableRegions returns the same error
// as Tables and Words.
func (p Page) TableRegions() ([]TableRegion, error) {
	results, err := p.reconstructTables()
	if err != nil {
		return nil, err
	}
	regions := make([]TableRegion, len(results))
	for i, r := range results {
		regions[i] = TableRegion{Rect: r.region}
	}
	return regions, nil
}

// reconstructTables delegates to reconstructTablesFromContent using this page's content
// and MediaBox. Both Tables() and TableRegions() call this so they share one reconstruction
// path by construction (the binding constraint in CONFIDENCE-WARNINGS-API.md).
func (p Page) reconstructTables() ([]tableResult, error) {
	return reconstructTablesFromContent(p.Content(), p.MediaBox())
}

// reconstructTablesFromContent is the single shared internal reconstruction path for
// both Tables() and TableRegions(). It takes raw page content and the PORTRAIT MediaBox.
// Skew-text filtering and 90°-CCW de-rotation are applied internally.
//
// Skew (diagonal) text is dropped UNCONDITIONALLY here — a grid cell never legitimately holds
// diagonal text, so a watermark or a rotated chart-axis label must never fuse into a cell value.
// This differs from the reading-order prose surfaces (Words/Lines/Blocks via layoutContent), which
// drop skew only for a detected cross-page watermark so a page-specific rotated label is preserved
// there; the table grid has the stricter requirement, so it always filters (this is also why a
// watermarked document's table grid was already clean before the reading-order watermark work).
//
// The empty-grid skip (len(grid)==0 → omit from results) is done here so that both
// public methods skip the same tables, keeping the 1:1 index correspondence intact
// by construction.
//
// When wasRotated, the region is converted from the de-rotated (landscape) cell frame
// back to the original portrait display space using the ORIGINAL media[1] (lly) and
// media[2] (urx) — NOT the de-rotated MediaBox. Using the de-rotated MediaBox here
// would return a wrong-frame region (the #1 coordinate bug risk); see
// cellsUnionRectRotated for the derivation.
func reconstructTablesFromContent(c Content, media [4]float64) ([]tableResult, error) {
	tableC := Content{Text: dropSkewRotatedText(c.Text), Rect: c.Rect, Stroke: c.Stroke}
	deRotC, deRotMedia, wasRotated := deRotateTableContent(tableC, media)
	tableWords, err := wordsFromContentRecovered(deRotC)
	if err != nil {
		return nil, err
	}
	var vRules []lEdge
	var lattices [][]lCell
	if wasRotated {
		vRules = verticalRules(deRotC)
		lattices = latticeTablesOpen(deRotC, tableWords, deRotMedia)
	} else {
		vRules = verticalRules(c)
		lattices = latticeTablesOpen(c, tableWords, media)
	}
	var results []tableResult
	for _, cells := range lattices {
		grid := reconstructGrid(cells, tableWords, vRules...)
		if len(grid) == 0 {
			continue // skip empty grids; both Tables() and TableRegions() project from this slice
		}
		var region Rect
		if wasRotated {
			lly, urx := media[1], media[2] // ORIGINAL portrait MediaBox — not deRotMedia
			region = cellsUnionRectRotated(cells, lly, urx)
		} else {
			region = cellsUnionRect(cells)
		}
		warnings := detectTableWarnings(grid)
		warnings = append(warnings, detectLegacyFontText(cells, tableWords)...)
		results = append(results, tableResult{
			grid:     grid,
			region:   region,
			warnings: warnings,
		})
	}
	return results, nil
}

// cellsUnionRect computes the union bounding box of a table's lCells in page display
// space (Y-up, same as Word/Stroke/Text). lCell uses top-origin coordinates where
// top < bottom (both values are negative for a standard page with lly=0):
//
//	display Y = −top_origin
//	cell.top (visual top, most negative) → Max.Y (largest display Y = visual top)
//	cell.bottom (visual bottom, least negative) → Min.Y (smallest display Y = visual bottom)
//
// Verified by TestDeRotateTableContentWordOrder and the fillbanded test fixtures:
// lCell{top:−30, bottom:−20} ↔ Rect{Min.Y:20, Max.Y:30}.
func cellsUnionRect(cells []lCell) Rect {
	if len(cells) == 0 {
		return Rect{}
	}
	minX := cells[0].x0
	maxX := cells[0].x1
	minTop := cells[0].top       // most negative = visual top → Max.Y after negation
	maxBottom := cells[0].bottom // least negative = visual bottom → Min.Y after negation
	for _, c := range cells[1:] {
		if c.x0 < minX {
			minX = c.x0
		}
		if c.x1 > maxX {
			maxX = c.x1
		}
		if c.top < minTop {
			minTop = c.top
		}
		if c.bottom > maxBottom {
			maxBottom = c.bottom
		}
	}
	return Rect{
		Min: Point{X: minX, Y: -maxBottom}, // visual bottom = lower display Y
		Max: Point{X: maxX, Y: -minTop},    // visual top = higher display Y
	}
}

// cellsUnionRectRotated converts a table's cell union bbox from the de-rotated
// (landscape top-origin) frame back to portrait display space (Y-up), which is
// the coordinate frame that Page.Words() uses.
//
// The de-rotation transform (rotPoint90CCW) maps portrait (x, y) to landscape:
//
//	newX = y − lly
//	newY = urx − x
//
// Inverting: landscape (lx, ly_display) → portrait (urx − ly_display, lx + lly).
// lCell fields are in landscape top-origin: top/bottom = −(landscape_Y_display).
// Substituting:
//
//	portrait_x = urx − (−top_origin) = urx + top_origin
//	portrait_y = landscape_x + lly
//
// lly and urx MUST come from the ORIGINAL portrait MediaBox (not deRotMedia).
// Using deRotMedia here is the #1 coordinate bug: deRotMedia is [0,0,pageH,pageW],
// whose urx is pageH (not the portrait page width), producing wrong portrait X values.
func cellsUnionRectRotated(cells []lCell, lly, urx float64) Rect {
	if len(cells) == 0 {
		return Rect{}
	}
	minTop := cells[0].top       // most negative → smallest portrait X = Min.X
	maxBottom := cells[0].bottom // least negative → largest portrait X = Max.X
	minX0 := cells[0].x0         // landscape X left → smallest portrait Y = Min.Y
	maxX1 := cells[0].x1         // landscape X right → largest portrait Y = Max.Y
	for _, c := range cells[1:] {
		if c.top < minTop {
			minTop = c.top
		}
		if c.bottom > maxBottom {
			maxBottom = c.bottom
		}
		if c.x0 < minX0 {
			minX0 = c.x0
		}
		if c.x1 > maxX1 {
			maxX1 = c.x1
		}
	}
	return Rect{
		Min: Point{X: urx + minTop, Y: minX0 + lly},
		Max: Point{X: urx + maxBottom, Y: maxX1 + lly},
	}
}

// detectTableWarnings checks a reconstructed grid for quality issues and returns
// any fired warnings. It is STRICTLY READ-ONLY: it never mutates grid or any cell string,
// never influences whether a table is appended to the output slice, and never changes
// whether Table.Cells is populated.
//
// D1: phantom detector. blankCol = (number of entirely-blank columns) / (total columns).
// A column is entirely blank when every row has an empty string in that column.
// Threshold blankCol >= 0.6: when more than 60 % of columns are blank the grid is likely
// a bar chart or infographic misread as a ruled table. The 0.6 threshold is FP-clean on the
// measured gov-statistical distribution (census §7: across 114 non-phantom tables the maximum
// blankCol was 0.5, so no real table reached the threshold; 9/9 fresh out-of-sample hits were
// true phantoms). Guard: 0 columns → no warning (avoids divide-by-zero).
//
// Known limitation (honest): this is a correlation flag, not a 0-FP classifier. A genuinely
// very sparse table — one with ≥60 % entirely-blank columns (e.g. many planned/future or
// trailing-empty columns) — outside the measured distribution will also be flagged. The
// warning is non-destructive (it only sets Confidence to Low, never alters Cells), and the
// Message is phrased to name the sparse-table possibility rather than asserting "chart".
func detectTableWarnings(grid [][]string) []TableWarning {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return nil
	}
	nCols := len(grid[0])
	nBlank := 0
	for col := range nCols {
		blank := true
		for _, row := range grid {
			if col < len(row) && row[col] != "" {
				blank = false
				break
			}
		}
		if blank {
			nBlank++
		}
	}
	blankFrac := float64(nBlank) / float64(nCols)
	if blankFrac >= 0.6 {
		return []TableWarning{{
			Code:    TableWarningPhantom,
			Message: "table grid is mostly blank columns — likely a chart or infographic misread as a table (a very sparse real table can also trip this); verify before relying on the grid",
			Detail:  fmt.Sprintf("blank_col_fraction=%.2f", blankFrac),
		}}
	}
	return nil
}

// legacyIndicFontFamilies lists known legacy NON-Unicode Devanagari (Hindi/Nepali/Marathi)
// font families whose glyphs decode to Latin gibberish instead of the intended script. Matched
// case-insensitively as a substring of Word.Font (the subset prefix like "ABCDEF+" is already
// stripped by the font layer, and both "Kruti Dev 010" and "KrutiDev010" hit the "kruti" entry).
// This is an EXTENSIBLE allowlist — like getEncoder's CMap switch and adobeCIDToUnicodeTable, new
// families are added as evidence arrives — so the detector is DETECTION-RELATIVE: an unrecognized
// legacy font is silently NOT flagged (honest — no false claim), never falsely flagged. The list is
// deliberately confined to validated and unambiguous legacy-font tokens (broad/ambiguous tokens
// like "shree"/"akshar" were dropped — a legitimate Latin font could coincidentally carry them).
// The script-mismatch corroboration (hasIndicScript) is the second, load-bearing gate regardless.
var legacyIndicFontFamilies = []string{
	"kruti", "devlys", "chanakya", "walkman", "preeti", "kantipur", "shusha", "vivek",
}

// strictLegacyIndicFontFamilies is the high-confidence subset used by the DOCUMENT-scoped
// warning (getEncoder), which fires on the font NAME alone — it runs before any text is
// decoded, so unlike the per-table detector it CANNOT corroborate with the decoded script. It
// therefore drops the family tokens that are also common given names / brands ("vivek",
// "preeti") to stay FP-safe without corroboration; the distinctive legacy-font names that
// remain are vanishingly unlikely to appear in a real Latin font. All three known fixtures
// still match (Kruti-Dev/DevLys via "kruti"/"devlys"; Walkman-Chanakya via "chanakya").
var strictLegacyIndicFontFamilies = []string{
	"kruti", "devlys", "chanakya", "walkman", "kantipur", "shusha",
}

// isLegacyIndicFont reports whether font names a known legacy non-Unicode Devanagari family.
// Used by the per-table detector, which corroborates each match with the no-Indic-script and
// count gates — so it can afford the broader list (incl. the common-word "vivek", validated on
// the in-ecosurvey fixture).
func isLegacyIndicFont(font string) bool {
	return matchesAnyFold(font, legacyIndicFontFamilies)
}

// isLegacyIndicFontStrict reports whether font names a HIGH-CONFIDENCE legacy family — used by
// the uncorroborated document-scoped warning (see strictLegacyIndicFontFamilies).
func isLegacyIndicFontStrict(font string) bool {
	return matchesAnyFold(font, strictLegacyIndicFontFamilies)
}

// matchesAnyFold reports whether font contains any of fams as a case-insensitive substring.
func matchesAnyFold(font string, fams []string) bool {
	for _, fam := range fams {
		if asciiContainsFold(font, fam) {
			return true
		}
	}
	return false
}

// asciiContainsFold reports whether needleLower occurs in haystack, comparing ASCII letters
// case-insensitively WITHOUT allocating (font names are ASCII; isLegacyIndicFont runs per
// in-cell word, so a strings.ToLower copy there showed up as +0.8% allocs/op). needleLower
// MUST already be lower-case (the legacyIndicFontFamilies entries are).
func asciiContainsFold(haystack, needleLower string) bool {
	n := len(needleLower)
	if n == 0 {
		return true
	}
	for i := 0; i+n <= len(haystack); i++ {
		match := true
		for j := range n {
			c := haystack[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needleLower[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// hasAnyLetter reports whether s contains at least one Unicode letter (so pure-numeric /
// punctuation cells are excluded from the legacy-font denominator).
func hasAnyLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// hasIndicScript reports whether s carries any codepoint in the contiguous Indic Unicode block
// (U+0900–U+0DFF: Devanagari, Bengali, Gurmukhi, Gujarati, Oriya, Tamil, Telugu, Kannada,
// Malayalam, Sinhala). A legacy Indic font that decoded CORRECTLY would emit characters in this
// range — for ANY Indic script, not only Devanagari; a broken legacy-font decode emits Latin /
// Latin-1 gibberish (e.g. "rkfydk", or "vfèkdkfj;ksa" where è = U+00E8 is Latin-1, NOT ASCII —
// which is why the test is "no Indic script", not "ASCII-only"). Its absence, combined with a
// legacy-Indic font, is the script-mismatch tell that SEPARATES garble from a correct decode —
// and checking the whole Indic block (not just Devanagari) avoids falsely flagging a correctly
// decoded non-Devanagari Indic font (e.g. a Gujarati "Shree" variant emitting real Gujarati).
func hasIndicScript(s string) bool {
	for _, r := range s {
		if r >= 0x0900 && r <= 0x0DFF {
			return true
		}
	}
	return false
}

// hasLatinLetters reports whether s contains any ASCII Latin letter. A legacy-Indic-font word that
// still carries Latin letters has NOT been cleanly recovered to Unicode — it is either un-remapped
// gibberish or a partial Devanagari+Latin remap; either way its labels are unreliable. (Distinct from
// the Latin-1 range hasIndicScript discusses: this is the positive "still has Latin" tell.)
func hasLatinLetters(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// hasInvalidHalantMatra reports whether s contains a Devanagari virama / halant (U+094D) immediately
// followed by a dependent vowel sign / matra (U+093E..U+094C). That sequence is orthographically
// impossible in valid Devanagari — a halant suppresses the inherent vowel, so no matra can attach to
// it — so it can only arise from a mis-decode. It is the signature of a precomposed-ligature glyph that
// a subsetted-font /Differences bridge could not resolve (e.g. फ्रेंच mis-decoded to प्रफ्ेंच, where फ्
// is followed by े), and also catches legacy-transducer residuals (e.g. क्ष्ोत्र for क्षेत्र). A single
// occurrence is a DEFINITE mis-decode (zero false positives on cleanly-recovered text).
func hasInvalidHalantMatra(s string) bool {
	prevHalant := false
	for _, r := range s {
		if prevHalant && r >= 0x093E && r <= 0x094C {
			return true
		}
		prevHalant = r == 0x094D
	}
	return false
}

// legacyWordFlags classifies a legacy-Indic-font word for detectLegacyFontText. garbled = the word is
// not cleanly recovered: either un-remapped (all-Latin gibberish, no Indic) or PARTIALLY remapped
// (Devanagari + leftover Latin "soup") — a fuzzy heuristic that needs a count/fraction gate to avoid
// false-positiving a legit Latin word in an Indic table. misdecoded = the word recovered to pure Indic
// but carries an orthographically-IMPOSSIBLE halant+matra cluster — the signature of a precomposed-
// ligature glyph a subsetted-font /Differences bridge could not resolve (e.g. फ्रेंच → प्रफ्ेंच), or a
// legacy-transducer residual (क्ष्ोत्र for क्षेत्र). A DEFINITE mis-decode (zero false positives), so a
// single one flags. A non-legacy font, or a cleanly-recovered word, is neither.
func legacyWordFlags(w Word) (garbled, misdecoded bool) {
	if !isLegacyIndicFont(w.Font) {
		return false, false
	}
	if !hasIndicScript(w.S) || hasLatinLetters(w.S) {
		return true, false
	}
	return false, hasInvalidHalantMatra(w.S)
}

// wordCenterInCells reports whether word w's center anchor falls inside any lattice cell, using
// the same top-origin containment test reconstructGrid uses to bucket words into cells
// (tables_lattice.go). Restricting the legacy-font scan to text that actually lands in the grid
// means a clean-grid table with a garbled caption *outside* its cells is not falsely flagged.
func wordCenterInCells(w Word, cells []lCell) bool {
	ax := w.X + w.W/2
	ay := -(w.Y + w.H/2) // top-origin anchor (mirrors reconstructGrid's word bucketing)
	for _, c := range cells {
		if ax >= c.x0 && ax <= c.x1 && ay >= c.top && ay <= c.bottom {
			return true
		}
	}
	return false
}

// legacyFontGarbleFrac / minLegacyGarbledWords: flag when BOTH (a) at least legacyFontGarbleFrac
// of a table's in-cell alphabetic words are legacy-Indic-font glyphs that decoded to non-Indic
// (Latin) gibberish, AND (b) there are at least minLegacyGarbledWords such words. The fraction
// gate alone gives no protection for a 1–2-label table (a single coincidental match reaches
// frac=1.0); the absolute floor requires a substantial body of garbled text, which a genuinely
// garbled table always has (its row-label column alone is dozens of words). The signal SEPARATES
// sharply — measured 0 on every clean corpus PDF (47/48) and fired on three legacy-font fixtures
// across three publishers and two embedding styles (in-ecosurvey Walkman-Chanakya/Vivek; Rajasthan
// Kruti-Dev/DevLys CID; Himachal Pradesh Kruti Dev TrueType).
const (
	legacyFontGarbleFrac  = 0.3
	minLegacyGarbledWords = 3
)

// detectLegacyFontText flags a table whose in-cell text was rendered through a legacy non-Unicode
// Indic font and decoded to Latin gibberish (see TableWarningLegacyFont). It is STRICTLY
// READ-ONLY: it reads cells/words and returns a warning; it never mutates the grid or influences
// which tables are emitted, so Tables().Cells stays byte-identical. words are the page words in
// the same coordinate frame as cells (de-rotated when the table was rotated) — the same pair
// reconstructGrid receives.
func detectLegacyFontText(cells []lCell, words []Word) []TableWarning {
	if len(cells) == 0 {
		return nil
	}
	alpha, garbled, misdecoded := 0, 0, 0
	famSeen := ""
	for _, w := range words {
		if !hasAnyLetter(w.S) || !wordCenterInCells(w, cells) {
			continue
		}
		alpha++
		g, m := legacyWordFlags(w)
		if g {
			garbled++
		} else if m {
			misdecoded++
		}
		if (g || m) && famSeen == "" {
			famSeen = w.Font
		}
	}
	if alpha == 0 {
		return nil
	}
	frac := float64(garbled) / float64(alpha)
	gibberish := garbled >= minLegacyGarbledWords && frac >= legacyFontGarbleFrac
	if !gibberish && misdecoded == 0 {
		return nil
	}
	return []TableWarning{{
		Code:    TableWarningLegacyFont,
		Message: "table text was rendered through a legacy non-Unicode Indic font (e.g. Kruti Dev / DevLys / Chanakya); some labels did not cleanly recover — either left as Latin gibberish, or mis-decoded from a precomposed-ligature glyph into an orthographically-invalid cluster — so numeric data may be intact but verify the affected text labels before relying on them",
		Detail:  fmt.Sprintf("legacy_font=%s; garble_fraction=%.2f; misdecoded_clusters=%d", famSeen, frac, misdecoded),
	}}
}
