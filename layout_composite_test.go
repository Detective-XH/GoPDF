// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// layout_composite_test.go — unit test for the multi-rune-CID advance fix in
// layoutComposite (content.go).
//
// TestLayoutCompositeMultiRuneCID locks the invariant: when a single CID decodes
// to N runes (a ligature), all runes are placed at the CID's origin (zero
// advance), and ONLY the last rune carries the full CID width — so the next CID
// starts correctly past the entire ligature, never overlapping any of its runes.
package pdf

import "testing"

// mockTwoByteDecoder is a minimal codeDecoder for testing layoutComposite.
// It uses a 2-byte stride:
//   - "\x00\x01" → ['f','i']  (ligature: one CID, two runes)
//   - "\x00\x02" → ['x']      (normal: one CID, one rune)
//   - any other 2-byte code   → [noRune] (fallthrough / unknown)
//
// It also satisfies TextEncoding (Decode) so it can be stored in g.enc if
// ever needed, though the test calls layoutComposite directly.
type mockTwoByteDecoder struct{}

func (m *mockTwoByteDecoder) Decode(raw string) string { return raw }

func (m *mockTwoByteDecoder) decodeOne(raw string) ([]rune, int) {
	if len(raw) < 2 {
		return nil, 0
	}
	switch raw[:2] {
	case "\x00\x01":
		return []rune{'f', 'i'}, 2
	case "\x00\x02":
		return []rune{'x'}, 2
	default:
		return []rune{noRune}, 2
	}
}

// buildDWZeroType0Font constructs a minimal Type0 Font with /DW==0 and a /W
// array that covers CIDs 1 and 2 at width 1000 using Form-2 (cFirst cLast w).
// cidWidth(1) and cidWidth(2) must both return 1000.
func buildDWZeroType0Font() Font {
	r := fontTestEmptyReader()
	// /W = [1 2 1000]: Form-2 entry covering CIDs 1..2 at width 1000.
	wArray := array{int64(1), int64(2), int64(1000)}
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("DW"):      int64(0),
		name("W"):       wArray,
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	return fontTestFontValue(r, fontDict)
}

// TestLayoutCompositeMultiRuneCID verifies the "advance on last rune" invariant
// introduced to fix the ligature-overlap bug in layoutComposite.
//
// Scenario: a DW==0 Type0 font; the input string "\x00\x01\x00\x02" contains
// two CIDs. CID 1 (bytes "\x00\x01") decodes to the ligature ['f','i'], while
// CID 2 (bytes "\x00\x02") decodes to ['x']. Both CIDs have width 1000 in /W.
//
// With Tfs=1, Th=1, Tc=0, Tm=ident, CTM=ident the advance per CID is
// (1000/1000 * 1 + 0) * 1 = 1.0 text-space units.
//
// Expected glyph X positions under the FIXED code (advance on LAST rune):
//
//	text[0] 'f': X=0.0  (zero advance — not the last rune of its CID)
//	text[1] 'i': X=0.0  (full advance 1.0 fires HERE, moving Tm to x=1.0)
//	text[2] 'x': X=1.0
//
// The critical assertions:
//
//	text[0].X == text[1].X  — both ligature runes sit at the CID origin
//	text[1].X  < text[2].X  — 'x' starts PAST the ligature, no overlap
//
// Under the OLD buggy code (advance on FIRST rune, `j==0`):
//
//	text[0] 'f': X=0.0  (advance fires here → Tm moves to x=1.0)
//	text[1] 'i': X=1.0  (wrong: trailing rune is displaced to next-CID origin)
//	text[2] 'x': X=1.0  (overlap: 'x' and 'i' share the same X)
//
// Both assertions fail under the old code, proving non-vacuousness.
func TestLayoutCompositeMultiRuneCID(t *testing.T) {
	// Build state: Tfs=1, Th=1, Tm=ident, CTM=ident (contentMakeState sets Th=1, CTM=ident).
	s := contentMakeState()
	s.g.Tfs = 1
	s.g.Tm = ident

	// Build the DW==0 Type0 font and verify preconditions.
	f := buildDWZeroType0Font()
	if w := f.cidWidth(1); w != 1000 {
		t.Fatalf("precondition: cidWidth(1) = %v, want 1000", w)
	}
	if w := f.cidWidth(2); w != 1000 {
		t.Fatalf("precondition: cidWidth(2) = %v, want 1000", w)
	}
	s.g.Tf = f

	// Call layoutComposite with the mock decoder and two-CID input.
	dec := &mockTwoByteDecoder{}
	s.layoutComposite(dec, "\x00\x01\x00\x02")

	// Expect 3 text entries: 'f', 'i', 'x'.
	if len(s.text) != 3 {
		t.Fatalf("len(s.text) = %d, want 3; entries: %v", len(s.text), s.text)
	}

	// Verify rune ordering.
	if s.text[0].S != "f" {
		t.Errorf("text[0].S = %q, want \"f\"", s.text[0].S)
	}
	if s.text[1].S != "i" {
		t.Errorf("text[1].S = %q, want \"i\"", s.text[1].S)
	}
	if s.text[2].S != "x" {
		t.Errorf("text[2].S = %q, want \"x\"", s.text[2].S)
	}

	t.Logf("X positions: 'f'=%.4f  'i'=%.4f  'x'=%.4f",
		s.text[0].X, s.text[1].X, s.text[2].X)

	// --- Key assertions ---

	// Both ligature runes ('f','i') must land at the CID origin (same X).
	// OLD buggy code (advance on j==0): 'f' advances, so text[1].X > text[0].X — FAILS here.
	if s.text[0].X != s.text[1].X {
		t.Errorf("ligature overlap fix: text[0].X (%.4f) != text[1].X (%.4f); "+
			"the two runes of CID 1 must both sit at the CID origin (zero advance on non-last runes)",
			s.text[0].X, s.text[1].X)
	}

	// The next CID ('x') must start strictly after the ligature origin.
	// OLD buggy code: 'x' lands at the same X as 'i' — FAILS here.
	if !(s.text[1].X < s.text[2].X) {
		t.Errorf("next-CID advance fix: text[1].X (%.4f) >= text[2].X (%.4f); "+
			"'x' must start past the ligature (no overlap with its trailing rune)",
			s.text[1].X, s.text[2].X)
	}
}
