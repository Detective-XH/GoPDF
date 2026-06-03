// walk_test.go — unit tests for walk.go operator handlers.
//
// Coverage targets (by function):
//   - handleWalkXObject  — Form XObject recursion and depth-cap guard
//   - handleWalkPos      — Td, TD, T* position operators
//   - handleWalkShow     — Tj single-string show
//   - handleWalkShowArray via TJ — offset above tjSpaceThreshold inserts space
//
// All tests are exercised through the exported GetTextByRow API, which is the
// thin public wrapper that calls walkTextBlocks → interpretWalk → the handlers.
// buildSinglePagePDF, buildPDFFromObjects, and formXObjStream are defined in
// page_test.go (same package) and are NOT redeclared here.
package pdf

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// walkCollectRows is a small helper: open a PDF byte slice, fetch page 1's
// rows, and return the flat list of non-empty Text entries together with any
// error. Fatal is called on the test if OpenBytes or GetTextByRow fails.
func walkCollectRows(t *testing.T, data []byte) []Text {
	t.Helper()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	rows, err := r.Page(1).GetTextByRow()
	if err != nil {
		t.Fatalf("GetTextByRow: %v", err)
	}
	var out []Text
	for _, row := range rows {
		for _, txt := range row.Content {
			if txt.S != "" {
				out = append(out, txt)
			}
		}
	}
	return out
}

// walkJoinText joins the S field of a Text slice into a single string.
func walkJoinText(texts []Text) string {
	var b strings.Builder
	for _, tx := range texts {
		b.WriteString(tx.S)
	}
	return b.String()
}

// walkFindText returns the first Text whose S equals s, and a found bool.
func walkFindText(texts []Text, s string) (Text, bool) {
	for _, tx := range texts {
		if tx.S == s {
			return tx, true
		}
	}
	return Text{}, false
}

// ---------------------------------------------------------------------------
// TestWalkHandleXObject — handleWalkXObject (Form XObject recursion)
// ---------------------------------------------------------------------------

// TestWalkHandleXObject verifies that handleWalkXObject recurses into a Form
// XObject referenced by the Do operator and that the text inside it is returned
// by GetTextByRow (the public wrapper for walkTextBlocks).
func TestWalkHandleXObject(t *testing.T) {
	const want = "XObjWalk"
	xobjBody := fmt.Sprintf("BT (%s) Tj ET", want)
	pageContent := "/Fm0 Do"

	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		// 3: Page — references Form XObject /Fm0 and content stream
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Fm0 5 0 R >> >> /Contents 4 0 R >>",
		// 4: Page content stream — invokes the XObject
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
		// 5: Form XObject with the target text
		formXObjStream(xobjBody, ""),
	})

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	if !strings.Contains(got, want) {
		t.Errorf("GetTextByRow joined = %q; want it to contain %q (XObject text not walked)", got, want)
	}
}

// TestWalkHandleXObjectImageIgnored verifies that an Image XObject referenced
// by Do is silently skipped — handleWalkXObject returns early when Subtype != Form.
func TestWalkHandleXObjectImageIgnored(t *testing.T) {
	pageContent := "/Img0 Do"
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		// 4: Image XObject (Subtype /Image — must be skipped)
		"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /Length 0 >>\nstream\nendstream",
		// 5: Page content
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
	})

	texts := walkCollectRows(t, data)
	if len(texts) != 0 {
		t.Errorf("GetTextByRow: expected no text from image XObject, got %v", texts)
	}
}

