// tables_rotated_text_test.go — unit tests for the skew-text drop filter and the
// "do it once" contract that applies it uniformly to every reading-order surface.
//
// dropSkewRotatedText removes diagonal/watermark glyphs while keeping all axis-aligned text
// (0°/90°/180°/270°). The table grid path applies it UNCONDITIONALLY (a cell never holds diagonal
// text). The reading-order prose surfaces (Words/Lines/Blocks) apply it via layoutFilter/
// layoutContent ONLY for a detected cross-page watermark, so a page-specific rotated label is
// preserved there. The raw glyphs (incl. skew) remain on Content()/Texts()/DebugJSON. The
// low-level wordsFromContent does NOT itself filter — it assembles whatever Content it is handed.
// These tests verify the filter's correctness, the watermark detector, and layoutFilter.
package pdf

import (
	"testing"
)

// TestDropSkewRotatedText verifies that dropSkewRotatedText keeps exactly the
// glyphs within skewAngleTolDeg of an axis (0°/90°/180°/270°) and drops the
// rest.
func TestDropSkewRotatedText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		rotation float64
		keep     bool
		label    string
	}{
		// ── Axis-aligned: must be kept ────────────────────────────────────────
		{0, true, "horizontal (0°)"},
		{5, true, "near horizontal (5°)"},
		{10, true, "boundary-keep (10° == skewAngleTolDeg)"},
		{80, true, "near 90° from below (80°)"},
		{85, true, "near 90° from below (85°)"},
		{90, true, "vertical (90°)"},
		{95, true, "near 90° from above (95°)"},
		{170, true, "near 180° from below (170°)"},
		{180, true, "horizontal flipped (180°)"},
		{265, true, "near 270° from below (265°)"},
		{270, true, "vertical flipped (270°)"},
		{355, true, "near 360°/0° from below (355°)"},
		{-90, true, "negative vertical (−90°)"},
		{-5, true, "small negative (−5°)"},
		// ── Skew/diagonal: must be dropped ───────────────────────────────────
		{11, false, "just outside tolerance (11°)"},
		{40, false, "shallow diagonal (40°)"},
		{45, false, "pure diagonal (45°) — watermark angle"},
		{135, false, "135° diagonal"},
		{225, false, "225° diagonal"},
		{315, false, "315° diagonal"},
		{-45, false, "negative diagonal (−45°)"},
		{30, false, "30° arc label"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			in := []Text{{S: "x", Rotation: tc.rotation}}
			out := dropSkewRotatedText(in)
			if tc.keep && len(out) == 0 {
				t.Errorf("rotation %.1f°: expected KEEP but got dropped", tc.rotation)
			}
			if !tc.keep && len(out) != 0 {
				t.Errorf("rotation %.1f°: expected DROP but got kept", tc.rotation)
			}
		})
	}
}

// TestDropSkewRotatedTextEmptyInput verifies that a nil/empty slice returns nil.
func TestDropSkewRotatedTextEmptyInput(t *testing.T) {
	t.Parallel()
	if out := dropSkewRotatedText(nil); out != nil {
		t.Errorf("nil input: expected nil output, got %v", out)
	}
	if out := dropSkewRotatedText([]Text{}); out != nil {
		t.Errorf("empty input: expected nil output, got %v", out)
	}
}

// TestDropSkewRotatedTextAllDropped verifies that an all-skew input returns nil
// (not an empty slice), preserving the nil-check contract of wordsFromContent.
func TestDropSkewRotatedTextAllDropped(t *testing.T) {
	t.Parallel()
	in := []Text{{S: "A", Rotation: 45}, {S: "B", Rotation: -45}, {S: "C", Rotation: 135}}
	out := dropSkewRotatedText(in)
	if out != nil {
		t.Errorf("all-dropped: expected nil output, got len=%d", len(out))
	}
}

// TestDropSkewRotatedTextMixed verifies that axis-aligned glyphs survive a
// mixed input that also contains diagonal ones.
func TestDropSkewRotatedTextMixed(t *testing.T) {
	t.Parallel()
	in := []Text{
		{S: "D", Rotation: 0},   // keep
		{S: "W", Rotation: 45},  // drop (watermark)
		{S: "a", Rotation: 90},  // keep (landscape)
		{S: "t", Rotation: 225}, // drop (diagonal)
	}
	out := dropSkewRotatedText(in)
	if len(out) != 2 {
		t.Fatalf("mixed: expected 2 kept glyphs, got %d: %v", len(out), out)
	}
	if out[0].S != "D" || out[1].S != "a" {
		t.Errorf("mixed: kept wrong glyphs: %v", out)
	}
}

