// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// legacy_krutidev010_composite_test.go — coverage for the composite (Type0) Kruti Dev 010 CID->keystroke
// bridge (legacy_devanagari.go: legacyCIDBridge / legacyCIDKeystrokeBridge / legacyCompositeRemap) and
// its width-tracked content.go integration (layoutCompositeLegacyRun). Mirrors the style of
// legacy_walkman_bridge_test.go: synthetic minimal-PDF builders, table-driven cases, explicit FP-decline
// tests, plus direct unit coverage for the bridge-builder functions and the width/position invariant.
package pdf

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// --- synthetic composite-PDF builders ---------------------------------------------------------------

// buildCompositeKrutiPDF assembles a minimal 1-page PDF whose only font is a Type0/Identity-H composite
// font named baseFont, with a DescendantFonts CIDFontType2 (DW 1000, no /W — width precision is covered
// separately by the direct layoutCompositeLegacyRun unit tests below, not by these PDF round-trips) and,
// when toUnicode != "", a /ToUnicode CMap stream. content is the page content stream; use
// krutiCompositeContentHex to build 2-byte Identity-H hex strings (code == CID).
func buildCompositeKrutiPDF(baseFont, content, toUnicode string) []byte {
	descendant := "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /" + baseFont +
		" /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 1000 >>"
	font := "<< /Type /Font /Subtype /Type0 /BaseFont /" + baseFont +
		" /Encoding /Identity-H /DescendantFonts [6 0 R]"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content)+1, content),
	}
	if toUnicode != "" {
		objs = append(objs, font+" /ToUnicode 7 0 R >>")
	} else {
		objs = append(objs, font+" >>")
	}
	objs = append(objs, descendant)
	if toUnicode != "" {
		objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(toUnicode)+1, toUnicode))
	}

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xrefOff := b.Len()
	n := len(objs) + 1
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", n)
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", n, xrefOff)
	return []byte(b.String())
}

// krutiCompositeToUnicodeRunes builds a /ToUnicode CMap body (2-byte codespace) mapping 2-byte CID i+1
// (1-based, in declaration order) to the given target rune. Direct control over each entry's target —
// a WinAnsi keystroke rune, a real Devanagari rune, or anything else — is what the FP-decline tests need.
func krutiCompositeToUnicodeRunes(targets ...rune) string {
	var b strings.Builder
	b.WriteString("/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n")
	b.WriteString("1 begincodespacerange <0000> <FFFF> endcodespacerange\n")
	fmt.Fprintf(&b, "%d beginbfchar\n", len(targets))
	for i, r := range targets {
		fmt.Fprintf(&b, "<%04X> <%04X>\n", i+1, r)
	}
	b.WriteString("endbfchar\nendcmap CMapName currentdict /CMap defineresource pop end end")
	return b.String()
}

// krutiCompositeToUnicode is krutiCompositeToUnicodeRunes for the recoverable-bridge pattern: CID i+1's
// target is the WinAnsi rune for keystrokes[i] — the breadcrumb legacyCIDKeystrokeBridge recovers.
func krutiCompositeToUnicode(keystrokes ...byte) string {
	targets := make([]rune, len(keystrokes))
	for i, k := range keystrokes {
		targets[i] = winAnsiEncoding[k]
	}
	return krutiCompositeToUnicodeRunes(targets...)
}

// krutiCompositeContentHex returns a Tj hex-string literal ("<...>") encoding cids as consecutive 2-byte
// big-endian Identity-H codes (code == CID).
func krutiCompositeContentHex(cids ...int) string {
	var b strings.Builder
	b.WriteByte('<')
	for _, c := range cids {
		fmt.Fprintf(&b, "%04X", c)
	}
	b.WriteByte('>')
	return b.String()
}

// --- equivalence: decodeKrutiDev010Widths vs decodeKrutiDev010 --------------------------------------

