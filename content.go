// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"math"
	"strings"
)

// The package walks PDF content streams with four parallel interpreters, each a
// distinct sink over the same operator grammar. They are intentionally separate,
// not duplication to merge (see the dead-code audit KEEP-AS-IS disposition):
//   - contentState  (content.go)   positioned Text/Rect with full graphics state
//   - plainTextState (plaintext.go) flat UTF-8 for GetPlainText
//   - walkState      (walk.go)      deprecated column/row extraction
//   - imageScanState (images.go)    image-draw metadata
//
// contentState holds the mutable interpreter state for the Content() operator loop.
// The text encoder lives in g.enc (part of the graphics state) so q/Q save and
// restore it together with the current font; see gstate.
type contentState struct {
	g             gstate
	text          []Text
	rect          []Rect
	stroke        []Stroke  // accumulated stroked straight-line segments
	subpaths      [][]Point // current path under construction (display space)
	gstack        []gstate
	p             Page
	resources     Value
	depth         int
	fonts         map[string]*Font
	counters      decodeCounters // per-decode-path glyph counts for this run
	rotatedWarned bool           // WarningRotatedText fired once for this run
	argbuf        []Value        // reused per-operator arg scratch — handlers MUST NOT retain args (see popArgsReuse)
}

// appendText decodes a genuine content show-string through the current encoder,
// records it against the decode-path counters, and lays out one Text entry per
// glyph. Interpreter-synthesised separators must use appendSeparator instead.
func (s *contentState) appendText(str string) {
	decoded := s.g.enc.Decode(str)
	s.counters.record(s.g.encSource, decoded)
	// Only a Type0 font with a degenerate /DW of 0 needs per-CID /W widths (the
	// Bold-Cambria stacking bug); every other font keeps the cheap one-byte-per-
	// rune path, byte-identical to before. The check is per text-show, not per glyph.
	if cd, ok := s.g.enc.(codeDecoder); ok && s.g.Tf.needsPerCIDWidth() {
		s.layoutComposite(cd, str)
		return
	}
	s.layoutDecoded(str, decoded)
}

// appendSeparator lays out an interpreter-synthesised separator (a TJ kerning
// space, or the newline appended after a TJ array) WITHOUT counting it: those
// runes are layout, not content. The plain-text sink emits no such separators,
// so counting them here would make the decode-path counters disagree by entry
// point — the exact discrepancy this framework exists to prevent.
//
// In vertical writing mode the separator must NOT advance the text matrix: it
// decodes to a real rune under the multibyte -V CMaps (e.g. "\n" → "\n" via
// Shift-JIS), and layoutDecoded's vertical branch advances a full em per rune
// regardless of the glyph — which would push any real text after the TJ in the
// same text object an extra line down. The separator Text entry is still
// recorded (a layout marker); only its advance is suppressed. Horizontal mode is
// left byte-identical (its separator advance is the glyph width, ~0 for "\n").
// Vertical word-segmentation/leading is a WS3 concern.
//
// sep is interpreter-chosen ASCII ("\n" or " "), not a font code byte, but is
// still routed through the content font's encoder so the -V CMaps keep the
// real-rune advance handling above. A simpleCmapEncoder (simple font + a
// ToUnicode CMap) has no bfchar/bfrange entry for the 0x0A/0x20 separator byte
// and decodes it to U+FFFD, which would otherwise leak a replacement glyph into
// the text/Words sink for every TJ array (observed as the trailing U+FFFD run on
// Adobe-subset-font tables). The separator is already its own Unicode, so when the
// decode is exactly that single unmapped U+FFFD, fall back to the literal sep —
// matching the byte-identical passthrough every other encoder gives "\n"/" ".
//
// The match is intentionally exact (== one U+FFFD), not "contains U+FFFD": sep is
// always a single ASCII byte, so a genuinely-unmapped decode is exactly one
// replacement rune. A font whose ToUnicode legitimately maps 0x0A/0x20 to a
// multi-rune sequence that happens to include U+FFFD is then left untouched —
// the font's own mapping wins over this fallback.
func (s *contentState) appendSeparator(sep string) {
	decoded := s.g.enc.Decode(sep)
	if decoded == string(noRune) {
		decoded = sep
	}
	if s.g.vertical {
		saved := s.g.Tm
		s.layoutDecoded(sep, decoded)
		s.g.Tm = saved
		return
	}
	s.layoutDecoded(sep, decoded)
}

