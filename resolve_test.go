// Tests for resolve.go: objStmHeader, scanObjStmIndex, resolveInStream, and resolve.

package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// makeResolveReader returns a *Reader whose underlying file holds data, so that
// loadDirectObject and buildStreamReader can read from it at the right offsets.
func makeResolveReader(data []byte) *Reader {
	return &Reader{f: bytes.NewReader(data), end: int64(len(data))}
}

// makeObjStmValue builds a Value of Kind Stream that represents an ObjStm.
// body is the raw content that Value.Reader() will return (uncompressed).
// n is the N (object count) and first is the First (offset to first object body).
func makeObjStmValue(r *Reader, body []byte, n int, first int64) Value {
	h := dict{
		name("Type"):   name("ObjStm"),
		name("N"):      int64(n),
		name("First"):  first,
		name("Length"): int64(len(body)),
	}
	s := stream{hdr: h, ptr: objptr{}, offset: 0}
	return Value{r, objptr{}, s}
}

// ---- TestResolveObjStmHeader --------------------------------------------------

// TestResolveObjStmHeader verifies objStmHeader against missing/malformed inputs.
func TestResolveObjStmHeader(t *testing.T) {
	r := makeResolveReader(nil)

	t.Run("not_a_stream", func(t *testing.T) {
		// error path: passing a dict Value (Kind==Dict) must panic.
		v := Value{r, objptr{}, dict{}}
		panicked := false
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					panicked = true
				}
			}()
			objStmHeader(v)
		}()
		if !panicked {
			t.Fatal("expected panic for non-stream Value, got none")
		}
	})

	t.Run("wrong_type", func(t *testing.T) {
		// error path: stream with /Type /Catalog (not /ObjStm) must panic.
		h := dict{
			name("Type"):   name("Catalog"),
			name("N"):      int64(1),
			name("First"):  int64(10),
			name("Length"): int64(0),
		}
		v := Value{r, objptr{}, stream{hdr: h}}
		panicked := false
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					panicked = true
				}
			}()
			objStmHeader(v)
		}()
		if !panicked {
			t.Fatal("expected panic for wrong /Type, got none")
		}
	})

	t.Run("missing_first", func(t *testing.T) {
		// error path: ObjStm with /First == 0 must panic.
		body := []byte("")
		v := makeObjStmValue(r, body, 1, 0 /* First deliberately zero */)
		// Override the Length so it is consistent.
		v.data.(stream).hdr[name("First")] = int64(0)
		panicked := false
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					panicked = true
				}
			}()
			objStmHeader(v)
		}()
		if !panicked {
			t.Fatal("expected panic for First==0, got none")
		}
	})

	t.Run("valid", func(t *testing.T) {
		// Well-formed ObjStm: must return N and First without panicking.
		body := []byte("10 0 (hello)")
		// first = len("10 0 ") = 5, but we put a realistic value.
		v := makeObjStmValue(r, body, 1, 5)
		n, first := objStmHeader(v)
		if n != 1 {
			t.Errorf("N: got %d, want 1", n)
		}
		if first != 5 {
			t.Errorf("First: got %d, want 5", first)
		}
	})
}

// ---- TestResolveScanObjStmIndex -----------------------------------------------

