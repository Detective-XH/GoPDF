package pdf

import "strings"

// FieldType classifies a PDF form field (/FT plus /Ff flag disambiguation).
type FieldType int

const (
	FieldText     FieldType = iota // /Tx — single or multiline text
	FieldCheckBox                  // /Btn without the Radio or Pushbutton flag
	FieldRadio                     // /Btn with the Radio flag
	FieldCombo                     // /Ch with the Combo flag
	FieldList                      // /Ch without the Combo flag
	FieldOther                     // pushbuttons, signatures, unknown /FT
)

// AcroForm field-flag masks (/Ff). Bit positions are 1-based in
// ISO 32000-1 Tables 221/226/228, so bit N is mask 1<<(N-1).
const (
	ffReadOnly   = 1 << 0  // common, bit 1
	ffRadio      = 1 << 15 // /Btn, bit 16
	ffPushbutton = 1 << 16 // /Btn, bit 17
	ffCombo      = 1 << 17 // /Ch, bit 18
)

// FormField is one terminal AcroForms field (leaf of the field tree).
type FormField struct {
	Name     string // fully qualified name (parent.child.leaf via /T)
	Type     FieldType
	Value    string // /V decoded; checkbox/radio on-state name ("Yes"/"Off")
	ReadOnly bool   // common /Ff ReadOnly flag (inherited)
	PageNum  int    // 1-based page of the field's (first) widget (0 if unknown)
	Rect     Rect   // widget bounding rectangle (zero value if unknown)
}

// Fields returns all terminal AcroForms fields in the document, in /Fields
// array order (depth-first). /FT, /Ff, and /V honor field-tree inheritance
// (ISO 32000-1 §12.7.3.1); a field merged with its single widget and a field
// with multiple widget kids (radio group) both yield one entry.
//
// Returns (nil, nil) for PDFs with no /AcroForm entry. Safe for concurrent
// use: each call builds its own transient lookup maps, so repeated calls on
// a long-lived Reader do not retain document-sized state.
func (r *Reader) Fields() ([]FormField, error) {
	fields := r.Trailer().Key("Root").Key("AcroForm").Key("Fields")
	if fields.Len() == 0 {
		return nil, nil
	}
	w := &formWalker{
		annotPage: r.annotPageMap(),
		pages:     r.buildPageMap(),
	}
	seen := make(map[uint32]bool)
	for i := 0; i < fields.Len(); i++ {
		w.walk(fields.Index(i), "", seen, 0)
	}
	return w.out, nil
}

// formWalker carries the per-call lookup context for one Fields() walk. Both
// maps are transient per call, honoring the no-lifetime-retention decision
// recorded on cachedPageMap (page_summary.go).
type formWalker struct {
	out       []FormField
	annotPage map[uint32]int // annotation objptr.id → 1-based page (from /Annots)
	pages     map[uint32]int // page objptr.id → 1-based page (for /P fallback)
}

// annotPageMap maps every page-level annotation's indirect-object id to its
// 1-based page number in one bounded Pages() scan, so widgets referenced from
// both the field tree and a page's /Annots resolve to their page without a
// per-field page search.
func (r *Reader) annotPageMap() map[uint32]int {
	m := make(map[uint32]int)
	for i, p := range r.Pages() {
		annots := p.V.Key("Annots")
		for j := 0; j < annots.Len(); j++ {
			if id := annots.Index(j).ptr.id; id != 0 {
				if _, ok := m[id]; !ok {
					m[id] = i
				}
			}
		}
	}
	return m
}