// appendGlyph emits one Text entry for ch with advance w0 and advances the text
// matrix. It is the per-glyph emission body, factored out of layoutDecoded so
// that layoutComposite can reuse it while supplying a per-CID width for DW==0 fonts.
func (s *contentState) appendGlyph(ch rune, w0 float64) {
	f := s.g.Tf.BaseFont()
	if i := strings.Index(f, "+"); i >= 0 {
		f = f[i+1:]
	}
	Trm := matrix{{s.g.Tfs * s.g.Th, 0, 0}, {0, s.g.Tfs, 0}, {0, s.g.Trise, 1}}.mul(s.g.Tm).mul(s.g.CTM)
	if !s.rotatedWarned && Trm[0][1] != 0 {
		s.rotatedWarned = true
		s.p.V.warn(WarningRotatedText, "rotated text matrix (non-horizontal baseline)")
	}
	// H is the up-vector magnitude (nominal font height: rotation-invariant
	// and always >= 0); Rotation is the baseline angle, CCW-positive in
	// degrees. math.Atan2(0, x>0) is exactly 0 and math.Hypot(0, h)=|h|, so
	// ordinary horizontal text yields Rotation==0 and H==FontSize, exactly.
	// Computed unconditionally: a guarded fast-path that skips these on
	// horizontal text was benchmarked and gave no benefit — the per-glyph
	// transcendentals are negligible; the measured extraction-path cost is the
	// two extra struct fields (a wider Text), which a guard cannot avoid.
	s.text = append(s.text, Text{
		Font:     f,
		FontSize: Trm[0][0],
		X:        Trm[2][0],
		Y:        Trm[2][1],
		W:        w0 / 1000 * Trm[0][0],
		H:        math.Hypot(Trm[1][0], Trm[1][1]),
		Rotation: math.Atan2(Trm[0][1], Trm[0][0]) * 180 / math.Pi,
		S:        string(ch),
	})
	// Advance to the next glyph. Vertical writing (a -V CMap) moves down the
	// page by the PDF default vertical displacement (DW2 w1 = -1000 → one em;
	// Th does not scale vertical advance, PDF 32000-1 §9.4.4) — GoPDF reads no
	// per-glyph CIDFont /W2 metrics, so every vertical glyph uses this
	// em-square default. Horizontal writing advances along x by the width.
	if s.g.vertical {
		s.g.advance(0, -s.g.Tfs+s.g.Tc)
	} else {
		s.g.advance((w0/1000*s.g.Tfs+s.g.Tc)*s.g.Th, 0)
	}
}

// layoutDecoded appends one Text entry per decoded rune to s.text, advancing the
// text matrix after each glyph. It fires WarningRotatedText once per run when the
// text-rendering matrix rotates the baseline off horizontal — detected by a
// nonzero Trm[0][1], the Y-component of the writing (advance) direction. That
// term is what collapses FontSize = Trm[0][0] for a 90° run. It deliberately
// ignores Trm[1][0]-only matrices: a horizontal-baseline shear (synthetic italic)
// slants glyph verticals while keeping the baseline horizontal and FontSize
// intact, so its geometry stays reliable and must not be flagged. str supplies
// the raw code points for width lookup; decoded supplies the Unicode runes.
// Separators and every non-DW==0 font use this path; DW==0 composite runs use
// layoutComposite instead (see appendText).
func (s *contentState) layoutDecoded(str, decoded string) {
	n := 0
	for _, ch := range decoded {
		var w0 float64
		if n < len(str) {
			w0 = s.g.Tf.effectiveWidth(int(str[n]))
		}
		n++
		s.appendGlyph(ch, w0)
	}
}

