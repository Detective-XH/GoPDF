// debugjson.go — structured JSON debug export over the stable v0.7 extraction primitives.
//
// Two Experimental methods: Page.DebugJSON and Reader.DebugJSON. Both return compact JSON
// shaped like PyMuPDF's get_text("dict") with an explicit coord_origin tag per page.

package pdf

import "encoding/json"

const (
	coordOriginTopLeft    = "TOPLEFT"
	coordOriginBottomLeft = "BOTTOMLEFT"
)

// ---- unexported DTO layer (json tags live ONLY here) ----

type jsonPage struct {
	Width       float64       `json:"width"`
	Height      float64       `json:"height"`
	CoordOrigin string        `json:"coord_origin"`
	Blocks      []jsonBlock   `json:"blocks"`
	Warnings    []jsonWarning `json:"warnings,omitempty"` // page-scoped (incl. image_only_page)
}

type jsonBlock struct {
	Type  int        `json:"type"` // 0 = text block (GoPDF emits text blocks only)
	Bbox  [4]float64 `json:"bbox"`
	Lines []jsonLine `json:"lines"`
}

type jsonLine struct {
	Bbox  [4]float64 `json:"bbox"`
	Spans []jsonSpan `json:"spans"`
}

type jsonSpan struct {
	Size   float64    `json:"size"`
	Font   string     `json:"font"`
	Origin [2]float64 `json:"origin"`
	Bbox   [4]float64 `json:"bbox"`
	Text   string     `json:"text"`
}

type jsonDoc struct {
	PageCount int           `json:"page_count"`
	Pages     []jsonPage    `json:"pages"`
	Fonts     []jsonFont    `json:"fonts,omitempty"`
	Links     []jsonLink    `json:"links,omitempty"`
	Warnings  []jsonWarning `json:"warnings,omitempty"` // document-scoped only (page==0)
}

type jsonFont struct {
	Name     string `json:"name"`
	Subtype  string `json:"subtype,omitempty"`
	Embedded bool   `json:"embedded"`
	Pages    []int  `json:"pages,omitempty"`
}

type jsonLink struct {
	FromPage int        `json:"from_page"`
	ToPage   int        `json:"to_page"`
	URI      string     `json:"uri,omitempty"`
	Bbox     [4]float64 `json:"bbox"`
}

