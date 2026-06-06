// Tests for xmp.go: raw XMP metadata extraction.

package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// buildXMPPDF builds a minimal PDF whose catalog /Metadata entry is a stream
// containing xmpContent. Length is set to len(xmpContent) per PDF spec §7.3.8.1
// (the EOL before endstream is not counted in Length).
func buildXMPPDF(xmpContent string) []byte {
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, 4) // 1-indexed

	// Object 1: catalog
	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /Metadata 3 0 R >>\nendobj\n")

	// Object 2: empty pages tree
	offsets[2] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n")

	// Object 3: XMP metadata stream
	// stream\n + xmpContent + \nendstream; Length = len(xmpContent)
	offsets[3] = b.Len()
	fmt.Fprintf(&b, "3 0 obj\n<< /Type /Metadata /Subtype /XML /Length %d >>\nstream\n%s\nendstream\nendobj\n",
		len(xmpContent), xmpContent)

	// Cross-reference table
	xrefOff := b.Len()
	b.WriteString("xref\n0 4\n0000000000 65535 f \n")
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 4 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	return []byte(b.String())
}

func TestXMPBasic(t *testing.T) {
	const xmpContent = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"></rdf:RDF><?xpacket end="w"?>`
	data := buildXMPPDF(xmpContent)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.XMP()
	if err != nil {
		t.Fatalf("XMP() error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("XMP() returned empty bytes, want non-empty")
	}
	s := string(got)
	if !strings.Contains(s, "<?xpacket") && !strings.Contains(s, "<rdf:RDF") {
		t.Errorf("XMP() = %q, want <?xpacket or <rdf:RDF marker", s)
	}
}

func TestXMPAbsent(t *testing.T) {
	data := buildMinimalPDF("") // reuse existing helper — no /Metadata
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, err := r.XMP()
	if err != nil {
		t.Fatalf("XMP() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("XMP() = %v, want nil for PDF without /Metadata", got)
	}
}

// TestXMPReadAllLimited pins the bounded-read guard with a small limit (the
// production cap, maxDecompressedSize, is too large to materialize in a unit
// test): at the limit the full content is returned; one byte past it errors
// rather than silently truncating.
func TestXMPReadAllLimited(t *testing.T) {
	t.Run("at_limit", func(t *testing.T) {
		data, err := readAllLimited(strings.NewReader("12345678"), 8)
		if err != nil {
			t.Fatalf("readAllLimited at limit: %v", err)
		}
		if string(data) != "12345678" {
			t.Errorf("readAllLimited = %q, want full content", data)
		}
	})
	t.Run("over_limit", func(t *testing.T) {
		_, err := readAllLimited(strings.NewReader("123456789"), 8)
		if err == nil {
			t.Fatal("readAllLimited over limit: want error, got nil")
		}
		if !strings.Contains(err.Error(), "exceeds") {
			t.Errorf("readAllLimited error = %v, want size-bound message", err)
		}
	})
}

// TestXMPEncryptedCleartextMetadata exercises the public API against the two
// REAL encrypted cleartext-metadata fixtures (added by the per-class crypt
// filter work): the catalog /Metadata stream of an /EncryptMetadata-false
// document must come back as verbatim cleartext XMP through XMP(). Same
// prefix assertions as assertCleartextXMP (encrypt_test.go).
func TestXMPEncryptedCleartextMetadata(t *testing.T) {
	for _, fixture := range []string{
		"aes128-r4-cleartext-meta.pdf",
		"aes256-r6-cleartext-meta.pdf",
	} {
		t.Run(fixture, func(t *testing.T) {
			r, err := openEncryptedFixture(t, fixture, "user-secret")
			if err != nil {
				t.Fatalf("open %s: %v", fixture, err)
			}
			got, err := r.XMP()
			if err != nil {
				t.Fatalf("XMP() error: %v", err)
			}
			s := string(got)
			if !strings.HasPrefix(s, "<?xpacket") && !strings.Contains(s, "<x:xmpmeta") {
				t.Errorf("XMP() = %.40q..., want cleartext XMP packet", s)
			}
		})
	}
}
