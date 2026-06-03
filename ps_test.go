// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// ps_test.go — unit tests for ps.go internals.
//
// Coverage targets:
//   - TestPsCurrentdict           psCurrentdict: normal push and empty-dicts panic
//   - TestPsExecDefNonNameKey     execDef: non-name key silent-skip path
//   - TestPsBeginNonDictPanic     psBegin: non-dict panic path
//   - TestPsEndEmptyPanic         psEnd: empty-dicts panic path
//   - TestPsOpenInterpBufferArray openInterpBuffer: Array path (io.MultiReader)
//   - TestPsEiKeywordTerminatesEOF eiKeywordTerminates: EOF → true
//   - TestPsSkipInlineImage       skipInlineImage: normal EI-found path
//   - TestPsExecPS                execPS: all keyword branches + unknown → false
//   - TestPsInterpretWithStack    Interpret integration: values pushed, do called
//
// S3 self-containment: all helpers are defined in this file only.
// Identifier prefix: ps* (avoids collisions in package pdf namespace).

package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// psMakeStack returns an empty Stack ready for use in tests.
func psMakeStack() *Stack {
	return &Stack{}
}

// psMakeIntValue wraps an int64 as a Value with no reader.
func psMakeIntValue(n int64) Value {
	return Value{nil, objptr{}, n}
}

// psMakeNameValue wraps a name as a Value with no reader.
func psMakeNameValue(n string) Value {
	return Value{nil, objptr{}, name(n)}
}

// psMakeDictValue wraps a dict as a Value with no reader.
// Kind() == Dict, suitable for psBegin.
func psMakeDictValue(d dict) Value {
	return Value{nil, objptr{}, d}
}

// psMakeStreamValue builds a Value of Kind Stream backed by the given bytes.
// The Reader reads from a shared backing buffer; the stream header carries the
// byte-exact length and the offset within that buffer.
//
// Call with a single shared Reader and the offset within it:
//
//	r  — minimal Reader whose f is a bytes.Reader over the combined backing bytes
//	off — byte offset where this stream's data begins in r.f
//	n   — byte length of this stream's data
func psMakeStreamValue(r *Reader, off, n int64) Value {
	s := stream{
		hdr:    dict{name("Length"): n},
		offset: off,
	}
	return Value{r, objptr{}, s}
}

// psMakeBuffer builds a buffer from a byte slice with allowEOF=true so that
// reaching EOF sets b.eof rather than panicking.
func psMakeBuffer(data []byte) *buffer {
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	return b
}

// psCmapStreamValue wraps a raw text body as a stream Value, mirroring the
// approach used in cmap_test.go but with a ps-prefixed name.
func psCmapStreamValue(body string) Value {
	data := []byte(body)
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}
	s := stream{
		hdr:    dict{name("Length"): int64(len(data))},
		offset: 0,
	}
	return Value{r, objptr{}, s}
}

// ---------------------------------------------------------------------------
// TestPsCurrentdict — psCurrentdict: push current dict onto stack
// ---------------------------------------------------------------------------

// TestPsCurrentdict verifies that psCurrentdict pushes the top dict onto the
// stack, and panics when the dict stack is empty.
func TestPsCurrentdict(t *testing.T) {
	t.Run("normal: pushes top dict", func(t *testing.T) {
		sentinel := name("sentinel")
		d := dict{sentinel: int64(42)}
		dicts := []dict{d}
		stk := psMakeStack()

		psCurrentdict(stk, &dicts)

		if stk.Len() != 1 {
			t.Fatalf("stack length: got %d, want 1", stk.Len())
		}
		top := stk.Pop()
		got, ok := top.data.(dict)
		if !ok {
			t.Fatalf("top value kind: got %T, want dict", top.data)
		}
		if got[sentinel] != int64(42) {
			t.Errorf("sentinel value: got %v, want 42", got[sentinel])
		}
	})

	t.Run("panic: empty dicts", func(t *testing.T) {
		var dicts []dict
		stk := psMakeStack()
		defer func() {
			if r := recover(); r == nil {
				t.Error("psCurrentdict on empty dicts: expected panic, got none")
			}
		}()
		psCurrentdict(stk, &dicts)
	})
}

// ---------------------------------------------------------------------------
// TestPsExecDefNonNameKey — execDef: non-name key is silently skipped
// ---------------------------------------------------------------------------

