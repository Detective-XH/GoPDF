// annotations_links_test.go — tests for Reader.Links() document-level link
// aggregation. Reuses the fixture builders in annotations_test.go.
package pdf

import (
	"reflect"
	"testing"
)

// buildMultiPageLinksPDF returns a three-page PDF:
//
//	page 1 — one /Link with a /URI action plus one /Text annotation (excluded),
//	page 2 — no /Annots entry,
//	page 3 — one /Link with a /GoTo array destination back to page 1.
func buildMultiPageLinksPDF() []byte {
	return buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Pages — three kids: objs 3/4/5 = pages 1/2/3
		"<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R] /Count 3 >>",
		// 3: Page 1 — URI link (obj 6) + text note (obj 7)
		"<< /Type /Page /Parent 2 0 R /Annots [6 0 R 7 0 R] >>",
		// 4: Page 2 — no annotations
		"<< /Type /Page /Parent 2 0 R >>",
		// 5: Page 3 — GoTo link (obj 8)
		"<< /Type /Page /Parent 2 0 R /Annots [8 0 R] >>",
		// 6: URI link annotation
		"<< /Type /Annot /Subtype /Link /Rect [10 700 60 712] /A << /S /URI /URI (https://example.com/a) >> >>",
		// 7: Text annotation — must not appear in Links()
		"<< /Type /Annot /Subtype /Text /Rect [0 0 10 10] /Contents (note) >>",
		// 8: GoTo link back to page 1 (obj 3)
		"<< /Type /Annot /Subtype /Link /Rect [20 30 80 42] /A << /S /GoTo /D [3 0 R /XYZ 0 0 0] >> >>",
	})
}

// TestLinksURI verifies that a URI link surfaces FromPage, Rect, and URI.
func TestLinksURI(t *testing.T) {
	const wantURI = "https://example.com/doc"
	r, err := OpenBytes(buildURIAnnotationPDF(wantURI))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len(Links) = %d, want 1", len(links))
	}
	l := links[0]
	if l.FromPage != 1 {
		t.Errorf("FromPage = %d, want 1", l.FromPage)
	}
	if l.URI != wantURI {
		t.Errorf("URI = %q, want %q", l.URI, wantURI)
	}
	if l.ToPage != 0 {
		t.Errorf("ToPage = %d, want 0 for a URI link", l.ToPage)
	}
	// Fixture rect [50 100 200 120].
	if l.Rect.Min.X != 50 || l.Rect.Min.Y != 100 || l.Rect.Max.X != 200 || l.Rect.Max.Y != 120 {
		t.Errorf("Rect = %+v, want {Min:{50 100} Max:{200 120}}", l.Rect)
	}
}

// TestLinksGoTo verifies ToPage resolution for an array-destination GoTo link.
func TestLinksGoTo(t *testing.T) {
	r, err := OpenBytes(buildGoToAnnotationPDF())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len(Links) = %d, want 1", len(links))
	}
	l := links[0]
	if l.FromPage != 1 {
		t.Errorf("FromPage = %d, want 1", l.FromPage)
	}
	if l.ToPage != 2 {
		t.Errorf("ToPage = %d, want 2", l.ToPage)
	}
	if l.URI != "" {
		t.Errorf("URI = %q, want empty for a GoTo link", l.URI)
	}
}

// TestLinksGoToNamed verifies ToPage resolution through the /Names/Dests
// name tree (exercises the shared dests context).
func TestLinksGoToNamed(t *testing.T) {
	r, err := OpenBytes(buildGoToNamedAnnotationPDF())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len(Links) = %d, want 1", len(links))
	}
	if links[0].ToPage != 2 {
		t.Errorf("ToPage = %d, want 2 (named dest chap2)", links[0].ToPage)
	}
}

// TestLinksNone verifies (nil, nil) for a document with no annotations at all
// (buildNamedDestPDF has a name tree but no /Annots anywhere).
func TestLinksNone(t *testing.T) {
	r, err := OpenBytes(buildNamedDestPDF())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if links != nil {
		t.Errorf("Links() = %v, want nil for a document without links", links)
	}
}

// TestLinksNonLinkExcluded verifies (nil, nil) — not an empty non-nil slice —
// when the only annotation is a /Text note.
func TestLinksNonLinkExcluded(t *testing.T) {
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
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if links != nil {
		t.Errorf("Links() = %v, want nil when no /Link annotations exist", links)
	}
}

// TestLinksMultiPage verifies document order (ascending FromPage), the
// per-page subtype filter, and that annotation-free pages are skipped.
func TestLinksMultiPage(t *testing.T) {
	r, err := OpenBytes(buildMultiPageLinksPDF())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("len(Links) = %d, want 2 (text annotation must be excluded)", len(links))
	}
	if links[0].FromPage != 1 || links[0].URI != "https://example.com/a" {
		t.Errorf("links[0] = %+v, want FromPage=1 URI=https://example.com/a", links[0])
	}
	if links[1].FromPage != 3 || links[1].ToPage != 1 {
		t.Errorf("links[1] = %+v, want FromPage=3 ToPage=1", links[1])
	}
	if links[1].URI != "" {
		t.Errorf("links[1].URI = %q, want empty for a GoTo link", links[1].URI)
	}
}

