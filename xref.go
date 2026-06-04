package pdf

import (
	"bytes"
	"fmt"
	"io"
)

type xref struct {
	ptr      objptr
	inStream bool
	stream   objptr
	offset   int64
}

func readXref(r *Reader, b *buffer) ([]xref, objptr, dict, error) {
	tok := b.readToken()
	if tok == keyword("xref") {
		return readXrefTable(r, b)
	}
	if _, ok := tok.(int64); ok {
		b.unreadToken(tok)
		return readXrefStream(r, b)
	}
	return nil, objptr{}, nil, fmt.Errorf("malformed PDF: cross-reference table not found: %v", tok)
}

func followXrefTablePrevChain(r *Reader, table []xref, trailer dict) ([]xref, error) {
	seen := map[int64]bool{}
	for prevoff := trailer["Prev"]; prevoff != nil; {
		off, ok := prevoff.(int64)
		if !ok {
			return nil, fmt.Errorf("malformed PDF: xref Prev is not integer: %v", prevoff)
		}
		if seen[off] {
			return nil, fmt.Errorf("malformed PDF: cyclic xref /Prev chain at offset %d", off)
		}
		seen[off] = true
		nextTable, nextPrev, err := applyPrevXrefTable(r, off, table)
		if err != nil {
			return nil, fmt.Errorf("malformed PDF: %v", err)
		}
		table, prevoff = nextTable, nextPrev
	}
	return table, nil
}

// applyPrevXrefTable loads one "Prev" xref table at absOffset, applies it to
// table, and returns the updated table and the next Prev value (nil when the
// chain ends).
func applyPrevXrefTable(r *Reader, absOffset int64, table []xref) ([]xref, any, error) {
	b := newBuffer(io.NewSectionReader(r.f, absOffset, r.end-absOffset), absOffset)
	if tok := b.readToken(); tok != keyword("xref") {
		return nil, nil, fmt.Errorf("xref Prev does not point to xref")
	}
	var err error
	if table, err = readXrefTableData(b, table); err != nil {
		return nil, nil, err
	}
	trailer, ok := b.readObject().(dict)
	if !ok {
		return nil, nil, fmt.Errorf("xref Prev table not followed by trailer dictionary")
	}
	return table, trailer["Prev"], nil
}

const maxXrefObjects = 8_388_607 // PDF spec max indirect object number

func ensureXrefSlot(table []xref, x int) ([]xref, error) {
	if x < 0 || x > maxXrefObjects {
		return nil, fmt.Errorf("malformed PDF: xref object number out of range: %d", x)
	}
	for cap(table) <= x {
		table = append(table[:cap(table)], xref{})
	}
	if len(table) <= x {
		table = table[:x+1]
	}
	return table, nil
}

// readXrefTableEntry reads and validates one xref table entry (offset gen alloc).
func readXrefTableEntry(b *buffer) (off, gen int64, alloc keyword, err error) {
	var ok1, ok2, ok3 bool
	off, ok1 = b.readToken().(int64)
	gen, ok2 = b.readToken().(int64)
	alloc, ok3 = b.readToken().(keyword)
	if !ok1 || !ok2 || !ok3 || alloc != keyword("f") && alloc != keyword("n") {
		return 0, 0, "", fmt.Errorf("malformed xref table")
	}
	return
}

func readXrefTable(r *Reader, b *buffer) ([]xref, objptr, dict, error) {
	var table []xref
	table, err := readXrefTableData(b, table)
	if err != nil {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: %v", err)
	}
	trailer, ok := b.readObject().(dict)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref table not followed by trailer dictionary")
	}
	if table, err = followXrefTablePrevChain(r, table, trailer); err != nil {
		return nil, objptr{}, nil, err
	}
	size, ok := trailer[name("Size")].(int64)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: trailer missing /Size entry")
	}
	if size < int64(len(table)) {
		table = table[:size]
	}
	return table, objptr{}, trailer, nil
}

func applyXrefTableSection(b *buffer, start int64, n int64, table []xref) ([]xref, error) {
	for i := 0; i < int(n); i++ {
		off, gen, alloc, err := readXrefTableEntry(b)
		if err != nil {
			return nil, err
		}
		x := int(start) + i
		table, err = ensureXrefSlot(table, x)
		if err != nil {
			return nil, err
		}
		if alloc == "n" && table[x].offset == 0 {
			table[x] = xref{ptr: objptr{uint32(x), uint16(gen)}, offset: int64(off)}
		}
	}
	return table, nil
}