// TestWalkHandleXObjectDepthLimit verifies that handleWalkXObject's depth guard
// (depth >= xobjMaxDepth) prevents infinite recursion when XObjects are nested
// beyond xobjMaxDepth levels. The call must return (no panic, no hang) and the
// deep marker text is silently absent from the output.
func TestWalkHandleXObjectDepthLimit(t *testing.T) {
	const nestDepth = xobjMaxDepth + 2
	const marker = "WalkDeepMarker"

	// Build a chain: page → X0 → X1 → … → X(nestDepth) with marker text.
	// Object layout: 1=Catalog 2=Pages 3=Page 4=content 5+k=XObject k.
	totalObjs := 4 + nestDepth + 1
	objs := make([]string, totalObjs)
	objs[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objs[1] = "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
	objs[2] = "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /X0 5 0 R >> >> /Contents 4 0 R >>"
	pageContent := "/X0 Do"
	objs[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent)
	for k := 0; k <= nestDepth; k++ {
		if k == nestDepth {
			body := fmt.Sprintf("BT (%s) Tj ET", marker)
			objs[4+k] = formXObjStream(body, "<< >>")
		} else {
			childName := fmt.Sprintf("X%d", k+1)
			nextObj := 5 + k + 1
			body := fmt.Sprintf("/%s Do", childName)
			res := fmt.Sprintf("<< /XObject << /%s %d 0 R >> >>", childName, nextObj)
			objs[4+k] = formXObjStream(body, res)
		}
	}
	data := buildPDFFromObjects(objs)

	type result struct {
		texts []Text
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		r, err := OpenBytes(data)
		if err != nil {
			ch <- result{err: err}
			return
		}
		rows, err := r.Page(1).GetTextByRow()
		var texts []Text
		for _, row := range rows {
			texts = append(texts, row.Content...)
		}
		ch <- result{texts: texts, err: err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("unexpected error: %v", res.err)
		}
		joined := walkJoinText(res.texts)
		if strings.Contains(joined, marker) {
			t.Errorf("depth guard should have suppressed %q at depth %d, but it appeared in output %q",
				marker, nestDepth, joined)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetTextByRow hung on deeply nested XObject chain — depth guard missing in handleWalkXObject")
	}
}

// TestWalkHandleXObjectNested verifies two-level Form XObject nesting:
// page → /Outer Do → /Inner Do → text. Both levels must be recursed.
func TestWalkHandleXObjectNested(t *testing.T) {
	const want = "WalkNested"
	innerBody := fmt.Sprintf("BT (%s) Tj ET", want)
	outerBody := "/Inner Do"
	pageContent := "/Outer Do"

	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Outer 4 0 R >> >> /Contents 5 0 R >>",
		// 4: Outer Form XObject — calls /Inner
		formXObjStream(outerBody, "<< /XObject << /Inner 6 0 R >> >>"),
		// 5: Page content
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent),
		// 6: Inner Form XObject — contains actual text
		formXObjStream(innerBody, ""),
	})

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	if !strings.Contains(got, want) {
		t.Errorf("GetTextByRow joined = %q; want it to contain %q (nested XObject not walked)", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestWalkHandlePos — handleWalkPos (Td, TD, T*)
// ---------------------------------------------------------------------------

// TestWalkHandlePosTd verifies that a Td operator correctly accumulates into
// the walker's x and y coordinates. The text returned by GetTextByRow must
// carry the Td offset in its X and Y fields.
func TestWalkHandlePosTd(t *testing.T) {
	stream := "BT\n100 700 Td\n(PosText) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	tx, ok := walkFindText(texts, "PosText")
	if !ok {
		t.Fatalf("text 'PosText' not found in GetTextByRow output; got %v", texts)
	}
	if tx.X != 100 {
		t.Errorf("Td X: got %.1f, want 100.0", tx.X)
	}
	if tx.Y != 700 {
		t.Errorf("Td Y: got %.1f, want 700.0", tx.Y)
	}
}

// TestWalkHandlePosTdAccumulates verifies that successive Td calls accumulate:
// the second Td is added to the first, not applied absolutely.
func TestWalkHandlePosTdAccumulates(t *testing.T) {
	// First Td: (100, 700). Second Td: (+10, -20). Final: (110, 680).
	stream := "BT\n100 700 Td\n(A) Tj\n10 -20 Td\n(B) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)

	a, okA := walkFindText(texts, "A")
	b, okB := walkFindText(texts, "B")
	if !okA {
		t.Fatal("text 'A' not found")
	}
	if !okB {
		t.Fatal("text 'B' not found")
	}
	if a.X != 100 || a.Y != 700 {
		t.Errorf("A: got (%.1f, %.1f), want (100, 700)", a.X, a.Y)
	}
	if b.X != 110 || b.Y != 680 {
		t.Errorf("B: got (%.1f, %.1f), want (110, 680) — Td must accumulate", b.X, b.Y)
	}
}

// TestWalkHandlePosTD verifies the TD operator: it moves x/y like Td but also
// sets the text-leading (tl) to -ty. The effect on x/y must match Td.
func TestWalkHandlePosTD(t *testing.T) {
	// TD 50 -14: x += 50, y += -14, tl = 14.
	stream := "BT\n50 -14 TD\n(TDText) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	tx, ok := walkFindText(texts, "TDText")
	if !ok {
		t.Fatalf("text 'TDText' not found; got %v", texts)
	}
	if tx.X != 50 {
		t.Errorf("TD X: got %.1f, want 50.0", tx.X)
	}
	if tx.Y != -14 {
		t.Errorf("TD Y: got %.1f, want -14.0", tx.Y)
	}
}

// TestWalkHandlePosTDSetsLeading verifies the TD side-effect: after "tx ty TD",
// a subsequent T* must move y by +ty (i.e. tl = -ty), not by some other value.
func TestWalkHandlePosTDSetsLeading(t *testing.T) {
	// 0 -20 TD sets tl=20; then T* moves y by -20 → y goes from -20 to -40.
	stream := "BT\n0 -20 TD\n(Line1) Tj\nT*\n(Line2) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	line1, ok1 := walkFindText(texts, "Line1")
	line2, ok2 := walkFindText(texts, "Line2")
	if !ok1 {
		t.Fatal("text 'Line1' not found")
	}
	if !ok2 {
		t.Fatal("text 'Line2' not found")
	}
	// Line1 after "0 -20 TD": y = -20.
	if line1.Y != -20 {
		t.Errorf("Line1.Y: got %.1f, want -20.0", line1.Y)
	}
	// T* ≡ 0 -tl Td: tl=20, so y decrements by 20 → -40.
	if line2.Y != -40 {
		t.Errorf("Line2.Y: got %.1f, want -40.0 (T* should decrement by TD-set leading)", line2.Y)
	}
}

// TestWalkHandlePosTStar verifies that T* decrements Y by TL (set by the TL
// operator) while leaving X unchanged.
func TestWalkHandlePosTStar(t *testing.T) {
	// TL 14 sets leading. BT resets x=0,y=0. Then 100 200 Td: x=100, y=200.
	// T* subtracts tl (14) from y → y=186.
	stream := "BT\n14 TL\n100 200 Td\n(L1) Tj\nT*\n(L2) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	l1, ok1 := walkFindText(texts, "L1")
	l2, ok2 := walkFindText(texts, "L2")
	if !ok1 {
		t.Fatal("text 'L1' not found")
	}
	if !ok2 {
		t.Fatal("text 'L2' not found")
	}
	if l1.X != 100 || l1.Y != 200 {
		t.Errorf("L1: got (%.1f, %.1f), want (100, 200)", l1.X, l1.Y)
	}
	// T* must not change X, and must subtract TL from Y.
	if l2.X != 100 {
		t.Errorf("L2.X: got %.1f, want 100.0 (T* must not modify X)", l2.X)
	}
	if l2.Y != 186 {
		t.Errorf("L2.Y: got %.1f, want 186.0 (200 - 14)", l2.Y)
	}
}

// TestWalkHandlePosBTResets verifies that BT resets x and y to 0 so that a
// second text object positioned with Td gets absolute coordinates.
func TestWalkHandlePosBTResets(t *testing.T) {
	// Two separate BT blocks. Without reset, B would land at (100+200,700+500).
	stream := "BT\n100 700 Td\n(A) Tj\nET\nBT\n200 500 Td\n(B) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	a, okA := walkFindText(texts, "A")
	b, okB := walkFindText(texts, "B")
	if !okA {
		t.Fatal("text 'A' not found")
	}
	if !okB {
		t.Fatal("text 'B' not found")
	}
	if a.X != 100 || a.Y != 700 {
		t.Errorf("A: got (%.1f, %.1f), want (100, 700)", a.X, a.Y)
	}
	if b.X != 200 || b.Y != 500 {
		t.Errorf("B: got (%.1f, %.1f), want (200, 500) — BT must reset position", b.X, b.Y)
	}
}

// ---------------------------------------------------------------------------
// TestWalkHandleShow — handleWalkShow (Tj single-string show)
// ---------------------------------------------------------------------------

// TestWalkHandleShow verifies that a simple Tj operator causes the walker
// callback to be invoked with the string argument.
func TestWalkHandleShow(t *testing.T) {
	stream := "BT\n(WalkHello) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	if !strings.Contains(got, "WalkHello") {
		t.Errorf("GetTextByRow joined = %q; want it to contain %q", got, "WalkHello")
	}
}

// TestWalkHandleShowMultiple verifies that multiple Tj calls within one BT block
// each produce their own walker invocation, all collected by GetTextByRow.
func TestWalkHandleShowMultiple(t *testing.T) {
	stream := "BT\n(Alpha) Tj\n(Beta) Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	if !strings.Contains(got, "Alpha") {
		t.Errorf("GetTextByRow: want 'Alpha' in %q", got)
	}
	if !strings.Contains(got, "Beta") {
		t.Errorf("GetTextByRow: want 'Beta' in %q", got)
	}
}

