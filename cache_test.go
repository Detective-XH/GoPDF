// Tests for cache.go: the Reader-level value cache and ObjStm bytes cache.

package pdf

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The encrypted three-way password matrix (TestEncryptFixturePasswords) plus
// the robustness suite gate the cache's opening-bypass semantics: any failure
// there after a cache change is a cache bug, not a flake.

// ---- TestObjCacheBounds ---------------------------------------------------------

// TestObjCacheBounds exercises the value cache's dual bound with synthetic
// objects: the entry-count cap and the byte budget both evict (rather than
// stop inserting), and a single oversized object is never cached.
func TestObjCacheBounds(t *testing.T) {
	t.Run("entry_count_cap", func(t *testing.T) {
		var c objCache
		for i := range maxCachedObjects + 1 {
			c.putObj(objptr{id: uint32(i)}, int64(i))
		}
		if len(c.obj) > maxCachedObjects {
			t.Errorf("entry count: got %d, want <= %d", len(c.obj), maxCachedObjects)
		}
		// Eviction, not stop-inserting: the newest entry must be present.
		if _, ok := c.getObj(objptr{id: maxCachedObjects}); !ok {
			t.Error("newest entry missing: cache stopped inserting instead of evicting")
		}
	})

	t.Run("byte_budget", func(t *testing.T) {
		var c objCache
		big := strings.Repeat("x", 1<<20) // 1 MiB, well under maxValueEntryBytes
		const n = 20                      // 20 MiB total, past the 16 MiB budget
		for i := range n {
			c.putObj(objptr{id: uint32(i)}, big)
			if c.objBytes > maxValueCacheBytes {
				t.Fatalf("after insert %d: objBytes %d exceeds budget %d",
					i, c.objBytes, maxValueCacheBytes)
			}
		}
		if _, ok := c.getObj(objptr{id: n - 1}); !ok {
			t.Error("newest entry missing after byte-budget evictions")
		}
	})

	t.Run("oversize_skip", func(t *testing.T) {
		var c objCache
		c.putObj(objptr{id: 1}, "small")
		wantBytes := c.objBytes
		huge := strings.Repeat("x", maxValueEntryBytes) // size estimate exceeds the limit
		c.putObj(objptr{id: 2}, huge)
		if _, ok := c.getObj(objptr{id: 2}); ok {
			t.Error("oversized object was cached; one huge object could evict the working set")
		}
		if c.objBytes != wantBytes {
			t.Errorf("objBytes changed by oversize put: got %d, want %d", c.objBytes, wantBytes)
		}
	})
}

// ---- TestObjCacheStmFIFO --------------------------------------------------------

// TestObjCacheStmFIFO pins whole-stream FIFO eviction: three payloads sized so
// the third overflows maxObjStmCacheBytes must evict the FIRST inserted, with
// exact byte accounting; an over-budget single payload is served uncached.
func TestObjCacheStmFIFO(t *testing.T) {
	var c objCache
	p1, p2, p3 := objptr{id: 1}, objptr{id: 2}, objptr{id: 3}
	d1 := make([]byte, 16<<20)
	d2 := make([]byte, 12<<20)
	d3 := make([]byte, 8<<20) // 16+12+8 = 36 MiB > 32 MiB budget
	c.putStm(p1, d1)
	c.putStm(p2, d2)
	if got, want := c.stmBytes, int64(28<<20); got != want {
		t.Fatalf("stmBytes after two puts: got %d, want %d", got, want)
	}
	c.putStm(p3, d3)
	if _, ok := c.getStm(p1); ok {
		t.Error("first-inserted payload still cached; eviction is not FIFO")
	}
	if _, ok := c.getStm(p2); !ok {
		t.Error("second payload evicted; FIFO should have removed only the first")
	}
	if _, ok := c.getStm(p3); !ok {
		t.Error("just-inserted payload missing")
	}
	if got, want := c.stmBytes, int64(20<<20); got != want {
		t.Errorf("stmBytes after FIFO eviction: got %d, want %d", got, want)
	}

	// A single payload bigger than the whole budget is served uncached.
	before := c.stmBytes
	c.putStm(objptr{id: 4}, make([]byte, maxObjStmCacheBytes+1))
	if _, ok := c.getStm(objptr{id: 4}); ok {
		t.Error("over-budget payload was cached")
	}
	if c.stmBytes != before {
		t.Errorf("stmBytes changed by over-budget put: got %d, want %d", c.stmBytes, before)
	}
}

// ---- TestApproxObjSize ----------------------------------------------------------

// TestApproxObjSize pins the estimator on each object kind so refactors cannot
// silently zero the cache's byte budget.
func TestApproxObjSize(t *testing.T) {
	cases := []struct {
		name string
		x    object
		want int64
	}{
		{"string", "abcde", 5 + 16},
		{"name", name("Font"), 4 + 16},
		{"dict", dict{name("A"): int64(1)}, 48 + 1 + 32 + 16},
		{"array", array{int64(7), "ab"}, 24 + 16 + 16 + 16 + (2 + 16)},
		{"stream", stream{hdr: dict{name("Length"): int64(4)}}, 32 + 48 + 6 + 32 + 16},
		{"nil", nil, 16},
		{"bool", true, 16},
		{"integer", int64(5), 16},
		{"real", 1.5, 16},
		{"objptr", objptr{id: 1}, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := approxObjSize(tc.x); got != tc.want {
				t.Errorf("approxObjSize(%T): got %d, want %d", tc.x, got, tc.want)
			}
		})
	}
}

