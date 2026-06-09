// attachments_test.go — tests for Reader.Attachments() embedded-file listing.
//
// External fixtures in testdata/attachments/ were generated with qpdf 12.3.2:
//
//	printf "hello embedded file" > /tmp/hello.txt   # 19 bytes -> /Params /Size 19
//	printf "second embedded file" > /tmp/second.txt
//	qpdf testdata/corpus/plaintext/hello-ascii.pdf testdata/attachments/one-attachment.pdf \
//	  --add-attachment /tmp/hello.txt --mimetype=text/plain --filename=hello.txt --
//	qpdf testdata/attachments/one-attachment.pdf testdata/attachments/two-attachments.pdf \
//	  --add-attachment /tmp/second.txt --mimetype=text/plain --filename=second.txt --
//
// They are committed as byte-level regression anchors — never regenerate or
// modify them; add new fixtures alongside. qpdf always writes a flat /Names
// leaf, so the /Kids and cycle paths are covered by synthetic
// buildPDFFromObjects fixtures instead.
package pdf

import (
	"io"
	"os"
	"strconv"
	"testing"
)

// openAttachmentFixture opens a fixture file at the given path (relative to the
// repo root, e.g. "testdata/attachments/one-attachment.pdf") via NewReader.
func openAttachmentFixture(t *testing.T, path string) *Reader {
	t.Helper()
	//nolint:gosec // G304: fixture is a fixed testdata path, not user input
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r
}

// readAttachment reads and closes one attachment's data.
func readAttachment(t *testing.T, a Attachment) string {
	t.Helper()
	rc, err := a.Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	return string(b)
}

// TestAttachmentsBasic verifies Name, MimeType, and Size on the single-file
// qpdf fixture.
func TestAttachmentsBasic(t *testing.T) {
	r := openAttachmentFixture(t, "testdata/attachments/one-attachment.pdf")
	atts, err := r.Attachments()
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1", len(atts))
	}
	a := atts[0]
	if a.Name != "hello.txt" {
		t.Errorf("Name = %q, want hello.txt", a.Name)
	}
	if a.MimeType != "text/plain" {
		t.Errorf("MimeType = %q, want text/plain", a.MimeType)
	}
	if a.Size != 19 {
		t.Errorf("Size = %d, want 19", a.Size)
	}
}

// TestAttachmentsData verifies the decoded bytes round-trip (the qpdf stream
// is FlateDecode-compressed; Value.Reader() must decode it).
func TestAttachmentsData(t *testing.T) {
	r := openAttachmentFixture(t, "testdata/attachments/one-attachment.pdf")
	atts, err := r.Attachments()
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1", len(atts))
	}
	if got := readAttachment(t, atts[0]); got != "hello embedded file" {
		t.Errorf("Data = %q, want %q", got, "hello embedded file")
	}
	// A second Data() call must return a fresh, fully readable stream.
	if got := readAttachment(t, atts[0]); got != "hello embedded file" {
		t.Errorf("second Data read = %q, want %q", got, "hello embedded file")
	}
}

// TestAttachmentsMulti verifies multiple /Names entries in tree order.
func TestAttachmentsMulti(t *testing.T) {
	r := openAttachmentFixture(t, "testdata/attachments/two-attachments.pdf")
	atts, err := r.Attachments()
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("len(Attachments) = %d, want 2", len(atts))
	}
	// qpdf writes name-tree keys in lexical order.
	if atts[0].Name != "hello.txt" || atts[1].Name != "second.txt" {
		t.Errorf("names = %q, %q; want hello.txt, second.txt", atts[0].Name, atts[1].Name)
	}
	if got := readAttachment(t, atts[1]); got != "second embedded file" {
		t.Errorf("second attachment data = %q, want %q", got, "second embedded file")
	}
}

// TestAttachmentsEmpty verifies (nil, nil) for a PDF without embedded files.
func TestAttachmentsEmpty(t *testing.T) {
	r := openAttachmentFixture(t, "testdata/corpus/plaintext/hello-ascii.pdf")
	atts, err := r.Attachments()
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if atts != nil {
		t.Errorf("Attachments() = %v, want nil for a document without embedded files", atts)
	}
}