// TestDeRotateTableContentNoOp verifies that a page whose glyphs are all
// horizontal (Rotation≈0) returns wasRotated=false and the identical Content
// and MediaBox with zero allocations.
func TestDeRotateTableContentNoOp(t *testing.T) {
	t.Parallel()
	// Three horizontal glyphs: nRot=0 < 3 → gate rejects → no-op path.
	texts := []Text{
		{S: "A", Rotation: 0, X: 10, Y: 100, W: 8, H: 12, FontSize: 12},
		{S: "B", Rotation: 0, X: 20, Y: 100, W: 8, H: 12, FontSize: 12},
		{S: "C", Rotation: 0, X: 30, Y: 100, W: 8, H: 12, FontSize: 12},
	}
	c := Content{Text: texts}
	media := [4]float64{0, 0, 612, 792}

	out, outMedia, wasRotated := deRotateTableContent(c, media)

	if wasRotated {
		t.Fatal("horizontal page: expected wasRotated=false, got true")
	}
	if outMedia != media {
		t.Errorf("no-op: outMedia changed: got %v, want %v", outMedia, media)
	}
	// Zero-allocation proof: the returned Text slice must share the backing
	// array with the input — the no-op path must return c unchanged (no copy).
	if len(out.Text) > 0 && &out.Text[0] != &c.Text[0] {
		t.Error("no-op: Text slice was copied — no-op path must return the input slice unchanged (zero allocations)")
	}
}

// TestDeRotateTableContentExactHalfNoOp locks the strict-majority gate: a page
// that is EXACTLY half rotated (a small portrait table carrying a few vertical
// column-header labels alongside an equal count of horizontal data glyphs) must
// NOT fire — wholesale-rotating it would corrupt the correct portrait table.
// The predicate is nRot*2 > nAll ("more than half"), so 3 rotated + 3 horizontal
// (nRot*2 == nAll) stays a no-op.
func TestDeRotateTableContentExactHalfNoOp(t *testing.T) {
	t.Parallel()
	texts := []Text{
		{S: "A", Rotation: 90, X: 10, Y: 100, W: 0, H: 8},
		{S: "B", Rotation: 90, X: 20, Y: 100, W: 0, H: 8},
		{S: "C", Rotation: 90, X: 30, Y: 100, W: 0, H: 8},
		{S: "1", Rotation: 0, X: 40, Y: 100, W: 8, H: 12, FontSize: 12},
		{S: "2", Rotation: 0, X: 50, Y: 100, W: 8, H: 12, FontSize: 12},
		{S: "3", Rotation: 0, X: 60, Y: 100, W: 8, H: 12, FontSize: 12},
	}
	c := Content{Text: texts}
	media := [4]float64{0, 0, 612, 792}

	out, _, wasRotated := deRotateTableContent(c, media)

	if wasRotated {
		t.Fatal("exact 50/50 split: expected wasRotated=false (strict majority), got true")
	}
	if len(out.Text) > 0 && &out.Text[0] != &c.Text[0] {
		t.Error("exact-half no-op: Text slice was copied — must return the input slice unchanged")
	}
}