// TestPsExecDefNonNameKey verifies that execDef does not write anything to the
// top dictionary when the key popped from the stack is not a name (int64 here).
// This exercises the "key, ok := ...; if !ok { return }" branch.
func TestPsExecDefNonNameKey(t *testing.T) {
	d := make(dict)
	dicts := []dict{d}
	stk := psMakeStack()

	// execDef pops val first (top), then key (second from top).
	// Push key first (int64, not a name), then push val on top.
	stk.Push(psMakeIntValue(0)) // key — non-name: int64
	stk.Push(psMakeIntValue(9)) // val

	execDef(stk, &dicts)

	if len(d) != 0 {
		t.Errorf("dict should be empty after non-name key; got %v", d)
	}
	// Stack should be empty: both items were popped.
	if stk.Len() != 0 {
		t.Errorf("stack length after execDef: got %d, want 0", stk.Len())
	}
}

// ---------------------------------------------------------------------------
// TestPsBeginNonDictPanic — psBegin panics when top of stack is not a dict
// ---------------------------------------------------------------------------

// TestPsBeginNonDictPanic verifies that psBegin panics when the value popped
// from the stack has Kind != Dict.
func TestPsBeginNonDictPanic(t *testing.T) {
	dicts := []dict{}
	stk := psMakeStack()
	stk.Push(psMakeIntValue(7)) // not a dict

	defer func() {
		if r := recover(); r == nil {
			t.Error("psBegin on non-dict: expected panic, got none")
		}
	}()
	psBegin(stk, &dicts)
}

// ---------------------------------------------------------------------------
// TestPsEndEmptyPanic — psEnd panics when dict stack is empty
// ---------------------------------------------------------------------------

// TestPsEndEmptyPanic verifies that psEnd panics with "mismatched begin/end"
// when called with an empty dict stack.
func TestPsEndEmptyPanic(t *testing.T) {
	dicts := []dict{}
	defer func() {
		if r := recover(); r == nil {
			t.Error("psEnd on empty dicts: expected panic, got none")
		}
	}()
	psEnd(&dicts)
}

// ---------------------------------------------------------------------------
// TestPsOpenInterpBufferArray — openInterpBuffer: Array path via io.MultiReader
// ---------------------------------------------------------------------------

// TestPsOpenInterpBufferArray verifies that openInterpBuffer concatenates the
// readers of all stream elements when the Value's Kind is Array.
// Two streams at offsets 0 and 3 share one backing Reader; the combined read
// must produce the concatenation of both bodies.
func TestPsOpenInterpBufferArray(t *testing.T) {
	// Backing data: "11 22" split into two streams of lengths 3 and 2.
	// Stream 0: "11 " (bytes 0-2), Stream 1: "22"  (bytes 3-4).
	combined := []byte("11 22")
	r := &Reader{f: bytes.NewReader(combined), end: int64(len(combined))}

	s0 := psMakeStreamValue(r, 0, 3) // "11 "
	s1 := psMakeStreamValue(r, 3, 2) // "22"

	// Build an array Value whose .Index(i) will call r.resolve with the stream
	// objects directly (no indirect references, so resolve returns as-is).
	arr := Value{r, objptr{}, array{s0.data, s1.data}}

	if arr.Kind() != Array {
		t.Fatalf("arr.Kind() = %v, want Array", arr.Kind())
	}

	b := openInterpBuffer(arr)

	// Read all available bytes through the buffer.
	var got []byte
	for {
		c := b.readByte()
		if b.eof {
			break
		}
		got = append(got, c)
	}

	want := "11 22"
	if string(got) != want {
		t.Errorf("concatenated stream: got %q, want %q", string(got), want)
	}
}

// ---------------------------------------------------------------------------
// TestPsEiKeywordTerminatesEOF — eiKeywordTerminates returns true on EOF
// ---------------------------------------------------------------------------

// TestPsEiKeywordTerminatesEOF exercises the path where readByte() at the
// start of eiKeywordTerminates immediately hits EOF (b.eof == true).
func TestPsEiKeywordTerminatesEOF(t *testing.T) {
	b := psMakeBuffer(nil) // empty — first readByte sets b.eof

	result := eiKeywordTerminates(b)
	if !result {
		t.Error("eiKeywordTerminates on empty buffer: got false, want true")
	}
}

