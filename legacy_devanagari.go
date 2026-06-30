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
	// decode is the variant's full byte→Unicode transducer; fallback is the encoder used for any code
	// the transducer leaves unmapped. bridge (non-nil only for a SUBSETTED variant) remaps each raw
	// content code to its canonical legacy keystroke byte BEFORE the transducer runs — a canonical-coded
	// font has bridge == nil (identity).
	decode   func(raw string, fallback TextEncoding) string
	fallback TextEncoding
	bridge   *[256]byte
}

func (e *legacyDevanagariRemap) Decode(raw string) string {
	if e.bridge != nil {
		buf := make([]byte, len(raw))
		for i := 0; i < len(raw); i++ {
			buf[i] = e.bridge[raw[i]]
		}
		raw = string(buf)
	}
	return e.decode(raw, e.fallback)
}

// legacyDevanagariEncoder returns a remap encoder for f, or nil so getEncoder falls through to today's
// warn-only behaviour. It is deliberately CONSERVATIVE — a false positive here is corrupted good text
// (the warn→decode-change line). Two regimes share the SIMPLE-subtype + variant-match + not-already-
// Devanagari gates and differ only in how the canonical legacy codes are obtained:
//
//  1. SIMPLE one-byte font (Type1/TrueType/…). Composite (Type0) legacy fonts decode multi-byte codes
//     and need the per-CID /W layout path; the per-byte table/fallback here would mis-chunk their codes.
//     Composite remap is a separate (content.go) step, deferred.
//  2. a per-variant transducer exists for the normalized BaseFont name (never remap blind).
//  3. the font's existing decode does NOT already yield Devanagari (a font already producing Indic text
//     is never overwritten).
//     4a. CANONICAL-CODED variant (encSourceSimple): the raw content codes ARE the font's canonical legacy
//     keyboard codes, so the code-keyed table applies directly. (Simple Kruti Dev 010.)
//     4b. SUBSETTED variant (needsDiffBridge): codes are remapped to a compact range and the font carries a
//     /ToUnicode (so src is NOT encSourceSimple). The canonical keystroke bytes are recovered from the
//     font's /Encoding /Differences (glyph name → WinAnsi byte) into a per-object bridge applied before
//     the transducer. The /Differences-resolves requirement IS the FP gate for this path. (Subsetted
//     Walkman-Chanakya 905.)
//
// (The strict-legacy-name gate is applied by the caller, getEncoder, before this runs.)
func (f Font) legacyDevanagariEncoder() TextEncoding {
	if !isSimpleFontSubtype(f.V.Key("Subtype").Name()) {
		return nil
	}
	v := legacyDevanagariVariant(f.BaseFont())
	if v == nil {
		return nil
	}
	fb, src := f.getEncoderInner()
	if encoderYieldsDevanagari(fb) {
		return nil
	}
	if v.needsDiffBridge {
		// Subsetted path. POSITIVE subset-pattern proof: the font must carry a usable /ToUnicode
		// (encSourceToUnicode), the hallmark of a re-encoded/subsetted font. A non-subsetted Walkman
		// with a plain /Encoding dict (encSourceDict) or a bare byte encoding (encSourceSimple) is NOT
		// the compact-subset pattern — its raw codes are not the remapped range the /Differences bridge
		// assumes — so decline it (it falls through to warn-only) rather than risk corrupting good text.
		if src != encSourceToUnicode {
			return nil
		}
		// Recover canonical keystrokes from /Encoding /Differences. Post-bridge bytes are WinAnsi
		// keystrokes, so an unmapped legacy byte falls back to WinAnsi (not the subset-code fb).
		bridge, ok := f.legacyDifferencesBridge()
		if !ok {
			return nil
		}
		return &legacyDevanagariRemap{decode: v.decode, fallback: &byteEncoder{&winAnsiEncoding}, bridge: bridge}
	}
	if src != encSourceSimple {
		return nil
	}
	return &legacyDevanagariRemap{decode: v.decode, fallback: fb}
}