// layoutComposite lays out a composite (Type0) run whose font has /DW==0, walking
// whole multi-byte codes so each CID's /W width is applied once — after the code's
// LAST decoded rune; any earlier ligature runes sit at the CID origin (zero
// advance), so the next code starts past them, never on top. Only reached for DW==0
// fonts that have a code decoder (see appendText). cidWidth is used (not
// effectiveWidth) so the full assembled CID is looked up in /W; effectiveWidth only
// returns the uniform /DW and is used by layoutDecoded where code is a single byte
// that may be a partial CID.
//
// Known limitation (no corpus fixture; degrades no worse than pre-fix baseline):
// two residual DW==0 zero-advance/stacking paths are accepted, not guarded. (1) A
// code whose ToUnicode mapping is EMPTY (decodeOne returns nb>0 with no runes)
// emits no glyph and loses that CID's advance. (2) A DW==0 Type0 font whose encoder
// is NOT a codeDecoder never reaches here at all — appendText sends it to
// layoutDecoded, where effectiveWidth's degenerate-DW guard gives a uniform 1000.
// Both are malformed/exotic inputs that already mis-rendered before this fix; the
// fix targets the real case (Identity-H ToUnicode CIDFonts like MS-Office Bold-Cambria).
func (s *contentState) layoutComposite(cd codeDecoder, str string) {
	for pos := 0; pos < len(str); {
		runes, nb := cd.decodeOne(str[pos:])
		if nb == 0 {
			// No code formed: mirror cmap.Decode's noRune recovery (one byte).
			s.appendGlyph(noRune, s.g.Tf.cidWidth(int(str[pos])))
			pos++
			continue
		}
		w0 := s.g.Tf.cidWidth(beCID(str[pos : pos+nb]))
		// One CID advances exactly once, after its LAST decoded rune. A CID that a
		// ToUnicode CMap expands into several runes (a ligature) places them all at
		// the CID origin (zero advance), then the final rune carries the single CID
		// width — so the next CID starts past the ligature, never on top of it. The
		// common 1-rune CID hits the last-rune branch directly.
		for j, ru := range runes {
			if j == len(runes)-1 {
				s.appendGlyph(ru, w0)
			} else {
				s.appendGlyph(ru, 0)
			}
		}
		pos += nb
	}
}

// beCID assembles a big-endian integer CID from up to 4 raw code bytes — the CID
// for an Identity-H composite font, where code == CID.
func beCID(code string) int {
	cid := 0
	for i := 0; i < len(code) && i < 4; i++ {
		cid = cid<<8 | int(code[i])
	}
	return cid
}

// handleGraphics handles path-construction and graphics-state operators.
func (s *contentState) handleGraphics(op string, args []Value) {
	switch op {
	case "cm":
		if len(args) != 6 {
			panic("bad g.Tm")
		}
		s.g.CTM = matrixFrom6Args(args).mul(s.g.CTM)
	case "re":
		if len(args) != 4 {
			panic("bad re")
		}
		x, y, w, h := args[0].Float64(), args[1].Float64(), args[2].Float64(), args[3].Float64()
		s.rect = append(s.rect, rectFromCTM(s.g.CTM, x, y, w, h))
	case "q":
		s.gstack = append(s.gstack, s.g)
	case "Q":
		if n := len(s.gstack) - 1; n >= 0 {
			s.g = s.gstack[n]
			s.gstack = s.gstack[:n]
		}
		// g, cs, scn, gs: no-op (color/graphics-state operators we don't model)
	}
}

func matrixFrom6Args(args []Value) matrix {
	var m matrix
	for i := range 6 {
		m[i/2][i%2] = args[i].Float64()
	}
	m[2][2] = 1
	return m
}

// rectFromCTM maps a user-space rectangle (the re operator's x, y, w, h operands)
// through the CTM and returns the axis-aligned bounding box of its four transformed
// corners — the display-space convention imageRefFromCTM already uses for images, so
// Content().Rect agrees with the Text and ImageRef geometry on a /Rotate- or
// cm-transformed page. With an identity CTM (an unrotated page, no cm) and non-negative
// w/h it reduces to {Point{x, y}, Point{x + w, y + h}}, byte-identical to the prior
// raw-operand result; the bbox is always normalized (Min <= Max), unlike the old
// un-normalized output for a negative-dimension re.
func rectFromCTM(ctm matrix, x, y, w, h float64) Rect {
	corners := [...]Point{
		transformPoint(ctm, x, y),
		transformPoint(ctm, x+w, y),
		transformPoint(ctm, x, y+h),
		transformPoint(ctm, x+w, y+h),
	}
	minX, maxX := corners[0].X, corners[0].X
	minY, maxY := corners[0].Y, corners[0].Y
	for _, p := range corners[1:] {
		minX = math.Min(minX, p.X)
		maxX = math.Max(maxX, p.X)
		minY = math.Min(minY, p.Y)
		maxY = math.Max(maxY, p.Y)
	}
	return Rect{Point{minX, minY}, Point{maxX, maxY}}
}

