// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// fontTestFontValue builds a Font whose V is a direct dict Value backed by r.
// All dict values must be direct objects (not objptr references) so that
// resolve() never tries to walk the xref table.
func fontTestFontValue(r *Reader, d dict) Font {
	v := Value{r, objptr{}, d}
	return Font{V: v}
}

// fontTestEmptyReader returns a *Reader backed by an empty byte slice.
// Safe for Values whose dicts contain only direct objects (non-objptr),
// because resolve() only dereferences r when the stored value is an objptr.
func fontTestEmptyReader() *Reader {
	return &Reader{f: bytes.NewReader(nil), end: 0}
}

// fontTestCMapStream wraps raw CMap bytes as a Value of Kind Stream (no filter).
func fontTestCMapStream(data []byte) Value {
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}
	s := stream{
		hdr:    dict{name("Length"): int64(len(data))},
		offset: 0,
	}
	return Value{r, objptr{}, s}
}

// fontTestBuildCMap constructs a minimal well-formed ToUnicode CMap body that
// maps a single 1-byte source code src to the Unicode rune dst.
// Format matches what PDF producers emit and what readCmap expects:
// a codespace range plus a bfchar section.
func fontTestBuildCMap(src byte, dst rune) string {
	return "/CIDInit /ProcSet findresource begin\n" +
		"12 dict begin\n" +
		"begincmap\n" +
		"/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n" +
		"/CMapName /Adobe-Identity-UCS def\n" +
		"/CMapType 2 def\n" +
		"1 begincodespacerange\n" +
		"<" + byteHex(src) + "> <" + byteHex(src) + ">\n" +
		"endcodespacerange\n" +
		"1 beginbfchar\n" +
		"<" + byteHex(src) + "> <" + runeHexBE(dst) + ">\n" +
		"endbfchar\n" +
		"endcmap\n" +
		"CMapToUnicode usecmap\n" +
		"end\n" +
		"end\n"
}

// byteHex formats a byte as a two-character uppercase hex string.
func byteHex(b byte) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[b>>4], digits[b&0xf]})
}

// runeHexBE formats a BMP rune as a 4-character uppercase hex string (UTF-16 BE).
func runeHexBE(r rune) string {
	const digits = "0123456789ABCDEF"
	u := uint16(r)
	return string([]byte{
		digits[u>>12],
		digits[(u>>8)&0xf],
		digits[(u>>4)&0xf],
		digits[u&0xf],
	})
}

// ---- TestFontGetEncoderToUnicode -------------------------------------------

// TestFontGetEncoderToUnicode verifies that when a Font has a valid ToUnicode
// CMap stream, getEncoder returns a cmap-backed encoder that decodes correctly.
func TestFontGetEncoderToUnicode(t *testing.T) {
	// Minimal ToUnicode CMap that maps byte 0x48 ('H') → U+0048 ('H').
	cmapBody := fontTestBuildCMap(0x48, 'H')
	cmapData := []byte(cmapBody)

	toUnicodeStream := fontTestCMapStream(cmapData)

	// The Font's V dict must contain a ToUnicode key whose resolved value is
	// the stream. Because resolve() is called via Key(), and the dict stores
	// the stream value directly (not as an objptr), this works without a real
	// xref table. We reuse the stream's own Reader for the font Value.
	r := toUnicodeStream.r
	fontDict := dict{
		name("ToUnicode"): toUnicodeStream.data,
	}
	font := Font{V: Value{r, objptr{}, fontDict}}

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil; expected a cmap encoder")
	}

	got := enc.Decode("\x48")
	if got != "H" {
		t.Errorf("ToUnicode encoder: Decode(\\x48) = %q; want %q", got, "H")
	}
}

// ---- TestFontGetEncoderDictEncoding ----------------------------------------

// TestFontGetEncoderDictEncoding verifies that when there is no ToUnicode
// stream but the Encoding key holds a Name, getEncoder returns the named
// encoder (e.g. WinAnsiEncoding).
func TestFontGetEncoderDictEncoding(t *testing.T) {
	r := fontTestEmptyReader()
	// Encoding is a Name value (stored as a name type, not string).
	fontDict := dict{
		name("Encoding"): name("WinAnsiEncoding"),
	}
	font := fontTestFontValue(r, fontDict)

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil; expected WinAnsiEncoding encoder")
	}

	// WinAnsiEncoding: 0x41 → 'A', 0x42 → 'B' (standard ASCII range).
	got := enc.Decode("\x41\x42")
	if got != "AB" {
		t.Errorf("WinAnsiEncoding encoder: Decode(%q) = %q; want %q", "\x41\x42", got, "AB")
	}
}

// TestFontGetEncoderDictEncodingDict verifies that when Encoding holds a Dict
// (with a BaseEncoding and optional Differences), getEncoder builds a
// dictEncoder and the resulting decoder works correctly.
func TestFontGetEncoderDictEncodingDict(t *testing.T) {
	r := fontTestEmptyReader()
	// Encoding dict: BaseEncoding = WinAnsiEncoding, no Differences.
	encodingDict := dict{
		name("BaseEncoding"): name("WinAnsiEncoding"),
	}
	fontDict := dict{
		name("Encoding"): encodingDict,
	}
	font := fontTestFontValue(r, fontDict)

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil; expected a dict encoder")
	}

	// WinAnsiEncoding: ASCII range maps to itself.
	got := enc.Decode("AB")
	if got != "AB" {
		t.Errorf("dictEncoder: Decode(%q) = %q; want %q", "AB", got, "AB")
	}
}

// ---- TestFontGetEncoderFallback --------------------------------------------

// TestFontGetEncoderFallback verifies that when neither ToUnicode nor Encoding
// is present, getEncoder falls back to pdfDocEncoding via byteEncoder.
func TestFontGetEncoderFallback(t *testing.T) {
	r := fontTestEmptyReader()
	// Empty font dict — no ToUnicode, no Encoding.
	fontDict := dict{}
	font := fontTestFontValue(r, fontDict)

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil; expected pdfDocEncoding fallback")
	}

	// pdfDocEncoding: standard ASCII bytes map to their Unicode equivalents.
	got := enc.Decode("Hello")
	if got != "Hello" {
		t.Errorf("pdfDocEncoding fallback: Decode(%q) = %q; want %q", "Hello", got, "Hello")
	}
}

