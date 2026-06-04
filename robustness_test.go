package pdf

import (
	"context"
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

// --- Tier 3: Class B — panic-escape getters + B-nil ---------------------------

// overDeepArray returns a nested array literal whose depth exceeds
// maxObjectDepth. Used as a malformed object body: resolving it drives
// loadDirectObject -> readObject past the depth cap, which panics via b.errorf.
// The PDF stays openable because OpenBytes never resolves the object — only a
// post-open getter does, where the Tier 3 resolve() recover must degrade the
// panic to a null Value instead of crashing the process.
func overDeepArray() string {
	return strings.Repeat("[", maxObjectDepth+50) + strings.Repeat("]", maxObjectDepth+50)
}

// TestRobustnessClassBMalformedCatalog covers the Class B getter surface reached
// through the trailer: the /Root object has a malformed body, so resolving it
// from Trailer().Key, Reader.Page, or Reader.NumPage must yield a null/zero
// result rather than panicking out of the unrecovered getter.
func TestRobustnessClassBMalformedCatalog(t *testing.T) {
	data := buildPDF([]string{
		overDeepArray(),                        // obj 1: /Root — malformed body
		"<< /Type /Pages /Count 0 /Kids [] >>", // obj 2
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes must succeed (malformed body is post-open only): %v", err)
	}

	withWatchdog(t, "Class B Trailer().Key chain", 5*time.Second, func() {
		got := r.Trailer().Key("Root").Key("Pages")
		if !got.IsNull() {
			t.Errorf("Trailer().Key(Root).Key(Pages): expected null, got Kind=%v", got.Kind())
		}
	})
	withWatchdog(t, "Class B Reader.Page", 5*time.Second, func() {
		if p := r.Page(1); !p.V.IsNull() {
			t.Errorf("Page(1): expected null page, got %+v", p.V)
		}
	})
	withWatchdog(t, "Class B Reader.NumPage", 5*time.Second, func() {
		if n := r.NumPage(); n != 0 {
			t.Errorf("NumPage(): expected 0, got %d", n)
		}
	})
}

// TestRobustnessClassBMalformedResources covers the page-level Class B getters:
// a valid page whose /Resources object has a malformed body must not crash
// Page.Fonts or Page.Font; both degrade to empty/null.
func TestRobustnessClassBMalformedResources(t *testing.T) {
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",                // obj 1
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",        // obj 2
		"<< /Type /Page /Parent 2 0 R /Resources 4 0 R >>", // obj 3
		overDeepArray(), // obj 4: /Resources — malformed body
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes must succeed: %v", err)
	}
	p := r.Page(1)
	if p.V.IsNull() {
		t.Fatal("expected a non-null page before exercising Fonts/Font")
	}

	withWatchdog(t, "Class B Page.Fonts", 5*time.Second, func() {
		if fonts := p.Fonts(); len(fonts) != 0 {
			t.Errorf("Fonts(): expected empty (resources degraded), got %v", fonts)
		}
	})
	withWatchdog(t, "Class B Page.Font", 5*time.Second, func() {
		if f := p.Font("F1"); !f.V.IsNull() {
			t.Errorf("Font(F1): expected null font, got %+v", f.V)
		}
	})
}

// TestRobustnessBNilCompositeKeyIndex covers B-nil: a composite Value carrying a
// nil Reader (as Interpret pushes its operands) must not nil-deref when Key or
// Index resolves an indirect element. The guard returns a null Value instead.
// With an indirect (objptr) element, the pre-fix code dereferenced the nil
// Reader inside resolve and panicked.
func TestRobustnessBNilCompositeKeyIndex(t *testing.T) {
	dictVal := Value{nil, objptr{}, dict{name("Foo"): objptr{id: 1, gen: 0}}}
	if got := dictVal.Key("Foo"); !got.IsNull() {
		t.Errorf("nil-Reader dict.Key: expected null, got Kind=%v", got.Kind())
	}
	arrVal := Value{nil, objptr{}, array{objptr{id: 1, gen: 0}}}
	if got := arrVal.Index(0); !got.IsNull() {
		t.Errorf("nil-Reader array.Index: expected null, got Kind=%v", got.Kind())
	}

	// A nil-Reader composite must still expose its DIRECT elements: this is the
	// TJ-array / inline-operand path that a blanket r==nil guard in Key/Index
	// would wrongly null out. The guard belongs in resolve (indirect-only), so
	// direct values stay readable; this assertion pins that distinction and
	// fails if the guard ever migrates back to Key/Index.
	dictDirect := Value{nil, objptr{}, dict{name("N"): int64(42)}}
	if got := dictDirect.Key("N"); got.Int64() != 42 {
		t.Errorf("nil-Reader dict.Key(direct): got %v, want 42", got.Int64())
	}
	arrDirect := Value{nil, objptr{}, array{name("Hi")}}
	if got := arrDirect.Index(0); got.Name() != "Hi" {
		t.Errorf("nil-Reader array.Index(direct): got %q, want Hi", got.Name())
	}
}

// --- Tier 2.5: count-driven post-open loops (A9, A10) -------------------------

// TestRobustnessObjStmHugeN covers A9: an ObjStm whose /N claims a near-maxint
// entry count must not make scanObjStmIndex loop the attacker's count. The
// /Extends depth cap bounds the number of hops but NOT the per-hop scan, so the
// /First boundary in scanObjStmIndex (plus the /Extends visited-set) is what
// keeps the work bounded by the real index region.
func TestRobustnessObjStmHugeN(t *testing.T) {
	const header = "%PDF-1.7\n"
	// ObjStm obj 5: index holds one real pair (id 999) but /N claims 9e18, and
	// /Extends 5 0 R self-cycles. Looking up the absent id 7 forces a full scan
	// on every hop; without the EOF break the first scan alone never returns.
	block := buildStreamObjBytes(5, "/Type /ObjStm /N 9000000000000000000 /First 6 /Extends 5 0 R", []byte("999 0 1"))
	data := append([]byte(header), block...)

	r := makeResolveReader(data)
	r.xref = make([]xref, 6)
	r.xref[5] = xref{ptr: objptr{5, 0}, offset: int64(len(header))}
	xr := xref{ptr: objptr{5, 0}, inStream: true, stream: objptr{5, 0}}

	var got any = "sentinel"
	withWatchdog(t, "ObjStm huge /N", 5*time.Second, func() {
		got = r.resolveInStream(objptr{}, objptr{7, 0}, xr)
	})
	if got != nil {
		t.Errorf("huge /N: expected nil (degraded), got %T(%v)", got, got)
	}
}

// TestRobustnessObjStmFirstBoundNoFalseMatch covers the /First boundary: an
// ObjStm whose /N over-claims must not let the index scan read object-body bytes
// as (id, offset) pairs. Here id 7 appears only in the body (after /First); the
// scan must stop at /First and report not-found rather than false-matching the
// body's "7 0" pair and seeking to an attacker-chosen offset.
func TestRobustnessObjStmFirstBoundNoFalseMatch(t *testing.T) {
	const header = "%PDF-1.7\n"
	// Index [0,6) = "999 0 " (one real entry, id 999); body = "7 0 5 5" holds a
	// numeric "7 0" pair. /N over-claims (10); /Extends self-cycles so a miss
	// degrades to nil.
	block := buildStreamObjBytes(5, "/Type /ObjStm /N 10 /First 6 /Extends 5 0 R", []byte("999 0 7 0 5 5"))
	data := append([]byte(header), block...)
	r := makeResolveReader(data)
	r.xref = make([]xref, 6)
	r.xref[5] = xref{ptr: objptr{5, 0}, offset: int64(len(header))}
	xr := xref{ptr: objptr{5, 0}, inStream: true, stream: objptr{5, 0}}

	got := r.resolveInStream(objptr{}, objptr{7, 0}, xr)
	if got != nil {
		t.Errorf("id 7 is only in the object-body bytes, not the index: expected nil (no false match), got %T(%v)", got, got)
	}
}

// TestRobustnessObjStmExtendsCycleBoundsRescan pins the /Extends visited-set: a
// self-cycling /Extends ObjStm with a large index region must not be re-scanned
// maxLinkDepth times. The /First bound caps a single scan, but without the
// visited-set the cycle still re-scans the whole index ~1024 times — a
// stream-size-amplified CPU sink. The index here is large enough that 1024
// rescans blow the watchdog while a single scan (the visited-set path) does not;
// this is the only test that distinguishes the visited-set from the depth cap.
func TestRobustnessObjStmExtendsCycleBoundsRescan(t *testing.T) {
	const header = "%PDF-1.7\n"
	// ~1.2 MB index of "1 2 " pairs (target id 7 absent → full scan each hop);
	// /First points just past it; /Extends self-cycles.
	index := strings.Repeat("1 2 ", 300000)
	hdr := fmt.Sprintf("/Type /ObjStm /N 300000 /First %d /Extends 5 0 R", len(index))
	block := buildStreamObjBytes(5, hdr, []byte(index))
	data := append([]byte(header), block...)
	r := makeResolveReader(data)
	r.xref = make([]xref, 6)
	r.xref[5] = xref{ptr: objptr{5, 0}, offset: int64(len(header))}
	xr := xref{ptr: objptr{5, 0}, inStream: true, stream: objptr{5, 0}}

	var got any = "sentinel"
	withWatchdog(t, "ObjStm /Extends self-cycle re-scan", 5*time.Second, func() {
		got = r.resolveInStream(objptr{}, objptr{7, 0}, xr)
	})
	if got != nil {
		t.Errorf("self-cycling /Extends: expected nil, got %T(%v)", got, got)
	}
}

// TestRobustnessHugePageCount covers A10: a tiny file declaring a near-maxint
// /Pages /Count must not drive Outline() (via buildPageMap -> Page) into an
// effectively unbounded loop. NumPage must clamp the count and buildPageMap must
// stop once the real (empty) page tree is exhausted.
func TestRobustnessHugePageCount(t *testing.T) {
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R /Outlines 3 0 R >>",
		"<< /Type /Pages /Count 9000000000000000000 /Kids [] >>", // attacker-sized /Count
		"<< /Type /Outlines >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if n := r.NumPage(); n > maxPageCount {
		t.Errorf("NumPage: expected clamp to <= %d, got %d", maxPageCount, n)
	}
	withWatchdog(t, "huge /Count Outline", 5*time.Second, func() {
		_ = r.Outline()
	})
	withWatchdog(t, "huge /Count GetPlainText", 5*time.Second, func() {
		_, _ = r.GetPlainText(context.Background())
	})
}

