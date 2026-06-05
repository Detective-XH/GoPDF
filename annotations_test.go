package pdf

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// buildURIAnnotationPDF returns a one-page PDF whose first page carries one
// /Link annotation with a /URI action pointing to uri.
// Annotation rect is [50 100 200 120] in PDF user space.
func buildURIAnnotationPDF(uri string) []byte {
	uriEsc := strings.ReplaceAll(uri, ")", "\\)")
	annot := fmt.Sprintf(
		"<< /Type /Annot /Subtype /Link /Rect [50 100 200 120] /Border [0 0 0] /A << /S /URI /URI (%s) >> >>",
		uriEsc,
	)
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [4 0 R] >>",
		annot,
	})
}

// buildGoToAnnotationPDF returns a two-page PDF whose first page carries one
// /Link annotation with a /GoTo action pointing to page 2.
func buildGoToAnnotationPDF() []byte {
	return buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages (two kids: obj 3 = page 1, obj 4 = page 2)
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		// 3: Page 1, with annotation
		"<< /Type /Page /Parent 2 0 R /Annots [5 0 R] >>",
		// 4: Page 2 (target)
		"<< /Type /Page /Parent 2 0 R >>",
		// 5: Link annotation — GoTo page 2 via array dest [4 0 R /XYZ 0 0 0]
		"<< /Type /Annot /Subtype /Link /Rect [10 200 150 220] /A << /S /GoTo /D [4 0 R /XYZ 0 0 0] >> >>",
	})
}

// buildGoToNamedAnnotationPDF returns a two-page PDF whose first page carries a
// /Link annotation with a /GoTo action referencing a named destination "chap2"
// that resolves to page 2 via /Names/Dests name tree.
func buildGoToNamedAnnotationPDF() []byte {
	return buildPDFFromObjects([]string{
		// 1: Catalog — references Names (obj 3) and Pages (obj 2)
		"<< /Type /Catalog /Pages 2 0 R /Names 3 0 R >>",
		// 2: Pages — two kids: obj 4 = page 1, obj 5 = page 2
		"<< /Type /Pages /Kids [4 0 R 5 0 R] /Count 2 >>",
		// 3: Names dict — Dests points to leaf name tree node (obj 6)
		"<< /Dests 6 0 R >>",
		// 4: Page 1, with link annotation (obj 7)
		"<< /Type /Page /Parent 2 0 R /Annots [7 0 R] >>",
		// 5: Page 2 (target of named dest)
		"<< /Type /Page /Parent 2 0 R >>",
		// 6: Dests name tree leaf: chap2 → dest array obj 8
		"<< /Names [(chap2) 8 0 R] >>",
		// 7: Link annotation with GoTo action using named dest string
		"<< /Type /Annot /Subtype /Link /Rect [0 0 1 1] /A << /S /GoTo /D (chap2) >> >>",
		// 8: Destination array [page2ref /XYZ 0 0 0]
		"[5 0 R /XYZ 0 0 0]",
	})
}

// buildNamedDestPDF returns a one-page PDF with a /Names/Dests name tree
// containing one entry: name → page 1.
func buildNamedDestPDF() []byte {
	return buildPDFFromObjects([]string{
		// 1: Catalog, references Names dict
		"<< /Type /Catalog /Pages 2 0 R /Names 3 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [4 0 R] /Count 1 >>",
		// 3: Names dict with /Dests pointing to leaf name tree node
		"<< /Dests 5 0 R >>",
		// 4: Page 1 (the destination target)
		"<< /Type /Page /Parent 2 0 R >>",
		// 5: Dests name tree leaf: /Names [(section1) 6 0 R]
		"<< /Names [(section1) 6 0 R] >>",
		// 6: Destination array [page1ref /XYZ 0 0 0]
		"[4 0 R /XYZ 0 0 0]",
	})
}

func TestAnnotationsURI(t *testing.T) {
	const wantURI = "https://example.com/doc"
	data := buildURIAnnotationPDF(wantURI)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	anns, err := r.Page(1).Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	if len(anns) != 1 {
		t.Fatalf("len(Annotations) = %d, want 1", len(anns))
	}
	ann := anns[0]
	if ann.Type != AnnotLink {
		t.Errorf("Type = %v, want AnnotLink", ann.Type)
	}
	if ann.URI != wantURI {
		t.Errorf("URI = %q, want %q", ann.URI, wantURI)
	}
	// Rect must be non-zero and within plausible page bounds.
	if ann.Rect.Min.X == 0 && ann.Rect.Min.Y == 0 && ann.Rect.Max.X == 0 && ann.Rect.Max.Y == 0 {
		t.Error("Rect is all-zero, want non-zero bounding box")
	}
	// Fixture rect [50 100 200 120].
	if ann.Rect.Min.X != 50 || ann.Rect.Min.Y != 100 || ann.Rect.Max.X != 200 || ann.Rect.Max.Y != 120 {
		t.Errorf("Rect = %+v, want {Min:{50 100} Max:{200 120}}", ann.Rect)
	}
}