// TestFontGetEncoderFallbackUnknownName verifies that an unrecognised Encoding
// name also falls back to pdfDocEncoding (the default branch in
// encoderForCMapName).
func TestFontGetEncoderFallbackUnknownName(t *testing.T) {
	r := fontTestEmptyReader()
	fontDict := dict{
		name("Encoding"): name("UnknownEncodingXYZ"),
	}
	font := fontTestFontValue(r, fontDict)

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil for unknown encoding name")
	}
	// Falls back to pdfDocEncoding — ASCII range is identity.
	got := enc.Decode("test")
	if got != "test" {
		t.Errorf("unknown-name fallback: Decode(%q) = %q; want %q", "test", got, "test")
	}
}

// TestFontGetEncoderToUnicodeFailedParse verifies that when ToUnicode is a
// stream but readCmap returns nil (malformed CMap), getEncoder falls through
// to the Encoding key (here a known Name → WinAnsiEncoding). // error path
func TestFontGetEncoderToUnicodeFailedParse(t *testing.T) {
	// A CMap stream with endbfchar before beginbfchar — sets s.ok=false in
	// handleEndBfchar (s.n < 0), causing readCmap to return nil.
	malformed := []byte(
		"/CIDInit /ProcSet findresource begin\n" +
			"12 dict begin\n" +
			"begincmap\n" +
			"endbfchar\n" + // error path: endbfchar without preceding beginbfchar
			"endcmap\n" +
			"end\nend\n",
	)
	badStream := fontTestCMapStream(malformed)

	r := badStream.r
	fontDict := dict{
		name("ToUnicode"): badStream.data,
		// Encoding is a Name so we can verify we reached the Name branch.
		name("Encoding"): name("WinAnsiEncoding"),
	}
	font := Font{V: Value{r, objptr{}, fontDict}}

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil after failed ToUnicode parse")
	}
	// WinAnsiEncoding: 0x41 → 'A'.
	got := enc.Decode("\x41")
	if got != "A" {
		t.Errorf("after failed ToUnicode: Decode(\\x41) = %q; want %q", got, "A")
	}
}

// TestFontGetEncoderUnexpectedEncodingKind verifies that an Encoding value
// whose Kind is neither Name, Dict, nor Null falls through to pdfDocEncoding.
// This exercises the default branch of the switch in getEncoder. // error path
func TestFontGetEncoderUnexpectedEncodingKind(t *testing.T) {
	r := fontTestEmptyReader()
	// Store an integer as Encoding — Kind == Integer, hits the default branch.
	fontDict := dict{
		name("Encoding"): int64(42),
	}
	font := fontTestFontValue(r, fontDict)

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil for unexpected Encoding kind")
	}
	// Falls back to pdfDocEncoding.
	got := enc.Decode("OK")
	if got != "OK" {
		t.Errorf("unexpected-kind fallback: Decode(%q) = %q; want %q", "OK", got, "OK")
	}
}

// ---- TestFontWidthOutOfRange -----------------------------------------------

// TestFontWidthOutOfRange verifies that Font.Width returns 0 when the
// requested code point is outside [FirstChar, LastChar].
func TestFontWidthOutOfRange(t *testing.T) {
	r := fontTestEmptyReader()
	// FirstChar=65 ('A'), LastChar=67 ('C'), Widths=[500, 600, 700].
	widths := array{int64(500), int64(600), int64(700)}
	fontDict := dict{
		name("FirstChar"): int64(65),
		name("LastChar"):  int64(67),
		name("Widths"):    widths,
	}
	font := fontTestFontValue(r, fontDict)

	tests := []struct {
		code int
		want float64
		desc string
	}{
		{64, 0, "code < FirstChar"}, // error path
		{68, 0, "code > LastChar"},  // error path
		{65, 500, "code == FirstChar (in range)"},
		{66, 600, "code in middle (in range)"},
		{67, 700, "code == LastChar (in range)"},
	}

	for _, tt := range tests {
		got := font.Width(tt.code)
		if got != tt.want {
			t.Errorf("Width(%d) [%s]: got %v, want %v", tt.code, tt.desc, got, tt.want)
		}
	}
}

// TestFontWidthEmpty verifies Width returns 0 when FirstChar == LastChar == 0
// and the Widths array is absent (zero value Font).
func TestFontWidthEmpty(t *testing.T) {
	r := fontTestEmptyReader()
	fontDict := dict{}
	font := fontTestFontValue(r, fontDict)

	// FirstChar() == 0, LastChar() == 0; code=1 is outside range. // error path
	if got := font.Width(1); got != 0 {
		t.Errorf("Width(1) on empty font: got %v, want 0", got)
	}
}

// ---- TestFontEffectiveWidth ------------------------------------------------

// TestFontEffectiveWidthType0NoDW verifies that effectiveWidth returns the
// PDF spec §9.7.4.3 default (1000) for a Type0 font whose descendant CIDFont
// carries no /DW key.
func TestFontEffectiveWidthType0NoDW(t *testing.T) {
	r := fontTestEmptyReader()
	descendant := dict{name("Subtype"): name("CIDFontType2")}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(r, fontDict)
	if got := font.effectiveWidth(0); got != 1000 {
		t.Errorf("effectiveWidth on Type0/no-/DW: got %v, want 1000", got)
	}
}

// TestFontEffectiveWidthType0WithDW verifies that effectiveWidth returns the
// explicit /DW value when present in the descendant CIDFont.
func TestFontEffectiveWidthType0WithDW(t *testing.T) {
	r := fontTestEmptyReader()
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("DW"):      int64(750),
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(r, fontDict)
	if got := font.effectiveWidth(0); got != 750 {
		t.Errorf("effectiveWidth on Type0/DW=750: got %v, want 750", got)
	}
}

// TestFontEffectiveWidthType0NoDescendants verifies that effectiveWidth falls
// back to the spec default (1000) when DescendantFonts is absent.
func TestFontEffectiveWidthType0NoDescendants(t *testing.T) {
	r := fontTestEmptyReader()
	fontDict := dict{name("Subtype"): name("Type0")}
	font := fontTestFontValue(r, fontDict)
	if got := font.effectiveWidth(0); got != 1000 {
		t.Errorf("effectiveWidth on Type0/no-DescendantFonts: got %v, want 1000", got)
	}
}