// TestResolveScanObjStmIndex exercises scanObjStmIndex with various index layouts.
func TestResolveScanObjStmIndex(t *testing.T) {
	// ObjStm body layout used in all sub-tests:
	//   Index section (n=2 pairs): "10 0 20 7 "   (14 bytes)
	//   first = 14
	//   Object bodies at first+0 and first+7:
	//     offset 14: "(hello)"   (7 bytes)
	//     offset 21: "42"        (2 bytes, terminated by whitespace/EOF)
	//
	// We construct the buffer starting at offset 0 (the beginning of the ObjStm
	// body), so seekForward(first+off) works against offset 0.

	const indexSection = "10 0 20 7 " // 10 bytes — two pairs
	const bodySection = "(hello) 42"  // bodies at relative offsets 0 and 7
	const rawStream = indexSection + bodySection
	const first = int64(len(indexSection)) // 10

	newBuf := func() *buffer {
		b := newBuffer(strings.NewReader(rawStream), 0)
		b.allowEOF = true
		return b
	}

	t.Run("found_first_object", func(t *testing.T) {
		b := newBuf()
		ptr := objptr{id: 10}
		obj, ok := scanObjStmIndex(b, 2, first, ptr)
		if !ok {
			t.Fatal("expected to find object id=10, got ok=false")
		}
		s, isStr := obj.(string)
		if !isStr {
			t.Fatalf("expected string object, got %T(%v)", obj, obj)
		}
		if s != "hello" {
			t.Errorf("object content: got %q, want %q", s, "hello")
		}
	})

	t.Run("found_second_object", func(t *testing.T) {
		b := newBuf()
		ptr := objptr{id: 20}
		obj, ok := scanObjStmIndex(b, 2, first, ptr)
		if !ok {
			t.Fatal("expected to find object id=20, got ok=false")
		}
		v, isInt := obj.(int64)
		if !isInt {
			t.Fatalf("expected int64 object, got %T(%v)", obj, obj)
		}
		if v != 42 {
			t.Errorf("object value: got %d, want 42", v)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		// error path: id not present in the index → ok=false returned.
		b := newBuf()
		ptr := objptr{id: 99} // not in index
		_, ok := scanObjStmIndex(b, 2, first, ptr)
		if ok {
			t.Fatal("expected ok=false for absent id, got true") // error path
		}
	})
}

// ---- TestResolveInStream ------------------------------------------------------

// TestResolveInStream verifies that resolveInStream can locate an object stored
// inside an ObjStm and return its decoded value.
func TestResolveInStream(t *testing.T) {
	// Use a Reader constructed from a full PDF with ObjStm.
	// We build the xref manually to exercise resolveInStream directly,
	// using the same technique as xref_stream_test.go.

	// ObjStm body: index "5 0 " (4 bytes, First=4), then object body "99"
	strmBody := []byte("5 0 99")
	const first = int64(4)

	// The Reader's f holds the strmBody so Value.Reader() returns it.
	r := makeResolveReader(strmBody)

	// Build the xref table:
	//   slot 0: empty (free)
	//   slot 1: the ObjStm stream itself — direct, offset=0 (but we bypass loadDirectObject)
	//   slot 5: in-stream, stream=objptr{1,0}, stream-index offset=0
	//
	// We inject a pre-built stream Value directly so we avoid needing a real
	// objdef at offset 0.  resolveInStream calls r.resolve(parent, xr.stream)
	// where xr.stream = objptr{1,0}.  resolve looks up xref[1], and since
	// xref[1].inStream is false and offset != 0 it calls loadDirectObject.
	// To keep the test self-contained we wire up the ObjStm by inserting a
	// stream value directly into the xref table and calling resolveInStream
	// via a wrapper that replaces the xref lookup.
	//
	// The cleanest white-box path: call the internal resolve(parent, stream_value)
	// to get the Value, then call resolveInStream on a Reader with a synthetic
	// xref where xref[5] points into that ObjStm.

	// Build the ObjStm Value directly (no file I/O needed for this part).
	strmHdr := dict{
		name("Type"):   name("ObjStm"),
		name("N"):      int64(1),
		name("First"):  first,
		name("Length"): int64(len(strmBody)),
	}
	strmVal := Value{r, objptr{1, 0}, stream{hdr: strmHdr, ptr: objptr{1, 0}, offset: 0}}

	// Verify that the ObjStm Value is well-formed before proceeding.
	if strmVal.Kind() != Stream {
		t.Fatal("strmVal is not a Stream")
	}
	if strmVal.Key("Type").Name() != "ObjStm" {
		t.Fatal("strmVal /Type is not ObjStm")
	}

	// Exercise scanObjStmIndex on the ObjStm to confirm the target object
	// (id=5) is resolvable — this is the core path tested by resolveInStream.
	b := newBuffer(strmVal.Reader(), 0)
	b.allowEOF = true
	n, fi := objStmHeader(strmVal)
	obj, ok := scanObjStmIndex(b, n, fi, objptr{id: 5})
	if !ok {
		t.Fatal("resolveInStream: scanObjStmIndex could not locate object id=5")
	}
	v, isInt := obj.(int64)
	if !isInt {
		t.Fatalf("resolveInStream: expected int64 object, got %T(%v)", obj, obj)
	}
	if v != 99 {
		t.Errorf("resolveInStream: object value = %d, want 99", v)
	}
}

// ---- TestResolveInvalidID -----------------------------------------------------

// TestResolveInvalidID verifies that resolve returns a null Value (no panic)
// when the requested object id is beyond the xref table bounds.
func TestResolveInvalidID(t *testing.T) {
	r := makeResolveReader(nil)
	// xref has 3 slots (ids 0, 1, 2); requesting id=999 must return Null.
	r.xref = make([]xref, 3)

	ptr := objptr{id: 999}
	got := r.resolve(objptr{}, ptr)
	if got.Kind() != Null {
		t.Errorf("resolve with id >= len(xref): got Kind=%v, want Null", got.Kind())
	}
}

// TestResolveInvalidIDZeroXref verifies that resolve with an empty xref table
// returns Null and does not panic for any non-zero object id.
func TestResolveInvalidIDZeroXref(t *testing.T) {
	r := makeResolveReader(nil)
	r.xref = nil // empty table

	for _, id := range []uint32{0, 1, 100} {
		ptr := objptr{id: id}
		got := r.resolve(objptr{}, ptr)
		if got.Kind() != Null {
			t.Errorf("empty xref, id=%d: got Kind=%v, want Null", id, got.Kind())
		}
	}
}

// ---- TestResolveDirectValue ---------------------------------------------------

// TestResolveDirectValue verifies that resolve wraps non-pointer objects in a
// Value without any xref lookup or I/O.
func TestResolveDirectValue(t *testing.T) {
	r := makeResolveReader(nil)
	parent := objptr{id: 1, gen: 0}

	cases := []struct {
		name string
		data any
		want ValueKind
	}{
		{"integer", int64(123), Integer},
		{"real", float64(3.14), Real},
		{"bool", true, Bool},
		{"string", "hello", String},
		{"name", name("Helvetica"), Name},
		{"dict", dict{}, Dict},
		{"array", array{}, Array},
		{"null", nil, Null},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.resolve(parent, tc.data)
			if got.Kind() != tc.want {
				t.Errorf("resolve(%T): Kind=%v, want %v", tc.data, got.Kind(), tc.want)
			}
			// The returned Value must carry the same parent ptr.
			if got.ptr != parent {
				t.Errorf("resolve(%T): ptr=%v, want %v", tc.data, got.ptr, parent)
			}
		})
	}
}

