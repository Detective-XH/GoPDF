// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for object.go: fmtString, fmtArray, objfmt, consumeStreamNewline,
// readKeywordObject, readDict, and maybeDecryptToken.
//
// All identifiers are prefixed with "object" to avoid collisions with other
// test files in package pdf.

package pdf

import (
	"bytes"
	"strconv"
	"testing"
)

// objectMakeBuffer creates a buffer from a byte slice for use in object tests.
// allowEOF is always true so that buffers exhausted during parsing never panic.
func objectMakeBuffer(data []byte) *buffer {
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	return b
}

// objectPanicToError calls f and returns the recovered value (if any).
// Returns nil when f completes without panicking.
func objectPanicToError(f func()) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	f()
	return nil
}

// ---------------------------------------------------------------------------
// TestObjectFmtString
// ---------------------------------------------------------------------------

func TestObjectFmtString(t *testing.T) {
	t.Run("pure ASCII is isPDFDocEncoded", func(t *testing.T) {
		// All ASCII bytes 0x20–0x7e are valid pdfDocEncoding code points and
		// map to themselves, so pdfDocDecode is a no-op and fmtString returns
		// strconv.Quote of the original string.
		input := "Hello"
		want := strconv.Quote("Hello")
		got := fmtString(input)
		if got != want {
			t.Errorf("fmtString(%q) = %q; want %q", input, got, want)
		}
	})

	t.Run("UTF-16 BOM prefix", func(t *testing.T) {
		// "\xfe\xff\x00H\x00i" — BOM followed by big-endian "Hi".
		// isUTF16 fires and fmtString returns strconv.Quote(utf16Decode("Hi-BE")).
		input := "\xfe\xff\x00H\x00i"
		want := strconv.Quote("Hi")
		got := fmtString(input)
		if got != want {
			t.Errorf("fmtString(UTF16 BOM+Hi) = %q; want %q", got, want)
		}
	})

	t.Run("UTF-16 BOM with CJK codepoint", func(t *testing.T) {
		// "\xfe\xff\x4e\x2d" — BOM + U+4E2D (中).
		// Distinct enough to fail if the UTF-16 branch accidentally took the
		// isPDFDocEncoded or fallback path.
		input := "\xfe\xff\x4e\x2d"
		want := strconv.Quote("中")
		got := fmtString(input)
		if got != want {
			t.Errorf("fmtString(UTF16 BOM+CJK) = %q; want %q", got, want)
		}
	})

	t.Run("fallback raw string with noRune byte", func(t *testing.T) {
		// Byte 0x7f maps to noRune in pdfDocEncoding (row 0x70, last entry),
		// so isPDFDocEncoded returns false.  No BOM, so isUTF16 also false.
		// fmtString must take the fallback strconv.Quote(s) path.
		input := "AB\x7fCD"
		want := strconv.Quote("AB\x7fCD")
		got := fmtString(input)
		if got != want {
			t.Errorf("fmtString(fallback) = %q; want %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// TestObjectFmtArray
// ---------------------------------------------------------------------------

func TestObjectFmtArray(t *testing.T) {
	t.Run("empty array", func(t *testing.T) {
		got := fmtArray(array{})
		if got != "[]" {
			t.Errorf("fmtArray(empty) = %q; want %q", got, "[]")
		}
	})

	t.Run("single name element", func(t *testing.T) {
		got := fmtArray(array{name("X")})
		if got != "[/X]" {
			t.Errorf("fmtArray([X]) = %q; want %q", got, "[/X]")
		}
	})

	t.Run("multiple mixed elements", func(t *testing.T) {
		got := fmtArray(array{int64(42), name("Foo")})
		if got != "[42 /Foo]" {
			t.Errorf("fmtArray([42 /Foo]) = %q; want %q", got, "[42 /Foo]")
		}
	})
}

// ---------------------------------------------------------------------------
// TestObjectObjfmt — one assertion per type-switch case
// ---------------------------------------------------------------------------

func TestObjectObjfmt(t *testing.T) {
	t.Run("string case", func(t *testing.T) {
		// ASCII string goes through isPDFDocEncoded → quoted.
		want := strconv.Quote("hi")
		got := objfmt("hi")
		if got != want {
			t.Errorf("objfmt(string) = %q; want %q", got, want)
		}
	})

	t.Run("name case", func(t *testing.T) {
		got := objfmt(name("Type"))
		if got != "/Type" {
			t.Errorf("objfmt(name) = %q; want %q", got, "/Type")
		}
	})

	t.Run("dict case", func(t *testing.T) {
		d := dict{name("K"): int64(1)}
		got := objfmt(d)
		if got != "<</K 1>>" {
			t.Errorf("objfmt(dict) = %q; want %q", got, "<</K 1>>")
		}
	})

	t.Run("array case", func(t *testing.T) {
		got := objfmt(array{int64(1), int64(2)})
		if got != "[1 2]" {
			t.Errorf("objfmt(array) = %q; want %q", got, "[1 2]")
		}
	})

	t.Run("stream case", func(t *testing.T) {
		s := stream{hdr: dict{}, ptr: objptr{}, offset: 0}
		got := objfmt(s)
		if got != "<<>>@0" {
			t.Errorf("objfmt(stream) = %q; want %q", got, "<<>>@0")
		}
	})

	t.Run("objptr case", func(t *testing.T) {
		got := objfmt(objptr{id: 3, gen: 0})
		if got != "3 0 R" {
			t.Errorf("objfmt(objptr) = %q; want %q", got, "3 0 R")
		}
	})

	t.Run("objdef case", func(t *testing.T) {
		got := objfmt(objdef{ptr: objptr{id: 5, gen: 1}, obj: int64(99)})
		if got != "{5 1 obj}99" {
			t.Errorf("objfmt(objdef) = %q; want %q", got, "{5 1 obj}99")
		}
	})

	t.Run("default case int64", func(t *testing.T) {
		got := objfmt(int64(42))
		if got != "42" {
			t.Errorf("objfmt(int64) = %q; want %q", got, "42")
		}
	})

	t.Run("default case bool", func(t *testing.T) {
		got := objfmt(true)
		if got != "true" {
			t.Errorf("objfmt(bool) = %q; want %q", got, "true")
		}
	})
}

// ---------------------------------------------------------------------------
// TestObjectConsumeStreamNewline
// ---------------------------------------------------------------------------

func TestObjectConsumeStreamNewline(t *testing.T) {
	t.Run("CR+LF consumed", func(t *testing.T) {
		b := objectMakeBuffer([]byte("\r\n"))
		err := objectPanicToError(func() { b.consumeStreamNewline() })
		if err != nil {
			t.Errorf("CR+LF: unexpected panic: %v", err)
		}
		// Buffer should be fully consumed — next readByte returns synthetic '\n'.
	})

	t.Run("LF alone consumed", func(t *testing.T) {
		b := objectMakeBuffer([]byte("\n"))
		err := objectPanicToError(func() { b.consumeStreamNewline() })
		if err != nil {
			t.Errorf("LF alone: unexpected panic: %v", err)
		}
	})

	t.Run("CR alone unreads non-LF byte", func(t *testing.T) {
		// "\rX" — CR is consumed, X is read then unread.
		// After consumeStreamNewline, the next byte must still be 'X'.
		b := objectMakeBuffer([]byte("\rX"))
		err := objectPanicToError(func() { b.consumeStreamNewline() })
		if err != nil {
			t.Errorf("CR alone: unexpected panic: %v", err)
		}
		next := b.readByte()
		if next != 'X' {
			t.Errorf("CR alone: expected next byte 'X', got %q", next)
		}
	})

	t.Run("other byte causes panic", func(t *testing.T) {
		b := objectMakeBuffer([]byte("X"))
		err := objectPanicToError(func() { b.consumeStreamNewline() })
		if err == nil {
			t.Error("expected panic for non-newline byte, got none")
		}
	})
}

// ---------------------------------------------------------------------------
// TestObjectReadKeywordObject
// ---------------------------------------------------------------------------

func TestObjectReadKeywordObject(t *testing.T) {
	t.Run("null keyword returns nil", func(t *testing.T) {
		b := objectMakeBuffer([]byte(""))
		got := b.readKeywordObject(keyword("null"))
		if got != nil {
			t.Errorf("readKeywordObject(null) = %v; want nil", got)
		}
	})

	t.Run(">> sentinel returns nil", func(t *testing.T) {
		b := objectMakeBuffer([]byte(""))
		got := b.readKeywordObject(keyword(">>"))
		if got != nil {
			t.Errorf("readKeywordObject(>>) = %v; want nil", got)
		}
	})

	t.Run("] sentinel returns nil", func(t *testing.T) {
		b := objectMakeBuffer([]byte(""))
		got := b.readKeywordObject(keyword("]"))
		if got != nil {
			t.Errorf("readKeywordObject(]) = %v; want nil", got)
		}
	})

	t.Run("<< keyword reads empty dict", func(t *testing.T) {
		// Feed just ">>" so readDict sees the closing delimiter.
		b := objectMakeBuffer([]byte(">>"))
		got := b.readKeywordObject(keyword("<<"))
		d, ok := got.(dict)
		if !ok {
			t.Fatalf("readKeywordObject(<<): want dict, got %T", got)
		}
		if len(d) != 0 {
			t.Errorf("readKeywordObject(<<): want empty dict, got %v", d)
		}
	})

	t.Run("[ keyword reads empty array", func(t *testing.T) {
		// Feed just "]" so readArray sees the closing delimiter.
		b := objectMakeBuffer([]byte("]"))
		got := b.readKeywordObject(keyword("["))
		_, ok := got.(array)
		if !ok {
			t.Fatalf("readKeywordObject([): want array, got %T", got)
		}
	})

	t.Run("unexpected keyword panics", func(t *testing.T) {
		b := objectMakeBuffer([]byte(""))
		err := objectPanicToError(func() {
			b.readKeywordObject(keyword("bogus"))
		})
		if err == nil {
			t.Error("expected panic for unexpected keyword, got none")
		}
	})
}

// ---------------------------------------------------------------------------
// TestObjectReadDict
// ---------------------------------------------------------------------------

func TestObjectReadDict(t *testing.T) {
	t.Run("normal key-value dict", func(t *testing.T) {
		// readDict is called AFTER the leading "<<" token has already been
		// consumed, so the input starts with the dict body.
		b := objectMakeBuffer([]byte("/Foo 42 >>"))
		obj := b.readDict()
		d, ok := obj.(dict)
		if !ok {
			t.Fatalf("readDict: want dict, got %T", obj)
		}
		v, exists := d[name("Foo")]
		if !exists {
			t.Fatal("readDict: key Foo not found")
		}
		if v != int64(42) {
			t.Errorf("readDict: Foo = %v; want 42", v)
		}
	})

	t.Run("EOF aborts without panic", func(t *testing.T) {
		// Input ends before ">>"; readDict should return the partial dict.
		b := objectMakeBuffer([]byte("/A 1 /B 2"))
		obj := b.readDict()
		d, ok := obj.(dict)
		if !ok {
			t.Fatalf("readDict (EOF): want dict, got %T", obj)
		}
		if len(d) == 0 {
			t.Error("readDict (EOF): expected at least one key parsed")
		}
	})

	t.Run("non-name key causes panic", func(t *testing.T) {
		// "42 >>" — integer key, not a name; readDict prints DEBUG and panics.
		b := objectMakeBuffer([]byte("42 >>"))
		err := objectPanicToError(func() {
			b.readDict()
		})
		if err == nil {
			t.Error("readDict (non-name key): expected panic, got none")
		}
	})
}

// ---------------------------------------------------------------------------
// TestObjectMaybeDecryptToken
// ---------------------------------------------------------------------------

func TestObjectMaybeDecryptToken(t *testing.T) {
	t.Run("nil key passes string through unchanged", func(t *testing.T) {
		b := objectMakeBuffer([]byte(""))
		// b.key is nil by default — no decryption.
		tok := object("secret")
		got := b.maybeDecryptToken(tok)
		if got != tok {
			t.Errorf("maybeDecryptToken (nil key): got %v; want %v", got, tok)
		}
	})

	t.Run("non-string token passes through unchanged regardless of key", func(t *testing.T) {
		b := objectMakeBuffer([]byte(""))
		b.key = []byte{0x01, 0x02}
		b.strMode = modeRC4
		b.objptr = objptr{id: 1, gen: 0}
		tok := object(name("MyName"))
		got := b.maybeDecryptToken(tok)
		if got != tok {
			t.Errorf("maybeDecryptToken (name token): got %v; want %v", got, tok)
		}
	})

	t.Run("string token with nil key passes through unchanged", func(t *testing.T) {
		b := objectMakeBuffer([]byte(""))
		// key is nil; even though tok is a string it must not be decrypted.
		tok := object("plaintext")
		got := b.maybeDecryptToken(tok)
		if got != tok {
			t.Errorf("maybeDecryptToken (string, nil key): got %v; want %v", got, tok)
		}
	})
}

// ---------------------------------------------------------------------------
// TestObjectMaybeDecryptTokenZeroObjptr
// ---------------------------------------------------------------------------

// TestObjectMaybeDecryptTokenZeroObjptr verifies that a string token with a
// non-nil key and active string mode but objptr.id == 0 is returned unchanged
// (the compound guard in maybeDecryptToken requires all conditions).
func TestObjectMaybeDecryptTokenZeroObjptr(t *testing.T) {
	b := objectMakeBuffer([]byte(""))
	b.key = []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	b.strMode = modeRC4
	b.objptr = objptr{id: 0, gen: 0} // zero id — decryption is skipped
	tok := object("plaintext")
	got := b.maybeDecryptToken(tok)
	if got != tok {
		t.Errorf("maybeDecryptToken (zero objptr.id): expected unchanged tok, got different")
	}
}

// ---------------------------------------------------------------------------
// TestObjectMaybeDecryptTokenRC4
// ---------------------------------------------------------------------------

// TestObjectMaybeDecryptTokenRC4 verifies the full decryption path: a string
// token with a non-nil key and non-zero objptr.id triggers decryptString.
// RC4 is symmetric, so applying maybeDecryptToken twice with the same key and
// objptr restores the original plaintext.
func TestObjectMaybeDecryptTokenRC4(t *testing.T) {
	b := objectMakeBuffer([]byte(""))
	b.key = []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	b.strMode = modeRC4
	b.objptr = objptr{id: 1, gen: 0} // non-zero id — decryption executes

	original := "hello"
	// First pass: RC4-encrypt (RC4 encrypt == decrypt).
	encrypted, ok := b.maybeDecryptToken(object(original)).(string)
	if !ok {
		t.Fatal("maybeDecryptToken RC4: result is not a string")
	}
	// Second pass: applying RC4 again with the same key and ptr restores the original.
	decrypted := b.maybeDecryptToken(object(encrypted))
	if decrypted != object(original) {
		t.Errorf("maybeDecryptToken RC4 round-trip: got %q, want %q", decrypted, original)
	}
}

// malformedDictInner is the inner content of the malformed /CIDSystemInfo
// dict literal: a stray bare "def" keyword in key-position after the first
// key-value pair. This is what readDict sees after the leading "<<" is
// already consumed. Both the direct unit test and the fixture builder
// reference this constant to guarantee they exercise the same bytes.
const malformedDictInner = `/Registry (Adobe) def /Ordering (UCS) /Supplement 0 >>`

// TestReadDictMalformedCMapKeyPanics verifies that the stray "def" keyword
// in key-position inside a CMap /CIDSystemInfo dict literal causes readDict
// to panic (via buffer.errorf). This is the low-level mechanism that
// cachedReadCmap's recover catches. The constant malformedDictInner is shared
// with buildMalformedToUnicodePDF so both tests exercise the same bytes.
func TestReadDictMalformedCMapKeyPanics(t *testing.T) {
	// readDict is called after "<<" is consumed; feed only the inner content.
	b := objectMakeBuffer([]byte(malformedDictInner))
	err := objectPanicToError(func() { b.readDict() })
	if err == nil {
		t.Error("readDict on malformed CMap dict inner bytes: expected panic, got none")
	}
}
