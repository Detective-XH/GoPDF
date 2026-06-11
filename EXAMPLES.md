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
	fmt.Printf("word=%q font=%q size=%.1f x=%.1f y=%.1f w=%.1f h=%.1f\n",
		w.S, w.Font, w.FontSize, w.X, w.Y, w.W, w.H)
}
```

`Word.Font`/`Word.FontSize` carry the font name and point size of the word's
first glyph; for a word mixing fonts or sizes the first glyph wins, and `Font`
is empty when the glyph carried no font name.

## Lines

```go
p := r.Page(1)
lines, err := p.Lines()
if err != nil {
	panic(err)
}

for _, l := range lines {
	fmt.Printf("line=%q font=%q size=%.1f x=%.1f y=%.1f w=%.1f h=%.1f words=%d\n",
		l.S, l.Font, l.FontSize, l.X, l.Y, l.W, l.H, len(l.Words))
}
```

`Line.Font`/`Line.FontSize` come from the line's first word (same first-wins
rule). `Line.S` joins the words with single spaces, except between two glyphs of
a space-less CJK script (Han, Hiragana, Katakana), where no space is inserted so
a per-glyph run rejoins seamlessly; Korean (Hangul) keeps its inter-word spaces.
On a multi-column page a line is split per column where a recurring column gutter
separates the words, so columns no longer glue into one line — but lines are
still emitted in top-to-bottom band order (columns interleaved by row), not full
column-major reading order.

The older `Page.GetTextByRow()` / `Page.GetTextByColumn()` methods are
**deprecated**: they run a separate text interpreter that does not carry per-word
font metadata or feed the decode-path quality signals. Use `Page.Lines()`
(column-aware) and `Page.Words()` instead. The legacy methods remain functional.

## Blocks

```go
p := r.Page(1)
blocks, err := p.Blocks()
if err != nil {
	panic(err)
}

for _, b := range blocks {
	fmt.Printf("block font=%q size=%.1f x=%.1f y=%.1f w=%.1f h=%.1f lines=%d\n%s\n",
		b.Font, b.FontSize, b.X, b.Y, b.W, b.H, len(b.Lines), b.S)
}
```

`Page.Blocks()` groups the page's lines into visual blocks read in **column-major**
order: on a multi-column page each detected column is read top-to-bottom in full
(unlike `Page.Lines()`, which stays row-major), and within a column consecutive
lines separated by no more than a block-sized vertical gap merge into one `Block`.
On a single-column page this degrades to gap-based paragraph grouping. `Block.S`
joins the constituent lines with `"\n"`; `Block.Lines` preserves them top-to-bottom
(each `Line` still carries its own `Font`/`FontSize` for heading-vs-body signals);
`Block.Font`/`FontSize` come from the first line (first-wins). `Block.X/Y/W/H` is the
bounding box (bottom-left origin, Y increases upward).

> **Experimental:** the grouping heuristic (line-to-block assignment, the gap
> threshold, and column-major ordering details) may change in a minor release; the Go
> signature and field set are additive-stable. Blocks are visual groupings only — no
> paragraph/section semantics, and reading order around a full-width masthead or
> mid-page heading is best-effort.

## Ligatures and Unicode normalization

GoPDF returns decoded text **verbatim** — it performs no Unicode normalization on
any extraction path (`GetPlainText`, `GetStyledTexts`, `Words`, `Lines`). Whatever
Unicode a PDF's encoding declares is exactly what you get back.

The case that surprises most pipelines is typographic ligatures. When a producer
encodes "fi"/"fl" as single glyphs, they commonly arrive as the Unicode
compatibility codepoints **U+FB01 "ﬁ"** and **U+FB02 "ﬂ"** rather than the ASCII
pairs `f`+`i` / `f`+`l`. GoPDF passes them through whichever decode path applies,
never normalizing them away. A `/ToUnicode` CMap or a `/Differences` array (which
resolves the `fi`/`fl`/`ff`/`ffi`/`ffl` glyph names through the Adobe Glyph List)
can carry all five Latin f-ligatures — U+FB00 "ﬀ", U+FB01 "ﬁ", U+FB02 "ﬂ",
U+FB03 "ﬃ", U+FB04 "ﬄ". The built-in MacRoman and PDFDoc byte encodings carry only
`ﬁ`/`ﬂ` (U+FB01/U+FB02) in fixed slots; WinAnsiEncoding carries no ligatures at all.

Left as-is, a substring search for `"find"` misses `"ﬁnd"`, and a tokenizer may
treat `ﬁ` as a single token. Search and RAG pipelines usually want to fold
ligatures to their ASCII expansions. GoPDF does not do this for you, and exposes no
normalization option — fold caller-side, choosing how aggressive to be.

**Targeted fold (recommended for most pipelines)** — a `strings.NewReplacer`
covering just the Latin ligatures. It is deterministic and leaves every other
character (fractions, symbols, digits) untouched:

```go
var ligatureFolder = strings.NewReplacer(
	"ﬀ", "ff",
	"ﬁ", "fi",
	"ﬂ", "fl",
	"ﬃ", "ffi",
	"ﬄ", "ffl",
)