// TestFontEffectiveWidthSimple verifies that effectiveWidth delegates to Width
// for simple (non-Type0) fonts.
func TestFontEffectiveWidthSimple(t *testing.T) {
	r := fontTestEmptyReader()
	widths := array{int64(500), int64(600)}
	fontDict := dict{
		name("Subtype"):   name("TrueType"),
		name("FirstChar"): int64(65),
		name("LastChar"):  int64(66),
		name("Widths"):    widths,
	}
	font := fontTestFontValue(r, fontDict)
	if got := font.effectiveWidth(65); got != 500 {
		t.Errorf("effectiveWidth on TrueType/code=65: got %v, want 500", got)
	}
	if got := font.effectiveWidth(99); got != 0 {
		t.Errorf("effectiveWidth on TrueType/out-of-range: got %v, want 0", got)
	}
}

// TestFontEffectiveWidthType0WithWAndDW verifies that effectiveWidth returns the
// uniform /DW for all CIDs when /DW is non-zero. effectiveWidth is the uniform-DW
// path used by layoutDecoded; it never consults /W (that is cidWidth's job). Both
// absent (CID 0) and present (CID 32) CIDs must return DW=800, per PDF 32000-1
// §9.7.4.3 (uniform-advance fast path).
func TestFontEffectiveWidthType0WithWAndDW(t *testing.T) {
	r := fontTestEmptyReader()
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("DW"):      int64(800),
		name("W"):       array{int64(32), array{int64(722)}},
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(r, fontDict)
	// CID 0: absent from /W → must return DW=800 (uniform-advance fast path).
	// effectiveWidth never consults /W; /W is cidWidth's responsibility.
	if got := font.effectiveWidth(0); got != 800 {
		t.Errorf("effectiveWidth on Type0/W+DW=800/cid=0 absent from W: got %v, want 800 (uniform DW, /W not consulted)", got)
	}
	// CID 32: present in /W but effectiveWidth is uniform-DW; /W is NOT consulted.
	if got := font.effectiveWidth(32); got != 800 {
		t.Errorf("effectiveWidth on Type0/W+DW=800/cid=32 present in W: got %v, want 800 (uniform DW, /W not consulted)", got)
	}
}

// TestFontEffectiveWidthType0WithWNoDW verifies that effectiveWidth returns the
// spec default 1000 for all CIDs when /DW is absent. effectiveWidth is the
// uniform-DW path (layoutDecoded); absent /DW → 1000, and /W is never consulted
// (that is cidWidth's responsibility). Per PDF 32000-1 §9.7.4.3 the default /DW
// is 1000; the per-CID /W path is only taken by cidWidth for degenerate /DW==0.
func TestFontEffectiveWidthType0WithWNoDW(t *testing.T) {
	r := fontTestEmptyReader()
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("W"):       array{int64(32), array{int64(722)}},
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(r, fontDict)
	// CID 0: absent from /W and no DW → must return spec default 1000.
	// effectiveWidth returns uniform DW; /W is cidWidth's job.
	if got := font.effectiveWidth(0); got != 1000 {
		t.Errorf("effectiveWidth on Type0/W-no-DW/cid=0: got %v, want 1000 (absent DW→spec default; /W not consulted)", got)
	}
	// CID 32: present in /W but effectiveWidth is uniform-DW only; /W is NOT consulted.
	if got := font.effectiveWidth(32); got != 1000 {
		t.Errorf("effectiveWidth on Type0/W-no-DW/cid=32: got %v, want 1000 (absent DW→spec default; /W not consulted)", got)
	}
}

// TestFontCIDWidthDWZero guards the BEA-class fix point: cidWidth is the method
// that corrects the Bold-Cambria stacking bug. A Type0 CIDFont with DW=0
// (degenerate — zero advance stacks every glyph at the same position) must consult
// /W for covered CIDs and return the spec default 1000 for absent CIDs — never 0.
// cidWidth (not effectiveWidth) is the fix point: layoutComposite calls cidWidth so
// that full assembled CIDs are looked up in /W; effectiveWidth is the uniform-DW
// path for layoutDecoded where codes may be partial bytes of a multi-byte CID.
func TestFontCIDWidthDWZero(t *testing.T) {
	r := fontTestEmptyReader()
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("DW"):      int64(0), // degenerate: Bold-Cambria in BEA fixture has DW=0
		name("W"):       array{int64(32), array{int64(722)}},
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(r, fontDict)
	// CID 32: covered by /W → must return the /W width (722), not the degenerate DW=0.
	if got := font.cidWidth(32); got != 722 {
		t.Errorf("cidWidth on Type0/DW=0/cid=32 present in W: got %v, want 722 (/W width used, not degenerate DW=0)", got)
	}
	// CID 0: absent from /W, DW==0 is degenerate → must return spec default 1000, NEVER 0.
	// A zero advance re-stacks glyphs at the same position, scrambling every label —
	// this is exactly the BEA Bold-Cambria label-decode bug this guard fixes.
	if got := font.cidWidth(0); got != 1000 {
		t.Errorf("cidWidth on Type0/DW=0/cid=0 absent from W: got %v, want 1000 (degenerate-DW=0 guard: must not return 0 advance)", got)
	}
}

// TestFontEffectiveWidthType0DWZeroFallsTo1000 locks the no-stacking guard on the
// layoutDecoded path: a DW==0 Type0 font reaches effectiveWidth only when it has no
// code decoder (so layoutComposite/cidWidth is unavailable). effectiveWidth must map
// the degenerate DW=0 to the spec default 1000 — never 0 — so the run does not stack
// at one x. (BEA's DW==0 fonts have a code decoder and go through cidWidth instead;
// this guards the encoder-less DW==0 case, which no corpus fixture exercises.)
func TestFontEffectiveWidthType0DWZeroFallsTo1000(t *testing.T) {
	r := fontTestEmptyReader()
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("DW"):      int64(0),
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(r, fontDict)
	if got := font.effectiveWidth(7); got != 1000 {
		t.Errorf("effectiveWidth on Type0/DW=0: got %v, want 1000 (degenerate DW=0 must not advance 0 / stack)", got)
	}
}

