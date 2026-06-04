// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"errors"
	"fmt"
)

// plainTextState holds mutable state for the GetPlainText Interpret callback.
type plainTextState struct {
	enc       TextEncoding
	fonts     map[string]*Font
	buf       bytes.Buffer
	resources Value
	depth     int
}

func (s *plainTextState) showEncoded(str string) {
	for _, ch := range s.enc.Decode(str) {
		_, err := s.buf.WriteRune(ch)
		if err != nil {
			panic(err)
		}
	}
}

func (s *plainTextState) handlePlainTf(args []Value) {
	if len(args) != 2 {
		panic("bad TL")
	}
	if font, ok := s.fonts[args[0].Name()]; ok {
		s.enc = font.cachedEncoder()
	} else {
		s.enc = &nopEncoder{}
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
		s.showEncoded("\n")
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
		f := Font{xobjRes.Key("Font").Key(fn), nil}
		sub.fonts[fn] = &f
	}
	Interpret(xobj, sub.interpretPlain)
	s.buf.WriteString(sub.buf.String())
}

func (s *plainTextState) interpretPlain(stk *Stack, op string) {
	n := stk.Len()
	args := make([]Value, n)
	for i := n - 1; i >= 0; i-- {
		args[i] = stk.Pop()
	}
	switch op {
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
func (p Page) GetPlainText(fonts map[string]*Font) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = ""
			err = errors.New(fmt.Sprint(r))
		}
	}()

	if p.V.IsNull() || p.V.Key("Contents").Kind() == Null {
		return "", nil
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
	s := &plainTextState{enc: &nopEncoder{}, fonts: local, resources: p.Resources()}
	Interpret(p.V.Key("Contents"), s.interpretPlain)
	return s.buf.String(), nil
}