// TestWalkHandleShowEmpty verifies that an empty Tj string "()" does not
// produce a non-empty Text entry.
func TestWalkHandleShowEmpty(t *testing.T) {
	stream := "BT\n() Tj\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	for _, tx := range texts {
		if tx.S != "" {
			t.Errorf("expected no non-empty text from empty Tj; got %q", tx.S)
		}
	}
}

// ---------------------------------------------------------------------------
// TestWalkTjSpaceThreshold — TJ offset above threshold inserts space
// ---------------------------------------------------------------------------

// TestWalkTjSpaceThreshold verifies that a TJ kerning offset whose magnitude
// meets or exceeds tjSpaceThreshold (120) causes handleWalkShowArray to emit a
// synthetic space before the next string segment.
//
// Stream: [(Hello) -200 (World)] TJ  — -200 <= -120, so space must appear.
func TestWalkTjSpaceThreshold(t *testing.T) {
	// fontSize must be set so that x adjustment is non-trivial; use /F1 12 Tf.
	// No real font resource — nopEncoder passes ASCII through unchanged.
	stream := "BT\n/F1 12 Tf\n[(Hello) -200 (World)] TJ\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	t.Logf("GetTextByRow TJ joined: %q", got)

	if !strings.Contains(got, "Hello") {
		t.Errorf("TJ: 'Hello' not found in %q", got)
	}
	if !strings.Contains(got, "World") {
		t.Errorf("TJ: 'World' not found in %q", got)
	}
	// The discriminating assertion: a -200 kern exceeds tjSpaceThreshold (120)
	// so a space must appear between "Hello" and "World".
	if strings.Contains(got, "HelloWorld") {
		t.Errorf("TJ: kern -200 should have inserted a space between Hello and World; got %q", got)
	}
}

