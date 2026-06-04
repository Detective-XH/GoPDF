// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

// A Font represent a font in a PDF file.
// The methods interpret a Font dictionary stored in V.
type Font struct {
	V   Value
	enc TextEncoding
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

// Encoder returns the encoding between font code point sequences and UTF-8.
// NOTE: this method has a VALUE receiver; the assignment to f.enc does NOT
// persist across calls. Internal hot paths (per-page font maps, the content
// interpreter) must use cachedEncoder instead.
func (f Font) Encoder() TextEncoding {
	if f.enc == nil {
		f.enc = f.getEncoder()
	}
	return f.enc
}

// cachedEncoder returns f's TextEncoding, parsing it once and memoizing it on
// f.enc. The pointer receiver is essential: it lets the cache persist across
// calls, so a font's ToUnicode CMap is parsed once instead of on every Tf
// operator. Callers that hold a *Font (the per-page font maps, the content
// interpreter's font cache) must use this, not the value-receiver Encoder.
func (f *Font) cachedEncoder() TextEncoding {
	if f.enc == nil {
		f.enc = f.getEncoder()
	}
	return f.enc
}

func (f Font) getEncoder() TextEncoding {
	toUnicode := f.V.Key("ToUnicode")
	if toUnicode.Kind() == Stream {
		if m := readCmap(toUnicode); m != nil {
			return m
		}
		if DebugOn {
			println("ToUnicode stream failed to parse, falling back to Encoding")
		}
	}
	enc := f.V.Key("Encoding")
	switch enc.Kind() {
	case Name:
		return encoderForCMapName(enc.Name())
	case Dict:
		return newDictEncoder(enc)
	case Null:
		return &byteEncoder{&pdfDocEncoding}
	default:
		if DebugOn {
			println("unexpected encoding", enc.String())
		}
		return &byteEncoder{&pdfDocEncoding}
	}
}
