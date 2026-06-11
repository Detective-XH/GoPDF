package main

import (
	"fmt"
	"os"

	pdf "github.com/Detective-XH/gopdf"
)

func main() {
	path := "testdata/corpus/bench/synthetic-multipage.pdf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	docs, err := loadDocuments(path)
	if err != nil {
		panic(err)
	}

	for _, d := range docs {
		fmt.Printf("page %v/%v label=%v confidence=%v title=%q chars=%d\n",
			d.Metadata["page"],
			d.Metadata["total_pages"],
			d.Metadata["page_label"],
			d.Metadata["extraction_confidence"],
			d.Metadata["title"],
			len(d.PageContent),
		)
	}

	// Structured-layout sidecar: one Page.DebugJSON() per page, index-aligned with the
	// Documents above. A RAG pipeline embeds d.PageContent and uses layouts[i] for
	// bbox-aware chunking or citation. Re-open is fine for a demo.
	f, r, err := pdf.Open(path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()
	layouts := pageLayouts(r)
	for i, layout := range layouts {
		fmt.Printf("page %d layout_json_bytes=%d\n", i, len(layout))
	}
	if len(layouts) > 0 {
		preview := layouts[0]
		const previewMax = 240
		if len(preview) > previewMax {
			preview = preview[:previewMax]
		}
		fmt.Printf("page 0 layout preview: %s...\n", preview)
	}
}