// TestLinksAfterNullGap verifies the Pages()-inherited skip semantics
// (gate-7 round 1, reworked in round 2): a real page after a SHORT run of
// null page slots keeps its links, with FromPage reporting the true slot
// number — the scan skips missing slots rather than stopping at the first
// one.
func TestLinksAfterNullGap(t *testing.T) {
	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Root pages node — claims 4 pages: a childless subtree
		//    swallowing slots 1-3, then one real page at slot 4.
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 4 >>",
		// 3: Empty subtree with inflated /Count — Page(1..3) resolve null.
		"<< /Type /Pages /Parent 2 0 R /Kids [] /Count 3 >>",
		// 4: The real page, reachable as Page(4), carrying a URI link.
		"<< /Type /Page /Parent 2 0 R /Annots [5 0 R] >>",
		// 5: URI link annotation
		"<< /Type /Annot /Subtype /Link /Rect [5 6 7 8] /A << /S /URI /URI (https://example.com/tail) >> >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len(Links) = %d, want 1 (link after a short null gap must not be dropped)", len(links))
	}
	if links[0].FromPage != 4 {
		t.Errorf("FromPage = %d, want 4", links[0].FromPage)
	}
	if links[0].URI != "https://example.com/tail" {
		t.Errorf("URI = %q, want https://example.com/tail", links[0].URI)
	}
}

// TestLinksBoundedNullScan locks the Pages()-inherited bail semantics
// (gate-7 round 2): a tiny PDF advertising a huge /Count made of null slots
// must return promptly instead of probing every slot up to the maxPageCount
// clamp (~1M Page() walks). Like Pages(), GetPlainText, and the page-map
// build, the scan ends after a long run of consecutive nulls — so the link
// parked after the run stays unreported, matching what a Pages()-based
// caller would see. An unbounded scan would find the link (and burn ~1M
// tree walks doing it), failing this test.
func TestLinksBoundedNullScan(t *testing.T) {
	data := buildPDFFromObjects([]string{
		// 1: Catalog
		"<< /Type /Catalog /Pages 2 0 R >>",
		// 2: Root pages node — claims 1048576 pages; slots 1-1048575 are a
		//    childless subtree, slot 1048576 is real but beyond the null-run
		//    bound shared with Pages().
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 1048576 >>",
		// 3: Empty subtree with inflated /Count.
		"<< /Type /Pages /Parent 2 0 R /Kids [] /Count 1048575 >>",
		// 4: Real page after the giant null run.
		"<< /Type /Page /Parent 2 0 R /Annots [5 0 R] >>",
		// 5: URI link annotation
		"<< /Type /Annot /Subtype /Link /Rect [5 6 7 8] /A << /S /URI /URI (https://example.com/unreachable) >> >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if links != nil {
		t.Errorf("Links() = %v, want nil (scan must end after a long null run, matching Pages())", links)
	}
}

// TestLinksEquivalence locks the load-bearing contract directly: Links() is
// exactly Pages() iteration plus per-page Annotations() filtered to
// AnnotLink, in the same order and coordinate system.
func TestLinksEquivalence(t *testing.T) {
	fixtures := map[string][]byte{
		"uri":       buildURIAnnotationPDF("https://example.com/eq"),
		"goToNamed": buildGoToNamedAnnotationPDF(),
		"multiPage": buildMultiPageLinksPDF(),
	}
	for name, data := range fixtures {
		t.Run(name, func(t *testing.T) {
			r, err := OpenBytes(data)
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			var want []LinkRef
			for i, p := range r.Pages() {
				anns, aerr := p.Annotations()
				if aerr != nil {
					t.Fatalf("Annotations(%d): %v", i, aerr)
				}
				for _, ann := range anns {
					if ann.Type != AnnotLink {
						continue
					}
					want = append(want, LinkRef{FromPage: i, Rect: ann.Rect, URI: ann.URI, ToPage: ann.Page})
				}
			}
			got, err := r.Links()
			if err != nil {
				t.Fatalf("Links: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Links() = %+v, want %+v (Pages()+Annotations() filtering)", got, want)
			}
		})
	}
}

// TestLinksUnsupportedActionKept verifies that a /Link whose action kind is
// not URI/GoTo (here GoToR, a remote go-to) is still reported, with both
// URI and ToPage zero-valued.
func TestLinksUnsupportedActionKept(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [4 0 R] >>",
		"<< /Type /Annot /Subtype /Link /Rect [1 2 3 4] /A << /S /GoToR /F (other.pdf) /D [0 /Fit] >> >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	links, err := r.Links()
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len(Links) = %d, want 1 (unsupported-action link must be kept)", len(links))
	}
	l := links[0]
	if l.URI != "" || l.ToPage != 0 {
		t.Errorf("links[0] = %+v, want zero-valued URI and ToPage for GoToR", l)
	}
	if l.FromPage != 1 {
		t.Errorf("FromPage = %d, want 1", l.FromPage)
	}
	if l.Rect.Min.X != 1 || l.Rect.Min.Y != 2 || l.Rect.Max.X != 3 || l.Rect.Max.Y != 4 {
		t.Errorf("Rect = %+v, want {Min:{1 2} Max:{3 4}}", l.Rect)
	}
}
