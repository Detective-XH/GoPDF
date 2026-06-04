// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// content_test.go — unit and integration tests for content.go internals.
//
// Test coverage:
//   - TestContentHandleTm          Tm operator sets both text matrices; subsequent Td position
//   - TestContentHandleTextParams  Tc/Tw/Tz/Ts operators update gstate fields
//   - TestContentRequireOneArg     requireOneArg panics on wrong arg count, no-ops on 1
//   - TestContentMatrixFrom6Args   6-element stack → correct 3×3 matrix
//   - TestContentXObjectDepth      Form XObject chain beyond xobjMaxDepth terminates
//   - BenchmarkContentInterpret    multi-operator content stream throughput
package pdf

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// contentMakeState returns a minimal, usable *contentState with Th=1 and CTM=identity.
// All fields are zero-valued except those required for the operator handlers to
// not panic (enc must be non-nil; Th must be 1 so text-matrix math works).
func contentMakeState() *contentState {
	return &contentState{
		g: gstate{Th: 1, CTM: ident, enc: &nopEncoder{}},
	}
}

// contentMakeFloat64Value wraps a float64 as a Value with no reader, suitable
// for use as an operator argument (mimics how Interpret pushes numeric tokens).
func contentMakeFloat64Value(f float64) Value {
	return Value{nil, objptr{}, f}
}

// contentArgs6 builds a 6-element []Value of float64s, in order.
func contentArgs6(a, b, c, d, e, f float64) []Value {
	return []Value{
		contentMakeFloat64Value(a),
		contentMakeFloat64Value(b),
		contentMakeFloat64Value(c),
		contentMakeFloat64Value(d),
		contentMakeFloat64Value(e),
		contentMakeFloat64Value(f),
	}
}

// contentArgs1 builds a single-element []Value of float64.
func contentArgs1(f float64) []Value {
	return []Value{contentMakeFloat64Value(f)}
}

// ---------------------------------------------------------------------------
// TestContentHandleTm — Tm operator sets text matrix; subsequent Td moves it
// ---------------------------------------------------------------------------

// TestContentHandleTm verifies that handleTm stores the given 6-element matrix
// in both s.g.Tm and s.g.Tlm, and that a subsequent Td call moves the position
// relative to the new Tm (not the old identity).
//
// The matrix used is a pure translation [1 0 0 1 tx ty] (a=1, b=0, c=0, d=1,
// e=tx, f=ty), which maps as:
//
//	m[0][0]=a=1, m[0][1]=b=0
//	m[1][0]=c=0, m[1][1]=d=1
//	m[2][0]=e=tx, m[2][1]=f=ty
//	m[2][2]=1
//
// After Tm(1,0,0,1,50,100), s.g.Tm[2][0] must equal 50 and s.g.Tm[2][1] must
// equal 100 (the translation components of the matrix).
func TestContentHandleTm(t *testing.T) {
	s := contentMakeState()

	const wantX, wantY = 50.0, 100.0
	// Tm operator: a b c d e f — a pure translation matrix.
	args := contentArgs6(1, 0, 0, 1, wantX, wantY)
	s.handleTm(args)

	// Both Tm and Tlm must be set to the same matrix.
	if s.g.Tm[2][0] != wantX {
		t.Errorf("Tm[2][0]: got %v, want %v", s.g.Tm[2][0], wantX)
	}
	if s.g.Tm[2][1] != wantY {
		t.Errorf("Tm[2][1]: got %v, want %v", s.g.Tm[2][1], wantY)
	}
	if s.g.Tlm[2][0] != wantX {
		t.Errorf("Tlm[2][0]: got %v, want %v", s.g.Tlm[2][0], wantX)
	}
	if s.g.Tlm[2][1] != wantY {
		t.Errorf("Tlm[2][1]: got %v, want %v", s.g.Tlm[2][1], wantY)
	}

	// After Td(dx, dy), the text line matrix must advance by (dx, dy).
	const dx, dy = 10.0, -20.0
	s.handleTd([]Value{contentMakeFloat64Value(dx), contentMakeFloat64Value(dy)})

	wantTmX := wantX + dx // = 60
	wantTmY := wantY + dy // = 80
	if s.g.Tm[2][0] != wantTmX {
		t.Errorf("after Td: Tm[2][0] = %v, want %v", s.g.Tm[2][0], wantTmX)
	}
	if s.g.Tm[2][1] != wantTmY {
		t.Errorf("after Td: Tm[2][1] = %v, want %v", s.g.Tm[2][1], wantTmY)
	}
}