// handlePath routes path-construction (m l c v y h) and path-painting
// (S s f F B b n W and variants) operators. The re rectangle stays in
// handleGraphics (it feeds Rect, not the segment accumulator).
func (s *contentState) handlePath(op string, args []Value) {
	switch op {
	case "m", "l", "c", "v", "y", "h":
		s.buildPath(op, args)
	default:
		s.paintPath(op)
	}
}

// buildPath extends the current path. m begins a subpath; l adds a straight
// segment; c/v/y are Bézier curves (not ruling lines), so they break the
// straight run by starting a fresh subpath at the curve's endpoint; h closes
// the current subpath. Points are mapped to display space through the CTM in
// effect now, matching rectFromCTM.
func (s *contentState) buildPath(op string, args []Value) {
	switch op {
	case "m":
		if len(args) != 2 {
			panic("bad m")
		}
		s.subpaths = append(s.subpaths, []Point{transformPoint(s.g.CTM, args[0].Float64(), args[1].Float64())})
	case "l":
		if len(args) != 2 {
			panic("bad l")
		}
		s.appendPathPoint(transformPoint(s.g.CTM, args[0].Float64(), args[1].Float64()))
	case "c", "v", "y":
		if n := len(args); n >= 2 {
			s.subpaths = append(s.subpaths, []Point{transformPoint(s.g.CTM, args[n-2].Float64(), args[n-1].Float64())})
		}
	case "h":
		s.closeSubpath()
	}
}

// appendPathPoint adds p to the current subpath, starting one if the path is
// empty (a lineto with no preceding moveto — lenient toward malformed streams).
func (s *contentState) appendPathPoint(p Point) {
	if len(s.subpaths) == 0 {
		s.subpaths = append(s.subpaths, []Point{p})
		return
	}
	i := len(s.subpaths) - 1
	s.subpaths[i] = append(s.subpaths[i], p)
}

// closeSubpath appends the current subpath's start point, closing the polygon so
// its final edge is captured when stroked. It is idempotent: a subpath already
// closed (last point already equals the start) is left unchanged, so an explicit
// h followed by a close-and-stroke painter (s/b/b*) does not synthesize a spurious
// zero-length closing segment.
func (s *contentState) closeSubpath() {
	if n := len(s.subpaths); n > 0 && len(s.subpaths[n-1]) > 0 {
		sp := s.subpaths[n-1]
		if sp[len(sp)-1] != sp[0] {
			s.subpaths[n-1] = append(sp, sp[0])
		}
	}
}

// paintPath consumes the current path. A stroke-painting operator (S s B B* b b*)
// emits each subpath's consecutive point pairs as Stroke segments; the close
// variants (s b b*) close first. Every painting operator then clears the path.
// Fill-only (f F f*) and end-path (n) emit nothing; clip (W W*) keeps the path
// for the following painter.
func (s *contentState) paintPath(op string) {
	switch op {
	case "W", "W*":
		return
	case "s", "b", "b*":
		s.closeSubpath()
	}
	switch op {
	case "S", "s", "B", "B*", "b", "b*":
		s.emitStrokes()
	}
	s.subpaths = s.subpaths[:0]
}

// emitStrokes appends each subpath's consecutive point pairs to s.stroke.
func (s *contentState) emitStrokes() {
	for _, sp := range s.subpaths {
		for i := 1; i < len(sp); i++ {
			s.stroke = append(s.stroke, Stroke{From: sp[i-1], To: sp[i]})
		}
	}
}

func (s *contentState) applyTd(tx, ty float64) {
	x := matrix{{1, 0, 0}, {0, 1, 0}, {tx, ty, 1}}
	s.g.Tlm = x.mul(s.g.Tlm)
	s.g.Tm = s.g.Tlm
}

