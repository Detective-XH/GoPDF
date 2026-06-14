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
	if got := (&simpleCmapEncoder{m}).Decode("\x30\x31\x32"); got != "012" {
		t.Errorf("simpleCmapEncoder.Decode(<30 31 32>) = %q, want %q", got, "012")
	}
}

// TestSimpleCmapEncoderBfrange covers the bfrange branch of the 1-byte decode.
func TestSimpleCmapEncoderBfrange(t *testing.T) {
	m := &cmap{}
	m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
	m.bfrange[0] = []bfrange{{lo: "\x30", hi: "\x39", dst: cmapTestStrVal(runeToUTF16BE('0'))}}
	m.buildIndex()
	if got := (&simpleCmapEncoder{m}).Decode("\x30\x35\x39"); got != "059" {
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
	wrapped := (&simpleCmapEncoder{m}).Decode("\x41")
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
	if got := (&simpleCmapEncoder{m}).Decode("\x30\x99"); got != "0�" {
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