// TestFontCIDWidthNonZeroDW verifies that cidWidth returns the uniform /DW for all
// CIDs when /DW is non-zero — the /W array is NOT consulted. This is the symmetric
// counterpart to TestFontCIDWidthDWZero: only DW==0 triggers the /W lookup.
func TestFontCIDWidthNonZeroDW(t *testing.T) {
	r := fontTestEmptyReader()
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("DW"):      int64(800),
		name("W"):       array{int64(32), array{int64(722)}},
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(r, fontDict)
	// CID 32: present in /W but DW=800 (non-zero) → uniform /DW returned, /W NOT consulted.
	if got := font.cidWidth(32); got != 800 {
		t.Errorf("cidWidth on Type0/DW=800/cid=32: got %v, want 800 (non-zero DW→uniform; /W not consulted)", got)
	}
	// CID 0: absent from /W; non-zero DW → uniform /DW returned.
	if got := font.cidWidth(0); got != 800 {
		t.Errorf("cidWidth on Type0/DW=800/cid=0: got %v, want 800 (uniform DW)", got)
	}
}

// TestBeCID verifies the big-endian CID assembly used by layoutComposite.
func TestBeCID(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"\x00A", 65},
		{"\x01\x02", 258},
		{"\x12\x34", 0x1234},
		{"", 0},
		{"\xff", 255},
	}
	for _, tc := range cases {
		if got := beCID(tc.input); got != tc.want {
			t.Errorf("beCID(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// ---- TestCIDWidthFromW -----------------------------------------------------

// TestCIDWidthFromW exercises cidWidthFromW with all /W entry forms defined in
// PDF 32000-1 §9.7.4.3: Form 1 (c [w...]) and Form 2 (cFirst cLast w), mixed
// sequences, non-array input, empty arrays, and malformed arrays.
func TestCIDWidthFromW(t *testing.T) {
	r := fontTestEmptyReader()

	// Helper: build a Value of kind Array from a flat slice of objects.
	makeArray := func(elems ...object) Value {
		return Value{r, objptr{}, array(elems)}
	}

	tests := []struct {
		name string
		w    Value
		cid  int
		want float64
	}{
		// Form 1: c [w1 w2 w3] — covers c, c+1, c+2
		{"form1 cid=3 hit", makeArray(int64(3), array{int64(250), int64(333), int64(500)}), 3, 250},
		{"form1 cid=4 hit", makeArray(int64(3), array{int64(250), int64(333), int64(500)}), 4, 333},
		{"form1 cid=5 hit", makeArray(int64(3), array{int64(250), int64(333), int64(500)}), 5, 500},
		{"form1 cid=6 miss", makeArray(int64(3), array{int64(250), int64(333), int64(500)}), 6, -1},
		{"form1 cid=2 miss", makeArray(int64(3), array{int64(250), int64(333), int64(500)}), 2, -1},

		// Form 2: cFirst cLast w — all CIDs in [cFirst, cLast] share the same width
		{"form2 cid=10 hit", makeArray(int64(10), int64(20), int64(600)), 10, 600},
		{"form2 cid=15 hit", makeArray(int64(10), int64(20), int64(600)), 15, 600},
		{"form2 cid=20 hit", makeArray(int64(10), int64(20), int64(600)), 20, 600},
		{"form2 cid=9 miss", makeArray(int64(10), int64(20), int64(600)), 9, -1},
		{"form2 cid=21 miss", makeArray(int64(10), int64(20), int64(600)), 21, -1},

		// Mixed: [3 [250 333] 10 20 600]
		{"mixed form1 cid=3", makeArray(int64(3), array{int64(250), int64(333)}, int64(10), int64(20), int64(600)), 3, 250},
		{"mixed form1 cid=4", makeArray(int64(3), array{int64(250), int64(333)}, int64(10), int64(20), int64(600)), 4, 333},
		{"mixed form2 cid=10", makeArray(int64(3), array{int64(250), int64(333)}, int64(10), int64(20), int64(600)), 10, 600},
		{"mixed form2 cid=20", makeArray(int64(3), array{int64(250), int64(333)}, int64(10), int64(20), int64(600)), 20, 600},
		{"mixed cid=5 miss", makeArray(int64(3), array{int64(250), int64(333)}, int64(10), int64(20), int64(600)), 5, -1},

		// Non-array Value (Null kind) → -1
		{"non-array null", Value{}, 3, -1},

		// Empty array → -1
		{"empty array", makeArray(), 0, -1},

		// Malformed: trailing lone integer [3] — bail at truncated Form 1/2 detection
		{"malformed lone int", makeArray(int64(3)), 3, -1},

		// Malformed: [3 [250] 99] — the 99 after the Form-1 entry is an uncovered start;
		// CID 3 is in Form-1, CID 99 is not reachable (truncated). No panic.
		{"malformed trailing int cid=3", makeArray(int64(3), array{int64(250)}, int64(99)), 3, 250},
		{"malformed trailing int cid=99", makeArray(int64(3), array{int64(250)}, int64(99)), 99, -1},

		// Fix 1: truncated Form 2 — [10 20] looks like cFirst=10 cLast=20 but the
		// width element (i+2) is missing. Index(2) returns Null; the kind check
		// must catch this and return -1, not 0 (which for a DW==0 font would bypass
		// the 1000 fallback and re-stack the glyph).
		{"truncated form2 cid=10", makeArray(int64(10), int64(20)), 10, -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cidWidthFromW(tc.w, tc.cid)
			if got != tc.want {
				t.Errorf("cidWidthFromW(cid=%d): got %v, want %v", tc.cid, got, tc.want)
			}
		})
	}
}

// ---- TestFontEncoder (caching) ---------------------------------------------

// TestFontEncoderCaching verifies that repeated Encoder() calls return the
// same (non-nil) result and that the encoding is consistent.
func TestFontEncoderCaching(t *testing.T) {
	r := fontTestEmptyReader()
	fontDict := dict{
		name("Encoding"): name("WinAnsiEncoding"),
	}
	font := fontTestFontValue(r, fontDict)

	enc1 := font.Encoder()
	enc2 := font.Encoder()
	if enc1 == nil || enc2 == nil {
		t.Fatal("Encoder() returned nil")
	}
	// Both calls must decode identically.
	if enc1.Decode("AB") != enc2.Decode("AB") {
		t.Error("Encoder() returned inconsistent encoders across calls")
	}
}

// ---- TestFontBaseFont ------------------------------------------------------

// TestFontBaseFont verifies that BaseFont returns the name stored in the dict.
func TestFontBaseFont(t *testing.T) {
	r := fontTestEmptyReader()
	fontDict := dict{
		name("BaseFont"): name("Helvetica"),
	}
	font := fontTestFontValue(r, fontDict)

	if got := font.BaseFont(); got != "Helvetica" {
		t.Errorf("BaseFont(): got %q, want %q", got, "Helvetica")
	}
}

// TestFontBaseFontMissing verifies that BaseFont returns "" when the key is absent.
func TestFontBaseFontMissing(t *testing.T) {
	r := fontTestEmptyReader()
	font := fontTestFontValue(r, dict{})
	if got := font.BaseFont(); got != "" {
		t.Errorf("BaseFont() on empty dict: got %q, want %q", got, "")
	}
}

// ---- DebugOn coverage tests ------------------------------------------------

// TestFontGetEncoderDebugToUnicodeFailedParse covers the println body at
// font.go line 65: ToUnicode stream present but readCmap returns nil (malformed
// CMap) while DebugOn is true. The fallback Encoding (WinAnsiEncoding) is
// returned and verified.
func TestFontGetEncoderDebugToUnicodeFailedParse(t *testing.T) {
	prev := DebugOn
	DebugOn = true
	defer func() { DebugOn = prev }()

	// Same malformed CMap as TestFontGetEncoderToUnicodeFailedParse:
	// endbfchar before beginbfchar sets s.ok=false → readCmap returns nil.
	malformed := []byte(
		"/CIDInit /ProcSet findresource begin\n" +
			"12 dict begin\n" +
			"begincmap\n" +
			"endbfchar\n" +
			"endcmap\n" +
			"end\nend\n",
	)
	badStream := fontTestCMapStream(malformed)

	r := badStream.r
	fontDict := dict{
		name("ToUnicode"): badStream.data,
		name("Encoding"):  name("WinAnsiEncoding"),
	}
	font := Font{V: Value{r, objptr{}, fontDict}}

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil after failed ToUnicode parse with DebugOn=true")
	}
	// WinAnsiEncoding: 0x41 → 'A'.
	got := enc.Decode("\x41")
	if got != "A" {
		t.Errorf("after failed ToUnicode (DebugOn): Decode(\\x41) = %q; want %q", got, "A")
	}
}

// TestFontGetEncoderDebugUnexpectedKind covers the println body at font.go
// line 78: Encoding value has an unexpected Kind (Integer) while DebugOn is
// true. The pdfDocEncoding fallback is returned and verified.
func TestFontGetEncoderDebugUnexpectedKind(t *testing.T) {
	prev := DebugOn
	DebugOn = true
	defer func() { DebugOn = prev }()

	r := fontTestEmptyReader()
	// Integer Encoding — Kind==Integer hits the default branch.
	fontDict := dict{
		name("Encoding"): int64(42),
	}
	font := fontTestFontValue(r, fontDict)

	enc, _ := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil for unexpected Encoding kind with DebugOn=true")
	}
	// Falls back to pdfDocEncoding — ASCII range is identity.
	got := enc.Decode("OK")
	if got != "OK" {
		t.Errorf("unexpected-kind fallback (DebugOn): Decode(%q) = %q; want %q", "OK", got, "OK")
	}
}

