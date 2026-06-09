// filter_test.go — unit, integration, fuzz, and benchmark tests for filter.go.
// Covers: alphaReader, checkASCII85, pngUpReader, applyFilter,
// applyStreamFilters, applyArrayFilters.

package pdf

import (
	"bytes"
	"compress/zlib"
	"io"
	"testing"
)

// makeIntValue builds a Value whose data is an int64 (Kind == Integer).
func makeIntValue(i int64) Value {
	return Value{r: &Reader{f: bytes.NewReader(nil), end: 0}, data: i}
}

// filterMakeDict builds a Value of Kind Dict from a Go map.
func filterMakeDict(m map[string]any) Value {
	r := &Reader{f: bytes.NewReader(nil), end: 0}
	d := make(dict)
	for k, v := range m {
		d[name(k)] = v
	}
	return Value{r, objptr{}, d}
}

// filterMakeArray builds a Value of Kind Array from the provided objects.
func filterMakeArray(elems ...any) Value {
	r := &Reader{f: bytes.NewReader(nil), end: 0}
	a := make(array, len(elems))
	for i, e := range elems {
		a[i] = e
	}
	return Value{r, objptr{}, a}
}

// filterMakeName returns a Value of Kind Name.
func filterMakeName(n string) Value {
	return Value{nil, objptr{}, name(n)}
}

// zlibCompress compresses src using zlib and returns the compressed bytes.
func zlibCompress(src []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(src)
	_ = w.Close()
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// TestFlateDecodeRoundtrip
// ---------------------------------------------------------------------------

// TestFlateDecodeRoundtrip compresses a payload with zlib.NewWriter then
// decodes it through applyFilter("FlateDecode") and verifies byte equality.
func TestFlateDecodeRoundtrip(t *testing.T) {
	original := []byte("the quick brown fox jumps over the lazy dog\n")
	compressed := zlibCompress(original)

	rd, err := applyFilter(bytes.NewReader(compressed), "FlateDecode", Value{})
	if err != nil {
		t.Fatalf("applyFilter FlateDecode: %v", err)
	}

	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll after FlateDecode: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("roundtrip mismatch: got %q, want %q", got, original)
	}
}

// ---------------------------------------------------------------------------
// TestFlateDecodePNGUp
// ---------------------------------------------------------------------------