clean := ligatureFolder.Replace(text)
```

**Built-in helper** — `pdf.NormalizeText` is that same targeted fold, in the library, so
you don't hand-roll the replacer. It folds U+FB00–U+FB06 (the five f-ligatures plus the two
long-s/st ligatures `ﬅ`/`ﬆ`) to their ASCII forms and leaves every other rune untouched. It
is opt-in (no extraction path ever calls it) and allocates nothing when the input carries no
ligature:

```go
clean := pdf.NormalizeText(text)
```

One ASCII-fold caveat: `ﬅ` (U+FB05, long-s + t) folds to `st`, mapping its long-s (U+017F) to
plain `s` — so `beﬅ` becomes `best`. That is the search/RAG-friendly fold and, for this one
codepoint, slightly *more* aggressive than NFKC (which yields `ſt`); a standalone long-s
elsewhere in the text is never touched. Otherwise `NormalizeText` deliberately does *not* fold
`½`, superscripts, or full-width forms — the NFKC trade-off below still applies, which is
exactly why the fold is targeted.

**Blanket NFKC** — `golang.org/x/text/unicode/norm` folds *all* Unicode
compatibility forms in one pass, ligatures included:

```go
import "golang.org/x/text/unicode/norm"

clean := norm.NFKC.String(text)
```

NFKC is heavier-handed: besides ligatures it also rewrites characters you may want
to keep — `½` (U+00BD) becomes `1⁄2` (with U+2044 fraction slash), superscript `²`
becomes `2`, and full-width forms collapse to ASCII. That is wrong for financial or
scientific text where `½` or `²` carry meaning. Prefer the targeted replacer unless
you genuinely want aggressive normalization everywhere.

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
issues that reduce confidence. An ecosystem adapter surfaces the page signal
under the cross-tool `extraction_confidence` metadata key — realised in
`examples/langchaingo_loader` (see [Ecosystem adapters](#ecosystem-adapters-langchaingo--rag-loaders)).

### Decode-quality ratios

`DecodeRatios` (on each `PageSignal` and rolled up on `DocumentSummary`) reports
what fraction of a page's decoded glyphs came through a lower-confidence decode
path, so a pipeline can re-route text that is *present but unreliable* — text the
signal alone would call `SignalText`. The fields are stable facts, not a score:
you choose the thresholds. `Glyphs` is the shared denominator; each ratio is in
`[0,1]`. Only text-classified pages contribute glyphs; the document rollup is the
weighted ratio (glyph-count weighted), not a mean of per-page ratios.

```go
ds := r.DocumentSummary()