// TestRobustnessBrokenMiddlePageNodeNotTruncated guards the count-driven loops'
// best-effort completeness: a malformed-but-openable page tree can yield a null
// page at one index yet a valid page at the next. The loops must skip the broken
// slot, not stop at it — a plain break-on-null would silently lose every page
// after the gap. (The DoS bound is the NumPage clamp + the consecutive-null cap.)
func TestRobustnessBrokenMiddlePageNodeNotTruncated(t *testing.T) {
	// Root[Count 7] -> [A(3 leaves), Broken(Count 1, Kids [99 0 R] dangling), C(3 leaves)].
	// Page(4) descends into Broken -> null; Page(5..7) land in C -> valid.
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",                       // 1
		"<< /Type /Pages /Count 7 /Kids [3 0 R 4 0 R 5 0 R] >>",   // 2 Root
		"<< /Type /Pages /Count 3 /Kids [6 0 R 7 0 R 8 0 R] >>",   // 3 A
		"<< /Type /Pages /Count 1 /Kids [99 0 R] >>",              // 4 Broken (99 dangling)
		"<< /Type /Pages /Count 3 /Kids [9 0 R 10 0 R 11 0 R] >>", // 5 C
		"<< /Type /Page /Parent 3 0 R >>",                         // 6  A1
		"<< /Type /Page /Parent 3 0 R >>",                         // 7  A2
		"<< /Type /Page /Parent 3 0 R >>",                         // 8  A3
		"<< /Type /Page /Parent 5 0 R >>",                         // 9  C1
		"<< /Type /Page /Parent 5 0 R >>",                         // 10 C2
		"<< /Type /Page /Parent 5 0 R >>",                         // 11 C3
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}

	var got []int
	for i, p := range r.Pages() {
		if !p.V.IsNull() {
			got = append(got, i)
		}
	}
	// 6 real leaves at indices 1,2,3,5,6,7 — index 4 is the broken slot, skipped.
	if len(got) != 6 {
		t.Errorf("expected 6 real pages despite broken middle node, got %d at %v", len(got), got)
	}
	reached := false
	for _, i := range got {
		if i == 5 {
			reached = true
		}
	}
	if !reached {
		t.Error("page index 5 (after the broken node) was not reached — loop truncated at the null slot")
	}
}
