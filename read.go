// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pdf implements reading of PDF files.
//
// # Overview
//
// PDF is Adobe's Portable Document Format, ubiquitous on the internet.
// A PDF document is a complex data format built on a fairly simple structure.
// This package exposes the simple structure along with some wrappers to
// extract basic information. If more complex information is needed, it is
// possible to extract that information by interpreting the structure exposed
// by this package.
//
// Specifically, a PDF is a data structure built from Values, each of which has
// one of the following Kinds:
//
//	Null, for the null object.
//	Integer, for an integer.
//	Real, for a floating-point number.
//	Bool, for a boolean value.
//	Name, for a name constant (as in /Helvetica).
//	String, for a string constant.
//	Dict, for a dictionary of name-value pairs.
//	Array, for an array of values.
//	Stream, for an opaque data stream and associated header dictionary.
//
// The accessors on Value—Int64, Float64, Bool, Name, and so on—return
// a view of the data as the given type. When there is no appropriate view,
// the accessor returns a zero result. For example, the Name accessor returns
// the empty string if called on a Value v for which v.Kind() != Name.
// Returning zero values this way, especially from the Dict and Array accessors,
// which themselves return Values, makes it possible to traverse a PDF quickly
// without writing any error checking. On the other hand, it means that mistakes
// can go unreported.
//
// The basic structure of the PDF file is exposed as the graph of Values.
//
// Most richer data structures in a PDF file are dictionaries with specific interpretations
// of the name-value pairs. The Font and Page wrappers make the interpretation
// of a specific Value as the corresponding type easier. They are only helpers, though:
// they are implemented only in terms of the Value API and could be moved outside
// the package. Equally important, traversal of other PDF data structures can be implemented
// in other packages as needed.
package pdf

// BUG(rsc): The package is incomplete, although it has been used successfully on some
// large real-world PDF files.

// BUG(rsc): The library makes no attempt at efficiency. A value cache maintained in the Reader
// would probably help significantly.

// BUG(detective-xh): Encryption covers the Standard security handler only:
// RC4 (V=1/2, R=2-3), AES-128 (V=4/R=4), and AES-256 (V=5, R=5-6), each with
// user- and owner-password authentication. Crypt filters resolve per class:
// /Identity, distinct stream/string filters (StmF != StrF), and
// /EncryptMetadata false are handled. The V=4 CFM /V2 (RC4 crypt filter)
// path is supported but not fixture-verified — no common tool emits it
// (qpdf's 128-bit RC4 output is V=2/R=3). Not supported: public-key (PKCS#7)
// handlers and the SASLprep prohibited-output/bidi checks (the mapping and
// NFKC normalization steps are applied).

// BUG(rsc): The Value API does not support error reporting. The intent is to allow users to
// set an error reporting callback in Reader, but that code has not been implemented.

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// DebugOn is responsible for logging messages into stdout. If problems arise during reading, set it true.
var DebugOn = false

// A Reader is a single PDF file open for reading.
type Reader struct {
	f          io.ReaderAt
	end        int64
	xref       []xref
	trailer    dict
	trailerptr objptr
	key        []byte
	stmMode    cipherMode // stream crypt-filter class
	strMode    cipherMode // string crypt-filter class
	// encryptMetadata mirrors /EncryptMetadata (default true, §7.6.3.2): false
	// adds the R=4 key-derivation step and skips decrypting the metadata stream.
	encryptMetadata bool
	// opening is true only while NewReaderEncrypted builds the Reader. It keeps
	// resolve strict on the open path (a malformed object body panics through to
	// the open-path recover and fails the load). After open it is false, so the
	// same panic degrades to a null Value instead of crashing a public getter.
	opening bool
}

// Open opens a file for reading.
func Open(file string) (*os.File, *Reader, error) {
	f, err := os.Open(file)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	reader, err := NewReader(f, fi.Size())
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, reader, err
}

// NewReader opens a file for reading, using the data in f with the given total size.
func NewReader(f io.ReaderAt, size int64) (*Reader, error) {
	return NewReaderEncrypted(f, size, nil)
}

// OpenBytes opens a PDF from an in-memory byte slice.
func OpenBytes(src []byte) (*Reader, error) {
	return NewReader(bytes.NewReader(src), int64(len(src)))
}

// NewReaderEncrypted opens a file for reading, using the data in f with the given total size.
// If the PDF is encrypted, NewReaderEncrypted calls pw repeatedly to obtain passwords
// to try. If pw returns the empty string, NewReaderEncrypted stops trying to decrypt
// the file and returns an error.
func NewReaderEncrypted(f io.ReaderAt, size int64, pw func() string) (r *Reader, err error) {
	defer func() {
		if x := recover(); x != nil {
			r = nil
			if e, ok := x.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("malformed PDF: %v", x)
			}
		}
	}()

	if err = validatePDFHeader(f); err != nil {
		return nil, err
	}
	startxrefPos, err := findStartxrefOffset(f, size)
	if err != nil {
		return nil, err
	}
	r = &Reader{f: f, end: size, opening: true}
	b := newBuffer(io.NewSectionReader(f, startxrefPos, size-startxrefPos), startxrefPos)
	if b.readToken() != keyword("startxref") {
		return nil, fmt.Errorf("malformed PDF file: missing startxref")
	}
	startxref, ok := b.readToken().(int64)
	if !ok {
		return nil, fmt.Errorf("malformed PDF file: startxref not followed by integer")
	}
	b = newBuffer(io.NewSectionReader(r.f, startxref, r.end-startxref), startxref)
	r.xref, r.trailerptr, r.trailer, err = readXref(r, b)
	if err != nil {
		return nil, err
	}
	if err = tryDecrypt(r, pw); err != nil {
		return nil, err
	}
	// Open succeeded; from here on resolve must not crash a public getter on a
	// malformed object body, so leave the strict open-path mode.
	r.opening = false
	return r, nil
}

// Trailer returns the file's Trailer value.
func (r *Reader) Trailer() Value {
	return Value{r, r.trailerptr, r.trailer}
}
