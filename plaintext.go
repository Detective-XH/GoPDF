// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"errors"
	"fmt"
)

// plainTextGState captures the text-encoder portion of the graphics state for
// plainTextState's q/Q save/restore. Only enc and encSource affect text decoding;
// other gstate fields (CTM, colour, …) are irrelevant to GetPlainText.
type plainTextGState struct {
	enc       TextEncoding
	encSource encSource
}

// plainTextState holds mutable state for the GetPlainText Interpret callback.
type plainTextState struct {
	enc       TextEncoding
	encSource encSource // decode-path tag for enc (set with enc on Tf)
	// gstack saves (enc, encSource) on q and restores them on Q, mirroring
	// contentState.gstack. Without it a Tf inside a q…Q block bleeds the inner
	// encoder past the Q operator, causing text shown after Q (relying on the
	// restored outer font) to be decoded through the wrong encoder → U+FFFD.
	gstack    []plainTextGState
	counters  decodeCounters // per-decode-path glyph counts for this run
	fonts     map[string]*Font
	buf       bytes.Buffer
	resources Value
	depth     int
}

// showEncoded decodes a genuine content show-string, records it against the
// decode-path counters, and writes its runes. Interpreter-synthesised separators
// must use writeSeparator so the counters match the content sink.
func (s *plainTextState) showEncoded(str string) {
	decoded := s.enc.Decode(str)
	s.counters.record(s.encSource, decoded)
	s.writeDecoded(decoded)
}

// writeSeparator writes the T* newline without counting it (layout, not content;
// the content sink emits no such separator, so counting it would make the two
// sinks' decode-path counters disagree).
func (s *plainTextState) writeSeparator(sep string) {
	decoded := s.enc.Decode(sep)
	// A 2-byte-codespace CMap can't decode the 1-byte separator and returns
	// noRune; emit the raw separator instead, mirroring content.go:appendSeparator,
	// so a T* line-move doesn't become U+FFFD under a Type0 font.
	if decoded == string(noRune) {
		decoded = sep
	}
	s.writeDecoded(decoded)
}

func (s *plainTextState) writeDecoded(decoded string) {
	for _, ch := range decoded {
		if _, err := s.buf.WriteRune(ch); err != nil {
			panic(err)
		}
	}
}

func (s *plainTextState) handlePlainTf(args []Value) {
	if len(args) != 2 {
		panic("bad Tf")
	}
	if font, ok := s.fonts[args[0].Name()]; ok {
		s.enc, s.encSource = font.cachedEncoder()
	} else {
		s.resources.warn(WarningMissingGlyphMapping, "font resource "+clampDetail(args[0].Name())+" not found in page resources")
		s.enc = &nopEncoder{}
		s.encSource = encSourceUnset
	}
}

// showArray decodes and writes every String element of a TJ array operand.
func (s *plainTextState) showArray(v Value) {
	for i := 0; i < v.Len(); i++ {
		x := v.Index(i)
		if x.Kind() == String {
			s.showEncoded(x.RawString())
		}
	}
}

func (s *plainTextState) handlePlainShow(op string, args []Value) {
	switch op {
	case "BT":
	case "T*":
		s.writeSeparator("\n")
	case "\"":
		if len(args) != 3 {
			panic("bad \" operator")
		}
		args = args[2:] // trim Aw/Ac; leave only the string operand
		fallthrough
	case "'":
		if len(args) != 1 {
			panic("bad ' operator")
		}
		fallthrough
	case "Tj":
		if len(args) != 1 {
			panic("bad Tj operator")
		}
		s.showEncoded(args[0].RawString())
	case "TJ":
		s.showArray(args[0])
	}
}

// handlePlainDo recurses into a Form XObject named by args[0] and appends its text.
func (s *plainTextState) handlePlainDo(args []Value) {
	if s.depth >= xobjMaxDepth || len(args) == 0 {
		return
	}
	xobj := s.resources.Key("XObject").Key(args[0].Name())
	if xobj.Key("Subtype").Name() != "Form" {
		return
	}
	xobjRes := xobj.Key("Resources")
	sub := &plainTextState{enc: &nopEncoder{}, resources: xobjRes, depth: s.depth + 1}
	sub.fonts = make(map[string]*Font)
	for _, fn := range xobjRes.Key("Font").Keys() {
		f := Font{V: xobjRes.Key("Font").Key(fn)}
		sub.fonts[fn] = &f
	}
	// Merge the sub-state's decode counts even if the form panics mid-stream, so
	// the glyphs it decoded before a malformed operator are not lost — that is
	// exactly the degraded-input evidence the counters exist to surface (mirrors
	// imageScanState.interpretXObject). The recover()==nil normal path falls
	// through to the single explicit merge below, so counts are never doubled.
	defer func() {
		if rec := recover(); rec != nil {
			s.counters.merge(sub.counters)
			panic(rec)
		}
	}()
	Interpret(xobj, sub.interpretPlain)
	s.buf.WriteString(sub.buf.String())
	s.counters.merge(sub.counters)
}

