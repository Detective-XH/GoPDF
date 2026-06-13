package pdf

import (
	"errors"
	"fmt"
	"math"
)

// ImageRef identifies an inline or XObject image drawn on a page without
// decoding its content. X and Y are the lower-left corner of the image's
// page-space bounding box. W and H are positive page-space extents after the
// current transformation matrix has been applied.
type ImageRef struct {
	X, Y float64
	W, H float64

	// Filter is the primary declared image filter name, such as DCTDecode or
	// FlateDecode. If /Filter is an array, Filter is the first name in the array.
	Filter string
	// DeclaredWidth and DeclaredHeight are the image's pixel dimensions from the
	// image dictionary's /Width and /Height (or the abbreviated /W and /H of an
	// inline image). They are 0 when the dictionary omits them.
	DeclaredWidth  int
	DeclaredHeight int
}

// Images returns metadata for all images drawn on the page.
//
// The method does not decode, decompress, or open image streams. It reports draw
// operations rather than distinct image resources: if one image resource is drawn
// three times, the returned slice contains three entries. Inline images are
// reported with CTM-derived bounds and empty/zero dictionary metadata because the
// interpreter currently skips inline image dictionaries with their byte payloads.
//
// Returns (nil, nil) for null pages, pages without content streams, or pages
// with no drawn images. The error return is reserved for future interpreter
// errors; current content interpretation recovers malformed streams and returns
// the metadata collected before recovery, matching Content().
func (p Page) Images() (images []ImageRef, err error) {
	s := newImageScanState(p, false)
	defer func() {
		if rec := recover(); rec != nil {
			if s != nil {
				images = s.images
			}
			err = errors.New(fmt.Sprint(rec))
		}
	}()
	if s == nil {
		return nil, nil
	}
	Interpret(p.V.Key("Contents"), s.interpret)
	images = s.images
	if len(images) == 0 {
		return nil, nil
	}
	return images, nil
}

// imageScanState is a narrow content interpreter for image draw metadata. It
// intentionally ignores text operators so image counting can still report
// determinable draw operations before a later text-extraction error.
type imageScanState struct {
	resources Value
	ctm       matrix
	gstack    []matrix
	depth     int
	images    []ImageRef
	count     int
	areaSum   float64 // summed page-space bbox area of drawn images (for coverage)
	countOnly bool
	inBI      bool
}

// scanPageImages interprets the page's content for drawn-image metadata. It always
// returns the draw count and the summed page-space bounding-box area; the []ImageRef
// slice is populated only when countOnly is false. The area accumulates in either
// mode, so a coverage caller can stay count-only (O(1) memory) on a content stream
// that draws an image many times.
func scanPageImages(p Page, countOnly bool) (images []ImageRef, count int, areaSum float64) {
	s := newImageScanState(p, countOnly)
	if s == nil {
		return nil, 0, 0
	}
	Interpret(p.V.Key("Contents"), s.interpret)
	return s.images, s.count, s.areaSum
}

func newImageScanState(p Page, countOnly bool) *imageScanState {
	if p.V.IsNull() || p.V.Key("Contents").Kind() == Null {
		return nil
	}
	return &imageScanState{
		resources: p.Resources(),
		ctm:       p.rotateMatrix(),
		countOnly: countOnly,
	}
}

func countDrawnImages(p Page) int {
	_, count, _ := scanPageImages(p, true)
	return count
}

func (s *imageScanState) interpret(stk *Stack, op string) {
	args := popArgs(stk)
	switch op {
	case "cm", "q", "Q":
		s.handleGraphics(op, args)
	case "Do":
		s.handleDo(args)
	case "BI", "EI":
		s.handleInlineImage(op, args)
	}
}

func (s *imageScanState) handleGraphics(op string, args []Value) {
	switch op {
	case "cm":
		if len(args) != 6 {
			panic("bad cm: expected 6 args")
		}
		s.ctm = matrixFrom6Args(args).mul(s.ctm)
	case "q":
		s.gstack = append(s.gstack, s.ctm)
	case "Q":
		if n := len(s.gstack) - 1; n >= 0 {
			s.ctm = s.gstack[n]
			s.gstack = s.gstack[:n]
		}
	}
}

func (s *imageScanState) handleDo(args []Value) {
	if len(args) == 0 {
		return
	}
	s.interpretXObject(args[0].Name())
}

