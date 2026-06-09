// Command langchaingo_loader is a runnable example: a langchaingo-style PDF
// document loader built on GoPDF. It emits one document per page, each carrying
// the page's plain text and a stable, LangChain/LlamaIndex-aligned metadata key
// set, so a Go RAG/ingestion pipeline can index GoPDF output directly.
package main

import (
	"maps"
	"strconv"
	"time"

	pdf "github.com/Detective-XH/gopdf"
)

// Document carries the fields this loader populates from langchaingo's
// schema.Document — PageContent and Metadata. langchaingo's schema.Document also
// has a Score float32 field, set later during retrieval rather than by a loader, so
// switching this example to schema.Document is an import swap that leaves Score at
// its zero value. The type is defined locally so the example adds no dependency to
// the library module.
type Document struct {
	PageContent string
	Metadata    map[string]any
}

// loadDocuments opens the PDF at path and returns one Document per reachable page.
// Only opening the file can fail; a per-page extraction failure does not abort the
// load (see buildDocuments).
func loadDocuments(path string) ([]Document, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return buildDocuments(r), nil
}

// buildDocuments maps an open Reader to per-page Documents. The document-property
// keys are computed once and copied onto every page so the key set is uniform.
func buildDocuments(r *pdf.Reader) []Document {
	summary := r.DocumentSummary()
	docProps := documentProperties(r.Info())

	docs := make([]Document, 0, len(summary.Pages))
	for _, ps := range summary.Pages {
		// A page whose strict text extraction fails is exactly the case the
		// extraction_confidence signal exists to route: emit it with empty content
		// and its signal (degraded) rather than aborting the whole document, so a
		// consumer can send just that page to OCR or review. GetPlainText already
		// returns "" on such a failure, which is what we surface here.
		text, _ := r.Page(ps.Page).GetPlainText(nil)
		meta := pageMetadata(ps, summary.TotalPages)
		maps.Copy(meta, docProps)
		docs = append(docs, Document{PageContent: text, Metadata: meta})
	}
	return docs
}

// pageMetadata builds the per-page metadata keys.
func pageMetadata(ps pdf.PageSignal, totalPages int) map[string]any {
	return map[string]any{
		// page is 0-based, matching the LangChain loader convention.
		"page": ps.Page - 1,
		// page_label is a FALLBACK: GoPDF exposes no PDF page-label tree, so the
		// example emits the 1-based page number as a string rather than the
		// document's own printed label (e.g. roman-numeral front matter).
		"page_label":  strconv.Itoa(ps.Page),
		"total_pages": totalPages,
		// extraction_confidence carries the page's extraction signal verbatim
		// (text / image_only / empty / degraded). It is a routing signal, not a
		// fabricated 0-1 score; consumers treat any unrecognised value as
		// "needs review".
		"extraction_confidence": string(ps.Signal),
	}
}

// documentProperties maps the /Info dictionary to lowercase metadata keys. Every
// key is always emitted: an absent string property is "" and an absent date is ""
// (never the zero-time sentinel), so the key set is uniform across documents.
func documentProperties(info pdf.Info) map[string]any {
	return map[string]any{
		"title":        info.Title(),
		"author":       info.Author(),
		"subject":      info.Subject(),
		"creator":      info.Creator(),
		"producer":     info.Producer(),
		"creationdate": formatDate(info.CreationDate()),
		"moddate":      formatDate(info.ModDate()),
	}
}

// formatDate renders a PDF date as RFC3339, or "" when the date is absent.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