// ---- TestCacheSharesIdenticalValue ----------------------------------------------

// countingReaderAt counts ReadAt calls so a test can assert that a second
// dereference of the same objptr is served from the cache, not from disk.
type countingReaderAt struct {
	r     io.ReaderAt
	reads int
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.reads++
	return c.r.ReadAt(p, off)
}

// TestCacheSharesIdenticalValue resolves the same objptr twice through a real
// Reader: both Values must be deep-equal and the second resolve must not
// touch the disk.
func TestCacheSharesIdenticalValue(t *testing.T) {
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	})
	cr := &countingReaderAt{r: bytes.NewReader(data)}
	r, err := NewReader(cr, int64(len(data)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	first := r.resolve(objptr{}, objptr{2, 0})
	if first.Kind() != Dict || first.Key("Type").Name() != "Pages" {
		t.Fatalf("first resolve: got Kind=%v, want the /Pages dict", first.Kind())
	}
	readsAfterFirst := cr.reads
	second := r.resolve(objptr{}, objptr{2, 0})
	if cr.reads != readsAfterFirst {
		t.Errorf("second dereference re-read disk: ReadAt count %d -> %d",
			readsAfterFirst, cr.reads)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("cached resolve diverged: first %v, second %v", first, second)
	}
}

// ---- TestConcurrentExtraction ---------------------------------------------------

// extractionSnapshot captures every surface the Reader doc comment promises is
// safe for concurrent use, in a deterministic comparable form.
type extractionSnapshot struct {
	text      string
	textErr   string
	pageTexts []int
	annots    []string
	outline   Outline
	info      [4]string
	rootKeys  []string
	kidTypes  []string
	pageCount int64
	fonts     []FontInfo
	xmp       string
}

func takeSnapshot(r *Reader) extractionSnapshot {
	var s extractionSnapshot
	rd, err := r.GetPlainText(context.Background())
	if err != nil {
		s.textErr = err.Error()
	} else {
		var sb strings.Builder
		_, _ = io.Copy(&sb, rd)
		s.text = sb.String()
	}
	n := r.NumPage()
	for i := 1; i <= n; i++ {
		p := r.Page(i)
		s.pageTexts = append(s.pageTexts, len(p.Content().Text))
		a, aerr := p.Annotations()
		s.annots = append(s.annots, fmt.Sprintf("%d %v", len(a), aerr))
	}
	s.outline = r.Outline()
	info := r.Info()
	s.info = [4]string{info.Title(), info.Author(), info.Creator(), info.Producer()}
	root := r.Trailer().Key("Root")
	s.rootKeys = root.Keys()
	pages := root.Key("Pages")
	kids := pages.Key("Kids")
	for i := 0; i < kids.Len(); i++ {
		s.kidTypes = append(s.kidTypes, kids.Index(i).Key("Type").Name())
	}
	s.pageCount = pages.Key("Count").Int64()
	s.fonts = r.Fonts()
	x, xerr := r.XMP()
	s.xmp = fmt.Sprintf("%d %v", len(x), xerr)
	return s
}

// TestConcurrentExtraction is the concurrency-contract test and the
// cache-contract (no mutation) canary at once: one Reader over an ObjStm
// fixture, GOMAXPROCS goroutines each exercising every surface the Reader doc
// comment promises, all compared to a single-goroutine baseline. The baseline
// comes from a separate Reader so the shared Reader starts with a cold cache
// and the goroutines race on cache misses under -race.
func TestConcurrentExtraction(t *testing.T) {
	data, err := os.ReadFile(corpusPath("cjk/irs-p850-zh-hant.pdf"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rBase, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes (baseline): %v", err)
	}
	want := takeSnapshot(rBase)
	if want.text == "" || len(want.pageTexts) == 0 {
		t.Fatalf("baseline extraction is empty: textErr=%q pages=%d", want.textErr, len(want.pageTexts))
	}

	rShared, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes (shared): %v", err)
	}
	var wg sync.WaitGroup
	for w := range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			if got := takeSnapshot(rShared); !reflect.DeepEqual(got, want) {
				t.Errorf("worker %d: concurrent extraction diverged from baseline", w)
			}
		})
	}
	wg.Wait()
}

// ---- TestObjStmTruncatedPrefix --------------------------------------------------