// ---- Identity-H /ToUnicode-as-Name → UCS-2 BE -------------------------------

// TestWantsIdentityUCS2 locks the FP-safety discriminator: ONLY a Type0 font with
// Identity-H|V encoding AND ToUnicode-as-Name Identity-H|V AND an Identity/absent ordering
// dispatches to the UCS-2 BE decoder. The Japan1/GB1/CNS1/Korea1 rows are the load-bearing
// negatives — a genuine CJK CIDFont (Identity-H codes are CIDs, not Unicode) must NOT match.
func TestWantsIdentityUCS2(t *testing.T) {
	cases := []struct {
		name                              string
		subtype, encName, toUni, ordering string
		want                              bool
	}{
		{"positive identity ordering", "Type0", "Identity-H", "Identity-H", "Identity", true},
		{"positive vertical", "Type0", "Identity-V", "Identity-V", "Identity", true},
		{"absent/malformed ordering excluded", "Type0", "Identity-H", "Identity-H", "", false},
		{"CJK Adobe-Japan1 excluded", "Type0", "Identity-H", "Identity-H", "Japan1", false},
		{"CJK Adobe-GB1 excluded", "Type0", "Identity-H", "Identity-H", "GB1", false},
		{"CJK Adobe-CNS1 excluded", "Type0", "Identity-H", "Identity-H", "CNS1", false},
		{"CJK Adobe-Korea1 excluded", "Type0", "Identity-H", "Identity-H", "Korea1", false},
		{"ToUnicode absent (real CJK-no-ToUnicode pattern)", "Type0", "Identity-H", "", "Identity", false},
		{"non-Identity encoding", "Type0", "WinAnsiEncoding", "Identity-H", "Identity", false},
		{"simple font excluded", "TrueType", "Identity-H", "Identity-H", "Identity", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsIdentityUCS2(tc.subtype, tc.encName, tc.toUni, tc.ordering); got != tc.want {
				t.Errorf("wantsIdentityUCS2(%q,%q,%q,%q) = %v, want %v",
					tc.subtype, tc.encName, tc.toUni, tc.ordering, got, tc.want)
			}
		})
	}
}

// identityUCS2FontDict builds a Type0 font dict with the given CIDSystemInfo ordering,
// /Encoding /Identity-H and /ToUnicode <toUni> (a Name; omitted when toUni == ""). Mirrors
// the effectiveWidth-test idiom (direct dicts, empty Reader).
func identityUCS2FontDict(toUni, ordering string) dict {
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("CIDSystemInfo"): dict{
			name("Registry"):   "Adobe",
			name("Ordering"):   ordering,
			name("Supplement"): int64(0),
		},
	}
	d := dict{
		name("Subtype"):         name("Type0"),
		name("Encoding"):        name("Identity-H"),
		name("DescendantFonts"): array{descendant},
	}
	if toUni != "" {
		d[name("ToUnicode")] = name(toUni)
	}
	return d
}

