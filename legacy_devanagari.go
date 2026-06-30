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

// legacyCompositeRemap is the composite (Type0) sibling of legacyDevanagariRemap. bridge is a
// precomputed CID→keystroke-byte table built once at encoder-selection time from the font's own
// /ToUnicode CMap (see legacyCIDKeystrokeBridge): for these legacy composite fonts /ToUnicode maps each
// CID directly to its ORIGINAL WinAnsi keystroke codepoint (a proven, settled fact for both the
// Rajasthan and HP fixtures), so a fixed-width walk of the raw content bytes plus an O(1) map lookup per
// CID recovers the keystroke byte stream — no per-glyph codespace matching needed. Decode here re-runs
// the (non-width) transducer over that keystroke string for callers that don't need positional layout
// (GetPlainText, appendSeparator, the decode-path counters). The width-PRESERVING positional path is
// layoutCompositeLegacyRun in content.go, which calls decodeKrutiDev010Widths directly instead of going
// through Decode.
type legacyCompositeRemap struct {
	bridge   *legacyCIDBridge
	fallback TextEncoding
}

// Decode builds the SAME kdUnit sequence layoutCompositeLegacyRun's position pass builds (width is
// irrelevant here, so every unit gets a dummy positive width) and runs it through the width-tracked
// transducer, taking only the runes. This — rather than concatenating into a string/strings.Builder —
// is deliberate: an early version wrote noRune via strings.Builder.WriteRune for an undecodable CID and
// fed the RESULT into the byte-oriented (non-width) transducer, which silently reinterpreted U+FFFD's
// 3-byte UTF-8 encoding (0xEF 0xBF 0xBD) as three bogus keystroke codes — caught by render-truth on the
// HP fixture as a "ड्ढ{)" artifact appended after appendSeparator(" ") fed a 1-byte separator to a
// 2-byte-codespace CMap. Routing through the SAME kdUnit pipeline the positional path uses sidesteps the
// byte/rune conflation entirely (kd010Pass6W's explicit noRune guard emits the sentinel as ONE rune,
// never explodes it). An undersized tail (fewer than codeWidth bytes remain) consumes one byte and emits
// noRune, mirroring cmap.decodeOne's n==0 (no codespace prefix matched) recovery; a full-width code with
// no bridge entry consumes the whole code and emits noRune, mirroring decodeOne's "codespace matched but
// no bfchar/bfrange target" recovery.
func (e *legacyCompositeRemap) Decode(raw string) string {
	cw := e.bridge.codeWidth
	units := make([]kdUnit, 0, len(raw)/cw+1)
	for pos := 0; pos < len(raw); {
		if pos+cw > len(raw) {
			units = append(units, kdUnit{code: int(noRune), w: 1})
			pos++
			continue
		}
		cid := beCID(raw[pos : pos+cw])
		if by, ok := e.bridge.byCID[cid]; ok {
			units = append(units, kdUnit{code: int(by), w: 1})
		} else {
			units = append(units, kdUnit{code: int(noRune), w: 1})
		}
		pos += cw
	}
	var sb strings.Builder
	for _, g := range decodeKrutiDev010Widths(units, e.fallback) {
		sb.WriteRune(g.r)
	}
	return sb.String()
}

// legacyDevanagariCompositeEncoder is the composite-font sibling of legacyDevanagariEncoder. Scoped to
// the krutidev010 variant only — the family proven on both the Rajasthan and HP fixtures — so an
// unproven composite variant declines rather than risk corrupting good text. Requires a usable font-level
// /ToUnicode (the CID→keystroke proof): fb, src := f.getEncoderInner() returning (*cmap, encSourceToUnicode)
// confirms the font's /ToUnicode is a real, successfully-parsed stream-backed CMap — not, say, the
// Identity-UCS2 name-based pseudo-encoder, which also reports encSourceToUnicode but is not a *cmap. The
// bridge builder then runs on that ALREADY-RESOLVED cmap directly (no redundant re-parse).
func (f Font) legacyDevanagariCompositeEncoder() TextEncoding {
	if f.V.Key("Subtype").Name() != "Type0" {
		return nil
	}
	v := legacyDevanagariVariant(f.BaseFont())
	if v == nil || v.token != "krutidev010" {
		return nil
	}
	fb, src := f.getEncoderInner()
	if src != encSourceToUnicode {
		return nil
	}
	cm, ok := fb.(*cmap)
	if !ok {
		return nil
	}
	bridge, ok := legacyCIDKeystrokeBridge(cm)
	if !ok {
		return nil
	}
	return &legacyCompositeRemap{bridge: bridge, fallback: &byteEncoder{&winAnsiEncoding}}
}

