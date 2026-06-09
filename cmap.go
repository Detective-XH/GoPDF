// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"fmt"
	"sort"
)

type byteRange struct {
	low  string
	high string
}

type bfchar struct {
	orig string
	repl string
}

type bfrange struct {
	lo  string
	hi  string
	dst Value
}

type cmap struct {
	space         [4][]byteRange // codespace range
	bfrange       [4][]bfrange   // indexed by source-code length-1, like space
	bfrangeSorted [4]bool        // bucket is disjoint+sorted → binary-searchable
	bfchar        []bfchar
}

// buildIndex sorts m.bfchar by orig and indexes each m.bfrange length-bucket, so
// lookupBfchar and lookupBfrange can binary-search instead of linear-scanning
// every entry per glyph — the scan that dominated CJK extraction time. Runs once
// at the end of readCmap (single-threaded); m.bfchar and m.bfrange are read-only
// during Decode, so concurrent calls stay safe. A stable sort keeps the
// first-defined entry first among equal keys, matching the previous linear scan's
// first-match result.
func (m *cmap) buildIndex() {
	sort.SliceStable(m.bfchar, func(i, j int) bool {
		return m.bfchar[i].orig < m.bfchar[j].orig
	})
	for n := range m.bfrange {
		m.indexBfrangeBucket(n)
	}
}

// indexBfrangeBucket prepares bucket n for lookup. Disjoint ranges are sorted by
// lo and flagged binary-searchable; overlapping ranges (malformed, non-conformant
// CMaps) are left in declaration order so lookupBfrange linear-scans them and
// returns the first-declared match — preserving the pre-index behaviour exactly.
// A largest-lo binary search would otherwise pick a different range, or miss a
// code that a wider earlier range covered, silently corrupting output. The
// disjoint check needs a sorted view, so it sorts a copy and keeps that copy as
// the bucket when disjoint (no extra steady-state memory); the rare overlapping
// bucket discards the copy and keeps the original order.
func (m *cmap) indexBfrangeBucket(n int) {
	br := m.bfrange[n]
	if len(br) < 2 {
		m.bfrangeSorted[n] = true // 0 or 1 entry: trivially binary-searchable
		return
	}
	sorted := make([]bfrange, len(br))
	copy(sorted, br)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].lo < sorted[j].lo
	})
	if bfrangeDisjoint(sorted) {
		m.bfrange[n] = sorted
		m.bfrangeSorted[n] = true
	}
}

// bfrangeDisjoint reports whether the lo-sorted ranges never overlap. It tracks a
// running maximum hi so a later range nested inside an earlier wide one still
// counts as overlapping. Equal-length lo/hi within a bucket make the string
// comparisons a plain byte ordering.
func bfrangeDisjoint(sorted []bfrange) bool {
	maxHi := sorted[0].hi
	for i := 1; i < len(sorted); i++ {
		if sorted[i].lo <= maxHi {
			return false
		}
		if sorted[i].hi > maxHi {
			maxHi = sorted[i].hi
		}
	}
	return true
}

// lookupBfchar returns the decoded runes for the bfchar whose orig equals text
// (the n-byte codespace prefix), via binary search over the sorted entries.
func (m *cmap) lookupBfchar(text string) ([]rune, bool) {
	bf := m.bfchar
	i := sort.Search(len(bf), func(i int) bool { return bf[i].orig >= text })
	if i < len(bf) && bf[i].orig == text {
		return []rune(utf16Decode(bf[i].repl)), true
	}
	return nil, false
}

// lookupBfrange finds the n-byte bfrange entry whose [lo,hi] range contains text.
// Bucketing by length makes the old len(lo)==n filter structural. A disjoint
// bucket is binary-searched (largest lo <= text is then the only candidate); an
// overlapping bucket falls back to a declaration-order linear scan so the
// first-declared match wins, exactly as before indexing.
func (m *cmap) lookupBfrange(text string, n int) ([]rune, bool) {
	if n < 1 || n > 4 {
		return nil, false
	}
	br := m.bfrange[n-1]
	if !m.bfrangeSorted[n-1] {
		return lookupBfrangeLinear(br, text)
	}
	i := sort.Search(len(br), func(i int) bool { return br[i].lo > text })
	if i == 0 {
		return nil, false
	}
	if entry := br[i-1]; text <= entry.hi {
		return decodeBfrange(entry, text)
	}
	return nil, false
}

// lookupBfrangeLinear scans an overlapping (unsorted) bucket in declaration order
// and returns the first range containing text, matching the pre-index behaviour.
func lookupBfrangeLinear(br []bfrange, text string) ([]rune, bool) {
	for _, entry := range br {
		if entry.lo <= text && text <= entry.hi {
			return decodeBfrange(entry, text)
		}
	}
	return nil, false
}

// decodeBfrange maps text against a matched bfrange entry, handling String and Array destinations.
func decodeBfrange(entry bfrange, text string) ([]rune, bool) {
	if entry.dst.Kind() == String {
		s := entry.dst.RawString()
		if entry.lo != text {
			b := []byte(s)
			if len(b) == 0 {
				return []rune{noRune}, true
			}
			b[len(b)-1] += text[len(text)-1] - entry.lo[len(entry.lo)-1]
			s = string(b)
		}
		return []rune(utf16Decode(s)), true
	}
	if entry.dst.Kind() == Array {
		idx := text[len(text)-1] - entry.lo[len(entry.lo)-1]
		v := entry.dst.Index(int(idx))
		if v.Kind() == String {
			return []rune(utf16Decode(v.RawString())), true
		}
		if DebugOn {
			fmt.Printf("array %v\n", entry.dst)
		}
	} else {
		if DebugOn {
			fmt.Printf("unknown dst %v\n", entry.dst)
		}
	}
	return []rune{noRune}, true
}

