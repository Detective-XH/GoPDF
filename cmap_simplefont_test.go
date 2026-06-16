// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import "testing"

// TestSimpleCmapEncoderTwoByteCodespace locks the real-world ToUnicode defect the
// simpleCmapEncoder fixes: an Adobe-emitted ToUnicode CMap for a SIMPLE (single-byte)
// font declares a 2-byte codespacerange (<0000> <FFFF>) while its bfchar keys — and
// the font's actual content codes — are 1-byte. The generic width-driven cmap.Decode
// reads the 1-byte codes two-at-a-time, misses every 1-byte bfchar, and yields U+FFFD;
// simpleCmapEncoder decodes one byte per code and recovers the text.
func TestSimpleCmapEncoderTwoByteCodespace(t *testing.T) {
	m := &cmap{}
	m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}} // 2-byte codespace (the Adobe quirk)
	m.bfchar = []bfchar{
		{orig: "\x30", repl: runeToUTF16BE('0')},
		{orig: "\x31", repl: runeToUTF16BE('1')},
		{orig: "\x32", repl: runeToUTF16BE('2')},
	}
	m.buildIndex()

	// Document the bug: generic Decode mis-chunks by the 2-byte codespace.
	if buggy := m.Decode("\x30\x31\x32"); buggy == "012" {
		t.Fatalf("generic cmap.Decode unexpectedly already correct (%q) — test no longer exercises the fix", buggy)
	} else {
		t.Logf("generic cmap.Decode(<30 31 32>) = %q (the mis-chunk the wrapper fixes)", buggy)
	}
	// The fix: decode each 1-byte code.
	if got := (&simpleCmapEncoder{m: m}).Decode("\x30\x31\x32"); got != "012" {
		t.Errorf("simpleCmapEncoder.Decode(<30 31 32>) = %q, want %q", got, "012")
	}
}

// TestSimpleCmapEncoderBfrange covers the bfrange branch of the 1-byte decode.
func TestSimpleCmapEncoderBfrange(t *testing.T) {
	m := &cmap{}
	m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
	m.bfrange[0] = []bfrange{{lo: "\x30", hi: "\x39", dst: cmapTestStrVal(runeToUTF16BE('0'))}}
	m.buildIndex()
	if got := (&simpleCmapEncoder{m: m}).Decode("\x30\x35\x39"); got != "059" {
		t.Errorf("bfrange decode = %q, want %q", got, "059")
	}
}

// TestSimpleCmapEncoderWellFormedUnchanged proves the fix is byte-identical to the
// generic path for a well-formed 1-byte-codespace simple-font ToUnicode — no
// regression for CMaps that were already correct.
func TestSimpleCmapEncoderWellFormedUnchanged(t *testing.T) {
	m := readCmap(makeCmapStream(buildBfcharCmap(0x41, 'A')))
	if m == nil {
		t.Fatal("readCmap returned nil")
	}
	generic := m.Decode("\x41")
	wrapped := (&simpleCmapEncoder{m: m}).Decode("\x41")
	if generic != "A" || wrapped != "A" {
		t.Errorf("well-formed 1-byte codespace: generic=%q wrapped=%q, want both %q", generic, wrapped, "A")
	}
}

// TestSimpleCmapEncoderUnmappedByte confirms a byte absent from bfchar/bfrange still
// decodes to U+FFFD — the wrapper widens code chunking, not the mapping coverage.
func TestSimpleCmapEncoderUnmappedByte(t *testing.T) {
	m := &cmap{}
	m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
	m.bfchar = []bfchar{{orig: "\x30", repl: runeToUTF16BE('0')}}
	m.buildIndex()
	if got := (&simpleCmapEncoder{m: m}).Decode("\x30\x99"); got != "0�" {
		t.Errorf("unmapped byte decode = %q, want %q", got, "0�")
	}
}

// TestIsSimpleFontSubtype locks the simple-vs-composite classification that gates
// the 1-byte ToUnicode decode (composite Type0 keeps the codespace-driven path).
func TestIsSimpleFontSubtype(t *testing.T) {
	for _, st := range []string{"Type1", "TrueType", "Type3", "MMType1"} {
		if !isSimpleFontSubtype(st) {
			t.Errorf("isSimpleFontSubtype(%q) = false, want true", st)
		}
	}
	for _, st := range []string{"Type0", "", "CIDFontType2", "CIDFontType0"} {
		if isSimpleFontSubtype(st) {
			t.Errorf("isSimpleFontSubtype(%q) = true, want false", st)
		}
	}
}