// TestContentHandleTmPanicOnBadArgs verifies that handleTm panics when given
// fewer than 6 arguments (malformed PDF content stream).
func TestContentHandleTmPanicOnBadArgs(t *testing.T) {
	s := contentMakeState()
	defer func() {
		if r := recover(); r == nil {
			t.Error("handleTm with 5 args: expected panic, got none")
		}
	}()
	// Only 5 arguments — must panic.
	args := contentArgs6(1, 0, 0, 1, 50, 100)
	s.handleTm(args[:5])
}

// ---------------------------------------------------------------------------
// TestContentHandleTextParams — Tc/Tw/Tz/Ts update gstate fields
// ---------------------------------------------------------------------------

// TestContentHandleTextParams verifies that each scalar text-state operator
// writes the expected float64 into the right gstate field.
//
//   - Tc  → g.Tc  (character spacing)
//   - Tw  → g.Tw  (word spacing)
//   - Tz  → g.Th  (horizontal scaling; stored as value/100)
//   - Ts  → g.Trise (text rise)
func TestContentHandleTextParams(t *testing.T) {
	cases := []struct {
		op      string
		val     float64
		check   func(g gstate) float64
		wantVal float64
	}{
		{
			op:      "Tc",
			val:     2.5,
			check:   func(g gstate) float64 { return g.Tc },
			wantVal: 2.5,
		},
		{
			op:      "Tw",
			val:     1.0,
			check:   func(g gstate) float64 { return g.Tw },
			wantVal: 1.0,
		},
		{
			op:    "Tz",
			val:   150, // Tz stores value/100 in Th
			check: func(g gstate) float64 { return g.Th },
			// 150/100 = 1.5
			wantVal: 1.5,
		},
		{
			op:      "Ts",
			val:     3.0,
			check:   func(g gstate) float64 { return g.Trise },
			wantVal: 3.0,
		},
		{
			op:      "TL",
			val:     12.0,
			check:   func(g gstate) float64 { return g.Tl },
			wantVal: 12.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			s := contentMakeState()
			s.handleTextParams(tc.op, contentArgs1(tc.val))
			got := tc.check(s.g)
			if got != tc.wantVal {
				t.Errorf("%s(%v): got %v, want %v", tc.op, tc.val, got, tc.wantVal)
			}
		})
	}
}

// TestContentHandleTextParamsTr verifies that the Tr (rendering mode) operator
// stores the integer value in g.Tmode. Tr uses Int64(), not Float64().
func TestContentHandleTextParamsTr(t *testing.T) {
	s := contentMakeState()
	s.handleTextParams("Tr", []Value{{nil, objptr{}, int64(2)}})
	if s.g.Tmode != 2 {
		t.Errorf("Tr(2): g.Tmode = %d, want 2", s.g.Tmode)
	}
}

// TestContentHandleTextParamsPanicOnEmpty verifies that requireOneArg (called
// by handleTextParams) panics when the argument slice is empty.
func TestContentHandleTextParamsPanicOnEmpty(t *testing.T) {
	s := contentMakeState()
	defer func() {
		if r := recover(); r == nil {
			t.Error("handleTextParams with 0 args: expected panic, got none")
		}
	}()
	s.handleTextParams("Tc", []Value{})
}

// ---------------------------------------------------------------------------
// TestContentRequireOneArg — requireOneArg panics iff len != 1
// ---------------------------------------------------------------------------

