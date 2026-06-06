// Stream filter decoders: ASCII85, ASCIIHex, RunLength,
// FlateDecode (zlib + PNG Up predictor).

package pdf

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"fmt"
	"io"
)

// errUnsupportedFilter marks filter names and filter-entry kinds this package
// cannot decode. Value.Reader surfaces it as an unsupported_filter warning;
// other (malformed-stream) errors are deliberately not warned — they belong
// to the future fallback-decoding framework, not this diagnostics layer.
var errUnsupportedFilter = errors.New("unsupported PDF filter")

const maxDecompressedSize = 256 << 20
const maxPNGColumns = 65536

type alphaReader struct {
	reader io.Reader
}

func newAlphaReader(reader io.Reader) *alphaReader {
	return &alphaReader{reader: reader}
}

func checkASCII85(r byte) byte {
	if r >= '!' && r <= 'u' { // 33 <= ascii85 <=117
		return r
	}
	if r == '~' {
		return 1 // for marking possible end of data
	}
	return 0 // if non-ascii85
}

func (a *alphaReader) Read(p []byte) (int, error) {
	n, err := a.reader.Read(p)
	if err != nil {
		return n, err
	}
	buf := make([]byte, n)
	tilda := false
	for i := range n {
		char := checkASCII85(p[i])
		if char == '>' && tilda { // end of data
			break
		}
		if char > 1 {
			buf[i] = char
		}
		if char == 1 {
			tilda = true // possible end of data
		}
	}

	copy(p, buf)
	return n, nil
}

func applyFilter(rd io.Reader, name string, param Value) (io.Reader, error) {
	switch name {
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFilter, name)
	case "FlateDecode":
		return applyFlateFilter(rd, param)
	case "ASCII85Decode":
		cleanASCII85 := newAlphaReader(rd)
		decoder := ascii85.NewDecoder(cleanASCII85)

		switch param.Keys() {
		default:
			if DebugOn {
				fmt.Println("param=", param)
			}
			return nil, fmt.Errorf("unexpected DecodeParms for ASCII85Decode")
		case nil:
			return decoder, nil
		}
	case "LZWDecode":
		return applyLZWFilter(rd, param)
	case "ASCIIHexDecode":
		if param.Keys() != nil {
			return nil, fmt.Errorf("unexpected DecodeParms for ASCIIHexDecode")
		}
		return &asciiHexReader{src: bufio.NewReader(rd)}, nil
	case "RunLengthDecode":
		if param.Keys() != nil {
			return nil, fmt.Errorf("unexpected DecodeParms for RunLengthDecode")
		}
		return io.LimitReader(&runLengthReader{src: bufio.NewReader(rd)}, maxDecompressedSize), nil
	}
}

// applyFlateFilter opens a zlib stream and applies the optional predictor
// declared in DecodeParms.
func applyFlateFilter(rd io.Reader, param Value) (io.Reader, error) {
	zr, err := zlib.NewReader(rd)
	if err != nil {
		return nil, fmt.Errorf("FlateDecode: %v", err)
	}
	return applyPredictor(io.LimitReader(zr, maxDecompressedSize), param)
}

// applyLZWFilter wraps rd in an LZW decoder (ISO 32000-1 §7.4.4) honoring
// the /EarlyChange convention (default 1), then applies the optional
// predictor declared in DecodeParms.
func applyLZWFilter(rd io.Reader, param Value) (io.Reader, error) {
	early := int64(1)
	if ec := param.Key("EarlyChange"); ec.Kind() != Null {
		early = ec.Int64()
		if early != 0 && early != 1 {
			return nil, fmt.Errorf("LZWDecode: invalid EarlyChange value: %d", early)
		}
	}
	limited := io.LimitReader(newLZWReader(rd, early == 1), maxDecompressedSize)
	return applyPredictor(limited, param)
}

// asciiHexReader decodes ASCIIHexDecode data (ISO 32000-1 §7.4.2): pairs of
// hex digits with whitespace ignored and '>' as end-of-data; a final odd
// digit is padded with zero.
type asciiHexReader struct {
	src *bufio.Reader
	eod bool
}

// nextHexDigit returns the value of the next hex digit, skipping whitespace.
// It returns -1 at end-of-data ('>' or EOF) and an error on any other byte.
func (r *asciiHexReader) nextHexDigit() (int, error) {
	for {
		c, err := r.src.ReadByte()
		if err == io.EOF || err == nil && c == '>' {
			r.eod = true
			return -1, nil
		}
		if err != nil {
			return 0, err
		}
		if isSpace(c) {
			continue
		}
		if v := unhex(c); v >= 0 {
			return v, nil
		}
		return 0, fmt.Errorf("ASCIIHexDecode: invalid byte %#x", c)
	}
}

