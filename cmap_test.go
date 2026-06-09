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

// ---------------------------------------------------------------------------
// Helpers — all defined in this file only, no cross-test-file imports.
// ---------------------------------------------------------------------------

// makeCmapStream wraps a raw ToUnicode CMap body as a Value of Kind Stream
// so that readCmap / Interpret can consume it.
func makeCmapStream(body string) Value {
	data := []byte(body)
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}
	s := stream{
		hdr:    dict{name("Length"): int64(len(data))},
		offset: 0,
	}
	return Value{r, objptr{}, s}
}

// cmapTestStrVal returns a Value of Kind String whose RawString() is s.
func cmapTestStrVal(s string) Value {
	return Value{nil, objptr{}, s}
}

// runeToUTF16BE encodes a single Unicode code point as a two-byte big-endian
// UTF-16 string suitable for use as a CMap replacement target.
func runeToUTF16BE(r rune) string {
	return string([]byte{byte(r >> 8), byte(r)})
}

// standardCmapHeader is preamble text shared by cmap stream helpers.
const standardCmapHeader = `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
`

const standardCmapFooter = `endcmap
CMapToUnicode usecmap
end
end
`

// buildBfcharCmap returns a complete ToUnicode CMap that maps a single 1-byte
// source code src to the UTF-16BE destination dst.
func buildBfcharCmap(src byte, dst rune) string {
	return fmt.Sprintf("%s1 begincodespacerange\n<%02X> <%02X>\nendcodespacerange\n1 beginbfchar\n<%02X> <%s>\nendbfchar\n%s",
		standardCmapHeader,
		src, src,
		src, hexBE(dst),
		standardCmapFooter,
	)
}

// buildBfrangeCmap returns a CMap with a single bfrange entry.
func buildBfrangeCmap(lo, hi byte, dstLo rune) string {
	return fmt.Sprintf("%s1 begincodespacerange\n<%02X> <%02X>\nendcodespacerange\n1 beginbfrange\n<%02X> <%02X> <%s>\nendbfrange\n%s",
		standardCmapHeader,
		lo, hi,
		lo, hi, hexBE(dstLo),
		standardCmapFooter,
	)
}

// hexBE formats rune r as a 4-hex-digit big-endian string (no angle brackets).
func hexBE(r rune) string {
	return fmt.Sprintf("%04X", r)
}

// ---------------------------------------------------------------------------
// TestCmapDecodeBfcharRoundtrip — single bfchar entry; Decode returns expected Unicode.
// ---------------------------------------------------------------------------

func TestCmapDecodeBfcharRoundtrip(t *testing.T) {
	// Map byte 0x41 ('A') → U+0041 ('A') via bfchar.
	body := buildBfcharCmap(0x41, 'A')
	m := readCmap(makeCmapStream(body))
	if m == nil {
		t.Fatal("readCmap returned nil for well-formed bfchar stream")
	}
	got := m.Decode("\x41")
	if got != "A" {
		t.Errorf("Decode(\\x41): got %q, want %q", got, "A")
	}
}

// ---------------------------------------------------------------------------
// TestCmapDecodeBfrangeString — string-form bfrange destination path.
// ---------------------------------------------------------------------------