// buildKidsTreeAttachmentsPDF returns a synthetic PDF whose /EmbeddedFiles
// name tree has a /Kids level above two leaves — the shape qpdf never emits —
// with uncompressed embedded streams.
func buildKidsTreeAttachmentsPDF() []byte {
	const (
		dataA = "alpha bytes"  // 11 bytes
		dataB = "beta payload" // 12 bytes
	)
	streamObj := func(data string) string {
		return "<< /Type /EmbeddedFile /Subtype /text#2Fplain /Length " +
			strconv.Itoa(len(data)) + " /Params << /Size " + strconv.Itoa(len(data)) + " >> >>\nstream\n" + data + "\nendstream"
	}
	return buildPDFFromObjects([]string{
		// 1: Catalog with the name-tree root at obj 4
		"<< /Type /Catalog /Pages 2 0 R /Names << /EmbeddedFiles 4 0 R >> >>",
		// 2-3: minimal page tree
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R >>",
		// 4: name-tree ROOT — /Kids only (internal node)
		"<< /Kids [5 0 R 6 0 R] >>",
		// 5-6: leaves
		"<< /Limits [(a.txt) (a.txt)] /Names [(a.txt) 7 0 R] >>",
		"<< /Limits [(b.txt) (b.txt)] /Names [(b.txt) 8 0 R] >>",
		// 7-8: filespecs
		"<< /Type /Filespec /F (a.txt) /UF (a.txt) /EF << /F 9 0 R >> >>",
		"<< /Type /Filespec /F (b.txt) /EF << /F 10 0 R >> >>",
		// 9-10: uncompressed embedded streams
		streamObj(dataA),
		streamObj(dataB),
	})
}

// TestAttachmentsKidsTree exercises the /Kids recursion path.
func TestAttachmentsKidsTree(t *testing.T) {
	r, err := OpenBytes(buildKidsTreeAttachmentsPDF())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	atts, err := r.Attachments()
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("len(Attachments) = %d, want 2", len(atts))
	}
	if atts[0].Name != "a.txt" || atts[1].Name != "b.txt" {
		t.Errorf("names = %q, %q; want a.txt, b.txt", atts[0].Name, atts[1].Name)
	}
	if atts[0].MimeType != "text/plain" {
		t.Errorf("MimeType = %q, want text/plain (lexer must decode #2F)", atts[0].MimeType)
	}
	if atts[0].Size != 11 || atts[1].Size != 12 {
		t.Errorf("sizes = %d, %d; want 11, 12", atts[0].Size, atts[1].Size)
	}
	if got := readAttachment(t, atts[0]); got != "alpha bytes" {
		t.Errorf("data = %q, want %q", got, "alpha bytes")
	}
}

// TestAttachmentsCycle verifies the visited-set guard: a /Kids edge pointing
// back to the tree root must terminate, returning the entries found.
func TestAttachmentsCycle(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /Names << /EmbeddedFiles 4 0 R >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R >>",
		// 4: root → kid 5
		"<< /Kids [5 0 R] >>",
		// 5: leaf with one entry AND a /Kids edge back to the root (cycle)
		"<< /Names [(x.txt) 6 0 R] /Kids [4 0 R] >>",
		// 6: filespec
		"<< /Type /Filespec /F (x.txt) /EF << /F 7 0 R >> >>",
		// 7: embedded stream
		"<< /Type /EmbeddedFile /Length 2 >>\nstream\nhi\nendstream",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	atts, err := r.Attachments()
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1 (cycle must terminate, not hang or dup)", len(atts))
	}
	if atts[0].Name != "x.txt" {
		t.Errorf("Name = %q, want x.txt", atts[0].Name)
	}
	if atts[0].Size != 0 {
		t.Errorf("Size = %d, want 0 (/Params absent)", atts[0].Size)
	}
}

// TestAttachmentsExternalFilespecSkipped verifies that a filespec without /EF
// (external file reference) is skipped, not surfaced as an empty attachment.
func TestAttachmentsExternalFilespecSkipped(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /Names << /EmbeddedFiles 4 0 R >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R >>",
		"<< /Names [(ext.txt) 5 0 R] >>",
		// 5: external-reference filespec — /F only, no /EF
		"<< /Type /Filespec /F (ext.txt) >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	atts, err := r.Attachments()
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if atts != nil {
		t.Errorf("Attachments() = %v, want nil (external filespec must be skipped)", atts)
	}
}