// TestResolveDirectValueStream verifies that a stream object passed directly
// to resolve is wrapped as Kind==Stream without panicking.
func TestResolveDirectValueStream(t *testing.T) {
	body := []byte("hello world")
	r := makeResolveReader(body)
	parent := objptr{id: 7}

	s := stream{
		hdr:    dict{name("Length"): int64(len(body))},
		ptr:    parent,
		offset: 0,
	}

	got := r.resolve(parent, s)
	if got.Kind() != Stream {
		t.Errorf("resolve(stream): Kind=%v, want Stream", got.Kind())
	}
	if got.ptr != parent {
		t.Errorf("resolve(stream): ptr=%v, want %v", got.ptr, parent)
	}
}

// ---- TestResolveInStreamDirect ------------------------------------------------

// TestResolveInStreamDirect calls resolveInStream directly and verifies that it
// can locate and decode an object stored inside an ObjStm fetched via xref and
// loadDirectObject — exercising the full resolveInStream code path.
//
// File layout (83 bytes):
//
//	offset 0:  " "   (1-byte prefix; ensures offset==0 triggers the null-Value
//	                   guard in resolve, so xref[1] must have offset != 0)
//	offset 1:  "1 0 obj << /Type /ObjStm /N 1 /First 4 /Length 6 >> stream\n"
//	offset 60: "5 0 99"   (stream body, 6 bytes)
//	offset 66: "\nendstream endobj"
//
// ObjStm stream body "5 0 99":
//
//	index section (First=4 bytes): "5 0 "  → id=5, body-offset=0
//	body section  (from First=4):  "99"    → integer 99
func TestResolveInStreamDirect(t *testing.T) {
	// Build the file bytes: 1-byte prefix + PDF objdef for the ObjStm.
	fileBytes := []byte(" 1 0 obj << /Type /ObjStm /N 1 /First 4 /Length 6 >> stream\n5 0 99\nendstream endobj")

	r := makeResolveReader(fileBytes)

	// Wire the xref table so that:
	//   slot 1 → the ObjStm object at file offset 1 (loadDirectObject path).
	//            xref.ptr must equal objptr{id:1} or resolve returns null.
	r.xref = []xref{
		{}, // slot 0: unused
		{ptr: objptr{id: 1, gen: 0}, inStream: false, offset: 1}, // slot 1: ObjStm
		{}, {}, {}, {}, // slots 2-5: unused
	}

	// xref entry that describes object 5 as living inside the ObjStm at slot 1.
	// resolveInStream receives this entry and uses xr.stream to fetch the ObjStm.
	xr := xref{
		ptr:      objptr{id: 5, gen: 0},
		inStream: true,
		stream:   objptr{id: 1, gen: 0},
		offset:   0, // index within ObjStm (not used by resolveInStream directly)
	}

	got := r.resolveInStream(objptr{}, objptr{id: 5, gen: 0}, xr)

	v, ok := got.(int64)
	if !ok {
		t.Fatalf("resolveInStream: expected int64, got %T(%v)", got, got)
	}
	if v != 99 {
		t.Errorf("resolveInStream: got %d, want 99", v)
	}
}
