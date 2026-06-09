package pdf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// --- helpers ------------------------------------------------------------------

// makeReaderFromBytes returns a *Reader whose underlying file is data.
func makeReaderFromBytes(data []byte) *Reader {
	return &Reader{f: bytes.NewReader(data), end: int64(len(data))}
}

// buildXrefTableSection returns raw PDF bytes for one xref subsection:
//
//	<start> <count>\n
//	<entry0>\n
//	...
//
// Each entry in entries is [offset, gen, alloc] e.g. [0, 65535, 'f'] or
// [100, 0, 'n'].
func buildXrefTableSection(start int, entries [][3]int64) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%d %d\n", start, len(entries))
	for _, e := range entries {
		// PDF xref entry format: 10-digit-offset SP 5-digit-gen SP alloc SP \r\n
		alloc := byte(e[2])
		fmt.Fprintf(&buf, "%010d %05d %c \r\n", e[0], e[1], alloc)
	}
	return buf.Bytes()
}

// --- TestXrefTableSingleSection -----------------------------------------------

// TestXrefTableSingleSection parses a classic one-section xref table through
// readXrefTableData and verifies that the returned slice has the expected
// offsets.
func TestXrefTableSingleSection(t *testing.T) {
	// Build: two entries — slot 0 free, slot 1 at offset 100.
	section := buildXrefTableSection(0, [][3]int64{
		{0, 65535, 'f'},
		{100, 0, 'n'},
	})
	// Append the trailer keyword so readXrefTableData stops correctly.
	data := append(section, []byte("trailer\n")...)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	table, err := readXrefTableData(b, nil)
	if err != nil {
		t.Fatalf("readXrefTableData: unexpected error: %v", err)
	}
	if len(table) < 2 {
		t.Fatalf("expected table len >= 2, got %d", len(table))
	}
	// Slot 0 is free ('f') → offset stays 0, ptr stays zero-value.
	if table[0].offset != 0 {
		t.Errorf("slot 0: expected offset 0 (free), got %d", table[0].offset)
	}
	// Slot 1 is 'n' at offset 100.
	if table[1].offset != 100 {
		t.Errorf("slot 1: expected offset 100, got %d", table[1].offset)
	}
	if table[1].ptr != (objptr{1, 0}) {
		t.Errorf("slot 1: expected ptr {1,0}, got %+v", table[1].ptr)
	}
}

// TestXrefTableMultipleSections verifies that readXrefTableData correctly
// handles two subsections in sequence.
func TestXrefTableMultipleSections(t *testing.T) {
	sec0 := buildXrefTableSection(0, [][3]int64{
		{0, 65535, 'f'},
	})
	sec3 := buildXrefTableSection(3, [][3]int64{
		{200, 0, 'n'},
		{300, 0, 'n'},
	})
	data := append(append(sec0, sec3...), []byte("trailer\n")...)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	table, err := readXrefTableData(b, nil)
	if err != nil {
		t.Fatalf("readXrefTableData (multi-section): %v", err)
	}
	if len(table) < 5 {
		t.Fatalf("expected table len >= 5, got %d", len(table))
	}
	if table[3].offset != 200 {
		t.Errorf("slot 3: expected offset 200, got %d", table[3].offset)
	}
	if table[4].offset != 300 {
		t.Errorf("slot 4: expected offset 300, got %d", table[4].offset)
	}
}

// --- TestXrefMalformedEntry ---------------------------------------------------

