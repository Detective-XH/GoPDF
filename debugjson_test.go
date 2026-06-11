package pdf

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ---- minimal test structs (prefixed dbg* to avoid colliding with the unexported
// DTO names jsonPage/jsonBlock/jsonLine/jsonSpan/… declared in debugjson.go) ----

type dbgSpan struct {
	Size   float64    `json:"size"`
	Font   string     `json:"font"`
	Origin [2]float64 `json:"origin"`
	Bbox   [4]float64 `json:"bbox"`
	Text   string     `json:"text"`
}

type dbgLine struct {
	Bbox  [4]float64 `json:"bbox"`
	Spans []dbgSpan  `json:"spans"`
}

type dbgBlock struct {
	Type  int        `json:"type"`
	Bbox  [4]float64 `json:"bbox"`
	Lines []dbgLine  `json:"lines"`
}

type dbgWarning struct {
	Page    int    `json:"page"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

type dbgPage struct {
	Width       float64      `json:"width"`
	Height      float64      `json:"height"`
	CoordOrigin string       `json:"coord_origin"`
	Blocks      []dbgBlock   `json:"blocks"`
	Warnings    []dbgWarning `json:"warnings"`
}

type dbgFont struct {
	Name     string `json:"name"`
	Subtype  string `json:"subtype"`
	Embedded bool   `json:"embedded"`
	Pages    []int  `json:"pages"`
}

type dbgLink struct {
	FromPage int        `json:"from_page"`
	ToPage   int        `json:"to_page"`
	URI      string     `json:"uri"`
	Bbox     [4]float64 `json:"bbox"`
}

type dbgDoc struct {
	PageCount int          `json:"page_count"`
	Pages     []dbgPage    `json:"pages"`
	Fonts     []dbgFont    `json:"fonts"`
	Links     []dbgLink    `json:"links"`
	Warnings  []dbgWarning `json:"warnings"`
}

// mustOpenBytes opens a PDF from bytes and fails the test on error.
func mustOpenBytes(t *testing.T, data []byte) *Reader {
	t.Helper()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	return r
}

// mustPageDebugJSON marshals Page.DebugJSON and fails on error.
func mustPageDebugJSON(t *testing.T, p Page) dbgPage {
	t.Helper()
	raw, err := p.DebugJSON()
	if err != nil {
		t.Fatalf("Page.DebugJSON: %v", err)
	}
	var out dbgPage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal page: %v", err)
	}
	return out
}

// mustReaderDebugJSON marshals Reader.DebugJSON and fails on error.
func mustReaderDebugJSON(t *testing.T, r *Reader) dbgDoc {
	t.Helper()
	raw, err := r.DebugJSON()
	if err != nil {
		t.Fatalf("Reader.DebugJSON: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("Reader.DebugJSON invalid JSON: %s", raw)
	}
	var doc dbgDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal dbgDoc: %v", err)
	}
	return doc
}

// dbgHasFontByName reports whether fonts contains an entry with the given base name.
func dbgHasFontByName(fonts []dbgFont, name string) bool {
	for _, f := range fonts {
		if f.Name == name {
			return true
		}
	}
	return false
}

// dbgHasWarningCode reports whether ws contains an entry with the given code.
func dbgHasWarningCode(ws []dbgWarning, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}

// dbgHasReaderWarningCode reports whether ExtractionWarning slice contains a given code.
func dbgHasReaderWarningCode(ws []ExtractionWarning, code string) bool {
	for _, w := range ws {
		if string(w.Code) == code {
			return true
		}
	}
	return false
}

// ---- Test 1: shape, coordinate transform, word segmentation ----

// TestPageDebugJSONShape verifies the basic shape of Page.DebugJSON output:
// correct width/height, TOPLEFT coord_origin, one text block with two word-level
// spans ("Hello" and "World" separately), and exact origin coordinates.
func TestPageDebugJSONShape(t *testing.T) {
	// MediaBox [0 0 200 100]; text at baseline x=20, y=80, size 12.
	cs := "BT /F1 12 Tf 20 80 Td (Hello World) Tj ET"
	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		// 4: Content stream
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cs), cs),
		// 5: Font
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
	r := mustOpenBytes(t, data)
	pg := mustPageDebugJSON(t, r.Page(1))

	if pg.Width != 200 {
		t.Errorf("width: got %v, want 200", pg.Width)
	}
	if pg.Height != 100 {
		t.Errorf("height: got %v, want 100", pg.Height)
	}
	if pg.CoordOrigin != "TOPLEFT" {
		t.Errorf("coord_origin: got %q, want TOPLEFT", pg.CoordOrigin)
	}
	if len(pg.Blocks) != 1 {
		t.Fatalf("blocks: got %d, want 1", len(pg.Blocks))
	}
	blk := pg.Blocks[0]
	if blk.Type != 0 {
		t.Errorf("block type: got %d, want 0", blk.Type)
	}
	if len(blk.Lines) < 1 {
		t.Fatalf("lines: got %d, want >=1", len(blk.Lines))
	}
	// Count all spans across all lines.
	var spans []dbgSpan
	for _, ln := range blk.Lines {
		spans = append(spans, ln.Spans...)
	}
	// Must have at least two spans: "Hello" and "World" individually.
	if len(spans) < 2 {
		t.Fatalf("spans: got %d, want >=2 (Hello and World separately)", len(spans))
	}
	// Check the first span text is "Hello" (not "HelloWorld" or "Hello World").
	if spans[0].Text != "Hello" {
		t.Errorf("spans[0].text: got %q, want \"Hello\"", spans[0].Text)
	}
	if spans[1].Text != "World" {
		t.Errorf("spans[1].text: got %q, want \"World\"", spans[1].Text)
	}
	// Exact origin check: x = 20-0 = 20, y = 100-80 = 20.
	// These are integer-valued operations; no FP drift is expected.
	if spans[0].Origin[0] != 20 {
		t.Errorf("spans[0].origin[0]: got %v, want 20", spans[0].Origin[0])
	}
	if spans[0].Origin[1] != 20 {
		t.Errorf("spans[0].origin[1]: got %v, want 20", spans[0].Origin[1])
	}
	// bbox structural check: top < bottom (height-dependent, always true since H=FontSize>0).
	// Note: bbox[2] > bbox[0] is NOT asserted here — the minimal Helvetica stub in this
	// fixture lacks a /Widths array, so W=0 and left==right. This is correct behavior;
	// origin and height remain exact.
	b := spans[0].Bbox
	if b[1] >= b[3] {
		t.Errorf("spans[0].bbox: top(%v) >= bottom(%v); want top<bottom", b[1], b[3])
	}
}

// ---- Test 2: non-zero CropBox origin ----

// TestPageDebugJSONOriginOffset verifies that the coordinate transform is origin-aware:
// x0 = X - llx, y0 = ury - Y (not naive height-y).
func TestPageDebugJSONOriginOffset(t *testing.T) {
	// MediaBox [10 20 210 120] → llx=10, lly=20, urx=210, ury=120
	// width=200, height=100
	// Text at x=30, y=80 → expected origin: [30-10, 120-80] = [20, 40]
	cs := "BT /F1 12 Tf 30 80 Td (Hi) Tj ET"
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [10 20 210 120] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cs), cs),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
	r := mustOpenBytes(t, data)
	pg := mustPageDebugJSON(t, r.Page(1))

	if pg.Width != 200 {
		t.Errorf("width: got %v, want 200", pg.Width)
	}
	if pg.Height != 100 {
		t.Errorf("height: got %v, want 100", pg.Height)
	}
	if pg.CoordOrigin != "TOPLEFT" {
		t.Errorf("coord_origin: got %q, want TOPLEFT", pg.CoordOrigin)
	}
	if len(pg.Blocks) < 1 {
		t.Fatalf("blocks: got %d, want >=1", len(pg.Blocks))
	}
	var spans []dbgSpan
	for _, ln := range pg.Blocks[0].Lines {
		spans = append(spans, ln.Spans...)
	}
	if len(spans) < 1 {
		t.Fatalf("spans: got 0, want >=1")
	}
	// x = 30 - 10 = 20
	if spans[0].Origin[0] != 20 {
		t.Errorf("origin[0]: got %v, want 20", spans[0].Origin[0])
	}
	// y = 120 - 80 = 40
	if spans[0].Origin[1] != 40 {
		t.Errorf("origin[1]: got %v, want 40", spans[0].Origin[1])
	}
}

// ---- Test 3: null page ----

// TestPageDebugJSONNullPage verifies that a null/missing page returns a valid minimal
// JSON object with empty blocks, BOTTOMLEFT coord_origin, and zero width/height.
func TestPageDebugJSONNullPage(t *testing.T) {
	data := buildTextPDF("BT /F1 12 Tf (Hello) Tj ET")
	r := mustOpenBytes(t, data)
	// r.Page(9999) returns a null page (p.V.IsNull() == true).
	nullPage := r.Page(9999)
	raw, err := nullPage.DebugJSON()
	if err != nil {
		t.Fatalf("null page DebugJSON returned error: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("null page DebugJSON returned invalid JSON: %s", raw)
	}
	var pg dbgPage
	if err := json.Unmarshal(raw, &pg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Null page CropBox → [0,0,0,0] → degenerate → BOTTOMLEFT.
	if pg.CoordOrigin != "BOTTOMLEFT" {
		t.Errorf("coord_origin: got %q, want BOTTOMLEFT", pg.CoordOrigin)
	}
	if pg.Width != 0 {
		t.Errorf("width: got %v, want 0", pg.Width)
	}
	if pg.Height != 0 {
		t.Errorf("height: got %v, want 0", pg.Height)
	}
	// blocks must be an empty JSON array [], not null.
	// We check the raw JSON contains "blocks":[] rather than "blocks":null.
	rawStr := string(raw)
	if len(pg.Blocks) != 0 {
		t.Errorf("blocks: got %d elements, want 0", len(pg.Blocks))
	}
	// Verify it round-trips as an array (not null) by checking the raw bytes.
	// json.Unmarshal into map to inspect the blocks field type.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("map unmarshal: %v", err)
	}
	blocksRaw, ok := m["blocks"]
	if !ok {
		t.Fatal("blocks key absent from null page JSON")
	}
	// Must be "[]", not "null".
	if string(blocksRaw) == "null" {
		t.Errorf("blocks is JSON null, want []; rawStr: %s", rawStr)
	}
}

// ---- Test 4: document envelope ----

// TestReaderDebugJSONEnvelope verifies the Reader.DebugJSON envelope contains
// correct page_count, fonts, links with transformed bbox, and round-trips cleanly.
func TestReaderDebugJSONEnvelope(t *testing.T) {
	doc := mustReaderDebugJSON(t, mustOpenBytes(t, buildEnvelopeFixture()))
	assertEnvelope(t, doc)
}

// buildEnvelopeFixture constructs a 1-page PDF with a font and a URI link annotation.
func buildEnvelopeFixture() []byte {
	annotStr := "<< /Type /Annot /Subtype /Link /Rect [10 10 90 30] /A << /S /URI /URI (https://example.com) >> >>"
	pageContent := "BT /F1 12 Tf 50 400 Td (Test) Tj ET"
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> /Annots [%s] >>", annotStr),
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

// assertEnvelope checks the structural invariants of a dbgDoc from the envelope fixture.
func assertEnvelope(t *testing.T, doc dbgDoc) {
	t.Helper()
	if doc.PageCount != 1 {
		t.Errorf("page_count: got %d, want 1", doc.PageCount)
	}
	if len(doc.Pages) != doc.PageCount {
		t.Errorf("len(pages): got %d, want %d", len(doc.Pages), doc.PageCount)
	}
	if !dbgHasFontByName(doc.Fonts, "Helvetica") {
		t.Errorf("fonts: no entry with name=Helvetica; got %+v", doc.Fonts)
	}
	assertLinkEnvelope(t, doc.Links)
}

// assertLinkEnvelope checks the links slice from the envelope fixture.
func assertLinkEnvelope(t *testing.T, links []dbgLink) {
	t.Helper()
	if len(links) != 1 {
		t.Fatalf("links: got %d, want 1", len(links))
	}
	lnk := links[0]
	if lnk.URI != "https://example.com" {
		t.Errorf("link URI: got %q, want https://example.com", lnk.URI)
	}
	bbox := lnk.Bbox
	if bbox[0] == 0 && bbox[1] == 0 && bbox[2] == 0 && bbox[3] == 0 {
		t.Errorf("link bbox is all zeros: %v", bbox)
	}
}

// ---- Test 5: routing warning ----

// TestDebugJSONRoutingWarning verifies that image_only_page appears in the page-scoped
// warnings when the page is image-only. It gates that pageModel actually calls
// ExtractionSummary (the sole emitter of the routing warning).
func TestDebugJSONRoutingWarning(t *testing.T) {
	data := buildImageFullBleedPDF()

	// Page.DebugJSON: image_only_page must appear in the page dict's warnings.
	pg := mustPageDebugJSON(t, mustOpenBytes(t, data).Page(1))
	if !dbgHasWarningCode(pg.Warnings, "image_only_page") {
		t.Errorf("Page(1).DebugJSON: image_only_page not found in page warnings; got %+v", pg.Warnings)
	}

	// Reader.DebugJSON: image_only_page must appear in pages[0].warnings.
	doc := mustReaderDebugJSON(t, mustOpenBytes(t, data))
	assertRoutingWarnInDoc(t, doc)

	// r.Warnings() side-effect: confirm the warning is emitted on the Reader after DebugJSON.
	r3 := mustOpenBytes(t, data)
	if _, err := r3.Page(1).DebugJSON(); err != nil {
		t.Fatalf("Page(1).DebugJSON (r3): %v", err)
	}
	if !dbgHasReaderWarningCode(r3.Warnings(), "image_only_page") {
		t.Errorf("r.Warnings(): image_only_page not found; got %+v", r3.Warnings())
	}
}

// assertRoutingWarnInDoc checks that pages[0].warnings contains image_only_page.
func assertRoutingWarnInDoc(t *testing.T, doc dbgDoc) {
	t.Helper()
	if len(doc.Pages) < 1 {
		t.Fatal("doc.Pages is empty")
	}
	if !dbgHasWarningCode(doc.Pages[0].Warnings, "image_only_page") {
		t.Errorf("Reader.DebugJSON: image_only_page not in pages[0].warnings; got %+v", doc.Pages[0].Warnings)
	}
}

// ---- Test 6: all outputs are valid JSON ----

// TestDebugJSONValidJSON verifies that every DebugJSON call produces well-formed JSON
// and round-trips cleanly into map[string]any.
func TestDebugJSONValidJSON(t *testing.T) {
	fixtures := []struct {
		name string
		data []byte
	}{
		{"text", buildTextPDF("BT /F1 12 Tf 72 720 Td (Valid JSON test) Tj ET")},
		{"image", buildImageFullBleedPDF()},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			r := mustOpenBytes(t, fx.data)
			// Page-level.
			pageRaw, err := r.Page(1).DebugJSON()
			if err != nil {
				t.Fatalf("Page.DebugJSON: %v", err)
			}
			if !json.Valid(pageRaw) {
				t.Fatalf("Page.DebugJSON returned invalid JSON")
			}
			var pm map[string]any
			if err := json.Unmarshal(pageRaw, &pm); err != nil {
				t.Fatalf("Page JSON unmarshal: %v", err)
			}
			// Document-level.
			docRaw, err := r.DebugJSON()
			if err != nil {
				t.Fatalf("Reader.DebugJSON: %v", err)
			}
			if !json.Valid(docRaw) {
				t.Fatalf("Reader.DebugJSON returned invalid JSON")
			}
			var dm map[string]any
			if err := json.Unmarshal(docRaw, &dm); err != nil {
				t.Fatalf("Doc JSON unmarshal: %v", err)
			}
		})
	}

	// Null page.
	t.Run("null_page", func(t *testing.T) {
		data := buildTextPDF("BT /F1 12 Tf (x) Tj ET")
		r := mustOpenBytes(t, data)
		raw, err := r.Page(9999).DebugJSON()
		if err != nil {
			t.Fatalf("null page DebugJSON: %v", err)
		}
		if !json.Valid(raw) {
			t.Fatalf("null page DebugJSON returned invalid JSON")
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("null page JSON unmarshal: %v", err)
		}
	})
}

// ---- Test 7: null page slot warnings are not dropped (partition regression) ----

// TestReaderDebugJSONNullSlotWarning is a regression guard for the warning-partition
// hole: a null_page_slot warning has Page>0 but its slot is SKIPPED by Pages(), so it
// has no page dict. Earlier the envelope kept only Page==0 warnings, silently dropping
// it. Reader.DebugJSON must surface every Reader warning exactly once — page-scoped in a
// page dict, everything else (doc-scoped + skipped-slot page-scoped) in the envelope.
func TestReaderDebugJSONNullSlotWarning(t *testing.T) {
	run := "BT /F1 12 Tf 72 700 Td (page four) Tj ET"
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 4 >>",    // declares 4 pages
		"<< /Type /Pages /Parent 2 0 R /Kids [] /Count 3 >>", // slots 1-3 are null
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 5 0 R /Resources << /Font << /F1 6 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(run), run),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
	r := mustOpenBytes(t, data)
	doc := mustReaderDebugJSON(t, r)

	// The dropped-warning defect: null_page_slot (Page>0, no page dict) must appear in
	// the envelope, not vanish.
	if !dbgHasWarningCode(doc.Warnings, "null_page_slot") {
		t.Errorf("envelope warnings missing null_page_slot; got %+v", doc.Warnings)
	}

	// Partition invariant: every Reader warning appears exactly once across page dicts
	// and the envelope — no drop, no duplication. Key on (page, code, detail) to match
	// the warningStore dedup key.
	key := func(page int, code, detail string) string {
		return fmt.Sprintf("%d\x00%s\x00%s", page, code, detail)
	}
	count := map[string]int{}
	for _, w := range doc.Warnings {
		count[key(w.Page, w.Code, w.Detail)]++
	}
	for _, p := range doc.Pages {
		for _, w := range p.Warnings {
			count[key(w.Page, w.Code, w.Detail)]++
		}
	}
	for _, w := range r.Warnings() {
		k := key(w.Page, string(w.Code), w.Detail)
		switch count[k] {
		case 0:
			t.Errorf("Reader warning {Page:%d Code:%s} dropped from DebugJSON output", w.Page, w.Code)
		case 1:
			// exactly once — correct
		default:
			t.Errorf("Reader warning {Page:%d Code:%s} duplicated %d× in DebugJSON output", w.Page, w.Code, count[k])
		}
	}
}

// TestDebugJSONMalformedNumericsSurvive locks the contract that DebugJSON always
// emits valid JSON, even when adversarial content-stream geometry overflows
// float64 to a non-finite coordinate. PDF reals have no exponent syntax, so a
// large magnitude is a long decimal real ("1" + N zeros + ".0"). The overflow
// magnitude differs by vector, so each literal is sized to actually overflow:
//   - coord (cm-scale × Td-translate) and fontsize (cm-scale × Tf) are
//     MULTIPLICATION: 1e200 × 1e200 = +Inf (overflows above ~1.34e154 per factor).
//   - MediaBox width (box[2] - box[0]) is SUBTRACTION: it needs ~1e308 per end
//     (1e308 - (-1e308) = 2e308 = +Inf); 1e200 there would stay finite and be a
//     silent paper gate.
//   - transform-internal: finite operands whose sum overflows INSIDE bbox — a
//     hugely-negative page-box llx (-1e308) with a word at X~1e308 makes x-llx =
//     2e308 = +Inf while every raw word/line field stays finite. It is caught only
//     because every emitted coordinate flows through the recording sanitizer;
//     detecting on the raw inputs would miss it.
//
// Every literal stays < max float64 (~1.797e308) so the lexer's ParseFloat yields a
// finite value rather than hitting ErrRange.
//
// Each vector was confirmed by probe to produce a sanitized non-finite coordinate, so
// this guards the fix rather than a no-op. Each case asserts both survival (valid JSON)
// AND the non_finite_geometry signal, so "valid JSON" cannot regress into "valid JSON,
// silently fabricated origin coordinates".
func TestDebugJSONMalformedNumericsSurvive(t *testing.T) {
	e154 := "1" + strings.Repeat("0", 154) + ".0" // product 1e308, each factor finite
	e200 := "1" + strings.Repeat("0", 200) + ".0"
	e308 := "1" + strings.Repeat("0", 308) + ".0"
	negE308 := "-1" + strings.Repeat("0", 308) + ".0"

	cases := []struct {
		name     string
		mediaBox string
		content  string
	}{
		{"coord_overflow", "[0 0 200 100]",
			fmt.Sprintf("%s 0 0 %s 0 0 cm BT /F1 10 Tf %s %s Td (Hi) Tj ET", e200, e200, e200, e200)},
		{"fontsize_overflow", "[0 0 200 100]",
			fmt.Sprintf("%s 0 0 %s 0 0 cm BT /F1 %s Tf 20 80 Td (Hi) Tj ET", e200, e200, e200)},
		{"mediabox_width_overflow", fmt.Sprintf("[%s 0 %s 100]", negE308, e308),
			"BT /F1 12 Tf 20 80 Td (Hi) Tj ET"},
		// Arithmetic overflow INSIDE the transform: every raw word/line field stays
		// finite, but a hugely-negative llx makes x-llx = 1e308-(-1e308) = +Inf in bbox.
		// Caught only because every emitted coordinate flows through the recording
		// sanitizer; raw-input detection would miss it (regression guard for that shortcut).
		{"transform_arith_overflow", fmt.Sprintf("[%s 0 200 100]", negE308),
			fmt.Sprintf("%s 0 0 %s 0 0 cm BT /F1 10 Tf %s %s Td (H) Tj ET", e154, e154, e154, e154)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := buildPDFFromObjects([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /MediaBox " + tc.mediaBox +
					" /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
				fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(tc.content), tc.content),
				"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
			})
			r := mustOpenBytes(t, data)

			// (1) Survive: mustPageDebugJSON asserts nil err + valid JSON internally.
			// (2) Signal: a sanitized-to-0 coordinate must be flagged, not silent.
			page := mustPageDebugJSON(t, r.Page(1))
			if !dbgHasWarningCode(page.Warnings, "non_finite_geometry") {
				t.Errorf("Page.DebugJSON missing non_finite_geometry warning; got %+v", page.Warnings)
			}

			// Reader.DebugJSON also survives and carries the page-scoped warning in the
			// page dict (page-scoped warnings live in their page, not the envelope).
			doc := mustReaderDebugJSON(t, r)
			signalled := false
			for _, pg := range doc.Pages {
				if dbgHasWarningCode(pg.Warnings, "non_finite_geometry") {
					signalled = true
				}
			}
			if !signalled {
				t.Errorf("Reader.DebugJSON page dicts missing non_finite_geometry warning; got %+v", doc.Pages)
			}

			// And it is observable on the Reader itself — the store the JSON derives from.
			if !dbgHasReaderWarningCode(r.Warnings(), "non_finite_geometry") {
				t.Errorf("Reader.Warnings() missing non_finite_geometry")
			}

			// Partition: a page-scoped warning lives in its page dict, NOT the document
			// envelope — it must not appear in both (the invariant the null_page_slot
			// fix established, exercised here for a page that DOES get a dict).
			if dbgHasWarningCode(doc.Warnings, "non_finite_geometry") {
				t.Errorf("non_finite_geometry leaked into the envelope; page-scoped warnings belong only in their page dict: %+v", doc.Warnings)
			}
		})
	}
}
