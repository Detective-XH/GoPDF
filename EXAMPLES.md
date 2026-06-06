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
	fmt.Printf("page=%d hasText=%t words=%d images=%d warnings=%d\n",
		summary.Page, summary.HasText, summary.WordCount,
		summary.ImageCount, len(summary.Warnings))
}
```

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
