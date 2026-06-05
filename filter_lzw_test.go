// filter_lzw_test.go — tests for the LZWDecode stream filter (ISO 32000-1 §7.4.4).

package pdf

import (
	"bytes"
	"compress/lzw"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// lzwEncodePDF: spec-faithful PDF LZW encoder (test-only)
// ---------------------------------------------------------------------------

// lzwBitWriter accumulates bits MSB-first and flushes to a byte slice.
type lzwBitWriter struct {
	buf   []byte
	bits  uint32
	nBits uint
}

func (w *lzwBitWriter) writeCode(code uint16, width uint) {
	w.bits = (w.bits << width) | uint32(code)
	w.nBits += width
	for w.nBits >= 8 {
		w.nBits -= 8
		w.buf = append(w.buf, byte(w.bits>>w.nBits))
		w.bits &= (1 << w.nBits) - 1
	}
}

func (w *lzwBitWriter) flush() []byte {
	if w.nBits > 0 {
		// Pad remaining bits with zeros and flush.
		w.buf = append(w.buf, byte(w.bits<<(8-w.nBits)))
		w.nBits = 0
		w.bits = 0
	}
	return w.buf
}

// lzwEncodePDF compresses data per ISO 32000-1 §7.4.4 for test fixtures.
// It emits ClearTable first, uses the earlyChange width-switch convention,
// emits ClearTable when the table fills, and ends with EOD.
func lzwEncodePDF(data []byte, earlyChange bool) []byte {
	type encEntry struct {
		prefix uint16
		suffix byte
	}

	w := &lzwBitWriter{}
	width := uint(9)
	next := uint16(lzwFirstCode)

	// table maps (prefix, suffix) → code.
	table := make(map[encEntry]uint16, 512)

	// widthLimit returns the threshold above which the code width must increase.
	// The encoder's next-to-assign counter is incremented before this check, so
	// it is one higher than the decoder's counter at the same stream position.
	// Using > (strict) here keeps the encoder one code behind the decoder's >=
	// check, ensuring both sides switch at the identical code boundary:
	//   earlyChange=true:  switch when next > 511 (i.e. next==512, 255th emit at new width)
	//   earlyChange=false: switch when next > 512 (i.e. next==513, 256th emit at new width)
	widthLimit := func() uint16 {
		lim := uint16(1) << width
		if earlyChange {
			return lim - 1
		}
		return lim
	}

	resetTable := func() {
		table = make(map[encEntry]uint16, 512)
		next = lzwFirstCode
		width = 9
	}

	advanceWidth := func() {
		if next > widthLimit() && width < lzwMaxBits {
			width++
		}
	}

	// Emit ClearTable at start.
	w.writeCode(lzwClearCode, width)

	if len(data) == 0 {
		w.writeCode(lzwEODCode, width)
		return w.flush()
	}

	prefix := uint16(data[0])

	for _, b := range data[1:] {
		key := encEntry{prefix: prefix, suffix: b}
		if code, ok := table[key]; ok {
			prefix = code
			continue
		}
		// Emit prefix code.
		w.writeCode(prefix, width)

		if next >= lzwMaxEntries {
			// Table full: emit ClearTable and reset.
			w.writeCode(lzwClearCode, width)
			resetTable()
			// Emit ClearTable resets state; current byte 'b' starts a new prefix.
			prefix = uint16(b)
			continue
		}

		table[key] = next
		next++
		advanceWidth()
		prefix = uint16(b)
	}

	// Emit the final prefix code.
	w.writeCode(prefix, width)
	w.writeCode(lzwEODCode, width)
	return w.flush()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// lcg produces a deterministic sequence of pseudo-random bytes without using
// the global math/rand source (safe under -race with no seeding).
func lcgBytes(n int) []byte {
	out := make([]byte, n)
	state := uint64(0xDEADBEEFCAFEBABE)
	for i := range out {
		state = state*6364136223846793005 + 1442695040888963407
		out[i] = byte(state >> 56)
	}
	return out
}

// lzwRoundtrip encodes data with lzwEncodePDF and decodes with newLZWReader,
// checking that the result equals data.
func lzwRoundtrip(t *testing.T, earlyChange bool, label string, data []byte) {
	t.Helper()
	encoded := lzwEncodePDF(data, earlyChange)
	got, err := io.ReadAll(newLZWReader(bytes.NewReader(encoded), earlyChange))
	if err != nil {
		t.Fatalf("%s: ReadAll: %v", label, err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("%s: roundtrip mismatch (got %d bytes, want %d bytes)", label, len(got), len(data))
	}
}

// ---------------------------------------------------------------------------
// TestLZWRoundtripEarlyChange
// ---------------------------------------------------------------------------

// TestLZWRoundtripEarlyChange verifies roundtrip correctness with earlyChange=true
// (the PDF default) for four payload shapes.
func TestLZWRoundtripEarlyChange(t *testing.T) {
	// Short ASCII.
	lzwRoundtrip(t, true, "short ASCII", []byte("Hello, PDF LZW filter!"))

	// 10 KiB of repetitive text (forces width growth).
	rep := []byte(strings.Repeat("abcdefghij", 1024))
	lzwRoundtrip(t, true, "10KiB repetitive", rep)

	// 20 KiB of deterministic pseudo-random bytes (LCG, no math/rand global).
	lzwRoundtrip(t, true, "20KiB pseudo-random", lcgBytes(20*1024))

	// Data that forces table-full → ClearTable at 12-bit saturation.
	// Use a payload derived from lcgBytes so it has high entropy (forces many
	// new table entries quickly) and is large enough to fill a 4096-entry table.
	lzwRoundtrip(t, true, "table-fill+clear", lcgBytes(64*1024))
}

// ---------------------------------------------------------------------------
// TestLZWRoundtripNoEarlyChange
// ---------------------------------------------------------------------------

// TestLZWRoundtripNoEarlyChange verifies roundtrip correctness with earlyChange=false.
func TestLZWRoundtripNoEarlyChange(t *testing.T) {
	lzwRoundtrip(t, false, "short ASCII", []byte("Hello, PDF LZW filter!"))

	rep := []byte(strings.Repeat("abcdefghij", 1024))
	lzwRoundtrip(t, false, "10KiB repetitive", rep)

	lzwRoundtrip(t, false, "20KiB pseudo-random", lcgBytes(20*1024))

	lzwRoundtrip(t, false, "table-fill+clear", lcgBytes(64*1024))
}

// ---------------------------------------------------------------------------
// TestLZWStdlibCrossCheck
// ---------------------------------------------------------------------------

// TestLZWStdlibCrossCheck encodes with compress/lzw (MSB, litWidth=8) and
// decodes with newLZWReader(earlyChange=false).
//
// The Go standard library's lzw.NewWriter uses earlyChange=false (it increments
// the code width when the next assignment would reach 2^width, not 2^width-1).
// This matches our earlyChange=false path, so the cross-check should pass.
func TestLZWStdlibCrossCheck(t *testing.T) {
	payloads := []struct {
		label string
		data  []byte
	}{
		{"short", []byte("hello world")},
		{"repetitive 4KiB", bytes.Repeat([]byte("ABCD"), 1024)},
		{"pseudo-random 8KiB", lcgBytes(8 * 1024)},
		{"pseudo-random 16KiB", lcgBytes(16 * 1024)},
	}

	for _, tc := range payloads {
		t.Run(tc.label, func(t *testing.T) {
			var buf bytes.Buffer
			enc := lzw.NewWriter(&buf, lzw.MSB, 8)
			if _, err := enc.Write(tc.data); err != nil {
				t.Fatalf("lzw.NewWriter Write: %v", err)
			}
			if err := enc.Close(); err != nil {
				t.Fatalf("lzw.NewWriter Close: %v", err)
			}
			encoded := buf.Bytes()

			got, err := io.ReadAll(newLZWReader(bytes.NewReader(encoded), false))
			if err != nil {
				t.Fatalf("newLZWReader ReadAll: %v", err)
			}
			if !bytes.Equal(got, tc.data) {
				t.Fatalf("stdlib cross-check mismatch: got %d bytes, want %d bytes", len(got), len(tc.data))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestLZWInvalidCode
// ---------------------------------------------------------------------------

// TestLZWInvalidCode verifies that a code referencing an unassigned table entry
// returns an error containing "invalid code".
func TestLZWInvalidCode(t *testing.T) {
	// Build a hand-crafted bit stream:
	//   [Clear=256, width:9] [literal 'A'=65, width:9] [invalid=259, width:9]
	// After Clear+literal 'A': next=258, so code 259 is unassigned → error.
	w := &lzwBitWriter{}
	w.writeCode(uint16(lzwClearCode), 9) // Clear
	w.writeCode(65, 9)                   // 'A' — valid literal; adds entry 258
	w.writeCode(259, 9)                  // 259 > next(259? let's think carefully)
	// After Clear: next=258 (first assignable). After seeing 'A' (first code
	// after Clear): prev='A', no table entry added yet (first-code-after-clear
	// rule). next stays at 258. Code 259 > 258 → invalid.
	// Wait — we need code > next. At this point next=258, so code 259 > 258. ✓
	encoded := w.flush()

	got, err := io.ReadAll(newLZWReader(bytes.NewReader(encoded), true))
	_ = got
	if err == nil || !strings.Contains(err.Error(), "invalid code") {
		t.Fatalf("expected 'invalid code' error, got: %v (decoded %d bytes)", err, len(got))
	}
}

// ---------------------------------------------------------------------------
// TestLZWTruncated
// ---------------------------------------------------------------------------

// TestLZWTruncated verifies that a stream cut mid-data decodes the available
// prefix without error (lenient EOF behaviour).
func TestLZWTruncated(t *testing.T) {
	data := []byte("abcdefghijklmnopqrstuvwxyz")
	encoded := lzwEncodePDF(data, true)

	// Truncate to roughly half.
	half := encoded[:len(encoded)/2]
	if len(half) == 0 {
		t.Skip("encoded too short to truncate")
	}

	// Must not return an error; decoded prefix must be a valid prefix of data.
	got, err := io.ReadAll(newLZWReader(bytes.NewReader(half), true))
	if err != nil {
		t.Fatalf("truncated: unexpected error: %v", err)
	}
	if len(got) > len(data) {
		t.Fatalf("truncated: decoded more bytes than original (%d > %d)", len(got), len(data))
	}
	if !bytes.HasPrefix(data, got) {
		t.Fatalf("truncated: decoded bytes are not a prefix of the original")
	}
}

// ---------------------------------------------------------------------------
// TestLZWImmediateEOD
// ---------------------------------------------------------------------------

// TestLZWImmediateEOD verifies that a stream of [Clear, EOD] yields empty output.
func TestLZWImmediateEOD(t *testing.T) {
	w := &lzwBitWriter{}
	w.writeCode(uint16(lzwClearCode), 9)
	w.writeCode(uint16(lzwEODCode), 9)
	encoded := w.flush()

	got, err := io.ReadAll(newLZWReader(bytes.NewReader(encoded), true))
	if err != nil {
		t.Fatalf("immediate EOD: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("immediate EOD: expected empty output, got %d bytes: %q", len(got), got)
	}
}

// TestLZWFirstCodeAfterClearInvalid verifies that a table code immediately
// after Clear is rejected instead of silently expanding a stale or
// zero-valued table entry (adversarial-review finding: [Clear, 258] slipped
// past the code > next check because 258 == next).
func TestLZWFirstCodeAfterClearInvalid(t *testing.T) {
	w := &lzwBitWriter{}
	w.writeCode(uint16(lzwClearCode), 9)
	w.writeCode(uint16(lzwFirstCode), 9) // 258: not a literal — invalid here
	encoded := w.flush()

	_, err := io.ReadAll(newLZWReader(bytes.NewReader(encoded), true))
	if err == nil {
		t.Fatal("first code 258 after clear: want error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid first code") {
		t.Fatalf("first code after clear: unexpected error: %v", err)
	}
}

// TestLZWFullTableFreeze verifies that a full 4096-entry table freezes
// (no further assignments, no error) instead of rejecting the stream:
// conforming encoders emit Clear at this point, but real encoders may keep
// emitting existing 12-bit codes (adversarial-review finding; matches
// stdlib compress/lzw). The full table is simulated directly on the reader
// state — building 3838 entries through the bit stream would obscure the
// boundary under test.
func TestLZWFullTableFreeze(t *testing.T) {
	w := &lzwBitWriter{}
	w.writeCode('A', 12)
	w.writeCode('B', 12)
	w.writeCode('C', 12)
	w.writeCode(uint16(lzwEODCode), 12)

	r := newLZWReader(bytes.NewReader(w.flush()), true).(*lzwReader)
	r.next = lzwMaxEntries // simulate: table filled, encoder sent no Clear
	r.width = lzwMaxBits

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("full-table decode: unexpected error: %v", err)
	}
	if string(got) != "ABC" {
		t.Fatalf("full-table decode: got %q, want %q", got, "ABC")
	}
	if r.next != lzwMaxEntries {
		t.Fatalf("table grew past freeze: next=%d", r.next)
	}
}

// TestLZWApplyFilterIntegration exercises the production entry point —
// applyFilter("LZWDecode", …) — rather than newLZWReader directly: the
// /EarlyChange parse (including the 0 → earlyChange=false flip), the
// maxDecompressedSize bound, and the LZW→predictor chain are glue that the
// unit tests above cannot see.
func TestLZWApplyFilterIntegration(t *testing.T) {
	payload := []byte("integration payload through the real filter path")

	t.Run("default EarlyChange", func(t *testing.T) {
		rd, err := applyFilter(bytes.NewReader(lzwEncodePDF(payload, true)), "LZWDecode", Value{})
		if err != nil {
			t.Fatalf("applyFilter: %v", err)
		}
		got, err := io.ReadAll(rd)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("got %q (err=%v), want %q", got, err, payload)
		}
	})

	t.Run("EarlyChange 0", func(t *testing.T) {
		param := filterMakeDict(map[string]any{"EarlyChange": int64(0)})
		rd, err := applyFilter(bytes.NewReader(lzwEncodePDF(payload, false)), "LZWDecode", param)
		if err != nil {
			t.Fatalf("applyFilter: %v", err)
		}
		got, err := io.ReadAll(rd)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("got %q (err=%v), want %q", got, err, payload)
		}
	})

	t.Run("EarlyChange invalid", func(t *testing.T) {
		param := filterMakeDict(map[string]any{"EarlyChange": int64(2)})
		if _, err := applyFilter(bytes.NewReader(nil), "LZWDecode", param); err == nil {
			t.Fatal("EarlyChange 2: want error, got nil")
		}
	})

	t.Run("with PNG predictor", func(t *testing.T) {
		// Same KAT as TestFlateDecodePNGUp: rows {2,10,20},{2,3,5}, Columns=2
		// → {10,20,13,25}, but compressed with LZW instead of zlib.
		rows := []byte{2, 10, 20, 2, 3, 5}
		param := filterMakeDict(map[string]any{
			"Predictor": int64(12),
			"Columns":   int64(2),
		})
		rd, err := applyFilter(bytes.NewReader(lzwEncodePDF(rows, true)), "LZWDecode", param)
		if err != nil {
			t.Fatalf("applyFilter: %v", err)
		}
		got, err := io.ReadAll(rd)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		want := []byte{10, 20, 13, 25}
		if !bytes.Equal(got, want) {
			t.Fatalf("LZW+predictor: got %v, want %v", got, want)
		}
	})
}

// TestLZWQpdfOracle validates lzwEncodePDF against qpdf as an external
// oracle: a PDF whose content stream was compressed by our test encoder must
// round-trip through `qpdf --stream-data=uncompress` and yield the original
// bytes. This pins the spec-correctness of the encoder that every roundtrip
// KAT in this file relies on — self-consistent encode/decode pairs cannot
// catch a shared spec-interpretation error. Skipped when qpdf is absent
// (e.g. CI); the verified behavior is environment-independent.
func TestLZWQpdfOracle(t *testing.T) {
	qpdfPath, err := exec.LookPath("qpdf")
	if err != nil {
		t.Skip("qpdf not installed; external oracle check skipped")
	}
	// ~8.4 KiB of repetitive text: drives the code width through 9→10→11 bits.
	payload := []byte(strings.Repeat("LZW oracle payload: the quick brown fox. ", 200))
	enc := lzwEncodePDF(payload, true)

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 5)
	writeObj := func(num int, body string) {
		offsets[num] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>")
	offsets[4] = pdf.Len()
	fmt.Fprintf(&pdf, "4 0 obj\n<< /Length %d /Filter /LZWDecode >>\nstream\n", len(enc))
	pdf.Write(enc)
	pdf.WriteString("\nendstream\nendobj\n")
	xrefOff := pdf.Len()
	pdf.WriteString("xref\n0 5\n0000000000 65535 f \n")
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)

	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	out := filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, pdf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: qpdfPath comes from LookPath; args are test temp files
	cmd := exec.Command(qpdfPath, "--stream-data=uncompress", in, out)
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("qpdf rejected our LZW bytes: %v\n%s", err, outb)
	}
	got, err := os.ReadFile(out) //nolint:gosec // G304: path is inside t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, payload) {
		t.Fatal("qpdf-uncompressed output does not contain the original payload")
	}
}
