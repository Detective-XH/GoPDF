// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"fmt"
	"strconv"
	"strings"
)

// A Font represent a font in a PDF file.
// The methods interpret a Font dictionary stored in V.
type Font struct {
	V         Value
	enc       TextEncoding
	encSource encSource // decode-path tag classified alongside enc; valid once enc != nil
}

// BaseFont returns the font's name (BaseFont property).
func (f Font) BaseFont() string {
	return f.V.Key("BaseFont").Name()
}

// FirstChar returns the code point of the first character in the font.
func (f Font) FirstChar() int {
	return int(f.V.Key("FirstChar").Int64())
}

// LastChar returns the code point of the last character in the font.
func (f Font) LastChar() int {
	return int(f.V.Key("LastChar").Int64())
}

// Widths returns the widths of the glyphs in the font.
// In a well-formed PDF, len(f.Widths()) == f.LastChar()+1 - f.FirstChar().
func (f Font) Widths() []float64 {
	x := f.V.Key("Widths")
	var out []float64
	for i := 0; i < x.Len(); i++ {
		out = append(out, x.Index(i).Float64())
	}
	return out
}

// Width returns the width of the given code point.
func (f Font) Width(code int) float64 {
	first := f.FirstChar()
	last := f.LastChar()
	if code < first || last < code {
		return 0
	}
	return f.V.Key("Widths").Index(code - first).Float64()
}

// effectiveWidth returns the horizontal glyph advance for code (1/1000 text-space
// units). Composite (Type0) fonts return the uniform /DW; a degenerate /DW of 0
// (or an absent /DW) maps to the spec default 1000 so glyphs never all stack at one
// x. This is the path layoutDecoded uses, where code may be a partial byte of a
// multi-byte CID, so a per-CID /W lookup would be wrong. The full-CID per-/W path
// lives in cidWidth, reached via layoutComposite for the per-CID cases needsPerCIDWidth
// selects (DW==0 code-decodable fonts like BEA's Bold-Cambria, and non-zero-DW Identity-H/V
// fonts that carry a /W array); the 1000 fallback here covers the rare DW==0 font that cannot
// be code-decoded (no corpus fixture hits it). Simple fonts delegate to Width.
func (f Font) effectiveWidth(code int) float64 {
	if f.V.Key("Subtype").Name() == "Type0" {
		dw := f.V.Key("DescendantFonts").Index(0).Key("DW")
		if (dw.Kind() == Integer || dw.Kind() == Real) && dw.Float64() != 0 {
			return dw.Float64()
		}
		return 1000
	}
	return f.Width(code)
}

// cidWidth returns the advance for a full CID in a composite (Type0) font. It consults the
// per-CID /W array (PDF 32000-1 §9.7.4.3) FIRST whenever /W covers the CID, falling back to the
// descendant /DW for CIDs absent from /W (or to the spec default 1000 when /DW is the degenerate
// 0). Only layoutComposite calls this, always with a full multi-byte CID assembled from
// Identity-H/V code bytes — needsPerCIDWidth gates the non-zero-/DW path to Identity encodings,
// so beCID == CID and the /W index is correct. Reading /W for a non-zero /DW is what corrects
// proportional Type0 glyph advances (Latin/Cyrillic CIDFont wrappers that declare /DW=1000 plus a
// real /W): without it the over-wide uniform advance drifts glyph positions and interleaves a long
// label with the adjacent number. The DW==0 branch is byte-identical to before (/W → else 1000).
func (f Font) cidWidth(cid int) float64 {
	desc := f.V.Key("DescendantFonts").Index(0)
	dw := 1000.0
	if d := desc.Key("DW"); d.Kind() == Integer || d.Kind() == Real {
		dw = d.Float64()
	}
	if w := cidWidthFromW(desc.Key("W"), cid); w >= 0 {
		return w
	}
	if dw == 0 {
		return 1000
	}
	return dw
}