// ---------------------------------------------------------------------------
// TestPsSkipInlineImage — skipInlineImage terminates normally on EI + space
// ---------------------------------------------------------------------------

// TestPsSkipInlineImage verifies that skipInlineImage consumes bytes up to and
// including the EI keyword boundary without panicking.
func TestPsSkipInlineImage(t *testing.T) {
	// PDF inline image data followed by EI with a trailing space (boundary byte).
	input := []byte("some binary data\nEI ")
	b := psMakeBuffer(input)

	// Must return normally (not panic, not loop forever).
	skipInlineImage(b)

	// After skipInlineImage the position is just after the space that follows EI.
	// The buffer may or may not be at EOF; the key assertion is no panic.
}

// ---------------------------------------------------------------------------
// TestPsExecPS — execPS: keyword dispatch and return values
// ---------------------------------------------------------------------------

func testPsExecPSPop(t *testing.T) {
	t.Helper()
	stk := psMakeStack()
	stk.Push(psMakeIntValue(1))
	var dicts []dict
	if !execPS("pop", stk, &dicts) {
		t.Error("execPS(pop): got false, want true")
	}
	if stk.Len() != 0 {
		t.Errorf("stack after pop: len=%d, want 0", stk.Len())
	}
}

func testPsExecPSDict(t *testing.T) {
	t.Helper()
	stk := psMakeStack()
	stk.Push(psMakeIntValue(5))
	var dicts []dict
	if !execPS("dict", stk, &dicts) {
		t.Error("execPS(dict): got false, want true")
	}
	if stk.Len() != 1 {
		t.Errorf("stack after dict: len=%d, want 1", stk.Len())
	}
	if stk.Pop().Kind() != Dict {
		t.Error("execPS(dict): top of stack is not a Dict")
	}
}

func testPsExecPSCurrentdict(t *testing.T) {
	t.Helper()
	stk := psMakeStack()
	d := make(dict)
	dicts := []dict{d}
	if !execPS("currentdict", stk, &dicts) {
		t.Error("execPS(currentdict): got false, want true")
	}
	if stk.Len() != 1 {
		t.Errorf("stack after currentdict: len=%d, want 1", stk.Len())
	}
}

func testPsExecPSBegin(t *testing.T) {
	t.Helper()
	stk := psMakeStack()
	d := make(dict)
	stk.Push(psMakeDictValue(d))
	dicts := []dict{}
	if !execPS("begin", stk, &dicts) {
		t.Error("execPS(begin): got false, want true")
	}
	if len(dicts) != 1 {
		t.Errorf("dicts after begin: len=%d, want 1", len(dicts))
	}
}

func testPsExecPSEnd(t *testing.T) {
	t.Helper()
	stk := psMakeStack()
	dicts := []dict{make(dict)}
	if !execPS("end", stk, &dicts) {
		t.Error("execPS(end): got false, want true")
	}
	if len(dicts) != 0 {
		t.Errorf("dicts after end: len=%d, want 0", len(dicts))
	}
}

func testPsExecPSDef(t *testing.T) {
	t.Helper()
	stk := psMakeStack()
	d := make(dict)
	dicts := []dict{d}
	stk.Push(psMakeNameValue("myKey"))
	stk.Push(psMakeIntValue(99))
	if !execPS("def", stk, &dicts) {
		t.Error("execPS(def): got false, want true")
	}
	if v, ok := d[name("myKey")]; !ok || v != int64(99) {
		t.Errorf("dict after def: d[myKey]=%v ok=%v, want 99/true", v, ok)
	}
}

func testPsExecPSUnknown(t *testing.T) {
	t.Helper()
	stk := psMakeStack()
	var dicts []dict
	if execPS("unknown_kw", stk, &dicts) {
		t.Error("execPS(unknown_kw): got true, want false")
	}
}

// TestPsExecPS verifies that execPS handles all built-in PostScript keywords
// and returns true, and returns false for unknown keywords.
func TestPsExecPS(t *testing.T) {
	t.Run("pop", testPsExecPSPop)
	t.Run("dict", testPsExecPSDict)
	t.Run("currentdict", testPsExecPSCurrentdict)
	t.Run("begin", testPsExecPSBegin)
	t.Run("end", testPsExecPSEnd)
	t.Run("def", testPsExecPSDef)
	t.Run("unknown", testPsExecPSUnknown)
}