func readXrefTableData(b *buffer, table []xref) ([]xref, error) {
	for {
		tok := b.readToken()
		if tok == keyword("trailer") {
			break
		}
		start, ok1 := tok.(int64)
		n, ok2 := b.readToken().(int64)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("malformed xref table")
		}
		var err error
		table, err = applyXrefTableSection(b, start, n, table)
		if err != nil {
			return nil, err
		}
	}
	return table, nil
}

func findLastLine(buf []byte, s string) int {
	bs := []byte(s)
	max := len(buf)
	for {
		i := bytes.LastIndex(buf[:max], bs)
		if i <= 0 || i+len(bs) >= len(buf) {
			return -1
		}
		if (buf[i-1] == '\n' || buf[i-1] == '\r') && (buf[i+len(bs)] == '\n' || buf[i+len(bs)] == '\r') {
			return i
		}
		max = i
	}
}

// findStartxrefFallback scans f backwards for %%EOF when it is not located
// within the last endChunk bytes, then returns the absolute file offset of
// the preceding "startxref" line.  Called only for non-conformant PDFs that
// append data (e.g. a digital signature) after %%EOF.
func findStartxrefFallback(f io.ReaderAt, size int64) (int64, error) {
	const scanChunk = 4096
	const overlap = 4 // len("%%EOF")-1: handles %%EOF straddling a chunk boundary

	var suffix []byte
	for pos := size; pos > 0; {
		start := max(pos-scanChunk, 0)
		chunkLen := pos - start
		combined := make([]byte, chunkLen+int64(len(suffix)))
		if _, err := f.ReadAt(combined[:chunkLen], start); err != nil && err != io.EOF {
			return -1, err
		}
		copy(combined[chunkLen:], suffix)

		if idx := bytes.LastIndex(combined, []byte("%%EOF")); idx >= 0 {
			eofAbs := start + int64(idx)
			ctxStart := max(eofAbs-512, 0)
			ctx := make([]byte, eofAbs-ctxStart)
			if _, err := f.ReadAt(ctx, ctxStart); err != nil && err != io.EOF {
				return -1, err
			}
			j := findLastLine(ctx, "startxref")
			if j < 0 {
				return -1, fmt.Errorf("%%EOF found but startxref missing")
			}
			return ctxStart + int64(j), nil
		}

		if chunkLen >= int64(overlap) {
			suffix = make([]byte, overlap)
			copy(suffix, combined[:overlap])
		} else {
			suffix = make([]byte, chunkLen)
			copy(suffix, combined[:chunkLen])
		}
		pos = start
	}
	return -1, fmt.Errorf("%%EOF not found in file")
}

func isValidPDFTerminator(b byte) bool {
	return b == '\r' || b == '\n' || b == ' ' || b == '\t'
}

// validatePDFHeader reads the first 10 bytes of f and returns an error when
// the %PDF-n.m header is missing or malformed.
func validatePDFHeader(f io.ReaderAt) error {
	buf := make([]byte, 10)
	if _, err := f.ReadAt(buf, 0); err != nil && err != io.EOF {
		return err
	}
	if !bytes.HasPrefix(buf, []byte("%PDF-1.")) || buf[7] < '0' || buf[7] > '7' || !isValidPDFTerminator(buf[8]) {
		return fmt.Errorf("not a PDF file: invalid header")
	}
	return nil
}

// findStartxrefOffset locates the "startxref" keyword near the end of f and
// returns its absolute byte offset.  It searches the last 1024 bytes first;
// when %%EOF is not found there, it falls back to a full reverse scan.
func findStartxrefOffset(f io.ReaderAt, size int64) (int64, error) {
	const endChunk = 1024
	readStart := max(size-endChunk, 0)
	buf := make([]byte, size-readStart)
	if _, err := f.ReadAt(buf, readStart); err != nil && err != io.EOF {
		return -1, err
	}
	buf = bytes.TrimRight(buf, "\r\n\t ")
	if bytes.HasSuffix(buf, []byte("%%EOF")) {
		i := findLastLine(buf, "startxref")
		if i < 0 {
			return -1, fmt.Errorf("malformed PDF file: missing final startxref")
		}
		return readStart + int64(i), nil
	}
	pos, err := findStartxrefFallback(f, size)
	if err != nil {
		return -1, fmt.Errorf("not a PDF file: missing %%%%EOF")
	}
	return pos, nil
}