func (s *plainTextState) interpretPlain(stk *Stack, op string) {
	n := stk.Len()
	args := make([]Value, n)
	for i := n - 1; i >= 0; i-- {
		args[i] = stk.Pop()
	}
	switch op {
	case "q":
		s.gstack = append(s.gstack, plainTextGState{s.enc, s.encSource})
	case "Q":
		if n := len(s.gstack) - 1; n >= 0 {
			saved := s.gstack[n]
			s.gstack = s.gstack[:n]
			s.enc, s.encSource = saved.enc, saved.encSource
		}
		// A stray Q with an empty stack is a no-op (leave enc/encSource unchanged).
	case "Tf":
		s.handlePlainTf(args)
	case "BT", "T*", "\"", "'", "Tj", "TJ":
		s.handlePlainShow(op, args)
	case "Do":
		s.handlePlainDo(args)
	}
}

// GetPlainText returns the page's all text without format.
// The returned string is verbatim UTF-8 with no escaping applied; callers must
// escape it at their output sink before embedding in HTML, shell commands, or
// any other context-sensitive environment.
// fonts can be passed in (to improve parsing performance) or left nil. A
// passed-in map is treated read-only: its Font values are copied internally
// before encoders are cached, so the same map is safe to share across calls.
// GetPlainText is safe to call concurrently on the same Page and does not mutate Page or Reader state.
func (p Page) GetPlainText(fonts map[string]*Font) (result string, err error) {
	result, _, err = p.plainTextAndCounters(fonts)
	return result, err
}

// plainTextAndCounters runs the plain-text interpreter once, returning both the
// extracted text and the per-decode-path counters accumulated at the showEncoded
// sink. GetPlainText wraps it (text only); decodeCountersFromPlainText wraps it
// (counters only). Sharing one pass keeps the two surfaces byte-consistent.
func (p Page) plainTextAndCounters(fonts map[string]*Font) (result string, counters decodeCounters, err error) {
	var s *plainTextState
	defer func() {
		if r := recover(); r != nil {
			// GetPlainText's contract returns "" text on a panic, but the decode
			// counters keep whatever was tallied before it (including a panicking
			// Form's pre-panic glyphs, merged by handlePlainDo) so the content and
			// plain-text sinks stay consistent on degraded input instead of one
			// zeroing while the other keeps a partial count.
			result = ""
			if s != nil {
				counters = s.counters
			}
			err = errors.New(fmt.Sprint(r))
		}
	}()

	if p.V.IsNull() || p.V.Key("Contents").Kind() == Null {
		return "", decodeCounters{}, nil
	}
	// Build a local map of fresh *Font so cachedEncoder memoizes on our own
	// copies; never write to a caller-supplied map, which may be shared across
	// goroutines. Missing fonts fall back to nopEncoder, exactly as before.
	local := make(map[string]*Font, len(fonts))
	if fonts == nil {
		for _, name := range p.Fonts() {
			f := p.Font(name)
			local[name] = &f
		}
	} else {
		for name, f := range fonts {
			cp := *f
			local[name] = &cp
		}
	}
	s = &plainTextState{enc: &nopEncoder{}, fonts: local, resources: p.Resources()}
	Interpret(p.V.Key("Contents"), s.interpretPlain)
	return s.buf.String(), s.counters, nil
}

// decodeCountersFromPlainText runs the plain-text interpreter (the GetPlainText
// sink) and returns its per-decode-path counters. Internal: the plain-text half
// of the cross-sink agreement check and the canonical source for the per-page
// extraction ratios (it shares classifyPageSignal's strict text authority).
func (p Page) decodeCountersFromPlainText() (decodeCounters, error) {
	_, counters, err := p.plainTextAndCounters(nil)
	return counters, err
}
