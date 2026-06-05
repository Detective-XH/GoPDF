// filter_predictor.go — ISO 32000-1 §7.4.4.4 predictor post-processing
// for FlateDecode and LZWDecode streams.
//
// Supported predictors:
//
//	1  — no-op (pass through)
//	2  — TIFF horizontal differencing (bpc=8 only)
//	10–15 — PNG reconstruction (per-row filter-type byte is authoritative)

package pdf

import (
	"fmt"
	"io"
)

// paramInt returns the integer value of param.Key(key).
// If that key is absent (Kind==Null) it returns def.
func paramInt(param Value, key string, def int64) int64 {
	v := param.Key(key)
	if v.Kind() == Null {
		return def
	}
	return v.Int64()
}

// validBPC returns true when bpc is one of the values allowed by the PDF spec.
func validBPC(bpc int64) bool {
	switch bpc {
	case 1, 2, 4, 8, 16:
		return true
	}
	return false
}

// predictorParams parses and validates DecodeParms fields that are shared by
// both the TIFF and PNG predictor paths.  predictor==1 (or Null) is handled
// before this call and is never passed in.
//
// Returns (columns, colors, bpc, rowBytes, bytesPerPixel, error).
func predictorParams(param Value) (columns, colors, bpc, rowBytes, bpp int64, err error) {
	columns = paramInt(param, "Columns", 1)
	if columns < 1 || columns > maxPNGColumns {
		return 0, 0, 0, 0, 0, fmt.Errorf("predictor: invalid Columns value: %d", columns)
	}

	colors = paramInt(param, "Colors", 1)
	if colors < 1 || colors > 32 {
		return 0, 0, 0, 0, 0, fmt.Errorf("predictor: invalid Colors value: %d", colors)
	}

	bpc = paramInt(param, "BitsPerComponent", 8)
	if !validBPC(bpc) {
		return 0, 0, 0, 0, 0, fmt.Errorf("predictor: unsupported BitsPerComponent %d", bpc)
	}

	rowBytes = (columns*colors*bpc + 7) / 8
	if rowBytes > 1<<20 {
		return 0, 0, 0, 0, 0, fmt.Errorf("predictor: row too large (%d bytes)", rowBytes)
	}

	bpp = max(colors*bpc/8, 1)
	return columns, colors, bpc, rowBytes, bpp, nil
}

// applyPredictor wraps rd with the predictor declared in the stream's
// DecodeParms (ISO 32000-1 §7.4.4.4). Predictor absent or 1 returns rd
// unchanged; 2 applies TIFF horizontal differencing; 10–15 apply the PNG
// reconstruction functions, dispatching on each row's filter-type byte.
func applyPredictor(rd io.Reader, param Value) (io.Reader, error) {
	pred := param.Key("Predictor")
	if pred.Kind() == Null {
		return rd, nil
	}
	predVal := pred.Int64()
	if predVal == 1 {
		return rd, nil
	}

	switch {
	case predVal == 2:
		return newTIFFPredictorReader(rd, param)
	case predVal >= 10 && predVal <= 15:
		return newPNGPredictorReader(rd, param)
	default:
		return nil, fmt.Errorf("unsupported predictor: %d", predVal)
	}
}

// ---------------------------------------------------------------------------
// TIFF predictor (predictor 2)
// ---------------------------------------------------------------------------

type tiffPredictorReader struct {
	r        io.Reader
	rowBytes int64
	bpp      int64
	pend     []byte
}

func newTIFFPredictorReader(rd io.Reader, param Value) (io.Reader, error) {
	_, _, bpc, rowBytes, bpp, err := predictorParams(param)
	if err != nil {
		return nil, err
	}
	if bpc != 8 {
		return nil, fmt.Errorf("TIFF predictor: unsupported BitsPerComponent %d", bpc)
	}
	return &tiffPredictorReader{
		r:        rd,
		rowBytes: rowBytes,
		bpp:      bpp,
	}, nil
}