// ---------------------------------------------------------------------------
// TestPsInterpretWithStack — Interpret integration test
// ---------------------------------------------------------------------------

// TestPsInterpretWithStack verifies that Interpret correctly pushes non-keyword
// tokens onto the stack and calls do() for operator keywords.
//
// Stream body: "42 true mark"
//   - "42" and "true" are non-keyword tokens pushed as Values.
//   - "mark" is a keyword not handled by execPS, so do() is called with op="mark".
//
// Inside do(), we verify the stack holds exactly 2 items with the expected values.
func TestPsInterpretWithStack(t *testing.T) {
	body := "42 true mark"
	strm := psCmapStreamValue(body)

	var gotOp string
	var gotLen int
	var gotInt int64
	var gotBool bool

	Interpret(strm, func(stk *Stack, op string) {
		gotOp = op
		gotLen = stk.Len()
		if gotLen >= 2 {
			// Peek: items are in order bottom→top: [42, true]
			// stk.stack is accessible from package pdf.
			gotInt = stk.stack[0].Int64()
			gotBool = stk.stack[1].Bool()
		}
	})

	if gotOp != "mark" {
		t.Errorf("do() op: got %q, want %q", gotOp, "mark")
	}
	if gotLen != 2 {
		t.Errorf("stack length at do(): got %d, want 2", gotLen)
	}
	if gotInt != 42 {
		t.Errorf("stack[0].Int64(): got %d, want 42", gotInt)
	}
	if !gotBool {
		t.Error("stack[1].Bool(): got false, want true")
	}
}

// ---------------------------------------------------------------------------
// TestPsBeginEndRoundtrip — psBegin + psEnd round-trip: dict stack management
// ---------------------------------------------------------------------------

// TestPsBeginEndRoundtrip verifies that psBegin appends a dict to the dict
// stack, and a subsequent psEnd removes it, restoring the original length.
func TestPsBeginEndRoundtrip(t *testing.T) {
	outer := make(dict)
	dicts := []dict{outer}

	inner := make(dict)
	stk := psMakeStack()
	stk.Push(psMakeDictValue(inner))

	psBegin(stk, &dicts)
	if len(dicts) != 2 {
		t.Fatalf("after psBegin: dicts len=%d, want 2", len(dicts))
	}

	psEnd(&dicts)
	if len(dicts) != 1 {
		t.Fatalf("after psEnd: dicts len=%d, want 1", len(dicts))
	}
}

// ---------------------------------------------------------------------------
// TestPsEiKeywordTerminatesSpace — eiKeywordTerminates returns true on space
// ---------------------------------------------------------------------------

// TestPsEiKeywordTerminatesSpace exercises the isSpace branch: a space after
// "EI" is a valid boundary, so eiKeywordTerminates should return true and
// unread the space back into the buffer.
func TestPsEiKeywordTerminatesSpace(t *testing.T) {
	b := psMakeBuffer([]byte(" rest"))
	result := eiKeywordTerminates(b)
	if !result {
		t.Error("eiKeywordTerminates(' rest'): got false, want true")
	}
	// The space must have been unread; next readByte should return ' '.
	c := b.readByte()
	if c != ' ' {
		t.Errorf("byte after unread: got %q, want ' '", c)
	}
}

// ---------------------------------------------------------------------------
// TestPsEiKeywordTerminatesNonBoundary — eiKeywordTerminates returns false
// ---------------------------------------------------------------------------

// TestPsEiKeywordTerminatesNonBoundary exercises the "neither EOF nor boundary"
// branch: a regular alphanumeric byte after "EI" means it's not a keyword end.
func TestPsEiKeywordTerminatesNonBoundary(t *testing.T) {
	b := psMakeBuffer([]byte("Xmore"))
	result := eiKeywordTerminates(b)
	if result {
		t.Error("eiKeywordTerminates('Xmore'): got true, want false")
	}
	// The 'X' must have been unread.
	c := b.readByte()
	if c != 'X' {
		t.Errorf("byte after unread: got %q, want 'X'", c)
	}
}

// ---------------------------------------------------------------------------
// TestPsInterpretNonArrayPath — openInterpBuffer with a plain (non-Array) stream
// ---------------------------------------------------------------------------