// Document-level: what share of the whole document decoded unreliably?
dr := ds.DecodeRatios
fmt.Printf("doc glyphs=%d missing_tounicode=%.1f%% fallback=%.1f%% unmapped=%.1f%%\n",
	dr.Glyphs, dr.MissingToUnicodeRatio*100, dr.FallbackRatio*100, dr.UnmappedRatio*100)

// Per-page: send a text page with mostly approximate Unicode to OCR anyway.
// The three ratios are not disjoint: a U+FFFD glyph is also counted in its
// decode-source bucket, so when that is missing-/ToUnicode or fallback the same
// glyph lands in two ratios. Threshold each one independently — never sum them.
for _, ps := range ds.Pages {
	dr := ps.DecodeRatios
	if ps.Signal == pdf.SignalText && dr.Glyphs > 0 &&
		(dr.MissingToUnicodeRatio > 0.5 || dr.FallbackRatio > 0.5 || dr.UnmappedRatio > 0.2) {
		fmt.Printf("page %d: text present but low-confidence decode — route to OCR\n", ps.Page)
	}
}
```

A page whose entire text decodes through an unknown `/Encoding` shows all three
ratios at 0 (that path is not one of the three named ratios); it is never silent,
though — it always fires the document-scoped `unsupported_encoding` warning.

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

A fourth code, `non_finite_geometry`, is emitted by `Page.DebugJSON` /
`Reader.DebugJSON` when extracted geometry held a non-finite coordinate (±Inf/NaN —
reachable when adversarial content-stream numbers overflow the text-matrix
multiplication, a page box overflows its width subtraction, or a link rectangle
overflows its per-page transform). DebugJSON sanitizes the value to `0` so its JSON
stays valid and records this warning, so a zeroed coordinate is distinguishable from
real geometry at the origin. Page/text geometry is page-scoped (in the page dict);
link geometry, surfaced only by `Reader.DebugJSON`, is document-scoped (`Page == 0`,
in the envelope) with the affected page in `Detail` ("link on page N").

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

## Page labels

`Reader.PageLabels()` returns the document's *printed* page label for every page —
the "iv", "A-3", or "12" a reader sees on the page — rather than its 1-based
sequence number. Front matter is routinely numbered in lower roman and the body
restarts at decimal 1, so for a typical book page 4 of the file prints "iv" and
page 9 prints "1". For citations in a RAG pipeline this is the difference between
"see page iv" (what the user can find) and "see page 4" (an index the user never
sees).

Labels come from the PDF `/PageLabels` number tree (PDF 32000-1 §12.4.2): an
optional prefix (`/P`), a numbering style (`/S` — decimal, upper/lower roman, or
upper/lower letters), and a per-range start value (`/St`).

```go
labels := r.PageLabels()
if labels == nil {
	// No /PageLabels tree — fall back to the 1-based page number.
	fmt.Println("document declares no page labels")
} else {
	for i, label := range labels {
		// labels[i] is the printed label for 1-based page i+1.
		fmt.Printf("page %d is printed as %q\n", i+1, label)
	}
}
```

Semantics: the result has length `NumPage()` (index N is 1-based page N+1).
`PageLabels()` returns `nil` when the document declares no `/PageLabels` tree, so
a caller can cleanly fall back to the page number. A page that the tree leaves
uncovered gets `""` at its index. The method is best-effort — malformed ranges
are skipped, never an error — and deterministic and safe for concurrent use.

## Structured JSON debug export (experimental)

`Page.DebugJSON()` and `Reader.DebugJSON()` serialise the stable extraction
primitives into a PyMuPDF `get_text("dict")`-shaped JSON snapshot — a thin,
deterministic projection for debugging and ingestion-pipeline inspection, **not a
converter**. Both return `[]byte`; unmarshal into your own structs or
`map[string]any`.

```go
p := r.Page(1)
js, err := p.DebugJSON()
if err != nil {
	panic(err)
}
fmt.Println(string(js))
// {"width":612,"height":792,"coord_origin":"TOPLEFT",
//  "blocks":[{"type":0,"bbox":[...],
//    "lines":[{"bbox":[...],"spans":[
//      {"size":12,"font":"Helvetica","origin":[x,y],"bbox":[x0,y0,x1,y1],"text":"Hello"}]}]}]}

