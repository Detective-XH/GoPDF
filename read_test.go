package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReadObjectMaxDepth(t *testing.T) {
	const depth = maxObjectDepth + 100
	var payload bytes.Buffer
	for i := 0; i < depth; i++ {
		payload.WriteString("0 0 obj\n")
	}
	b := newBuffer(&payload, 0)
	b.allowEOF = true
	panicked := false
	var panicMsg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				panicMsg = r.(error).Error()
			}
		}()
		b.readObject()
	}()
	if !panicked {
		t.Fatal("expected panic from deeply nested objects, but readObject returned normally")
	}
	if !strings.Contains(panicMsg, "maximum depth") {
		t.Fatalf("expected 'maximum depth' in panic message, got: %s", panicMsg)
	}
}

func TestReadDictMaxDepth(t *testing.T) {
	var payload bytes.Buffer
	const depth = maxObjectDepth + 100
	for i := 0; i < depth; i++ {
		payload.WriteString("<< /A ")
	}
	payload.WriteString("null")
	for i := 0; i < depth; i++ {
		payload.WriteString(" >>")
	}
	b := newBuffer(&payload, 0)
	b.allowEOF = true
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		b.readObject()
	}()
	if !panicked {
		t.Fatal("expected panic from deeply nested dicts, but readObject returned normally")
	}
}

func TestReadArrayMaxDepth(t *testing.T) {
	var payload bytes.Buffer
	const depth = maxObjectDepth + 100
	for i := 0; i < depth; i++ {
		payload.WriteString("[ ")
	}
	payload.WriteString("null")
	for i := 0; i < depth; i++ {
		payload.WriteString(" ]")
	}
	b := newBuffer(&payload, 0)
	b.allowEOF = true
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		b.readObject()
	}()
	if !panicked {
		t.Fatal("expected panic from deeply nested arrays, but readObject returned normally")
	}
}

func TestReadObjectNormalDepth(t *testing.T) {
	var payload bytes.Buffer
	const depth = 50
	for i := 0; i < depth; i++ {
		payload.WriteString("<< /A ")
	}
	payload.WriteString("(hello)")
	for i := 0; i < depth; i++ {
		payload.WriteString(" >>")
	}
	b := newBuffer(&payload, 0)
	b.allowEOF = true
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		obj := b.readObject()
		if obj == nil {
			t.Fatal("expected non-nil object from moderately nested dict")
		}
	}()
	if panicked {
		t.Fatal("unexpected panic from moderately nested dicts")
	}
}

// testStream wraps raw bytes as a Value of Kind Stream (no filter).
func testStream(data []byte) Value {
	r := &Reader{f: bytes.NewReader(data), end: int64(len(data))}
	s := stream{hdr: dict{name("Length"): int64(len(data))}, offset: 0}
	return Value{r, objptr{}, s}
}

// TestReadHexStringEOF confirms readHexString terminates when the stream ends
// before the closing '>'.  Without an EOF guard the function spins forever:
// readByte() returns '\n' (a space) after EOF, isSpace('\n') is true, and
// `goto Loop` loops indefinitely without advancing the stream position.
// The input must be all-whitespace after '<' so the loop spins rather than
// hitting the errorf() path (which only fires on invalid hex pairs).
func TestReadHexStringEOF(t *testing.T) {
	// '<' followed by whitespace only — no closing '>'.
	// After all bytes are consumed, readByte returns '\n' (EOF sentinel),
	// isSpace('\n')==true → goto Loop → infinite spin without the fix.
	b := newBuffer(strings.NewReader("<   \t  "), 0)
	b.allowEOF = true

	done := make(chan token, 1)
	go func() { done <- b.readToken() }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readHexString hung: missing EOF guard in readHexString")
	}
}

