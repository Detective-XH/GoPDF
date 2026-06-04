// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// PDF object graph resolution: indirect references and object streams.

package pdf

import (
	"fmt"
	"io"
)

// maxLinkDepth caps the length of any PDF-controlled link chain or recursion the
// parser walks after open time: the page-tree /Kids descent, /Parent inheritance
// chains, ObjStm /Extends chains, and the outline /First+/Next tree. A malformed
// PDF can make these cyclic or arbitrarily deep; the cap converts an otherwise
// unbounded loop (or stack-overflowing recursion) into a bounded, best-effort
// result. Mirrors xobjMaxDepth (gstate.go), which caps Form XObject recursion.
const maxLinkDepth = 1024

// objStmHeader validates that strm is a well-formed ObjStm and returns
// the entry count n and the byte offset of the first object body (first).
func objStmHeader(strm Value) (n int, first int64) {
	if strm.Kind() != Stream {
		panic("not a stream")
	}
	if strm.Key("Type").Name() != "ObjStm" {
		panic("not an object stream")
	}
	n = int(strm.Key("N").Int64())
	first = strm.Key("First").Int64()
	if first == 0 {
		panic("missing First")
	}
	return n, first
}

// scanObjStmIndex reads the (id, offset) index pairs from b, and if one
// matches ptr.id it seeks to first+offset and returns the decoded object.
// ok is false when the id is not found in this stream's index.
func scanObjStmIndex(b *buffer, n int, first int64, ptr objptr) (obj object, ok bool) {
	for range n {
		// The index section is [0, first); the bytes at and after /First are
		// object bodies, not (id, offset) pairs. Bound the scan by /First (and by
		// EOF, in case /First runs past the stream): an attacker-controlled /N
		// that over-claims the entry count must not make the scan tokenize body
		// bytes — that would cost O(stream size) per hop and could false-match an
		// id from body content, seeking to an attacker-chosen offset.
		if b.eof || b.readOffset() >= first {
			break
		}
		id, _ := b.readToken().(int64)
		off, _ := b.readToken().(int64)
		// A pair whose tokens ran past /First straddled the index/body boundary
		// (the index lands just short of /First because of the trailing
		// separator); it is body content, so discard it and stop before matching.
		if b.readOffset() > first {
			break
		}
		if uint32(id) == ptr.id {
			b.seekForward(first + off)
			return b.readObject(), true
		}
	}
	return nil, false
}

// resolveInStream locates ptr inside an ObjStm (object stream) by scanning
// the index entries, following Extends chains as needed.
func (r *Reader) resolveInStream(parent objptr, ptr objptr, xr xref) any {
	strm := r.resolve(parent, xr.stream)
	visited := make(map[objptr]bool)
	for depth := 0; depth < maxLinkDepth; depth++ {
		// A cyclic /Extends chain would otherwise re-scan the same ObjStm on every
		// hop up to maxLinkDepth times (each scan is O(/First)); stop as soon as it
		// revisits a stream. ObjStms are always indirect, so strm.ptr is a stable
		// identity. The depth cap still backstops a long acyclic chain.
		if visited[strm.ptr] {
			return nil
		}
		visited[strm.ptr] = true
		n, first := objStmHeader(strm)
		b := newBuffer(strm.Reader(), 0)
		b.allowEOF = true
		if obj, ok := scanObjStmIndex(b, n, first, ptr); ok {
			return obj
		}
		ext := strm.Key("Extends")
		if ext.Kind() != Stream {
			panic("cannot find object in stream")
		}
		strm = ext
	}
	// The /Extends chain exceeded maxLinkDepth (pathologically deep): degrade to a
	// null object rather than continuing.
	return nil
}

// loadDirectObject reads the object at xr.offset from the cross-reference
// table, validates the objdef header, and returns the payload object.
func (r *Reader) loadDirectObject(ptr objptr, xr xref) object {
	b := newBuffer(io.NewSectionReader(r.f, xr.offset, r.end-xr.offset), xr.offset)
	b.key = r.key
	b.useAES = r.useAES
	b.aes256 = r.aes256
	obj := b.readObject()
	def, ok := obj.(objdef)
	if !ok {
		panic(fmt.Errorf("loading %v: found %T instead of objdef", ptr, obj))
	}
	if def.ptr != ptr {
		panic(fmt.Errorf("loading %v: found %v", ptr, def.ptr))
	}
	return def.obj
}

func (r *Reader) resolve(parent objptr, x any) (v Value) {
	if ptr, ok := x.(objptr); ok {
		// B-nil: an indirect reference cannot be resolved without a Reader. A
		// composite Value popped from an Interpret callback carries a nil Reader
		// (ps.go pushes operands as Value{nil, ...}); resolving an indirect element
		// through it would nil-deref. Direct (non-objptr) values fall through to
		// the switch below and are returned even with a nil Reader, so a TJ array
		// and other inline operands stay readable.
		if r == nil || ptr.id >= uint32(len(r.xref)) {
			return Value{}
		}
		xr := r.xref[ptr.id]
		if xr.ptr != ptr || !xr.inStream && xr.offset == 0 {
			return Value{}
		}
		// The reference is structurally valid, but the object body it points at
		// may still be malformed. On the open path the parser stays strict: a
		// body-parse panic propagates to NewReaderEncrypted's recover and fails
		// the load exactly as before (r.opening is true, so no recover is armed
		// here). After open, a public getter must not crash on a crafted file, so
		// degrade any loadDirectObject / resolveInStream / default-case panic to a
		// null Value. This single boundary hardens the whole getter surface.
		if !r.opening {
			defer func() {
				if recover() != nil {
					v = Value{}
				}
			}()
		}
		if xr.inStream {
			x = r.resolveInStream(parent, ptr, xr)
		} else {
			x = r.loadDirectObject(ptr, xr)
		}
		parent = ptr
	}

	switch x := x.(type) {
	case nil, bool, int64, float64, name, dict, array, stream:
		return Value{r, parent, x}
	case string:
		return Value{r, parent, x}
	default:
		panic(fmt.Errorf("unexpected value type %T in resolve", x))
	}
}