// needsPerCIDWidth reports whether a Type0 run must be laid out through the per-CID /W path
// (layoutComposite) rather than the cheap uniform-/DW path (layoutDecoded). Two cases:
//   - /DW == 0 (the Bold-Cambria stacking bug): /W must be read or every CID advances 0 and the
//     run stacks at one x. UNCHANGED behaviour.
//   - /DW != 0 with a /W array AND an Identity-H/V encoding: /W carries the real proportional
//     advances that the uniform /DW ignores; reading them stops a long label's over-wide bbox from
//     engulfing the adjacent right-aligned number (the cross-script char-interleaving bug). Gated
//     on Identity because layoutComposite derives the CID as beCID(codeBytes) — a valid /W index
//     only when code == CID (Identity-H/V). Non-Identity composite fonts (predefined/embedded
//     CMaps; GoPDF has no code→CID map) keep uniform /DW; they are CJK full-width where /DW ≈
//     correct, so the uniform advance is already right.
//
// The content interpreter keeps the cheap one-byte-per-rune width path for every other font.
// Checked once per text-show.
func (f Font) needsPerCIDWidth() bool {
	if f.V.Key("Subtype").Name() != "Type0" {
		return false
	}
	desc := f.V.Key("DescendantFonts").Index(0)
	d := desc.Key("DW")
	if (d.Kind() == Integer || d.Kind() == Real) && d.Float64() == 0 {
		return true
	}
	if !isIdentityHVName(f.V.Key("Encoding").Name()) {
		return false
	}
	w := desc.Key("W")
	return w.Kind() == Array && w.Len() > 0
}

// cidWidthFromW returns the width of cid from a CIDFont /W array, or -1 if the
// array does not cover cid. /W mixes two entry forms: `c [w1 w2 …]` (consecutive
// CIDs from c) and `cFirst cLast w` (a run sharing one width). Malformed arrays
// yield -1 rather than panicking.
func cidWidthFromW(w Value, cid int) float64 {
	if w.Kind() != Array {
		return -1
	}
	i := 0
	for i < w.Len() {
		c := w.Index(i)
		if c.Kind() != Integer {
			return -1 // malformed: bail out
		}
		first := int(c.Int64())
		next := w.Index(i + 1) // null Value if out of range (Kind()==Null)
		switch next.Kind() {
		case Array: // Form 1: c [w...]
			if cid >= first && cid < first+next.Len() {
				return next.Index(cid - first).Float64()
			}
			i += 2
		case Integer, Real: // Form 2: cFirst cLast w
			last := int(next.Int64())
			wval := w.Index(i + 2)
			if cid >= first && cid <= last {
				if wval.Kind() != Integer && wval.Kind() != Real {
					return -1 // truncated / malformed array
				}
				return wval.Float64()
			}
			i += 3
		default:
			return -1 // malformed / truncated
		}
	}
	return -1
}

// Encoder returns the encoding between font code point sequences and UTF-8.
// NOTE: this method has a VALUE receiver; the assignment to f.enc does NOT
// persist across calls. Internal hot paths (per-page font maps, the content
// interpreter) must use cachedEncoder instead.
func (f Font) Encoder() TextEncoding {
	if f.enc == nil {
		f.enc, _ = f.getEncoder() // value receiver: the source tag cannot persist, so discard it
	}
	return f.enc
}

// cachedEncoder returns f's TextEncoding and its decode-path source, parsing
// them once and memoizing on f.enc / f.encSource. The pointer receiver is
// essential: it lets the cache persist across calls, so a font's ToUnicode CMap
// is parsed once instead of on every Tf operator. Callers that hold a *Font (the
// per-page font maps, the content interpreter's font cache) must use this, not
// the value-receiver Encoder.
func (f *Font) cachedEncoder() (TextEncoding, encSource) {
	if f.enc == nil {
		f.enc, f.encSource = f.getEncoder()
	}
	return f.enc, f.encSource
}

// cachedReadCmap parses toUnicode's CMap, memoizing it on the Reader's
// document-level encoder cache keyed by the ToUnicode stream's own object
// pointer, so a font referenced on many pages parses its CMap once per Reader
// instead of once per (page, sink). It falls back to a direct readCmap when no
// Reader is reachable (a detached Font value) or the stream has no stable object
// pointer (always indirect in a conformant PDF, but a zero ptr is guarded so a
// pathological direct stream can never alias the cache).
func (f Font) cachedReadCmap(toUnicode Value) (m *cmap) {
	defer func() {
		if rec := recover(); rec != nil {
			if !isIntentionalParserPanic(rec) {
				panic(rec) // runtime fault or unknown value — a real bug, fail loudly
			}
			f.V.warn(WarningMalformedToUnicode,
				fontRef(f)+": ToUnicode CMap parse panicked: "+fmt.Sprintf("%v", rec))
			m = nil
		}
	}()
	r := f.V.r
	if r == nil || toUnicode.ptr == (objptr{}) {
		return readCmap(toUnicode)
	}
	return r.encoders.lookup(toUnicode.ptr, toUnicode)
}