type jsonWarning struct {
	Page    int    `json:"page"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ---- transform ----

// boxTransform converts GoPDF-native baseline-anchored geometry to top-left, y-down,
// relative to a page box [llx, lly, urx, ury]. flip is false for a degenerate box, in
// which case Y is emitted natively (no inversion).
type boxTransform struct {
	llx, ury float64
	flip     bool
}

func newBoxTransform(box [4]float64) boxTransform {
	llx, lly, urx, ury := box[0], box[1], box[2], box[3]
	return boxTransform{llx: llx, ury: ury, flip: urx > llx && ury > lly}
}

func (t boxTransform) bbox(x, y, w, h float64) [4]float64 {
	if !t.flip {
		return [4]float64{x - t.llx, y, x + w - t.llx, y + h}
	}
	return [4]float64{x - t.llx, t.ury - (y + h), x + w - t.llx, t.ury - y}
}

func (t boxTransform) origin(x, y float64) [2]float64 {
	if !t.flip {
		return [2]float64{x - t.llx, y}
	}
	return [2]float64{x - t.llx, t.ury - y}
}

// rectBbox transforms a native PDF Rect (Min bottom-left, Max top-right).
func (t boxTransform) rectBbox(r Rect) [4]float64 {
	if !t.flip {
		return [4]float64{r.Min.X - t.llx, r.Min.Y, r.Max.X - t.llx, r.Max.Y}
	}
	return [4]float64{r.Min.X - t.llx, t.ury - r.Max.Y, r.Max.X - t.llx, t.ury - r.Min.Y}
}

// ---- per-page model ----

func (p Page) pageModel() jsonPage {
	box := p.CropBox()
	t := newBoxTransform(box)
	coordOrigin := coordOriginTopLeft
	if !t.flip {
		coordOrigin = coordOriginBottomLeft
	}
	jp := jsonPage{
		Width:       box[2] - box[0],
		Height:      box[3] - box[1],
		CoordOrigin: coordOrigin,
		Blocks:      []jsonBlock{},
	}
	if p.V.IsNull() {
		return jp
	}
	// Classification pass: the SOLE emitter of the page-scoped routing warnings
	// (image_only_page, sparse_text). Best-effort: a degraded page returns an error
	// and simply carries no summary warnings.
	if s, err := p.ExtractionSummary(); err == nil {
		jp.Warnings = warningsToJSON(s.Warnings)
	}
	lines, err := p.Lines()
	if err != nil || len(lines) == 0 {
		return jp
	}
	blk := jsonBlock{Type: 0, Lines: make([]jsonLine, 0, len(lines))}
	var bb [4]float64
	first := true
	for _, ln := range lines {
		jl := jsonLine{Bbox: t.bbox(ln.X, ln.Y, ln.W, ln.H), Spans: make([]jsonSpan, 0, len(ln.Words))}
		for _, w := range ln.Words {
			jl.Spans = append(jl.Spans, jsonSpan{
				Size:   w.FontSize,
				Font:   w.Font,
				Origin: t.origin(w.X, w.Y),
				Bbox:   t.bbox(w.X, w.Y, w.W, w.H),
				Text:   w.S,
			})
		}
		blk.Lines = append(blk.Lines, jl)
		bb = unionBbox(bb, jl.Bbox, &first)
	}
	blk.Bbox = bb
	jp.Blocks = append(jp.Blocks, blk)
	return jp
}

func unionBbox(acc, b [4]float64, first *bool) [4]float64 {
	if *first {
		*first = false
		return b
	}
	if b[0] < acc[0] {
		acc[0] = b[0]
	}
	if b[1] < acc[1] {
		acc[1] = b[1]
	}
	if b[2] > acc[2] {
		acc[2] = b[2]
	}
	if b[3] > acc[3] {
		acc[3] = b[3]
	}
	return acc
}

// DebugJSON returns a structured JSON snapshot of the page's extracted text geometry,
// shaped like PyMuPDF's get_text("dict") output: { width, height, coord_origin, blocks,
// warnings }. Coordinates are top-left origin, y increasing downward (coord_origin
// "TOPLEFT"); a degenerate/missing page box reports "BOTTOMLEFT" with native y.
//
// Scope & honesty: GoPDF emits only the fields it deterministically computes. Per-span
// font flags (bold/italic), text color, and writing-mode/direction vectors are NOT
// included — GoPDF does not compute them. GoPDF performs no paragraph/block
// segmentation, so every page yields exactly one text block. Per-word boxes are
// baseline-anchored (height ≈ font size, no descenders); the span "origin" (baseline
// point) is exact. The page's own page-scoped warnings — including the OCR-routing
// signals image_only_page and sparse_text — are included under "warnings".
//
// A null Page returns a valid minimal object ({width,height from the (possibly empty)
// page box, empty blocks}) and a nil error. DebugJSON is best-effort: a content-parse
// error from the underlying Lines() pass degrades the page to empty blocks (the page box
// and any page-scoped warnings still emit) rather than returning an error, so the only
// non-nil error a readable page can return comes from json.Marshal. Running DebugJSON
// executes the content interpreter AND the page-classification pass
// (Page.ExtractionSummary), so page-scoped routing warnings (image_only_page,
// sparse_text) and document-scoped font/encoding warnings may be newly observed on the
// Reader as a side effect — exactly as calling Page.ExtractionSummary would. The result
// is deterministic and safe for concurrent use; the method does not mutate Page or Reader
// state beyond warning deduplication.
//
// Experimental: the JSON wire format may change in a future minor release and is not yet
// covered by the API-STABILITY frozen contract. The Go signature (returning []byte) is
// stable. Callers should unmarshal into their own structs or map[string]any.
func (p Page) DebugJSON() ([]byte, error) {
	return json.Marshal(p.pageModel())
}

// ---- document model ----

// DebugJSON returns a document-level structured JSON snapshot:
// { page_count, pages, fonts, links, warnings }. "pages" entries are the same per-page
// dicts as Page.DebugJSON (each carrying its own coord_origin and page-scoped warnings);
// "fonts"/"links" are the document's Fonts() and Links() (link boxes transformed to each
// link's from_page convention). "warnings" here holds every warning NOT already carried
// by a page dict: document-scoped warnings (Page==0) plus page-scoped warnings for page
// slots that were skipped (e.g. null_page_slot on a malformed page tree) and so have no
// page entry. Each emitted page dict carries its own page-scoped warnings, so the two
// together reproduce Reader.Warnings() exactly — no warning is dropped or duplicated.
// Same coordinate, honesty, determinism, concurrency, and Experimental notes as
// Page.DebugJSON apply.
//
// A nil Reader panics. Per-page extraction and Fonts() are best-effort (their failures
// degrade to missing content, not a returned error); the only error DebugJSON returns is
// from Links() (or, in practice never, json.Marshal).
func (r *Reader) DebugJSON() ([]byte, error) {
	doc := jsonDoc{
		PageCount: r.NumPage(),
		Pages:     make([]jsonPage, 0, r.NumPage()),
	}
	// seen records every 1-based page index that produced a page dict. Pages() skips
	// null page slots (emitting a page-scoped null_page_slot warning for them), so those
	// indices never enter seen and their warning is rescued into the envelope below.
	seen := make(map[int]bool)
	for i, p := range r.Pages() {
		seen[i] = true
		// pageModel runs ExtractionSummary per page, so all page-scoped routing
		// warnings are emitted on the Reader before the envelope snapshot below.
		doc.Pages = append(doc.Pages, p.pageModel())
	}
	doc.Fonts = r.buildJSONFonts()
	links, err := r.buildJSONLinks()
	if err != nil {
		return nil, err
	}
	doc.Links = links
	// Envelope warnings = every warning NOT already carried by an emitted page dict:
	// document-scoped (Page==0) PLUS page-scoped warnings for slots Pages() skipped
	// (e.g. null_page_slot), which have no page entry to hold them. Snapshot taken AFTER
	// every page/font/link pass so it is complete and deduplicated. Together with each
	// page dict's own page-scoped warnings this partitions Reader.Warnings() exactly:
	// no warning is dropped, none duplicated.
	doc.Warnings = warningsToJSON(filterWarnings(r.Warnings(), func(w ExtractionWarning) bool {
		return !seen[w.Page]
	}))
	return json.Marshal(doc)
}

func (r *Reader) buildJSONFonts() []jsonFont {
	fs := r.Fonts()
	if len(fs) == 0 {
		return nil
	}
	out := make([]jsonFont, len(fs))
	for i, f := range fs {
		// Struct conversion (jsonFont mirrors FontInfo field-for-field). This is
		// compile-safe drift detection: Go permits the conversion only while the two
		// types have identical field names/types/order (tags ignored), so any future
		// FontInfo reorder or added field breaks the build here rather than silently
		// mis-mapping. staticcheck S1016 also prefers this over a field-by-field literal.
		out[i] = jsonFont(f)
	}
	return out
}

func (r *Reader) buildJSONLinks() ([]jsonLink, error) {
	links, err := r.Links()
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, nil
	}
	// Per-page transform map so each link box uses its own page's coordinate system.
	xf := make(map[int]boxTransform)
	out := make([]jsonLink, 0, len(links))
	for _, l := range links {
		t, ok := xf[l.FromPage]
		if !ok {
			t = newBoxTransform(r.Page(l.FromPage).CropBox())
			xf[l.FromPage] = t
		}
		out = append(out, jsonLink{
			FromPage: l.FromPage, ToPage: l.ToPage, URI: l.URI, Bbox: t.rectBbox(l.Rect),
		})
	}
	return out, nil
}

// warningsToJSON maps a warning slice to DTOs; shared by the page-scoped (pageModel)
// and document-scoped (Reader.DebugJSON) paths. Returns nil for an empty input so the
// omitempty tag drops the key entirely.
func warningsToJSON(ws []ExtractionWarning) []jsonWarning {
	if len(ws) == 0 {
		return nil
	}
	out := make([]jsonWarning, len(ws))
	for i, w := range ws {
		out[i] = jsonWarning{Page: w.Page, Code: string(w.Code), Message: w.Message, Detail: w.Detail}
	}
	return out
}
