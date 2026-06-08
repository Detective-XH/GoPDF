# GoPDF Examples

This guide shows common API patterns. For complete runnable programs, see the
`examples/` directory.

## Open a PDF

```go
f, r, err := pdf.Open("document.pdf")
if err != nil {
	panic(err)
}
defer func() { _ = f.Close() }()
```

For encrypted PDFs:

```go
f, err := os.Open("encrypted.pdf")
if err != nil {
	panic(err)
}
defer func() { _ = f.Close() }()

stat, err := f.Stat()
if err != nil {
	panic(err)
}

r, err := pdf.NewReaderEncrypted(f, stat.Size(), func() string {
	return "user-or-owner-password"
})
if err != nil {
	panic(err)
}
```

## Plain Text

```go
reader, err := r.GetPlainText(context.Background())
if err != nil {
	panic(err)
}

var text bytes.Buffer
_, _ = text.ReadFrom(reader)
fmt.Println(text.String())
```

## Styled Text

```go
texts, err := r.GetStyledTexts(context.Background())
if err != nil {
	panic(err)
}

for _, t := range texts {
	fmt.Printf("font=%s size=%.1f x=%.1f y=%.1f text=%s\n",
		t.Font, t.FontSize, t.X, t.Y, t.S)
}
```

## Words and Bounding Boxes

```go
p := r.Page(1)
words, err := p.Words()
if err != nil {
	panic(err)
}

for _, w := range words {
	fmt.Printf("word=%q x=%.1f y=%.1f w=%.1f h=%.1f\n",
		w.S, w.X, w.Y, w.W, w.H)
}
```

## Lines

```go
p := r.Page(1)
lines, err := p.Lines()
if err != nil {
	panic(err)
}

for _, l := range lines {
	fmt.Printf("line=%q x=%.1f y=%.1f w=%.1f h=%.1f words=%d\n",
		l.S, l.X, l.Y, l.W, l.H, len(l.Words))
}
```

## Image Draw Metadata

`Page.Images()` reports draw operations, not distinct resources. It does not
decode, decompress, or validate image bytes.

```go
p := r.Page(1)
images, err := p.Images()
if err != nil {
	panic(err)
}

for _, img := range images {
	fmt.Printf("image x=%.1f y=%.1f w=%.1f h=%.1f filter=%s declared=%dx%d\n",
		img.X, img.Y, img.W, img.H,
		img.Filter, img.DeclaredWidth, img.DeclaredHeight)
}
```

## Extraction Readiness

`Page.ExtractionSummary()` is useful for ingestion pipelines that need to route
pages with extractable text, image-only pages, or extraction warnings.

```go
for pageNum, p := range r.Pages() {
	summary, err := p.ExtractionSummary()
	if err != nil {
		fmt.Printf("page %d: extraction failed: %v\n", pageNum, err)
		continue
	}
	fmt.Printf("page=%d hasText=%t words=%d images=%d coverage=%.2f warnings=%d\n",
		summary.Page, summary.HasText, summary.WordCount,
		summary.ImageCount, summary.ImageCoverage, len(summary.Warnings))
}
```

`summary.ImageCoverage` is the fraction of the page covered by drawn image
bounding boxes (clamped to `[0,1]`): a value near `1.0` is a full-bleed scan
(route to OCR), while a small value is an incidental thumbnail or logo. A
text-bearing page whose entire text layer is page furniture — a page number or
folio at the margin — records a `sparse_text` page-scoped warning so a scanned
page with a stray page number is still routed to OCR rather than indexed as
clean text.

## Routing signal

`Reader.DocumentSummary()` and `Page.ExtractionSignal()` provide ingestion
pipelines with deterministic routing decisions (index as-is / send to OCR /
flag) without parsing logs. The page signal maps onto the usual
fast / hi_res / ocr_only routing families: text-bearing pages index as-is
(fast), image-only pages go to OCR (ocr_only), and empty or degraded pages are
flagged for review.

```go
ds := r.DocumentSummary()
fmt.Printf("total pages=%d text=%d image-only=%d empty=%d degraded=%d\n",
	ds.TotalPages, ds.TextPages, ds.ImageOnlyPages,
	ds.EmptyPages, ds.DegradedPages)

for _, ps := range ds.Pages {
	switch ps.Signal {
	case pdf.SignalText:
		fmt.Printf("page %d: fast/index as-is (%d images)\n", ps.Page, ps.ImageCount)
	case pdf.SignalImageOnly:
		fmt.Printf("page %d: ocr_only (no extractable text)\n", ps.Page)
	case pdf.SignalDegraded, pdf.SignalEmpty:
		fmt.Printf("page %d: flag for review (signal=%s)\n", ps.Page, ps.Signal)
	default:
		// Tolerate unknown values added in later releases.
		fmt.Printf("page %d: unknown signal %q (flag for review)\n", ps.Page, ps.Signal)
	}
}
```

