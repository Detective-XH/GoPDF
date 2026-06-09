# API Stability Contract

GoPDF is an extraction-first library: exported APIs are long-term contracts.
This document classifies every exported symbol into a stability tier and states
exactly what each tier promises. It applies from the release that ships it and
holds until v1.0.0 declares the whole surface frozen under Go module semver
(breaking changes would then require a `/v2` module path).

## Tiers at a glance

| Tier | Promise |
|------|---------|
| **Stable** | No signature changes, no removals, no semantic narrowing. New struct fields and new methods may be ADDED (use keyed struct literals). |
| **Additive-evolving** | Same as Stable, plus: named upcoming releases WILL add fields/codes here. Listed so additions never surprise. |
| **Deprecation-review** | Not covered by this contract. May be deprecated in a future minor release after a documented review. |

Everything exported and not listed under Additive-evolving or Deprecation-review
is **Stable**.

## Stable tier

Reader construction and document access:

- `Open(file string) (*os.File, *Reader, error)`
- `OpenBytes(src []byte) (*Reader, error)`
- `NewReader(f io.ReaderAt, size int64) (*Reader, error)`
- `NewReaderEncrypted(f io.ReaderAt, size int64, pw func() string) (*Reader, error)`
- `Reader.NumPage() int`, `Reader.Page(num int) Page` (1-indexed),
  `Reader.Pages() iter.Seq2[int, Page]`
- `Reader.GetPlainText(ctx context.Context) (io.Reader, error)`
- `Reader.GetStyledTexts(ctx context.Context) ([]Text, error)`
- `Reader.Trailer() Value`, `Reader.Outline() Outline`,
  `Reader.Dest(name string) (int, error)`, `Reader.Info() Info`
- Aggregations: `Reader.Fonts() []FontInfo`, `Reader.Links() ([]LinkRef, error)`,
  `Reader.Fields() ([]FormField, error)`,
  `Reader.Attachments() ([]Attachment, error)`, `Reader.XMP() ([]byte, error)`,
  `Reader.Warnings() []ExtractionWarning`

Page-level extraction primitives:

- `Page.GetPlainText(fonts map[string]*Font) (string, error)`
- `Page.Texts() iter.Seq[Text]`, `Page.Words() ([]Word, error)`,
  `Page.Lines() ([]Line, error)`
- `Page.Annotations() ([]Annotation, error)`, `Page.Images() ([]ImageRef, error)`
- `Page.ExtractionSummary() (PageExtractionSummary, error)` — the method is
  frozen; its result struct is Additive-evolving (see below)
- `Page.Fonts() []string`, `Page.Font(name string) Font`,
  `Page.Resources() Value`, `Page.MediaBox()`, `Page.CropBox()`
- `Page.Content() Content`

Types backing the above (`Text`, `Word`, `Line`, `Content`, `FontInfo`,
`LinkRef`, `FormField`, `Attachment`, `Annotation`, `Outline`, `Info`, `Point`,
`Rect`, `Value`, `Font`, `TextEncoding`) — existing fields and methods are
frozen; fields may be added.

### Drop-in lineage compatibility

GoPDF descends from `ledongthuc/pdf` / `dslipak/pdf` / `rsc.io/pdf`. The full
call-site set used by langchaingo's PDF document loader (verified against its
source, 2026-06-07) — `NewReader`, `NewReaderEncrypted`, `Reader.NumPage`,
`Reader.Page`, `Page.Fonts`, `Page.Font`, `Page.GetPlainText(fonts)` — is
signature-identical in GoPDF and frozen here. Known divergences from the
ancestors, kept deliberately: `Open` returns `(*os.File, *Reader, error)`;
`Reader.GetPlainText` takes a `context.Context`.

### Determinism promise

For the same input bytes and the same sequence of calls, every Stable and
Additive-evolving API returns identical output, on every platform. Recoverable
extraction problems produce partial results plus `Warnings()`, never silent
loss where detectable.

### Coordinate system

All geometry is PDF-native user space: origin at the bottom-left of the page,
X rightward, Y upward, units in points (1/72 inch).

