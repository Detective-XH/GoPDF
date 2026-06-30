// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import "strings"

// legacyDevanagariRemap recovers REAL searchable Unicode Devanagari from a legacy non-Unicode Indic
// font (Kruti-Dev / Walkman-Chanakya / …) whose content-stream codes map to Latin gibberish on every
// existing decode path. It is a stateless TextEncoding selected in getEncoder (before the ToUnicode
// branch) for a strict-legacy-named font that has a per-variant byte→Unicode table. Decode maps each
// raw code byte through the variant table, then applies the deterministic visual→logical reorder
// (no NLP). Codes absent from the table fall back to the font's own decode (Latin), so an unmapped
// byte degrades to today's gibberish rather than dropping.
//
// NOTE: this is the M0/M1-proven mechanism. The per-variant TABLE below is a HAND-DERIVED STUB
// (Walkman-Chanakya905, ~6 glyphs, derived from the in-ecosurvey p8 fixture's own codes verified
// against a direct READ of the page) — enough to prove table+reorder end-to-end before the full,
// owner-vetted SIL wsresources (MIT) import supplies the complete per-variant tables.
type legacyDevanagariRemap struct {
	// table maps a raw code byte to its Unicode STRING (not a single rune): a legacy half-form byte
	// expands to a consonant + halant (क + ्), so a one-byte→multi-rune mapping is required.
	table    map[byte]string
	fallback TextEncoding // the font's own encoder, for codes absent from the table
}

func (e *legacyDevanagariRemap) Decode(raw string) string {
	rs := make([]rune, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if s, ok := e.table[raw[i]]; ok {
			rs = append(rs, []rune(s)...)
			continue
		}
		rs = append(rs, []rune(e.fallback.Decode(raw[i:i+1]))...)
	}
	return string(reorderDevanagariVisualToLogical(rs))
}

// reorderDevanagariVisualToLogical rewrites a visual-order Devanagari rune run (the order legacy fonts
// store glyphs in, left-to-right on the page) into Unicode logical order, returning a new slice.
//
// Cluster-aware: the pre-base short-i matra ि (U+093F) is stored BEFORE the consonant cluster it
// attaches to and must move AFTER the WHOLE cluster — a base consonant followed by zero or more
// (halant ् + consonant) conjunct pairs. So `ि C` → `C ि` and `ि C ् C` → `C ् C ि` (e.g. त्रि).
//
// M1 scope still covers short-i only; reph, split matras (ो/ौ), and nukta remain for the full impl.
func reorderDevanagariVisualToLogical(rs []rune) []rune {
	const (
		shortI = 0x093F
		halant = 0x094D
	)
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		if rs[i] == shortI && i+1 < len(rs) && isDevanagariConsonant(rs[i+1]) {
			// Span the maximal consonant cluster starting at i+1: C (halant C)* .
			j := i + 1
			for {
				j++ // consumed a base/conjunct consonant
				if j+1 < len(rs) && rs[j] == halant && isDevanagariConsonant(rs[j+1]) {
					j++ // consume the halant; the loop's j++ consumes the next consonant
					continue
				}
				break
			}
			out = append(out, rs[i+1:j]...) // the cluster
			out = append(out, shortI)       // then the short-i, in its logical slot
			i = j - 1
			continue
		}
		out = append(out, rs[i])
	}
	return out
}

// isDevanagariConsonant reports whether r is a base Devanagari consonant (U+0915–U+0939).
func isDevanagariConsonant(r rune) bool {
	return r >= 0x0915 && r <= 0x0939
}

// legacyDevanagariEncoder returns a remap encoder for f, or nil so getEncoder falls through to today's
// warn-only behaviour. It is deliberately CONSERVATIVE — a false positive here is corrupted good text
// (the warn→decode-change line), so all four gates must hold:
//  1. SIMPLE one-byte font (Type1/TrueType/…). Composite (Type0) legacy fonts decode multi-byte codes
//     and need the per-CID /W layout path; the per-byte table/fallback here would mis-chunk their
//     codes. Composite remap is a separate (content.go) step, deferred.
//  2. a per-variant byte→Unicode table exists for the normalized BaseFont name (never remap blind).
//  3. CANONICAL-CODED font (encSourceSimple): the raw content codes ARE the font's canonical legacy
//     keyboard codes (the trivial byte=PDFDoc path), so the code-keyed table applies directly. A
//     SUBSETTED/re-encoded legacy font remaps its codes to a compact range (carrying a /ToUnicode or
//     /Differences) that the canonical table does NOT match — applying the table to its raw codes would
//     CORRUPT. Such fonts stay warn-only (a /Differences-glyph-name → canonical bridge is future work).
//  4. the font's existing decode does NOT already yield Devanagari (belt — subsumed by (3) today, kept
//     for if (3) later admits other byte-encodings: a font already producing Indic text is never remapped).
//
// (The strict-legacy-name gate is applied by the caller, getEncoder, before this runs.)
func (f Font) legacyDevanagariEncoder() TextEncoding {
	if !isSimpleFontSubtype(f.V.Key("Subtype").Name()) {
		return nil
	}
	tbl := legacyDevanagariTable(f.BaseFont())
	if tbl == nil {
		return nil
	}
	fb, src := f.getEncoderInner()
	if src != encSourceSimple {
		return nil
	}
	if encoderYieldsDevanagari(fb) {
		return nil
	}
	return &legacyDevanagariRemap{table: tbl, fallback: fb}
}

