// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for buf.go — buffered I/O primitives.
// All package-level identifiers carry the "buf" prefix to avoid collisions
// in the package pdf namespace.

package pdf

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// bufChunkReader is an io.Reader that returns at most chunkSize bytes per Read
// call, allowing tests to force multiple reload() iterations in seekForward.
type bufChunkReader struct {
	data      []byte
	pos       int
	chunkSize int
}

func (r *bufChunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := min(r.chunkSize, len(p))
	if r.pos+n > len(r.data) {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

// bufErrReader is an io.Reader that immediately returns a non-EOF error.
type bufErrReader struct {
	err error
}

func (r *bufErrReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

// bufMustPanic calls fn and asserts that it panics.
func bufMustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected a panic but none occurred")
		}
	}()
	fn()
}

// bufMakeChunkBuffer creates a buffer backed by a bufChunkReader with the
// given data and chunk size. offset is set to 0.
func bufMakeChunkBuffer(data []byte, chunkSize int) *buffer {
	r := &bufChunkReader{data: data, chunkSize: chunkSize}
	return newBuffer(r, 0)
}

// --------------------------------------------------------------------------
// TestBufReadByteBasic verifies that readByte returns the correct bytes and
// triggers a reload when the buffer is exhausted.
// --------------------------------------------------------------------------

func TestBufReadByteBasic(t *testing.T) {
	data := []byte("Hello")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	for i, want := range data {
		got := b.readByte()
		if got != want {
			t.Errorf("readByte()[%d] = %q; want %q", i, got, want)
		}
	}
}

// --------------------------------------------------------------------------
// TestBufReadByteEOF verifies that readByte returns '\n' indefinitely after
// EOF when allowEOF=true, and does not panic.
// --------------------------------------------------------------------------

func TestBufReadByteEOF(t *testing.T) {
	b := newBuffer(bytes.NewReader(nil), 0)
	b.allowEOF = true

	for i := range 5 {
		got := b.readByte()
		if got != '\n' {
			t.Errorf("readByte() after EOF iteration %d = %q; want '\\n'", i, got)
		}
	}
}

// --------------------------------------------------------------------------
// TestBufReloadPanic verifies that reload (and by extension readByte) panics
// when allowEOF=false and the reader returns a non-EOF error.
// --------------------------------------------------------------------------

func TestBufReloadPanic(t *testing.T) {
	sentinel := errors.New("read error")
	b := newBuffer(&bufErrReader{err: sentinel}, 0)
	// allowEOF defaults to false — reload must panic on any error.

	bufMustPanic(t, func() {
		b.readByte()
	})
}

// --------------------------------------------------------------------------
// TestBufReloadPanicOnEOF verifies that reload panics on io.EOF when
// allowEOF=false (the zero value).
// --------------------------------------------------------------------------

func TestBufReloadPanicOnEOF(t *testing.T) {
	// Empty reader returns (0, io.EOF) immediately.
	b := newBuffer(bytes.NewReader(nil), 0)
	// allowEOF is false by default — EOF must panic.

	bufMustPanic(t, func() {
		b.readByte()
	})
}

// --------------------------------------------------------------------------
// TestBufSeekForward covers the seekForward function.
// --------------------------------------------------------------------------

// TestBufSeekForwardBasic: single-chunk reader, seek then read.
func TestBufSeekForwardBasic(t *testing.T) {
	data := []byte("ABCDEFGHIJ")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	b.seekForward(5)

	got := b.readByte()
	if got != 'F' {
		t.Errorf("readByte() after seekForward(5) = %q; want 'F'", got)
	}
}

// TestBufSeekForwardMultiChunk: forces multiple reload() iterations by using
// a reader that delivers at most 3 bytes per Read call.
func TestBufSeekForwardMultiChunk(t *testing.T) {
	data := []byte("ABCDEFGHIJ") // 10 bytes
	b := bufMakeChunkBuffer(data, 3)
	b.allowEOF = true

	// offset=5 lies in the second or third 3-byte chunk — reload must loop.
	b.seekForward(5)

	got := b.readByte()
	if got != 'F' {
		t.Errorf("readByte() after seekForward(5) with 3-byte chunks = %q; want 'F'", got)
	}
}

// TestBufSeekForwardAlreadySatisfied: when b.offset already exceeds the target
// the loop body is never entered and pos is set from the current buf.
func TestBufSeekForwardAlreadySatisfied(t *testing.T) {
	data := []byte("ABCDEFGHIJ")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	// Consume enough bytes to load all data into the buffer via readByte.
	for range 7 {
		b.readByte()
	}
	// b.offset is now 10 (end of the 10-byte chunk); target=5 < b.offset,
	// so seekForward must skip the loop and set pos via the final assignment.
	b.seekForward(5)

	got := b.readByte()
	if got != 'F' {
		t.Errorf("readByte() after seekForward(5) with pre-loaded buf = %q; want 'F'", got)
	}
}