// TestContentRequireOneArgPanicsOnEmpty verifies that requireOneArg panics
// when given zero arguments.
func TestContentRequireOneArgPanicsOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("requireOneArg with 0 args: expected panic, got none")
		}
	}()
	requireOneArg([]Value{}, "op")
}

// TestContentRequireOneArgPanicsOnTwo verifies that requireOneArg panics when
// given two arguments (too many).
func TestContentRequireOneArgPanicsOnTwo(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("requireOneArg with 2 args: expected panic, got none")
		}
	}()
	requireOneArg([]Value{contentMakeFloat64Value(1), contentMakeFloat64Value(2)}, "op")
}

// TestContentRequireOneArgNoOp verifies that requireOneArg does not panic
// when given exactly one argument.
func TestContentRequireOneArgNoOp(t *testing.T) {
	// Must not panic.
	requireOneArg([]Value{contentMakeFloat64Value(42)}, "op")
}

// ---------------------------------------------------------------------------
// TestContentMatrixFrom6Args — 6-element args → 3×3 matrix
// ---------------------------------------------------------------------------

// TestContentMatrixFrom6Args verifies that matrixFrom6Args assembles the PDF
// 6-element form [a b c d e f] into the expected 3×3 column-major matrix:
//
//	[a c e]   m[0][0]=a  m[0][1]=b
//	[b d f]   m[1][0]=c  m[1][1]=d
//	[0 0 1]   m[2][0]=e  m[2][1]=f  m[2][2]=1
//
// The indexing used in matrixFrom6Args is:
//
//	for i in range 6: m[i/2][i%2] = args[i]
//
// which maps: args[0]→m[0][0], args[1]→m[0][1], args[2]→m[1][0], args[3]→m[1][1],
// args[4]→m[2][0], args[5]→m[2][1].
func TestContentMatrixFrom6Args(t *testing.T) {
	// Use distinct non-zero values so any transposition is detectable.
	a, b, c, d, e, f := 2.0, 3.0, 5.0, 7.0, 11.0, 13.0
	args := contentArgs6(a, b, c, d, e, f)
	m := matrixFrom6Args(args)

	checks := []struct {
		row, col int
		want     float64
	}{
		{0, 0, a},
		{0, 1, b},
		{1, 0, c},
		{1, 1, d},
		{2, 0, e},
		{2, 1, f},
		{2, 2, 1}, // always 1
		// off-diagonal corners must be zero
		{0, 2, 0},
		{1, 2, 0},
	}

	for _, tc := range checks {
		got := m[tc.row][tc.col]
		if got != tc.want {
			t.Errorf("m[%d][%d] = %v, want %v", tc.row, tc.col, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestContentXObjectDepth — Form XObject chain beyond xobjMaxDepth terminates
// ---------------------------------------------------------------------------

// contentBuildDeepXObjectPDF constructs a PDF with a chain of Form XObjects
// nested n levels deep.  Text at the innermost level is "marker".  The chain
// is: page content → /X0 Do → /X1 Do → … → /X(n) Do → BT (marker) Tj ET.
//
// This is a local variant of buildDeepXObjectPDF (xobject_text_test.go) kept
// here to avoid cross-test-file coupling; it uses buildPDFFromObjects and
// formXObjStream which are defined in page_test.go (same package).
func contentBuildDeepXObjectPDF(n int, marker string) []byte {
	totalObjs := 4 + n + 1 // Catalog + Pages + Page + content + (n+1) xobjs

	xobjObjs := make([]string, n+1)
	for k := 0; k <= n; k++ {
		objNum := 5 + k
		if k == n {
			body := fmt.Sprintf("BT (%s) Tj ET", marker)
			xobjObjs[k] = formXObjStream(body, "<< >>")
		} else {
			nextObjNum := objNum + 1
			childName := fmt.Sprintf("X%d", k+1)
			body := fmt.Sprintf("/%s Do", childName)
			res := fmt.Sprintf("<< /XObject << /%s %d 0 R >> >>", childName, nextObjNum)
			xobjObjs[k] = formXObjStream(body, res)
		}
	}

	objs := make([]string, totalObjs)
	objs[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objs[1] = "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
	objs[2] = "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /X0 5 0 R >> >> /Contents 4 0 R >>"
	pageContent := "/X0 Do"
	objs[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent)
	for k, s := range xobjObjs {
		objs[4+k] = s
	}

	return buildPDFFromObjects(objs)
}

// TestContentXObjectDepth verifies that a Form XObject chain nested beyond
// xobjMaxDepth terminates without panic or infinite loop, and that text at the
// unreachable level is silently absent from Content().Text output.
func TestContentXObjectDepth(t *testing.T) {
	const nestDepth = xobjMaxDepth + 2 // two levels past the cap
	const marker = "ContentDepthMarker"

	data := contentBuildDeepXObjectPDF(nestDepth, marker)

	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		r, err := OpenBytes(data)
		if err != nil {
			ch <- result{err: err}
			return
		}
		texts := r.Page(1).Content().Text
		var sb strings.Builder
		for _, tx := range texts {
			sb.WriteString(tx.S)
		}
		ch <- result{text: sb.String()}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("TestContentXObjectDepth: OpenBytes error: %v", res.err)
		}
		// The depth guard silently truncates; marker must not appear in output.
		if strings.Contains(res.text, marker) {
			t.Errorf("Content().Text = %q; depth cap should have suppressed %q at depth %d",
				res.text, marker, nestDepth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TestContentXObjectDepth: Content().Text hung on deeply nested XObject chain")
	}
}

// ---------------------------------------------------------------------------
// BenchmarkContentInterpret — multi-operator content stream throughput
// ---------------------------------------------------------------------------

// contentBuildBenchmarkPDF constructs a minimal PDF with a rich content stream
// that exercises text-state, graphics-state, and text-show operators.
func contentBuildBenchmarkPDF() []byte {
	// Stream exercises: BT, Td, Tc, Tw, Tz, Ts, Tj, TJ, T*, ET, q, re, Q, cm.
	stream := strings.Join([]string{
		"q",
		"1 0 0 1 10 10 cm",
		"10 20 100 50 re",
		"Q",
		"BT",
		"1 0 0 1 50 700 Tm",
		"2 Tc",
		"1 Tw",
		"100 Tz",
		"2 Ts",
		"14 TL",
		"(Hello World) Tj",
		"T*",
		"[(Kern) -120 (ed)] TJ",
		"0 -20 Td",
		"(More text here) Tj",
		"ET",
	}, "\n")

	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	})
}

// BenchmarkContentInterpret measures Content() throughput on a synthetic
// multi-operator stream that exercises all major operator categories.
func BenchmarkContentInterpret(b *testing.B) {
	data := contentBuildBenchmarkPDF()
	r, err := OpenBytes(data)
	if err != nil {
		b.Fatalf("OpenBytes: %v", err)
	}
	page := r.Page(1)
	b.ResetTimer()
	for b.Loop() {
		_ = page.Content()
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleTextShowQuote — ' operator applies Td(0,-Tl) and appends text
// ---------------------------------------------------------------------------

// TestContentHandleTextShowQuote verifies that the single-quote (') operator
// moves the text position down by Tl (equivalent to T*) and then appends the
// string argument as individual rune entries in s.text.
//
// appendText appends one Text entry per rune, so the assertion collects all
// S fields and compares the concatenation against the input string.
func TestContentHandleTextShowQuote(t *testing.T) {
	s := contentMakeState()

	// Seed a known Tm via handleTm so that applyTd has a non-zero base.
	s.handleTm(contentArgs6(1, 0, 0, 1, 100, 200))
	s.g.Tl = 10.0

	strVal := Value{nil, objptr{}, "hello"}
	s.handleTextShow("'", []Value{strVal})

	// Verify text was appended: concatenate all S fields (one per rune).
	var sb strings.Builder
	for _, tx := range s.text {
		sb.WriteString(tx.S)
	}
	if got := sb.String(); got != "hello" {
		t.Errorf("' operator: concatenated text = %q, want %q", got, "hello")
	}

	// Verify Tm shifted down by Tl=10: Tm[2][1] must equal 200 - 10 = 190.
	// applyTd multiplied into Tlm (seeded to 100,200); Tm follows Tlm.
	const wantY = 190.0
	if s.g.Tm[2][1] != wantY {
		t.Errorf("' operator: Tm[2][1] = %v, want %v", s.g.Tm[2][1], wantY)
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleTextShowDoubleQuote — " operator sets Tw, Tc, appends text
// ---------------------------------------------------------------------------

// TestContentHandleTextShowDoubleQuote verifies that the double-quote (")
// operator correctly sets g.Tw from the first arg, g.Tc from the second arg,
// and appends the string (third arg) to s.text.
//
// The implementation trims args to [str] before fallthrough to the ' case, so
// this also exercises the Tl-shift path.
func TestContentHandleTextShowDoubleQuote(t *testing.T) {
	s := contentMakeState()
	// Seed Tm so applyTd has a non-zero base (required for the ' fallthrough).
	s.handleTm(contentArgs6(1, 0, 0, 1, 0, 0))

	args := []Value{
		contentMakeFloat64Value(0.5),  // aw → Tw
		contentMakeFloat64Value(0.25), // ac → Tc
		{nil, objptr{}, "dq-text"},    // string
	}
	s.handleTextShow("\"", args)

	if s.g.Tw != 0.5 {
		t.Errorf("\" operator: g.Tw = %v, want 0.5", s.g.Tw)
	}
	if s.g.Tc != 0.25 {
		t.Errorf("\" operator: g.Tc = %v, want 0.25", s.g.Tc)
	}

	var sb strings.Builder
	for _, tx := range s.text {
		sb.WriteString(tx.S)
	}
	if got := sb.String(); got != "dq-text" {
		t.Errorf("\" operator: concatenated text = %q, want %q", got, "dq-text")
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleTextMatrixET — ET is a no-op
// ---------------------------------------------------------------------------

// TestContentHandleTextMatrixET verifies that the ET operator leaves Tm and Tl
// unchanged (it is documented and implemented as a deliberate no-op).
func TestContentHandleTextMatrixET(t *testing.T) {
	s := contentMakeState()
	s.handleTm(contentArgs6(1, 0, 0, 1, 30, 40))
	s.g.Tl = 14.0

	tmBefore := s.g.Tm
	s.handleTextMatrix("ET", nil)

	if s.g.Tm != tmBefore {
		t.Errorf("ET operator: Tm changed from %v to %v, want no change", tmBefore, s.g.Tm)
	}
	if s.g.Tl != 14.0 {
		t.Errorf("ET operator: Tl = %v, want 14.0", s.g.Tl)
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleTextMatrixTStar — T* applies Td(0, -Tl)
// ---------------------------------------------------------------------------

// TestContentHandleTextMatrixTStar verifies that the T* operator advances the
// text line by (0, -Tl), identical to calling Td(0, -Tl).
func TestContentHandleTextMatrixTStar(t *testing.T) {
	s := contentMakeState()
	s.handleTm(contentArgs6(1, 0, 0, 1, 100, 200))
	s.g.Tl = 12.0

	s.handleTextMatrix("T*", nil)

	const wantY = 200 - 12 // = 188
	if s.g.Tm[2][1] != wantY {
		t.Errorf("T* operator: Tm[2][1] = %v, want %v", s.g.Tm[2][1], wantY)
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleTextMatrixTD — TD sets Tl = -ty and applies Td(tx, ty)
// ---------------------------------------------------------------------------

// TestContentHandleTextMatrixTD verifies that the TD operator sets g.Tl to the
// negation of the y argument and then moves the text position by (tx, ty).
// BT is called first to seed identity Tm/Tlm, so applyTd starts from origin.
func TestContentHandleTextMatrixTD(t *testing.T) {
	s := contentMakeState()
	// Seed identity matrices via BT so that applyTd starts from a known origin.
	s.handleTextMatrix("BT", nil)

	args := []Value{
		contentMakeFloat64Value(10),
		contentMakeFloat64Value(-15),
	}
	s.handleTextMatrix("TD", args)

	// Tl must be set to -(-15) = 15.
	if s.g.Tl != 15.0 {
		t.Errorf("TD operator: g.Tl = %v, want 15.0", s.g.Tl)
	}
	// Tm must reflect the Td(10, -15) applied to the identity origin.
	if s.g.Tm[2][0] != 10 {
		t.Errorf("TD operator: Tm[2][0] = %v, want 10", s.g.Tm[2][0])
	}
	if s.g.Tm[2][1] != -15 {
		t.Errorf("TD operator: Tm[2][1] = %v, want -15", s.g.Tm[2][1])
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleGraphicsCm — cm post-multiplies a translation into CTM
// ---------------------------------------------------------------------------

// TestContentHandleGraphicsCm verifies that the cm operator post-multiplies a
// pure-translation matrix [1 0 0 1 50 100] into the current CTM.
// Starting from identity, the result must have CTM[2][0]==50 and CTM[2][1]==100.
func TestContentHandleGraphicsCm(t *testing.T) {
	s := contentMakeState()
	// CTM is already identity (set by contentMakeState via gstate{CTM: ident}).

	s.handleGraphics("cm", contentArgs6(1, 0, 0, 1, 50, 100))

	if s.g.CTM[2][0] != 50.0 {
		t.Errorf("cm operator: CTM[2][0] = %v, want 50.0", s.g.CTM[2][0])
	}
	if s.g.CTM[2][1] != 100.0 {
		t.Errorf("cm operator: CTM[2][1] = %v, want 100.0", s.g.CTM[2][1])
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleGraphicsRe — re appends a Rect derived from (x, y, w, h)
// ---------------------------------------------------------------------------

// TestContentHandleGraphicsRe verifies that the re operator appends a Rect with
// Min=Point{x,y} and Max=Point{x+w, y+h} to s.rect.
func TestContentHandleGraphicsRe(t *testing.T) {
	s := contentMakeState()

	args := []Value{
		contentMakeFloat64Value(10),
		contentMakeFloat64Value(20),
		contentMakeFloat64Value(100),
		contentMakeFloat64Value(50),
	}
	s.handleGraphics("re", args)

	if len(s.rect) != 1 {
		t.Fatalf("re operator: len(s.rect) = %d, want 1", len(s.rect))
	}
	wantMin := Point{10, 20}
	wantMax := Point{110, 70}
	if s.rect[0].Min != wantMin {
		t.Errorf("re operator: rect.Min = %v, want %v", s.rect[0].Min, wantMin)
	}
	if s.rect[0].Max != wantMax {
		t.Errorf("re operator: rect.Max = %v, want %v", s.rect[0].Max, wantMax)
	}
}

// ---------------------------------------------------------------------------
// TestContentHandleTfNilEncoder — handleTf with missing font uses byteEncoder fallback
// ---------------------------------------------------------------------------

// TestContentHandleTfNilEncoder verifies that handleTf sets s.g.enc to a non-nil
// encoder even when the font resource is absent (empty resources Value).
//
// The call path is: Font.Encoder() → getEncoder() → Encoding kind==Null →
// returns &byteEncoder{&pdfDocEncoding}.  The explicit nil-check branch in
// handleTf is dead code; this test documents the actually-reachable fallback.
func TestContentHandleTfNilEncoder(t *testing.T) {
	s := contentMakeState()
	// s.resources is zero Value; Key("Font").Key("F1") resolves to null Value.

	args := []Value{
		{nil, objptr{}, name("F1")}, // font name arg
		contentMakeFloat64Value(12), // Tfs arg
	}
	s.handleTf(args)

	if s.g.enc == nil {
		t.Error("handleTf with missing font resource: s.g.enc is nil, want non-nil encoder")
	}
	if s.g.Tfs != 12.0 {
		t.Errorf("handleTf: g.Tfs = %v, want 12.0", s.g.Tfs)
	}
}