func TestCmapDecodeBfrangeString(t *testing.T) {
	// Range [0x20, 0x22] → U+0020 ('space'), U+0021 ('!'), U+0022 ('"').
	body := buildBfrangeCmap(0x20, 0x22, 0x0020)
	m := readCmap(makeCmapStream(body))
	if m == nil {
		t.Fatal("readCmap returned nil")
	}

	tests := []struct {
		in   string
		want string
	}{
		{"\x20", " "},
		{"\x21", "!"},
		{"\x22", "\""},
	}
	for _, tt := range tests {
		got := m.Decode(tt.in)
		if got != tt.want {
			t.Errorf("Decode(%q): got %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCmapDecodeBfrangeArray — array-form bfrange destination path.
// ---------------------------------------------------------------------------

func TestCmapDecodeBfrangeArray(t *testing.T) {
	// Construct a cmap manually with an array-dst bfrange so we exercise
	// decodeBfrange's array branch directly.
	// bfrange lo="\x30" hi="\x32", dst=array["\x00A", "\x00B", "\x00C"]
	lo := "\x30"
	hi := "\x32"
	// UTF-16BE strings for 'X', 'Y', 'Z'
	dstArr := filterMakeArray(
		runeToUTF16BE('X'),
		runeToUTF16BE('Y'),
		runeToUTF16BE('Z'),
	)
	m := &cmap{}
	m.space[0] = []byteRange{{lo, hi}}
	m.bfrange[0] = []bfrange{{lo: lo, hi: hi, dst: dstArr}}
	m.buildIndex()

	tests := []struct {
		in   string
		want string
	}{
		{"\x30", "X"},
		{"\x31", "Y"},
		{"\x32", "Z"},
	}
	for _, tt := range tests {
		got := m.Decode(tt.in)
		if got != tt.want {
			t.Errorf("Decode(%q) array dst: got %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCmapLongestPrefix — 1-byte and 2-byte codespaces; decodeOne chooses the
// first matching codespace length starting from n=1.
// ---------------------------------------------------------------------------

func TestCmapLongestPrefix(t *testing.T) {
	// Design: use distinct, non-overlapping codespace ranges so that
	// 1-byte input matches only the 1-byte range, and 2-byte input matches
	// only the 2-byte range. This mirrors real PDF CMap practice.
	//
	// 1-byte codespace:  [0x20, 0x7E] → bfchar 0x41 → 'a'
	// 2-byte codespace:  [0x8100, 0x81FF] → bfchar 0x8141 → 'Z'
	m := &cmap{}
	m.space[0] = []byteRange{{"\x20", "\x7e"}}
	m.space[1] = []byteRange{{"\x81\x00", "\x81\xff"}}

	m.bfchar = []bfchar{
		{orig: "\x41", repl: runeToUTF16BE('a')},
		{orig: "\x81\x41", repl: runeToUTF16BE('Z')},
	}
	m.buildIndex()

	// Single-byte input in the 1-byte range should yield 'a'.
	if got := m.Decode("\x41"); got != "a" {
		t.Errorf("1-byte: got %q, want %q", got, "a")
	}
	// Two-byte input in the 2-byte range should yield 'Z'.
	if got := m.Decode("\x81\x41"); got != "Z" {
		t.Errorf("2-byte: got %q, want %q", got, "Z")
	}
}

// ---------------------------------------------------------------------------
// TestCmapLookupBfchar — direct unit calls on lookupBfchar.
// ---------------------------------------------------------------------------

func TestCmapLookupBfchar(t *testing.T) {
	m := &cmap{}
	m.bfchar = []bfchar{
		{orig: "\x41", repl: runeToUTF16BE('A')},
		{orig: "\x42", repl: runeToUTF16BE('B')},
	}
	m.buildIndex()

	t.Run("hit", func(t *testing.T) {
		runes, ok := m.lookupBfchar("\x41")
		if !ok {
			t.Fatal("lookupBfchar: expected hit for \\x41")
		}
		if len(runes) == 0 || runes[0] != 'A' {
			t.Errorf("lookupBfchar: got %v, want ['A']", runes)
		}
	})

	t.Run("miss_wrong_length", func(t *testing.T) {
		// A multi-byte text cannot match a 1-byte key. // error path
		_, ok := m.lookupBfchar("\x41\x42")
		if ok {
			t.Error("lookupBfchar: expected miss for wrong length, got hit")
		}
	})

	t.Run("miss_no_entry", func(t *testing.T) {
		// error path
		_, ok := m.lookupBfchar("\x99")
		if ok {
			t.Error("lookupBfchar: expected miss for unknown byte")
		}
	})
}

// ---------------------------------------------------------------------------
// TestCmapLookupBfrange — direct unit calls on lookupBfrange.
// ---------------------------------------------------------------------------

func TestCmapLookupBfrange(t *testing.T) {
	lo := "\x20"
	hi := "\x7e"
	// Destination is a UTF-16BE string for space (0x0020).
	dst := cmapTestStrVal(runeToUTF16BE(0x0020))
	m := &cmap{}
	m.bfrange[0] = []bfrange{{lo: lo, hi: hi, dst: dst}}
	m.buildIndex()

	t.Run("hit_lo", func(t *testing.T) {
		runes, ok := m.lookupBfrange("\x20", 1)
		if !ok {
			t.Fatal("lookupBfrange: expected hit at lo boundary")
		}
		if len(runes) == 0 || runes[0] != ' ' {
			t.Errorf("lookupBfrange: got %v, want [' ']", runes)
		}
	})

	t.Run("hit_mid", func(t *testing.T) {
		// 0x21 = '!' = 0x0020+1
		runes, ok := m.lookupBfrange("\x21", 1)
		if !ok {
			t.Fatal("lookupBfrange: expected hit for 0x21")
		}
		if len(runes) == 0 || runes[0] != '!' {
			t.Errorf("lookupBfrange: got %v, want ['!']", runes)
		}
	})

	t.Run("miss_below_range", func(t *testing.T) {
		// error path
		_, ok := m.lookupBfrange("\x1f", 1)
		if ok {
			t.Error("lookupBfrange: expected miss below range")
		}
	})

	t.Run("miss_above_range", func(t *testing.T) {
		// error path
		_, ok := m.lookupBfrange("\x7f", 1)
		if ok {
			t.Error("lookupBfrange: expected miss above range")
		}
	})

	t.Run("miss_wrong_length", func(t *testing.T) {
		// error path
		_, ok := m.lookupBfrange("\x20\x00", 2)
		if ok {
			t.Error("lookupBfrange: expected miss for 2-byte when range is 1-byte")
		}
	})
}

// ---------------------------------------------------------------------------
// TestCmapLookupBfrangeMultiEntry — binary search across several ranges in one
// length bucket plus a second bucket. Entries are inserted out of sorted order
// so a passing run proves buildIndex sorts each bucket and the search picks the
// range actually containing text (not merely the first-defined one).
// ---------------------------------------------------------------------------

func TestCmapLookupBfrangeMultiEntry(t *testing.T) {
	m := &cmap{}
	m.bfrange[0] = []bfrange{
		{lo: "\x50", hi: "\x5f", dst: cmapTestStrVal(runeToUTF16BE('0'))},
		{lo: "\x10", hi: "\x1f", dst: cmapTestStrVal(runeToUTF16BE('A'))},
		{lo: "\x30", hi: "\x3f", dst: cmapTestStrVal(runeToUTF16BE('a'))},
	}
	m.bfrange[1] = []bfrange{
		{lo: "\x81\x40", hi: "\x81\x4f", dst: cmapTestStrVal(runeToUTF16BE('Z'))},
	}
	m.buildIndex()

	// Both buckets are disjoint, so they must take the binary-search path.
	if !m.bfrangeSorted[0] || !m.bfrangeSorted[1] {
		t.Fatalf("disjoint buckets should be binary-searchable: sorted=%v", m.bfrangeSorted)
	}

	cases := []struct {
		text string
		n    int
		want rune
		ok   bool
	}{
		{"\x10", 1, 'A', true},     // lo of the first range (by value)
		{"\x15", 1, 'F', true},     // 'A'+5, mid-range offset
		{"\x30", 1, 'a', true},     // lo of the middle range
		{"\x5f", 1, '?', true},     // '0'+15 = 0x3f, hi of the last range
		{"\x81\x40", 2, 'Z', true}, // separate 2-byte bucket
		{"\x0f", 1, 0, false},      // below all ranges
		{"\x20", 1, 0, false},      // gap between ranges
		{"\x60", 1, 0, false},      // above all 1-byte ranges
		{"\x81\x50", 2, 0, false},  // above the 2-byte range
	}
	for _, c := range cases {
		runes, ok := m.lookupBfrange(c.text, c.n)
		if ok != c.ok {
			t.Errorf("lookupBfrange(%q, %d): ok=%v, want %v", c.text, c.n, ok, c.ok)
			continue
		}
		if ok && (len(runes) == 0 || runes[0] != c.want) {
			t.Errorf("lookupBfrange(%q, %d): got %v, want %q", c.text, c.n, runes, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCmapLookupBfrangeOverlap — a directly-constructed overlapping bucket must
// be flagged unsorted and linear-scanned so the first matching slice entry wins,
// exactly as the old flat-slice scan did. A largest-lo binary search would pick
// the wrong entry for codes both cover, and MISS entirely for codes only the
// wider entry covers (silent �). This builds the bucket in [A,B] order directly
// (the parse-path ordering is covered separately in the end-to-end test, where
// the PostScript stack reverses it).
// ---------------------------------------------------------------------------

func TestCmapLookupBfrangeOverlap(t *testing.T) {
	// A is first in the slice and is the wider range [0x10,0x90]→U+10xx; B nests
	// inside it [0x20,0x30]→U+20xx. First-in-slice (A) and largest-lo (B) disagree
	// wherever both cover a code, so A-wins pins the linear first-match behaviour.
	a := bfrange{lo: "\x10", hi: "\x90", dst: cmapTestStrVal("\x10\x00")} // base U+1000
	b := bfrange{lo: "\x20", hi: "\x30", dst: cmapTestStrVal("\x20\x00")} // base U+2000
	m := &cmap{}
	m.bfrange[0] = []bfrange{a, b}
	m.buildIndex()

	if m.bfrangeSorted[0] {
		t.Fatal("buildIndex must flag an overlapping bucket as unsorted (linear scan)")
	}

	cases := []struct {
		text string
		want rune
	}{
		{"\x25", 0x1015}, // in both A and B → first-in-slice A wins, not B's U+2005
		{"\x28", 0x1018}, // in both → A wins
		{"\x50", 0x1040}, // only A covers it → largest-lo would MISS, linear hits A
		{"\x10", 0x1000}, // A's lo boundary
	}
	for _, c := range cases {
		runes, ok := m.lookupBfrange(c.text, 1)
		if !ok {
			t.Errorf("lookupBfrange(%q): expected hit via first-declared range A", c.text)
			continue
		}
		if len(runes) != 1 || runes[0] != c.want {
			t.Errorf("lookupBfrange(%q): got %v, want U+%04X", c.text, runes, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCmapDecodeBfrangeOverlapEndToEnd — overlapping bfrange driven through
// readCmap→Decode, proving the linear fallback is byte-identical to the
// pre-index behaviour on the real parse path. The PostScript operand stack
// reverses order, so handleEndBfrange appends the stream's *second* range first;
// for codes both ranges cover, that range wins. The want values below were
// confirmed against f0727bd (the flat-slice linear scan): U+2005, U+2008, U+1040.
// ---------------------------------------------------------------------------

func TestCmapDecodeBfrangeOverlapEndToEnd(t *testing.T) {
	// 1-byte codespace; stream order: R1 [0x10,0x90]→U+1000, then R2 [0x20,0x30]→
	// U+2000. Stack reversal makes the parsed bucket [R2, R1], so R2 wins overlaps.
	body := standardCmapHeader +
		"1 begincodespacerange\n<00> <FF>\nendcodespacerange\n" +
		"2 beginbfrange\n<10> <90> <1000>\n<20> <30> <2000>\nendbfrange\n" +
		standardCmapFooter
	m := readCmap(makeCmapStream(body))
	if m == nil {
		t.Fatal("readCmap returned nil for overlapping bfrange stream")
	}
	if m.bfrangeSorted[0] {
		t.Fatal("parsed overlapping bucket should be flagged unsorted (linear scan)")
	}
	cases := []struct {
		in   string
		want rune
	}{
		{"\x25", 0x2005}, // both cover → first-in-bucket R2 wins (matches old)
		{"\x28", 0x2008}, // both cover → R2 wins
		{"\x50", 0x1040}, // only R1 covers → largest-lo would miss, linear hits R1
	}
	for _, c := range cases {
		if got := m.Decode(c.in); got != string(c.want) {
			t.Errorf("Decode(%q): got %q (%U), want U+%04X", c.in, got, []rune(got), c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCmapMaxEntries — > 65536 entries truncated/rejected, no panic.
// ---------------------------------------------------------------------------

func TestCmapMaxEntries(t *testing.T) {
	// Build a stream that tries to declare maxCmapEntries+1 bfchar entries.
	// The interpreter should set ok=false and readCmap should return nil.
	var sb strings.Builder
	sb.WriteString(standardCmapHeader)
	sb.WriteString("1 begincodespacerange\n<00> <FF>\nendcodespacerange\n")
	// Announce more entries than allowed.
	fmt.Fprintf(&sb, "%d beginbfchar\n", maxCmapEntries+1)
	sb.WriteString("endbfchar\n")
	sb.WriteString(standardCmapFooter)

	m := readCmap(makeCmapStream(sb.String()))
	// readCmap must return nil (ok=false) — not panic.
	if m != nil {
		t.Error("expected readCmap to return nil for count > maxCmapEntries")
	}
}

// ---------------------------------------------------------------------------
// TestCmapMalformed — missing begincodespacerange; empty body; returns nil/empty, no panic.
// ---------------------------------------------------------------------------

func TestCmapMalformed(t *testing.T) {
	t.Run("missing_begincodespacerange", func(t *testing.T) {
		// endcodespacerange without begincodespacerange → ok=false. // error path
		body := standardCmapHeader +
			"1 endcodespacerange\n" +
			standardCmapFooter
		m := readCmap(makeCmapStream(body))
		if m != nil {
			t.Error("expected nil cmap for missing begincodespacerange")
		}
	})

	t.Run("empty_body", func(t *testing.T) {
		// Empty stream — Interpret sees no operators, ok stays true, m is empty.
		// readCmap may return a non-nil empty cmap; either way must not panic.
		m := readCmap(makeCmapStream(""))
		// We simply verify no panic occurred; m may be nil or empty.
		_ = m
	})

	t.Run("empty_cmap_body", func(t *testing.T) {
		// A stream containing only the cmap header/footer but no codespace/bfchar
		// operators: ok stays true, Decode on an empty cmap returns replacement chars.
		body := standardCmapHeader + standardCmapFooter
		m := readCmap(makeCmapStream(body))
		// m may be non-nil with empty mappings; Decode must not panic.
		if m != nil {
			got := m.Decode("\x41")
			_ = got // replacement char expected; value not asserted
		}
	})
}

// ---------------------------------------------------------------------------
// TestCmapNewDictEncoder — cover newDictEncoder.
// ---------------------------------------------------------------------------

func TestCmapNewDictEncoder(t *testing.T) {
	t.Run("WinAnsiEncoding_no_differences", func(t *testing.T) {
		enc := filterMakeDict(map[string]any{
			"BaseEncoding": name("WinAnsiEncoding"),
		})
		de, _ := newDictEncoder(enc)
		if de == nil {
			t.Fatal("newDictEncoder returned nil")
		}
		// 0x41 in WinAnsiEncoding is 'A' (0x0041).
		got := de.Decode("\x41")
		if got != "A" {
			t.Errorf("WinAnsi Decode(\\x41): got %q, want %q", got, "A")
		}
	})

	t.Run("MacRomanEncoding_no_differences", func(t *testing.T) {
		enc := filterMakeDict(map[string]any{
			"BaseEncoding": name("MacRomanEncoding"),
		})
		de, _ := newDictEncoder(enc)
		if de == nil {
			t.Fatal("newDictEncoder returned nil")
		}
		// 0x41 in MacRomanEncoding is 'A'.
		got := de.Decode("\x41")
		if got != "A" {
			t.Errorf("MacRoman Decode(\\x41): got %q, want %q", got, "A")
		}
	})

	t.Run("default_encoding_no_differences", func(t *testing.T) {
		// No BaseEncoding key → falls back to pdfDocEncoding.
		enc := filterMakeDict(map[string]any{})
		de, _ := newDictEncoder(enc)
		if de == nil {
			t.Fatal("newDictEncoder returned nil")
		}
		// 0x41 in pdfDocEncoding is 'A'.
		got := de.Decode("\x41")
		if got != "A" {
			t.Errorf("pdfDocEncoding Decode(\\x41): got %q, want %q", got, "A")
		}
	})
}

// ---------------------------------------------------------------------------
// TestCmapApplyDifferences — cover applyDifferences.
// ---------------------------------------------------------------------------

func TestCmapApplyDifferences(t *testing.T) {
	t.Run("non_array_is_nop", func(t *testing.T) {
		// Passing a null Value should be a no-op. // error path (wrong kind)
		var table [256]rune
		copy(table[:], winAnsiEncoding[:])
		applyDifferences(&table, Value{})
		// Table unchanged: 0x41 should still be 'A'.
		if table[0x41] != 'A' {
			t.Errorf("applyDifferences with null diff mutated table unexpectedly")
		}
	})

	t.Run("remap_entry", func(t *testing.T) {
		// Differences array: [65 /sterling] maps code 65 (0x41) to sterling (U+00A3).
		// Build as an array Value: [int64(65), name("sterling")]
		r := &Reader{f: bytes.NewReader(nil), end: 0}
		diffArr := Value{r, objptr{}, array{int64(65), name("sterling")}}

		var table [256]rune
		copy(table[:], winAnsiEncoding[:])
		applyDifferences(&table, diffArr)

		if table[65] != 0x00A3 {
			t.Errorf("applyDifferences: table[65] = %U, want U+00A3", table[65])
		}
		// Code 66 (0x42, 'B') should be unchanged.
		if table[66] != 'B' {
			t.Errorf("applyDifferences: table[66] = %U, want U+0042", table[66])
		}
	})

	t.Run("consecutive_remapping", func(t *testing.T) {
		// Differences: [32 /space /exclam] → code 32 → space, code 33 → exclam.
		r := &Reader{f: bytes.NewReader(nil), end: 0}
		diffArr := Value{r, objptr{}, array{int64(32), name("space"), name("exclam")}}

		var table [256]rune
		copy(table[:], pdfDocEncoding[:])
		applyDifferences(&table, diffArr)

		if table[32] != ' ' {
			t.Errorf("applyDifferences consecutive: table[32] = %U, want U+0020", table[32])
		}
		if table[33] != '!' {
			t.Errorf("applyDifferences consecutive: table[33] = %U, want U+0021", table[33])
		}
	})

	t.Run("unknown_glyph_name_no_change", func(t *testing.T) {
		// An unknown glyph name should leave the entry unchanged (nameToRune returns 0).
		r := &Reader{f: bytes.NewReader(nil), end: 0}
		diffArr := Value{r, objptr{}, array{int64(0x41), name("__unknown_glyph_xyz__")}}

		var table [256]rune
		copy(table[:], winAnsiEncoding[:])
		orig := table[0x41]
		applyDifferences(&table, diffArr)
		if table[0x41] != orig {
			t.Errorf("applyDifferences: unknown glyph should not change table entry")
		}
	})

	t.Run("newDictEncoder_with_differences", func(t *testing.T) {
		// Wire applyDifferences through newDictEncoder: remap 0x42 ('B') to sterling.
		r := &Reader{f: bytes.NewReader(nil), end: 0}
		diffArrData := array{int64(0x42), name("sterling")}
		enc := Value{r, objptr{}, dict{
			name("BaseEncoding"): name("WinAnsiEncoding"),
			name("Differences"):  diffArrData,
		}}
		de, _ := newDictEncoder(enc)
		if de == nil {
			t.Fatal("newDictEncoder returned nil")
		}
		got := de.Decode("\x42")
		if got != "£" {
			t.Errorf("Decode(\\x42) after Differences: got %q, want %q", got, "£")
		}
	})
}

// ---------------------------------------------------------------------------
// FuzzCmapDecode — seed with representative bfchar/bfrange stream snippets.
// No recover() wrapper: let Go's fuzz engine detect panics natively.
// ---------------------------------------------------------------------------

func FuzzCmapDecode(f *testing.F) {
	// Seed: simple bfchar mapping.
	f.Add([]byte(buildBfcharCmap(0x41, 'A')))

	// Seed: bfrange mapping.
	f.Add([]byte(buildBfrangeCmap(0x20, 0x7e, 0x0020)))

	// Seed: empty stream.
	f.Add([]byte(""))

	// Seed: well-formed but unusual — two codespace ranges, bfchar + bfrange.
	f.Add([]byte(standardCmapHeader +
		"2 begincodespacerange\n<00> <7F>\n<80> <FF>\nendcodespacerange\n" +
		"1 beginbfchar\n<41> <0041>\nendbfchar\n" +
		"1 beginbfrange\n<20> <22> <0020>\nendbfrange\n" +
		standardCmapFooter))

	// Seed: truncated / malformed.
	f.Add([]byte("/CIDInit /ProcSet findresource begin\nbegincmap\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// readCmap runs Interpret, which drives readToken/readObject below any
		// recover boundary and panics-as-error on a lexically malformed stream;
		// only a runtime fault is a real bug here.
		defer recoverIntentionalParserPanic(t)
		m := readCmap(makeCmapStream(string(data)))
		if m != nil {
			// Exercise Decode on a non-trivial input string.
			m.Decode("Hello\x00\xff")
		}
	})
}

// ---------------------------------------------------------------------------
// BenchmarkCmapDecode — S2 benchmark: 1000-rune loop over a realistic ToUnicode stream.
// ---------------------------------------------------------------------------

func BenchmarkCmapDecode(b *testing.B) {
	// Build a ToUnicode stream mapping ASCII printable range 0x20-0x7E.
	body := buildBfrangeCmap(0x20, 0x7e, 0x0020)
	m := readCmap(makeCmapStream(body))
	if m == nil {
		b.Fatal("readCmap returned nil for benchmark stream")
	}

	// Construct a 1000-byte input cycling through the mapped range.
	input := make([]byte, 1000)
	for i := range input {
		input[i] = byte(0x20 + (i % (0x7e - 0x20 + 1)))
	}
	raw := string(input)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Decode(raw)
	}
}

// ---------------------------------------------------------------------------
// TestCmapDecodeBfrangeArrayNonStringDst — Array dst whose indexed element is
// NOT a String (name value); exercises lines 122-124 and 130.
// ---------------------------------------------------------------------------

func TestCmapDecodeBfrangeArrayNonStringDst(t *testing.T) {
	prev := DebugOn
	DebugOn = true
	defer func() { DebugOn = prev }()

	// Array containing a name element (not a String) at index 0.
	dstArr := filterMakeArray(name("NotAString"))
	entry := bfrange{lo: "\x30", hi: "\x32", dst: dstArr}

	result, ok := decodeBfrange(entry, "\x30") // offset 0 -> Index(0) = name element
	if !ok {
		t.Errorf("decodeBfrange: expected ok=true, got false")
	}
	if len(result) != 1 || result[0] != noRune {
		t.Errorf("decodeBfrange array non-string dst: got %v, want [noRune]", result)
	}
}

// ---------------------------------------------------------------------------
// TestCmapDecodeBfrangeUnknownDst — dst Kind is neither String nor Array
// (integer Value); exercises lines 125-128 and 130.
// ---------------------------------------------------------------------------

func TestCmapDecodeBfrangeUnknownDst(t *testing.T) {
	prev := DebugOn
	DebugOn = true
	defer func() { DebugOn = prev }()

	// Integer dst — neither String nor Array.
	intDst := Value{nil, objptr{}, int64(99)}
	entry := bfrange{lo: "\x30", hi: "\x32", dst: intDst}

	result, ok := decodeBfrange(entry, "\x30")
	if !ok {
		t.Errorf("decodeBfrange: expected ok=true, got false")
	}
	if len(result) != 1 || result[0] != noRune {
		t.Errorf("decodeBfrange unknown dst: got %v, want [noRune]", result)
	}
}

// ---------------------------------------------------------------------------
// TestCmapHandleEndCodespaceMissingBeginDebug — s.n < 0 with DebugOn; exercises
// lines 181-183 (DebugOn println body) and sets s.ok = false.
// ---------------------------------------------------------------------------

func TestCmapHandleEndCodespaceMissingBeginDebug(t *testing.T) {
	prev := DebugOn
	DebugOn = true
	defer func() { DebugOn = prev }()

	s := &cmapInterp{n: -1, ok: true}
	var stk Stack
	s.handleEndCodespace(&stk)

	if s.ok {
		t.Errorf("handleEndCodespace with n<0: expected ok=false, got true")
	}
}

// ---------------------------------------------------------------------------
// TestCmapHandleEndCodespaceBadRange — mismatched lo/hi lengths; exercises
// lines 189-194 (bad-range true branch + DebugOn body + s.ok=false + return).
// ---------------------------------------------------------------------------

func TestCmapHandleEndCodespaceBadRange(t *testing.T) {
	prev := DebugOn
	DebugOn = true
	defer func() { DebugOn = prev }()

	s := &cmapInterp{n: 1, ok: true}
	var stk Stack
	// handleEndCodespace pops hi first, then lo: hi, lo := stk.Pop(), stk.Pop()
	// Push lo first (bottom), then hi (top).
	stk.Push(Value{nil, objptr{}, "\x00"})     // lo: 1 byte
	stk.Push(Value{nil, objptr{}, "\x00\x00"}) // hi: 2 bytes (mismatched)
	s.handleEndCodespace(&stk)

	if s.ok {
		t.Errorf("handleEndCodespace bad range: expected ok=false, got true")
	}
}

// ---------------------------------------------------------------------------
// TestCmapInterpretCmapDefineresource — exercises lines 265-269 (all 4
// statements in the defineresource case body).
// ---------------------------------------------------------------------------

func TestCmapInterpretCmapDefineresource(t *testing.T) {
	s := &cmapInterp{n: -1, ok: true}
	var stk Stack

	// defineresource pops (top->bottom): category_name, value, resource_name
	// Push resource_name first (bottom), then value, then category_name (top).
	stk.Push(Value{nil, objptr{}, name("Identity")}) // resource name (popped last)
	stk.Push(newDict())                              // the value (popped second)
	stk.Push(Value{nil, objptr{}, name("CMap")})     // category name (popped first)

	s.interpretCmap(&stk, "defineresource")

	if !s.ok {
		t.Errorf("interpretCmap defineresource: s.ok became false unexpectedly")
	}
	if stk.Len() != 1 {
		t.Errorf("interpretCmap defineresource: stk.Len()=%d, want 1", stk.Len())
	}
}
