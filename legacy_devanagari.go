// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import "strings"

// legacyDevanagariRemap recovers REAL searchable Unicode Devanagari from a legacy non-Unicode Indic
// font (Kruti-Dev / …) whose content-stream codes map to Latin gibberish on every existing decode
// path. It is a stateless TextEncoding selected in getEncoder (before the ToUnicode branch) for a
// strict-legacy-named font that has a per-variant transducer. Decode runs the variant's byte-level
// transducer (reorder/normalize the legacy codes, then map to Unicode); codes the transducer leaves
// unmapped fall back to the font's own decode (Latin), so an unmapped byte degrades to today's
// gibberish rather than dropping.
type legacyDevanagariRemap struct {
	// decode is the variant's full byte→Unicode transducer; fallback is the font's own encoder, used
	// for any code the transducer leaves unmapped.
	decode   func(raw string, fallback TextEncoding) string
	fallback TextEncoding
}

func (e *legacyDevanagariRemap) Decode(raw string) string {
	return e.decode(raw, e.fallback)
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
	dec := legacyDevanagariVariantDecoder(f.BaseFont())
	if dec == nil {
		return nil
	}
	fb, src := f.getEncoderInner()
	if src != encSourceSimple {
		return nil
	}
	if encoderYieldsDevanagari(fb) {
		return nil
	}
	return &legacyDevanagariRemap{decode: dec, fallback: fb}
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

// legacyVariants lists each known variant's canonical normalized token and its byte→Unicode transducer,
// in no particular order. Per-variant because each legacy family/variant has its own keyboard glyph
// layout (a Walkman code is NOT a Kruti-Dev code).
var legacyVariants = []struct {
	token  string // a legacyVariantKey-normalized token expected as a substring of the font's key
	decode func(raw string, fallback TextEncoding) string
}{
	{"krutidev010", decodeKrutiDev010},
	// Walkman-Chanakya 905 is intentionally ABSENT: the available fixture (in-ecosurvey) is a SUBSETTED
	// Walkman whose codes are remapped (and carry a /ToUnicode), so it is excluded by the canonical-coded
	// gate AND a raw-code table would be overfit/unsafe for other subsets. A canonical-coded Walkman, or a
	// /Differences→canonical bridge for the subsetted case, is future work (see SIL-TABLE-SOURCING doc).
}

// legacyDevanagariVariantDecoder selects the per-variant transducer for a normalized BaseFont name, or
// nil when none matches. It picks the LONGEST matching token so an overlapping/longer variant name
// (e.g. a future "krutidev010wide") cannot be mis-routed to a shorter token's transducer.
func legacyDevanagariVariantDecoder(baseFont string) func(raw string, fallback TextEncoding) string {
	key := legacyVariantKey(baseFont)
	var best func(raw string, fallback TextEncoding) string
	bestLen := 0
	for _, e := range legacyVariants {
		if len(e.token) > bestLen && strings.Contains(key, e.token) {
			best, bestLen = e.decode, len(e.token)
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