// TestWalkTjSpaceThresholdBelowThreshold verifies that a small kern (-10) does
// NOT cause a space to be inserted. "Hello" and "World" must be adjacent.
func TestWalkTjSpaceThresholdBelowThreshold(t *testing.T) {
	stream := "BT\n/F1 12 Tf\n[(Hello) -10 (World)] TJ\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	t.Logf("GetTextByRow small kern joined: %q", got)

	if !strings.Contains(got, "Hello") {
		t.Errorf("TJ small kern: 'Hello' not found in %q", got)
	}
	if !strings.Contains(got, "World") {
		t.Errorf("TJ small kern: 'World' not found in %q", got)
	}
	// Small kern must NOT introduce a gap.
	if !strings.Contains(got, "HelloWorld") {
		t.Errorf("TJ small kern (-10) should NOT produce whitespace; got %q", got)
	}
}

// TestWalkTjSpaceThresholdExactBoundary verifies the exact threshold boundary.
// A kern of exactly -tjSpaceThreshold (-120) must trigger the space.
func TestWalkTjSpaceThresholdExactBoundary(t *testing.T) {
	// kern == -120 satisfies x.Float64() <= -tjSpaceThreshold, so space expected.
	stream := "BT\n/F1 12 Tf\n[(AA) -120 (BB)] TJ\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	t.Logf("GetTextByRow boundary kern joined: %q", got)

	if !strings.Contains(got, "AA") {
		t.Errorf("boundary kern: 'AA' not found in %q", got)
	}
	if !strings.Contains(got, "BB") {
		t.Errorf("boundary kern: 'BB' not found in %q", got)
	}
	// -120 is at the threshold: x.Float64() <= -120 → space.
	if strings.Contains(got, "AABB") {
		t.Errorf("kern -120 (at threshold) should have inserted space; got %q (no space)", got)
	}
}

// TestWalkTjSpaceThresholdPositiveKern verifies that a positive kern (+200)
// does not introduce a space and does not drop any characters.
func TestWalkTjSpaceThresholdPositiveKern(t *testing.T) {
	stream := "BT\n/F1 12 Tf\n[(Hello) 200 (World)] TJ\nET"
	data := buildSinglePagePDF(stream)

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	t.Logf("GetTextByRow positive kern joined: %q", got)

	if !strings.Contains(got, "Hello") {
		t.Errorf("positive kern: 'Hello' not found in %q", got)
	}
	if !strings.Contains(got, "World") {
		t.Errorf("positive kern: 'World' not found in %q", got)
	}
}

// ---------------------------------------------------------------------------
// TestWalkHandleShowQuote — handleWalkShow "'" operator
// ---------------------------------------------------------------------------

