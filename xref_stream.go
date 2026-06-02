package pdf

import (
	"fmt"
	"io"
)

func readXrefStream(r *Reader, b *buffer) ([]xref, objptr, dict, error) {
	obj1 := b.readObject()
	obj, ok := obj1.(objdef)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: cross-reference table not found: %v", objfmt(obj1))
	}
	strmptr := obj.ptr
	strm, ok := obj.obj.(stream)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: cross-reference table not found: %v", objfmt(obj))
	}
	if strm.hdr["Type"] != name("XRef") {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref stream does not have type XRef")
	}
	size, ok := strm.hdr["Size"].(int64)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref stream missing Size")
	}
	if size <= 0 || size > maxXrefObjects {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref Size out of range: %d", size)
	}
	table := make([]xref, size)
	table, err := readXrefStreamData(r, strm, table, size)
	if err != nil {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: %v", err)
	}
	if table, err = followXrefStreamPrevChain(r, table, size, strm.hdr); err != nil {
		return nil, objptr{}, nil, err
	}
	return table, strmptr, strm.hdr, nil
}

func followXrefStreamPrevChain(r *Reader, table []xref, size int64, hdr dict) ([]xref, error) {
	for prevoff := hdr["Prev"]; prevoff != nil; {
		off, ok := prevoff.(int64)
		if !ok {
			return nil, fmt.Errorf("malformed PDF: xref Prev is not integer: %v", prevoff)
		}
		nextTable, nextPrev, err := applyPrevXrefStream(r, off, table, size)
		if err != nil {
			return nil, fmt.Errorf("malformed PDF: %v", err)
		}
		table, prevoff = nextTable, nextPrev
	}
	return table, nil
}

// applyPrevXrefStream loads one "Prev" xref stream at absOffset, applies it to
// table, and returns the updated table and the next Prev value (nil when the
// chain ends).
func applyPrevXrefStream(r *Reader, absOffset int64, table []xref, maxSize int64) ([]xref, any, error) {
	b := newBuffer(io.NewSectionReader(r.f, absOffset, r.end-absOffset), absOffset)
	obj1 := b.readObject()
	obj, ok := obj1.(objdef)
	if !ok {
		return nil, nil, fmt.Errorf("xref prev stream not found: %v", objfmt(obj1))
	}
	prevstrm, ok := obj.obj.(stream)
	if !ok {
		return nil, nil, fmt.Errorf("xref prev stream not found: %v", objfmt(obj))
	}
	prev := Value{r, objptr{}, prevstrm}
	if prev.Kind() != Stream {
		return nil, nil, fmt.Errorf("xref prev stream is not stream: %v", prev)
	}
	if prev.Key("Type").Name() != "XRef" {
		return nil, nil, fmt.Errorf("xref prev stream does not have type XRef")
	}
	psize := prev.Key("Size").Int64()
	if psize > maxSize {
		return nil, nil, fmt.Errorf("xref prev stream larger than last stream")
	}
	var err error
	if table, err = readXrefStreamData(r, prev.data.(stream), table, psize); err != nil {
		return nil, nil, fmt.Errorf("reading xref prev stream: %v", err)
	}
	return table, prevstrm.hdr["Prev"], nil
}

// parseWArray validates and converts the PDF xref-stream W array to []int.
func parseWArray(ww array) ([]int, error) {
	var w []int
	for _, x := range ww {
		i, ok := x.(int64)
		if !ok || int64(int(i)) != i {
			return nil, fmt.Errorf("invalid W array %v", objfmt(ww))
		}
		w = append(w, int(i))
	}
	if len(w) < 3 {
		return nil, fmt.Errorf("invalid W array %v", objfmt(ww))
	}
	return w, nil
}

// processXrefEntry reads one entry from an xref stream and updates table.
func processXrefEntry(buf []byte, w []int, start int64, i int, table []xref, data io.ReadCloser) ([]xref, error) {
	if _, err := io.ReadFull(data, buf); err != nil {
		return nil, fmt.Errorf("error reading xref stream: %v", err)
	}
	v1 := decodeInt(buf[0:w[0]])
	if w[0] == 0 {
		v1 = 1
	}
	v2 := decodeInt(buf[w[0] : w[0]+w[1]])
	v3 := decodeInt(buf[w[0]+w[1] : w[0]+w[1]+w[2]])
	x := int(start) + i
	var err error
	table, err = ensureXrefSlot(table, x)
	if err != nil {
		return nil, err
	}
	if table[x].ptr != (objptr{}) {
		return table, nil
	}
	switch v1 {
	case 0:
		table[x] = xref{ptr: objptr{0, 65535}}
	case 1:
		table[x] = xref{ptr: objptr{uint32(x), uint16(v3)}, offset: int64(v2)}
	case 2:
		table[x] = xref{ptr: objptr{uint32(x), 0}, inStream: true, stream: objptr{uint32(v2), 0}, offset: int64(v3)}
	default:
		if DebugOn {
			fmt.Printf("invalid xref stream type %d: %x\n", v1, buf)
		}
	}
	return table, nil
}

func readXrefStreamData(r *Reader, strm stream, table []xref, size int64) ([]xref, error) {
	index, _ := strm.hdr["Index"].(array)
	if index == nil {
		index = array{int64(0), size}
	}
	if len(index)%2 != 0 {
		return nil, fmt.Errorf("invalid Index array %v", objfmt(index))
	}
	ww, ok := strm.hdr["W"].(array)
	if !ok {
		return nil, fmt.Errorf("xref stream missing W array")
	}
	w, err := parseWArray(ww)
	if err != nil {
		return nil, err
	}
	v := Value{r, objptr{}, strm}
	wtotal := 0
	for _, wid := range w {
		wtotal += wid
	}
	buf := make([]byte, wtotal)
	data := v.Reader()
	return processXrefIndexPairs(data, buf, w, index, table)
}

func processXrefIndexPairs(data io.ReadCloser, buf []byte, w []int, index array, table []xref) ([]xref, error) {
	for len(index) > 0 {
		start, ok1 := index[0].(int64)
		n, ok2 := index[1].(int64)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("malformed Index pair %v %v %T %T", objfmt(index[0]), objfmt(index[1]), index[0], index[1])
		}
		index = index[2:]
		for i := 0; i < int(n); i++ {
			var err error
			table, err = processXrefEntry(buf, w, start, i, table, data)
			if err != nil {
				return nil, err
			}
		}
	}
	return table, nil
}

func decodeInt(b []byte) int {
	x := 0
	for _, c := range b {
		x = x<<8 | int(c)
	}
	return x
}