// TestXrefMalformedEntry verifies that a truncated or garbage xref entry
// causes readXrefTableData to return an error without panicking.
func TestXrefMalformedEntry(t *testing.T) {
	// A subsection header claiming 1 entry followed by garbage bytes — not a
	// valid 20-byte entry.
	data := []byte("0 1\nGARBAGE_DATA_HERE\ntrailer\n")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	_, err := readXrefTableData(b, nil) // error path
	if err == nil {
		t.Fatal("malformed entry: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "malformed xref table") {
		t.Errorf("malformed entry: unexpected error message: %v", err)
	}
}

// TestXrefMalformedSubsectionHeader verifies that a subsection header with
// non-integer tokens triggers an error.
func TestXrefMalformedSubsectionHeader(t *testing.T) {
	// Non-integer start token.
	data := []byte("bad 1\n0000000000 65535 f \r\ntrailer\n")
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true

	_, err := readXrefTableData(b, nil) // error path
	if err == nil {
		t.Fatal("malformed subsection header: expected error, got nil")
	}
}

// --- TestXrefApplyPrevTable ---------------------------------------------------

// TestXrefApplyPrevTable exercises applyPrevXrefTable with a two-level Prev
// chain: the initial table references a "previous" xref block at a known
// offset inside the Reader's underlying file.
func TestXrefApplyPrevTable(t *testing.T) {
	// Prev xref block layout (to be embedded inside the Reader's file at
	// offset prevOff):
	//   xref\n
	//   0 1\n0000000000 65535 f \r\n   <- slot 0 free
	//   trailer\n<< /Size 1 >>\n
	//
	// We want applyPrevXrefTable to:
	//   1. Read the xref keyword at prevOff.
	//   2. Parse the table data (slot 0 free).
	//   3. Read the trailer dict (no further Prev → return nil).
	// Build the "prev" xref block.  After the trailer dict we need a few extra
	// bytes so the buffer's look-ahead for a "stream" keyword after ">>" does
	// not hit EOF (which would panic without allowEOF).  A newline followed by
	// "startxref" is enough because it is not "stream".
	var prevBlock bytes.Buffer
	prevBlock.WriteString("xref\n")
	prevBlock.Write(buildXrefTableSection(0, [][3]int64{
		{0, 65535, 'f'},
	}))
	prevBlock.WriteString("trailer\n<< /Size 1 >>\nstartxref\n0\n%%%%EOF\n")

	// The file for the Reader consists of arbitrary leading bytes followed by
	// the prev block.  prevOff is chosen so that it is a known position.
	const prevOff = 20
	var file bytes.Buffer
	for file.Len() < prevOff {
		file.WriteByte(' ')
	}
	file.Write(prevBlock.Bytes())
	fileBytes := file.Bytes()

	r := makeReaderFromBytes(fileBytes)
	// Start with an empty table.
	table, nextPrev, err := applyPrevXrefTable(r, int64(prevOff), nil)
	if err != nil {
		t.Fatalf("applyPrevXrefTable: unexpected error: %v", err)
	}
	// Trailer has no Prev key → nextPrev should be nil.
	if nextPrev != nil {
		t.Errorf("applyPrevXrefTable: expected nil nextPrev, got %v", nextPrev)
	}
	// Table should have at least 1 slot.
	if len(table) < 1 {
		t.Fatalf("applyPrevXrefTable: expected table len >= 1, got %d", len(table))
	}
}

// TestXrefApplyPrevTableBadOffset verifies that pointing applyPrevXrefTable
// at a file position that does not start with the "xref" keyword returns an
// error.
func TestXrefApplyPrevTableBadOffset(t *testing.T) {
	// File filled with garbage — no xref keyword.
	fileBytes := bytes.Repeat([]byte("GARBAGE "), 32)
	r := makeReaderFromBytes(fileBytes)

	_, _, err := applyPrevXrefTable(r, 0, nil) // error path
	if err == nil {
		t.Fatal("bad offset: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xref Prev does not point to xref") {
		t.Errorf("bad offset: unexpected error: %v", err)
	}
}

// TestXrefApplyPrevTableNoTrailer verifies that an xref block without a
// trailer dict is rejected.
func TestXrefApplyPrevTableNoTrailer(t *testing.T) {
	// xref block with table data but no "trailer" dict.  Use "null" in place of
	// the dictionary, followed by extra bytes so the buffer look-ahead never
	// hits EOF.
	var prevBlock bytes.Buffer
	prevBlock.WriteString("xref\n")
	prevBlock.Write(buildXrefTableSection(0, [][3]int64{{0, 65535, 'f'}}))
	prevBlock.WriteString("trailer\nnull\nstartxref\n0\n%%%%EOF\n") // null instead of dict

	const prevOff = 0
	r := makeReaderFromBytes(prevBlock.Bytes())

	_, _, err := applyPrevXrefTable(r, int64(prevOff), nil) // error path
	if err == nil {
		t.Fatal("no trailer dict: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "trailer dictionary") {
		t.Errorf("no trailer dict: unexpected error: %v", err)
	}
}

// --- TestXrefFollowPrevChain --------------------------------------------------

// TestXrefFollowPrevChain exercises followXrefTablePrevChain with a two-level
// Prev chain embedded in the Reader's file.
func TestXrefFollowPrevChain(t *testing.T) {
	// Each trailer dict must be followed by extra bytes so the buffer look-ahead
	// for a "stream" keyword after ">>" does not panic on EOF.
	const trailSuffix = "\nstartxref\n0\n%%%%EOF\n"

	// Level-2 (oldest) prev block at offset 30.  No further Prev.
	var level2 bytes.Buffer
	level2.WriteString("xref\n")
	level2.Write(buildXrefTableSection(2, [][3]int64{
		{400, 0, 'n'}, // slot 2 at offset 400
	}))
	level2.WriteString("trailer\n<< /Size 3 >>" + trailSuffix)

	// Level-1 prev block at offset 0.  Its trailer points Prev to level2.
	// The level2Off must be determined by layout: level1 block + padding.
	// We'll fix level2Off at 100 to give level1 plenty of room.
	const level2Off = 100
	var level1 bytes.Buffer
	level1.WriteString("xref\n")
	level1.Write(buildXrefTableSection(1, [][3]int64{
		{200, 0, 'n'}, // slot 1 at offset 200
	}))
	fmt.Fprintf(&level1, "trailer\n<< /Size 3 /Prev %d >>%s", level2Off, trailSuffix)

	// Stitch together: level1 at 0, level2 at level2Off.
	var file bytes.Buffer
	file.Write(level1.Bytes())
	// Pad to level2Off.
	for file.Len() < level2Off {
		file.WriteByte(' ')
	}
	file.Write(level2.Bytes())
	fileBytes := file.Bytes()

	r := makeReaderFromBytes(fileBytes)

	// Start with a table that already has slot 0 populated (simulates the
	// current/latest xref table that callers would pass in).
	startTable := make([]xref, 3)
	startTable[0] = xref{ptr: objptr{0, 65535}}

	// Trailer for the current table points Prev to level1 at offset 0.
	trailer := dict{
		"Prev": int64(0),
	}

	table, err := followXrefTablePrevChain(r, startTable, trailer)
	if err != nil {
		t.Fatalf("followXrefTablePrevChain (two-level): unexpected error: %v", err)
	}
	if len(table) < 3 {
		t.Fatalf("expected table len >= 3, got %d", len(table))
	}
	// Slot 1 was contributed by level1.
	if table[1].offset != 200 {
		t.Errorf("slot 1: expected offset 200 from level-1 prev, got %d", table[1].offset)
	}
	// Slot 2 was contributed by level2.
	if table[2].offset != 400 {
		t.Errorf("slot 2: expected offset 400 from level-2 prev, got %d", table[2].offset)
	}
}

// TestXrefFollowPrevChainNoPrev verifies that a trailer with no Prev key
// returns the input table unchanged.
func TestXrefFollowPrevChainNoPrev(t *testing.T) {
	r := makeReaderFromBytes([]byte("%PDF-1.4\n"))
	startTable := make([]xref, 2)
	startTable[1] = xref{ptr: objptr{1, 0}, offset: 42}
	trailer := dict{} // no Prev key

	got, err := followXrefTablePrevChain(r, startTable, trailer)
	if err != nil {
		t.Fatalf("no-prev: unexpected error: %v", err)
	}
	if got[1].offset != 42 {
		t.Errorf("no-prev: slot 1 offset changed; got %d, want 42", got[1].offset)
	}
}

// TestXrefFollowPrevChainNonIntegerPrev verifies that a trailer with a
// non-integer Prev value is rejected.
func TestXrefFollowPrevChainNonIntegerPrev(t *testing.T) {
	r := makeReaderFromBytes([]byte("%PDF-1.4\n"))
	trailer := dict{
		"Prev": name("notAnInt"), // error path
	}

	_, err := followXrefTablePrevChain(r, nil, trailer)
	if err == nil {
		t.Fatal("non-integer Prev: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not integer") {
		t.Errorf("non-integer Prev: unexpected error: %v", err)
	}
}

// TestXrefFollowPrevChainBrokenLink verifies that a Prev offset pointing at
// non-xref data propagates an error through the chain.  The file content must
// contain whitespace so the lexer can finish reading the first keyword token
// without panicking on EOF; we then rely on the "xref Prev does not point to
// xref" error returned when the token is not the "xref" keyword.
func TestXrefFollowPrevChainBrokenLink(t *testing.T) {
	// "NOTANXREF " — space terminates keyword scan; "xref" check will fail.
	fileBytes := bytes.Repeat([]byte("NOTANXREF "), 8)
	r := makeReaderFromBytes(fileBytes)
	trailer := dict{
		"Prev": int64(0), // error path
	}

	_, err := followXrefTablePrevChain(r, nil, trailer)
	if err == nil {
		t.Fatal("broken Prev link: expected error, got nil")
	}
}

// --- TestXrefReadTable* -------------------------------------------------------

// TestXrefReadTableMultiSection exercises readXrefTable directly with two xref
// subsections and a well-formed trailer.  Verifies that both sections are
// loaded into the returned table.
func TestXrefReadTableMultiSection(t *testing.T) {
	sec1 := buildXrefTableSection(0, [][3]int64{
		{0, 65535, 'f'},
		{100, 0, 'n'},
	})
	sec2 := buildXrefTableSection(3, [][3]int64{
		{200, 0, 'n'},
		{300, 0, 'n'},
	})
	trailerBytes := []byte("trailer\n<< /Size 5 >>\n")
	data := append(append(sec1, sec2...), trailerBytes...)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := makeReaderFromBytes(data)

	table, _, _, err := readXrefTable(r, b)
	if err != nil {
		t.Fatalf("readXrefTable: unexpected error: %v", err)
	}
	if len(table) < 5 {
		t.Fatalf("expected table len >= 5, got %d", len(table))
	}
	if table[1].offset != 100 {
		t.Errorf("slot 1: expected offset 100, got %d", table[1].offset)
	}
	if table[3].offset != 200 {
		t.Errorf("slot 3: expected offset 200, got %d", table[3].offset)
	}
}

// TestXrefReadTableFreeEntry verifies that a free ('f') xref entry does NOT
// set the slot's offset (it must remain 0).
func TestXrefReadTableFreeEntry(t *testing.T) {
	sec := buildXrefTableSection(0, [][3]int64{
		{0, 65535, 'f'},
		{50, 0, 'n'},
	})
	trailerBytes := []byte("trailer\n<< /Size 2 >>\n")
	data := append(sec, trailerBytes...)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := makeReaderFromBytes(data)

	table, _, _, err := readXrefTable(r, b)
	if err != nil {
		t.Fatalf("readXrefTable: unexpected error: %v", err)
	}
	if len(table) < 2 {
		t.Fatalf("expected table len >= 2, got %d", len(table))
	}
	// Free entry: offset must stay 0.
	if table[0].offset != 0 {
		t.Errorf("slot 0 (free): expected offset 0, got %d", table[0].offset)
	}
	if table[1].offset != 50 {
		t.Errorf("slot 1: expected offset 50, got %d", table[1].offset)
	}
}

// TestXrefReadTableNoTrailerDict exercises the "trailer not a dict" error path
// in readXrefTable by feeding a "null" object instead of a dict after the xref
// section data.
func TestXrefReadTableNoTrailerDict(t *testing.T) {
	sec := buildXrefTableSection(0, [][3]int64{{0, 65535, 'f'}})
	// "null" is not a dict → triggers the !ok branch.
	trailerBytes := []byte("trailer\nnull\nstartxref\n0\n%%%%EOF\n")
	data := append(sec, trailerBytes...)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := makeReaderFromBytes(data)

	_, _, _, err := readXrefTable(r, b)
	if err == nil {
		t.Fatal("expected error for non-dict trailer, got nil")
	}
	if !strings.Contains(err.Error(), "trailer dictionary") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestXrefReadTableSizeTruncates exercises the size < len(table) truncation
// branch: the table holds 5 entries but /Size is 2, so the returned slice
// must be truncated to length 2.
func TestXrefReadTableSizeTruncates(t *testing.T) {
	sec := buildXrefTableSection(0, [][3]int64{
		{0, 65535, 'f'},
		{100, 0, 'n'},
		{200, 0, 'n'},
		{300, 0, 'n'},
		{400, 0, 'n'},
	})
	// /Size 2 is less than the 5 entries in the section → truncation.
	trailerBytes := []byte("trailer\n<< /Size 2 >>\n")
	data := append(sec, trailerBytes...)
	b := newBuffer(bytes.NewReader(data), 0)
	b.allowEOF = true
	r := makeReaderFromBytes(data)

	table, _, _, err := readXrefTable(r, b)
	if err != nil {
		t.Fatalf("readXrefTable: unexpected error: %v", err)
	}
	if len(table) != 2 {
		t.Errorf("expected table truncated to size 2, got len %d", len(table))
	}
}

// --- FuzzXrefTable -----------------------------------------------------------

// FuzzXrefTable feeds arbitrary bytes as the body of an xref-table buffer to
// readXrefTableData.  The Go fuzz engine detects panics natively; no
// recover() wrapper is used.
func FuzzXrefTable(f *testing.F) {
	// Seed: minimal valid xref block (one free entry, trailer).
	f.Add(append(
		buildXrefTableSection(0, [][3]int64{{0, 65535, 'f'}}),
		[]byte("trailer\n")...,
	))
	// Seed: two entries — free + in-use.
	f.Add(append(
		buildXrefTableSection(0, [][3]int64{
			{0, 65535, 'f'},
			{9, 0, 'n'},
		}),
		[]byte("trailer\n")...,
	))
	// Seed: completely empty input.
	f.Add([]byte("trailer\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// readXrefTableData drives readToken/readObject below the open-path recover
		// boundary and panics-as-error on malformed input; only a runtime fault is
		// a real bug here.
		defer recoverIntentionalParserPanic(t)
		b := newBuffer(bytes.NewReader(data), 0)
		b.allowEOF = true
		//nolint:errcheck
		readXrefTableData(b, nil) //nolint:errcheck,gosec
	})
}

// --- validatePDFHeader ----------------------------------------------------------

// TestValidatePDFHeaderVersions covers the accepted version range: 1.0–1.7
// and 2.0 pass; 1.8, 2.1, a bad terminator, and a non-PDF prefix fail.
func TestValidatePDFHeaderVersions(t *testing.T) {
	cases := []struct {
		hdr string
		ok  bool
	}{
		{"%PDF-1.0\n", true},
		{"%PDF-1.7\n", true},
		{"%PDF-2.0\n", true},
		{"%PDF-2.0 \n", true}, // space terminator
		{"%PDF-1.8\n", false},
		{"%PDF-2.1\n", false},
		{"%PDF-2.0x", false}, // bad terminator
		{"%XDF-1.4\n", false},
	}
	for _, c := range cases {
		err := validatePDFHeader(bytes.NewReader([]byte(c.hdr)))
		if c.ok && err != nil {
			t.Errorf("%q: unexpected error: %v", c.hdr, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%q: want error, got nil", c.hdr)
		}
	}
}

// --- applyXrefStm (hybrid-reference files) ---------------------------------------

// TestXrefTableXRefStmMerge verifies that a hybrid-reference table merges
// entries from the supplemental cross-reference stream named by /XRefStm
// (ISO 32000-1 §7.5.8.4): slots the table leaves free are filled from the
// stream, and entries set by the table win.
func TestXrefTableXRefStmMerge(t *testing.T) {
	// Supplemental xref stream at offset 0, W=[1,2,1], 4 slots:
	//   slot 0: free; slot 1: type-1 offset=200 (must lose to the table);
	//   slot 2: type-1 offset=77 (fills the slot the table marks free);
	//   slot 3: type-2 in object stream 9 at index 1 — the objstm-resident
	//   "hidden object" case hybrid-reference files exist for.
	entries := [][]byte{
		{0x00, 0x00, 0x00, 0x00},
		{0x01, 0x00, 0xC8, 0x00}, // 0xC8 = 200
		{0x01, 0x00, 0x4D, 0x00}, // 0x4D = 77
		{0x02, 0x00, 0x09, 0x01}, // type-2: stream obj 9, index 1
	}
	stmBlock := xrefStreamBuildPrevBlock(99, 4, entries, "")

	// Hybrid classic table after the stream block: slot 1 in-use at 100,
	// slots 0, 2, and 3 free; trailer names the stream via /XRefStm.
	var fileBuf bytes.Buffer
	fileBuf.Write(stmBlock)
	tableOff := int64(fileBuf.Len())
	fileBuf.WriteString("xref\n")
	fileBuf.Write(buildXrefTableSection(0, [][3]int64{
		{0, 65535, 'f'},
		{100, 0, 'n'},
		{0, 0, 'f'},
		{0, 0, 'f'},
	}))
	fileBuf.WriteString("trailer\n<< /Size 4 /XRefStm 0 >>\nstartxref\n0\n%%EOF\n")

	data := fileBuf.Bytes()
	r := makeReaderFromBytes(data)
	b := newBuffer(bytes.NewReader(data[tableOff:]), tableOff)
	b.allowEOF = true

	table, _, trailer, err := readXref(r, b)
	if err != nil {
		t.Fatalf("readXref: %v", err)
	}
	if trailer["XRefStm"] != int64(0) {
		t.Fatalf("trailer lost XRefStm: %v", trailer)
	}
	if table[1].offset != 100 {
		t.Errorf("slot 1: got offset %d, want 100 (table entry must win)", table[1].offset)
	}
	if table[2].offset != 77 {
		t.Errorf("slot 2: got offset %d, want 77 (filled from XRefStm)", table[2].offset)
	}
	want3 := xref{ptr: objptr{3, 0}, inStream: true, stream: objptr{9, 0}, offset: 1}
	if table[3] != want3 {
		t.Errorf("slot 3: got %+v, want %+v (type-2 objstm entry from XRefStm)", table[3], want3)
	}
}

// TestXrefTableXRefStmInvalid verifies that a /XRefStm offset that does not
// point at a valid XRef stream is reported as an error. The target is a
// well-formed object that is not a stream, so the failure surfaces as an
// error rather than a lexer panic (which only NewReader recovers).
func TestXrefTableXRefStmInvalid(t *testing.T) {
	var fileBuf bytes.Buffer
	fileBuf.WriteString("1 0 obj\n<< /Type /Catalog >>\nendobj\n")
	tableOff := int64(fileBuf.Len())
	fileBuf.WriteString("xref\n")
	fileBuf.Write(buildXrefTableSection(0, [][3]int64{{0, 65535, 'f'}}))
	fileBuf.WriteString("trailer\n<< /Size 1 /XRefStm 0 >>\nstartxref\n0\n%%EOF\n")

	data := fileBuf.Bytes()
	r := makeReaderFromBytes(data)
	b := newBuffer(bytes.NewReader(data[tableOff:]), tableOff)
	b.allowEOF = true

	_, _, _, err := readXref(r, b)
	if err == nil {
		t.Fatal("invalid XRefStm: want error, got nil")
	}
	if !strings.Contains(err.Error(), "XRefStm") {
		t.Errorf("error should mention XRefStm, got: %v", err)
	}
}