// isSimpleFontSubtype reports whether a /Subtype names a simple font, which by
// PDF 32000-1:2008 §9.6 always uses single-byte character codes (as opposed to a
// composite Type0 font, whose code width is set by its CMap). Used to pick 1-byte
// ToUnicode decoding. An unknown/empty subtype is treated as composite (false) so
// the codespace-driven path stays the default for anything not provably simple.
func isSimpleFontSubtype(subtype string) bool {
	switch subtype {
	case "Type1", "TrueType", "Type3", "MMType1":
		return true
	default:
		return false
	}
}

// getEncoder selects the font's TextEncoding and reports the decode-path source
// it came from (so decoded glyphs can be attributed without extending the
// TextEncoding interface). The source mirrors the diagnostic emitted at each
// branch 1:1.
func (f Font) getEncoder() (TextEncoding, encSource) {
	// A known legacy non-Unicode Indic font decodes its script to Latin gibberish unless a per-variant
	// transducer recovers it. Try the remap FIRST; flag document-scoped ONLY on the fall-through (no
	// transducer / declined gate), so Words/Lines/GetPlainText consumers get an honest signal on a
	// still-gibberish font without a RECOVERED font carrying a now-false "decodes to gibberish" warning.
	// The STRICT family list is used here because this path cannot corroborate with decoded script (it
	// runs before decoding), unlike the per-table detector. The remap is placed BEFORE the ToUnicode
	// branch because these fonts carry a Latin /ToUnicode that would otherwise return first (M2).
	if isLegacyIndicFontStrict(f.BaseFont()) {
		if enc := f.legacyDevanagariEncoder(); enc != nil {
			return enc, encSourceLegacyRemap
		}
		// Composite (Type0) sibling — see legacy_krutidev010_widths.go.
		if enc := f.legacyDevanagariCompositeEncoder(); enc != nil {
			return enc, encSourceLegacyRemapComposite
		}
		f.V.warn(WarningLegacyFont, fontRef(f)+": legacy non-Unicode Indic font; text decodes to Latin gibberish, not the intended script")
	}
	return f.getEncoderInner()
}

func (f Font) getEncoderInner() (TextEncoding, encSource) {
	toUnicode := f.V.Key("ToUnicode")
	if toUnicode.Kind() == Stream {
		if m := f.cachedReadCmap(toUnicode); m != nil {
			// A simple font always uses 1-byte codes; decode it one byte per code
			// so a (common, Adobe) 2-byte ToUnicode codespacerange cannot make the
			// width-driven cmap.Decode mis-chunk the 1-byte codes into U+FFFD.
			if isSimpleFontSubtype(f.V.Key("Subtype").Name()) {
				return &simpleCmapEncoder{m: m, fallback: f.encodingByteFallback()}, encSourceToUnicode
			}
			return m, encSourceToUnicode
		}
		if DebugOn {
			println("ToUnicode stream failed to parse, falling back to Encoding")
		}
		f.V.warn(WarningMissingToUnicode, fontRef(f)+": ToUnicode CMap failed to parse")
	}
	// A Type0 font that declares /ToUnicode /Identity-H (or -V) as a NAME (not a stream)
	// is a real producer pattern (e.g. some LibreOffice/FreeType output): its 2-byte codes
	// are UCS-2 big-endian Unicode, not glyph/CID indices. Decode them directly. The
	// dual gate (wantsIdentityUCS2) keeps genuine CJK CIDFonts off this path.
	if e := f.identityUCS2Encoder(toUnicode); e != nil {
		return e, encSourceToUnicode
	}
	// A Type0 font with /Encoding /Identity-H|V, a genuine Adobe CID ordering (Japan1/…), and
	// NO usable /ToUnicode has 2-byte codes that are CIDs (Identity ⇒ code==CID), not Unicode.
	// Map them through the Adobe CID→Unicode table. adobeCIDToUnicodeTable's default→nil keeps
	// the PR #71 Identity-ordering case and every non-Adobe font off this path.
	if e := f.adobeOrderingCIDEncoder(); e != nil {
		return e, encSourceCIDMap
	}
	enc := f.V.Key("Encoding")
	switch enc.Kind() {
	case Name:
		return f.namedEncoder(enc.Name())
	case Dict:
		d, unknown := newDictEncoder(enc)
		if unknown > 0 {
			f.V.warn(WarningMissingGlyphMapping,
				fontRef(f)+": "+strconv.Itoa(unknown)+" unmappable glyph entries in /Differences")
		}
		return d, encSourceDict
	case Null:
		return &byteEncoder{&pdfDocEncoding}, encSourceSimple
	default:
		if DebugOn {
			println("unexpected encoding", enc.String())
		}
		f.V.warn(WarningUnsupportedEncoding, fontRef(f)+": unexpected /Encoding kind")
		return &byteEncoder{&pdfDocEncoding}, encSourceUnsupported
	}
}