Per-page routing:

```go
p := r.Page(1)
if p.ExtractionSignal() == pdf.SignalText {
	fmt.Println("page 1 has extractable text: index as-is")
}
```

Document-scoped confidence metadata: `ds.Warnings` carries font and encoding
issues that reduce confidence. An ecosystem adapter would surface the page
signal under the cross-tool `extraction_confidence` metadata key.

## Diagnostics

Warnings are deterministic and deduplicated. They are intended for pipeline
confidence signals rather than log parsing.

```go
_, _ = r.GetPlainText(context.Background())

for _, warning := range r.Warnings() {
	fmt.Printf("page=%d code=%s detail=%s\n",
		warning.Page, warning.Code, warning.Detail)
}
```

`warning.Code` is an `ExtractionWarningCode`; codes are additive across minor
versions, so callers must tolerate unrecognised values. Decode-path codes flag
text whose Unicode may be approximate — `missing_tounicode`, `fallback_encoding`,
`unsupported_encoding`, `missing_glyph_mapping`. Two geometry routing signals flag
runs whose layout geometry is unreliable: `rotated_text` (a text run with a
rotated, non-horizontal baseline — synthetic-italic shear is *not* flagged) and
`vertical_writing_mode` (a vertical `-V` CMap whose advances are not honored). Both
are document-scoped (`Page == 0`); `rotated_text` is observed only on the
`Content`/`Words`/`Lines`/`Texts` path (the plain-text path tracks no geometry).

Three page-scoped codes (`Page > 0`, emitted by `Page.ExtractionSummary`) route
pages for OCR: `image_only_page` (images drawn, no extractable text),
`sparse_text` (the only text is page furniture — a page number/folio at the
margin), and `null_page_slot` (a null page-tree slot was skipped).

## Metadata

Classic `/Info` metadata:

```go
info := r.Info()
fmt.Println(info.Title())
fmt.Println(info.Author())
fmt.Println(info.CreationDate())
```

Raw XMP metadata:

```go
xmp, err := r.XMP()
if err != nil {
	panic(err)
}
if xmp != nil {
	fmt.Printf("xmp bytes=%d\n", len(xmp))
}
```

## Font Inventory

```go
for _, font := range r.Fonts() {
	fmt.Printf("font=%s subtype=%s embedded=%t pages=%v\n",
		font.Name, font.Subtype, font.Embedded, font.Pages)
}
```

## Annotations and Named Destinations

```go
p := r.Page(1)
annotations, err := p.Annotations()
if err != nil {
	panic(err)
}

for _, ann := range annotations {
	fmt.Printf("type=%d uri=%s targetPage=%d rect=%+v content=%q\n",
		ann.Type, ann.URI, ann.Page, ann.Rect, ann.Content)
}

page, err := r.Dest("Chapter1")
if errors.Is(err, pdf.ErrDestNotFound) {
	fmt.Println("destination not found")
} else if err != nil {
	panic(err)
} else {
	fmt.Printf("Chapter1 starts on page %d\n", page)
}
```

## Link Aggregation

```go
links, err := r.Links()
if err != nil {
	panic(err)
}

for _, l := range links {
	if l.URI != "" {
		fmt.Printf("page %d: external link %s\n", l.FromPage, l.URI)
		continue
	}
	fmt.Printf("page %d: internal link to page %d\n", l.FromPage, l.ToPage)
}
```

## Form Fields

```go
fields, err := r.Fields()
if err != nil {
	panic(err)
}

for _, f := range fields {
	fmt.Printf("%s (page %d) = %q\n", f.Name, f.PageNum, f.Value)
}
```

## Attachments

```go
atts, err := r.Attachments()
if err != nil {
	panic(err)
}

for _, a := range atts {
	fmt.Printf("%s (%s, %d bytes)\n", a.Name, a.MimeType, a.Size)
	rc, err := a.Data()
	if err != nil {
		panic(err)
	}
	// ... read rc ...
	_ = rc.Close()
}
```

## Page Iteration

```go
for pageNum, p := range r.Pages() {
	if p.V.IsNull() {
		continue
	}
	fmt.Printf("page %d mediaBox=%+v cropBox=%+v\n",
		pageNum, p.MediaBox(), p.CropBox())
}
```
