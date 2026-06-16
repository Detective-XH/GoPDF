// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
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

// effectiveWidth returns the horizontal glyph advance for the given character
// code (in 1/1000 text-space units). For composite (Type0) fonts it reads /DW
// from the descendant CIDFont dict, falling back to 1000 per PDF spec §9.7.4.3
// when /DW is absent. Per-CID /W array lookup is not yet implemented: the
// content-stream decoder advances one byte per rune (n++) rather than one
// multi-byte CID, so any per-CID lookup would read a garbage CID. /DW is
// approximate but sufficient for word segmentation, and is locked by the corpus
// gate; per-CID /W support is deferred behind the n++ two-byte-code fix.
// Simple fonts always delegate to Width.
func (f Font) effectiveWidth(code int) float64 {
	if f.V.Key("Subtype").Name() == "Type0" {
		desc := f.V.Key("DescendantFonts").Index(0)
		dw := desc.Key("DW")
		if dw.Kind() == Integer || dw.Kind() == Real {
			return dw.Float64()
		}
		return 1000
	}
	return f.Width(code)
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
func (f Font) cachedReadCmap(toUnicode Value) *cmap {
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
	toUnicode := f.V.Key("ToUnicode")
	if toUnicode.Kind() == Stream {
		if m := f.cachedReadCmap(toUnicode); m != nil {
			// A simple font always uses 1-byte codes; decode it one byte per code
			// so a (common, Adobe) 2-byte ToUnicode codespacerange cannot make the
			// width-driven cmap.Decode mis-chunk the 1-byte codes into U+FFFD.
			if isSimpleFontSubtype(f.V.Key("Subtype").Name()) {
				return &simpleCmapEncoder{m}, encSourceToUnicode
			}
			return m, encSourceToUnicode
		}
		if DebugOn {
			println("ToUnicode stream failed to parse, falling back to Encoding")
		}
		f.V.warn(WarningMissingToUnicode, fontRef(f)+": ToUnicode CMap failed to parse")
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

// verticalWritingCMap reports whether a predefined PDF CMap/Encoding name selects
// vertical writing mode (WMode 1). The predefined CJK CMaps encode direction in
// the name: a "-V" suffix is vertical; "-H" (or no suffix) is horizontal.
func verticalWritingCMap(name string) bool {
	return strings.HasSuffix(name, "-V")
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