// TestDeRotateTableContentMediaBox verifies that deRotateTableContent maps the
// portrait MediaBox to [0, 0, pageH, pageW] and transforms Rect and Stroke
// endpoints correctly under the 90°-CCW rotation.
//
// Transform for a point (x, y) with media=[llx, lly, urx, ury]:
//
//	newX = y − lly   newY = urx − x
func TestDeRotateTableContentMediaBox(t *testing.T) {
	t.Parallel()
	// Portrait MediaBox [10, 20, 622, 812]: lly=20, urx=622, pageH=792, pageW=612.
	// Three rotated glyphs are required to satisfy detectPredominantCCWRotation.
	media := [4]float64{10, 20, 622, 812}
	rotGlyphs := []Text{
		{S: "X", Rotation: 90, X: 50, Y: 300, W: 0, H: 8},
		{S: "Y", Rotation: 90, X: 50, Y: 308, W: 0, H: 8},
		{S: "Z", Rotation: 90, X: 50, Y: 316, W: 0, H: 8},
	}
	// Rect Min=(100,200), Max=(300,400) in portrait.
	//   Min rotated: newX=200−20=180, newY=622−100=522 → (180,522)
	//   Max rotated: newX=400−20=380, newY=622−300=322 → (380,322)
	//   Normalized:  Min=(180,322), Max=(380,522)
	rect := Rect{Min: Point{X: 100, Y: 200}, Max: Point{X: 300, Y: 400}}
	// Stroke From=(100,300) To=(200,500) in portrait.
	//   From rotated: newX=300−20=280, newY=622−100=522 → (280,522)
	//   To   rotated: newX=500−20=480, newY=622−200=422 → (480,422)
	stroke := Stroke{From: Point{X: 100, Y: 300}, To: Point{X: 200, Y: 500}}

	c := Content{Text: rotGlyphs, Rect: []Rect{rect}, Stroke: []Stroke{stroke}}
	out, outMedia, wasRotated := deRotateTableContent(c, media)

	if !wasRotated {
		t.Fatal("expected wasRotated=true for all-rotated content")
	}
	// MediaBox: [0, 0, pageH, pageW]
	pageH := media[3] - media[1] // 812 − 20 = 792
	pageW := media[2] - media[0] // 622 − 10 = 612
	wantMedia := [4]float64{0, 0, pageH, pageW}
	if outMedia != wantMedia {
		t.Errorf("MediaBox: got %v, want %v", outMedia, wantMedia)
	}
	// Rect transform.
	if len(out.Rect) != 1 {
		t.Fatalf("Rect: expected 1 rect, got %d", len(out.Rect))
	}
	wantMin := Point{X: 180, Y: 322}
	wantMax := Point{X: 380, Y: 522}
	if out.Rect[0].Min != wantMin {
		t.Errorf("Rect.Min: got %v, want %v", out.Rect[0].Min, wantMin)
	}
	if out.Rect[0].Max != wantMax {
		t.Errorf("Rect.Max: got %v, want %v", out.Rect[0].Max, wantMax)
	}
	// Stroke transform.
	if len(out.Stroke) != 1 {
		t.Fatalf("Stroke: expected 1 stroke, got %d", len(out.Stroke))
	}
	wantFrom := Point{X: 280, Y: 522}
	wantTo := Point{X: 480, Y: 422}
	if out.Stroke[0].From != wantFrom {
		t.Errorf("Stroke.From: got %v, want %v", out.Stroke[0].From, wantFrom)
	}
	if out.Stroke[0].To != wantTo {
		t.Errorf("Stroke.To: got %v, want %v", out.Stroke[0].To, wantTo)
	}
}

