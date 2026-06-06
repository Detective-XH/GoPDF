// Document-level font inventory: enumerate the fonts referenced by a PDF's
// page resources.

package pdf

// FontInfo summarises a font resource referenced in the document.
type FontInfo struct {
	Name string // /BaseFont value
	// Subtype is the top-level /Subtype (/Type1, /TrueType, /Type0, /Type3,
	// etc.) of the first encountered instance of this BaseFont.
	Subtype string
	// Embedded is true when ANY instance of this BaseFont in the document
	// carries a /FontFile* stream in its font descriptor: the same name may
	// be unembedded on one page and embedded on another.
	Embedded bool
	Pages    []int // 1-based page numbers where this font appears (ascending)
}

// Fonts returns metadata for every distinct font referenced in the document's
// page-level Resources, deduplicated by BaseFont name.
//
// Scope: enumerates fonts reachable via each page's /Resources /Font dict
// (including resources inherited from /Pages ancestors). Fonts used
// exclusively inside Form XObject appearance streams or annotation content
// streams are not enumerated — those nested resources require a separate
// XObject walk that is out of scope for v0.7.
//
// Returns nil for documents with no font resources.
// Malformed or partially unreadable font entries are skipped without error.
func (r *Reader) Fonts() []FontInfo {
	seen := make(map[string]*FontInfo)
	var order []string

	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		seenOnPage := make(map[string]bool)
		for _, name := range p.Fonts() {
			f := p.Font(name)
			base := f.BaseFont()
			if base == "" {
				continue
			}
			fi, ok := seen[base]
			if !ok {
				fi = &FontInfo{
					Name:    base,
					Subtype: f.V.Key("Subtype").Name(),
				}
				seen[base] = fi
				order = append(order, base)
			}
			// Aggregate embedding across EVERY instance sharing this
			// BaseFont — two aliases (even on one page) may point at
			// different font objects, and a name unembedded here may be
			// embedded elsewhere. Short-circuit once known embedded.
			if !fi.Embedded && fontIsEmbedded(f) {
				fi.Embedded = true
			}
			// seenOnPage only gates the Pages append (no duplicate page
			// numbers); it must not skip the metadata aggregation above.
			if !seenOnPage[base] {
				seenOnPage[base] = true
				fi.Pages = append(fi.Pages, i)
			}
		}
	}

	if len(order) == 0 {
		return nil
	}
	out := make([]FontInfo, len(order))
	for i, name := range order {
		out[i] = *seen[name]
	}
	return out
}

// fontIsEmbedded reports whether a font has an embedded font program.
// For Type0 composite fonts the descriptor lives on the first descendant CIDFont.
func fontIsEmbedded(f Font) bool {
	desc := f.V.Key("FontDescriptor")
	if desc.IsNull() {
		desc = f.V.Key("DescendantFonts").Index(0).Key("FontDescriptor")
	}
	if desc.IsNull() {
		return false
	}
	return desc.Key("FontFile").Kind() == Stream ||
		desc.Key("FontFile2").Kind() == Stream ||
		desc.Key("FontFile3").Kind() == Stream
}
