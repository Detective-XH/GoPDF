// Raw XMP metadata extraction from the PDF catalog's /Metadata stream.

package pdf

import (
	"fmt"
	"io"
)

// XMP returns the decoded bytes of the PDF catalog's /Metadata stream, if
// present. The content is returned as stored — typically a UTF-8 XMP packet
// (application/rdf+xml) — without any validation; callers should parse and
// validate it with standard XML tooling. Returns (nil, nil) for PDFs whose
// catalog has no /Metadata entry, or whose metadata stream is empty. A
// metadata stream larger than maxDecompressedSize returns an error: the
// stream length is attacker-controlled, and the one-call API must not be a
// memory-exhaustion surface (same bound the stream filters enforce).
func (r *Reader) XMP() ([]byte, error) {
	catalog := r.Trailer().Key("Root")
	if catalog.IsNull() {
		return nil, nil
	}
	meta := catalog.Key("Metadata")
	if meta.IsNull() {
		return nil, nil
	}
	rc := meta.Reader()
	defer func() { _ = rc.Close() }()
	data, err := readAllLimited(rc, maxDecompressedSize)
	if err != nil || len(data) == 0 {
		// Normalize a present-but-empty stream to (nil, nil): xmp == nil is
		// the single "no metadata" check for callers.
		return nil, err
	}
	return data, nil
}

// readAllLimited reads rc to EOF, erroring when the source yields more than
// limit bytes. Unlike a bare io.LimitReader (which would silently truncate),
// exceeding the bound is reported: serving a partial metadata packet as if
// complete would hand callers undetectably corrupt XML.
func readAllLimited(rc io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("malformed PDF: metadata stream exceeds %d bytes", limit)
	}
	return data, nil
}