// TestKrutiDev010WidthsEquivalence locks that the width-tracked port's RUNE output (widths ignored)
// matches decodeKrutiDev010's output exactly, across real keystroke strings derived from the
// Rajasthan/HP fixtures (cited in the composite-Kruti feasibility spike): lHkh->सभी,
// vuqlwfpr->अनुसूचित, and the आर्थिक सर्वेक्षण title fragments vkfFkZd / losZ{k.k.
func TestKrutiDev010WidthsEquivalence(t *testing.T) {
	cases := []string{"lHkh", "vuqlwfpr", "vkfFkZd", "losZ{k.k"}
	for _, raw := range cases {
		want := decodeKrutiDev010(raw, &byteEncoder{&winAnsiEncoding})
		got := widthsDecodeASCII(raw)
		if got != want {
			t.Errorf("decodeKrutiDev010Widths(%q) = %q, want %q (decodeKrutiDev010 output)", raw, got, want)
		}
	}
}

// TestKrutiDev010WidthsEquivalenceCodeCoverage extends the equivalence check across the byte-code
// patterns legacy_krutidev010_test.go pins (short-i/IKAR reorder, reph, conjunct stack-glyph expansion,
// nukta, nasal reorder, digits/punctuation, the Latin fallback) — not just printable-ASCII words — so
// the width-tracked port is confirmed aligned on every pass the plain transducer exercises, not just the
// common case.
func TestKrutiDev010WidthsEquivalenceCodeCoverage(t *testing.T) {
	cases := [][]int{
		{102, 100},                                   // कि (IKAR reorder)
		{102, 111, 100, 107},                         // विका (IKAR + plain consonant cluster)
		{100, 101, 90},                               // कर्म (reph)
		{41}, {193}, {61}, {123}, {243}, {233}, {90}, // conjuncts (single-byte stack glyphs)
		{43},                                  // nukta
		{100, 97, 115},                        // कें (nasal reorder, Pass 0)
		{48, 57}, {131}, {65}, {37}, {53, 37}, // digits/punctuation/visarga-colon
		{250}, // Latin fallback (no transducer rule)
	}
	for _, codes := range cases {
		raw := make([]byte, len(codes))
		for i, c := range codes {
			raw[i] = byte(c)
		}
		want := decodeKrutiDev010(string(raw), &byteEncoder{&winAnsiEncoding})
		got := widthsDecodeASCII(string(raw))
		if got != want {
			t.Errorf("decodeKrutiDev010Widths(%v) = %q, want %q", codes, got, want)
		}
	}
}

// widthsDecodeASCII runs raw (each byte its own unit, width 1) through decodeKrutiDev010Widths and
// returns the concatenated rune output, discarding widths — the shape decodeKrutiDev010 itself returns,
// for direct equivalence comparison.
func widthsDecodeASCII(raw string) string {
	units := make([]kdUnit, len(raw))
	for i := 0; i < len(raw); i++ {
		units[i] = kdUnit{code: int(raw[i]), w: 1}
	}
	var sb strings.Builder
	for _, g := range decodeKrutiDev010Widths(units, &byteEncoder{&winAnsiEncoding}) {
		sb.WriteRune(g.r)
	}
	return sb.String()
}

// --- end-to-end synthetic-PDF recovery ----------------------------------------------------------------

// TestCompositeKrutiDev010Recovers exercises the composite bridge end to end: a Type0/Identity-H font
// whose /ToUnicode maps CIDs 1..8 to the WinAnsi keystroke targets for "विका" (102,111,100,107) and
// "सभी" (l,H,k,h = 108,72,107,104) — 8 entries, at legacyCIDMinEntries exactly, all resolving (frac=1.0).
// Also asserts the recovered font emits NO document-scoped WarningLegacyFont (recovery-aware change,
// mirroring TestLegacyWalkmanBridgeRecovers).
func TestCompositeKrutiDev010Recovers(t *testing.T) {
	toUni := krutiCompositeToUnicode(102, 111, 100, 107, 'l', 'H', 'k', 'h')
	content := "BT /F1 12 Tf 72 700 Td " + krutiCompositeContentHex(1, 2, 3, 4) + " Tj " +
		"0 -20 Td " + krutiCompositeContentHex(5, 6, 7, 8) + " Tj ET"
	pdf := buildCompositeKrutiPDF("ABCDEF+KrutiDev010", content, toUni)

	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	if !strings.Contains(got, "विका") {
		t.Errorf("composite Kruti bridge: got %q, want it to contain विका", got)
	}
	if !strings.Contains(got, "सभी") {
		t.Errorf("composite Kruti bridge: got %q, want it to contain सभी", got)
	}
	for _, w := range r.Warnings() {
		if w.Code == WarningLegacyFont {
			t.Errorf("recovered composite Kruti: unexpected document-scoped WarningLegacyFont: %v", r.Warnings())
			break
		}
	}
}