// buildTruncatedObjStmPDF assembles a minimal, openable PDF whose single ObjStm
// is byte-truncated mid-Flate-stream AFTER the first resident object's body:
// the decodable prefix covers the index section and object 11, object 12's
// body straddles the corruption point, and object 13 lies entirely past it.
//
// ObjStm payload (uncompressed layout, /First = 17):
//
//	offset  0: "11 0 12 20 13 60 "    index — three (id, offset) pairs
//	offset 17: "(alpha)"              object 11 — inside the readable prefix
//	offset 37: "(" + 30 x 's' + ")"   object 12 — body straddles the truncation
//	offset 77: "(beta)"               object 13 — entirely past the truncation
//
// The compressed bytes are cut at a point found by trial decode: the kept
// prefix must decode to >= 40 bytes (index, object 11's body, and the start of
// object 12's body) and < 69 bytes (object 12's closing parenthesis, so its
// body is left unterminated). Searching instead of hard-coding the cut keeps
// the fixture independent of the compressor's exact byte layout;
// NoCompression (stored blocks) makes prefix lengths reachable byte-by-byte.
func buildTruncatedObjStmPDF(t *testing.T) []byte {
	t.Helper()

	const index = "11 0 12 20 13 60 "
	payload := index + "(alpha)" + strings.Repeat(" ", 13) +
		"(" + strings.Repeat("s", 30) + ")" + strings.Repeat(" ", 8) + "(beta)"

	var zbuf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&zbuf, zlib.NoCompression)
	if err != nil {
		t.Fatalf("zlib.NewWriterLevel: %v", err)
	}
	if _, err := zw.Write([]byte(payload)); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	full := zbuf.Bytes()

	// decodedLen reports how many payload bytes a cut of the compressed
	// stream still yields (the readable prefix; a decode error is expected).
	decodedLen := func(cut []byte) int {
		zr, err := zlib.NewReader(bytes.NewReader(cut))
		if err != nil {
			return 0
		}
		data, _ := io.ReadAll(zr)
		return len(data)
	}

	var truncated []byte
	for cutLen := 3; cutLen < len(full); cutLen++ {
		if n := decodedLen(full[:cutLen]); n >= 40 && n < 69 {
			truncated = full[:cutLen]
			break
		}
	}
	if truncated == nil {
		t.Fatal("no truncation point yields a prefix covering object 11 and splitting object 12")
	}

	// Assemble the file: catalog (1), truncated ObjStm (2), XRef stream (3),
	// residents 11-13. Stream /Length values are exact so Value.Reader()
	// yields precisely the truncated bytes.
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	off1 := b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog >>\nendobj\n")
	off2 := b.Len()
	fmt.Fprintf(&b, "2 0 obj\n<< /Type /ObjStm /N 3 /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
		len(index), len(truncated))
	b.Write(truncated)
	b.WriteString("\nendstream\nendobj\n")
	off3 := b.Len()

	// XRef stream, W=[1 2 1]: 14 rows of (type, field2 BE16, field3).
	rows := make([][4]byte, 14)
	setType1 := func(id, off int) {
		rows[id] = [4]byte{1, byte(off >> 8), byte(off), 0}
	}
	setType1(1, off1)
	setType1(2, off2)
	setType1(3, off3)
	rows[11] = [4]byte{2, 0, 2, 0} // object 11: in ObjStm 2, index 0
	rows[12] = [4]byte{2, 0, 2, 1} // object 12: in ObjStm 2, index 1
	rows[13] = [4]byte{2, 0, 2, 2} // object 13: in ObjStm 2, index 2
	var xbody bytes.Buffer
	for _, row := range rows {
		xbody.Write(row[:])
	}
	fmt.Fprintf(&b, "3 0 obj\n<< /Type /XRef /Size 14 /Root 1 0 R /W [1 2 1] /Length %d >>\nstream\n",
		xbody.Len())
	b.Write(xbody.Bytes())
	b.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&b, "startxref\n%d\n%%%%EOF\n", off3)
	return b.Bytes()
}

// TestObjStmTruncatedPrefix pins the malformed-PDF tolerance of ObjStm
// resolution against a byte-truncated stream, byte-identical to the pre-cache
// lazy path: (a) an object in the readable prefix still resolves, (b) an
// object whose body STRADDLES the truncation degrades to a null Value
// post-open (the decode error must not be turned into a clean EOF that parses
// a partial string), (c) an object entirely past the truncation degrades to
// null instead of failing the whole document, and (d) a second dereference
// returns the same results — a truncated stream is never cached, so every
// pass re-walks the lazy path deterministically.
func TestObjStmTruncatedPrefix(t *testing.T) {
	data := buildTruncatedObjStmPDF(t)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	for pass := 1; pass <= 2; pass++ {
		got := r.resolve(objptr{}, objptr{11, 0})
		if got.Kind() != String || got.RawString() != "alpha" {
			t.Errorf("pass %d: object 11 (readable prefix): got Kind=%v %q, want String \"alpha\"",
				pass, got.Kind(), got.RawString())
		}
		straddler := r.resolve(objptr{}, objptr{12, 0})
		if straddler.Kind() != Null {
			t.Errorf("pass %d: object 12 (body straddles truncation): got Kind=%v %q, want Null",
				pass, straddler.Kind(), straddler.RawString())
		}
		gone := r.resolve(objptr{}, objptr{13, 0})
		if gone.Kind() != Null {
			t.Errorf("pass %d: object 13 (past truncation): got Kind=%v, want Null",
				pass, gone.Kind())
		}
	}
}