// encoderYieldsDevanagari reports whether enc decodes any single byte code to a Devanagari rune
// (U+0900–U+097F). A legacy font targeted for remap decodes to Latin gibberish — it yields ZERO
// Devanagari — whereas a correctly-encoded font carrying a real ToUnicode/Encoding does yield it. This
// is the decode-change FP gate: a font already producing Devanagari is declined, never overwritten.
// One-time (memoized via cachedEncoder), 256 single-byte probes — negligible.
func encoderYieldsDevanagari(enc TextEncoding) bool {
	var buf [1]byte
	for c := range 256 {
		buf[0] = byte(c)
		for _, r := range enc.Decode(string(buf[:])) {
			if r >= 0x0900 && r <= 0x097F {
				return true
			}
		}
	}
	return false
}

// legacyVariantTables lists each known variant's canonical normalized token and its byte→Unicode table,
// in no particular order. Per-variant because each legacy family/variant has its own keyboard glyph
// layout (a Walkman code is NOT a Kruti-Dev code).
var legacyVariantTables = []struct {
	token string // a legacyVariantKey-normalized token expected as a substring of the font's key
	table map[byte]string
}{
	{"krutidev010", krutiDev010SimpleTable},
	// Walkman-Chanakya 905 is intentionally ABSENT: the available fixture (in-ecosurvey) is a SUBSETTED
	// Walkman whose codes are remapped (and carry a /ToUnicode), so it is excluded by the canonical-coded
	// gate AND a raw-code table would be overfit/unsafe for other subsets. A canonical-coded Walkman, or a
	// /Differences→canonical bridge for the subsetted case, is future work (see SIL-TABLE-SOURCING doc).
}

// legacyDevanagariTable selects the per-variant byte→Unicode table for a normalized BaseFont name, or
// nil when none matches. It picks the LONGEST matching token so an overlapping/longer variant name
// (e.g. a future "krutidev010wide") cannot be mis-routed to a shorter token's table.
func legacyDevanagariTable(baseFont string) map[byte]string {
	key := legacyVariantKey(baseFont)
	var best map[byte]string
	bestLen := 0
	for _, e := range legacyVariantTables {
		if len(e.token) > bestLen && strings.Contains(key, e.token) {
			best, bestLen = e.table, len(e.token)
		}
	}
	return best
}

// legacyVariantKey normalizes a BaseFont name for variant lookup: strip the subset prefix
// (XXXXXX+), fold ASCII case, and drop every non-alphanumeric rune (so "Walkman-Chanakya905Bold"
// and "Walkman Chanakya 905" both fold to "walkmanchanakya905bold"/"walkmanchanakya905").
func legacyVariantKey(baseFont string) string {
	if i := strings.IndexByte(baseFont, '+'); i >= 0 {
		baseFont = baseFont[i+1:]
	}
	var b strings.Builder
	b.Grow(len(baseFont))
	for _, r := range baseFont {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// krutiDev010SimpleTable is the M1 HAND-DERIVED STUB for the SIMPLE (1-byte, ASCII-coded) Kruti Dev 010
// font, derived from HP economic-survey p8 (a Devanagari-abbreviation column) and verified against a
// direct READ of the page: "एन.यू.एल.एम." (N.U.L.M.) extracts from the codes ",u-;w-,y-,e-".
// (The composite/CIDFontType0C Kruti Dev — e.g. Rajasthan — uses 2-byte codes with a DIFFERENT layout
// and is handled by the deferred composite path, gated out here by the simple-font check.)
// REPLACE with the full owner-vetted SIL wsresources (MIT) Kruti Dev table.
var krutiDev010SimpleTable = map[byte]string{
	',': "ए", // ए (independent e)
	'u': "न", // न (na)
	'l': "स", // स (sa)
	'y': "ल", // ल (la)
	'e': "म", // म (ma)
	';': "य", // य (ya)
	'w': "ू", // ू (uu matra, post-base below)
	'-': ".", // abbreviation period (the glyph at code 0x2d is a full stop)
}