// TestSimpleCmapEncoderPoisonFallback locks the four branches of the poison fallback:
// poison hit -> /Encoding char; miss -> noRune (NOT rescued); poison whose /Encoding is
// also undefined -> noRune; nil fallback -> prior behavior (poison stays U+FFFD).
func TestSimpleCmapEncoderPoisonFallback(t *testing.T) {
	m := &cmap{}
	m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
	m.bfchar = []bfchar{
		{orig: "\x34", repl: runeToUTF16BE('4')}, // real
		{orig: "\x2e", repl: runeToUTF16BE('�')}, // POISON (period)
		{orig: "\x30", repl: runeToUTF16BE('0')}, // real
		{orig: "\x81", repl: runeToUTF16BE('�')}, // POISON; WinAnsi 0x81 is undefined
	}
	m.buildIndex()
	fb := &byteEncoder{&winAnsiEncoding} // WinAnsi: 0x2E='.', 0x81=noRune, 0x99='™'
	enc := &simpleCmapEncoder{m: m, fallback: fb}

	if got := enc.Decode("\x34\x2e\x30"); got != "4.0" {
		t.Errorf("poison fallback: Decode(<34 2e 30>) = %q, want %q", got, "4.0")
	}
	if got := enc.Decode("\x99"); got != "�" { // 0x99 absent (MISS) — must NOT fall back to '™'
		t.Errorf("miss must stay U+FFFD, got %q", got)
	}
	if got := enc.Decode("\x81"); got != "�" { // POISON but /Encoding also undefined
		t.Errorf("poison with undefined /Encoding must stay U+FFFD, got %q", got)
	}
	if got := (&simpleCmapEncoder{m: m}).Decode("\x2e"); got != "�" { // nil fallback = prior behavior
		t.Errorf("nil fallback: poison must stay U+FFFD, got %q", got)
	}
}

// TestTrustedBaseByteEncoding locks the no-fabrication gate: only an explicit base-encoding
// name (WinAnsi/MacRoman/Standard) is a confident byte fallback for a poisoned ToUnicode code.
// An unknown or Identity-style name returns nil so encodingByteFallback yields nil and the
// poisoned code stays U+FFFD rather than being fabricated from a PDFDocEncoding guess. Combined
// with the nil-fallback branch of TestSimpleCmapEncoderPoisonFallback, this end-to-end proves
// an unknown-named /Encoding plus a poisoned hit remains U+FFFD.
func TestTrustedBaseByteEncoding(t *testing.T) {
	for _, n := range []string{"WinAnsiEncoding", "MacRomanEncoding", "StandardEncoding"} {
		if trustedBaseByteEncoding(n) == nil {
			t.Errorf("trustedBaseByteEncoding(%q) = nil, want a byte encoder", n)
		}
	}
	for _, n := range []string{"CustomFoo", "Identity-H", "Identity-V", "UniGB-UCS2-H", "90ms-RKSJ-H", ""} {
		if trustedBaseByteEncoding(n) != nil {
			t.Errorf("trustedBaseByteEncoding(%q) = non-nil, want nil (unknown/unsupported name must not fabricate)", n)
		}
	}
}

// TestFallbackBaseTable locks the dict-path half of the no-fabrication invariant (the symmetric
// guard to TestTrustedBaseByteEncoding): the poison-fallback base table is PDFDocEncoding for an
// absent /BaseEncoding (GoPDF's documented default — 0x2E -> '.'), the trusted table for a known
// base name, and all-U+FFFD for an unknown base name so a poisoned code not covered by
// /Differences stays U+FFFD instead of being fabricated from a PDFDocEncoding guess.
func TestFallbackBaseTable(t *testing.T) {
	if got := fallbackBaseTable(Value{}); got[0x2e] != '.' { // absent base -> PDFDocEncoding
		t.Errorf("absent base: table[0x2E] = %q, want '.'", got[0x2e])
	}
	if got := fallbackBaseTable(Value{data: name("WinAnsiEncoding")}); got[0x2e] != '.' { // trusted name
		t.Errorf("WinAnsi base: table[0x2E] = %q, want '.'", got[0x2e])
	}
	if got := fallbackBaseTable(Value{data: name("CustomFoo")}); got[0x2e] != noRune { // unknown name
		t.Errorf("unknown base: table[0x2E] = %q, want U+FFFD (no fabrication)", got[0x2e])
	}
	if got := fallbackBaseTable(Value{data: name("Identity-H")}); got[0x41] != noRune { // Identity excluded
		t.Errorf("Identity-H base: table[0x41] = %q, want U+FFFD (no fabrication)", got[0x41])
	}
	if got := fallbackBaseTable(Value{data: int64(5)}); got[0x2e] != noRune { // malformed (non-Name) base != absent
		t.Errorf("malformed base: table[0x2E] = %q, want U+FFFD (malformed is not absent)", got[0x2e])
	}
}