// encodingByteFallback returns the font's /Encoding decoded as a byte-table encoder, or nil
// when there is no CONFIDENT byte mapping. It is the per-byte fallback simpleCmapEncoder uses
// for ToUnicode entries poisoned to U+FFFD, so it must never fabricate. The governing
// invariant, defined by getEncoder's OWN encSource confidence: yield a character ONLY where
// getEncoder's /Encoding branch is confident and warning-free — an explicit /Differences entry,
// a known base-encoding name (WinAnsi/MacRoman/Standard), or an absent /Encoding (PDFDocEncoding,
// GoPDF's documented simple-font default, encSourceSimple). Where getEncoder would itself tag
// encSourceUnsupported/encSourceMissingToUnicode (an unknown / Identity / CJK name, whether at
// the top level or as a dict /BaseEncoding), decline → the poisoned code stays U+FFFD.
func (f Font) encodingByteFallback() TextEncoding {
	enc := f.V.Key("Encoding")
	switch enc.Kind() {
	case Name:
		if e := trustedBaseByteEncoding(enc.Name()); e != nil {
			return e
		}
		return nil // unknown / Identity / CJK name: no confident byte fallback
	case Dict:
		return dictByteFallback(enc)
	case Null:
		// Absent /Encoding: PDFDocEncoding is GoPDF's simple-font default (getEncoder returns
		// the same here, encSourceSimple). This is NOT fabrication — a font with no /Encoding
		// and no ToUnicode already decodes via pdfDoc; declining would make a poisoned-ToUnicode
		// font decode strictly worse than the identical font without ToUnicode (incoherent: a
		// poisoned entry should at worst be ignored, falling back to what GoPDF would use anyway).
		return &byteEncoder{&pdfDocEncoding}
	default:
		return nil
	}
}

// trustedBaseByteEncoding returns the byte-table encoder for a base-encoding NAME a simple
// font may genuinely declare (WinAnsi/MacRoman/Standard), or nil for any other name (unknown,
// Identity-*, or a CJK predefined CMap). It mirrors baseEncodingTable's recognized names but,
// unlike it (and unlike encoderForCMapName), does NOT default an unknown name to PDFDocEncoding:
// nil means "no confident byte mapping", so a ToUnicode code poisoned to U+FFFD stays U+FFFD
// rather than being fabricated from a guessed encoding. Returning a concrete *byteEncoder (never
// a typed-nil) keeps encodingByteFallback's nil check honest.
func trustedBaseByteEncoding(name string) *byteEncoder {
	switch name {
	case "WinAnsiEncoding":
		return &byteEncoder{&winAnsiEncoding}
	case "MacRomanEncoding":
		return &byteEncoder{&macRomanEncoding}
	case "StandardEncoding":
		return &byteEncoder{&standardEncoding}
	default:
		return nil
	}
}

// dictByteFallback builds the poison-fallback byte encoder for an /Encoding dict. Unlike
// newDictEncoder it never fabricates from an unknown explicit /BaseEncoding: /Differences
// entries (authoritative AGL glyph-name → Unicode mappings) always decode, but the base table
// is confident only for an absent base (PDFDocEncoding default) or a trusted base name; an
// unknown / Identity / CJK base name contributes an all-U+FFFD base, so a poisoned code that is
// NOT in /Differences stays U+FFFD instead of being guessed. (The symmetric guard to the Name
// branch — closes the dict-wrapper fabrication path.)
func dictByteFallback(enc Value) TextEncoding {
	table := fallbackBaseTable(enc.Key("BaseEncoding"))
	applyDifferences(&table, enc.Key("Differences"))
	return &dictEncoder{table: table}
}