// TestInterpretInlineImage confirms that Interpret skips inline-image binary
// data (BI … ID <binary> EI) and continues processing operators after EI.
// The binary blob deliberately contains 0x3c ('<') to trigger the lexer path
// that hangs without the ID skip + readHexString EOF guard.
func TestInterpretInlineImage(t *testing.T) {
	binary := []byte{0x3c, 0xff, 0x00, 0x3c, 0x41} // contains '<' (0x3c)
	var buf bytes.Buffer
	buf.WriteString("BI\n/W 10\n/H 10\nID\n")
	buf.Write(binary)
	buf.WriteString("\nEI\n(hello) Tj\n")

	strm := testStream(buf.Bytes())

	type result struct {
		ops []string
		err interface{}
	}
	ch := make(chan result, 1)
	go func() {
		var ops []string
		var panicVal interface{}
		func() {
			defer func() { panicVal = recover() }()
			Interpret(strm, func(stk *Stack, op string) {
				ops = append(ops, op)
			})
		}()
		ch <- result{ops, panicVal}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("Interpret panicked: %v", r.err)
		}
		found := false
		for _, op := range r.ops {
			if op == "Tj" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("'Tj' not seen after inline image EI; ops: %v", r.ops)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Interpret hung on content stream with inline image (binary data contains 0x3c)")
	}
}

// TestReadLiteralStringEscapes verifies appendEscape handles all escape
// families: named (\n), octal (\101 = 'A'), literal-paren \(, and
// literal-backslash \\. These branches have no other test coverage.
func TestReadLiteralStringEscapes(t *testing.T) {
	// PDF string: (a\nb\101c\(d\\e)
	b := newBuffer(strings.NewReader("(a\\nb\\101c\\(d\\\\e)"), 0)
	b.allowEOF = true
	tok := b.readToken()
	got, ok := tok.(string)
	if !ok {
		t.Fatalf("expected string token, got %T(%v)", tok, tok)
	}
	want := "a\nbAc(d\\e"
	if got != want {
		t.Fatalf("readLiteralString escapes: got %q, want %q", got, want)
	}
}

// TestReadLiteralStringLineContinuation verifies that \<CR>, \<LF>, and
// \<CR><LF> are all treated as line-continuation (no character appended).
func TestReadLiteralStringLineContinuation(t *testing.T) {
	// "\\\r\\\n\\\r\n" → three line continuations → empty string
	b := newBuffer(strings.NewReader("(\\\r\\\n\\\r\n)"), 0)
	b.allowEOF = true
	tok := b.readToken()
	got, ok := tok.(string)
	if !ok {
		t.Fatalf("expected string token, got %T(%v)", tok, tok)
	}
	if got != "" {
		t.Fatalf("line continuations should yield empty string, got %q", got)
	}
}

// TestInterpretDictStack exercises the execPS dict-stack path:
// begin/def/end store a value; symbol lookup retrieves it without calling do;
// end closes the dict; unknown operators beyond the dict scope reach do.
func TestInterpretDictStack(t *testing.T) {
	// "1 dict begin" — create dict, open it.
	// "/K 99 def"    — store 99 under key K.
	// "K"            — resolved via dict lookup → pushed, NOT dispatched to do.
	// "pop"          — handled by execPS, discards the 99.
	// "end"          — closes the dict.
	// "sentinel"     — unknown keyword; must reach do.
	const ps = "1 dict begin /K 99 def K pop end sentinel"
	var ops []string
	Interpret(testStream([]byte(ps)), func(stk *Stack, op string) {
		ops = append(ops, op)
	})
	if len(ops) != 1 || ops[0] != "sentinel" {
		t.Fatalf("expected [sentinel], got %v", ops)
	}
}

// buildTextPDF returns a minimal single-page PDF whose content stream is
// contentStream.  The page declares Helvetica (/F1) so Tf operators work.
// Length is len(contentStream)+1 to account for the \n written before endstream.
func buildTextPDF(contentStream string) []byte {
	var b strings.Builder
	offsets := make([]int, 6) // offsets[1..5] for objects 1-5

	b.WriteString("%PDF-1.4\n")

	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	offsets[4] = b.Len()
	fmt.Fprintf(&b, "4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n",
		len(contentStream)+1, contentStream)

	offsets[5] = b.Len()
	b.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	xrefOff := b.Len()
	fmt.Fprintf(&b, "xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	return []byte(b.String())
}

// TestGetPlainTextNoBTNewline is a regression guard for upstream #48:
// a past commit added showText("\n") inside case "BT", prepending a newline
// before every text object.  BT is a matrix-initialisation operator (PDF
// spec §9.4.1) and should not emit whitespace.
func TestGetPlainTextNoBTNewline(t *testing.T) {
	data := buildTextPDF("BT /F1 12 Tf (Hello) Tj ET")
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("BT operator injected newline into output; got %q", got)
	}
}