// TestBufSeekForwardPastEOF: seeking beyond data with allowEOF=true must not
// panic — the loop terminates when reload() returns false.
func TestBufSeekForwardPastEOF(t *testing.T) {
	data := []byte("ABC")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	// No panic expected; just silently stop at EOF.
	b.seekForward(9999)

	if !b.eof {
		t.Error("expected b.eof=true after seeking past EOF")
	}
}

// --------------------------------------------------------------------------
// TestBufReadOffset verifies that readOffset returns the number of bytes
// consumed from the start of the stream.
// --------------------------------------------------------------------------

func TestBufReadOffset(t *testing.T) {
	data := []byte("0123456789")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	// No bytes consumed yet — readOffset should be 0.
	if off := b.readOffset(); off != 0 {
		t.Errorf("readOffset() before any read = %d; want 0", off)
	}

	// Consume 3 bytes.
	for range 3 {
		b.readByte()
	}

	if off := b.readOffset(); off != 3 {
		t.Errorf("readOffset() after 3 readByte calls = %d; want 3", off)
	}
}

// --------------------------------------------------------------------------
// TestBufUnreadByte verifies that unreadByte decrements pos when pos > 0.
// --------------------------------------------------------------------------

func TestBufUnreadByte(t *testing.T) {
	data := []byte("AB")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	first := b.readByte() // 'A', pos becomes 1
	if first != 'A' {
		t.Fatalf("expected 'A', got %q", first)
	}

	b.unreadByte() // pos back to 0

	again := b.readByte() // should re-read 'A'
	if again != 'A' {
		t.Errorf("readByte() after unreadByte = %q; want 'A'", again)
	}
}

// --------------------------------------------------------------------------
// TestBufUnreadByteAtZero verifies that unreadByte is a no-op when pos==0.
// --------------------------------------------------------------------------

func TestBufUnreadByteAtZero(t *testing.T) {
	data := []byte("A")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	// Do not read anything — pos is 0.
	b.unreadByte()

	if b.pos != 0 {
		t.Errorf("pos after unreadByte at zero = %d; want 0", b.pos)
	}
}

// --------------------------------------------------------------------------
// TestBufNewBuffer verifies basic newBuffer construction.
// --------------------------------------------------------------------------

func TestBufNewBuffer(t *testing.T) {
	r := bytes.NewReader([]byte("x"))
	b := newBuffer(r, 42)

	if b.r != r {
		t.Error("newBuffer: r field not set correctly")
	}
	if b.offset != 42 {
		t.Errorf("newBuffer: offset = %d; want 42", b.offset)
	}
	if cap(b.buf) != 4096 {
		t.Errorf("newBuffer: cap(buf) = %d; want 4096", cap(b.buf))
	}
	if !b.allowObjptr {
		t.Error("newBuffer: allowObjptr should be true")
	}
	if !b.allowStream {
		t.Error("newBuffer: allowStream should be true")
	}
}

// TestBufIsIntentionalParserPanic locks the discriminator that parser recover
// boundaries (cachedReadCmap, the fuzz shim) depend on: ONLY the lexer's
// intentional errorf(error) signal may be swallowed. A runtime fault or any
// non-error panic value must be reported as NOT intentional, so the boundary
// re-panics it and a genuine bug fails loudly instead of masquerading as
// malformed input.
func TestBufIsIntentionalParserPanic(t *testing.T) {
	// A plain error (what buffer.errorf raises) is the intentional signal.
	if !isIntentionalParserPanic(errors.New("malformed input")) {
		t.Error("plain error must be classified intentional (swallowed)")
	}

	// A runtime.Error (a real out-of-range fault via indexInto, which survives
	// static analysis) is a genuine bug and must NOT be classified intentional.
	var rec any
	func() {
		defer func() { rec = recover() }()
		_ = indexInto([]int{}, 1)
	}()
	if rec == nil {
		t.Fatal("expected a runtime panic to capture")
	}
	if isIntentionalParserPanic(rec) {
		t.Errorf("runtime.Error (%T) must NOT be classified intentional", rec)
	}

	// A non-error panic value (a bare string) is unexpected and must re-panic.
	if isIntentionalParserPanic("bare string") {
		t.Error("non-error panic value must NOT be classified intentional")
	}
}