// Whole-document envelope: pages + fonts + links + warnings.
doc, err := r.DebugJSON()
```

What it emits, and what it deliberately does not:

- **Coordinates are top-left, y-down** (PyMuPDF convention), tagged per page with
  `coord_origin` (`"TOPLEFT"`; a degenerate/missing page box reports `"BOTTOMLEFT"`
  with native y). Each span's `origin` is the exact text baseline point; `bbox` is
  baseline-anchored (height ≈ font size, no glyph descenders).
- **Only fields GoPDF actually computes.** PyMuPDF's `flags` (bold/italic),
  `color`, and per-line `wmode`/`dir` are **omitted, never zero-filled** — GoPDF
  does not compute them, and a misleading `0` would imply otherwise. Vertical /
  rotated content is surfaced through warnings, not faked geometry.
- **One text block per page.** GoPDF performs no paragraph/block segmentation, so
  every page is a single `type:0` block; spans are one per word.
- **Diagnostics travel in-band.** Each page dict carries its page-scoped `warnings`
  (including the OCR-routing `image_only_page` / `sparse_text` signals);
  `Reader.DebugJSON`'s envelope carries the document-scoped warnings plus any
  page-scoped warning whose slot was skipped (e.g. `null_page_slot`). Page dicts and
  envelope together reproduce `Reader.Warnings()` exactly.
- **Experimental:** the JSON wire format may change in a future minor release and is
  not yet covered by the [API stability contract](API-STABILITY.md). The Go
  signatures (returning `[]byte`) are stable.

Calling `DebugJSON` runs the content interpreter and the page-classification pass,
so warnings may newly appear on `Reader.Warnings()` as a side effect — the same
contract as `Page.ExtractionSummary`. For a ready-made RAG metadata projection
instead of raw geometry, see the adapter below.

## Ecosystem adapters (langchaingo / RAG loaders)

`examples/langchaingo_loader` is a runnable adapter for Go RAG pipelines. It emits
one document per page, each carrying the page's plain text and a stable,
LangChain/LlamaIndex-aligned metadata key set. It depends only on GoPDF: the
`Document` type is defined locally and carries the fields this loader populates —
`PageContent string` and `Metadata map[string]any`. langchaingo's `schema.Document`
additionally has a `Score float32` field, set during retrieval rather than by a
loader, so switching this example to `schema.Document` is an import swap that leaves
`Score` at its zero value.

Per-page metadata keys:

| Key | Type | Source |
|---|---|---|
| `page` | int | 0-based page index |
| `page_label` | string | the document's own printed label via [`Reader.PageLabels()`](#page-labels) (roman-numeral front matter, an offset like "32", letter ranges), falling back to the 1-based page number when the document declares no `/PageLabels` tree |
| `total_pages` | int | `Reader.NumPage()` |
| `title`, `author`, `subject`, `creator`, `producer` | string | `Reader.Info()` (empty string when absent) |
| `creationdate`, `moddate` | string | `Reader.Info()` dates as RFC3339 (empty string when absent) |
| `extraction_confidence` | string | the page's extraction signal (`text` / `image_only` / `empty` / `degraded`) — a routing signal, not a 0–1 score |

Every key is always present; a missing document property is an empty string, so
downstream consumers see a uniform schema across pages and documents.

```go
docs, err := loadDocuments("input.pdf")
// docs[i].PageContent  -> page i's plain text
// docs[i].Metadata     -> the keys above; route on extraction_confidence
```

Run it:

```bash
go run ./examples/langchaingo_loader
```
