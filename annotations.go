package pdf

import "errors"

// ErrDestNotFound is returned by Reader.Dest when the named destination is absent
// from the document's /Names/Dests name tree.
var ErrDestNotFound = errors.New("named destination not found")

// AnnotationType classifies a PDF annotation (/Subtype).
type AnnotationType int

const (
	AnnotLink AnnotationType = iota // /Link — URI or GoTo action
	AnnotText                       // /Text — sticky note / comment
	AnnotOther
)

// Annotation represents a single PDF annotation on a page.
//
// URI is non-empty only for /Link annotations with a /URI action.
// Page is non-zero only for /Link annotations with a /GoTo action that can be resolved.
// Content carries the /Contents field (comment body for /Text annotations).
// Rect is in PDF user space (origin bottom-left, y increases upward).
type Annotation struct {
	Type    AnnotationType
	Rect    Rect   // bounding rectangle in PDF coordinate space
	URI     string // non-empty for URI actions
	Page    int    // 1-based target page for GoTo actions (0 if not applicable)
	Content string // /Contents text (comment body)
}

// Annotations returns all annotations on the page.
//
// The result slice is in /Annots array order; the PDF spec does not guarantee reading order.
// Annotations returns (nil, nil) for pages with no /Annots entry or an empty array.
// The call does not mutate Reader or Page state and is safe for repeated calls.
func (p Page) Annotations() ([]Annotation, error) {
	annots := p.V.Key("Annots")
	if annots.IsNull() {
		return nil, nil
	}
	n := annots.Len()
	if n == 0 {
		return nil, nil
	}
	pages := p.V.r.buildPageMap()
	dests := p.V.r.Trailer().Key("Root").Key("Names").Key("Dests")
	out := make([]Annotation, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, parseAnnotation(annots.Index(i), pages, dests))
	}
	return out, nil
}

func parseAnnotation(a Value, pages map[uint32]int, dests Value) Annotation {
	var ann Annotation
	switch a.Key("Subtype").Name() {
	case "Link":
		ann.Type = AnnotLink
	case "Text":
		ann.Type = AnnotText
	default:
		ann.Type = AnnotOther
	}
	if rect := a.Key("Rect"); !rect.IsNull() {
		r4 := rectFromValue(rect)
		ann.Rect = Rect{Min: Point{r4[0], r4[1]}, Max: Point{r4[2], r4[3]}}
	}
	ann.Content = a.Key("Contents").Text()
	if action := a.Key("A"); !action.IsNull() {
		switch action.Key("S").Name() {
		case "URI":
			ann.URI = action.Key("URI").RawString()
		case "GoTo":
			ann.Page = resolveAnnotDest(action.Key("D"), pages, dests)
		}
	}
	// Direct /Dest on annotation (no action wrapper; used in older PDFs).
	if dest := a.Key("Dest"); !dest.IsNull() && ann.Page == 0 {
		ann.Page = resolveAnnotDest(dest, pages, dests)
	}
	return ann
}

// resolveAnnotDest converts a PDF destination value to a 1-based page number.
// d may be an array ([pageRef /XYZ ...]) or a string (named destination).
func resolveAnnotDest(d Value, pages map[uint32]int, dests Value) int {
	switch d.Kind() {
	case Array:
		return pageFromDestArray(d, pages)
	case String:
		if !dests.IsNull() {
			return walkNameTree(dests, d.RawString(), pages)
		}
	}
	return 0
}

// Dest resolves a named destination to a 1-based page number.
// Returns (0, ErrDestNotFound) if the name is not in the /Names/Dests tree
// or if the document has no /Names/Dests entry.
func (r *Reader) Dest(name string) (int, error) {
	dests := r.Trailer().Key("Root").Key("Names").Key("Dests")
	if dests.IsNull() {
		return 0, ErrDestNotFound
	}
	pages := r.buildPageMap()
	if p := walkNameTree(dests, name, pages); p > 0 {
		return p, nil
	}
	return 0, ErrDestNotFound
}

// walkNameTree searches a PDF name tree node for name and returns the resolved
// 1-based page number, or 0 if not found. It delegates to walkNameTreeDepth with
// a fresh visited set, which bounds traversal of an attacker-controlled tree.
// Leaf nodes carry a /Names array of alternating [key, destArray] pairs.
// Internal nodes carry a /Kids array of child node dicts.
func walkNameTree(node Value, name string, pages map[uint32]int) int {
	return walkNameTreeDepth(node, name, pages, make(map[uint32]bool), 0)
}

// walkNameTreeDepth is the bounded recursion behind walkNameTree. The depth cap
// (maxLinkDepth) prevents a deeply nested tree from overflowing the stack, and
// the visited set short-circuits a /Kids cycle that points back to an ancestor
// node, matching how the rest of the package treats PDF links as untrusted.
func walkNameTreeDepth(node Value, name string, pages map[uint32]int, seen map[uint32]bool, depth int) int {
	if depth > maxLinkDepth {
		return 0
	}
	// A /Kids edge can point back to an already-visited indirect object; stop the
	// cycle. Inline (id == 0) nodes cannot be referenced twice, so skip the guard.
	if id := node.ptr.id; id != 0 {
		if seen[id] {
			return 0
		}
		seen[id] = true
	}
	if ns := node.Key("Names"); !ns.IsNull() {
		for i := 0; i+1 < ns.Len(); i += 2 {
			if ns.Index(i).RawString() == name {
				if dest := ns.Index(i + 1); dest.Kind() == Array {
					return pageFromDestArray(dest, pages)
				}
			}
		}
	}
	kids := node.Key("Kids")
	for i := 0; i < kids.Len(); i++ {
		if p := walkNameTreeDepth(kids.Index(i), name, pages, seen, depth+1); p > 0 {
			return p
		}
	}
	return 0
}
