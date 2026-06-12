// Reader-level value cache: memoizes resolved indirect objects and decoded
// object streams so hot dereference loops stop re-reading the file.

package pdf

import "sync"

// Cache bounds, following the package's DoS-bound convention (compare
// maxDecompressedSize, maxLinkDepth, maxObjectDepth, maxXrefObjects). An
// adversarial xref can declare up to maxXrefObjects objects; these caps keep
// the cache's retained heap bounded no matter what the file claims.
const (
	// maxCachedObjects caps how many resolved objects are memoized.
	maxCachedObjects = 65536
	// maxValueCacheBytes caps the approximate retained bytes of memoized
	// objects (approxObjSize estimates, computed once at insert).
	maxValueCacheBytes = 16 << 20
	// maxValueEntryBytes is the largest single object worth memoizing: one
	// huge object must not evict the whole working set.
	maxValueEntryBytes = maxValueCacheBytes / 8
	// maxObjStmCacheBytes caps the total decoded (decompressed and, for
	// encrypted files, decrypted) object-stream payload bytes retained.
	maxObjStmCacheBytes = 32 << 20
)

// cachedObj pairs a memoized object with its size estimate so eviction does
// not recompute approxObjSize.
type cachedObj struct {
	x    object
	size int64
}

// objCache memoizes resolved indirect objects and decoded object streams for
// one Reader. PDFs are immutable post-open, so entries are never invalidated,
// only evicted for space.
//
// Contract: resolved objects (dict, array, stream headers, strings) are
// treated as immutable by the entire package — recon found zero mutation
// sites — and one cached object is shared by every caller that dereferences
// the same objptr. Never write to a dict/array obtained from resolve; the
// concurrent-extraction test under -race enforces this.
//
// Locking: mu guards only map/size bookkeeping. Parsing happens OUTSIDE the
// lock; two goroutines racing one miss both parse and the losing put is
// dropped — duplicated work on an equivalent value, never a correctness
// issue. Holding mu across a parse would serialize extraction and could
// re-enter resolve.
type objCache struct {
	mu       sync.RWMutex
	obj      map[objptr]cachedObj
	objBytes int64
	stm      map[objptr][]byte
	stmBytes int64
	stmOrder []objptr // FIFO eviction order for stm
}

func (c *objCache) getObj(ptr objptr) (object, bool) {
	c.mu.RLock()
	e, ok := c.obj[ptr]
	c.mu.RUnlock()
	return e.x, ok
}

// putObj memoizes a successfully resolved object. Oversized objects are not
// cached. When either bound is hit, pseudo-random entries (Go map iteration
// order) are evicted until the new entry fits: O(1) amortized, no per-read
// bookkeeping (an LRU would turn every read into a write and destroy RWMutex
// read-parallelism), and unlike stop-inserting it cannot be permanently
// poisoned by a prefix of cold objects.
func (c *objCache) putObj(ptr objptr, x object) {
	size := approxObjSize(x)
	if size > maxValueEntryBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.obj == nil {
		c.obj = make(map[objptr]cachedObj)
	}
	if _, ok := c.obj[ptr]; ok {
		return // a racing miss already stored the equivalent value
	}
	for len(c.obj) >= maxCachedObjects || c.objBytes+size > maxValueCacheBytes {
		evicted := false
		for k, e := range c.obj {
			delete(c.obj, k)
			c.objBytes -= e.size
			evicted = true
			break
		}
		if !evicted {
			break // empty cache cannot fit the entry; give up
		}
	}
	c.obj[ptr] = cachedObj{x, size}
	c.objBytes += size
}

func (c *objCache) getStm(ptr objptr) ([]byte, bool) {
	c.mu.RLock()
	data, ok := c.stm[ptr]
	c.mu.RUnlock()
	return data, ok
}

// putStm retains one decoded ObjStm payload, evicting whole streams FIFO
// when the byte budget overflows. A payload bigger than the whole budget is
// served uncached.
func (c *objCache) putStm(ptr objptr, data []byte) {
	if int64(len(data)) > maxObjStmCacheBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stm == nil {
		c.stm = make(map[objptr][]byte)
	}
	if _, ok := c.stm[ptr]; ok {
		return
	}
	for c.stmBytes+int64(len(data)) > maxObjStmCacheBytes && len(c.stmOrder) > 0 {
		old := c.stmOrder[0]
		c.stmOrder = c.stmOrder[1:]
		c.stmBytes -= int64(len(c.stm[old]))
		delete(c.stm, old)
	}
	c.stm[ptr] = data
	c.stmBytes += int64(len(data))
	c.stmOrder = append(c.stmOrder, ptr)
}