func (t *tiffPredictorReader) Read(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if len(t.pend) > 0 {
			m := copy(p, t.pend)
			n += m
			p = p[m:]
			t.pend = t.pend[m:]
			continue
		}
		row := make([]byte, t.rowBytes)
		_, err := io.ReadFull(t.r, row)
		if err != nil {
			return n, err
		}
		for i := t.bpp; i < t.rowBytes; i++ {
			row[i] += row[i-t.bpp]
		}
		t.pend = row
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// PNG predictor (predictors 10–15)
// ---------------------------------------------------------------------------

type pngPredictorReader struct {
	r        io.Reader
	rowBytes int64
	bpp      int64
	prior    []byte // previous decoded row (all zeros before first row)
	pend     []byte // decoded data ready to copy out
}

func newPNGPredictorReader(rd io.Reader, param Value) (io.Reader, error) {
	_, _, _, rowBytes, bpp, err := predictorParams(param)
	if err != nil {
		return nil, err
	}
	return &pngPredictorReader{
		r:        rd,
		rowBytes: rowBytes,
		bpp:      bpp,
		prior:    make([]byte, rowBytes),
	}, nil
}

func (p *pngPredictorReader) Read(b []byte) (int, error) {
	n := 0
	for len(b) > 0 {
		if len(p.pend) > 0 {
			m := copy(b, p.pend)
			n += m
			b = b[m:]
			p.pend = p.pend[m:]
			continue
		}
		// Read 1 filter-type byte + rowBytes data bytes.
		tmp := make([]byte, 1+p.rowBytes)
		_, err := io.ReadFull(p.r, tmp)
		if err != nil {
			return n, err
		}
		filterType := tmp[0]
		row := tmp[1:]
		if err2 := p.reconstruct(filterType, row); err2 != nil {
			return n, err2
		}
		// row is now the decoded output; save as prior for next iteration.
		decoded := make([]byte, p.rowBytes)
		copy(decoded, row)
		p.prior = decoded
		p.pend = decoded
	}
	return n, nil
}

// reconstruct applies the PNG filter reconstruction in-place on row.
// prior is the previously decoded row (zeros on first row).
func (p *pngPredictorReader) reconstruct(filterType byte, row []byte) error {
	switch filterType {
	case 0: // None
		// nothing to do
	case 1: // Sub
		reconstructSub(row, p.bpp)
	case 2: // Up
		reconstructUp(row, p.prior)
	case 3: // Average
		reconstructAverage(row, p.prior, p.bpp)
	case 4: // Paeth
		reconstructPaeth(row, p.prior, p.bpp)
	default:
		return fmt.Errorf("invalid PNG predictor row filter: %d", filterType)
	}
	return nil
}

// reconstructSub applies PNG Sub filter: each byte is the sum of the raw byte
// and the byte bpp positions to the left.
func reconstructSub(row []byte, bpp int64) {
	for i := bpp; i < int64(len(row)); i++ {
		row[i] += row[i-bpp]
	}
}

// reconstructUp applies PNG Up filter: each byte is the sum of the raw byte
// and the corresponding byte from the prior row.
func reconstructUp(row, prior []byte) {
	for i := range row {
		row[i] += prior[i]
	}
}

// reconstructAverage applies PNG Average filter: each byte is the raw byte
// plus floor((left+up)/2).
func reconstructAverage(row, prior []byte, bpp int64) {
	ibpp := int(bpp)
	for i := range row {
		left := 0
		if i >= ibpp {
			left = int(row[i-ibpp])
		}
		up := int(prior[i])
		row[i] += byte((left + up) / 2)
	}
}

// reconstructPaeth applies PNG Paeth filter: each byte is the raw byte plus
// the Paeth predictor value selected from left, up, or upper-left.
func reconstructPaeth(row, prior []byte, bpp int64) {
	ibpp := int(bpp)
	for i := range row {
		var left, upLeft int
		if i >= ibpp {
			left = int(row[i-ibpp])
			upLeft = int(prior[i-ibpp])
		}
		up := int(prior[i])
		row[i] += byte(paethPredictor(left, up, upLeft))
	}
}

// paethPredictor returns the Paeth predictor value (PNG spec §6.6).
// It picks the neighbor (a=left, b=up, c=upper-left) closest to p=a+b-c;
// ties prefer left, then up, then upper-left.
func paethPredictor(a, b, c int) int {
	p := a + b - c
	pa := abs(p - a)
	pb := abs(p - b)
	pc := abs(p - c)
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