// TestFlateDecodePNGUp verifies that pngUpReader correctly decodes a
// hand-crafted PNG-Up (predictor 12) encoded stream.
//
// PNG Up filter (predictor 2): each row begins with a filter-type byte (0x02),
// followed by delta bytes relative to the previous row.  pngUpReader adds
// the deltas to the running history to reconstruct the original row.
//
// This test exercises pngUpReader.Read at the unit level (0% coverage target).
func TestFlateDecodePNGUp(t *testing.T) {
	// Two columns (Columns=2), so each row is 3 bytes: [filter-byte, col0, col1].
	// Row 0: filter=2, delta=[10, 20]  → decoded row = [10, 20]
	// Row 1: filter=2, delta=[ 3,  5]  → decoded row = [13, 25]
	const columns = 2
	rawRows := []byte{
		2, 10, 20, // row 0: up-filter type byte + deltas
		2, 3, 5, // row 1
	}

	// Wrap the raw rows in zlib so applyFilter can open it.
	compressed := zlibCompress(rawRows)

	param := filterMakeDict(map[string]any{
		"Predictor": int64(12),
		"Columns":   int64(columns),
	})

	rd, err := applyFilter(bytes.NewReader(compressed), "FlateDecode", param)
	if err != nil {
		t.Fatalf("applyFilter FlateDecode+PNG-Up: %v", err)
	}

	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll PNG-Up: %v", err)
	}

	want := []byte{10, 20, 13, 25}
	if !bytes.Equal(got, want) {
		t.Fatalf("PNG-Up decode: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestFlateDecodeMaxSize
// ---------------------------------------------------------------------------

// TestFlateDecodeMaxSize verifies that corrupt zlib data returns an error and
// that legitimate payloads under the cap decompress correctly.  The
// LimitReader at maxDecompressedSize prevents OOM on adversarial inputs.
func TestFlateDecodeMaxSize(t *testing.T) {
	// error path: corrupt zlib data must return an error from applyFilter.
	_, err := applyFilter(bytes.NewReader([]byte("not zlib")), "FlateDecode", Value{}) // error path
	if err == nil {
		t.Fatal("expected error for non-zlib input, got nil")
	}

	// Verify LimitReader ceiling: build a 1 MB payload (well below the 256 MB
	// cap) and confirm full read succeeds.
	payload := bytes.Repeat([]byte("A"), 1<<20) // 1 MB
	compressed := zlibCompress(payload)

	rd, err := applyFilter(bytes.NewReader(compressed), "FlateDecode", Value{})
	if err != nil {
		t.Fatalf("applyFilter FlateDecode 1 MB: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll 1 MB: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("expected %d bytes, got %d", len(payload), len(got))
	}
}

// ---------------------------------------------------------------------------
// TestASCII85DecodeBasic
// ---------------------------------------------------------------------------

// TestASCII85DecodeBasic verifies the full ASCII85Decode pipeline through
// applyFilter using a known round-trip.  The newAlphaReader is exercised as
// part of the pipeline (0% coverage target).
//
// We encode with the inline simpleASCII85Encoder (defined below) so there is
// no dependency on an external test file.
func TestASCII85DecodeBasic(t *testing.T) {
	original := []byte("Hello, world!")

	// Encode with the local helper and append the tilde-gt end-of-data marker.
	var encBuf bytes.Buffer
	enc := &simpleASCII85Encoder{w: &encBuf}
	_, _ = enc.Write(original)
	_ = enc.Close()
	encoded := append(encBuf.Bytes(), '~', '>')

	// Exercise alphaReader directly: it should pass ASCII85 chars through.
	ar := newAlphaReader(bytes.NewReader(encoded))
	var arBuf bytes.Buffer
	_, err := io.Copy(&arBuf, ar)
	if err != nil {
		t.Fatalf("alphaReader.Read: %v", err)
	}
	if arBuf.Len() == 0 {
		t.Fatal("alphaReader returned no bytes")
	}

	// Full pipeline through applyFilter.
	rd, err := applyFilter(bytes.NewReader(encoded), "ASCII85Decode", Value{})
	if err != nil {
		t.Fatalf("applyFilter ASCII85Decode: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll ASCII85Decode: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("ASCII85 decode: got %q, want %q", got, original)
	}
}

// simpleASCII85Encoder is a minimal streaming ASCII85 encoder used only in
// tests.  It avoids importing encoding/ascii85 at the test-file level while
// still exercising the full alphaReader + ascii85.NewDecoder pipeline.
type simpleASCII85Encoder struct {
	w   io.Writer
	buf [4]byte
	n   int
}

func (e *simpleASCII85Encoder) Write(p []byte) (int, error) {
	total := 0
	for _, b := range p {
		e.buf[e.n] = b
		e.n++
		if e.n == 4 {
			if err := e.flushGroup4(); err != nil {
				return total, err
			}
		}
		total++
	}
	return total, nil
}

func (e *simpleASCII85Encoder) flushGroup4() error {
	v := uint32(e.buf[0])<<24 | uint32(e.buf[1])<<16 | uint32(e.buf[2])<<8 | uint32(e.buf[3])
	e.n = 0
	if v == 0 {
		_, err := e.w.Write([]byte{'z'})
		return err
	}
	var out [5]byte
	for i := 4; i >= 0; i-- {
		out[i] = byte(v%85) + '!'
		v /= 85
	}
	_, err := e.w.Write(out[:])
	return err
}

func (e *simpleASCII85Encoder) Close() error {
	if e.n == 0 {
		return nil
	}
	src := e.buf
	for i := e.n; i < 4; i++ {
		src[i] = 0
	}
	v := uint32(src[0])<<24 | uint32(src[1])<<16 | uint32(src[2])<<8 | uint32(src[3])
	var out [5]byte
	for i := 4; i >= 0; i-- {
		out[i] = byte(v%85) + '!'
		v /= 85
	}
	_, err := e.w.Write(out[:e.n+1])
	return err
}

// ---------------------------------------------------------------------------
// TestASCII85CheckByte
// ---------------------------------------------------------------------------

// TestASCII85CheckByte exercises checkASCII85 across all boundary cases:
// valid range '!' (33) to 'u' (117), the tilde sentinel (~), and
// out-of-range bytes.  All branches reach 0% coverage before this test.
func TestASCII85CheckByte(t *testing.T) {
	cases := []struct {
		input byte
		want  byte
		label string
	}{
		{'!', '!', "lower boundary of valid range"},
		{'u', 'u', "upper boundary of valid range"},
		{'A', 'A', "mid-range valid ASCII85 char"},
		{'z', 'z', "all-zero-group shorthand passes through"},
		{'~', 1, "tilde returns sentinel 1"},
		{' ', 0, "space is out-of-range, returns 0"},
		{0x00, 0, "NUL byte is out-of-range"},
		{'v', 0, "one above upper boundary 'u'+1"},
		{0xFF, 0, "high byte out of range"},
	}
	for _, tc := range cases {
		got := checkASCII85(tc.input)
		if got != tc.want {
			t.Errorf("checkASCII85(%q) = %d, want %d (%s)", tc.input, got, tc.want, tc.label)
		}
	}
}

// TestASCII85DecodeZShorthand verifies the 'z' all-zero-group shorthand decodes
// to four zero bytes through the full applyFilter pipeline. Regression guard:
// checkASCII85 previously mapped 'z' to 0, so alphaReader stripped it and the
// four zero bytes were silently dropped before the stdlib decoder could expand it.
func TestASCII85DecodeZShorthand(t *testing.T) {
	rd, err := applyFilter(bytes.NewReader([]byte("z~>")), "ASCII85Decode", Value{})
	if err != nil {
		t.Fatalf("applyFilter ASCII85Decode: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll ASCII85Decode: %v", err)
	}
	want := []byte{0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("ASCII85 z-shorthand: got %v, want %v", got, want)
	}
}

// TestASCII85DecodeWhitespace verifies that whitespace-wrapped ASCII85 (the
// conventional column wrapping found in real PDFs) decodes cleanly. alphaReader
// zeroes interleaved whitespace and the stdlib decoder skips 0x00 exactly as it
// skips whitespace. This is a deliberate guard against "fixing" alphaReader to
// strip-and-compact: a claimed whitespace→0x00 corruption was REFUTED, and this
// test fails if that no-op behaviour is ever broken.
func TestASCII85DecodeWhitespace(t *testing.T) {
	original := []byte("The quick brown fox jumps over the lazy dog, twice over.")

	var encBuf bytes.Buffer
	enc := &simpleASCII85Encoder{w: &encBuf}
	_, _ = enc.Write(original)
	_ = enc.Close()

	// Interleave newlines, spaces, and tabs through the encoded stream; ASCII85
	// whitespace is ignored anywhere, so this must not change the decoded bytes.
	var wrapped bytes.Buffer
	for i, b := range encBuf.Bytes() {
		if i > 0 && i%8 == 0 {
			wrapped.WriteByte('\n')
		}
		wrapped.WriteByte(b)
		if i%5 == 0 {
			wrapped.WriteByte(' ')
		}
	}
	wrapped.WriteString("\t\r\n~>")

	rd, err := applyFilter(bytes.NewReader(wrapped.Bytes()), "ASCII85Decode", Value{})
	if err != nil {
		t.Fatalf("applyFilter ASCII85Decode: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll ASCII85Decode: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("ASCII85 whitespace-wrapped: got %q, want %q", got, original)
	}
}

// TestASCII85DecodeStripsNonASCII85 verifies that bytes outside the ASCII85
// alphabet interleaved into the stream are dropped (zeroed by alphaReader, then
// skipped by the stdlib decoder) and the surrounding data still decodes.
func TestASCII85DecodeStripsNonASCII85(t *testing.T) {
	original := []byte("strip me clean")

	var encBuf bytes.Buffer
	enc := &simpleASCII85Encoder{w: &encBuf}
	_, _ = enc.Write(original)
	_ = enc.Close()

	// Splice a high byte and a NUL into the middle of the encoded stream.
	raw := encBuf.Bytes()
	mid := len(raw) / 2
	var dirty bytes.Buffer
	dirty.Write(raw[:mid])
	dirty.Write([]byte{0xFF, 0x00})
	dirty.Write(raw[mid:])
	dirty.WriteString("~>")

	rd, err := applyFilter(bytes.NewReader(dirty.Bytes()), "ASCII85Decode", Value{})
	if err != nil {
		t.Fatalf("applyFilter ASCII85Decode: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll ASCII85Decode: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("ASCII85 non-alphabet strip: got %q, want %q", got, original)
	}
}

// ---------------------------------------------------------------------------
// TestFilterChain
// ---------------------------------------------------------------------------

// TestFilterChain exercises applyArrayFilters with a two-element filter array,
// the primary 0%-coverage path for that function.
// We chain two FlateDecode passes over doubly-compressed data.
func TestFilterChain(t *testing.T) {
	// Build doubly-compressed payload.
	inner := []byte("filter chain test payload")
	once := zlibCompress(inner)
	twice := zlibCompress(once)

	// filter array: [/FlateDecode, /FlateDecode]
	filterArr := filterMakeArray(name("FlateDecode"), name("FlateDecode"))
	// param array: empty — Index out-of-bounds returns null Value for each.
	paramArr := filterMakeArray()

	rd, err := applyArrayFilters(bytes.NewReader(twice), filterArr, paramArr)
	if err != nil {
		t.Fatalf("applyArrayFilters: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll double-FlateDecode: %v", err)
	}
	if !bytes.Equal(got, inner) {
		t.Fatalf("filter chain: got %q, want %q", got, inner)
	}
}

// TestFilterChainError exercises the error path in applyArrayFilters when
// one of the filters fails.
func TestFilterChainError(t *testing.T) {
	// Filter array contains an unsupported filter name — must return error.
	filterArr := filterMakeArray(name("UnknownFilter"))
	paramArr := filterMakeArray()

	_, err := applyArrayFilters(bytes.NewReader([]byte("data")), filterArr, paramArr) // error path
	if err == nil {
		t.Fatal("expected error for unsupported filter in chain, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestFilterNotStream
// ---------------------------------------------------------------------------

// TestFilterNotStream verifies that Value.Reader() returns a ReadCloser that
// errors (rather than panicking) when the Value is not a Stream.  This
// exercises the errorReadCloser path in Reader().
func TestFilterNotStream(t *testing.T) {
	// A name Value (not a stream) should return an errorReadCloser.
	v := filterMakeName("NotAStream")
	rc := v.Reader()
	if rc == nil {
		t.Fatal("Reader() returned nil for non-stream Value")
	}
	buf := make([]byte, 8)
	_, err := rc.Read(buf) // error path
	if err == nil {
		t.Fatal("expected error reading from non-stream Value, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestApplyStreamFilters — multiple branches (40% coverage target)
// ---------------------------------------------------------------------------

// TestApplyStreamFiltersNull confirms that a Null filter Kind passes data
// through unchanged (Null case in applyStreamFilters).
func TestApplyStreamFiltersNull(t *testing.T) {
	payload := []byte("passthrough data")
	rd, err := applyStreamFilters(bytes.NewReader(payload), Value{}, Value{})
	if err != nil {
		t.Fatalf("applyStreamFilters Null: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Null filter: got %q, want %q", got, payload)
	}
}

// TestApplyStreamFiltersName covers the Name case in applyStreamFilters (40%).
func TestApplyStreamFiltersName(t *testing.T) {
	payload := []byte("stream data for name filter")
	compressed := zlibCompress(payload)

	filterVal := filterMakeName("FlateDecode")
	rd, err := applyStreamFilters(bytes.NewReader(compressed), filterVal, Value{})
	if err != nil {
		t.Fatalf("applyStreamFilters Name: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Name filter: got %q, want %q", got, payload)
	}
}

// TestApplyStreamFiltersArray covers the Array case in applyStreamFilters (40%).
func TestApplyStreamFiltersArray(t *testing.T) {
	payload := []byte("array filter stream data")
	compressed := zlibCompress(payload)
	filterArr := filterMakeArray(name("FlateDecode"))
	paramArr := filterMakeArray()

	rd, err := applyStreamFilters(bytes.NewReader(compressed), filterArr, paramArr)
	if err != nil {
		t.Fatalf("applyStreamFilters Array: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Array filter: got %q, want %q", got, payload)
	}
}

// TestApplyStreamFiltersUnsupported covers the default (error) branch in
// applyStreamFilters when the filter Value is neither Null, Name, nor Array.
func TestApplyStreamFiltersUnsupported(t *testing.T) {
	// An Integer Value has Kind == Integer, which hits the default error branch.
	intFilter := makeIntValue(42)
	_, err := applyStreamFilters(bytes.NewReader([]byte("x")), intFilter, Value{}) // error path
	if err == nil {
		t.Fatal("expected error for unsupported filter Kind, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestApplyFilter — multiple branches (42% coverage target)
// ---------------------------------------------------------------------------

// TestApplyFilterUnsupported covers the default (error) case in applyFilter
// when an unknown filter name is given.
func TestApplyFilterUnsupported(t *testing.T) {
	_, err := applyFilter(bytes.NewReader(nil), "UnknownFilter", Value{}) // error path
	if err == nil {
		t.Fatal("expected error for unsupported filter name, got nil")
	}
}

// TestApplyFilterASCII85WithDecodeParms exercises the error path inside
// applyFilter("ASCII85Decode") when DecodeParms is non-null (has keys).
func TestApplyFilterASCII85WithDecodeParms(t *testing.T) {
	// A dict param with at least one key triggers the default case in the
	// param.Keys() switch inside applyFilter for ASCII85Decode.
	param := filterMakeDict(map[string]any{"SomeKey": int64(1)})
	_, err := applyFilter(bytes.NewReader([]byte("9jqo^~>")), "ASCII85Decode", param) // error path
	if err == nil {
		t.Fatal("expected error for ASCII85Decode with DecodeParms, got nil")
	}
}

// TestApplyFilterPNGUpBadColumns exercises the columns > maxPNGColumns error
// path inside applyFilter for FlateDecode with predictor 12.
func TestApplyFilterPNGUpBadColumns(t *testing.T) {
	compressed := zlibCompress([]byte("x"))
	param := filterMakeDict(map[string]any{
		"Predictor": int64(12),
		"Columns":   int64(maxPNGColumns + 1),
	})
	_, err := applyFilter(bytes.NewReader(compressed), "FlateDecode", param) // error path
	if err == nil {
		t.Fatal("expected error for Columns > maxPNGColumns, got nil")
	}
}

// TestApplyFilterUnsupportedPredictor exercises the unsupported-predictor
// error path (predictor != 12) inside applyFilter for FlateDecode.
func TestApplyFilterUnsupportedPredictor(t *testing.T) {
	compressed := zlibCompress([]byte("data"))
	param := filterMakeDict(map[string]any{
		"Predictor": int64(99),
		"Columns":   int64(4),
	})
	_, err := applyFilter(bytes.NewReader(compressed), "FlateDecode", param) // error path
	if err == nil {
		t.Fatal("expected error for unsupported predictor, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestPNGUpReaderMalformed
// ---------------------------------------------------------------------------

// TestPNGUpReaderMalformed verifies that pngUpReader.Read returns an error
// (not a panic) when the filter-type byte is not 2.
func TestPNGUpReaderMalformed(t *testing.T) {
	const columns = 2
	// Row with filter byte != 2 (use 0 instead of 2).
	badRow := []byte{0, 10, 20}
	compressed := zlibCompress(badRow)

	param := filterMakeDict(map[string]any{
		"Predictor": int64(12),
		"Columns":   int64(columns),
	})
	rd, err := applyFilter(bytes.NewReader(compressed), "FlateDecode", param)
	if err != nil {
		t.Fatalf("applyFilter: %v", err)
	}
	buf := make([]byte, 64)
	_, err = rd.Read(buf) // error path
	if err == nil {
		t.Fatal("expected error for malformed PNG-Up filter byte, got nil")
	}
}

// ---------------------------------------------------------------------------
// FuzzFilterFlateDecode
// ---------------------------------------------------------------------------

// FuzzFilterFlateDecode feeds arbitrary bytes to applyFilter("FlateDecode").
// The Go fuzz engine detects panics natively; no recover() wrapper is used.
// Seeds include valid zlib-compressed data and edge cases.
func FuzzFilterFlateDecode(f *testing.F) {
	// Seed: valid zlib-compressed short payload.
	f.Add(zlibCompress([]byte("hello")))
	// Seed: valid zlib-compressed empty payload.
	f.Add(zlibCompress([]byte{}))
	// Seed: corrupt / non-zlib input.
	f.Add([]byte("not zlib data"))
	// Seed: empty input.
	f.Add([]byte{})
	// Seed: valid zlib with repetitive content.
	f.Add(zlibCompress(bytes.Repeat([]byte{0xAB}, 256)))

	f.Fuzz(func(t *testing.T, data []byte) {
		rd, err := applyFilter(bytes.NewReader(data), "FlateDecode", Value{})
		if err != nil {
			return // error path is expected for malformed input
		}
		_, _ = io.Copy(io.Discard, rd)
	})
}

// ---------------------------------------------------------------------------
// BenchmarkFlateDecode
// ---------------------------------------------------------------------------

// BenchmarkFlateDecode measures FlateDecode throughput for a 64 KB synthetic
// payload (S2 benchmark requirement).
func BenchmarkFlateDecode(b *testing.B) {
	const size = 64 << 10 // 64 KB
	unit := []byte("benchmark payload data ")
	payload := bytes.Repeat(unit, (size/len(unit))+1)[:size]
	compressed := zlibCompress(payload)

	b.ResetTimer()
	b.SetBytes(int64(size))

	for b.Loop() {
		rd, err := applyFilter(bytes.NewReader(compressed), "FlateDecode", Value{})
		if err != nil {
			b.Fatalf("applyFilter: %v", err)
		}
		_, err = io.Copy(io.Discard, rd)
		if err != nil {
			b.Fatalf("io.Copy: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// TestFilterFlateCloseWriterClose
// ---------------------------------------------------------------------------

// TestFilterFlateCloseWriterClose builds a FlateDecode stream Value entirely
// in-process, calls v.Reader() to obtain an io.ReadCloser, verifies that
// reading the decompressed content matches the original payload, and
// confirms that Close() returns nil.
//
// Value.Reader() returns io.NopCloser(out) on the success path, so Close()
// must always return nil — this test pins that invariant.
func TestFilterFlateCloseWriterClose(t *testing.T) {
	payload := []byte("hello world test payload")
	compressed := zlibCompress(payload)

	// Build a Reader whose backing store is a bytes.Reader positioned at
	// offset 0; the stream body occupies [0, len(compressed)).
	r := &Reader{f: bytes.NewReader(compressed), end: int64(len(compressed))}

	s := stream{
		hdr: dict{
			name("Length"): int64(len(compressed)),
			name("Filter"): name("FlateDecode"),
		},
		offset: 0,
	}
	v := Value{r, objptr{}, s}

	rc := v.Reader()
	if rc == nil {
		t.Fatal("Reader() returned nil")
	}

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decompressed content mismatch: got %q, want %q", got, payload)
	}

	if err := rc.Close(); err != nil {
		t.Fatalf("Close() returned non-nil error: %v", err)
	}
}

// TestFilterErrorReadCloserClose covers errorReadCloser.Close (filter.go:141).
// Value.Reader() returns an errorReadCloser when the Value is not a stream.
func TestFilterErrorReadCloserClose(t *testing.T) {
	r := &Reader{f: bytes.NewReader(nil), end: 0}
	v := Value{r, objptr{}, dict{}} // dict is not a stream
	rc := v.Reader()
	if err := rc.Close(); err == nil {
		t.Error("expected non-nil error from errorReadCloser.Close(), got nil")
	}
}

// ---------------------------------------------------------------------------
// ASCIIHexDecode
// ---------------------------------------------------------------------------

// asciiHexDecodeAll runs input through applyFilter("ASCIIHexDecode") and
// returns the decoded bytes.
func asciiHexDecodeAll(t *testing.T, input string) ([]byte, error) {
	t.Helper()
	rd, err := applyFilter(bytes.NewReader([]byte(input)), "ASCIIHexDecode", Value{})
	if err != nil {
		t.Fatalf("applyFilter ASCIIHexDecode: %v", err)
	}
	return io.ReadAll(rd)
}

// TestASCIIHexDecodeBasic decodes a hex payload terminated by '>' with mixed
// case and interleaved whitespace.
func TestASCIIHexDecodeBasic(t *testing.T) {
	got, err := asciiHexDecodeAll(t, "48 65\n6c\t6C 6F>")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, []byte("Hello")) {
		t.Errorf("got %q, want %q", got, "Hello")
	}
}

// TestASCIIHexDecodeOddDigit verifies that a final odd hex digit is padded
// with an implied zero (ISO 32000-1 §7.4.2).
func TestASCIIHexDecodeOddDigit(t *testing.T) {
	got, err := asciiHexDecodeAll(t, "486>")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, []byte{0x48, 0x60}) {
		t.Errorf("got %x, want 4860", got)
	}
}

// TestASCIIHexDecodeNoEOD verifies that EOF without the '>' marker decodes
// the available digits.
func TestASCIIHexDecodeNoEOD(t *testing.T) {
	got, err := asciiHexDecodeAll(t, "4865")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, []byte("He")) {
		t.Errorf("got %q, want %q", got, "He")
	}
}

// TestASCIIHexDecodeEmpty verifies that an immediate EOD yields empty output.
func TestASCIIHexDecodeEmpty(t *testing.T) {
	got, err := asciiHexDecodeAll(t, ">")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %x, want empty", got)
	}
}

// TestASCIIHexDecodeInvalidByte verifies that a non-hex, non-whitespace byte
// surfaces as a read error.
func TestASCIIHexDecodeInvalidByte(t *testing.T) {
	if _, err := asciiHexDecodeAll(t, "48zz>"); err == nil {
		t.Error("invalid byte: want error, got nil")
	}
}

// TestASCIIHexDecodeRejectsParms verifies that DecodeParms is rejected, as
// the filter defines none.
func TestASCIIHexDecodeRejectsParms(t *testing.T) {
	param := filterMakeDict(map[string]any{"K": int64(1)})
	if _, err := applyFilter(bytes.NewReader(nil), "ASCIIHexDecode", param); err == nil {
		t.Error("DecodeParms: want error, got nil")
	}
}

// ---------------------------------------------------------------------------
// RunLengthDecode
// ---------------------------------------------------------------------------

// runLengthDecodeAll runs input through applyFilter("RunLengthDecode") and
// returns the decoded bytes.
func runLengthDecodeAll(t *testing.T, input []byte) ([]byte, error) {
	t.Helper()
	rd, err := applyFilter(bytes.NewReader(input), "RunLengthDecode", Value{})
	if err != nil {
		t.Fatalf("applyFilter RunLengthDecode: %v", err)
	}
	return io.ReadAll(rd)
}

// TestRunLengthDecodeRuns decodes a literal run followed by a repeat run
// (ISO 32000-1 §7.4.5): {0,'a'} → "a"; {255,'b'} → 257-255 = 2 × 'b'.
func TestRunLengthDecodeRuns(t *testing.T) {
	got, err := runLengthDecodeAll(t, []byte{0, 'a', 255, 'b', 128})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, []byte("abb")) {
		t.Errorf("got %q, want %q", got, "abb")
	}
}

// TestRunLengthDecodeLiteral decodes a multi-byte literal run: length byte 2
// copies the next 3 bytes verbatim.
func TestRunLengthDecodeLiteral(t *testing.T) {
	got, err := runLengthDecodeAll(t, []byte{2, 'a', 'b', 'c', 128})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, []byte("abc")) {
		t.Errorf("got %q, want %q", got, "abc")
	}
}

// TestRunLengthDecodeEODStops verifies that data after the 128 end-of-data
// marker is not decoded.
func TestRunLengthDecodeEODStops(t *testing.T) {
	got, err := runLengthDecodeAll(t, []byte{0, 'a', 128, 0, 'b'})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, []byte("a")) {
		t.Errorf("got %q, want %q", got, "a")
	}
}

// TestRunLengthDecodeMissingEOD verifies that EOF at a run boundary without
// the 128 marker is treated as end-of-data.
func TestRunLengthDecodeMissingEOD(t *testing.T) {
	got, err := runLengthDecodeAll(t, []byte{1, 'h', 'i'})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, []byte("hi")) {
		t.Errorf("got %q, want %q", got, "hi")
	}
}

// TestRunLengthDecodeTruncated verifies that EOF inside a literal or repeat
// run surfaces as a read error.
func TestRunLengthDecodeTruncated(t *testing.T) {
	if _, err := runLengthDecodeAll(t, []byte{5, 'a'}); err == nil {
		t.Error("truncated literal run: want error, got nil")
	}
	if _, err := runLengthDecodeAll(t, []byte{200}); err == nil {
		t.Error("truncated repeat run: want error, got nil")
	}
}

// TestRunLengthDecodeRejectsParms verifies that DecodeParms is rejected, as
// the filter defines none.
func TestRunLengthDecodeRejectsParms(t *testing.T) {
	param := filterMakeDict(map[string]any{"K": int64(1)})
	if _, err := applyFilter(bytes.NewReader(nil), "RunLengthDecode", param); err == nil {
		t.Error("DecodeParms: want error, got nil")
	}
}
