package pdf

import (
	"fmt"
	"math"
	"testing"
)

func assertImageRef(t *testing.T, got ImageRef, want ImageRef) {
	t.Helper()
	if !closeFloat(got.X, want.X) || !closeFloat(got.Y, want.Y) ||
		!closeFloat(got.W, want.W) || !closeFloat(got.H, want.H) {
		t.Fatalf("image bounds = (%g,%g,%g,%g), want (%g,%g,%g,%g)",
			got.X, got.Y, got.W, got.H, want.X, want.Y, want.W, want.H)
	}
	if got.Filter != want.Filter {
		t.Fatalf("image Filter = %q, want %q", got.Filter, want.Filter)
	}
	if got.DeclaredWidth != want.DeclaredWidth || got.DeclaredHeight != want.DeclaredHeight {
		t.Fatalf("image declared size = %dx%d, want %dx%d",
			got.DeclaredWidth, got.DeclaredHeight, want.DeclaredWidth, want.DeclaredHeight)
	}
}

func closeFloat(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func imageStream(width, height int, filter string) string {
	filterEntry := ""
	if filter != "" {
		filterEntry = " /Filter /" + filter
	}
	return fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d%s /Length 0 >>\nstream\nendstream",
		width, height, filterEntry)
}

func singleImagePagePDF(content string, imageObj string) []byte {
	return buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		imageObj,
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	})
}

func TestImagesBasic(t *testing.T) {
	r := mustOpen(t, singleImagePagePDF("/Img0 Do", imageStream(640, 480, "DCTDecode")))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want 1: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 0, Y: 0, W: 1, H: 1,
		Filter: "DCTDecode", DeclaredWidth: 640, DeclaredHeight: 480,
	})
}

func TestImagesTransformed(t *testing.T) {
	content := "0 20 -30 0 100 200 cm /Img0 Do"
	r := mustOpen(t, singleImagePagePDF(content, imageStream(2, 3, "FlateDecode")))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want 1: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 70, Y: 200, W: 30, H: 20,
		Filter: "FlateDecode", DeclaredWidth: 2, DeclaredHeight: 3,
	})
}

func TestImagesMultiple(t *testing.T) {
	content := "q 10 0 0 20 5 6 cm /Img0 Do Q q 3 0 0 4 30 40 cm /Img0 Do Q"
	r := mustOpen(t, singleImagePagePDF(content, imageStream(1, 1, "DCTDecode")))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("Images returned %d refs, want 2: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 5, Y: 6, W: 10, H: 20,
		Filter: "DCTDecode", DeclaredWidth: 1, DeclaredHeight: 1,
	})
	assertImageRef(t, images[1], ImageRef{
		X: 30, Y: 40, W: 3, H: 4,
		Filter: "DCTDecode", DeclaredWidth: 1, DeclaredHeight: 1,
	})
}

func TestImagesNoImages(t *testing.T) {
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		"<< /Length 0 >>\nstream\nendstream",
	}))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if images != nil {
		t.Fatalf("Images = %+v, want nil", images)
	}
}

func TestImagesFormXObjectPageSpace(t *testing.T) {
	formBody := "/Img0 Do"
	content := "1 0 0 1 100 200 cm /Fm0 Do"
	form := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Form /Matrix [2 0 0 2 10 20] "+
			"/Resources << /XObject << /Img0 6 0 R >> >> /Length %d >>\nstream\n%s\nendstream",
		len(formBody), formBody)
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Fm0 4 0 R >> >> /Contents 5 0 R >>",
		form,
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		imageStream(8, 9, "JPXDecode"),
	}))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want 1: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 110, Y: 220, W: 2, H: 2,
		Filter: "JPXDecode", DeclaredWidth: 8, DeclaredHeight: 9,
	})
}

func TestImagesInlineImage(t *testing.T) {
	content := "10 0 0 20 5 6 cm BI /W 1 /H 1 ID A EI"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want 1: %+v", len(images), images)
	}
	// /W 1 /H 1 are now captured from the inline dict into DeclaredWidth/Height.
	assertImageRef(t, images[0], ImageRef{X: 5, Y: 6, W: 10, H: 20, DeclaredWidth: 1, DeclaredHeight: 1})
}

func TestImagesInlineImagePayloadFalseEINotTerminator(t *testing.T) {
	content := "BI /W 1 /H 1 ID abcEI /Img0 Do EI"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		imageStream(1, 1, "DCTDecode"),
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want only the inline image: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{X: 0, Y: 0, W: 1, H: 1, DeclaredWidth: 1, DeclaredHeight: 1})
}

func TestImagesInlineImageUnterminatedNotCounted(t *testing.T) {
	content := "BI /W 1 /H 1 ID abc"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if images != nil {
		t.Fatalf("Images = %+v, want nil for unterminated inline image", images)
	}
}

func TestImagesRecoverKeepsPartialRefs(t *testing.T) {
	content := "/Img0 Do 1 2 cm"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Img0 4 0 R >> >> /Contents 5 0 R >>",
		imageStream(3, 4, "DCTDecode"),
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}))
	images, err := r.Page(1).Images()
	if err == nil {
		t.Fatalf("Images: want malformed stream error, got nil")
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want partial image ref before panic: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 0, Y: 0, W: 1, H: 1,
		Filter: "DCTDecode", DeclaredWidth: 3, DeclaredHeight: 4,
	})
}