// legacyDifferencesBridge builds a subset-code → canonical legacy keystroke byte table from f's
// /Encoding /Differences, for a SUBSETTED legacy font whose content codes were remapped to a compact
// range. Each /Differences entry names a standard glyph (e.g. /m, /bullet); the name's WinAnsi byte
// position is the canonical keystroke the variant transducer consumes.
//
// It REQUIRES a clean Latin-keystroke map: every /Differences entry must name a glyph that resolves to a
// WinAnsi byte and sit at an in-range code. A single unresolvable name (e.g. a Devanagari glyph name, or
// an arbitrary /gNN slot) makes ok=false so the font falls through to warn-only — never a partial bridge
// that would feed unproven subset codes to the transducer as if they were canonical keystrokes. Codes
// NOT listed in /Differences keep the /BaseEncoding (WinAnsi) identity, which is correct: those are the
// font's plain ASCII codes (space, digits, punctuation) and the transducer maps that ASCII range to
// itself; the subset's recoded glyphs all carry an explicit /Differences entry.
func (f Font) legacyDifferencesBridge() (*[256]byte, bool) {
	enc := f.V.Key("Encoding")
	if enc.Kind() != Dict {
		return nil, false
	}
	diff := enc.Key("Differences")
	if diff.Kind() != Array || diff.Len() == 0 {
		return nil, false
	}
	var b [256]byte
	for i := range b {
		b[i] = byte(i) // identity for codes not recoded by /Differences (the font's plain ASCII range)
	}
	resolved, code := 0, -1
	for j := 0; j < diff.Len(); j++ {
		x := diff.Index(j)
		if x.Kind() == Integer {
			code = int(x.Int64())
			continue
		}
		if x.Kind() != Name {
			continue
		}
		if code < 0 || code >= len(b) {
			return nil, false // an out-of-range code slot → malformed, not the clean subset pattern
		}
		by, ok := winAnsiByteForName(x.Name())
		if !ok {
			return nil, false // an unresolvable glyph name → not a clean Latin-keystroke subset map
		}
		b[byte(code)] = by // code ∈ [0,255] here; a byte index into [256]byte is always in bounds
		resolved++
		code++
	}
	if resolved == 0 {
		return nil, false
	}
	return &b, true
}

// winAnsiRuneToByte inverts winAnsiEncoding (byte→rune) to rune→byte, skipping the undefined CP1252
// slots (rune 0); the lowest byte wins on the (rare) duplicate rune. Built once at init.
var winAnsiRuneToByte = func() map[rune]byte {
	m := make(map[rune]byte, 256)
	for b := range 256 {
		if r := winAnsiEncoding[b]; r != 0 {
			if _, dup := m[r]; !dup {
				m[r] = byte(b)
			}
		}
	}
	return m
}()

// winAnsiByteForName resolves a glyph name to its WinAnsi byte position (Adobe Glyph List → WinAnsi
// inverse). For a subsetted legacy font this byte is the canonical keystroke. Returns false when the
// name is unknown or its rune has no WinAnsi slot.
func winAnsiByteForName(name string) (byte, bool) {
	r := nameToRune[name]
	if r == 0 {
		return 0, false
	}
	b, ok := winAnsiRuneToByte[r]
	return b, ok
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

// legacyVariant is one known legacy family/variant: its legacyVariantKey-normalized token, its
// byte→Unicode transducer, and whether its codes are SUBSETTED (so they need a /Differences→canonical
// bridge before the transducer — see legacyDevanagariEncoder regime 4b).
type legacyVariant struct {
	token           string // a legacyVariantKey-normalized substring of the font's key
	decode          func(raw string, fallback TextEncoding) string
	needsDiffBridge bool
}

// legacyVariants lists each known variant in no particular order. Per-variant because each legacy
// family has its own keyboard glyph layout (a Walkman code is NOT a Kruti-Dev code).
var legacyVariants = []legacyVariant{
	{"krutidev010", decodeKrutiDev010, false},
	// Subsetted Walkman-Chanakya 905: its content codes are remapped to a compact range and it carries a
	// /ToUnicode, so the per-object /Differences bridge (regime 4b) recovers the canonical w-c-905
	// keystrokes the transducer consumes.
	{"walkmanchanakya905", decodeWalkmanC905, true},
}

// legacyDevanagariVariant selects the per-variant entry for a normalized BaseFont name, or nil when none
// matches. It picks the LONGEST matching token so an overlapping/longer variant name (e.g. a future
// "krutidev010wide") cannot be mis-routed to a shorter token's transducer.
func legacyDevanagariVariant(baseFont string) *legacyVariant {
	key := legacyVariantKey(baseFont)
	var best *legacyVariant
	bestLen := 0
	for i := range legacyVariants {
		e := &legacyVariants[i]
		if len(e.token) > bestLen && strings.Contains(key, e.token) {
			best, bestLen = e, len(e.token)
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