// handleTd validates args and moves the text position by (tx, ty).
func (s *contentState) handleTd(args []Value) {
	if len(args) != 2 {
		panic("bad Td")
	}
	s.applyTd(args[0].Float64(), args[1].Float64())
}

// handleTm validates args and sets both text matrices from a 6-element array.
func (s *contentState) handleTm(args []Value) {
	if len(args) != 6 {
		panic("bad g.Tm")
	}
	m := matrixFrom6Args(args)
	s.g.Tm = m
	s.g.Tlm = m
}

// handleTextMatrix handles BT, ET, T*, TD, Td, and Tm operators.
func (s *contentState) handleTextMatrix(op string, args []Value) {
	switch op {
	case "BT":
		s.g.Tm = ident
		s.g.Tlm = s.g.Tm
	case "ET":
		// no-op
	case "T*":
		s.applyTd(0, -s.g.Tl)
	case "TD":
		if len(args) != 2 {
			panic("bad Td")
		}
		s.g.Tl = -args[1].Float64()
		s.handleTd(args)
	case "Td":
		s.handleTd(args)
	case "Tm":
		s.handleTm(args)
	}
}

// handleTf handles the Tf (set text font and size) operator.
func (s *contentState) handleTf(args []Value) {
	if len(args) != 2 {
		panic("bad Tf")
	}
	f := s.font(args[0].Name())
	s.g.Tf = *f
	s.g.vertical = verticalWritingCMap(f.V.Key("Encoding").Name())
	s.g.enc, s.g.encSource = f.cachedEncoder()
	if s.g.enc == nil {
		if DebugOn {
			println("no cmap for", args[0].Name())
		}
		s.g.enc = &nopEncoder{}
		s.g.encSource = encSourceUnset
	}
	s.g.Tfs = args[1].Float64()
}

// font returns the cached *Font for the named resource, building it once so the
// font's encoder (and ToUnicode CMap) is parsed a single time per interpreter run.
func (s *contentState) font(name string) *Font {
	if cached, ok := s.fonts[name]; ok {
		return cached
	}
	v := s.resources.Key("Font").Key(name)
	if v.IsNull() {
		s.resources.warn(WarningMissingGlyphMapping, "font resource "+clampDetail(name)+" not found in page resources")
	}
	f := &Font{V: v}
	if s.fonts == nil {
		s.fonts = map[string]*Font{}
	}
	s.fonts[name] = f
	return f
}

func requireOneArg(args []Value, op string) {
	if len(args) != 1 {
		panic("bad " + op)
	}
}

// handleTextParams handles scalar text-state operators: Tc, TL, Tr, Ts, Tw, Tz.
func (s *contentState) handleTextParams(op string, args []Value) {
	requireOneArg(args, op)
	switch op {
	case "Tc":
		s.g.Tc = args[0].Float64() //nolint:gosec // requireOneArg guarantees len==1
	case "TL":
		s.g.Tl = args[0].Float64() //nolint:gosec // requireOneArg guarantees len==1
	case "Tr":
		s.g.Tmode = int(args[0].Int64()) //nolint:gosec // requireOneArg guarantees len==1
	case "Ts":
		s.g.Trise = args[0].Float64() //nolint:gosec // requireOneArg guarantees len==1
	case "Tw":
		s.g.Tw = args[0].Float64() //nolint:gosec // requireOneArg guarantees len==1
	case "Tz":
		s.g.Th = args[0].Float64() / 100 //nolint:gosec // requireOneArg guarantees len==1
	}
}

// tjSpaceThreshold is the minimum TJ kerning magnitude (in thousandths of a
// text-space unit) that is treated as a word-boundary gap. Values at or beyond
// this threshold cause a synthetic space to be emitted before the next string
// segment. 120 is a conservative word-gap threshold (unidoc/unipdf #524).
const tjSpaceThreshold = 120.0