// TestFontGetEncoderIdentityToUnicodeUCS2 verifies the positive path end-to-end through
// getEncoder: an Identity ToUnicode-as-Name Type0 font decodes its 2-byte codes as UCS-2 BE.
func TestFontGetEncoderIdentityToUnicodeUCS2(t *testing.T) {
	font := fontTestFontValue(fontTestEmptyReader(), identityUCS2FontDict("Identity-H", "Identity"))
	enc, src := font.getEncoder()
	if _, ok := enc.(*ucs2BEEncoder); !ok {
		t.Fatalf("getEncoder returned %T, want *ucs2BEEncoder", enc)
	}
	if src != encSourceToUnicode {
		t.Errorf("encSource = %v, want encSourceToUnicode", src)
	}
	if got := enc.Decode("\x00H\x00i"); got != "Hi" {
		t.Errorf("Decode(UCS-2 BE 'Hi') = %q, want %q", got, "Hi")
	}
	// High-byte path (diacritics): U+0141 U+00F3 U+0064 U+017A = "Łódź".
	if got := enc.Decode("\x01\x41\x00\xf3\x00\x64\x01\x7a"); got != "Łódź" {
		t.Errorf("Decode(UCS-2 BE 'Łódź') = %q, want %q", got, "Łódź")
	}
}

// TestFontGetEncoderIdentityCJKOrderingNotUCS2 is the FP-safety check separating the PR #71
// UCS-2 case (Ordering "Identity") from a genuine Adobe-Japan1 CIDFont: a Type0 Identity-H font
// with a ToUnicode-as-Name shape but an Adobe-Japan1 ordering must NOT be UCS-2-decoded. Since the
// Adobe-Japan1 CID→Unicode table shipped, such a font (whose codes are CIDs, and whose Name
// /ToUnicode /Identity-H is not a usable CMap) is now recovered through the CID→Unicode map
// (encSourceCIDMap) instead of being left to garble on the missing-ToUnicode byte path — strictly
// better, and still never the ucs2BEEncoder path.
func TestFontGetEncoderIdentityCJKOrderingNotUCS2(t *testing.T) {
	font := fontTestFontValue(fontTestEmptyReader(), identityUCS2FontDict("Identity-H", "Japan1"))
	enc, src := font.getEncoder()
	if _, ok := enc.(*ucs2BEEncoder); ok {
		t.Fatalf("Adobe-Japan1 CIDFont was UCS-2-decoded; the ordering FP-guard failed")
	}
	if _, ok := enc.(*adobeCIDEncoder); !ok {
		t.Fatalf("getEncoder returned %T, want *adobeCIDEncoder (Adobe-Japan1 CID→Unicode recovery)", enc)
	}
	if src != encSourceCIDMap {
		t.Errorf("encSource = %v, want encSourceCIDMap", src)
	}
}

// TestFontGetEncoderIdentityAbsentOrderingNotUCS2 is the codex-HIGH FP guard: a Type0
// Identity-H font with /ToUnicode /Identity-H (Name) but NO usable CIDSystemInfo /Ordering
// (chained Key/Index collapses missing DescendantFonts / CIDSystemInfo / Ordering to "") must
// NOT be UCS-2-decoded — an incompletely-described CJK CIDFont whose codes are CIDs would
// otherwise be garbled. Requires explicit Ordering == "Identity".
func TestFontGetEncoderIdentityAbsentOrderingNotUCS2(t *testing.T) {
	font := fontTestFontValue(fontTestEmptyReader(), identityUCS2FontDict("Identity-H", ""))
	enc, src := font.getEncoder()
	if _, ok := enc.(*ucs2BEEncoder); ok {
		t.Fatalf("Identity-H with absent /Ordering was UCS-2-decoded; the explicit-Identity gate failed")
	}
	if src != encSourceMissingToUnicode {
		t.Errorf("encSource = %v, want encSourceMissingToUnicode", src)
	}
}

// TestFontGetEncoderIdentityNoToUnicodeNotUCS2 covers the real CJK-without-ToUnicode pattern:
// Identity-H + Identity ordering but NO ToUnicode → must stay on the missing-ToUnicode path
// (the ToUnicode-is-Name signal is the producer's explicit "codes are Unicode" claim).
func TestFontGetEncoderIdentityNoToUnicodeNotUCS2(t *testing.T) {
	font := fontTestFontValue(fontTestEmptyReader(), identityUCS2FontDict("", "Identity"))
	enc, src := font.getEncoder()
	if _, ok := enc.(*ucs2BEEncoder); ok {
		t.Fatalf("Identity-H without ToUnicode was UCS-2-decoded; ToUnicode-Name gate failed")
	}
	if src != encSourceMissingToUnicode {
		t.Errorf("encSource = %v, want encSourceMissingToUnicode", src)
	}
}

// adobeCIDFontDict builds a Type0 font dict with /Encoding <enc>, the given CIDSystemInfo
// ordering, and (optionally) a /ToUnicode Name. No /ToUnicode key when toUni == "".
func adobeCIDFontDict(enc, toUni, ordering string) dict {
	descendant := dict{
		name("Subtype"): name("CIDFontType0"),
		name("CIDSystemInfo"): dict{
			name("Registry"):   "Adobe",
			name("Ordering"):   ordering,
			name("Supplement"): int64(2),
		},
	}
	d := dict{
		name("Subtype"):         name("Type0"),
		name("Encoding"):        name(enc),
		name("DescendantFonts"): array{descendant},
	}
	if toUni != "" {
		d[name("ToUnicode")] = name(toUni)
	}
	return d
}

// TestFontGetEncoderAdobeJapan1CIDMap: Type0 Identity-H, Japan1 ordering, NO ToUnicode →
// dispatches to *adobeCIDEncoder with encSourceCIDMap and decodes a known CID to its Unicode.
func TestFontGetEncoderAdobeJapan1CIDMap(t *testing.T) {
	font := fontTestFontValue(fontTestEmptyReader(), adobeCIDFontDict("Identity-H", "", "Japan1"))
	enc, src := font.getEncoder()
	ce, ok := enc.(*adobeCIDEncoder)
	if !ok {
		t.Fatalf("getEncoder returned %T, want *adobeCIDEncoder", enc)
	}
	if src != encSourceCIDMap {
		t.Errorf("encSource = %v, want encSourceCIDMap", src)
	}
	// CID 2434 (0x0982) → 序 (U+5E8F) in the committed Adobe-Japan1 table.
	if got := ce.Decode("\x09\x82"); got != "序" {
		t.Errorf("Decode(CID 2434) = %q, want %q", got, "序")
	}
}