func TestImagesRecoverKeepsPartialRefsInsideForm(t *testing.T) {
	formContent := "/Img0 Do 1 2 cm"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Fm0 4 0 R >> >> /Contents 6 0 R >>",
		formXObjStream(formContent, "<< /XObject << /Img0 5 0 R >> >>"),
		imageStream(3, 4, "DCTDecode"),
		"<< /Length 7 >>\nstream\n/Fm0 Do\nendstream",
	}))
	images, err := r.Page(1).Images()
	if err == nil {
		t.Fatalf("Images: want malformed Form stream error, got nil")
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want Form partial image ref before panic: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 0, Y: 0, W: 1, H: 1,
		Filter: "DCTDecode", DeclaredWidth: 3, DeclaredHeight: 4,
	})
}

func TestImagesFormWithoutResourcesUsesCallSiteResources(t *testing.T) {
	formContent := "/Img0 Do"
	r := mustOpen(t, buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Fm0 4 0 R /Img0 5 0 R >> >> /Contents 6 0 R >>",
		fmt.Sprintf("<< /Type /XObject /Subtype /Form /Length %d >>\nstream\n%s\nendstream", len(formContent), formContent),
		imageStream(6, 7, "DCTDecode"),
		"<< /Length 7 >>\nstream\n/Fm0 Do\nendstream",
	}))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want image resolved from call-site resources: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 0, Y: 0, W: 1, H: 1,
		Filter: "DCTDecode", DeclaredWidth: 6, DeclaredHeight: 7,
	})
}

func TestImagesStrayEINotCounted(t *testing.T) {
	for _, content := range []string{"EI", "ID A EI"} {
		r := mustOpen(t, buildPDFFromObjects([]string{
			"<< /Type /Catalog /Pages 2 0 R >>",
			"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
			"<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>",
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		}))
		images, err := r.Page(1).Images()
		if err != nil {
			t.Fatalf("%q: Images: %v", content, err)
		}
		if images != nil {
			t.Fatalf("%q: Images = %+v, want nil", content, images)
		}
	}
}

func TestImagesAtMaxFormDepth(t *testing.T) {
	const formCount = xobjMaxDepth
	objs := make([]string, 5+formCount)
	objs[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objs[1] = "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
	objs[2] = "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /X0 5 0 R >> >> /Contents 4 0 R >>"
	pageContent := "/X0 Do"
	objs[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pageContent), pageContent)
	imageObj := 5 + formCount
	for k := 0; k < formCount; k++ {
		if k == formCount-1 {
			body := "/Img0 Do"
			resources := fmt.Sprintf("<< /XObject << /Img0 %d 0 R >> >>", imageObj)
			objs[4+k] = formXObjStream(body, resources)
			continue
		}
		nextName := fmt.Sprintf("X%d", k+1)
		body := "/" + nextName + " Do"
		resources := fmt.Sprintf("<< /XObject << /%s %d 0 R >> >>", nextName, 6+k)
		objs[4+k] = formXObjStream(body, resources)
	}
	objs[4+formCount] = imageStream(10, 20, "DCTDecode")

	r := mustOpen(t, buildPDFFromObjects(objs))
	images, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("Images returned %d refs, want image drawn at max form depth: %+v", len(images), images)
	}
	assertImageRef(t, images[0], ImageRef{
		X: 0, Y: 0, W: 1, H: 1,
		Filter: "DCTDecode", DeclaredWidth: 10, DeclaredHeight: 20,
	})
}

// inlineImages opens a one-page PDF whose content is an inline-image stream and
// returns its drawn images. buildTextPDF wraps content in a page with a content
// stream; inline images need no XObject resources.
func inlineImages(t *testing.T, content string) []ImageRef {
	t.Helper()
	r := mustOpen(t, buildTextPDF(content))
	imgs, err := r.Page(1).Images()
	if err != nil {
		t.Fatalf("Images(%q): %v", content, err)
	}
	return imgs
}

// TestImagesInlineImageDeclaredDims captures the inline-image /W and /H pixel
// dimensions (abbreviated keys) into DeclaredWidth/DeclaredHeight.
func TestImagesInlineImageDeclaredDims(t *testing.T) {
	imgs := inlineImages(t, "q 30 0 0 20 0 0 cm BI /W 8 /H 4 /CS /G /BPC 8 ID xxxx EI Q")
	if len(imgs) != 1 {
		t.Fatalf("want 1 inline image, got %d: %+v", len(imgs), imgs)
	}
	if imgs[0].DeclaredWidth != 8 || imgs[0].DeclaredHeight != 4 {
		t.Fatalf("inline dims: got %dx%d, want 8x4", imgs[0].DeclaredWidth, imgs[0].DeclaredHeight)
	}
}

// TestImagesInlineImageLongFormDims accepts the spelled-out /Width /Height keys.
func TestImagesInlineImageLongFormDims(t *testing.T) {
	imgs := inlineImages(t, "BI /Width 5 /Height 7 ID a EI")
	if len(imgs) != 1 || imgs[0].DeclaredWidth != 5 || imgs[0].DeclaredHeight != 7 {
		t.Fatalf("long-form inline dims: %+v", imgs)
	}
}

// TestImagesInlineImageBoolValueKeepsAlignment locks the case where a boolean
// inline-dict value (/IM true) is a bool token, NOT a keyword, so it does not
// drain the operand stack before EI - /W /H are still captured.
func TestImagesInlineImageBoolValueKeepsAlignment(t *testing.T) {
	imgs := inlineImages(t, "BI /W 6 /H 3 /IM true ID a EI")
	if len(imgs) != 1 || imgs[0].DeclaredWidth != 6 || imgs[0].DeclaredHeight != 3 {
		t.Fatalf("inline dims with bool value: %+v, want 6x3", imgs)
	}
}
