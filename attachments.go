package pdf

import "io"

// Attachment describes one file embedded at the document level via the
// /Names /EmbeddedFiles name tree.
type Attachment struct {
	Name     string // filename: /UF preferred, /F fallback, then the name-tree key
	MimeType string // /Subtype of the embedded-file stream dict ("" if absent)
	Size     int64  // /Params /Size of the embedded-file stream (0 if absent)
	// Data returns a fresh reader over the decoded (decompressed, decrypted)
	// bytes of the embedded file. The caller must Close it. Decode errors
	// (e.g. an unsupported /Crypt filter) surface on the first Read, matching
	// Value.Reader() semantics; the error return is reserved and nil today.
	Data func() (io.ReadCloser, error)
}

// Attachments returns all files embedded at the document level via the
// /Names /EmbeddedFiles name tree, in name-tree order.
//
// Returns (nil, nil) for documents with no embedded files. Filespec entries
// without an embedded-file stream (/EF) are skipped: they reference external
// files rather than embedding data. Page-level /FileAttachment annotations
// are not scanned (deferred — see ROADMAP-V0-8-0). Safe for concurrent use:
// each call walks the immutable name tree with per-call locals only.
func (r *Reader) Attachments() ([]Attachment, error) {
	tree := r.Trailer().Key("Root").Key("Names").Key("EmbeddedFiles")
	if tree.IsNull() {
		return nil, nil
	}
	var out []Attachment
	collectNameTree(tree, make(map[uint32]bool), 0, func(key, filespec Value) {
		ef := filespec.Key("EF")
		stream := ef.Key("UF")
		if stream.Kind() != Stream {
			stream = ef.Key("F")
		}
		if stream.Kind() != Stream {
			return // external-reference or malformed filespec: nothing embedded
		}
		name := filespec.Key("UF").Text()
		if name == "" {
			name = filespec.Key("F").Text()
		}
		if name == "" {
			name = key.Text()
		}
		out = append(out, Attachment{
			Name:     name,
			MimeType: stream.Key("Subtype").Name(),
			Size:     stream.Key("Params").Key("Size").Int64(),
			Data:     func() (io.ReadCloser, error) { return stream.Reader(), nil },
		})
	})
	return out, nil
}

// collectNameTree walks a PDF name tree rooted at node and invokes fn for
// every (key, value) leaf pair in tree order. It is the enumeration sibling
// of walkNameTreeDepth (annotations.go) and shares its bounds: the depth cap
// (maxLinkDepth) prevents a deeply nested tree from overflowing the stack,
// and the visited set short-circuits a /Kids cycle that points back to an
// ancestor node. The lookup walker is left untouched (it has no direct test
// coverage to anchor a refactor against).
func collectNameTree(node Value, seen map[uint32]bool, depth int, fn func(key, val Value)) {
	if depth > maxLinkDepth {
		return
	}
	if id := node.ptr.id; id != 0 {
		if seen[id] {
			return
		}
		seen[id] = true
	}
	if ns := node.Key("Names"); !ns.IsNull() {
		for i := 0; i+1 < ns.Len(); i += 2 {
			fn(ns.Index(i), ns.Index(i+1))
		}
	}
	kids := node.Key("Kids")
	for i := 0; i < kids.Len(); i++ {
		collectNameTree(kids.Index(i), seen, depth+1, fn)
	}
}