// TestFontGetEncoderAdobeJapan1IdentityVPreservesVerticalWarning: Identity-V + Japan1 + no
// ToUnicode → CID map AND WarningVerticalWritingMode still fires (the early return must not drop it).
func TestFontGetEncoderAdobeJapan1IdentityVPreservesVerticalWarning(t *testing.T) {
	r := fontTestEmptyReader()
	font := fontTestFontValue(r, adobeCIDFontDict("Identity-V", "", "Japan1"))
	enc, src := font.getEncoder()
	if _, ok := enc.(*adobeCIDEncoder); !ok {
		t.Fatalf("getEncoder returned %T, want *adobeCIDEncoder", enc)
	}
	if src != encSourceCIDMap {
		t.Errorf("encSource = %v, want encSourceCIDMap", src)
	}
	if !hasWarning(r.Warnings(), WarningVerticalWritingMode) {
		t.Errorf("Identity-V matched the CID map but WarningVerticalWritingMode was dropped")
	}
}

// TestFontGetEncoderIdentityOrderingNotCIDMap: Ordering=="Identity" is PR #71's territory — it
// must NOT reach the CID map. With ToUnicode absent it stays on the missing-ToUnicode byte path.
func TestFontGetEncoderIdentityOrderingNotCIDMap(t *testing.T) {
	font := fontTestFontValue(fontTestEmptyReader(), adobeCIDFontDict("Identity-H", "", "Identity"))
	enc, src := font.getEncoder()
	if _, ok := enc.(*adobeCIDEncoder); ok {
		t.Fatalf("Identity ordering reached the CID map; mutual-exclusion with PR #71 failed")
	}
	if src != encSourceMissingToUnicode {
		t.Errorf("encSource = %v, want encSourceMissingToUnicode", src)
	}
}

// TestFontGetEncoderUnsupportedOrderingNotCIDMap: GB1/CNS1/Korea1/absent are out of v1 scope →
// nil table → stay on the existing missing-ToUnicode path.
func TestFontGetEncoderUnsupportedOrderingNotCIDMap(t *testing.T) {
	for _, ord := range []string{"GB1", "CNS1", "Korea1", "" /* absent */} {
		font := fontTestFontValue(fontTestEmptyReader(), adobeCIDFontDict("Identity-H", "", ord))
		enc, src := font.getEncoder()
		if _, ok := enc.(*adobeCIDEncoder); ok {
			t.Fatalf("ordering %q reached the CID map (out of v1 scope)", ord)
		}
		if src != encSourceMissingToUnicode {
			t.Errorf("ordering %q: encSource = %v, want encSourceMissingToUnicode", ord, src)
		}
	}
}

// TestFontGetEncoderJapan1WithToUnicodeStreamWins: a Japan1 font WITH a parseable /ToUnicode
// stream must take the ToUnicode path (encSourceToUnicode), never the CID map.
func TestFontGetEncoderJapan1WithToUnicodeStreamWins(t *testing.T) {
	toUni := fontTestCMapStream([]byte(fontTestBuildCMap(0x48, 'H')))
	descendant := dict{
		name("Subtype"): name("CIDFontType0"),
		name("CIDSystemInfo"): dict{
			name("Registry"): "Adobe", name("Ordering"): "Japan1", name("Supplement"): int64(2),
		},
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("Encoding"):        name("Identity-H"),
		name("DescendantFonts"): array{descendant},
		name("ToUnicode"):       toUni.data,
	}
	font := Font{V: Value{toUni.r, objptr{}, fontDict}}
	enc, src := font.getEncoder()
	if _, ok := enc.(*adobeCIDEncoder); ok {
		t.Fatalf("Japan1 font with a parseable ToUnicode stream reached the CID map; ToUnicode must win")
	}
	if src != encSourceToUnicode {
		t.Errorf("encSource = %v, want encSourceToUnicode", src)
	}
}

// TestFontGetEncoderSimpleFontNotCIDMap: a non-Type0 font early-outs (Type1/TrueType).
func TestFontGetEncoderSimpleFontNotCIDMap(t *testing.T) {
	d := dict{name("Subtype"): name("TrueType"), name("Encoding"): name("Identity-H")}
	font := fontTestFontValue(fontTestEmptyReader(), d)
	if enc, _ := font.getEncoder(); func() bool { _, ok := enc.(*adobeCIDEncoder); return ok }() {
		t.Fatalf("simple font reached the CID map")
	}
}

// TestAdobeJapan1CIDToUnicodeKnownPairs is the CI lock on table correctness: CID→rune pairs whose
// rune is confirmed by pdftotext (an independent engine) on the rendered jo.pdf and whose CID is
// the raw 2-byte stream value — NEITHER side is read from the table under test, so a wrong-column
// or off-by-one generator bug fails here. Stable global Adobe-Japan1 data; runs in CI.
func TestAdobeJapan1CIDToUnicodeKnownPairs(t *testing.T) {
	pairs := []struct {
		cid  uint16
		want rune
	}{
		{2434, '序'}, {920, 'わ'}, {872, 'た'}, {856, 'く'}, {864, 'し'},
		{881, 'と'}, {845, 'い'}, {894, 'ふ'}, {1905, '現'}, {2501, '象'},
		{888, 'は'}, {1824, '景'}, {7923, 'っ'}, {7926, 'ょ'},
		// Radical-skip lock: these CIDs are "radical,ideograph" in cid2code (e.g. CID 1200 =
		// "2f00,4e00"); without the non-radical preference the table would map them to the Kangxi
		// radical (⼀ U+2F00), not the unified ideograph. pdftotext emits the ideographs on
		// jo/nlp2004/kampo (一/日/人 all present), so this stays an independent-engine anchor.
		{1200, '一'}, {3284, '日'}, {2579, '人'},
	}
	for _, p := range pairs {
		got := rune(0)
		if int(p.cid) < len(adobeJapan1CIDToUnicode) {
			got = rune(adobeJapan1CIDToUnicode[p.cid])
		}
		if got != p.want {
			t.Errorf("adobeJapan1CIDToUnicode[%d] = U+%04X, want %q (U+%04X)", p.cid, got, p.want, p.want)
		}
	}
}