// fallbackBaseTable returns the base 256-rune table for a poison-fallback dict decode:
// PDFDocEncoding when /BaseEncoding is absent (GoPDF's simple-font default), the trusted table
// for a known base name (WinAnsi/MacRoman/Standard), or an all-U+FFFD table for an explicit but
// unknown / Identity / CJK base name — so an unknown base contributes no fabricated character.
func fallbackBaseTable(base Value) [256]rune {
	switch base.Kind() {
	case Null:
		return pdfDocEncoding // absent base: GoPDF's documented simple-font default
	case Name:
		if e := trustedBaseByteEncoding(base.Name()); e != nil {
			return *e.table
		}
	}
	// Unknown base name, or a present-but-malformed (non-Name, non-Null) base — neither is a
	// confident mapping (a malformed base is NOT absent): an all-U+FFFD base, so a poisoned code
	// not covered by an explicit /Differences entry stays U+FFFD rather than guessing pdfDoc.
	var t [256]rune
	for i := range t {
		t[i] = noRune
	}
	return t
}

// verticalWritingCMap reports whether a predefined PDF CMap/Encoding name selects
// vertical writing mode (WMode 1). The predefined CJK CMaps encode direction in
// the name: a "-V" suffix is vertical; "-H" (or no suffix) is horizontal.
func verticalWritingCMap(name string) bool {
	return strings.HasSuffix(name, "-V")
}

// identityUCS2Encoder returns a UCS-2 big-endian decoder for the (nonstandard but real)
// pattern where a Type0 font declares /Encoding /Identity-H (2-byte codes) and /ToUnicode
// /Identity-H AS A NAME, signalling that its 2-byte codes are UCS-2 BE Unicode code points
// rather than glyph/CID indices. Returns nil (getEncoder continues unchanged) unless every
// gate in wantsIdentityUCS2 passes. Without this, getEncoder falls through to the byte path
// and decodes each 2-byte code as two 1-byte codes (the high byte -> U+FFFD), garbling the
// entire document.
func (f Font) identityUCS2Encoder(toUnicode Value) TextEncoding {
	// Cheap early-out for the common simple (non-composite) font: this branch runs on
	// every encoder selection that lacks a ToUnicode stream, so the DescendantFonts/
	// CIDSystemInfo walk must not be paid for a WinAnsi/TrueType font.
	if f.V.Key("Subtype").Name() != "Type0" {
		return nil
	}
	ordering := f.V.Key("DescendantFonts").Index(0).Key("CIDSystemInfo").Key("Ordering").Text()
	if wantsIdentityUCS2("Type0", f.V.Key("Encoding").Name(), toUnicode.Name(), ordering) {
		return &ucs2BEEncoder{}
	}
	return nil
}

// wantsIdentityUCS2 is the FP-safety discriminator for identityUCS2Encoder. All four gates
// must hold: a Type0 font (composite, 2-byte-capable); /Encoding Identity-H|V (2-byte codes);
// /ToUnicode Identity-H|V as a Name (the "codes are Unicode" producer signal); and an EXPLICIT
// Identity CIDSystemInfo ordering. The ordering gate is load-bearing and requires the literal
// "Identity" — an ABSENT or malformed /Ordering (chained Key/Index collapses all of those to "")
// is NOT trusted, because a genuine but incompletely-described CJK CIDFont (Adobe-Japan1/GB1/
// CNS1/Korea1) whose Identity-H codes are CIDs could otherwise slip through and be garbled as
// if its CIDs were Unicode. A real CJK CIDFont fails the ToUnicode-is-Name gate (its /ToUnicode
// is absent) AND, even if mislabelled, the explicit-Identity-ordering gate, so it stays on the
// missing-ToUnicode path with its WarningMissingToUnicode. (Reopen trigger: real evidence of a
// producer that omits /Ordering yet needs this path.)
func wantsIdentityUCS2(subtype, encName, toUniName, ordering string) bool {
	return subtype == "Type0" &&
		isIdentityHVName(encName) &&
		isIdentityHVName(toUniName) &&
		ordering == "Identity"
}

// isIdentityHVName reports whether n is the Identity-H or Identity-V CMap name.
func isIdentityHVName(n string) bool {
	return n == "Identity-H" || n == "Identity-V"
}