// legacyCIDBridge is a composite (Type0) legacy font's CID → canonical legacy keystroke byte table,
// built by legacyCIDKeystrokeBridge from the font's /ToUnicode CMap. Resolving it ONCE at
// encoder-selection time (rather than re-running cm.decodeOne's variable-width codespace match per
// glyph) gives O(1) lookup by CID for every glyph in the document.
type legacyCIDBridge struct {
	byCID     map[int]byte
	codeWidth int
}

const (
	// legacyCIDMinEntries is the minimum number of declared /ToUnicode entries (bfchar + bfrange) before
	// the fraction gate below is trusted — a CMap with only a handful of entries cannot give
	// legacyCIDMinKeystrokeFrac meaningful statistical weight.
	legacyCIDMinEntries = 8
	// legacyCIDMinKeystrokeFrac is the fraction of declared /ToUnicode entries that must resolve to a
	// single WinAnsi keystroke byte for the font to qualify as a legacy composite candidate. This is an
	// AFFIRMATIVE proof — most of the CMap's own declared targets are recoverable keystrokes — stricter
	// than a "did we fail to find a Devanagari target" check, and (via expandBfrangeKeystrokes) it covers
	// bfrange entries too.
	legacyCIDMinKeystrokeFrac = 0.9
	// legacyCIDMaxRangeSpan bounds the per-bfrange code span expanded into byCID, so a malformed or
	// oversized range (e.g. a stray <0000> <FFFF> bfrange) cannot blow up memory/time. Note: the
	// "vary only the last byte" constraint in expandBfrangeKeystrokes already caps any single accepted
	// range at exactly 256 codes (one byte's full 0x00-0xFF span), so today this check is a defensive
	// cap against a future relaxation of that constraint rather than a path expandBfrangeKeystrokes can
	// currently reach — kept anyway as the explicit, documented bound.
	legacyCIDMaxRangeSpan = 256
)

// legacyCIDKeystrokeBridge builds the CID → keystroke-byte bridge for m, a cmap already resolved from a
// Type0 font's /ToUnicode stream (see legacyDevanagariCompositeEncoder). It declines (ok=false) unless
// the codespace is single-width AND at least legacyCIDMinEntries declared entries are present AND at
// least legacyCIDMinKeystrokeFrac of them resolve to exactly one WinAnsi keystroke byte each.
func legacyCIDKeystrokeBridge(m *cmap) (*legacyCIDBridge, bool) {
	codeWidth := cmapCodeWidth(m)
	if codeWidth == 0 {
		return nil, false
	}
	byCID := make(map[int]byte)
	total, resolved := 0, 0
	for _, bc := range m.bfchar {
		total++
		if by, ok := singleKeystrokeByte([]rune(utf16Decode(bc.repl))); ok {
			byCID[beCID(bc.orig)] = by
			resolved++
		}
	}
	for _, bucket := range m.bfrange {
		for _, entry := range bucket {
			// Count by EXPANDED CID, not declared entry: a bfrange entry is one declaration but can
			// claim anywhere from 1 to legacyCIDMaxRangeSpan CIDs. Counting it as a single vote (the
			// prior bug, caught by adversarial review) lets a handful of wide bfrange entries either
			// swamp a real font's genuine ASCII/Latin /ToUnicode past the threshold (a large real-script
			// range hides as "1 unresolved" while a few small resolving entries dominate the fraction —
			// the font gets wrongly bridged and its real, already-correct CIDs are corrupted to noRune)
			// or starve a real keystroke-coded font expressed via few large ranges below
			// legacyCIDMinEntries (false decline).
			claimed, gotResolved := expandBfrangeKeystrokes(entry, byCID)
			total += claimed
			resolved += gotResolved
		}
	}
	if total < legacyCIDMinEntries {
		return nil, false
	}
	if float64(resolved)/float64(total) < legacyCIDMinKeystrokeFrac {
		return nil, false
	}
	if len(byCID) == 0 {
		return nil, false
	}
	return &legacyCIDBridge{byCID: byCID, codeWidth: codeWidth}, true
}