// TestCompositeBridgeDeclinesRealDevanagariToUnicode locks the decode-change FP gate for the composite
// path: a Type0 font whose /ToUnicode ALREADY resolves to real Devanagari (8 consonants, none a WinAnsi
// keystroke target) must NOT be touched — legacyCIDKeystrokeBridge's fraction gate declines (0/8
// resolve), so the font falls through to its own genuine /ToUnicode decode, unchanged.
func TestCompositeBridgeDeclinesRealDevanagariToUnicode(t *testing.T) {
	consonants := []rune{0x0915, 0x0916, 0x0917, 0x0918, 0x0919, 0x091A, 0x091B, 0x091C} // क ख ग घ ङ च छ ज
	toUni := krutiCompositeToUnicodeRunes(consonants...)
	content := "BT /F1 12 Tf 72 700 Td " + krutiCompositeContentHex(1, 2, 3, 4, 5, 6, 7, 8) + " Tj ET"
	pdf := buildCompositeKrutiPDF("ABCDEF+KrutiDev010", content, toUni)

	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	want := string(consonants)
	if !strings.Contains(got, want) {
		t.Errorf("a font already producing Devanagari must decode unchanged: got %q, want it to contain %q", got, want)
	}
}

// TestCompositeBridgeDeclinesNoToUnicode locks that a composite Kruti-named font with NO /ToUnicode at
// all is declined (legacyDevanagariCompositeEncoder's src != encSourceToUnicode gate, before even
// attempting to build a bridge) and still raises the document-scoped warning.
func TestCompositeBridgeDeclinesNoToUnicode(t *testing.T) {
	content := "BT /F1 12 Tf 72 700 Td " + krutiCompositeContentHex(1, 2, 3, 4) + " Tj ET"
	pdf := buildCompositeKrutiPDF("ABCDEF+KrutiDev010", content, "") // no /ToUnicode
	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if _, err := r.Page(1).GetPlainText(nil); err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	hasWarn := false
	for _, w := range r.Warnings() {
		if w.Code == WarningLegacyFont {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("composite Kruti font without /ToUnicode: want WarningLegacyFont, got %v", r.Warnings())
	}
}

// TestCompositeBridgeDeclinesFewEntries locks the legacyCIDMinEntries gate at the PDF-integration level:
// 3 declared bfchar entries, ALL resolving cleanly to WinAnsi keystrokes (fraction 1.0), is still below
// legacyCIDMinEntries (8) and must decline — too few entries to trust the fraction statistically.
func TestCompositeBridgeDeclinesFewEntries(t *testing.T) {
	toUni := krutiCompositeToUnicode(100, 101, 102) // 3 entries, all valid WinAnsi keystrokes
	content := "BT /F1 12 Tf 72 700 Td " + krutiCompositeContentHex(1, 2, 3) + " Tj ET"
	pdf := buildCompositeKrutiPDF("ABCDEF+KrutiDev010", content, toUni)
	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	// Declined: text decodes through the font's own /ToUnicode (the literal keystroke targets, here
	// plain WinAnsi letters), never through the Kruti transducer (which would have produced कें or
	// similar Devanagari for codes 100,101,102 — see TestKrutiDev010NasalReorder/Anchor).
	if strings.ContainsAny(got, "कखगघङचछजटठडढणतथदधनपफबभमयरलवशषसहािीुूेैोौंः") {
		t.Errorf("below-threshold composite font was WRONGLY bridged to Devanagari: got %q", got)
	}
	hasWarn := false
	for _, w := range r.Warnings() {
		if w.Code == WarningLegacyFont {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("below-threshold composite Kruti font: want WarningLegacyFont, got %v", r.Warnings())
	}
}

// --- direct unit coverage for the bridge-builder functions --------------------------------------------

// TestSingleKeystrokeByte locks singleKeystrokeByte's shape check: exactly one printable rune with a
// WinAnsi keystroke byte resolves; everything else (empty, multi-rune, control char, the noRune
// sentinel, a rune with no WinAnsi slot) declines.
func TestSingleKeystrokeByte(t *testing.T) {
	cases := []struct {
		name   string
		runes  []rune
		want   byte
		wantOK bool
	}{
		{"ascii letter", []rune{'k'}, 'k', true},
		{"ascii digit", []rune{'5'}, '5', true},
		{"empty", []rune{}, 0, false},
		{"multi-rune", []rune{'a', 'b'}, 0, false},
		{"control char", []rune{0x09}, 0, false},
		{"noRune sentinel", []rune{noRune}, 0, false},
		{"devanagari has no WinAnsi slot", []rune{0x0915}, 0, false}, // क
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := singleKeystrokeByte(c.runes)
			if ok != c.wantOK || (ok && got != c.want) {
				t.Errorf("singleKeystrokeByte(%v) = (%v, %v), want (%v, %v)", c.runes, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestCmapCodeWidth locks cmapCodeWidth's single-width requirement: exactly one non-empty codespace
// bucket resolves to that width; an empty codespace or a MIXED-width codespace (the failure mode the
// fixed-step bridge cannot walk) both decline to 0.
func TestCmapCodeWidth(t *testing.T) {
	t.Run("single 2-byte bucket", func(t *testing.T) {
		m := &cmap{}
		m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
		if w := cmapCodeWidth(m); w != 2 {
			t.Errorf("cmapCodeWidth = %d, want 2", w)
		}
	})
	t.Run("single 1-byte bucket", func(t *testing.T) {
		m := &cmap{}
		m.space[0] = []byteRange{{"\x00", "\xff"}}
		if w := cmapCodeWidth(m); w != 1 {
			t.Errorf("cmapCodeWidth = %d, want 1", w)
		}
	})
	t.Run("empty codespace", func(t *testing.T) {
		m := &cmap{}
		if w := cmapCodeWidth(m); w != 0 {
			t.Errorf("cmapCodeWidth = %d, want 0", w)
		}
	})
	t.Run("mixed-width codespace declines", func(t *testing.T) {
		m := &cmap{}
		m.space[0] = []byteRange{{"\x00", "\x7f"}}
		m.space[1] = []byteRange{{"\x80\x00", "\xff\xff"}}
		if w := cmapCodeWidth(m); w != 0 {
			t.Errorf("cmapCodeWidth = %d, want 0 (mixed-width codespace must decline)", w)
		}
	})
}

// TestExpandBfrangeKeystrokes locks expandBfrangeKeystrokes' all-or-nothing resolution AND its
// (claimed, resolved) statistics contract (the bfrange CID-counting fix from Codex adversarial review:
// a wide range must count proportionally to its real expanded size, never as a single declared entry —
// see TestLegacyCIDKeystrokeBridgeGates for the end-to-end gate consequence): a clean String-dst range
// expands every code and reports claimed==resolved==span size; an oversized span, a lo/hi length
// mismatch, a non-last-byte span, or ANY single non-keystroke target anywhere in the range (String or
// Array dst) declines the WHOLE range (resolved=0) without partially populating byCID, while claimed
// still reports the real (or best-effort) size so the caller's fraction gate sees accurate evidence.
func TestExpandBfrangeKeystrokes(t *testing.T) {
	t.Run("string-dst range resolves", func(t *testing.T) {
		entry := bfrange{lo: "\x00\x01", hi: "\x00\x03", dst: cmapTestStrVal(runeToUTF16BE('a'))}
		byCID := map[int]byte{}
		claimed, resolved := expandBfrangeKeystrokes(entry, byCID)
		if claimed != 3 || resolved != 3 {
			t.Fatalf("expandBfrangeKeystrokes = (%d, %d), want (3, 3)", claimed, resolved)
		}
		want := map[int]byte{1: 'a', 2: 'b', 3: 'c'}
		if len(byCID) != len(want) {
			t.Fatalf("byCID = %v, want %v", byCID, want)
		}
		for cid, b := range want {
			if byCID[cid] != b {
				t.Errorf("byCID[%d] = %v, want %v", cid, byCID[cid], b)
			}
		}
	})

	t.Run("a maximal single-byte-last-position span (256 codes) resolves and is never an oversized decline", func(t *testing.T) {
		// The lo/hi-must-share-all-but-the-last-byte constraint above caps span+1 at 256 (a single
		// byte's full 0x00-0xFF range) for ANY accepted shape — legacyCIDMaxRangeSpan (256) can
		// therefore never be exceeded by a shape this function actually walks; the ">legacyCIDMaxRangeSpan"
		// check is a defensive cap against a future relaxation of that constraint, not a reachable path
		// today. This case locks the boundary: the largest possible legal range (all 256 codes) still
		// resolves normally, claiming and resolving its real size.
		entry := bfrange{lo: "\x00\x00", hi: "\x00\xff", dst: cmapTestStrVal(runeToUTF16BE(0))}
		byCID := map[int]byte{}
		claimed, resolved := expandBfrangeKeystrokes(entry, byCID)
		// Most of these 256 targets are control codes / non-printable, so most will NOT resolve to a
		// keystroke byte (singleKeystrokeByte rejects rune < 0x20) — the whole range therefore declines
		// (resolved=0, all-or-nothing), but claimed must still report the true 256-code size.
		if claimed != 256 || resolved != 0 {
			t.Errorf("expandBfrangeKeystrokes = (%d, %d), want (256, 0) (the maximal legal span still reports its true claimed size even though most targets aren't printable keystrokes)", claimed, resolved)
		}
		if len(byCID) != 0 {
			t.Error("byCID must stay empty on decline")
		}
	})

	t.Run("lo/hi length mismatch declines as one indeterminate entry", func(t *testing.T) {
		entry := bfrange{lo: "\x00\x01", hi: "\x02", dst: cmapTestStrVal(runeToUTF16BE('a'))}
		byCID := map[int]byte{}
		claimed, resolved := expandBfrangeKeystrokes(entry, byCID)
		if claimed != 1 || resolved != 0 {
			t.Errorf("expandBfrangeKeystrokes = (%d, %d), want (1, 0) (length mismatch: no span to size, counts as one indeterminate entry)", claimed, resolved)
		}
	})

	t.Run("varying a non-last byte declines as one indeterminate entry", func(t *testing.T) {
		entry := bfrange{lo: "\x00\x01", hi: "\x01\x03", dst: cmapTestStrVal(runeToUTF16BE('a'))}
		byCID := map[int]byte{}
		claimed, resolved := expandBfrangeKeystrokes(entry, byCID)
		if claimed != 1 || resolved != 0 {
			t.Errorf("expandBfrangeKeystrokes = (%d, %d), want (1, 0) (non-last-byte span: no span to size, counts as one indeterminate entry)", claimed, resolved)
		}
	})

	t.Run("array-dst range with one non-keystroke target declines the whole range but counts its real size", func(t *testing.T) {
		// CIDs 1,3 -> 'a','c' (valid keystrokes); CID 2 -> क (no WinAnsi slot) invalidates the range.
		dst := filterMakeArray(runeToUTF16BE('a'), runeToUTF16BE(0x0915), runeToUTF16BE('c'))
		entry := bfrange{lo: "\x00\x01", hi: "\x00\x03", dst: dst}
		byCID := map[int]byte{}
		claimed, resolved := expandBfrangeKeystrokes(entry, byCID)
		if claimed != 3 || resolved != 0 {
			t.Errorf("expandBfrangeKeystrokes = (%d, %d), want (3, 0) (one non-keystroke target invalidates the range, but it still claims its real 3-CID size)", claimed, resolved)
		}
		if len(byCID) != 0 {
			t.Error("byCID must stay empty when the range is declined (no partial population)")
		}
	})
}

// TestLegacyCIDKeystrokeBridgeGates exercises legacyCIDKeystrokeBridge directly against hand-built
// *cmap values (cmap_test.go style), pinning the three independent gates: codespace width, minimum
// entry count, and minimum resolved fraction.
func TestLegacyCIDKeystrokeBridgeGates(t *testing.T) {
	bfcharASCII := func(n int) []bfchar {
		out := make([]bfchar, n)
		for i := 0; i < n; i++ {
			out[i] = bfchar{orig: string([]byte{0, byte(i + 1)}), repl: runeToUTF16BE(rune('a' + i))}
		}
		return out
	}

	t.Run("accepts a clean single-width bfchar map", func(t *testing.T) {
		m := &cmap{}
		m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
		m.bfchar = bfcharASCII(8)
		m.buildIndex()
		bridge, ok := legacyCIDKeystrokeBridge(m)
		if !ok {
			t.Fatal("want ok=true")
		}
		if len(bridge.byCID) != 8 || bridge.codeWidth != 2 {
			t.Errorf("bridge = %+v, want 8 entries, codeWidth=2", bridge)
		}
	})

	t.Run("declines below legacyCIDMinEntries even at 100% resolution", func(t *testing.T) {
		m := &cmap{}
		m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
		m.bfchar = bfcharASCII(4) // below legacyCIDMinEntries (8)
		m.buildIndex()
		if _, ok := legacyCIDKeystrokeBridge(m); ok {
			t.Error("want ok=false (too few declared entries)")
		}
	})

	t.Run("declines below legacyCIDMinKeystrokeFrac", func(t *testing.T) {
		m := &cmap{}
		m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
		m.bfchar = bfcharASCII(4) // resolvable WinAnsi keystrokes
		for i := 5; i <= 10; i++ {
			// Real Devanagari targets, NOT keystrokes — 6 more entries, none resolve.
			m.bfchar = append(m.bfchar, bfchar{orig: string([]byte{0, byte(i)}), repl: runeToUTF16BE(rune(0x0900 + i))})
		}
		m.buildIndex()
		// 4 resolved / 10 total = 0.4 < legacyCIDMinKeystrokeFrac (0.9).
		if _, ok := legacyCIDKeystrokeBridge(m); ok {
			t.Error("want ok=false (resolved fraction below threshold)")
		}
	})

	t.Run("declines a mixed-width codespace", func(t *testing.T) {
		m := &cmap{}
		m.space[0] = []byteRange{{"\x00", "\x7f"}}
		m.space[1] = []byteRange{{"\x80\x00", "\xff\xff"}}
		m.bfchar = bfcharASCII(8)
		m.buildIndex()
		if _, ok := legacyCIDKeystrokeBridge(m); ok {
			t.Error("want ok=false (mixed-width codespace)")
		}
	})

	// The next two cases lock the bfrange CID-counting fix (Codex adversarial review): a bfrange entry
	// must count toward total/resolved by its REAL expanded CID span, never as a single declared entry.

	t.Run("a single bfrange spanning legacyCIDMinEntries CIDs satisfies min-entries alone", func(t *testing.T) {
		m := &cmap{}
		m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
		// ONE declared bfrange entry, CIDs 1..8, consecutively resolving to keystrokes 'a'..'h' — the
		// realistic shape a PDF producer emits for a long consecutive CID->ASCII run (range-compressed
		// rather than 8 separate bfchar entries). Pre-fix, this counted as total=1 (the entry, not its
		// span) and was UNCONDITIONALLY declined below legacyCIDMinEntries(8) no matter how many real
		// CIDs it actually covered.
		m.bfrange[1] = []bfrange{{lo: "\x00\x01", hi: "\x00\x08", dst: cmapTestStrVal(runeToUTF16BE('a'))}}
		m.buildIndex()
		bridge, ok := legacyCIDKeystrokeBridge(m)
		if !ok {
			t.Fatal("want ok=true (one bfrange entry claiming 8 real CIDs must satisfy legacyCIDMinEntries on its own)")
		}
		if len(bridge.byCID) != 8 {
			t.Errorf("bridge.byCID has %d entries, want 8", len(bridge.byCID))
		}
	})

	t.Run("declines a font whose bfrange-declared real script text was undercounted pre-fix", func(t *testing.T) {
		m := &cmap{}
		m.space[1] = []byteRange{{"\x00\x00", "\xff\xff"}}
		// 9 bfchar entries, all resolving to keystrokes (CIDs 1..9). PLUS one bfrange entry, CIDs
		// 100..149, all REAL Devanagari (not keystrokes) — a realistic shape for a font whose searchable
		// Devanagari content got range-compressed into one declared bfrange entry by the producer. Total
		// REAL CIDs claimed = 9 + 50 = 59; resolved = 9. Fraction = 9/59 ~ 0.153, well below
		// legacyCIDMinKeystrokeFrac (0.9): must decline, leaving this font's real, already-correct
		// Devanagari CIDs untouched (a bridge accepted by mistake would corrupt them to noRune, since
		// none of those 50 CIDs would have a byCID entry).
		m.bfchar = bfcharASCII(9)
		devanagariTargets := make([]any, 50)
		for i := range devanagariTargets {
			devanagariTargets[i] = runeToUTF16BE(rune(0x0900 + i))
		}
		m.bfrange[1] = []bfrange{{lo: "\x00\x64", hi: "\x00\x95", dst: filterMakeArray(devanagariTargets...)}}
		m.buildIndex()
		if _, ok := legacyCIDKeystrokeBridge(m); ok {
			t.Error("want ok=false (the bfrange's real 50-CID Devanagari span must drag the fraction below threshold, not hide as 1 declared entry)")
		}
	})
}

// --- width/position invariant: the load-bearing risk for this feature ---------------------------------

// buildKrutiWidthFont constructs a minimal Type0 Font (CIDFontType2, /DW 1000) whose /W array (Form 2:
// cFirst cLast w) gives CID i (1-based) the width widths[i-1].
func buildKrutiWidthFont(widths ...int) Font {
	r := fontTestEmptyReader()
	var wArray array
	for i, w := range widths {
		cid := int64(i + 1)
		wArray = append(wArray, cid, cid, int64(w))
	}
	descendant := dict{
		name("Subtype"): name("CIDFontType2"),
		name("DW"):      int64(1000),
		name("W"):       wArray,
	}
	fontDict := dict{
		name("Subtype"):         name("Type0"),
		name("DescendantFonts"): array{descendant},
	}
	return fontTestFontValue(r, fontDict)
}

// TestLayoutCompositeLegacyRunPermutationWidthInvariant covers the common case: a 4-CID run whose
// transducer step is a PURE permutation (विका — the IKAR/consonant reorder from
// TestKrutiDev010ShortIReorder), each CID carrying a distinct real /W width. The run's total advance
// (sum of every emitted glyph's Text.W) must equal the sum of the consumed CIDs' real widths, regardless
// of how the reorder redistributes them glyph-to-glyph — the invariant the whole composite-width design
// depends on.
func TestLayoutCompositeLegacyRunPermutationWidthInvariant(t *testing.T) {
	s := contentMakeState()
	s.g.Tfs = 1
	s.g.Tm = ident
	s.g.Tf = buildKrutiWidthFont(500, 800, 300, 1200) // CIDs 1..4

	bridge := &legacyCIDBridge{byCID: map[int]byte{1: 102, 2: 111, 3: 100, 4: 107}, codeWidth: 2}
	e := &legacyCompositeRemap{bridge: bridge, fallback: &byteEncoder{&winAnsiEncoding}}

	s.layoutCompositeLegacyRun(e, "\x00\x01\x00\x02\x00\x03\x00\x04")

	if len(s.text) != 4 {
		t.Fatalf("len(s.text) = %d, want 4; entries: %+v", len(s.text), s.text)
	}
	var got strings.Builder
	var sumW float64
	for _, tx := range s.text {
		got.WriteString(tx.S)
		sumW += tx.W
	}
	if got.String() != "विका" {
		t.Errorf("recovered run = %q, want विका", got.String())
	}
	const wantSum = (500.0 + 800.0 + 300.0 + 1200.0) / 1000.0
	if math.Abs(sumW-wantSum) > 1e-9 {
		t.Errorf("sum(Text.W) = %v, want %v (run total advance must equal the sum of consumed CID widths)", sumW, wantSum)
	}
	for i := 1; i < len(s.text); i++ {
		if s.text[i].X <= s.text[i-1].X {
			t.Errorf("text[%d].X (%v) <= text[%d].X (%v): a pure permutation (no zero-width siblings) must advance strictly",
				i, s.text[i].X, i-1, s.text[i-1].X)
		}
	}
}

// TestLayoutCompositeLegacyRunSplitNoOverlap covers the harder case: a SPLIT step (one CID's keystroke
// byte 41 expands to the 3-rune conjunct द्ध — TestKrutiDev010Conjuncts) followed by a second, unrelated
// CID. This is the direct "recount doesn't corrupt the running advance" check the task calls out: the
// split runes must all share the CID's origin (zero advance on every rune but the last, mirroring
// layoutComposite's ligature handling — TestLayoutCompositeMultiRuneCID), the NEXT CID must start
// strictly past the whole cluster (no overlap), and the run's total advance must still equal the sum of
// the two CIDs' real widths.
func TestLayoutCompositeLegacyRunSplitNoOverlap(t *testing.T) {
	s := contentMakeState()
	s.g.Tfs = 1
	s.g.Tm = ident
	s.g.Tf = buildKrutiWidthFont(900, 400) // CID1: conjunct (splits to 3 runes); CID2: plain digit

	bridge := &legacyCIDBridge{byCID: map[int]byte{1: 41, 2: 48}, codeWidth: 2}
	e := &legacyCompositeRemap{bridge: bridge, fallback: &byteEncoder{&winAnsiEncoding}}

	s.layoutCompositeLegacyRun(e, "\x00\x01\x00\x02")

	if len(s.text) != 4 {
		t.Fatalf("len(s.text) = %d, want 4 (3-rune conjunct split + 1 plain CID); entries: %+v", len(s.text), s.text)
	}
	if got := s.text[0].S + s.text[1].S + s.text[2].S; got != "द्ध" {
		t.Errorf("CID1 conjunct expansion = %q, want द्ध", got)
	}
	if s.text[3].S != "0" {
		t.Errorf("CID2 = %q, want \"0\"", s.text[3].S)
	}
	if s.text[0].X != s.text[1].X || s.text[1].X != s.text[2].X {
		t.Errorf("split-cluster runes must share one X (zero advance on non-last runes): got %v, %v, %v",
			s.text[0].X, s.text[1].X, s.text[2].X)
	}
	if !(s.text[2].X < s.text[3].X) {
		t.Errorf("next-CID advance: text[2].X (%v) must be < text[3].X (%v) — no overlap with the split cluster",
			s.text[2].X, s.text[3].X)
	}
	var sumW float64
	for _, tx := range s.text {
		sumW += tx.W
	}
	const wantSum = (900.0 + 400.0) / 1000.0
	if math.Abs(sumW-wantSum) > 1e-9 {
		t.Errorf("sum(Text.W) = %v, want %v (a split must not change the run's total advance)", sumW, wantSum)
	}
}
