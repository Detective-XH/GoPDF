package pdf

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Open-time DoS hardening for malformed cross-reference structures.
//
// A malicious PDF is untrusted input.  These tests assert that the three
// open-time denial-of-service vectors closed by this change surface a clean
// error rather than hanging the process (infinite /Prev loop) or exhausting
// memory (attacker-sized make([]byte, wtotal)).  Each potentially-looping call
// runs under a watchdog so a regression that re-introduces a hang fails the
// test instead of stalling CI forever.

// withWatchdog runs fn in a goroutine and fails the test if it does not finish
// within timeout.  A true regression (infinite loop) cannot be interrupted, so
// the goroutine is allowed to leak — it dies with the test process — and the
// watchdog reports the failure.  recover() does not stop a loop, which is why
// the open-path recover does not mitigate the cyclic /Prev findings.
func withWatchdog(t *testing.T, name string, timeout time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s: did not complete within %s — possible infinite loop (DoS regression)", name, timeout)
	}
}

// buildCyclicXrefTablePDF returns a complete PDF whose two xref tables point at
// each other via /Prev (A↔B).  Before the fix this drives
// followXrefTablePrevChain into an infinite loop at open time; after the fix it
// must surface a "cyclic /Prev" error.  Offsets are resolved by fixpoint so the
// circular dependency between offsetB and the digit-width of its own decimal
// representation cannot desynchronise the byte layout.
func buildCyclicXrefTablePDF() []byte {
	const header = "%PDF-1.7\n"
	// A minimal one-free-slot xref table whose trailer /Prev points at prevOff.
	tbl := func(prevOff int) string {
		return "xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 /Prev " +
			fmt.Sprintf("%d", prevOff) + " >>\n"
	}
	offsetA := len(header)
	offsetB := offsetA + len(tbl(0))
	for {
		next := offsetA + len(tbl(offsetB))
		if next == offsetB {
			break
		}
		offsetB = next
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(tbl(offsetB)) // table A at offsetA, /Prev -> offsetB
	b.WriteString(tbl(offsetA)) // table B at offsetB, /Prev -> offsetA
	fmt.Fprintf(&b, "startxref\n%d\n%%%%EOF", offsetA)
	return []byte(b.String())
}

// TestRobustnessCyclicXrefTablePrev: a mutual /Prev xref-table cycle must fail
// OpenBytes with a "cyclic" error, not hang.
func TestRobustnessCyclicXrefTablePrev(t *testing.T) {
	data := buildCyclicXrefTablePDF()
	var err error
	withWatchdog(t, "cyclic xref table /Prev", 5*time.Second, func() {
		_, err = OpenBytes(data)
	})
	if err == nil {
		t.Fatal("cyclic xref table /Prev: expected OpenBytes error, got nil")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Errorf("cyclic xref table /Prev: expected 'cyclic' error, got: %v", err)
	}
}

// TestRobustnessCyclicXrefStreamPrev: an xref stream whose /Prev points back at
// itself must fail followXrefStreamPrevChain with a "cyclic" error, not hang.
// Exercised at the function level against a real on-disk stream block (the
// broad open-path coverage lives in FuzzOpenAndExtract).
func TestRobustnessCyclicXrefStreamPrev(t *testing.T) {
	entries := [][]byte{
		{0x00, 0x00, 0x00, 0x00}, // slot 0: free
		{0x01, 0x00, 0x32, 0x00}, // slot 1: type=1, offset=50, gen=0
	}
	// hdrExtra " /Prev 0" makes the stream's own /Prev point at offset 0,
	// where the stream itself lives — a self-cycle.
	block := xrefStreamBuildPrevBlock(1, 2, entries, " /Prev 0")
	r := makeXrefReader(block)
	hdr := dict{name("Prev"): int64(0)}
	table := make([]xref, 2)

	var err error
	withWatchdog(t, "cyclic xref stream /Prev", 5*time.Second, func() {
		_, err = followXrefStreamPrevChain(r, table, 2, hdr)
	})
	if err == nil {
		t.Fatal("cyclic xref stream /Prev: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Errorf("cyclic xref stream /Prev: expected 'cyclic' error, got: %v", err)
	}
}

// TestRobustnessParseWArrayBounds exercises the per-field /W width cap.  Before
// the fix the `int64(int(i)) != i` guard was a no-op on 64-bit int, so
// W=[1e9,1e9,1e9] passed through and sized make([]byte, ~3GB).
func TestRobustnessParseWArrayBounds(t *testing.T) {
	good := []array{
		{int64(1), int64(2), int64(1)},
		{int64(0), int64(2), int64(1)}, // W[0]==0 is legal (type defaults to 1)
		{int64(maxXrefFieldWidth), int64(maxXrefFieldWidth), int64(maxXrefFieldWidth)},
	}
	for _, ww := range good {
		if _, err := parseWArray(ww); err != nil {
			t.Errorf("parseWArray(%v): unexpected error: %v", ww, err)
		}
	}
	bad := []array{
		{int64(maxXrefFieldWidth + 1), int64(2), int64(1)},                 // field width > cap
		{int64(-1), int64(2), int64(1)},                                    // negative width
		{int64(1_000_000_000), int64(1_000_000_000), int64(1_000_000_000)}, // the 3GB exploit
		{int64(1), int64(2)},                                               // fewer than three fields
	}
	for _, ww := range bad {
		if _, err := parseWArray(ww); err == nil {
			t.Errorf("parseWArray(%v): expected error, got nil", ww)
		}
	}
}

// TestRobustnessXrefStreamRowWidthCapped exercises the belt-and-suspenders
// row-width guard: a /W array whose elements each pass the per-field cap but
// whose summed row width is large must be rejected before make([]byte, wtotal).
func TestRobustnessXrefStreamRowWidthCapped(t *testing.T) {
	ww := make(array, 0, 4000)
	for range 4000 {
		ww = append(ww, int64(maxXrefFieldWidth)) // 4000 * 8 = 32000 bytes/row
	}
	body := []byte{1, 0, 10, 0}
	r := makeXrefReader(body)
	strm := makeXrefStream(body, dict{name("W"): ww})
	table := make([]xref, 1)

	var err error
	withWatchdog(t, "oversized xref stream W row width", 5*time.Second, func() {
		_, err = readXrefStreamData(r, strm, table, 1)
	})
	if err == nil {
		t.Fatal("oversized W row width: expected error, got nil")
	}
}