// cmapCodeWidth returns m's single declared codespace byte-width (1-4), or 0 when the codespace is
// empty or spans more than one width — a mixed-width codespace the fixed-step CID bridge cannot walk.
func cmapCodeWidth(m *cmap) int {
	width := 0
	for n, bucket := range m.space {
		if len(bucket) == 0 {
			continue
		}
		if width != 0 {
			return 0
		}
		width = n + 1
	}
	return width
}

// singleKeystrokeByte reports whether runes is exactly one printable rune with a WinAnsi keystroke byte
// — the shape a legacy composite font's /ToUnicode entry takes when it targets the font's OWN original
// keystroke codepoint rather than the intended (Devanagari) script.
func singleKeystrokeByte(runes []rune) (byte, bool) {
	if len(runes) != 1 || runes[0] < 0x20 || runes[0] == noRune {
		return 0, false
	}
	by, ok := winAnsiRuneToByte[runes[0]]
	return by, ok
}

// expandBfrangeKeystrokes resolves every code in entry's [lo,hi] span to a keystroke byte and adds them
// all to byCID, or declines (byCID untouched) if the range shape is unsupported (zero-length, mismatched
// lo/hi length, varying any byte but the last, an inverted span) or ANY code in the span fails to
// resolve to a single keystroke byte. A partial range is not trustworthy evidence either way, so it
// never partially populates byCID.
//
// Returns (claimed, resolved): claimed is how many CIDs this entry's caller should count toward the
// statistical gate's total — the real expanded span size ([lo,hi] inclusive) whenever the shape is
// well-formed enough to compute one, or 1 for a shape that cannot be sized at all (zero-length /
// length-mismatched / non-last-byte-varying — there's no span to report, so it counts as a single
// indeterminate declared entry, matching this function's pre-fix behavior for that one case only).
// resolved is claimed when every CID in the range resolves (the range is added to byCID), else 0 — the
// all-or-nothing population policy extends to the statistics: a partially-bad range counts as ZERO
// resolved evidence at its full claimed weight, never as partial credit.
func expandBfrangeKeystrokes(entry bfrange, byCID map[int]byte) (claimed, resolved int) {
	n := len(entry.lo)
	if n == 0 || len(entry.hi) != n {
		return 1, 0
	}
	if n > 1 && entry.lo[:n-1] != entry.hi[:n-1] {
		return 1, 0
	}
	lastLo, lastHi := entry.lo[n-1], entry.hi[n-1]
	if lastHi < lastLo {
		return 1, 0
	}
	span := int(lastHi - lastLo)
	claimed = span + 1
	if claimed > legacyCIDMaxRangeSpan {
		return claimed, 0
	}
	code := []byte(entry.lo)
	added := make(map[int]byte, claimed)
	for off := 0; off <= span; off++ {
		code[n-1] = lastLo + byte(off)
		runes, _ := decodeBfrange(entry, string(code))
		by, ok := singleKeystrokeByte(runes)
		if !ok {
			return claimed, 0
		}
		added[beCID(string(code))] = by
	}
	for cid, by := range added {
		byCID[cid] = by
	}
	return claimed, claimed
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
