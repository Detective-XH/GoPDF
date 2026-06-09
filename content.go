// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
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
	gstack        []gstate
	p             Page
	resources     Value
	depth         int
	fonts         map[string]*Font
	counters      decodeCounters // per-decode-path glyph counts for this run
	rotatedWarned bool           // WarningRotatedText fired once for this run
}

// appendText decodes a genuine content show-string through the current encoder,
// records it against the decode-path counters, and lays out one Text entry per
// glyph. Interpreter-synthesised separators must use appendSeparator instead.
func (s *contentState) appendText(str string) {
	decoded := s.g.enc.Decode(str)
	s.counters.record(s.g.encSource, decoded)
	s.layoutDecoded(str, decoded)
}

// appendSeparator lays out an interpreter-synthesised separator (a TJ kerning
// space, or the newline appended after a TJ array) WITHOUT counting it: those
// runes are layout, not content. The plain-text sink emits no such separators,
// so counting them here would make the decode-path counters disagree by entry
// point — the exact discrepancy this framework exists to prevent.
func (s *contentState) appendSeparator(sep string) {
	s.layoutDecoded(sep, s.g.enc.Decode(sep))
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
func (s *contentState) layoutDecoded(str, decoded string) {
	n := 0
	for _, ch := range decoded {
		var w0 float64
		if n < len(str) {
			w0 = s.g.Tf.Width(int(str[n]))
		}
		n++
		f := s.g.Tf.BaseFont()
		if i := strings.Index(f, "+"); i >= 0 {
			f = f[i+1:]
		}
		Trm := matrix{{s.g.Tfs * s.g.Th, 0, 0}, {0, s.g.Tfs, 0}, {0, s.g.Trise, 1}}.mul(s.g.Tm).mul(s.g.CTM)
		if !s.rotatedWarned && Trm[0][1] != 0 {
			s.rotatedWarned = true
			s.p.V.warn(WarningRotatedText, "rotated text matrix (non-horizontal baseline)")
		}
		s.text = append(s.text, Text{f, Trm[0][0], Trm[2][0], Trm[2][1], w0 / 1000 * Trm[0][0], string(ch)})
		tx := w0/1000*s.g.Tfs + s.g.Tc
		tx *= s.g.Th
		s.g.Tm = matrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(s.g.Tm)
	}
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
		s.rect = append(s.rect, Rect{Point{x, y}, Point{x + w, y + h}})
	case "q":
		s.gstack = append(s.gstack, s.g)
	case "Q":
		if n := len(s.gstack) - 1; n >= 0 {
			s.g = s.gstack[n]
			s.gstack = s.gstack[:n]
		}
		// f, g, l, m, cs, scn, gs: no-op
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
			tx := -x.Float64() / 1000 * s.g.Tfs * s.g.Th
			s.g.Tm = matrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(s.g.Tm)
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

// popArgs drains the stack into a slice, preserving argument order.
func popArgs(stk *Stack) []Value {
	n := stk.Len()
	args := make([]Value, n)
	for i := n - 1; i >= 0; i-- {
		args[i] = stk.Pop()
	}
	return args
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
	args := popArgs(stk)
	switch op {
	case "cm", "re", "q", "Q", "f", "g", "l", "m", "cs", "scn", "gs":
		s.handleGraphics(op, args)
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
// For a page with no Contents stream, Content returns a zero Content whose Text and Rect slices are nil.
// If the content stream causes a panic (e.g. malformed operator arguments),
// the defer/recover returns whatever text and rectangles were collected before
// the crash rather than propagating the panic to the caller.
func (p Page) Content() (out Content) {
	var s *contentState
	defer func() {
		if recover() != nil && s != nil {
			out = Content{s.text, s.rect}
		}
	}()
	s = newContentState(p)
	if s == nil {
		return
	}
	Interpret(p.V.Key("Contents"), s.interpret)
	return Content{s.text, s.rect}
}

func newContentState(p Page) *contentState {
	if p.V.IsNull() || p.V.Key("Contents").Kind() == Null {
		return nil
	}
	return &contentState{
		g:         gstate{Th: 1, CTM: ident, enc: &nopEncoder{}},
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