// TestDeRotateTableContentWordOrder is the primary regression lock for the
// landscape-table de-rotation path. It constructs a synthetic 90°-CCW content
// that models the np-nso class of PDFs (landscape statistical tables embedded
// on portrait pages) and asserts that the word assembler produces the correct
// merged, non-reversed output after de-rotation.
//
// Root causes exercised:
//   - W≈0 root cause: rotated glyphs arrive with FontSize≈0, W≈0; de-rotation
//     recovers FontSize←H and W←ΔnewX.
//   - Reversal: the 90°-CCW portrait Y order (small→large) maps to left→right
//     landscape X order (B at newX=50, j at newX=98), producing "Birganj" not
//     "jnagriB".
//   - Merge: zero-gap adjacent glyphs (ΔX=W=8) are joined into one word, not
//     emitted as seven single-character words.
func TestDeRotateTableContentWordOrder(t *testing.T) {
	t.Parallel()
	// Portrait MediaBox [0, 0, 612, 792]: lly=0, urx=612.
	// Landscape row 1 — "Birganj": 7 glyphs at portrait X=100, Y=50..98 (step 8).
	//   After rotPoint90CCW: newX=Y, newY=612−100=512. All in same landscape row.
	//   After deRotateBandGlyphs: ΔX=8 → W=8, FontSize=8, Rotation=0.
	//   wordsFromBand: gap=0 ≤ threshold → all 7 letters join → "Birganj".
	// Landscape row 2 — "67": 2 glyphs at portrait X=200, Y=50..58.
	//   After rotPoint90CCW: newX=Y, newY=612−200=412. Second landscape row.
	media := [4]float64{0, 0, 612, 792}
	const h float64 = 8
	var texts []Text
	for k, ch := range []string{"B", "i", "r", "g", "a", "n", "j"} {
		texts = append(texts, Text{
			S:        ch,
			Rotation: 90,
			X:        100,
			Y:        50 + float64(k)*h,
			W:        0,
			H:        h,
			FontSize: 0,
		})
	}
	for k, ch := range []string{"6", "7"} {
		texts = append(texts, Text{
			S:        ch,
			Rotation: 90,
			X:        200,
			Y:        50 + float64(k)*h,
			W:        0,
			H:        h,
			FontSize: 0,
		})
	}
	// Synthetic cell-bounding rules: a Rect and a Stroke (their transformed
	// coordinates are verified separately in TestDeRotateTableContentMediaBox).
	rects := []Rect{{Min: Point{X: 80, Y: 40}, Max: Point{X: 220, Y: 110}}}
	strokes := []Stroke{{From: Point{X: 80, Y: 75}, To: Point{X: 220, Y: 75}}}

	c := Content{Text: texts, Rect: rects, Stroke: strokes}
	deRotC, _, wasRotated := deRotateTableContent(c, media)

	if !wasRotated {
		t.Fatal("expected wasRotated=true: 9 glyphs at Rotation=90 must exceed the gate threshold")
	}

	words, err := wordsFromContentRecovered(deRotC)
	if err != nil {
		t.Fatalf("wordsFromContentRecovered: %v", err)
	}

	var gotBirganj, got67 bool
	for _, w := range words {
		switch w.S {
		case "Birganj":
			gotBirganj = true
		case "67":
			got67 = true
		}
	}
	if !gotBirganj {
		var ss []string
		for _, w := range words {
			ss = append(ss, w.S)
		}
		t.Errorf("expected word %q (whole, non-reversed); got words: %v", "Birganj", ss)
	}
	if !got67 {
		var ss []string
		for _, w := range words {
			ss = append(ss, w.S)
		}
		t.Errorf("expected word %q (second landscape row); got words: %v", "67", ss)
	}
}

// TestWatermarkVerdict locks the cross-page watermark threshold: a document is watermarked only
// when the SAME diagonal signature (a phrase of >= minWatermarkRunes) recurs on >= watermarkPageFrac
// of sampled pages (a stamp on ~every page), NOT when a few pages carry skew, NOR when a lone short
// rotated symbol recurs. Zero sample → false.
func TestWatermarkVerdict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sig               string
		dominant, sampled int
		want              bool
		note              string
	}{
		{"GKNTÔ", 16, 16, true, "same 5-rune watermark phrase on every sampled page (e.g. vn-gso)"},
		{"ABCDE", 8, 16, true, "phrase recurs on exactly the 0.5 boundary → watermarked"},
		{"Restaurantes", 2, 20, false, "chart labels recur on a minority of pages (ar-indec 2/20)"},
		{"abc", 6, 908, false, "sparse recurrence (it-istat 6/908<1%)"},
		{"GKNTÔ", 0, 0, false, "empty sample guard"},
		{"", 0, 16, false, "no skew on any sampled page → clean"},
		{"§", 16, 16, false, "lone rotated symbol recurs but is not a phrase (< minWatermarkRunes)"},
		{"Whk", 16, 16, false, "short rotated unit (kWh, 3 runes) repeated every page is NOT a watermark (< minWatermarkRunes) — the reviewer's false-positive case"},
	}
	for _, tc := range cases {
		if got := watermarkVerdict(tc.sig, tc.dominant, tc.sampled); got != tc.want {
			t.Errorf("watermarkVerdict(%q,%d,%d)=%v want %v — %s", tc.sig, tc.dominant, tc.sampled, got, tc.want, tc.note)
		}
	}
}