// TestGetPlainTextBTNoSeparator pins the pre-826abbb behaviour: adjacent
// BT/ET blocks are not separated by a newline.
func TestGetPlainTextBTNoSeparator(t *testing.T) {
	data := buildTextPDF("BT /F1 12 Tf (Hello) Tj ET\nBT (World) Tj ET")
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatalf("GetPlainText: %v", err)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("adjacent BT/ET blocks should not produce newlines; got %q", got)
	}
}

// TestOpenBytesSpaceAfterHeader confirms that PDFs with a space between the
// version string and the newline (e.g. libtiff/tiff2pdf output) are accepted.
// Upstream issue #22.
func TestOpenBytesSpaceAfterHeader(t *testing.T) {
	// Build a minimal valid PDF but with "%PDF-1.4 \n" header (space before newline).
	base := buildTextPDF("BT (Hi) Tj ET")
	// Replace the header newline with space+newline.
	src := strings.Replace(string(base), "%PDF-1.4\n", "%PDF-1.4 \n", 1)
	_, err := OpenBytes([]byte(src))
	if err != nil {
		t.Fatalf("OpenBytes rejected PDF with space after header: %v", err)
	}
}

// TestOpenBytesEOFBeyond100 confirms that PDFs with %%EOF more than 100 bytes
// before the end (e.g. libtiff-generated files with trailing newlines) are
// accepted under the expanded 1024-byte search window.  Upstream issue #20.
func TestOpenBytesEOFBeyond100(t *testing.T) {
	data := buildTextPDF("BT /F1 12 Tf (hello) Tj ET")
	// Append 400 newlines after %%EOF, pushing it 400 bytes before the end.
	data = append(data, bytes.Repeat([]byte{'\n'}, 400)...)
	if _, err := OpenBytes(data); err != nil {
		t.Fatalf("OpenBytes rejected PDF with %%EOF >100 bytes before end: %v", err)
	}
}

// TestOpenBytesEOFBeyond1024 confirms that PDFs with %%EOF more than 1024 bytes
// before the end are handled by the fallback full-file reverse scan.
// Upstream issue #20.
func TestOpenBytesEOFBeyond1024(t *testing.T) {
	data := buildTextPDF("BT /F1 12 Tf (hello) Tj ET")
	// Append 1500 non-whitespace bytes after %%EOF so TrimRight cannot expose it,
	// forcing the fallback reverse-scan path.
	data = append(data, bytes.Repeat([]byte{'x'}, 1500)...)
	if _, err := OpenBytes(data); err != nil {
		t.Fatalf("OpenBytes rejected PDF with %%EOF >1024 bytes before end: %v", err)
	}
}

// TestDecryptStringMisalignedAES verifies that decryptString returns "" rather
// than panicking when the AES ciphertext is shorter than one block (< 16 bytes)
// or has a tail that is not a full block multiple after the IV.
func TestDecryptStringMisalignedAES(t *testing.T) {
	key := make([]byte, 16)
	if got := decryptString(key, true, objptr{}, string(make([]byte, 10))); got != "" {
		t.Errorf("short ciphertext: want \"\", got %q", got)
	}
	if got := decryptString(key, true, objptr{}, string(make([]byte, 20))); got != "" {
		t.Errorf("misaligned ciphertext (20 bytes): want \"\", got %q", got)
	}
}

// TestEnsureXrefSlotLimit verifies that ensureXrefSlot returns an error for
// object numbers exceeding maxXrefObjects rather than growing a huge slice.
func TestEnsureXrefSlotLimit(t *testing.T) {
	if _, err := ensureXrefSlot(nil, maxXrefObjects+1); err == nil {
		t.Error("expected error for object number > maxXrefObjects, got nil")
	}
	if _, err := ensureXrefSlot(nil, -1); err == nil {
		t.Error("expected error for negative object number, got nil")
	}
}

// TestFlateDecode_ColumnsLimit verifies that applyFilter returns an error for a
// Columns value exceeding maxPNGColumns rather than allocating a huge slice.
func TestFlateDecode_ColumnsLimit(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write([]byte("x"))
	_ = zw.Close()

	r := &Reader{f: bytes.NewReader(nil), end: 0}
	param := Value{r, objptr{}, dict{
		name("Predictor"): int64(12),
		name("Columns"):   int64(maxPNGColumns + 1),
	}}
	if _, err := applyFilter(bytes.NewReader(buf.Bytes()), "FlateDecode", param); err == nil {
		t.Error("expected error for Columns > maxPNGColumns, got nil")
	}
}