func TestAnnotationsGoTo(t *testing.T) {
	data := buildGoToAnnotationPDF()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	anns, err := r.Page(1).Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	if len(anns) != 1 {
		t.Fatalf("len(Annotations) = %d, want 1", len(anns))
	}
	ann := anns[0]
	if ann.Type != AnnotLink {
		t.Errorf("Type = %v, want AnnotLink", ann.Type)
	}
	if ann.Page != 2 {
		t.Errorf("Page = %d, want 2", ann.Page)
	}
	if ann.URI != "" {
		t.Errorf("URI = %q, want empty for GoTo annotation", ann.URI)
	}
}

func TestAnnotationsEmpty(t *testing.T) {
	// Page with no /Annots entry at all.
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	anns, err := r.Page(1).Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	if anns != nil {
		t.Errorf("Annotations() = %v, want nil for page with no /Annots", anns)
	}
}

func TestAnnotationsEmptyArray(t *testing.T) {
	// Page with /Annots [] (empty array, not absent).
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	anns, err := r.Page(1).Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	if anns != nil {
		t.Errorf("Annotations() = %v, want nil for empty /Annots array", anns)
	}
}

func TestDestNotFound(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	page, err := r.Dest("nonexistent")
	if err != ErrDestNotFound {
		t.Errorf("Dest error = %v, want ErrDestNotFound", err)
	}
	if page != 0 {
		t.Errorf("Dest page = %d, want 0", page)
	}
}

func TestDestFound(t *testing.T) {
	data := buildNamedDestPDF()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	page, err := r.Dest("section1")
	if err != nil {
		t.Fatalf("Dest: %v", err)
	}
	if page != 1 {
		t.Errorf("Dest(\"section1\") = %d, want 1", page)
	}
}

func TestAnnotationsGoToNamed(t *testing.T) {
	data := buildGoToNamedAnnotationPDF()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	anns, err := r.Page(1).Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	if len(anns) != 1 {
		t.Fatalf("len(Annotations) = %d, want 1", len(anns))
	}
	ann := anns[0]
	if ann.Type != AnnotLink {
		t.Errorf("Type = %v, want AnnotLink", ann.Type)
	}
	if ann.Page != 2 {
		t.Errorf("Page = %d, want 2 (named dest chap2 → page 2)", ann.Page)
	}
	if ann.URI != "" {
		t.Errorf("URI = %q, want empty for GoTo annotation", ann.URI)
	}
}

func TestAnnotationsText(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [4 0 R] >>",
		"<< /Type /Annot /Subtype /Text /Rect [10 10 30 30] /Contents (A note) >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	anns, err := r.Page(1).Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	if len(anns) != 1 {
		t.Fatalf("len = %d, want 1", len(anns))
	}
	if anns[0].Type != AnnotText {
		t.Errorf("Type = %v, want AnnotText", anns[0].Type)
	}
	if anns[0].Content != "A note" {
		t.Errorf("Content = %q, want \"A note\"", anns[0].Content)
	}
}

// TestAnnotationsRectNormalized verifies that rectFromValue and the Rect
// assignment produce sane values (both axes non-negative width/height).
func TestAnnotationsRectNormalized(t *testing.T) {
	data := buildURIAnnotationPDF("https://example.com")
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	anns, err := r.Page(1).Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	if len(anns) == 0 {
		t.Fatal("no annotations returned")
	}
	a := anns[0]
	w := a.Rect.Max.X - a.Rect.Min.X
	h := a.Rect.Max.Y - a.Rect.Min.Y
	if math.IsNaN(w) || w < 0 {
		t.Errorf("rect width = %f, want >= 0", w)
	}
	if math.IsNaN(h) || h < 0 {
		t.Errorf("rect height = %f, want >= 0", h)
	}
}

// buildCyclicNameTreePDF returns a PDF whose /Names/Dests name tree node (obj 5)
// has a /Kids array that points back to itself, forming a cycle. It exercises
// walkNameTree's cycle guard: resolution must terminate, not overflow the stack.
func buildCyclicNameTreePDF() []byte {
	return buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R /Names 3 0 R >>",
		// 2: Pages
		"<< /Type /Pages /Kids [4 0 R] /Count 1 >>",
		// 3: Names dict — Dests points to the root name tree node (obj 5)
		"<< /Dests 5 0 R >>",
		// 4: Page 1
		"<< /Type /Page /Parent 2 0 R >>",
		// 5: Name tree node whose /Kids points back to itself (cycle)
		"<< /Kids [5 0 R] >>",
	})
}

func TestDestCyclicNameTree(t *testing.T) {
	data := buildCyclicNameTreePDF()
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	// Must terminate (not stack-overflow) and report the name as not found.
	page, err := r.Dest("anything")
	if err != ErrDestNotFound {
		t.Errorf("Dest error = %v, want ErrDestNotFound", err)
	}
	if page != 0 {
		t.Errorf("Dest page = %d, want 0", page)
	}
}
