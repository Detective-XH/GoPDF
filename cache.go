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