// interpretTJArray handles the TJ operand array; numeric elements are kerning offsets.
func (s *contentState) interpretTJArray(v Value) {
	needSpace := false
	for i := 0; i < v.Len(); i++ {
		x := v.Index(i)
		if x.Kind() == String {
			if needSpace {
				s.appendSeparator(" ")
				needSpace = false
			}
			s.appendText(x.RawString())
		} else {
			// A TJ numeric adjustment translates along the writing axis: x for
			// horizontal, y for vertical (-V CMap). Same magnitude; Th scales only
			// the horizontal axis (PDF 32000-1 §9.4.4).
			adj := -x.Float64() / 1000 * s.g.Tfs
			if s.g.vertical {
				s.g.advance(0, adj)
			} else {
				s.g.advance(adj*s.g.Th, 0)
			}
			// The word-gap threshold sign is horizontal-specific (a vertical gap is
			// the opposite sign); left unchanged, so vertical word-segmentation is
			// unaffected and deferred to WS3.
			if x.Float64() <= -tjSpaceThreshold {
				needSpace = true
			}
		}
	}
}

// handleTextShow handles text-show operators: Tj, TJ, ', and ".
func (s *contentState) handleTextShow(op string, args []Value) {
	switch op {
	case "\"":
		if len(args) != 3 {
			panic("bad \" operator")
		}
		s.g.Tw = args[0].Float64()
		s.g.Tc = args[1].Float64()
		args = args[2:]
		fallthrough
	case "'":
		if len(args) != 1 {
			panic("bad ' operator")
		}
		s.applyTd(0, -s.g.Tl)
		fallthrough
	case "Tj":
		if len(args) != 1 {
			panic("bad Tj operator")
		}
		s.appendText(args[0].RawString())
	case "TJ":
		s.interpretTJArray(args[0])
		s.appendSeparator("\n")
	}
}

// interpretXObject is the Do operator body for Form XObjects, walked in their
// own resource context. Image XObjects are handled by Page.Images().
func (s *contentState) interpretXObject(name string) {
	xobj := s.resources.Key("XObject").Key(name)
	if xobj.Key("Subtype").Name() != "Form" {
		return
	}
	// Interpret the form in its own space: concatenate the form's /Matrix (form
	// space → the user space at the Do site) with the CTM in effect at the Do
	// operator, so the form's text comes out in page space rather than
	// form-local. Note: GetTextByRow/GetTextByColumn use a separate interpreter
	// (walk.go) that tracks no CTM, so their XObject coordinates stay form-local;
	// only Content()/Words() get page-space coordinates from here.
	sub := &contentState{
		g:         gstate{Th: 1, CTM: formMatrix(xobj).mul(s.g.CTM), enc: &nopEncoder{}},
		p:         s.p,
		resources: xobj.Key("Resources"),
		depth:     s.depth + 1,
	}
	// Merge the sub-state's decode counts even if the form panics mid-stream, so
	// glyphs decoded before a malformed operator survive into the partial result
	// (mirrors imageScanState.interpretXObject). On the normal path recover()==nil,
	// so the single explicit merge below runs and counts are never doubled. Text
	// and rects keep their pre-existing recover semantics (Content returns the
	// parent's partial text on a panic; the form's partial text is not merged).
	defer func() {
		if rec := recover(); rec != nil {
			s.counters.merge(sub.counters)
			panic(rec)
		}
	}()
	Interpret(xobj, sub.interpret)
	s.text = append(s.text, sub.text...)
	s.rect = append(s.rect, sub.rect...)
	s.counters.merge(sub.counters)
}

// formMatrix returns a Form XObject's /Matrix entry as a matrix, or the identity
// matrix when /Matrix is absent or malformed. /Matrix maps form space into the
// user space in effect where the form is invoked (PDF 32000-1:2008 §8.10.1).
// Every element must be a number: a non-numeric entry would otherwise resolve to
// 0 via Float64() and silently collapse the form's text coordinates, so a
// length-6 array containing any non-number is treated as malformed → identity.
func formMatrix(xobj Value) matrix {
	m := xobj.Key("Matrix")
	if m.Kind() != Array || m.Len() != 6 {
		return ident
	}
	args := make([]Value, 6)
	for i := range args {
		e := m.Index(i)
		if k := e.Kind(); k != Integer && k != Real {
			return ident
		}
		args[i] = e
	}
	return matrixFrom6Args(args)
}