// TestSamplePageIndices locks the EVENLY-SPACED sampling (the prefix-bias fix): for a document
// larger than the cap, the sample must span the whole range — include the last page and not be a
// mere prefix or a fixed parity class.
func TestSamplePageIndices(t *testing.T) {
	t.Parallel()
	// n <= cap → every page.
	if got := samplePageIndices(5, 16); len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Errorf("samplePageIndices(5,16)=%v; want all 5 pages", got)
	}
	// The prefix-bias case the reviewer flagged: n=60, cap=16 must NOT be pages 1..16.
	got := samplePageIndices(60, 16)
	if len(got) != 16 {
		t.Fatalf("samplePageIndices(60,16) len=%d want 16", len(got))
	}
	if got[0] != 1 {
		t.Errorf("first index = %d, want 1", got[0])
	}
	if got[len(got)-1] != 60 {
		t.Errorf("last index = %d, want 60 (sample must reach the final page, not stop at a prefix)", got[len(got)-1])
	}
	// Must reach well beyond a 16-page prefix.
	if got[len(got)-1] <= 16 {
		t.Errorf("sample %v is a prefix — prefix-bias bug not fixed", got)
	}
	// Not a single parity class (the 64-page aliasing concern): indices must include both parities.
	even, odd := 0, 0
	for _, v := range samplePageIndices(64, 16) {
		if v%2 == 0 {
			even++
		} else {
			odd++
		}
	}
	if even == 0 || odd == 0 {
		t.Errorf("samplePageIndices(64,16) hit a single parity class (even=%d odd=%d) — aliasing", even, odd)
	}
}

// TestLayoutFilterDropsSkew is the "do it once" contract test. It verifies that the
// shared layoutFilter projection — the single drop point that Words()/Lines()/Blocks()/
// Tables() all consume via layoutContent() — removes diagonal glyphs before assembly, while
// the low-level wordsFromContent left alone does NOT filter (the filter is layoutFilter's
// job, not the assembler's):
//
//	(a) wordsFromContent on RAW Content KEEPS the diagonal glyph — the assembler itself
//	    does not filter; the raw glyphs remain reachable via Content()/Texts().
//	(b) wordsFromContent on layoutFilter(c) DROPS the diagonal glyph — this is what EVERY
//	    reading-order surface now does (Words/Lines/Blocks/Tables all assemble from
//	    layoutContent()), so a watermark glyph cannot fuse into a value on any of them.
//
// A synthetic Content is used so the test is self-contained and fixture-free.
func TestLayoutFilterDropsSkew(t *testing.T) {
	t.Parallel()

	const (
		y        = 100.0
		h        = 12.0
		fontSize = 12.0
	)
	// Two glyphs at the same Y level, well separated in X so they form distinct
	// words (144 pt gap far exceeds any word-gap threshold).
	dataGlyph := Text{S: "9", Rotation: 0, X: 50, Y: y, W: 6, H: h, FontSize: fontSize}
	skewGlyph := Text{S: "W", Rotation: 45, X: 200, Y: y, W: 8, H: h, FontSize: fontSize}

	c := Content{Text: []Text{dataGlyph, skewGlyph}}

	// ── (a) Raw assembler: wordsFromContent does NOT filter ───────────────────
	// The skew glyph survives — it is layoutFilter, not the assembler, that drops it.
	// (This is why Content()/Texts(), which never pass through layoutFilter, keep the
	// watermark glyphs.)
	rawWords := wordsFromContent(c)
	var gotData, gotSkew bool
	for _, w := range rawWords {
		gotData = gotData || w.S == "9"
		gotSkew = gotSkew || w.S == "W"
	}
	if !gotData {
		t.Error("raw (a): axis-aligned glyph '9' missing from wordsFromContent — unexpected regression")
	}
	if !gotSkew {
		t.Error("raw (a): wordsFromContent must not itself filter — diagonal 'W' should survive (the filter lives in layoutFilter)")
	}

	// ── (b) layoutFilter projection: skew dropped for the prose surfaces ──────
	// On a watermarked document Words()/Lines()/Blocks() assemble from layoutContent() ==
	// layoutFilter of the page Content (and the table grid path always filters), so the
	// diagonal glyph must be absent.
	filtered := layoutFilter(c)
	if filtered.Rect == nil && c.Rect != nil || len(filtered.Stroke) != len(c.Stroke) {
		t.Error("layoutFilter must pass Rect/Stroke through unchanged")
	}
	assembledWords := wordsFromContent(filtered)
	gotData, gotSkew = false, false
	for _, w := range assembledWords {
		gotData = gotData || w.S == "9"
		gotSkew = gotSkew || w.S == "W"
	}
	if !gotData {
		t.Error("filtered (b): axis-aligned glyph '9' missing — layoutFilter must not drop 0° text")
	}
	if gotSkew {
		t.Error("filtered (b): diagonal glyph 'W' survived layoutFilter — watermark would contaminate every assembled surface")
	}
}
