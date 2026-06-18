// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Buffered I/O primitives for reading raw bytes from a PDF stream.

package pdf

import (
	"fmt"
	"io"
	"runtime"
)

// A buffer holds buffered input bytes from the PDF file.
type buffer struct {
	r           io.Reader // source of data
	buf         []byte    // buffered data
	pos         int       // read index in buf
	offset      int64     // offset at end of buf; aka offset of next read
	tmp         []byte    // scratch space for accumulating token
	unread      []token   // queue of read but then unread tokens
	allowEOF    bool
	allowObjptr bool
	allowStream bool
	eof         bool
	key         []byte
	strMode     cipherMode // strings are the only thing a buffer decrypts
	objptr      objptr
	depth       int
}

// newBuffer returns a new buffer reading from r at the given offset.
func newBuffer(r io.Reader, offset int64) *buffer {
	return &buffer{
		r:           r,
		offset:      offset,
		buf:         make([]byte, 0, 4096),
		allowObjptr: true,
		allowStream: true,
	}
}

func (b *buffer) readByte() byte {
	if b.pos >= len(b.buf) {
		b.reload()
		if b.pos >= len(b.buf) {
			return '\n'
		}
	}
	c := b.buf[b.pos]
	b.pos++
	return c
}

func (b *buffer) errorf(format string, args ...any) {
	panic(fmt.Errorf(format, args...))
}

// isIntentionalParserPanic reports whether a recovered panic value is the
// lexer's intentional malformed-input signal — a plain error raised by
// buffer.errorf — rather than a genuine fault that must propagate. Order
// matters: runtime.Error (nil deref, index-out-of-range, bad type assertion,
// integer divide) embeds error, so it is checked first and classified as a
// real fault; any non-error panic value is likewise a real fault. A parser
// boundary that recovers (e.g. cachedReadCmap, or the fuzz shim) uses this to
// swallow ONLY malformed-input panics and re-panic everything else, so an
// internal bug still fails loudly instead of masquerading as malformed input.
func isIntentionalParserPanic(rec any) bool {
	if _, ok := rec.(runtime.Error); ok {
		return false
	}
	_, ok := rec.(error)
	return ok
}

func (b *buffer) reload() bool {
	n := cap(b.buf) - int(b.offset%int64(cap(b.buf)))
	n, err := b.r.Read(b.buf[:n])
	if n == 0 && err != nil {
		b.buf = b.buf[:0]
		b.pos = 0
		if b.allowEOF && err == io.EOF {
			b.eof = true
			return false
		}
		b.errorf("malformed PDF: reading at offset %d: %v", b.offset, err)
		return false
	}
	b.offset += int64(n)
	b.buf = b.buf[:n]
	b.pos = 0
	return true
}

func (b *buffer) seekForward(offset int64) {
	for b.offset < offset {
		if !b.reload() {
			return
		}
	}
	b.pos = len(b.buf) - int(b.offset-offset)
}

func (b *buffer) readOffset() int64 {
	return b.offset - int64(len(b.buf)) + int64(b.pos)
}

func (b *buffer) unreadByte() {
	if b.pos > 0 {
		b.pos--
	}
}