func (s *imageScanState) interpretXObject(name string) {
	xobj := s.resources.Key("XObject").Key(name)
	switch xobj.Key("Subtype").Name() {
	case "Image":
		s.recordImage(imageRefFromXObject(xobj, s.ctm))
	case "Form":
		if s.depth >= xobjMaxDepth {
			return
		}
		resources := xobj.Key("Resources")
		if resources.IsNull() {
			resources = s.resources
		}
		sub := &imageScanState{
			resources: resources,
			ctm:       formMatrix(xobj).mul(s.ctm),
			depth:     s.depth + 1,
			countOnly: s.countOnly,
		}
		defer func() {
			if rec := recover(); rec != nil {
				s.merge(sub)
				panic(rec)
			}
		}()
		Interpret(xobj, sub.interpret)
		s.merge(sub)
	}
}

func (s *imageScanState) merge(sub *imageScanState) {
	s.count += sub.count
	s.areaSum += sub.areaSum
	if !s.countOnly {
		s.images = append(s.images, sub.images...)
	}
}

func (s *imageScanState) handleInlineImage(op string, args []Value) {
	switch op {
	case "BI":
		s.inBI = true
	case "EI":
		// The lexer dispatches EI after byte-skipping an ID payload. A stray
		// literal EI arrives through the same callback, so only a BI-opened EI
		// is treated as image draw evidence. args holds the inline image
		// dictionary (the operands pushed between BI and ID).
		if s.inBI {
			ref := imageRefFromCTM(s.ctm)
			applyInlineDims(&ref, args)
			s.recordImage(ref)
			s.inBI = false
		}
	}
}

// applyInlineDims reads /W (or /Width) and /H (or /Height) from a flattened inline
// image dictionary (alternating name/value operands) into ref's declared pixel
// dimensions. It is best-effort: non-name keys, a trailing key with no value, and
// unknown keys are skipped, so a malformed dict leaves the fields at zero rather
// than panicking. Other inline keys (/CS /F /BPC /D /IM /I) are ignored.
func applyInlineDims(ref *ImageRef, args []Value) {
	for i := 0; i+1 < len(args); i += 2 {
		key := args[i]
		if key.Kind() != Name {
			continue
		}
		switch key.Name() {
		case "W", "Width":
			ref.DeclaredWidth = int(args[i+1].Int64())
		case "H", "Height":
			ref.DeclaredHeight = int(args[i+1].Int64())
		}
	}
}

// imageCoverage returns the fraction of the page (MediaBox area) covered by
// areaSum, the summed page-space bounding-box area of the page's drawn images,
// clamped to [0,1]. It distinguishes a full-bleed scan (near 1.0) from an
// incidental thumbnail (well under 1.0). Coarse by design: overlapping or partly
// off-page images are summed naively, so areaSum may exceed the page before the
// clamp - a density signal, not exact coverage. Returns 0 when box has no positive
// area. areaSum is non-negative (each term is a max-minus-min extent), so no lower
// clamp is needed.
func imageCoverage(areaSum float64, box [4]float64) float64 {
	pageArea := (box[2] - box[0]) * (box[3] - box[1])
	if pageArea <= 0 {
		return 0
	}
	if cov := areaSum / pageArea; cov < 1 {
		return cov
	}
	return 1
}

func (s *imageScanState) recordImage(ref ImageRef) {
	s.count++
	s.areaSum += ref.W * ref.H
	if !s.countOnly {
		s.images = append(s.images, ref)
	}
}

func imageRefFromXObject(xobj Value, ctm matrix) ImageRef {
	ref := imageRefFromCTM(ctm)
	ref.Filter = primaryImageFilter(xobj.Key("Filter"))
	ref.DeclaredWidth = int(xobj.Key("Width").Int64())
	ref.DeclaredHeight = int(xobj.Key("Height").Int64())
	return ref
}

func imageRefFromCTM(ctm matrix) ImageRef {
	points := [...]Point{
		transformPoint(ctm, 0, 0),
		transformPoint(ctm, 1, 0),
		transformPoint(ctm, 0, 1),
		transformPoint(ctm, 1, 1),
	}
	minX, maxX := points[0].X, points[0].X
	minY, maxY := points[0].Y, points[0].Y
	for _, p := range points[1:] {
		minX = math.Min(minX, p.X)
		maxX = math.Max(maxX, p.X)
		minY = math.Min(minY, p.Y)
		maxY = math.Max(maxY, p.Y)
	}
	return ImageRef{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}
}

func transformPoint(m matrix, x, y float64) Point {
	return Point{
		X: x*m[0][0] + y*m[1][0] + m[2][0],
		Y: x*m[0][1] + y*m[1][1] + m[2][1],
	}
}

func primaryImageFilter(v Value) string {
	switch v.Kind() {
	case Name:
		return v.Name()
	case Array:
		for i := 0; i < v.Len(); i++ {
			if f := v.Index(i); f.Kind() == Name {
				return f.Name()
			}
		}
	}
	return ""
}
