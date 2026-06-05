// Stream filter decoder: LZWDecode (ISO 32000-1 §7.4.4).

package pdf

import (
	"fmt"
	"io"
)

// lzwCode is a decoded LZW code (up to 12 bits).
type lzwCode = uint16

const (
	lzwClearCode lzwCode = 256
	lzwEODCode   lzwCode = 257
	lzwFirstCode lzwCode = 258

	lzwMaxBits    = 12
	lzwMaxEntries = 1 << lzwMaxBits // 4096
)

// lzwEntry is one entry in the LZW string table.
type lzwEntry struct {
	prefix lzwCode // parent code (lzwClearCode means root/literal)
	suffix byte    // the byte appended to the parent string
}

// lzwReader decodes an LZW-compressed stream per ISO 32000-1 §7.4.4.
// It satisfies io.Reader and buffers decoded bytes in pend.
type lzwReader struct {
	src         io.Reader
	earlyChange bool

	// bit-reader state
	bits  uint32
	nBits uint // number of valid bits in bits (from the MSB end)
	eof   bool // underlying reader is exhausted

	// LZW table
	table [lzwMaxEntries]lzwEntry
	next  lzwCode // next code to assign (starts at lzwFirstCode after Clear)
	width uint    // current code width in bits (9–12)

	// decode state
	prev lzwCode // last emitted code (lzwClearCode means "just cleared")
	pend []byte  // buffered output bytes (oldest first)
	eod  bool    // EOD code was seen

	// scratch buffer for string expansion (reused to avoid allocs)
	scratch []byte
}

// newLZWReader returns a reader decoding LZW data per ISO 32000-1 §7.4.4.
// earlyChange selects the code-width increment convention: true (the PDF
// default, /EarlyChange 1) increments one code early, as in TIFF.
func newLZWReader(rd io.Reader, earlyChange bool) io.Reader {
	r := &lzwReader{
		src:         rd,
		earlyChange: earlyChange,
	}
	r.resetTable()
	return r
}

// resetTable initialises (or resets) the LZW string table.
func (r *lzwReader) resetTable() {
	r.next = lzwFirstCode
	r.width = 9
	r.prev = lzwClearCode
}

// widthLimit returns the code value at which the width must be incremented.
// With earlyChange=true (TIFF/PDF default) we switch one code early.
func (r *lzwReader) widthLimit() lzwCode {
	lim := lzwCode(1) << r.width
	if r.earlyChange {
		return lim - 1
	}
	return lim
}

// readCode reads the next width-bit code from the stream (MSB-first packing).
// Returns (code, true) on success, (0, false) on clean EOF / truncation.
func (r *lzwReader) readCode() (lzwCode, bool, error) {
	// Refill the bit buffer byte by byte until we have enough bits.
	for r.nBits < r.width {
		if r.eof {
			// Partial code at end of stream → treat as EOD (lenient).
			return 0, false, nil
		}
		var buf [1]byte
		n, err := r.src.Read(buf[:])
		if n == 1 {
			r.bits = (r.bits << 8) | uint32(buf[0])
			r.nBits += 8
		}
		if err == io.EOF {
			r.eof = true
			if r.nBits < r.width {
				// Check if trailing bits are all zero (pad) → lenient EOD.
				return 0, false, nil
			}
		} else if err != nil {
			return 0, false, err
		}
	}
	// Extract width bits from the MSB end.
	shift := r.nBits - r.width
	code := lzwCode((r.bits >> shift) & ((1 << r.width) - 1))
	r.nBits -= r.width
	r.bits &= (1 << r.nBits) - 1
	return code, true, nil
}

// expand returns the string for the given code, prepended into r.scratch.
// The result is appended to r.pend (in correct order).
func (r *lzwReader) expand(code lzwCode) {
	// Walk the chain from code to a literal, collecting suffixes in reverse.
	r.scratch = r.scratch[:0]
	cur := code
	for cur >= lzwFirstCode {
		entry := r.table[cur]
		r.scratch = append(r.scratch, entry.suffix)
		cur = entry.prefix
	}
	// cur is now a literal byte (0–255).
	r.scratch = append(r.scratch, byte(cur))
	// Reverse into pend.
	for i := len(r.scratch) - 1; i >= 0; i-- {
		r.pend = append(r.pend, r.scratch[i])
	}
}

// addEntry adds a new entry to the LZW table and advances the width when
// needed. When the table is full (4096 entries) it freezes instead of
// erroring: a conforming encoder emits Clear at that point, but real
// encoders may keep emitting existing 12-bit codes without clearing — the
// decoder stays compatible by assigning no further entries, matching the
// stdlib compress/lzw behavior.
func (r *lzwReader) addEntry(prefix lzwCode, suffix byte) {
	if r.next >= lzwMaxEntries {
		return
	}
	r.table[r.next] = lzwEntry{prefix: prefix, suffix: suffix}
	r.next++
	// Advance width when next assigned code reaches the threshold.
	if r.next >= r.widthLimit() && r.width < lzwMaxBits {
		r.width++
	}
}

// firstByteOf returns the first byte of the string represented by code.
func (r *lzwReader) firstByteOf(code lzwCode) byte {
	cur := code
	for cur >= lzwFirstCode {
		cur = r.table[cur].prefix
	}
	return byte(cur)
}

// decodeNext decodes a single LZW code and appends its output to r.pend.
// On EOD or truncated input it sets r.eod = true and returns nil.
func (r *lzwReader) decodeNext() error {
	code, ok, err := r.readCode()
	if err != nil {
		return err
	}
	if !ok {
		r.eod = true
		return nil
	}

	switch code {
	case lzwEODCode:
		r.eod = true
		return nil
	case lzwClearCode:
		r.resetTable()
		return nil
	}

	// Validate: code must be ≤ next (known entry or KwKwK case).
	if code > r.next {
		return fmt.Errorf("LZWDecode: invalid code %d (next=%d)", code, r.next)
	}

	if r.prev == lzwClearCode {
		// First data code after Clear must be a literal (ISO 32000-1
		// §7.4.4.2): no multi-byte entry exists yet, so a table code here
		// would expand stale or zero-valued entries into forged output.
		if code >= lzwFirstCode {
			return fmt.Errorf("LZWDecode: invalid first code %d after clear", code)
		}
		r.expand(code)
		r.prev = code
		return nil
	}

	// Determine the first byte of the code's string for the new table entry.
	var firstByte byte
	if code == r.next {
		// KwKwK: the new entry's first byte equals its own first byte → use prev's first byte.
		firstByte = r.firstByteOf(r.prev)
	} else {
		firstByte = r.firstByteOf(code)
	}

	r.addEntry(r.prev, firstByte)
	r.expand(code)
	r.prev = code
	return nil
}

// Read implements io.Reader.
func (r *lzwReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.pend) > 0 {
			m := copy(p[n:], r.pend)
			n += m
			r.pend = r.pend[m:]
			continue
		}
		if r.eod {
			break
		}
		// decodeNext sets r.eod on EOD/EOF and appends to r.pend on success.
		if err := r.decodeNext(); err != nil {
			return n, err
		}
	}
	if n == 0 && r.eod {
		return 0, io.EOF
	}
	return n, nil
}