func (r *asciiHexReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) && !r.eod {
		hi, err := r.nextHexDigit()
		if err != nil {
			return n, err
		}
		if hi < 0 {
			break
		}
		lo, err := r.nextHexDigit()
		if err != nil {
			return n, err
		}
		if lo < 0 {
			lo = 0 // odd digit count: final digit is followed by an implied 0
		}
		p[n] = byte(hi<<4 | lo)
		n++
	}
	if n == 0 && r.eod {
		return 0, io.EOF
	}
	return n, nil
}

// runLengthReader decodes RunLengthDecode data (ISO 32000-1 §7.4.5): a length
// byte L followed by L+1 literal bytes (L <= 127), or one byte repeated
// 257-L times (L >= 129); L == 128 is end-of-data.
type runLengthReader struct {
	src  *bufio.Reader
	pend []byte
	eod  bool
}

// fill decodes the next run into r.pend. EOF at a run boundary is treated as
// end-of-data (a missing EOD marker); EOF inside a run is an error.
func (r *runLengthReader) fill() error {
	l, err := r.src.ReadByte()
	if err == io.EOF || err == nil && l == 128 {
		r.eod = true
		return nil
	}
	if err != nil {
		return err
	}
	if l <= 127 {
		buf := make([]byte, int(l)+1)
		if _, err := io.ReadFull(r.src, buf); err != nil {
			return fmt.Errorf("RunLengthDecode: truncated literal run")
		}
		r.pend = buf
		return nil
	}
	c, err := r.src.ReadByte()
	if err != nil {
		return fmt.Errorf("RunLengthDecode: truncated repeat run")
	}
	r.pend = bytes.Repeat([]byte{c}, 257-int(l))
	return nil
}

func (r *runLengthReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.pend) == 0 {
			if r.eod {
				break
			}
			if err := r.fill(); err != nil {
				return n, err
			}
			continue
		}
		m := copy(p[n:], r.pend)
		n += m
		r.pend = r.pend[m:]
	}
	if n == 0 && r.eod {
		return 0, io.EOF
	}
	return n, nil
}

type errorReadCloser struct {
	err error
}

func (e *errorReadCloser) Read([]byte) (int, error) {
	return 0, e.err
}

func (e *errorReadCloser) Close() error {
	return e.err
}

// Reader returns the data contained in the stream v.
// If v.Kind() != Stream, Reader returns a ReadCloser that
// responds to all reads with a "stream not present" error.
func (v Value) Reader() io.ReadCloser {
	x, ok := v.data.(stream)
	if !ok {
		return &errorReadCloser{fmt.Errorf("stream not present")}
	}
	streamLen := v.Key("Length").Int64()
	if streamLen == 0 {
		return io.NopCloser(bytes.NewReader(nil))
	}
	rd := v.buildStreamReader(x, streamLen)
	filter := v.Key("Filter")
	param := v.Key("DecodeParms")
	out, err := applyStreamFilters(rd, filter, param)
	if err != nil {
		if errors.Is(err, errUnsupportedFilter) {
			v.warn(WarningUnsupportedFilter, filterDetail(filter))
		}
		return &errorReadCloser{err}
	}
	return io.NopCloser(out)
}

func (v Value) buildStreamReader(x stream, streamLen int64) io.Reader {
	var rd io.Reader
	rd = io.NewSectionReader(v.r.f, x.offset, streamLen)
	if v.r.key != nil && v.r.stmMode != modeNone && !v.isCleartextMetadata() {
		rd = decryptStream(v.r.key, v.r.stmMode, x.ptr, rd)
	}
	return rd
}

// isCleartextMetadata reports whether v is the XMP metadata stream of a
// document whose /EncryptMetadata is false (ISO 32000-1 §7.6.5.4): such a
// stream is stored in cleartext and must not be run through the cipher.
// The dict keys match what producers write (qpdf 12 emits
// << /Type /Metadata /Subtype /XML >> — verified against the
// cleartext-metadata fixtures in testdata/encrypted). Key resolves indirect
// objects, matching how Length/Filter/DecodeParms are read elsewhere in this
// file.
func (v Value) isCleartextMetadata() bool {
	return !v.r.encryptMetadata &&
		v.Key("Type").Name() == "Metadata" && v.Key("Subtype").Name() == "XML"
}

func applyStreamFilters(rd io.Reader, filter, param Value) (io.Reader, error) {
	switch filter.Kind() {
	case Null:
		return rd, nil
	case Name:
		return applyFilter(rd, filter.Name(), param)
	case Array:
		return applyArrayFilters(rd, filter, param)
	default:
		return nil, fmt.Errorf("%w: non-name /Filter entry", errUnsupportedFilter)
	}
}

func applyArrayFilters(rd io.Reader, filter, param Value) (io.Reader, error) {
	for i := 0; i < filter.Len(); i++ {
		var err error
		rd, err = applyFilter(rd, filter.Index(i).Name(), param.Index(i))
		if err != nil {
			return nil, err
		}
	}
	return rd, nil
}