// decodeOne matches the longest codespace prefix of raw (up to 4 bytes),
// looks it up in bfchar/bfrange, and returns the decoded runes plus the
// number of bytes consumed. Returns (nil, 0) when no codespace matches.
func (m *cmap) decodeOne(raw string) ([]rune, int) {
	for n := 1; n <= 4 && n <= len(raw); n++ {
		for _, space := range m.space[n-1] {
			if space.low <= raw[:n] && raw[:n] <= space.high {
				text := raw[:n]
				if runes, ok := m.lookupBfchar(text); ok {
					return runes, n
				}
				if runes, ok := m.lookupBfrange(text, n); ok {
					return runes, n
				}
				return []rune{noRune}, n
			}
		}
	}
	return nil, 0
}

func (m *cmap) Decode(raw string) (text string) {
	var r []rune
	for len(raw) > 0 {
		runes, n := m.decodeOne(raw)
		if n == 0 {
			if DebugOn {
				println("no code space found")
			}
			r = append(r, noRune)
			raw = raw[1:]
			continue
		}
		r = append(r, runes...)
		raw = raw[n:]
	}
	return string(r)
}

// cmapInterp holds mutable state for the readCmap Interpret callback.
type cmapInterp struct {
	n  int
	m  cmap
	ok bool
}

func (s *cmapInterp) handleEndCodespace(stk *Stack) {
	if s.n < 0 {
		if DebugOn {
			println("missing begincodespacerange")
		}
		s.ok = false
		return
	}
	for i := 0; i < s.n; i++ {
		hi, lo := stk.Pop().RawString(), stk.Pop().RawString()
		if len(lo) == 0 || len(lo) > 4 || len(lo) != len(hi) {
			if DebugOn {
				println("bad codespace range")
			}
			s.ok = false
			return
		}
		s.m.space[len(lo)-1] = append(s.m.space[len(lo)-1], byteRange{lo, hi})
	}
	s.n = -1
}

func (s *cmapInterp) handleEndBfchar(stk *Stack) {
	if s.n < 0 {
		s.ok = false
		return
	}
	for i := 0; i < s.n; i++ {
		repl, orig := stk.Pop().RawString(), stk.Pop().RawString()
		s.m.bfchar = append(s.m.bfchar, bfchar{orig, repl})
	}
}

func (s *cmapInterp) handleEndBfrange(stk *Stack) {
	if s.n < 0 {
		s.ok = false
		return
	}
	for i := 0; i < s.n; i++ {
		dst, srcHi, srcLo := stk.Pop(), stk.Pop().RawString(), stk.Pop().RawString()
		// Bucket by source-code length (1..4) so lookupBfrange binary-searches
		// only the matching-length entries. Lengths outside 1..4 could never
		// match decodeOne's n=1..4 probes, so dropping them preserves behaviour.
		if n := len(srcLo); n >= 1 && n <= 4 {
			s.m.bfrange[n-1] = append(s.m.bfrange[n-1], bfrange{srcLo, srcHi, dst})
		}
	}
}

// maxCmapEntries caps the number of entries in a single CMap section.
// The PDF spec permits at most 100 per section; we allow 65536 (full BMP) to
// tolerate non-compliant generators while blocking resource-exhaustion attacks.
const maxCmapEntries = 65536

// interpretCmapRanges handles the codespace/bfchar/bfrange operators and the
// debug-unknown default. Separated from interpretCmap to reduce cyclomatic complexity.
func (s *cmapInterp) interpretCmapRanges(stk *Stack, op string) {
	switch op {
	case "begincodespacerange", "beginbfchar", "beginbfrange":
		n := stk.Pop().Int64()
		if n < 0 || n > maxCmapEntries {
			s.ok = false
			return
		}
		s.n = int(n)
	case "endcodespacerange":
		s.handleEndCodespace(stk)
	case "endbfchar":
		s.handleEndBfchar(stk)
	case "endbfrange":
		s.handleEndBfrange(stk)
	default:
		if DebugOn {
			println("interp\t", op)
		}
	}
}

func (s *cmapInterp) interpretCmap(stk *Stack, op string) {
	if !s.ok {
		return
	}
	switch op {
	case "findresource":
		stk.Pop()
		stk.Pop()
		stk.Push(newDict())
	case "begincmap":
		stk.Push(newDict())
	case "endcmap":
		stk.Pop()
	case "defineresource":
		stk.Pop().Name()
		value := stk.Pop()
		stk.Pop().Name()
		stk.Push(value)
	default:
		s.interpretCmapRanges(stk, op)
	}
}

func readCmap(toUnicode Value) *cmap {
	s := &cmapInterp{n: -1, ok: true}
	Interpret(toUnicode, s.interpretCmap)
	if !s.ok {
		return nil
	}
	s.m.buildIndex()
	return &s.m
}