// TestFontGetEncoderNonAdobeRegistryNotCIDMap is the FP-safety guard that a custom CID
// collection reusing the ordering name "Japan1" under a non-Adobe /Registry does NOT reach the
// Adobe-Japan1 table — Registry+Ordering together name a collection, so a non-Adobe registry is a
// different (custom) ordering whose CIDs the Adobe table would mis-decode.
func TestFontGetEncoderNonAdobeRegistryNotCIDMap(t *testing.T) {
	descendant := dict{
		name("Subtype"): name("CIDFontType0"),
		name("CIDSystemInfo"): dict{
			name("Registry"):   "Custom",
			name("Ordering"):   "Japan1",
			name("Supplement"): int64(0),
		},
	}
	d := dict{
		name("Subtype"):         name("Type0"),
		name("Encoding"):        name("Identity-H"),
		name("DescendantFonts"): array{descendant},
	}
	font := fontTestFontValue(fontTestEmptyReader(), d)
	enc, src := font.getEncoder()
	if _, ok := enc.(*adobeCIDEncoder); ok {
		t.Fatalf("non-Adobe registry reached the CID map; Registry FP-guard failed")
	}
	if src != encSourceMissingToUnicode {
		t.Errorf("encSource = %v, want encSourceMissingToUnicode", src)
	}
}

// buildMalformedToUnicodePDF returns a minimal 2-page PDF with a Type0
// font whose /ToUnicode stream contains a stray "def" inside a dict literal,
// triggering the parse-panic path on every page. Both pages reference the
// same font so the ToUnicode object is looked up twice (once per
// Page.Content() call), exercising the multipage re-lookup path.
//
// Structure:
//
//	1 0 obj  Catalog → Pages 2 0 R
//	2 0 obj  Pages   → [3 0 R, 8 0 R] /Count 2
//	3 0 obj  Page 1  → Contents 4 0 R, Resources (Font/F1 = 5 0 R)
//	4 0 obj  Content stream: BT /F1 12 Tf <0041> Tj ET  (shared by both pages)
//	5 0 obj  Font (Type0, /Encoding /Identity-H, /ToUnicode 6 0 R,
//	          /DescendantFonts [7 0 R])
//	6 0 obj  ToUnicode stream (malformed: stray "def" inside << >>)
//	7 0 obj  CIDFont (Type2, CIDSystemInfo, /DW 1000)
//	8 0 obj  Page 2  → Contents 4 0 R, Resources (Font/F1 = 5 0 R)
func buildMalformedToUnicodePDF(t *testing.T) []byte {
	t.Helper()

	// The malformed CMap stream. /CIDSystemInfo embeds malformedDictInner
	// (with its enclosing << >>) so the same raw bytes drive the panic
	// in both the direct unit test (TestReadDictMalformedCMapKeyPanics) and here.
	// malformedDictInner already contains the closing >>; readDict sees it
	// after the leading << is consumed by the PS interpreter.
	cmapStream := `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << ` + malformedDictInner + ` def
/CMapName /Adobe-Identity-UCS def
/CMapType 1 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 beginbfchar
<0041> <0041>
endbfchar
endcmap
CMapName currentdict /CMap defineresource pop
end
end`

	// Content stream: set font F1 at 12pt, show 2-byte code 0x0041.
	const contentStream = "BT /F1 12 Tf <0041> Tj ET"

	var buf strings.Builder
	buf.WriteString("%PDF-1.4\n")

	// Track byte offsets for xref. Index = object number (1-based).
	offsets := make([]int, 9) // objects 1..8

	// Object 1: Catalog
	offsets[1] = buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Object 2: Pages — 2 pages
	offsets[2] = buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R 8 0 R] /Count 2 >>\nendobj\n")

	// Object 3: Page 1
	offsets[3] = buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]" +
		" /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	// Object 4: Content stream (shared by both pages)
	offsets[4] = buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n",
		len(contentStream)+1, contentStream)

	// Object 5: Type0 font with /ToUnicode 6 0 R
	offsets[5] = buf.Len()
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type0 /BaseFont /TestFont" +
		" /Encoding /Identity-H /ToUnicode 6 0 R /DescendantFonts [7 0 R] >>\nendobj\n")

	// Object 6: malformed ToUnicode CMap stream
	offsets[6] = buf.Len()
	fmt.Fprintf(&buf, "6 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n",
		len(cmapStream)+1, cmapStream)

	// Object 7: CIDFont descriptor (minimal)
	offsets[7] = buf.Len()
	buf.WriteString("7 0 obj\n<< /Type /Font /Subtype /CIDFontType2 /BaseFont /TestFont" +
		" /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >>" +
		" /DW 1000 >>\nendobj\n")

	// Object 8: Page 2 — same Resources and Contents as Page 1
	offsets[8] = buf.Len()
	buf.WriteString("8 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]" +
		" /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	// xref table
	xrefOff := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 9\n0000000000 65535 f \n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 9 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	return []byte(buf.String())
}

// TestMalformedToUnicodeCMapGracefulDegradation verifies that a /ToUnicode
// CMap stream with a stray "def" inside a dict literal does not panic, and
// that across both pages:
//
//	(a) Page.Content().Text is non-empty — the whole-doc-empty symptom does
//	    not recur, confirming the recover is the load-bearing change.
//	(b) Reader.Warnings() contains WarningMalformedToUnicode — the warning
//	    is emitted, confirming silent swallowing does not occur.
//
// The 2-page fixture also confirms no deadlock on the second lookup of the
// same ToUnicode object (encoderCache multipage re-lookup path).
func TestMalformedToUnicodeCMapGracefulDegradation(t *testing.T) {
	data := buildMalformedToUnicodePDF(t)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}

	// (a) Both pages must return non-empty Content().Text.
	// Page numbers are 1-indexed (Reader.Page(num int) Page, page.go:23).
	for _, pageNum := range []int{1, 2} {
		got := r.Page(pageNum).Content()
		if len(got.Text) == 0 {
			t.Errorf("page %d: Content().Text is empty; expected at least one text element after malformed-ToUnicode recovery", pageNum)
		}
	}

	// (b) WarningMalformedToUnicode must appear in Reader.Warnings().
	var found bool
	for _, w := range r.Warnings() {
		if w.Code == WarningMalformedToUnicode {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("WarningMalformedToUnicode not found in Warnings(); got: %v", r.Warnings())
	}
}