// walk recurses the /Fields tree. A node with at least one /T-carrying kid is
// internal: recursion descends into those child fields only ( /T-less kids of
// an internal node are nonconformant and skipped). A node whose kids are all
// widget annotations (no /T) is a terminal field. The depth cap and visited
// set mirror walkNameTreeDepth: the tree is attacker-controlled input.
func (w *formWalker) walk(node Value, prefix string, seen map[uint32]bool, depth int) {
	if depth > maxLinkDepth || node.IsNull() {
		return
	}
	if id := node.ptr.id; id != 0 {
		if seen[id] {
			return
		}
		seen[id] = true
	}
	name := prefix
	if t := node.Key("T").Text(); t != "" {
		if name != "" {
			name += "."
		}
		name += t
	}
	kids := node.Key("Kids")
	internal := false
	for i := 0; i < kids.Len(); i++ {
		if !kids.Index(i).Key("T").IsNull() {
			internal = true
			break
		}
	}
	if internal {
		for i := 0; i < kids.Len(); i++ {
			kid := kids.Index(i)
			if !kid.Key("T").IsNull() {
				w.walk(kid, name, seen, depth+1)
			}
		}
		return
	}
	w.emit(node, name)
}

// emit appends one terminal field, resolving /FT, /Ff, and /V through the
// inheritance chain and taking Rect/PageNum from the merged widget dict or
// the first widget kid.
func (w *formWalker) emit(node Value, name string) {
	ft := fieldInheritedKey(node, "FT").Name()
	if ft == "" {
		return // no /FT anywhere up the chain: not a field (e.g. orphan widget)
	}
	ff := fieldInheritedKey(node, "Ff").Int64()
	f := FormField{
		Name:     name,
		Type:     classifyField(ft, ff),
		Value:    formFieldValue(ft, ff, fieldInheritedKey(node, "V")),
		ReadOnly: ff&ffReadOnly != 0,
	}
	widget := node
	if kids := node.Key("Kids"); kids.Len() > 0 {
		widget = kids.Index(0) // multi-widget field (radio group): first widget
	}
	if rect := widget.Key("Rect"); !rect.IsNull() {
		r4 := rectFromValue(rect)
		f.Rect = Rect{Min: Point{r4[0], r4[1]}, Max: Point{r4[2], r4[3]}}
	}
	f.PageNum = w.pageOf(widget)
	w.out = append(w.out, f)
}

// pageOf resolves a widget annotation to its 1-based page: first by the
// widget's own indirect-object id in the /Annots map, then by the /P
// back-reference into the page map. Inline widgets without /P yield 0.
func (w *formWalker) pageOf(widget Value) int {
	if id := widget.ptr.id; id != 0 {
		if n := w.annotPage[id]; n > 0 {
			return n
		}
	}
	return w.pages[widget.Key("P").ptr.id]
}

// fieldInheritedKey resolves key on node or its /Parent ancestors — /FT,
// /Ff, /V, /DA are inheritable in the field tree (ISO 32000-1 §12.7.3.1).
// Mirrors findInherited (page.go) with the same depth cap; the cap also
// bounds a malicious /Parent cycle.
func fieldInheritedKey(v Value, key string) Value {
	for depth := 0; !v.IsNull(); v = v.Key("Parent") {
		if depth++; depth > maxLinkDepth {
			return Value{}
		}
		if r := v.Key(key); !r.IsNull() {
			return r
		}
	}
	return Value{}
}

// classifyField maps /FT + /Ff flags to a FieldType.
func classifyField(ft string, ff int64) FieldType {
	switch ft {
	case "Tx":
		return FieldText
	case "Btn":
		switch {
		case ff&ffPushbutton != 0:
			return FieldOther // pushbuttons carry no persistent value
		case ff&ffRadio != 0:
			return FieldRadio
		default:
			return FieldCheckBox
		}
	case "Ch":
		if ff&ffCombo != 0 {
			return FieldCombo
		}
		return FieldList
	}
	return FieldOther
}

// formFieldValue renders /V: name objects for checkable buttons (absent /V
// means unchecked, i.e. "Off"), joined text for multi-select choice arrays,
// decoded text otherwise.
func formFieldValue(ft string, ff int64, v Value) string {
	switch ft {
	case "Btn":
		if ff&ffPushbutton != 0 {
			return ""
		}
		if name := v.Name(); name != "" {
			return name
		}
		return "Off"
	case "Ch":
		if v.Kind() == Array {
			parts := make([]string, 0, v.Len())
			for i := 0; i < v.Len(); i++ {
				parts = append(parts, v.Index(i).Text())
			}
			return strings.Join(parts, ", ")
		}
	}
	return v.Text()
}
