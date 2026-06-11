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
	labels := r.PageLabels() // nil when the document declares no /PageLabels tree

	docs := make([]Document, 0, len(summary.Pages))
	for _, ps := range summary.Pages {
		// A page whose strict text extraction fails is exactly the case the
		// extraction_confidence signal exists to route: emit it with empty content
		// and its signal (degraded) rather than aborting the whole document, so a
		// consumer can send just that page to OCR or review. GetPlainText already
		// returns "" on such a failure, which is what we surface here.
		text, _ := r.Page(ps.Page).GetPlainText(nil)
		meta := pageMetadata(ps, summary.TotalPages, pageLabel(labels, ps.Page))
		maps.Copy(meta, docProps)
		docs = append(docs, Document{PageContent: text, Metadata: meta})
	}
	return docs
}

// pageMetadata builds the per-page metadata keys. label is the resolved printed page
// label (see pageLabel) — passed in rather than computed here so the document's
// PageLabels() slice is read once per load, not once per page.
func pageMetadata(ps pdf.PageSignal, totalPages int, label string) map[string]any {
	return map[string]any{
		// page is 0-based, matching the LangChain loader convention.
		"page": ps.Page - 1,
		// page_label is the document's OWN printed label (roman-numeral front matter,
		// an offset like "32", letter ranges) via Reader.PageLabels(), falling back to
		// the 1-based page number when the document declares no /PageLabels tree. See
		// pageLabel. This matches LangChain's PyPDFLoader, which sets page_label from
		// the document's label tree.
		"page_label":  label,
		"total_pages": totalPages,
		// extraction_confidence carries the page's extraction signal verbatim
		// (text / image_only / empty / degraded). It is a routing signal, not a
		// fabricated 0-1 score; consumers treat any unrecognised value as
		// "needs review".
		"extraction_confidence": string(ps.Signal),
	}
}

// pageLabel resolves the printed page label for 1-based page n: the document's own
// label from Reader.PageLabels() when it has one, else the 1-based page number as a
// string. The fallback applies when the document declares no /PageLabels tree
// (labels == nil), when n is out of range, or when the page's label resolves empty
// (Reader.PageLabels yields "" — an uncovered page, or a label dict with no /S and
// no /P).
func pageLabel(labels []string, n int) string {
	if n >= 1 && n <= len(labels) && labels[n-1] != "" {
		return labels[n-1]
	}
	return strconv.Itoa(n)
}

// pageLayouts returns one structured-layout JSON document per reachable page, aligned
// one-to-one with buildDocuments' Documents: layouts[i] is the PyMuPDF-dict-shaped
// Page.DebugJSON() for the same page as docs[i]. It is the structured sidecar to the
// plain-text Documents — a RAG pipeline embeds docs[i].PageContent and uses layouts[i]
// for bbox-aware chunking or citation. Both iterate the Reader's reachable pages (null
// page slots are skipped identically), so the slices stay index-aligned. A per-page
// marshal failure yields a nil entry rather than aborting, mirroring buildDocuments'
// per-page tolerance.
func pageLayouts(r *pdf.Reader) [][]byte {
	out := make([][]byte, 0, r.NumPage())
	for _, p := range r.Pages() {
		js, _ := p.DebugJSON()
		out = append(out, js)
	}
	return out
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