func TestNewReaderMaliciousPDF(t *testing.T) {
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.0\n")
	for range 10_000 {
		pdf.WriteString("0\n0\nobj\n")
	}
	pdf.WriteString("startxref\n0\n%%EOF\n")
	data := pdf.Bytes()
	_, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected error from malicious PDF, got nil")
	}
}

// ---- findStartxrefFallback tests ----------------------------------------

// TestFindStartxrefFallbackBasic verifies that the fallback finds %%EOF and
// returns the offset of the preceding startxref line.  The payload is larger
// than the standard 1024-byte endChunk window so that findStartxrefOffset
// would actually call the fallback, but we test findStartxrefFallback directly.
func TestFindStartxrefFallbackBasic(t *testing.T) {
	// \n before startxref satisfies findLastLine's i>0 && buf[i-1]=='\n' guard.
	// \n after startxref satisfies buf[i+len("startxref")]=='\n' guard.
	// 42\n occupies the line between startxref and %%EOF.
	data := []byte("\nstartxref\n42\n%%EOF\nsome extra bytes appended after eof")
	r := bytes.NewReader(data)
	offset, err := findStartxrefFallback(r, int64(len(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The returned offset must point at the 's' of "startxref".
	if string(data[offset:offset+9]) != "startxref" {
		t.Errorf("offset %d does not point to 'startxref'; got %q", offset, string(data[offset:offset+9]))
	}
}

// TestFindStartxrefFallbackEOFBeforeEnd verifies the case where %%EOF is NOT
// within the last endChunk bytes — more trailing data exists after it.
// This is the canonical "non-conformant PDF with appended signature" scenario.
func TestFindStartxrefFallbackEOFBeforeEnd(t *testing.T) {
	// 1500 'x' bytes after %%EOF pushes it well beyond the 1024-byte window.
	base := []byte("\nstartxref\n99\n%%EOF\n")
	data := append(base, bytes.Repeat([]byte("x"), 1500)...)
	r := bytes.NewReader(data)
	offset, err := findStartxrefFallback(r, int64(len(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data[offset:offset+9]) != "startxref" {
		t.Errorf("offset %d does not point to 'startxref'; got %q", offset, string(data[offset:offset+9]))
	}
}

// TestFindStartxrefFallbackNoStartxref verifies that %%EOF present but no
// preceding startxref line returns a specific error.
func TestFindStartxrefFallbackNoStartxref(t *testing.T) {
	// %%EOF is present but there is nothing before it in the 512-byte context.
	data := []byte("%%EOF\n")
	r := bytes.NewReader(data)
	_, err := findStartxrefFallback(r, int64(len(data)))
	if err == nil {
		t.Fatal("expected error when startxref is absent, got nil")
	}
	if !strings.Contains(err.Error(), "startxref missing") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestFindStartxrefFallbackNoEOF verifies that a file with no %%EOF at all
// returns the "not found" error.
func TestFindStartxrefFallbackNoEOF(t *testing.T) {
	data := []byte("this is not a pdf at all")
	r := bytes.NewReader(data)
	_, err := findStartxrefFallback(r, int64(len(data)))
	if err == nil {
		t.Error("expected error when percent-EOF is absent, got nil")
		return
	}
	if !strings.Contains(err.Error(), "EOF not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestFindStartxrefFallbackChunkBoundary verifies that %%EOF straddling a
// 4096-byte chunk boundary is found via the 4-byte overlap mechanism.
//
// Construction: head (18 bytes) + 4094 'x' bytes = 4112 bytes total.
// Chunk boundary = 4112-4096 = 16.  %%EOF starts at offset 13 (inside head),
// so the split is at position 16, which cuts through the middle of "%%EOF".
// The overlap stitches the two chunks together and %%EOF is found.
func TestFindStartxrefFallbackChunkBoundary(t *testing.T) {
	// head: \n(0) s(1)..f(9) \n(10) 9(11) \n(12) %(13)%(14)E(15)O(16)F(17)
	head := []byte("\nstartxref\n9\n%%EOF")
	data := append(head, bytes.Repeat([]byte("x"), 4094)...)
	// Sanity: %%EOF starts at index 13 within head.
	if string(data[13:18]) != "%%EOF" {
		t.Fatalf("test construction error: %%EOF not at expected offset 13")
	}
	r := bytes.NewReader(data)
	offset, err := findStartxrefFallback(r, int64(len(data)))
	if err != nil {
		t.Fatalf("unexpected error on chunk-boundary case: %v", err)
	}
	if string(data[offset:offset+9]) != "startxref" {
		t.Errorf("offset %d does not point to 'startxref'; got %q", offset, string(data[offset:offset+9]))
	}
	// startxref is at index 1 (after the leading \n).
	if offset != 1 {
		t.Errorf("expected offset 1, got %d", offset)
	}
}

// ---- NewReaderEncrypted tests --------------------------------------------

// TestNewReaderEncryptedInvalidHeader verifies that a buffer not starting with
// a valid %PDF-n.m header is rejected immediately.
func TestNewReaderEncryptedInvalidHeader(t *testing.T) {
	buf := []byte("not a pdf\nstartxref\n0\n%%EOF\n")
	_, err := NewReaderEncrypted(bytes.NewReader(buf), int64(len(buf)), nil)
	if err == nil {
		t.Fatal("expected error for invalid PDF header, got nil")
	}
}

// TestNewReaderEncryptedNoStartxref verifies that a valid header followed by
// %%EOF but no startxref line produces an error from findStartxrefOffset.
func TestNewReaderEncryptedNoStartxref(t *testing.T) {
	// %%EOF present but findLastLine cannot find "startxref" → missing final startxref.
	buf := []byte("%PDF-1.4\n%%EOF\n")
	_, err := NewReaderEncrypted(bytes.NewReader(buf), int64(len(buf)), nil)
	if err == nil {
		t.Fatal("expected error when startxref line is absent, got nil")
	}
}

// TestNewReaderEncryptedStartxrefNotInteger verifies that "startxref" followed
// by a non-integer token returns an error.
func TestNewReaderEncryptedStartxrefNotInteger(t *testing.T) {
	// Build a payload: valid header, then startxref followed by a word (not int).
	// findStartxrefOffset will locate the startxref line; the buffer will then
	// try to read the next token as int64 and fail.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	// Padding to push startxref/%%EOF within the last 1024 bytes.
	sxOff := buf.Len()
	buf.WriteString("\nstartxref\nNOTANUMBER\n%%EOF\n")
	_ = sxOff
	data := buf.Bytes()
	_, err := NewReaderEncrypted(bytes.NewReader(data), int64(len(data)), nil)
	if err == nil {
		t.Fatal("expected error when startxref offset is not an integer, got nil")
	}
}

// TestNewReaderEncryptedBrokenXref verifies that a startxref pointing at
// content that is not a valid xref table or stream returns an error from readXref.
func TestNewReaderEncryptedBrokenXref(t *testing.T) {
	// Build payload where the number after startxref points at "garbage\n" which
	// is neither "xref" nor an integer, so readXref returns an error.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	// The "xref content" is replaced by garbage at offset 9.
	garbageOff := buf.Len() // offset 9
	buf.WriteString("GARBAGE JUNK HERE\n")
	// Now write startxref pointing at garbageOff, then %%EOF.
	fmt.Fprintf(&buf, "\nstartxref\n%d\n%%%%EOF\n", garbageOff)
	data := buf.Bytes()
	_, err := NewReaderEncrypted(bytes.NewReader(data), int64(len(data)), nil)
	if err == nil {
		t.Fatal("expected error from broken xref section, got nil")
	}
}

// TestReadOpen verifies that Open reads a real PDF file from disk,
// returns a non-nil *os.File and *Reader, and that NumPage is correct.
func TestReadOpen(t *testing.T) {
	data := buildTextPDF("")
	f, err := os.CreateTemp("", "gopdf-test-*.pdf")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { _ = f.Close(); _ = os.Remove(f.Name()) })
	if _, err := f.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	fh, r, err := Open(f.Name())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = fh.Close() })

	if r.NumPage() != 1 {
		t.Errorf("NumPage = %d, want 1", r.NumPage())
	}
}

// TestReadOpenNonexistent verifies that Open returns a non-nil error when
// the requested file does not exist.
func TestReadOpenNonexistent(t *testing.T) {
	_, _, err := Open("/nonexistent/gopdf-test-file-that-does-not-exist.pdf")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}
