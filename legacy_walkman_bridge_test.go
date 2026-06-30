// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// buildSubsettedWalkmanPDF assembles a minimal 1-page PDF whose only font is a SIMPLE Type1 font with
// the given BaseFont and an /Encoding dict (/BaseEncoding /WinAnsiEncoding + the given /Differences body,
// e.g. "1 /r /k /f"). When toUnicode is non-empty the font also carries a /ToUnicode CMap — the full
// subsetted-Walkman pattern (encSourceToUnicode). When toUnicode is "" the font has NO /ToUnicode
// (encSourceDict) — used to verify the bridge declines a non-subsetted /Encoding-dict font. content is
// the page content stream; use hex strings <..> for the subset codes.
func buildSubsettedWalkmanPDF(baseFont, differences, content, toUnicode string) []byte {
	font := "<< /Type /Font /Subtype /Type1 /BaseFont /" + baseFont +
		" /Encoding << /Type /Encoding /BaseEncoding /WinAnsiEncoding /Differences [" + differences + "] >>"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content)+1, content),
	}
	if toUnicode != "" {
		objs = append(objs, font+" /ToUnicode 6 0 R >>")
		objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(toUnicode)+1, toUnicode))
	} else {
		objs = append(objs, font+" >>")
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

// TestLegacyWalkmanBridgeRecovers exercises the subsetted-Walkman path end to end: the /Differences maps
// subset codes 1..6 to standard Latin glyph names whose WinAnsi bytes are the canonical w-c-905
// keystrokes r,k,f,y,d,l; the per-object bridge recovers those keystrokes and decodeWalkmanC905
// transduces them. Content emits the subset codes for the legacy strings "rkfydk" (तालिका) and "ldy"
// (सकल). The font also carries a /ToUnicode (so its decode path is encSourceToUnicode) — the regime the
// canonical-coded gate declines, here correctly bridged. Also asserts the recovered font emits NO
// document-scoped WarningLegacyFont (the recovery-aware change).
func TestLegacyWalkmanBridgeRecovers(t *testing.T) {
	diff := "1 /r /k /f /y /d /l" // 1/r 2/k 3/f 4/y 5/d 6/l
	toUni := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange <00> <FF> endcodespacerange\n" +
		"6 beginbfchar <01> <0072> <02> <006B> <03> <0066> <04> <0079> <05> <0064> <06> <006C> endbfchar\n" +
		"endcmap CMapName currentdict /CMap defineresource pop end end"
	// तालिका = legacy "rkfydk" = codes 1 2 3 4 5 2 ; सकल = legacy "ldy" = codes 6 5 4.
	content := "BT /F1 12 Tf 72 700 Td <010203040502> Tj 0 -20 Td <060504> Tj ET"
	pdf := buildSubsettedWalkmanPDF("JNXPQQ+Walkman-Chanakya905Normal", diff, content, toUni)

	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	if !strings.Contains(got, "तालिका") {
		t.Errorf("subsetted Walkman bridge: got %q, want it to contain तालिका", got)
	}
	if !strings.Contains(got, "सकल") {
		t.Errorf("subsetted Walkman bridge: got %q, want it to contain सकल", got)
	}
	for _, w := range r.Warnings() {
		if w.Code == WarningLegacyFont {
			t.Errorf("recovered Walkman: unexpected document-scoped WarningLegacyFont: %v", r.Warnings())
			break
		}
	}
}

// TestLegacyWalkmanBridgeDeclinesWithoutDifferences locks the bridged-path FP gate: a Walkman-named
// simple font that carries a /ToUnicode but NO /Encoding /Differences cannot be bridged, so it is
// DECLINED (stays gibberish) and still raises the document-scoped warning.
func TestLegacyWalkmanBridgeDeclinesWithoutDifferences(t *testing.T) {
	// No /Encoding at all → legacyDifferencesBridge returns ok=false → declined.
	toUni := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange <00> <FF> endcodespacerange\n" +
		"1 beginbfchar <01> <0072> endbfchar\n" +
		"endcmap CMapName currentdict /CMap defineresource pop end end"
	pdf := buildSimpleLegacyPDF("JNXPQQ+Walkman-Chanakya905Normal", "BT /F1 12 Tf 72 700 Td <01> Tj ET", toUni)
	r, err := OpenBytes(pdf)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	if strings.ContainsAny(got, "तालिकासकल") {
		t.Errorf("Walkman without /Differences was WRONGLY bridged to Devanagari: got %q", got)
	}
	hasWarn := false
	for _, w := range r.Warnings() {
		if w.Code == WarningLegacyFont {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("declined Walkman (no /Differences): want WarningLegacyFont, got %v", r.Warnings())
	}
}

// TestLegacyWalkmanBridgeDeclinesNonSubset locks the positive subset-pattern gate: a Walkman simple
// font with a plain /Encoding dict and a fully-resolvable /Differences but NO /ToUnicode (decode path
// encSourceDict — NOT the compact-subset pattern) must NOT be bridged. Only encSourceToUnicode (a
// re-encoded/subsetted font) qualifies for the /Differences bridge.
func TestLegacyWalkmanBridgeDeclinesNonSubset(t *testing.T) {
	diff := "1 /r /k /f /y /d /l"
	content := "BT /F1 12 Tf 72 700 Td <010203040502> Tj ET"
	pdf := buildSubsettedWalkmanPDF("JNXPQQ+Walkman-Chanakya905Normal", diff, content, "") // no /ToUnicode
	got := plainTextOf(t, pdf)
	if strings.ContainsAny(got, "तालिकासकल") {
		t.Errorf("non-subset (encSourceDict) Walkman was WRONGLY bridged to Devanagari: got %q", got)
	}
}

// TestLegacyWalkmanBridgeDeclinesDirtyDifferences locks that a partial/dirty /Differences (one
// unresolvable glyph name) is NOT bridged — a clean Latin-keystroke map is required, so an unproven
// subset code is never fed to the transducer as if it were a canonical keystroke.
func TestLegacyWalkmanBridgeDeclinesDirtyDifferences(t *testing.T) {
	diff := "1 /r /k /notarealglyphname /y /d /l" // code 0x03 names an unresolvable glyph
	toUni := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange <00> <FF> endcodespacerange\n" +
		"1 beginbfchar <01> <0072> endbfchar\n" +
		"endcmap CMapName currentdict /CMap defineresource pop end end"
	content := "BT /F1 12 Tf 72 700 Td <010203040502> Tj ET"
	pdf := buildSubsettedWalkmanPDF("JNXPQQ+Walkman-Chanakya905Normal", diff, content, toUni)
	got := plainTextOf(t, pdf)
	if strings.ContainsAny(got, "तालिकासकल") {
		t.Errorf("dirty /Differences (unresolvable name) was WRONGLY bridged: got %q", got)
	}
}

// TestGetEncoderLegacyRecoveredSuppressesWarning locks the recovery-aware document-scoped warning
// (font.go): a RECOVERED legacy font emits NO WarningLegacyFont, while a strict-legacy font with no
// transducer still does.
func TestGetEncoderLegacyRecoveredSuppressesWarning(t *testing.T) {
	hasLegacyWarn := func(r *Reader) bool {
		for _, w := range r.Warnings() {
			if w.Code == WarningLegacyFont {
				return true
			}
		}
		return false
	}

	// Recovered canonical simple Kruti Dev 010 (encSourceSimple, transducer fires) → NO warning.
	r := fontTestEmptyReader()
	f := fontTestFontValue(r, dict{name("Subtype"): name("Type1"), name("BaseFont"): name("KrutiDev010")})
	if _, src := f.getEncoder(); src != encSourceLegacyRemap {
		t.Fatalf("recovered Kruti: want encSourceLegacyRemap, got %v", src)
	}
	if hasLegacyWarn(r) {
		t.Errorf("recovered Kruti: unexpected WarningLegacyFont in %v", r.Warnings())
	}

	// A strict-legacy font with NO transducer (Kruti-Dev680) STILL warns (unchanged).
	r2 := fontTestEmptyReader()
	f2 := fontTestFontValue(r2, dict{name("Subtype"): name("Type1"), name("BaseFont"): name("ABCDEF+Kruti-Dev680")})
	f2.getEncoder()
	if !hasLegacyWarn(r2) {
		t.Errorf("tableless legacy font: want WarningLegacyFont, got %v", r2.Warnings())
	}
}

// TestHasInvalidHalantMatra locks the mode-B mis-decode detector: a halant (U+094D) immediately
// followed by a matra (U+093E..U+094C) is orthographically impossible, so it is a definite mis-decode;
// a halant followed by a consonant (a valid conjunct) is not.
func TestHasInvalidHalantMatra(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"प्रफ्ेंच", true},   // फ् + े — mis-decoded फ्रेंच (detectable mode-B)
		{"अप्रफ्ीका", true},  // फ् + ी — mis-decoded अफ्रीका
		{"क्ष्ोत्र", true},   // ष् + ो — legacy-transducer residual for क्षेत्र
		{"क्षेत्र", false},   // valid conjunct क्ष + े (halant before a consonant)
		{"राष्ट्रीय", false}, // valid conjunct ष्ट्र
		{"वर्ष", false},      // reph, no matra after the halant
		{"तालिका", false},    // clean
		{"व्रेफ्डिट", false}, // फ् + ड (halant+consonant) — the UNDETECTABLE क्रेडिट mis-decode
	}
	for _, c := range cases {
		if got := hasInvalidHalantMatra(c.s); got != c.want {
			t.Errorf("hasInvalidHalantMatra(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

// TestDetectLegacyMisdecodeFlags locks that a single recovered-but-mis-decoded ligature word (pure
// Devanagari, no Latin) flags the table via the new misdecoded-cluster path, while a cleanly-recovered
// word does not.
func TestDetectLegacyMisdecodeFlags(t *testing.T) {
	cells := []lCell{{x0: 0, x1: 100, top: -20, bottom: 0}}
	const walkman = "JNXPQQ+Walkman-Chanakya905Normal"

	flagged := detectLegacyFontText(cells, []Word{legacyCellWord("प्रफ्ेंच", walkman)})
	if len(flagged) == 0 {
		t.Fatal("mis-decoded ligature word: want a legacy_font_text warning, got none")
	}
	if !strings.Contains(flagged[0].Detail, "misdecoded_clusters=1") {
		t.Errorf("Detail should report the mis-decode count: got %q", flagged[0].Detail)
	}

	if clean := detectLegacyFontText(cells, []Word{legacyCellWord("तालिका", walkman)}); len(clean) != 0 {
		t.Errorf("cleanly-recovered word: want no warning, got %+v", clean)
	}
}