// popArgsReuse drains stk into buf (grown if needed), preserving argument order,
// and returns the filled prefix. The caller stores the returned slice's backing
// (buf may have been reallocated) and reuses it for the next operator, so the
// per-operator []Value allocation popArgs made is paid once per interpreter pass
// instead of once per operator.
//
// CONTRACT — MUST NOT be violated: a handler MUST NOT retain the returned slice
// or any element of it beyond its own operator dispatch. The backing array is
// OVERWRITTEN on the next operator. Storing args, args[i], or &args[i] anywhere
// that outlives the dispatch (a struct field, append, closure, goroutine, or
// deferred read) is a silent corruption bug that only manifests under specific
// operator sequences. Every current handler stores only COMPUTED values
// (Rect/gstate/matrix/decoded string), never an arg — keep it that way.
func popArgsReuse(stk *Stack, buf []Value) []Value {
	n := stk.Len()
	if cap(buf) < n {
		buf = make([]Value, n)
	}
	buf = buf[:n]
	for i := n - 1; i >= 0; i-- {
		buf[i] = stk.Pop()
	}
	return buf
}

// handleDo executes the Do operator, walking Form XObjects up to the depth limit.
func (s *contentState) handleDo(args []Value) {
	if s.depth >= xobjMaxDepth || len(args) == 0 {
		return
	}
	s.interpretXObject(args[0].Name())
}

// interpret is the per-operator callback passed to Interpret.  It collects
// stack arguments then dispatches to the appropriate handler.
func (s *contentState) interpret(stk *Stack, op string) {
	s.argbuf = popArgsReuse(stk, s.argbuf)
	args := s.argbuf
	switch op {
	case "cm", "re", "q", "Q", "g", "cs", "scn", "gs":
		s.handleGraphics(op, args)
	case "m", "l", "c", "v", "y", "h",
		"S", "s", "f", "F", "f*", "B", "B*", "b", "b*", "n", "W", "W*":
		s.handlePath(op, args)
	case "BT", "ET", "T*", "TD", "Td", "Tm":
		s.handleTextMatrix(op, args)
	case "Tf":
		s.handleTf(args)
	case "Tc", "TL", "Tr", "Ts", "Tw", "Tz":
		s.handleTextParams(op, args)
	case "Tj", "TJ", "'", "\"":
		s.handleTextShow(op, args)
	case "Do":
		s.handleDo(args)
	}
}

// Content returns the page's content.
// All Text.S values in the returned Content are verbatim UTF-8 extracted from the
// PDF; no HTML, shell, or other escaping is applied. Callers must escape at their
// output sink (e.g. html.EscapeString before writing to an HTML template).
// For a page with no Contents stream, Content returns a zero Content whose Text, Rect, and
// Stroke slices are nil.
// Rect coordinates are in the page's upright display space: each re rectangle's
// corners are mapped through the current transformation matrix (page /Rotate and any
// cm) and returned as their axis-aligned bounding box.
// Stroke segments are the straight lines painted by a stroke operator (S, s, B, b),
// with endpoints in the same display space as Rect; see Stroke.
// If the content stream causes a panic (e.g. malformed operator arguments),
// the defer/recover returns whatever text and rectangles were collected before
// the crash rather than propagating the panic to the caller.
func (p Page) Content() (out Content) {
	var s *contentState
	defer func() {
		if recover() != nil && s != nil {
			out = Content{s.text, s.rect, s.stroke}
		}
	}()
	s = newContentState(p)
	if s == nil {
		return
	}
	Interpret(p.V.Key("Contents"), s.interpret)
	return Content{s.text, s.rect, s.stroke}
}

func newContentState(p Page) *contentState {
	if p.V.IsNull() || p.V.Key("Contents").Kind() == Null {
		return nil
	}
	return &contentState{
		g:         gstate{Th: 1, CTM: p.rotateMatrix(), enc: &nopEncoder{}},
		p:         p,
		resources: p.Resources(),
	}
}

// decodeCountersFromContent runs the content interpreter (the Words/Texts/Lines
// sink) and returns its per-decode-path counters. Internal: it is the content
// half of the cross-sink agreement check and a source for the per-page
// extraction ratios. Partial counts are returned on a content-stream panic,
// mirroring Content's recover contract.
func (p Page) decodeCountersFromContent() (c decodeCounters) {
	s := newContentState(p)
	if s == nil {
		return
	}
	defer func() {
		if recover() != nil {
			c = s.counters
		}
	}()
	Interpret(p.V.Key("Contents"), s.interpret)
	return s.counters
}
