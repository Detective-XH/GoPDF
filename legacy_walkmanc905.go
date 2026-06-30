// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Walkman-Chanakya 905 legacy-font → Unicode transducer.
// The byte-class definitions and the three-pass mapping/reorder rules are a
// pure-Go port of the TECkit mapping W-C-905.map distributed by SIL International
// in the silnrsi/wsresources repository. Only the character-equivalence DATA
// (byte codes, Unicode code points, and the documented reorder passes) is ported;
// no TECkit source code or runtime is included, and ZERO_WIDTH_JOINER/NON_JOINER
// are intentionally omitted for searchable text. Attribution: see NOTICE.

package pdf

import "strings"

// decodeWalkmanC905 converts a string of Walkman-Chanakya 905 legacy byte codes to
// searchable Unicode Devanagari. It faithfully ports the SIL W-C-905 TECkit mapping
// (MIT, silnrsi/wsresources) as three sequential passes:
//  1. pass(Byte_Unicode): positional ByteClass[Cons]/UniClass[Cons] zip + individual rules
//     (multi-byte rules tried longest-first; unmapped bytes fall back to fallback.Decode).
//  2. pass(Unicode) nukta-coalesce: combine base consonant + U+093C pairs.
//  3. pass(Unicode) visual→logical reorder: move pre-positioned LeftMarks after their
//     consonant; move reph (Cons U+0930 U+094D) before its consonant; and other mark swaps.
//
// ZWJ (U+200D) and ZWNJ (U+200C) are stripped from output.
// Unmapped bytes degrade to fallback.Decode (Latin) rather than dropping.
func decodeWalkmanC905(raw string, fallback TextEncoding) string {
	runes := wc905ByteToUnicode([]byte(raw), fallback)
	runes = wc905Nukta(runes)
	runes = wc905Reorder(runes)
	var sb strings.Builder
	sb.Grow(len(runes) * 3)
	for _, r := range runes {
		if r == 0x200D || r == 0x200C { // strip ZWJ/ZWNJ for searchable text
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// --- Pass 1: Byte → Unicode ---

// wc905ConsMap is the ByteClass[Cons] → UniClass[Cons] positional zip (28 entries).
// Source: W-C-905.map ByteClass[Cons] / UniClass[Cons] line, flattened left-to-right.
// Alignment is the #1 risk: each index must match its peer. Verified against the 6 anchors.
var wc905ConsMap = map[int]rune{
	// ByteClass[Cons] → UniClass[Cons] zip
	// idx byte   Unicode  name
	/* 0 */ 0x64: 0x0915, // क  KA
	/* 1 */ 0x95: 0x0916, // ख  KHA
	/* 2 */ 0x78: 0x0917, // ग  GA
	/* 3 */ 0xC4: 0x0919, // ङ  NGA
	/* 4 */ 0x70: 0x091A, // च  CA
	/* 5 */ 0x4E: 0x091B, // छ  CHA
	/* 6 */ 0x74: 0x091C, // ज  JA
	/* 7 */ 0x3E: 0x091D, // झ  JHA
	/* 8 */ 0x56: 0x091F, // ट  TTA
	/* 9 */ 0x42: 0x0920, // ठ  TTHA
	/* 10 */ 0x4D: 0x0921, // ड  DDA
	/* 11 */ 0x3C: 0x0922, // ढ  DDHA
	/* 12 */ 0x72: 0x0924, // त  TA   — anchor: r→U+0924
	/* 13 */ 0x6E: 0x0926, // द  DA
	/* 14 */ 0x2F: 0x0927, // ध  DHA
	/* 15 */ 0x75: 0x0928, // न  NA
	/* 16 */ 0x69: 0x092A, // प  PA
	/* 17 */ 0x63: 0x092C, // ब  BA
	/* 18 */ 0x65: 0x092E, // म  MA
	/* 19 */ 0x3B: 0x092F, // य  YA
	/* 20 */ 0x6A: 0x0930, // र  RA
	/* 21 */ 0x79: 0x0932, // ल  LA   — anchor: y→U+0932
	/* 22 */ 0xC7: 0x090C, // ऌ  VOCALIC L
	/* 23 */ 0x47: 0x0933, // ळ  LLA
	/* 24 */ 0x6F: 0x0935, // व  VA
	/* 25 */ 0x6C: 0x0938, // स  SA   — anchor: l→U+0938
	/* 26 */ 0x67: 0x0939, // ह  HA
	/* 27 */ 0xDE: 0x0939, // ह  HA   (duplicate last UniClass[Cons] entry)
}

// wc905Single maps single-byte legacy codes to their Unicode string equivalents.
// Bytes covered by wc905ConsMap or multi-byte rules are absent; they are handled
// in wc905ByteToUnicode before reaching this map.
// Rules are transcribed verbatim from W-C-905.map, preserving duplicates.
var wc905Single = map[int]string{
	// --- punctuation ---
	0x20: "\x20", // space
	0x21: "!",    // U+0021
	0x24: "+",    // U+002B
	0x25: "ः",    // visarga ः  (context rule 0x25/digits→":" handled inline)
	0x26: "-",    // U+002D
	0x28: "(",    // U+0028
	0x29: ")",    // U+0029
	0x2A: "’",    // right single quotation mark '
	0x2B: "़",    // nukta ़
	0x2D: ".",    // U+002E
	0x40: "/",    // U+002F
	0x5C: "?",    // U+003F
	0x5D: ",",    // U+002C
	0x5E: "‘",    // left single quotation mark '
	0x5F: ";",    // U+003B
	0xA0: " ",    // NBSP
	0xB7: "*",    // U+002A
	0xB9: "[",    // U+005B
	0xBA: "]",    // U+005D
	0xBB: "%",    // U+0025
	0xBE: "=",    // U+003D

	// --- danda / double-danda (single-byte; 0x41 0x41 is two-byte) ---
	0x41: "।", // । danda
	0xAB: "॥", // ॥ double danda

	// --- independent vowels ---
	0x76: "अ",  // अ A
	0xC3: "अ",  // अ A (alternate)
	0x62: "इ",  // इ I  (0x62 0x5A and 0x62 0xB1 are two-byte)
	0x6D: "उ",  // उ U
	0xC5: "ऊ",  // ऊ UU
	0xCD: "ऋ",  // ऋ RRI
	0xBD: "ऋ",  // ऋ RRI (alternate)
	0xC6: "ऌ",  // ऌ LRI
	0x2C: "ए",  // ए E   (0x2C 0x57 → U+090D, 0x2C 0x73 → U+0910 are two-byte)
	0x3A: "रू", // रू  ru+uu-matra (0x30→U+0930 U+0942)
	0xCE: "ॠ",  // ॠ RR

	// --- vowel matras (dependent) ---
	0x6B: "ा", // ा  AA-matra        — anchor: k→U+093E
	0x66: "ि", // ि  short-i (LeftMark) — anchor: f→U+093F
	0x68: "ी", // ी  II-matra
	0x98: "ु", // ु  U-matra
	0x71: "ु", // ु  U-matra (alternate)
	0xB0: "ु", // ु  U-matra (alternate)
	0x77: "ू", // ू  UU-matra
	0x99: "ू", // ू  UU-matra (alternate)
	0x60: "ृ", // ृ  RI-matra
	0x57: "ॅ", // ॅ  candra-E matra
	0x73: "े", // े  E-matra
	0x53: "ै", // ै  AI-matra

	// --- signs / specials ---
	0x61: "ं", // ं  anusvara
	0xA1: "ँ", // ँ  chandrabindu
	0xA9: "ऺ", // ॉ  (U+093A DEVANAGARI VOWEL SIGN OE)
	0x7E: "्", // ् virama

	// --- reph/conjunct forms encoding multiple runes ---
	0x23: "रु",   // रु  RA+U-matra
	0xAF: "ंि",   // ं  anusvara + short-i
	0xB1: "र्ं",  // र् + anusvara
	0xB2: "र्ंि", // र् + anusvara + short-i
	0xA3: "र्ि",  // र् + short-i

	// --- special consonant symbols ---
	0xBF: "ऽ", // ऽ avagraha
	0xF1: "॰", // ॱ Devanagari abbreviation sign
	0xAC: "ॐ", // ॐ OM

	// --- Devnagari digits (Devdigits class: 0xFA..0xFF 0xF6..0xF9 → U+0966..U+096F) ---
	0xFA: "०", // ०
	0xFB: "१", // १
	0xFC: "२", // २
	0xFD: "३", // ३
	0xFE: "४", // ४
	0xFF: "५", // ५
	0xF6: "६", // ६
	0xF7: "७", // ७
	0xF8: "८", // ८
	0xF9: "९", // ९

	// --- half-consonant (virama) forms: ConsByte > Cons+virama ---
	0x44: "क्", // क्
	0x5B: "ख्", // ख्  (0x5B 0x6B → U+0916 full form — two-byte overrides)
	0x58: "ग्", // ग्  (0x58 0x6B → U+0917)
	0x3F: "घ्", // घ्  (0x3F 0x6B → U+0918)
	0x50: "च्", // च्  (0x50 0x6B → U+091A)
	0x54: "ज्", // ज्  (0x54 0x6B → U+091C)
	0xD6: "झ्", // झ्
	0xD7: "ञ्", // ञ्  (0xD7 0x6B → U+091E)
	0x2E: "ण्", // ण्  (0x2E 0x6B → U+0923)
	0x52: "त्", // त्  (0x52 0x6B → U+0924)
	0x46: "थ्", // थ्  (0x46 0x6B → U+0925)
	0xE8: "ध्", // ध्  (0xE8 0x6B → U+0927)
	0x55: "न्", // न्  (0x55 0x6B → U+0928)
	0x49: "प्", // प्  (0x49 0x6B → U+092A)
	0x43: "ब्", // ब्  (0x43 0x6B → U+092C)
	0x48: "भ्", // भ्
	0x45: "म्", // म्  (0x45 0x6B → U+092E)
	0xD5: "य्", // य्  (0xD5 0x6B → U+092F)
	0x5A: "र्", // र्
	0x59: "ल्", // ल्  (0x59 0x6B → U+0932)
	0x9F: "ळ्", // ळ्
	0x9B: "ळ्", // ळ्  (duplicate)
	0x4F: "व्", // व्  (0x4F 0x6B → U+0935)
	0x27: "श्", // श्  (0x27 0x6B → U+0936)
	0x22: "ष्", // ष्  (0x22 0x6B → U+0937)
	0x4C: "स्", // स्  (0x4C 0x6B → U+0938)
	0x94: "ज़्", // ज़्  (0x94 0x6B → U+095B)
	0x51: "फ्", // फ्  (Q — map comment: "not sure about this!")

	// --- conjunct halves: virama + following consonant ---
	0xEF: "्क", // ्क
	0xF5: "्ग", // ्ग
	0xD3: "्च", // ्च
	0x93: "्ज", // ्ज
	0xDB: "्ञ", // ्ञ
	0xF0: "्ट", // ्ट
	0xF2: "्ठ", // ्ठ
	0xF3: "्ड", // ्ड
	0xF4: "्ढ", // ्ढ
	0xD4: "्य", // ्य
	0x7A: "्र", // ्र
	0xAA: "्र", // ्र  (alternate)
	0x9A: "्ल", // ्ल  (0x9a lowercase in source)
	0xD2: "्व", // ्व

	// --- preformed conjuncts (single byte → multi-rune cluster) ---
	0x82: "क्च",   // क्च
	0xD8: "क्र",   // क्र
	0x87: "क्व",   // क्व
	0x7B: "क्ष्",  // क्ष्  (0x7B 0x6B → full क्ष two-byte)
	0x97: "ख्फ",   // ख्फ
	0x96: "ख्र",   // ख्र
	0x83: "ङ्क्त", // ङ्क्त
	0x8C: "ङ्क्ष", // ङ्क्ष
	0x85: "ङ्ख",   // ङ्ख
	0x86: "ङ्घ",   // ङ्घ
	0x84: "छ्व",   // छ्व
	0x4B: "ज्ञ",   // ज्ञ
	0xAE: "ज्ञ्",  // ज्ञ्
	0xC1: "ड्म",   // ड्म
	0xD9: "त्त्",  // त्त्  (0xD9 0x6B → त्त two-byte)
	0xCB: "त्र",   // त्र
	0x3D: "त्र्",  // त्र्  (0x3D 0x6B → त्र two-byte)
	0xE5: "द्ग",   // द्ग
	0xED: "द्द",   // द्द
	0xBC: "द्ध",   // द्ध
	0x89: "द्ध",   // द्ध  (duplicate)
	0xE4: "द्न",   // द्न
	0x88: "द्ब",   // द्ब
	0x9C: "द्ब्र", // द्ब्र
	0x7C: "द्य",   // द्य
	0xE6: "द्र",   // द्र
	0x7D: "द्व",   // द्व
	0x7F: "द्ध",   // द्ध  (duplicate)
	0xC2: "न्न",   // न्न
	0xE7: "प्र",   // प्र
	0xDA: "फ्र",   // फ्र
	0x8A: "ल्ल",   // ल्ल
	0x4A: "श्र",   // श्र
	0x91: "ष्ट",   // ष्ट
	0x8B: "ष्ट्व", // ष्ट्व
	0x92: "ष्ठ",   // ष्ठ
	0xE2: "हृ",    // हृ
	0xCA: "ह्ण",   // ह्ण
	0xC9: "ह्न",   // ह्न
	0xE3: "ह्म",   // ह्म
	0xE1: "ह्य",   // ह्य
	0xDF: "ह्र",   // ह्र
	0xC8: "ह्ल",   // ह्ल
	0xE0: "ह्व",   // ह्व
	0xD1: "कृ",    // कृ

	// --- alternate consonant glyphs (same Unicode as ByteClass[Cons] members) ---
	0xE9: "क", // क  (alternate glyph)
	0xEA: "ट", // ट
	0xEB: "ठ", // ठ
	0xEE: "ड", // ड
	0xEC: "ढ", // ढ
}

// wc905TwoByte maps two-byte legacy sequences to their Unicode string equivalents.
// Keyed as string([]byte{b0, b1}). Multi-byte rules have priority over single-byte.
var wc905TwoByte = func() map[string]string {
	return map[string]string{
		// --- danda ---
		string([]byte{0x41, 0x41}): "॥", // ॥ double-danda

		// --- conjuncts (two-byte) ---
		string([]byte{0x7B, 0x6B}): "क्ष", // क्ष
		string([]byte{0x78, 0x7A}): "ग्र", // ग्र  xz
		string([]byte{0x78, 0x5A}): "र्ग", // र्ग  xZ
		// 0xDC is ABSENT from the SIL v0.61 draft map (a gap). Render-truth on the recurring
		// "2011-12 श्रृंखला" series header (bytes dc 6b 60 61 5b 6b 79 6b = श्र ृ ं ख ल ा) gives
		// 0xDC 0x6B → श्र, mirroring the map's other half-form + 0x6B → full-consonant rules.
		string([]byte{0xDC, 0x6B}): "श्र", // श्र  (U+0936 U+094D U+0930), render-verified gap-fill
		string([]byte{0xD9, 0x6B}): "त्त", // त्त
		string([]byte{0x3D, 0x6B}): "त्र", // त्र

		// --- half-form + 0x6B → full consonant ---
		string([]byte{0x5B, 0x6B}): "ख", // ख
		string([]byte{0x58, 0x6B}): "ग", // ग
		string([]byte{0x3F, 0x6B}): "घ", // घ
		string([]byte{0x50, 0x6B}): "च", // च
		string([]byte{0x54, 0x6B}): "ज", // ज
		string([]byte{0xD7, 0x6B}): "ञ", // ञ
		string([]byte{0x2E, 0x6B}): "ण", // ण
		string([]byte{0x52, 0x6B}): "त", // त
		string([]byte{0x46, 0x6B}): "थ", // थ
		string([]byte{0xE8, 0x6B}): "ध", // ध
		string([]byte{0x55, 0x6B}): "न", // न
		string([]byte{0x49, 0x6B}): "प", // प
		string([]byte{0x69, 0x51}): "फ", // फ
		string([]byte{0xDD, 0x51}): "फ", // फ  (alternate)
		string([]byte{0x43, 0x6B}): "ब", // ब
		string([]byte{0x48, 0x6B}): "भ", // भ
		string([]byte{0x45, 0x6B}): "म", // म
		string([]byte{0xD5, 0x6B}): "य", // य
		string([]byte{0x59, 0x6B}): "ल", // ल
		string([]byte{0x4F, 0x6B}): "व", // व
		string([]byte{0x27, 0x6B}): "श", // श
		string([]byte{0x22, 0x6B}): "ष", // ष
		string([]byte{0x4C, 0x6B}): "स", // स
		string([]byte{0x94, 0x6B}): "ज़", // ज़
		string([]byte{0x4D, 0x2B}): "ड़", // ड़
		string([]byte{0x3C, 0x2B}): "ढ़", // ढ़

		// --- vowel matras (two-byte) ---
		string([]byte{0x6B, 0x57}): "ॉ", // ॉ candra-O matra
		string([]byte{0x6B, 0x73}): "ो", // ो O-matra
		string([]byte{0x6B, 0x53}): "ौ", // ौ AU-matra

		// --- independent vowels (two-byte) ---
		string([]byte{0x76, 0x6B}): "आ",  // आ AA  (0x76 0x6B 0x73/0x53 are three-byte)
		string([]byte{0x62, 0x5A}): "ई",  // ई II
		string([]byte{0x62, 0xB1}): "ईं", // ई + anusvara
		string([]byte{0x2C, 0x57}): "ऍ",  // ऍ candra-E
		string([]byte{0x2C, 0x73}): "ऐ",  // ऐ AI
	}
}()

// wc905ThreeByte maps three-byte legacy sequences to their Unicode string equivalents.
var wc905ThreeByte = func() map[string]string {
	return map[string]string{
		string([]byte{0x69, 0x51, 0x2B}): "फ़", // फ़  i+Q+nukta
		string([]byte{0x76, 0x6B, 0x73}): "ओ", // ओ  O
		string([]byte{0x76, 0x6B, 0x53}): "औ", // औ  AU
	}
}()

// wc905FourByte maps four-byte legacy sequences to their Unicode string equivalents.
var wc905FourByte = func() map[string]string {
	return map[string]string{
		string([]byte{0x61, 0x69, 0x2B, 0x51}): "फ़",    // फ़
		string([]byte{0x69, 0x73, 0x7A, 0x51}): "फ्रे", // फ्रे  iszQ
	}
}()

// wc905ByteToUnicode applies pass(Byte_Unicode): convert each legacy byte (or multi-byte
// sequence) to Unicode runes. Longest-match first (4→3→2→1 byte). ByteClass[Cons]
// positional mapping applies for single-byte consonants. Unmapped bytes go to fallback.
func wc905ByteToUnicode(b []byte, fallback TextEncoding) []rune {
	out := make([]rune, 0, len(b)*2)
	for i := 0; i < len(b); {
		if s, adv := wc905MultiByte(b, i); adv > 0 {
			out = append(out, []rune(s)...)
			i += adv
			continue
		}
		out = wc905AppendSingle(out, b, i, fallback)
		i++
	}
	return out
}

// wc905MultiByte returns the decoded string and consumed byte count for the LONGEST 4/3/2-byte
// rule matching at b[i], or ("", 0) when no multi-byte rule applies.
func wc905MultiByte(b []byte, i int) (string, int) {
	n := len(b)
	if i+3 < n {
		if s, ok := wc905FourByte[string(b[i:i+4])]; ok {
			return s, 4
		}
	}
	if i+2 < n {
		if s, ok := wc905ThreeByte[string(b[i:i+3])]; ok {
			return s, 3
		}
	}
	if i+1 < n {
		if s, ok := wc905TwoByte[string(b[i:i+2])]; ok {
			return s, 2
		}
	}
	return "", 0
}

// wc905AppendSingle resolves the single byte b[i] (passthroughs, the 0x25-colon context, the
// ByteClass[Cons] zip, the individual rules, else the Latin fallback) and appends its runes to out.
func wc905AppendSingle(out []rune, b []byte, i int, fallback TextEncoding) []rune {
	c := int(b[i])
	switch {
	case c <= 0x1F: // CTL passthrough
		return append(out, rune(c))
	case c >= 0x30 && c <= 0x39: // ASCII digit passthrough
		return append(out, rune(c))
	case c == 0x25 && wc905ColonContext(out, b, i): // 0x25 between digits → colon
		return append(out, ':')
	}
	if r, ok := wc905ConsMap[c]; ok {
		return append(out, r)
	}
	if s, ok := wc905Single[c]; ok {
		return append(out, []rune(s)...)
	}
	return append(out, []rune(fallback.Decode(string(b[i:i+1])))...) // Latin fallback
}

// wc905ColonContext reports whether 0x25 at b[i] sits between two ASCII digits (the map's
// "0x25 / [digits] _ [digits] > U+003A" reference-colon rule).
func wc905ColonContext(out []rune, b []byte, i int) bool {
	return len(out) > 0 && out[len(out)-1] >= 0x30 && out[len(out)-1] <= 0x39 &&
		i+1 < len(b) && b[i+1] >= 0x30 && b[i+1] <= 0x39
}

// --- Pass 2: Nukta-coalesce (pass(Unicode)) ---

// wc905NuktaPairs is the nukta-coalesce map: base consonant + U+093C → precomposed form.
// Source: W-C-905.map pass(Unicode) "Consonants with dots" block.
// Note: the map has `; U+0921 U+093C > U+0919` commented out.
var wc905NuktaPairs = map[rune]rune{
	0x0928: 0x0929, // न  + nukta → ऩ
	0x0930: 0x0931, // र  + nukta → ऱ
	0x0933: 0x0934, // ळ  + nukta → ऴ
	0x0915: 0x0958, // क  + nukta → क़
	0x0916: 0x0959, // ख  + nukta → ख़
	0x0917: 0x095A, // ग  + nukta → ग़
	0x091C: 0x095B, // ज  + nukta → ज़
	0x0921: 0x095C, // ड  + nukta → ड़
	0x0922: 0x095D, // ढ  + nukta → ढ़
	0x092B: 0x095E, // फ  + nukta → फ़
	0x092F: 0x095F, // य  + nukta → य़
}

// wc905Nukta applies pass(Unicode) nukta-coalesce: Cons U+093C → precomposed form.
func wc905Nukta(in []rune) []rune {
	out := make([]rune, 0, len(in))
	for i := 0; i < len(in); {
		if i+1 < len(in) && in[i+1] == 0x093C {
			if precomp, ok := wc905NuktaPairs[in[i]]; ok {
				out = append(out, precomp)
				i += 2
				continue
			}
		}
		out = append(out, in[i])
		i++
	}
	return out
}

// --- Pass 3: Visual → logical reorder (pass(Unicode)) ---

// Unicode class predicates, sourced from W-C-905.map Define + pass(Unicode) UniClass lines.

// wc905IsCons reports whether r is a Devanagari consonant per _Cons.
// _Cons: U+0915..U+0939 U+0958..U+095F
func wc905IsCons(r rune) bool {
	return (r >= 0x0915 && r <= 0x0939) || (r >= 0x0958 && r <= 0x095F)
}

// wc905IsLeftMark reports whether r is in _LeftMarks.
// _LeftMarks: U+093F U+094E
func wc905IsLeftMark(r rune) bool {
	return r == 0x093F || r == 0x094E
}

// wc905IsAboveMark reports whether r is in _AboveMarks.
// _AboveMarks: U+0900..U+0902 U+093A U+0945..U+0948 U+0951 U+0953..U+0955
func wc905IsAboveMark(r rune) bool {
	return (r >= 0x0900 && r <= 0x0902) || r == 0x093A ||
		(r >= 0x0945 && r <= 0x0948) || r == 0x0951 ||
		(r >= 0x0953 && r <= 0x0955)
}

// wc905IsBelowMark reports whether r is in _BelowMarks.
// _BelowMarks: U+093C U+0941..U+0944 U+094D U+0952 U+0956..U+0957 U+0962..U+0963
func wc905IsBelowMark(r rune) bool {
	return r == 0x093C || (r >= 0x0941 && r <= 0x0944) || r == 0x094D ||
		r == 0x0952 || (r >= 0x0956 && r <= 0x0957) ||
		(r >= 0x0962 && r <= 0x0963)
}

// wc905IsRightMark reports whether r is in _RightMarks.
// _RightMarks: U+0903 U+093B U+093E U+0940 U+0949..U+094C U+094F
func wc905IsRightMark(r rune) bool {
	return r == 0x0903 || r == 0x093B || r == 0x093E || r == 0x0940 ||
		(r >= 0x0949 && r <= 0x094C) || r == 0x094F
}

// wc905Reorder applies pass(Unicode) visual→logical reorder rules from W-C-905.map,
// in their original order (top-to-bottom), left-to-right, non-overlapping:
//
//	[LeftMarks]=a [Cons]=b U+094D=c [Cons]=d  > @b @c @d @a  (conjunct + LeftMark)
//	[LeftMarks]=a [Cons]=b                     > @b @a        (simple LeftMark)
//	U+0902=a [RightMarks]=b                    > @b @a        (anusvara before RightMark)
//	[AboveMarks]=a [BelowMarks]=b              > @b @a        (AboveMark before BelowMark)
//	U+0902=a [AboveMarks]=b                    > @b @a        (anusvara before AboveMark)
//	[Cons]=a U+0930 U+094D                     > U+0930 U+094D @a  (reph to pre-base)
func wc905Reorder(in []rune) []rune {
	out := make([]rune, 0, len(in))
	for i := 0; i < len(in); {
		if app, adv := wc905ReorderMatch(in, i); adv > 0 {
			out = append(out, app...)
			i += adv
			continue
		}
		out = append(out, in[i])
		i++
	}
	return out
}

// wc905ReorderMatch returns the reordered runes and consumed count for the FIRST matching reorder
// rule at in[i] (LeftMark rules before the other-mark rules, matching the map's rule order), or
// (nil, 0) when no rule applies.
func wc905ReorderMatch(in []rune, i int) ([]rune, int) {
	if app, adv := wc905ReorderLeftMark(in, i); adv > 0 {
		return app, adv
	}
	return wc905ReorderMarks(in, i)
}

// wc905ReorderLeftMark handles the two LeftMark rules (a pre-positioned short-i moves after its
// consonant, or after the whole consonant+halant+consonant conjunct).
func wc905ReorderLeftMark(in []rune, i int) ([]rune, int) {
	n := len(in)
	r := in[i]
	if !wc905IsLeftMark(r) {
		return nil, 0
	}
	// Rule 1: [LeftMarks]=a [Cons]=b U+094D=c [Cons]=d > @b @c @d @a
	if i+3 < n && wc905IsCons(in[i+1]) && in[i+2] == 0x094D && wc905IsCons(in[i+3]) {
		return []rune{in[i+1], in[i+2], in[i+3], r}, 4
	}
	// Rule 1r: [LeftMarks]=a [Cons]=b U+0930 U+094D > U+0930 U+094D @b @a  (short-i + Cons + REPH).
	// The map's separate LeftMark (line 293) and reph (line 299) rules do not compose in a single
	// forward pass when both attach to ONE consonant: the short-i rule consumes the consonant before
	// the reph rule can match it, stranding the reph after the short-i (आ ि थ र ् → आ थ ि र ्, i.e.
	// आर्थिक → आथिर्क). Render-truth (आर्थिक) requires the reph to lead and the short-i to trail the
	// consonant; this combined rule reproduces what TECkit's overlapping match yields.
	if i+3 < n && wc905IsCons(in[i+1]) && in[i+2] == 0x0930 && in[i+3] == 0x094D {
		return []rune{0x0930, 0x094D, in[i+1], r}, 4
	}
	// Rule 2: [LeftMarks]=a [Cons]=b > @b @a
	if i+1 < n && wc905IsCons(in[i+1]) {
		return []rune{in[i+1], r}, 2
	}
	return nil, 0
}

// wc905ReorderMarks handles the anusvara/mark-swap rules and the reph rule, in the map's order.
func wc905ReorderMarks(in []rune, i int) ([]rune, int) {
	n := len(in)
	r := in[i]
	switch {
	// Rule 3: U+0902=a [RightMarks]=b > @b @a
	case r == 0x0902 && i+1 < n && wc905IsRightMark(in[i+1]):
		return []rune{in[i+1], r}, 2
	// Rule 4: [AboveMarks]=a [BelowMarks]=b > @b @a
	case wc905IsAboveMark(r) && i+1 < n && wc905IsBelowMark(in[i+1]):
		return []rune{in[i+1], r}, 2
	// Rule 5: U+0902=a [AboveMarks]=b > @b @a
	case r == 0x0902 && i+1 < n && wc905IsAboveMark(in[i+1]):
		return []rune{in[i+1], r}, 2
	// Rule 6: [Cons]=a U+0930 U+094D > U+0930 U+094D @a  (reph to pre-base)
	case wc905IsCons(r) && i+2 < n && in[i+1] == 0x0930 && in[i+2] == 0x094D:
		return []rune{0x0930, 0x094D, r}, 3
	}
	return nil, 0
}