Semantics differ by type — apply conversions accordingly:

- `Text.X`/`Text.Y` is the glyph's text-space position (the baseline origin,
  with text rise applied). `Text` carries a width `W` but no height.
- `Word` and `Line` carry best-effort boxes: `Y` is the lowest glyph baseline
  in the unit, and `H` extends from that baseline to the top of the tallest
  nominal font box (baseline + font size). Descenders may extend below `Y`.
  The nominal box spans `[X, X+W] × [Y, Y+H]`.

To convert a nominal box to top-left screen space (MuPDF, poppler, OCR and
layout-model conventions):

```
y_screen_top = pageHeight − (Y + H)   // pageHeight from MediaBox/CropBox
```

Any future serialized export will tag its coordinate origin explicitly rather
than assume one.

## Additive-evolving tier

Planned, pre-announced additions (additive only — nothing existing changes):

| Symbol | Planned additions |
|--------|-------------------|
| `PageExtractionSummary` | further per-page fields (the `ImageCoverage` image-coverage ratio has shipped; decode-path quality ratios shipped on `PageSignal`/`DocumentSummary` as `DecodeRatios`) |
| `ExtractionWarningCode` | new warning codes (the enum is additive by design; match known codes, pass unknown ones through). Shipped: page-scoped `sparse_text` (page furniture, no body text) |
| `ImageRef` | image metadata fields (e.g. color space, inline-image dimensions) |
| `Word`, `Line` | font name/size fields (per-word font info), aligning with the cross-ecosystem norm. Shipped: `Font string` + `FontSize float64` on both (first-glyph/first-word wins) |
| `Text` | height field completing the bounding box; possibly an orientation field later |

### Additive-evolving: extraction-quality tier

- `type ExtractionSignal string` — routing signal enum. Values: `SignalText` ("text"), `SignalImageOnly` ("image_only"), `SignalEmpty` ("empty"), `SignalDegraded` ("degraded"). The value set is additive (callers MUST tolerate unknown values, treating them as "needs review").
- `Page.ExtractionSignal() ExtractionSignal` — classifies the page's extraction readiness for ingestion routing. Deterministic, safe for concurrent use.
- `type PageSignal struct { Page int; Signal ExtractionSignal; ImageCount int; DecodeRatios DecodeRatios }` — one page's routing classification with image-draw count and decode-path quality ratios.
- `type DocumentSummary struct { TotalPages int; Pages []PageSignal; TextPages int; ImageOnlyPages int; EmptyPages int; DegradedPages int; Warnings []ExtractionWarning; DecodeRatios DecodeRatios }` — document-level extraction summary with per-signal tallies and a weighted decode-ratio rollup. Fields are additive.
- `type DecodeRatios struct { Glyphs int; MissingToUnicodeRatio float64; FallbackRatio float64; UnmappedRatio float64 }` — the fraction of decoded glyphs that came through a lower-confidence decode path (missing `/ToUnicode`, charset fallback, or U+FFFD-unmapped), over the shared `Glyphs` denominator. Stable facts, not a score; deterministic and concurrency-safe (never reads the warning store). Fields are additive.
- `Reader.DocumentSummary() DocumentSummary` — classifies every page and aggregates signals to document level.

Consumers should treat these structs as growable: decode JSON leniently and
construct values with keyed literals.

## Deprecation-review tier

| Symbol | Status |
|--------|--------|
| `Page.GetTextByColumn() (Columns, error)` | Legacy layout path; under review. Prefer `Page.Words()` / `Page.Lines()`. |
| `Page.GetTextByRow() (Rows, error)` | Same. |
| `Column`, `Columns`, `Row`, `Rows` | Types backing the above. |

These remain functional but are excluded from the freeze; a future minor
release may mark them `// Deprecated:` pointing at their replacements.

## v1.0 milestone

v1.0.0 — the full-surface freeze — is planned after the extraction-quality and
structure milestones complete and the deprecation review above is resolved.
Until then this tiered contract is the compatibility promise.