// approxObjSize estimates the retained bytes of a parsed object. It is a
// budget heuristic, not an accounting truth: the per-node constants
// approximate Go string/slice/map/interface overheads. Recursion is bounded
// by maxObjectDepth, which the parser enforced when the object was built.
func approxObjSize(x object) int64 {
	switch x := x.(type) {
	case string:
		return int64(len(x)) + 16
	case name:
		return int64(len(x)) + 16
	case dict:
		n := int64(48)
		for k, v := range x {
			n += int64(len(k)) + 32 + approxObjSize(v)
		}
		return n
	case array:
		n := int64(24)
		for _, v := range x {
			n += 16 + approxObjSize(v)
		}
		return n
	case stream:
		return 32 + approxObjSize(x.hdr)
	default: // nil, bool, int64, float64, objptr
		return 16
	}
}

// Encoder-cache bounds mirror the object cache's: a COUNT cap bounds the entry
// map, and a BYTE cap bounds retained heap. A count cap alone is not enough —
// one ToUnicode stream can accumulate arbitrarily many bfchar/bfrange sections
// (interpretCmapRanges bounds a single section by maxCmapEntries, but the number
// of sections is unbounded), so a parsed *cmap is NOT bounded by maxCmapEntries.
// Without a byte budget an adversarial many-section, many-font document could
// pin unbounded heap on the Reader for its lifetime.
const (
	// maxCachedEncoders caps how many parsed CMaps are memoized.
	maxCachedEncoders = 1024
	// maxEncoderCacheBytes caps the approximate retained bytes of memoized CMaps.
	maxEncoderCacheBytes = 16 << 20
	// maxEncoderEntryBytes is the largest single CMap worth memoizing: one huge
	// CMap must not evict the whole working set, so it is served uncached.
	maxEncoderEntryBytes = maxEncoderCacheBytes / 8
)

// cachedCmap pairs a memoized CMap with its size estimate so eviction does not
// recompute approxCmapSize.
type cachedCmap struct {
	m    *cmap
	size int64
}

// encoderCache memoizes parsed ToUnicode CMaps for one Reader, keyed by the
// ToUnicode stream's own object pointer (NEVER the font dict pointer, which
// aliases to the enclosing object's ptr for inline fonts and would collide
// distinct CMaps). PDFs are immutable post-open, so a parsed *cmap is read-only
// after buildIndex and is shared by every page that references the same
// ToUnicode stream.
//
// Locking mirrors objCache: mu guards only map/size bookkeeping; readCmap runs
// OUTSIDE the lock, so two goroutines racing one miss both parse and the losing
// store is dropped (duplicated work on an equivalent value, never a correctness
// issue). Parsing before taking mu also keeps a consistent lock order — readCmap
// re-enters resolve and takes objCache.mu, so encoderCache.mu is never held
// across an objCache.mu acquisition — and avoids serializing parses behind the
// lock (the same rationale objCache states for its own put path).
type encoderCache struct {
	mu    sync.RWMutex
	m     map[objptr]cachedCmap
	bytes int64
}

// lookup returns the parsed CMap for the ToUnicode stream identified by key,
// parsing and memoizing it on a miss. key must be the ToUnicode stream's own
// non-zero objptr; callers with no Reader or a zero key parse directly instead.
// A failed parse (readCmap == nil) is NOT cached: that is the rare malformed
// path, re-parsing it per page reproduces today's behaviour exactly, and not
// storing nil keeps the accounting to real CMaps. A CMap larger than
// maxEncoderEntryBytes is parsed and returned but NOT retained (served uncached),
// so a pathological CMap cannot evict the working set or balloon the Reader.
func (c *encoderCache) lookup(key objptr, toUnicode Value) *cmap {
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if ok {
		return e.m
	}
	parsed := readCmap(toUnicode) // parse OUTSIDE the lock
	if parsed == nil {
		return nil
	}
	size := approxCmapSize(parsed)
	if size > maxEncoderEntryBytes {
		return parsed // too big to cache; serve uncached, never retained
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.m[key]; ok {
		return existing.m // a racing miss already stored the equivalent CMap
	}
	if c.m == nil {
		c.m = make(map[objptr]cachedCmap)
	}
	for len(c.m) >= maxCachedEncoders || c.bytes+size > maxEncoderCacheBytes {
		evicted := false
		for k, ev := range c.m { // pseudo-random eviction, like objCache.putObj
			delete(c.m, k)
			c.bytes -= ev.size
			evicted = true
			break
		}
		if !evicted {
			break // empty cache cannot fit; unreachable since size <= entry cap
		}
	}
	c.m[key] = cachedCmap{parsed, size}
	c.bytes += size
	return parsed
}

// approxCmapSize estimates the retained bytes of a parsed CMap. Like
// approxObjSize it is a budget heuristic: per-entry constants approximate the
// Go string-header + slice overheads of bfchar/bfrange.
func approxCmapSize(m *cmap) int64 {
	n := int64(64)
	for _, bc := range m.bfchar {
		n += int64(len(bc.orig)+len(bc.repl)) + 32
	}
	for i := range m.bfrange {
		for _, br := range m.bfrange[i] {
			n += int64(len(br.lo)+len(br.hi)) + 48
		}
	}
	return n
}
