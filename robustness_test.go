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

// --- Tier 2: post-open link-chain cycles (A4-A8) ------------------------------

// buildPDF assembles a minimal, openable PDF from object bodies (objs[i] is the
// body of object i+1). Object 1 is the catalog (/Root). The xref table and
// trailer are generated with correct offsets so OpenBytes succeeds; the
// malformed structure under test lives in the object bodies, exercised only
// after open by a getter (Page/MediaBox/Outline).
func buildPDF(objs []string) []byte {
	var b strings.Builder
	b.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefOff := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefOff)
	return []byte(b.String())
}

// TestRobustnessCyclicPageTreeKids covers A4: a /Kids page-tree cycle (two
// Pages nodes referencing each other) must not make (*Reader).Page loop forever.
func TestRobustnessCyclicPageTreeKids(t *testing.T) {
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 2 /Kids [3 0 R] >>", // Pages A -> B
		"<< /Type /Pages /Count 2 /Kids [2 0 R] >>", // Pages B -> A (cycle)
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	var p Page
	withWatchdog(t, "cyclic /Kids page tree", 5*time.Second, func() {
		p = r.Page(1)
	})
	if !p.V.IsNull() {
		t.Errorf("cyclic /Kids: expected null Page (cycle truncated), got %+v", p.V)
	}
}

// TestRobustnessCyclicPageParent covers A5: a self-referential /Parent chain
// must not make findInherited (MediaBox/Resources/...) loop forever.
func TestRobustnessCyclicPageParent(t *testing.T) {
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 3 0 R >>", // Parent points at itself (cycle)
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	page := r.Page(1)
	if page.V.IsNull() {
		t.Fatal("expected a non-null page before exercising findInherited")
	}
	withWatchdog(t, "cyclic /Parent chain", 5*time.Second, func() {
		_ = page.MediaBox() // walks /Parent via findInherited
	})
}

// TestRobustnessCyclicObjStmExtends covers A6: an ObjStm whose /Extends points
// back at itself must not make resolveInStream loop forever; it degrades to a
// null object.
func TestRobustnessCyclicObjStmExtends(t *testing.T) {
	const header = "%PDF-1.7\n"
	// ObjStm obj 5 with one index pair (id 999) and /Extends 5 0 R (self-cycle).
	// Looking up id 7 (absent) forces the resolver to follow /Extends.
	block := buildStreamObjBytes(5, "/Type /ObjStm /N 1 /First 6 /Extends 5 0 R", []byte("999 0 1"))
	data := append([]byte(header), block...)

	r := makeResolveReader(data)
	r.xref = make([]xref, 6)
	r.xref[5] = xref{ptr: objptr{5, 0}, offset: int64(len(header))}
	xr := xref{ptr: objptr{5, 0}, inStream: true, stream: objptr{5, 0}}

	var got any = "sentinel"
	withWatchdog(t, "cyclic ObjStm /Extends", 5*time.Second, func() {
		got = r.resolveInStream(objptr{}, objptr{7, 0}, xr)
	})
	if got != nil {
		t.Errorf("cyclic /Extends: expected nil (degraded), got %T(%v)", got, got)
	}
}

// TestRobustnessCyclicOutlineNext covers A7: a self-referential /Next outline
// sibling chain must not make buildOutline loop forever.
func TestRobustnessCyclicOutlineNext(t *testing.T) {
	data := buildPDF([]string{
		"<< /Type /Catalog /Outlines 2 0 R >>",
		"<< /Type /Outlines /First 3 0 R >>",
		"<< /Title (A) /Next 3 0 R >>", // /Next points at itself (cycle)
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	withWatchdog(t, "cyclic outline /Next", 5*time.Second, func() {
		_ = r.Outline()
	})
}

// TestRobustnessDeepOutlineFirst covers A8: a self-referential /First chain
// must not drive buildOutline into unbounded recursion (stack-overflow fatal
// error); the depth cap bounds it.
func TestRobustnessDeepOutlineFirst(t *testing.T) {
	data := buildPDF([]string{
		"<< /Type /Catalog /Outlines 2 0 R >>",
		"<< /Type /Outlines /First 3 0 R >>",
		"<< /Title (A) /First 3 0 R >>", // /First points at itself (recursion)
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	withWatchdog(t, "deep outline /First", 5*time.Second, func() {
		_ = r.Outline()
	})
}
