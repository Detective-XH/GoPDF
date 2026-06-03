package pdf

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"strings"
	"testing"
)

// makeXrefReader returns a *Reader whose underlying file holds body, so that
// Value.Reader() for a stream positioned at offset 0 returns the body bytes.
func makeXrefReader(body []byte) *Reader {
	return &Reader{f: bytes.NewReader(body), end: int64(len(body))}
}

// makeXrefStream returns a stream value whose raw bytes are body and whose
// header includes the provided extra entries plus /Length.
func makeXrefStream(body []byte, extraHdr dict) stream {
	h := dict{
		name("Length"): int64(len(body)),
	}
	maps.Copy(h, extraHdr)
	return stream{hdr: h, offset: 0}
}

// nopData wraps a byte slice as an io.ReadCloser.
func nopData(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}

// --- processXrefEntry ----------------------------------------------------------

// TestXrefStreamProcessEntryType0 verifies that type-0 (free) entries are
// recorded with the sentinel ptr {0, 65535}.
func TestXrefStreamProcessEntryType0(t *testing.T) {
	// W=[1,2,1]: type(1) + offset(2) + gen(1) = 4 bytes per entry.
	w := []int{1, 2, 1}
	entryBytes := []byte{0, 0, 0, 0} // type=0, offset=0, gen=0
	buf := make([]byte, 4)
	table := make([]xref, 2)

	got, err := processXrefEntry(buf, w, 0, 1, table, nopData(entryBytes))
	if err != nil {
		t.Fatalf("processXrefEntry type-0: unexpected error: %v", err)
	}
	want := xref{ptr: objptr{0, 65535}}
	if got[1] != want {
		t.Errorf("type-0: got %+v, want %+v", got[1], want)
	}
}

// TestXrefStreamProcessEntryType1 verifies that type-1 (in-use) entries are
// recorded with the correct object offset and generation number.
func TestXrefStreamProcessEntryType1(t *testing.T) {
	w := []int{1, 2, 1}
	// type=1, offset=0x000A (10), gen=2
	entryBytes := []byte{1, 0x00, 0x0A, 2}
	buf := make([]byte, 4)
	table := make([]xref, 5)

	got, err := processXrefEntry(buf, w, 0, 3, table, nopData(entryBytes))
	if err != nil {
		t.Fatalf("processXrefEntry type-1: unexpected error: %v", err)
	}
	want := xref{ptr: objptr{3, 2}, offset: 10}
	if got[3] != want {
		t.Errorf("type-1: got %+v, want %+v", got[3], want)
	}
}

// TestXrefStreamProcessEntryType2 verifies that type-2 (compressed) entries
// are recorded with inStream=true, the stream object pointer, and the index.
func TestXrefStreamProcessEntryType2(t *testing.T) {
	w := []int{1, 2, 1}
	// type=2, stream-obj=5, index-within-stream=0
	entryBytes := []byte{2, 0x00, 0x05, 0}
	buf := make([]byte, 4)
	table := make([]xref, 5)

	got, err := processXrefEntry(buf, w, 0, 4, table, nopData(entryBytes))
	if err != nil {
		t.Fatalf("processXrefEntry type-2: unexpected error: %v", err)
	}
	want := xref{ptr: objptr{4, 0}, inStream: true, stream: objptr{5, 0}, offset: 0}
	if got[4] != want {
		t.Errorf("type-2: got %+v, want %+v", got[4], want)
	}
}

// TestXrefStreamProcessEntryAlreadyOccupied verifies that a slot with a
// non-zero ptr is not overwritten (first writer wins, matching xref-table
// semantics for incremental updates).
func TestXrefStreamProcessEntryAlreadyOccupied(t *testing.T) {
	w := []int{1, 2, 1}
	entryBytes := []byte{1, 0x00, 0x64, 0} // type=1, offset=100
	buf := make([]byte, 4)
	table := make([]xref, 3)
	// Pre-occupy slot 2.
	table[2] = xref{ptr: objptr{99, 0}, offset: 999}

	got, err := processXrefEntry(buf, w, 0, 2, table, nopData(entryBytes))
	if err != nil {
		t.Fatalf("already-occupied: unexpected error: %v", err)
	}
	if got[2].offset != 999 {
		t.Errorf("already-occupied: slot was overwritten; offset = %d, want 999", got[2].offset)
	}
}