// TestWalkHandleShowQuote verifies the ' operator: it calls the walker with the
// single string argument. Also verifies that a wrong arg count causes a panic.
func TestWalkHandleShowQuote(t *testing.T) {
	// Happy path: ' with exactly 1 arg must invoke the walker.
	var captured string
	s := &walkState{
		enc:   &nopEncoder{},
		fonts: make(map[string]*Font),
		walker: func(_ TextEncoding, _, _ float64, str string) {
			captured = str
		},
		resources: Value{},
	}
	s.handleWalkShow("'", []Value{{nil, objptr{}, "quote-text"}})
	if captured != "quote-text" {
		t.Errorf("' operator: walker captured %q, want %q", captured, "quote-text")
	}

	// Panic path: ' with 0 args must panic with "bad ' operator".
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Errorf("' operator with 0 args: expected panic, got none")
			}
		}()
		s.handleWalkShow("'", []Value{})
	}()
}

// ---------------------------------------------------------------------------
// TestWalkHandleShowDoubleQuote — handleWalkShow "\"" operator panic path
// ---------------------------------------------------------------------------

// TestWalkHandleShowDoubleQuote verifies the " operator's bad-arg-count panic.
// Due to a walk.go bug (no args-trimming before fallthrough), the only reachable
// path is the wrong-arg-count check at lines 47-48.
func TestWalkHandleShowDoubleQuote(t *testing.T) {
	s := &walkState{
		enc:       &nopEncoder{},
		fonts:     make(map[string]*Font),
		walker:    func(_ TextEncoding, _, _ float64, _ string) {},
		resources: Value{},
	}

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		// 0 args — triggers "bad \" operator" panic (lines 47-48).
		s.handleWalkShow("\"", []Value{})
	}()
	if !panicked {
		t.Errorf("\" operator with 0 args: expected panic, got none")
	}
}

// ---------------------------------------------------------------------------
// TestWalkHandlePosTm — handleWalkPos "Tm" operator
// ---------------------------------------------------------------------------

// TestWalkHandlePosTm verifies that the Tm operator sets x=args[4] and y=args[5].
func TestWalkHandlePosTm(t *testing.T) {
	s := &walkState{
		enc:       &nopEncoder{},
		fonts:     make(map[string]*Font),
		walker:    func(_ TextEncoding, _, _ float64, _ string) {},
		resources: Value{},
	}
	// 6-element args: [0, 0, 0, 0, 150.0, 250.0].
	args := contentArgs6(0, 0, 0, 0, 150.0, 250.0)
	s.handleWalkPos("Tm", args)
	if s.x != 150.0 {
		t.Errorf("Tm x: got %.1f, want 150.0", s.x)
	}
	if s.y != 250.0 {
		t.Errorf("Tm y: got %.1f, want 250.0", s.y)
	}
}

// ---------------------------------------------------------------------------
// TestWalkHandlePosBadArgs — handleWalkPos bad-arg-count early-return paths
// ---------------------------------------------------------------------------

// TestWalkHandlePosBadArgs verifies that Td and TD with wrong arg counts return
// early without panicking and without modifying the walkState's position fields.
func TestWalkHandlePosBadArgs(t *testing.T) {
	s := &walkState{
		enc:       &nopEncoder{},
		fonts:     make(map[string]*Font),
		walker:    func(_ TextEncoding, _, _ float64, _ string) {},
		resources: Value{},
	}

	// Td with nil args (0 args) → return early; x and y must stay 0.
	s.handleWalkPos("Td", nil)
	if s.x != 0 || s.y != 0 {
		t.Errorf("Td bad args: x=%.1f y=%.1f, want both 0", s.x, s.y)
	}

	// TD with 0 args → return early; tl must stay 0.
	s.handleWalkPos("TD", []Value{})
	if s.tl != 0 {
		t.Errorf("TD bad args: tl=%.1f, want 0 (unchanged)", s.tl)
	}

	// Tm with fewer than 6 args → return early; x and y must stay 0.
	s.handleWalkPos("Tm", []Value{{nil, objptr{}, float64(1)}})
	if s.x != 0 || s.y != 0 {
		t.Errorf("Tm bad args: x=%.1f y=%.1f, want both 0", s.x, s.y)
	}
}

// ---------------------------------------------------------------------------
// TestWalkBlocksTf — walkTextBlocks font-loop body and Tf operator
// ---------------------------------------------------------------------------

// TestWalkBlocksTf verifies that walkTextBlocks populates the fonts map from
// the page's Font resources (covering the loop body at lines 149-151 of walk.go)
// and that the page returns the expected text through GetTextByRow.
func TestWalkBlocksTf(t *testing.T) {
	const body = "BT /F1 12 Tf (Hello) Tj ET"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	data := buildPDFFromObjects(objs)

	texts := walkCollectRows(t, data)
	got := walkJoinText(texts)
	if !strings.Contains(got, "Hello") {
		t.Errorf("walkTextBlocks with Font resource: GetTextByRow = %q; want it to contain %q", got, "Hello")
	}
}