// TestPsInterpretNonArrayPath verifies that Interpret processes a plain stream
// Value (not an Array) correctly, exercising the else branch of openInterpBuffer.
func TestPsInterpretNonArrayPath(t *testing.T) {
	// "99 add" → 99 is pushed, "add" triggers do().
	strm := psCmapStreamValue("99 add")

	var called bool
	var gotVal int64

	Interpret(strm, func(stk *Stack, op string) {
		if op == "add" {
			called = true
			if stk.Len() >= 1 {
				gotVal = stk.stack[0].Int64()
			}
		}
	})

	if !called {
		t.Error("do() was never called for 'add' keyword")
	}
	if gotVal != 99 {
		t.Errorf("stack[0] at 'add': got %d, want 99", gotVal)
	}
}

// ---------------------------------------------------------------------------
// TestPsLookupInDicts — lookupInDicts finds values innermost-first
// ---------------------------------------------------------------------------

// TestPsLookupInDicts verifies that lookupInDicts searches from the innermost
// (last) dict outward and pushes the found value onto the stack.
func TestPsLookupInDicts(t *testing.T) {
	outer := dict{name("x"): int64(1)}
	inner := dict{name("x"): int64(2), name("y"): int64(3)}
	dicts := []dict{outer, inner}
	stk := psMakeStack()

	// "x" should resolve to inner's value (2), not outer's (1).
	found := lookupInDicts(keyword("x"), stk, dicts)
	if !found {
		t.Fatal("lookupInDicts: 'x' not found, want found")
	}
	if stk.Len() != 1 {
		t.Fatalf("stack len: got %d, want 1", stk.Len())
	}
	if v := stk.Pop().Int64(); v != 2 {
		t.Errorf("lookupInDicts 'x': got %d, want 2 (inner)", v)
	}

	// "y" is only in inner.
	found = lookupInDicts(keyword("y"), stk, dicts)
	if !found {
		t.Fatal("lookupInDicts: 'y' not found, want found")
	}
	if v := stk.Pop().Int64(); v != 3 {
		t.Errorf("lookupInDicts 'y': got %d, want 3", v)
	}

	// "z" is not in any dict.
	found = lookupInDicts(keyword("z"), stk, dicts)
	if found {
		t.Error("lookupInDicts: 'z' found unexpectedly")
	}
}

// ---------------------------------------------------------------------------
// TestPsInterpretDefBeginEnd — Interpret uses def/begin/end internally
// ---------------------------------------------------------------------------

// TestPsInterpretDefBeginEnd verifies that Interpret handles the full
// dict-management PostScript sequence:  dict begin /key val def currentdict end.
// After interpretation do() is called with "check"; by then the dicts list is
// empty (end was called) but the currentdict was pushed onto the stack before end.
func TestPsInterpretDefBeginEnd(t *testing.T) {
	// "1 dict  begin  /mykey 77 def  currentdict  end  check"
	// Walkthrough:
	//   1 dict       → pushes new dict D onto stack
	//   begin        → pops D from stack, appends to dicts
	//   /mykey       → pushes name "mykey"
	//   77           → pushes int64 77
	//   def          → stores dicts[0]["mykey"]=77
	//   currentdict  → pushes dicts[0] (a dict) onto stack
	//   end          → removes dicts[0]; dicts is now empty
	//   check        → do(stk, "check") — stk has one item: the dict D
	body := strings.Join([]string{
		"1 dict begin",
		"/mykey 77 def",
		"currentdict end",
		"check",
	}, "\n")
	strm := psCmapStreamValue(body)

	var gotOp string
	var gotStackLen int
	var gotDictHasKey bool

	Interpret(strm, func(stk *Stack, op string) {
		gotOp = op
		gotStackLen = stk.Len()
		if stk.Len() >= 1 {
			if d, ok := stk.stack[0].data.(dict); ok {
				_, gotDictHasKey = d[name("mykey")]
			}
		}
	})

	if gotOp != "check" {
		t.Errorf("do() op: got %q, want %q", gotOp, "check")
	}
	if gotStackLen != 1 {
		t.Errorf("stack len at do(): got %d, want 1", gotStackLen)
	}
	if !gotDictHasKey {
		t.Error("currentdict pushed onto stack does not contain 'mykey'")
	}
}