// adobeOrderingCIDEncoder returns an Adobe CID→Unicode decoder for a Type0 font whose /Encoding
// is /Identity-H|V and whose DescendantFonts CIDSystemInfo identifies a supported Adobe CID
// collection — /Registry (Adobe) AND a supported /Ordering (currently Japan1). Reached from
// getEncoder ONLY when /ToUnicode is absent/unparseable and the PR #71 Name-Identity path
// declined, so the 2-byte codes are CIDs. Returns nil (getEncoder continues unchanged) for any
// other font. The Registry gate plus the ordering switch's default→nil are the FP-safety gates: a
// custom collection that merely reuses the name "Japan1" under a non-Adobe registry, an Identity
// ordering (PR #71's territory), and every non-Adobe font all fall through to nil.
func (f Font) adobeOrderingCIDEncoder() TextEncoding {
	// Cheap early-out for the common simple (non-composite) font — this runs on every encoder
	// selection lacking a ToUnicode stream; the DescendantFonts/CIDSystemInfo walk must not be
	// paid for a WinAnsi/TrueType font.
	if f.V.Key("Subtype").Name() != "Type0" {
		return nil
	}
	encName := f.V.Key("Encoding").Name()
	if !isIdentityHVName(encName) {
		return nil
	}
	// Registry+Ordering together name a CID collection: a non-Adobe registry that reuses the name
	// "Japan1" is a DIFFERENT (custom) ordering whose CIDs the Adobe table would mis-decode, so
	// require the genuine Adobe registry before trusting the ordering.
	cidSystemInfo := f.V.Key("DescendantFonts").Index(0).Key("CIDSystemInfo")
	if cidSystemInfo.Key("Registry").Text() != "Adobe" {
		return nil
	}
	ordering := cidSystemInfo.Key("Ordering").Text()
	table := adobeCIDToUnicodeTable(ordering)
	if table == nil {
		return nil
	}
	// Preserve the vertical-writing diagnostic that namedEncoder would otherwise have fired:
	// this early return bypasses namedEncoder, the only other site that flags Identity-V.
	if verticalWritingCMap(encName) {
		f.V.warn(WarningVerticalWritingMode, fontRef(f)+": vertical writing-mode CMap "+clampDetail(encName))
	}
	f.V.warn(WarningFallbackEncoding,
		fontRef(f)+": Adobe-"+ordering+" Identity CMap decoded via CID→Unicode map (no ToUnicode)")
	return &adobeCIDEncoder{table: table}
}

// adobeCIDToUnicodeTable returns the CID→Unicode table for a supported Adobe ordering, or nil.
// Extensible: GB1/CNS1/Korea1 slot in as new cases once their generated tables + fixtures land.
// The default→nil is the FP-safety gate (excludes Ordering=="Identity" and all non-Adobe orderings
// without an explicit "Identity" check).
func adobeCIDToUnicodeTable(ordering string) []uint16 {
	switch ordering {
	case "Japan1":
		return adobeJapan1CIDToUnicode
	default:
		return nil
	}
}

// namedEncoder resolves a named /Encoding via encoderForCMapName, emits the
// matching diagnostic, and reports the decode-path source. Reached only when the
// font has no usable ToUnicode (getEncoder returns early on a parsed ToUnicode
// CMap), which is why an Identity CMap here means the missing-ToUnicode case.
// The vertical writing-mode check is FIRST, before the Identity / unknown-name
// early returns, so an Identity-V CMap is flagged too. Split from getEncoder to
// keep both under the gocyclo threshold.
func (f Font) namedEncoder(n string) (TextEncoding, encSource) {
	if verticalWritingCMap(n) {
		f.V.warn(WarningVerticalWritingMode, fontRef(f)+": vertical writing-mode CMap "+clampDetail(n))
	}
	e := encoderForCMapName(n)
	if n == "Identity-H" || n == "Identity-V" {
		f.V.warn(WarningMissingToUnicode, fontRef(f)+": Identity CMap without usable ToUnicode")
		return e, encSourceMissingToUnicode
	}
	if _, known := cmapEncoderTable[n]; !known {
		f.V.warn(WarningUnsupportedEncoding, fontRef(f)+": unknown encoding "+clampDetail(n))
		return e, encSourceUnsupported
	}
	switch e.(type) {
	case *multibyteCMapEncoder, *ucs2BEEncoder:
		f.V.warn(WarningFallbackEncoding, fontRef(f)+": predefined CMap "+n+" decoded via charset approximation")
		return e, encSourceFallback
	}
	return e, encSourceSimple
}