// TestXrefStreamProcessEntryOutOfRange verifies that an object number beyond
// maxXrefObjects returns an error rather than silently growing the table.
func TestXrefStreamProcessEntryOutOfRange(t *testing.T) {
	w := []int{1, 2, 1}
	entryBytes := []byte{1, 0x00, 0x01, 0}
	buf := make([]byte, 4)
	table := make([]xref, 1)

	_, err := processXrefEntry(buf, w, int64(maxXrefObjects)+1, 0, table, nopData(entryBytes))
	if err == nil {
		t.Fatal("out-of-range: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("out-of-range: error should mention 'out of range', got: %v", err)
	}
}

// TestXrefStreamProcessEntryDefaultType verifies that when W[0]==0 (no type
// field in the stream), processXrefEntry defaults the entry type to 1
// (in-use), per PDF spec §7.5.8.3.
func TestXrefStreamProcessEntryDefaultType(t *testing.T) {
	// W=[0,2,1]: no type byte, offset(2), gen(1) — type defaults to 1.
	w := []int{0, 2, 1}
	// No type byte: just offset=0x0014 (20), gen=0.
	entryBytes := []byte{0x00, 0x14, 0}
	buf := make([]byte, 3)
	table := make([]xref, 5)

	got, err := processXrefEntry(buf, w, 0, 4, table, nopData(entryBytes))
	if err != nil {
		t.Fatalf("default-type: unexpected error: %v", err)
	}
	want := xref{ptr: objptr{4, 0}, offset: 20}
	if got[4] != want {
		t.Errorf("default-type (W[0]==0): got %+v, want %+v", got[4], want)
	}
}

// --- readXrefStreamData -------------------------------------------------------

// buildXrefBody returns a raw xref-stream body for entries described by
// entries. Each entry is [type, offset_hi, offset_lo, gen] matching W=[1,2,1].
func buildXrefBody(entries [][]byte) []byte {
	var body []byte
	for _, e := range entries {
		body = append(body, e...)
	}
	return body
}

// TestXrefStreamReadDataNoIndex tests readXrefStreamData with no Index array:
// the default [0, size] range should be used, processing all entries.
func TestXrefStreamReadDataNoIndex(t *testing.T) {
	// Two entries: obj 0 (free), obj 1 (type-1, offset=10, gen=0).
	body := buildXrefBody([][]byte{
		{0, 0, 0, 0},       // slot 0: free
		{1, 0x00, 0x0A, 0}, // slot 1: offset=10, gen=0
	})
	r := makeXrefReader(body)
	strm := makeXrefStream(body, dict{
		name("W"): array{int64(1), int64(2), int64(1)},
	})
	table := make([]xref, 2)

	got, err := readXrefStreamData(r, strm, table, 2)
	if err != nil {
		t.Fatalf("readXrefStreamData (no Index): %v", err)
	}
	if got[0] != (xref{ptr: objptr{0, 65535}}) {
		t.Errorf("slot 0: got %+v, want free entry", got[0])
	}
	want1 := xref{ptr: objptr{1, 0}, offset: 10}
	if got[1] != want1 {
		t.Errorf("slot 1: got %+v, want %+v", got[1], want1)
	}
}

// TestXrefStreamReadDataWithIndex tests readXrefStreamData when an Index array
// is present, so only the specified range of slots is populated.
func TestXrefStreamReadDataWithIndex(t *testing.T) {
	// One entry at slot 5 (Index=[5,1]): type-1, offset=42, gen=0.
	body := buildXrefBody([][]byte{
		{1, 0x00, 0x2A, 0}, // type=1, offset=42
	})
	r := makeXrefReader(body)
	strm := makeXrefStream(body, dict{
		name("W"):     array{int64(1), int64(2), int64(1)},
		name("Index"): array{int64(5), int64(1)},
	})
	table := make([]xref, 6)

	got, err := readXrefStreamData(r, strm, table, 6)
	if err != nil {
		t.Fatalf("readXrefStreamData (with Index): %v", err)
	}
	// Slots 0-4 should be zero (untouched).
	for i := range 5 {
		if got[i] != (xref{}) {
			t.Errorf("slot %d: want empty, got %+v", i, got[i])
		}
	}
	want5 := xref{ptr: objptr{5, 0}, offset: 42}
	if got[5] != want5 {
		t.Errorf("slot 5: got %+v, want %+v", got[5], want5)
	}
}

// --- readXrefStream -----------------------------------------------------------

// buildStreamObjBytes returns raw PDF bytes for a complete stream object with
// the given header entries and body.  The /Length is set to len(body)+1 to
// match the '\n' before endstream that buildTextPDF uses.
func buildStreamObjBytes(objNum int, hdrEntries string, body []byte) []byte {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d 0 obj\n<< %s /Length %d >>\nstream\n", objNum, hdrEntries, len(body)+1)
	sb.Write(body)
	sb.WriteString("\nendstream\nendobj\n")
	return []byte(sb.String())
}

// TestXrefStreamReadNonObjdef verifies that feeding a bare dict (not an objdef)
// to readXrefStream returns a "cross-reference table not found" error.
func TestXrefStreamReadNonObjdef(t *testing.T) {
	// A bare dict literal: not preceded by "N G obj".
	data := []byte("<< /Type /XRef >>\n")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}

	_, _, _, err := readXrefStream(r, b)
	if err == nil {
		t.Fatal("non-objdef: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cross-reference table not found") {
		t.Errorf("non-objdef: unexpected error message: %v", err)
	}
}

// TestXrefStreamReadNonStream verifies that an objdef whose inner object is a
// dict (not a stream) is rejected with "cross-reference table not found".
func TestXrefStreamReadNonStream(t *testing.T) {
	data := []byte("1 0 obj\n<< /Type /XRef >>\nendobj\n")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}

	_, _, _, err := readXrefStream(r, b)
	if err == nil {
		t.Fatal("non-stream: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cross-reference table not found") {
		t.Errorf("non-stream: unexpected error message: %v", err)
	}
}

// TestXrefStreamReadMissingTypeXRef verifies that a stream missing /Type /XRef
// is rejected.
func TestXrefStreamReadMissingTypeXRef(t *testing.T) {
	// Valid stream object but /Type /Catalog instead of /XRef.
	body := []byte{1, 0x00, 0x00, 0}
	data := buildStreamObjBytes(1, "/Type /Catalog /Size 1 /W [1 2 1]", body)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}

	_, _, _, err := readXrefStream(r, b)
	if err == nil {
		t.Fatal("missing-type-xref: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "type XRef") {
		t.Errorf("missing-type-xref: unexpected error message: %v", err)
	}
}

// TestXrefStreamReadMissingSize verifies that a stream without a /Size entry
// is rejected.
func TestXrefStreamReadMissingSize(t *testing.T) {
	body := []byte{1, 0x00, 0x00, 0}
	data := buildStreamObjBytes(1, "/Type /XRef /W [1 2 1]", body)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}

	_, _, _, err := readXrefStream(r, b)
	if err == nil {
		t.Fatal("missing-size: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing Size") {
		t.Errorf("missing-size: unexpected error message: %v", err)
	}
}

// TestXrefStreamReadSizeOutOfRange verifies that a /Size that is zero or
// negative (or above the allowed limit) is rejected.
func TestXrefStreamReadSizeOutOfRange(t *testing.T) {
	for _, badSize := range []int{0, -1, maxXrefObjects + 1} {
		body := []byte{1, 0x00, 0x00, 0}
		hdr := fmt.Sprintf("/Type /XRef /Size %d /W [1 2 1]", badSize)
		data := buildStreamObjBytes(1, hdr, body)
		b := newBuffer(bytes.NewReader(data), 0)
		b.allowEOF = true
		r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}

		_, _, _, err := readXrefStream(r, b)
		if err == nil {
			t.Fatalf("size-out-of-range (%d): expected error, got nil", badSize)
		}
		if !strings.Contains(err.Error(), "out of range") {
			t.Errorf("size-out-of-range (%d): unexpected error: %v", badSize, err)
		}
	}
}
