// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
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

	enc := font.getEncoder()
	if enc == nil {
		t.Fatal("getEncoder returned nil for unexpected Encoding kind with DebugOn=true")
	}
	// Falls back to pdfDocEncoding — ASCII range is identity.
	got := enc.Decode("OK")
	if got != "OK" {
		t.Errorf("unexpected-kind fallback (DebugOn): Decode(%q) = %q; want %q", "OK", got, "OK")
	}
}
