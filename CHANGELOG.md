# Changelog

All notable changes to GoPDF are documented here (from v0.6.0 onward).
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## Fixed, pending release

### Added

- `Text.H` and `Text.Rotation` complete the per-glyph geometry on the `Text` struct
  (returned by `Page.Texts()`, `Page.Content()`, and `Reader.GetStyledTexts`).
  `Text.H` is the nominal font-box height in points — the magnitude of the text
  rendering matrix's up-vector, so it equals the font size for ordinary horizontal
  text and stays **positive and rotation-invariant** for rotated runs, where
  `FontSize` (taken from the matrix x-scale) collapses toward zero: on a 90°-rotated
  run whose `FontSize` reads 0, `H` still reports the true 12 pt. `Text.Rotation` is
  the baseline's angle in **degrees, counter-clockwise-positive** from horizontal
  (`0` for upright text) — the rendering matrix's baseline angle, distinct from and
  opposite-signed to the page `/Rotate` attribute (clockwise), which is not applied
  here. Both fields are additive (`Text` stays Stable-tier); no existing field value,
  golden, or `DebugJSON` output changes. `Word.H`/`Line.H` are unchanged (still the
  first-glyph font size); unifying them with the rotation-aware `Text.H` is left to a
  later vertical-layout change. Carrying the two new `float64` fields widens `Text`
  from 64 to 80 bytes, so the structured-extraction paths
  (`Texts`/`GetStyledTexts`/`Content`/`DebugJSON`) allocate **+8–13 % more bytes** and
  run **+2–3.5 % slower**, with the allocation *count* unchanged; `GetPlainText` is a
  separate path and is unaffected (measured same-machine A/B, Apple M4 Pro). A
  fast-path that skips the per-glyph trigonometry on horizontal text was measured and
  gave no benefit, confirming the cost is the wider struct rather than the
  computation — so the simpler unconditional form ships. See [EXAMPLES.md](EXAMPLES.md).

- Vertical writing-mode text (a `-V` predefined CMap) now advances **down the page**
  instead of horizontally. Previously such text decoded correctly but every glyph
  advanced along the x-axis (or, for composite fonts, did not advance at all), so a
  multi-glyph vertical run overprinted at a single point. The per-glyph advance and the
  `TJ` kerning adjustment now translate along the vertical axis using the PDF default
  one-em displacement, and text following a `TJ` array in the same text block is no
  longer pushed an extra line down. The `vertical_writing_mode` warning message now
  states that the advance is applied with a default-metrics approximation rather than
  ignored. Per-glyph vertical metrics (`/W2`), the glyph position vector, and vertical
  word/line grouping are not yet modelled; horizontal text, `GetPlainText`, and all
  existing field values and goldens are unchanged.

- Page `/Rotate` (the clockwise display-rotation attribute) is now **honored**, so
  extracted text and image coordinates land in the page's upright display space instead
  of the raw content space that previously ignored the rotation. A page authored sideways
  with `/Rotate` set to display upright now extracts with a horizontal baseline — the font
  size comes back, the `rotated_text` warning stops, and reading-order grouping works. A
  new `Page.Rotate() int` accessor returns the applied clockwise rotation
  (`0`/`90`/`180`/`270`); `/Rotate` is inheritable and normalized (a non-multiple of 90,
  an absent, or a non-integer value reads as 0). `Text.Rotation` now reflects the combined
  text-matrix and page rotation in display space. The change is additive and
  **golden-neutral**: an unrotated page (`/Rotate 0`) is bit-for-bit unchanged,
  `GetPlainText` is unaffected, and every corpus, `DebugJSON`, and signal golden is
  unchanged; the per-page cost is within measurement noise. See [EXAMPLES.md](EXAMPLES.md).

### Changed

- `Content().Rect` (the `re` path-construction rectangles from `Page.Content()`) now
  **honors the CTM**: each rectangle's four corners are mapped through the current
  transformation matrix and returned as their axis-aligned display-space bounding box, so
  it agrees with the `/Rotate`-honored text and image geometry on a rotated or
  `cm`-transformed page — the last mixed-frame surface after the page-`/Rotate` work. An
  unrotated page with no `cm` transform is bit-for-bit unchanged; the returned bbox is now
  always normalized (`Min <= Max`), which additionally corrects un-normalized output for a
  negative-dimension `re`. `DebugJSON` does not emit content rectangles, so its goldens are
  unaffected.

- `Word.H` and `Line.H` (and the `Block` and `DebugJSON` boxes derived from them) are
  now the **rotation-aware nominal font height** — the same basis as `Text.H` — finishing
  the unification noted when `Text.H` shipped. Previously they came from the text
  matrix's x-scale (`FontSize`), which collapses toward `0` on a 90°-rotated run, doubles
  under horizontal text scaling, and goes **negative** under a horizontal flip; they now
  report the text up-vector magnitude, which is rotation-invariant and **always
  non-negative**, so a word, line, or block reports the same nominal height as the glyphs
  in it. For ordinary horizontal text this value equals the font size exactly, so all
  existing extraction output — every corpus golden, `DebugJSON`, and `Page.Words()`/
  `Page.Lines()` result — is byte-for-byte unchanged; only rotated, flipped, or scaled
  runs (none of which were previously locked by a test) take the new value. One
  consequence on the Experimental `Page.Blocks()` API: because the corrected line height
  also feeds block grouping, the way **non-horizontal** lines group into blocks can
  change — in particular a 90°-rotated column, which used to collapse into a single block
  because its old height read `0`, can now split into separate blocks.

### Performance

- The text-extraction paths that group glyphs into lines — `Page.Words`,
  `Page.Lines`, `Page.Blocks`, `Reader.DebugJSON`, and `Page.ExtractionSummary` —
  now allocate substantially less memory. The internal y-band grouping step used to
  copy the page's glyph slice **twice** per page (a defensive copy plus per-band
  value copies); it now sorts a lightweight index permutation and references the
  original glyphs in place, leaving the page's content untouched. Output is
  byte-for-byte identical. On a 22-page Traditional-Chinese document,
  `Reader.DebugJSON` drops **−21.6 % bytes allocated** (and ~1 % fewer allocations),
  and the word/line path drops **−29–31 % bytes**. Time per operation is flat to
  slightly lower — the workload is dominated by garbage-collection pressure, so
  fewer bytes chiefly relieve GC. This more than offsets the +8–13 % bytes the new
  `Text.H`/`Text.Rotation` fields added (above).

## v0.7.8 — 2026-06-12

### Performance

- CJK and other ToUnicode-based text extraction is substantially faster and
  lighter: a font's ToUnicode character map is now decoded once per document
  instead of re-parsed on every page of every extraction pass. Repeated
  extraction calls and multi-page documents reuse the decoded map, which is
  bounded in memory and safe for concurrent use. Output is byte-identical — the
  cache only changes *when* a map is parsed, never *what* it decodes to. On a
  22-page Traditional-Chinese document, `Reader.GetPlainText` drops **−28 % time,
  −35 % bytes, −18 % allocations**; cold open-and-extract **−26 % / −31 % /
  −17 %**; and `Reader.DebugJSON` a further **−12 % / −10 % / −11 %**.

- `Reader.DebugJSON` / `Page.DebugJSON` got faster and lighter again: each page's
  words are no longer assembled twice. Building on the v0.7.7 single-interpret change,
  the structured-export path now reuses the words it already grouped into lines to
  drive its routing summary, instead of re-grouping the page from scratch. On a
  22-page CJK document `Reader.DebugJSON` drops from **67.1 to 59.7 ms/op (−11 %)**,
  **74.8 to 59.3 MB/op (−21 %)**, and allocations from **921k to 863k per call
  (−6 %)**. The output is byte-identical.

## v0.7.7 — 2026-06-11

### Added

- `Page.Blocks() ([]Block, error)` returns a page's text grouped into **column-major**
  visual blocks — the chunking unit for RAG/LLM ingestion. On a multi-column page each
  detected column is read top-to-bottom in full (unlike `Page.Lines()`, which stays
  row-major with columns interleaved by row), and consecutive lines separated by no more
  than a block-sized vertical gap merge into one `Block`; a single-column page degrades to
  gap-based paragraph grouping. The new `Block` type carries `S` (the constituent lines
  joined by `"\n"`), the bounding box (`X/Y/W/H`, bottom-left origin), the first line's
  `Font`/`FontSize` (a heading-vs-body signal), and `Lines` (top-to-bottom). A full-width
  masthead or mid-page heading spanning the gutters is assigned to its left-edge column;
  reading order around such interruptions is best-effort (visual grouping only — no
  paragraph or section semantics). Marked **Experimental**: the Go signature and field set
  are additive-stable, but the segmentation heuristic may be refined in a minor release.
  Deterministic and safe for concurrent use. See [EXAMPLES.md](EXAMPLES.md).

### Performance

- `Reader.DebugJSON` / `Page.DebugJSON` no longer interpret each page's content stream
  twice, so structured JSON export is faster and lighter: on a 22-page CJK document
  `Reader.DebugJSON` drops from **101.4 to 66.0 ms/op (−35 %)** and **100.6 to 78.5 MB/op
  (−22 %)**. The output is byte-identical.

## v0.7.6 — 2026-06-11

### Added

- `Page.DebugJSON()` and `Reader.DebugJSON()` return an **experimental** structured JSON
  snapshot of a page (or the whole document) shaped like PyMuPDF's `get_text("dict")`:
  page width/height → one text block → lines → word-level spans, each with a baseline
  `origin` and a bounding box, under a per-page `coord_origin` tag (`TOPLEFT`, top-left and
  y-down to match the PyMuPDF/RAG ecosystem; `BOTTOMLEFT` for a degenerate page box). The
  document form adds `fonts`, `links`, and `warnings`. It is a thin, deterministic
  projection for debugging and ingestion pipelines — not a converter: it emits only the
  fields GoPDF actually computes (font flags, colour, and writing-mode are omitted rather
  than reported as a misleading zero) and carries GoPDF's own routing/diagnostic warnings
  (e.g. `image_only_page`) in-band. Adversarial page, text, or link geometry that overflows to
  a non-finite coordinate (±Inf/NaN) is sanitized to `0` and flagged with a new
  `non_finite_geometry` warning (page-scoped for page/text geometry, document-scoped for links),
  so the output is always valid JSON and a zeroed value is not mistaken for real geometry at the
  origin. Both return `[]byte`; the JSON wire format is
  experimental and may change in a future minor release, while the Go signatures are stable.
  See [EXAMPLES.md](EXAMPLES.md) and the Experimental tier in
  [API-STABILITY.md](API-STABILITY.md).

### Changed

- The langchaingo loader example now consumes `DebugJSON` for a structured per-page layout
  sidecar (one layout JSON per page, index-aligned with the per-page text documents, for
  bbox-aware chunking or citation) and sets `page_label` from the document's real printed
  labels via `Reader.PageLabels()` (falling back to the 1-based page number when the document
  declares no page-label tree), matching LangChain's PyPDFLoader.

## v0.7.5 — 2026-06-11

### Added

- `Reader.PageLabels() []string` returns the document's *printed* page label for every page —
  the "iv", "A-3", or "32" a reader sees on the page rather than its 1-based position — read from
  the PDF `/PageLabels` number tree (prefix, numbering style, and per-range start value). It
  returns `nil` when the document declares no page labels, so callers cleanly fall back to the
  page number; a page left uncovered by the tree gets `""`. Best-effort and deterministic:
  malformed ranges are skipped rather than raising an error, and hostile inputs (cyclic label
  trees, an absurd start value) are bounded. The payoff for citation and RAG is referencing
  "page iv" — what the reader can actually find — instead of "page 4" when front matter is
  numbered in roman.
- `NormalizeText(string) string` folds the Latin typographic ligatures U+FB00–U+FB06
  (ﬀﬁﬂﬃﬄﬅﬆ) to their ASCII forms (`ff`, `fi`, `fl`, `ffi`, `ffl`, `st`, `st`), leaving every
  other rune unchanged. It is the targeted, deterministic alternative to blanket Unicode NFKC
  (which also rewrites `½`, superscripts, and full-width forms — usually wrong for extracted
  text): extraction output stays verbatim, and callers opt in only where they want ligatures
  folded for search or RAG. Allocation-free when the input contains no ligature.

## v0.7.4 — 2026-06-10

### Fixed

- ASCII85-encoded streams that use the `z` all-zero-group shorthand now decode
  correctly. The shorthand was previously stripped before decoding, silently dropping
  the four zero bytes it represents, so affected streams lost data. A malformed
  mid-stream `z` now surfaces a decode error instead of being silently dropped.

### Changed

- Corrected and clarified godoc for several public APIs: `Value.Int64`/`Float64` now
  name the actual kind constants (`Integer`/`Real`); `Value.Keys` documents that an
  empty dictionary returns a non-nil empty slice (versus `nil` for a non-dictionary);
  `IsSameSentence` no longer claims a sentence-ending-punctuation check it does not
  perform; `Stack.Len`/`Push`/`Pop` document their silent-drop-at-capacity and
  null-on-empty behaviour; and `Content`, `GetPlainText`, `Outline`, `Attachments`, and
  `Reader.DocumentSummary` document their empty-return, concurrency, and stability
  contracts. Content-stream panic messages for malformed `Tf`/`cm` operators were
  corrected to name the operator that actually failed.

### Performance

- CJK and other `/ToUnicode`-CMap text extraction is now substantially faster. The
  CMap glyph lookup previously scanned every mapping entry for every decoded
  character (`O(characters × entries)`), which dominated extraction time on large
  CJK fonts; it now uses a sorted binary search. On a 22-page Traditional Chinese
  document extraction is ~1.9× faster (45–48% less wall-clock time), and a CMap
  expressed mostly as character ranges can be over 20× faster. Allocated memory is
  effectively unchanged, and extracted output is byte-for-byte identical for every
  input — including malformed CMaps with overlapping ranges, which keep their
  previous decoding.
- Text extraction now allocates less, most visibly on CJK and other
  `/ToUnicode`-CMap documents. The lexer interns the fixed set of content-stream
  operators and structural delimiters (`[`, `]`, `<<`, `>>`, and the operator
  keywords), returning a shared token instead of allocating a new one on every
  occurrence. On a 22-page Traditional Chinese document this cut extraction
  allocations by ~23% and allocated memory by ~7%, with a small (~2.5%) speedup;
  extracted output is byte-for-byte unchanged.

## v0.7.3 — 2026-06-09

### Added

- A langchaingo-style PDF document loader example (`examples/langchaingo_loader`): a
  runnable adapter for Go RAG and ingestion pipelines that emits one document per
  page, each carrying the page's plain text and a stable, LangChain/LlamaIndex-aligned
  metadata key set — `page` (0-based), `page_label`, `total_pages`, lowercase document
  properties (`title`, `author`, `subject`, `creator`, `producer`, and
  `creationdate`/`moddate` as RFC3339), and `extraction_confidence` (the page's
  extraction signal `text`/`image_only`/`empty`/`degraded` — a routing signal, not a
  0–1 score). Every key is always present (a missing property is an empty string), and
  a per-page extraction failure surfaces that page with its `degraded` signal instead
  of aborting the document, so a pipeline can route just that page to OCR or review.
  The example's local `Document` type is field-compatible with langchaingo's
  `schema.Document` for the fields a loader populates, so adoption is an import swap
  and no dependency is added. The README now carries a lineage comparison table
  (GoPDF vs the inactive `ledongthuc/pdf` and `dslipak/pdf` readers) and EXAMPLES.md a
  new "Ecosystem adapters (langchaingo / RAG loaders)" section. Examples and
  documentation only — no library code, public API, or dependency change.
- Documented ligature behaviour and caller-side normalization in a new EXAMPLES.md
  section, "Ligatures and Unicode normalization". GoPDF returns text verbatim and
  applies no Unicode normalization on any extraction path
  (`GetPlainText`/`GetStyledTexts`/`Words`/`Lines`), so typographic ligatures arrive
  as Unicode compatibility codepoints (U+FB01 "ﬁ", U+FB02 "ﬂ", and U+FB00/FB03/FB04)
  rather than ASCII pairs. The section explains which decode paths carry them
  (`/ToUnicode` and `/Differences` via the Adobe Glyph List can emit all five; the
  built-in MacRoman/PDFDoc byte tables carry only fi/fl; WinAnsiEncoding none) and
  shows two caller-side folds: a targeted `strings.NewReplacer` (recommended — it
  leaves `½`, `²` untouched) and blanket `golang.org/x/text/unicode/norm` NFKC (with
  the warning that NFKC also rewrites `½`→`1⁄2`, `²`→`2`, and full-width forms, which
  is wrong for financial or scientific text). Documentation only — no API or
  behaviour change.

## v0.7.2 — 2026-06-09

### Added

- **Per-word/per-line fonts, and multi-column + CJK line reading** —
  `Page.Words()` and `Page.Lines()` now report the font name and size of each
  word and line (`Word.Font`/`Word.FontSize`, `Line.Font`/`Line.FontSize`; for a
  word or line that mixes fonts or sizes the first glyph/word wins). `Page.Lines()`
  no longer glues side-by-side columns into one line: on a multi-column page a
  line is split per column wherever a recurring column gutter separates the text,
  while a full-width masthead or heading that flows across the page stays a single
  line. Within a line, runs of a space-less CJK script (Chinese, and Japanese
  kana/kanji) rejoin without the spurious per-glyph spaces a page can introduce
  (`联 合 国 大 会` → `联合国大会`), while Korean keeps its real inter-word spaces.
  Line grouping is deterministic across runs and platforms. Reading order is a
  bounded per-column split, not full column-major ordering (columns are still
  interleaved row by row); `Page.Words()` and `Page.Texts()` are unchanged.
  EXAMPLES.md and API-STABILITY.md document the new fields.

- **Decode-quality ratios** — `Reader.DocumentSummary()` now reports `DecodeRatios`,
  per-page (on each `PageSignal`) and rolled up across the document. Each measures
  what fraction of a page's decoded glyphs came through a lower-confidence decode
  path — text with no usable `/ToUnicode` (`MissingToUnicodeRatio`), a predefined-CMap
  charset approximation (`FallbackRatio`), or glyphs that could not be mapped at all
  (`UnmappedRatio`, the U+FFFD share) — over the shared `Glyphs` denominator. They let
  an ingestion pipeline re-route text that is *present but unreliable*: text the
  routing signal alone would classify as clean. The fields are stable facts, not a
  score — you set your own thresholds — and the three ratios overlap (a U+FFFD glyph
  is also counted in its decode-source bucket), so they are thresholded independently,
  never summed. The document rollup is glyph-count weighted, not a mean of per-page
  ratios, and only text-bearing pages contribute. The values are computed from the
  same extraction pass at no extra cost, never read the warning store, and are fully
  deterministic and concurrency-safe. The `ExtractionSignal` enum is unchanged.
  EXAMPLES.md, API-STABILITY.md, and README document the new struct and fields.

- **Image-only / scanned-page classifier v2** — three new OCR-routing signals,
  all without decoding image streams. `Page.ExtractionSummary()` now reports
  `ImageCoverage`, the fraction of the page area covered by drawn image bounding
  boxes (clamped to `[0,1]`): a value near `1.0` is a full-bleed scan, a small
  value an incidental thumbnail or logo — the case the previous binary classifier
  could not tell apart. A new page-scoped `sparse_text` warning flags a
  text-bearing page whose entire text layer is page furniture (a few short
  page-number-like tokens at the top or bottom margin), so a scan carrying only a
  stray page number still routes to OCR instead of being indexed as clean text;
  it recognises Unicode decimal digits, so fullwidth and Arabic-Indic page numbers
  are caught while letters of any script (including a lone CJK glyph) are not.
  Inline image (`BI`) `/W`/`/H` dimensions are now captured into
  `ImageRef.DeclaredWidth`/`DeclaredHeight` (previously always zero for inline
  images). The coverage signal is computed without retaining per-image data, so it
  stays O(1) in memory even on documents that draw an image many times. The
  `ExtractionSignal` enum is unchanged. EXAMPLES.md, API-STABILITY.md, and README
  document the new field and warning.

### Deprecated

- The legacy `Page.GetTextByRow()` / `Page.GetTextByColumn()` methods (and their
  `Row`, `Rows`, `Column`, `Columns` result types) are now marked deprecated. Use
  `Page.Lines()` for column-aware visual lines and `Page.Words()` for per-word reading
  order instead — both carry per-word font name and size and feed the extraction
  quality signals (`Page.ExtractionSignal()` and the `DocumentSummary` decode ratios)
  that the legacy methods, built on a separate text interpreter, do not. The deprecated
  methods remain fully functional and are not scheduled for removal before a future
  `/v2` module path; only their godoc and the API-stability contract changed.

### Tests

- **`Page.Lines()` reading-order characterization** — `Page.Lines()` now has
  corpus-level test coverage over the real multicolumn (Federal Register) and CJK
  (UDHR Japanese / Simplified Chinese / Korean) fixtures, where it previously had
  none. The tests lock today's behaviour so future reading-order work changes it
  deliberately: a per-page invariant that `Lines()` neither drops nor invents a glyph
  versus the page content, and sentinels for the current multi-column line merging
  (text from physically separate columns is currently joined into one visual line) and
  CJK intra-line spacing (Simplified-Chinese and Korean runs are currently split into
  space-separated glyphs/syllables; Japanese stays contiguous). No public API or
  extraction behaviour changed — this is characterization only, documenting known
  reading-order limitations ahead of the stabilisation work that will address them.

---

## v0.7.1 — 2026-06-08

### Added

- [`API-STABILITY.md`](API-STABILITY.md): a tiered API stability contract.
  The Stable tier freezes today's extraction surface (no signature changes,
  no removals — additive struct fields only); the Additive-evolving tier
  pre-announces upcoming field additions (quality signals, per-word font
  info); the Deprecation-review tier flags `GetTextByColumn`/`GetTextByRow`.
  Also documents the PDF-native coordinate system (baseline vs box semantics
  per type, with a screen-space conversion recipe), drop-in compatibility
  with the `ledongthuc/pdf` lineage call sites, and the v1.0 freeze
  milestone. README links it from the new "API stability" section.
- **Extraction routing signals** — `Page.ExtractionSignal()` returns a per-page
  routing classification (`text` / `image_only` / `empty` / `degraded`), and
  `Reader.DocumentSummary()` rolls the per-page signals up to the document level
  (per-signal page counts plus document-scoped encoder/filter warnings). The
  signal is a deterministic index / send-to-OCR / flag hint for ingestion
  pipelines, derived only from existing extraction diagnostics — no OCR, no
  rendering. It uses the strict `GetPlainText` path as the text authority, so a
  truncated content stream is reported as `degraded` rather than a silent
  success; the classification and counts never read the warning store, so they
  are fully deterministic and safe for concurrent use. EXAMPLES.md and
  API-STABILITY.md document the new APIs.
- **Decode-path diagnostics** — extraction now emits two new document-scoped
  warnings that flag text whose layout geometry is unreliable: `rotated_text`
  (a text run with a rotated, non-horizontal baseline — a synthetic-italic
  slant, which keeps a horizontal baseline, is deliberately not flagged) and
  `vertical_writing_mode` (a vertical `-V` CMap whose glyph advances are not
  honoured). Internally, every decoded glyph is now attributed to its decode
  path (parsed `/ToUnicode`, charset fallback, encoding dictionary, …) and
  unmapped (U+FFFD) glyphs are counted per page, consistently across the
  `Words`/`Lines`/`Texts` and `GetPlainText` paths — groundwork for upcoming
  per-page extraction-confidence ratios. No public type or function signature
  changed.

### Security

- **Security:** `decryptAES` now validates the AES key length (16 / 24 / 32
  bytes) at the function boundary. Behaviour is unchanged — an invalid key
  length was already rejected downstream — but the contract is now explicit at
  the entry point, surfaced during a security audit.

### Fixed

- **`Reader.Fonts()` now uses the hardened page iterator** — document-level
  font inventory inherits the same malformed page-tree handling as other
  document-wide extraction APIs. Overstated page counts now emit the standard
  `null_page_slot` warning and stop after a long run of null slots instead of
  walking the raw page count directly, while reported font page numbers remain
  1-based.
- **`Page.GetTextByColumn()` / `Page.GetTextByRow()` ordering is now stable** —
  the result slices are sorted with a stable sort, so the column/row ordering is
  deterministic across runs and platforms. Output is byte-identical to before;
  the change makes the determinism guarantee explicit and robust.
- **`StandardEncoding` fonts now decode correctly** — a font declaring
  `/Encoding /StandardEncoding` by name previously fell back to PDFDocEncoding and
  emitted a spurious `unsupported_encoding` warning. It is now recognised like
  `WinAnsiEncoding` / `MacRomanEncoding`, decoding the StandardEncoding curly
  single quotes (`0x27` → `’`, `0x60` → `‘`); the `/BaseEncoding /StandardEncoding`
  dictionary form is recognised the same way. (Other upper-range StandardEncoding
  glyphs continue to map through PDFDocEncoding for now.)

## v0.7.0 — 2026-06-07

Milestone release: the extraction-ready-structure scope is complete — words, lines,
annotations, links, images, fonts, XMP, diagnostics, page summaries, modern encryption,
and now AcroForm fields and embedded-file attachments.

### Added

- **`Reader.Fields()` — read-only AcroForm extraction** — `Reader.Fields() ([]FormField, error)` returns every terminal form field in the document, in `/Fields` array order (depth-first). Each `FormField` reports the fully qualified name (`parent.child.leaf` via `/T`), the classified type (text, checkbox, radio, combo, list; pushbuttons and signatures map to `FieldOther`), the decoded value (`/V` as UTF-8 text; checkbox/radio on-state name with absent `/V` reported as `"Off"`; multi-select choice arrays joined with `", "`), the ReadOnly flag, the widget bounding rectangle, and the 1-based page number of the field's widget (0 when unknown). `/FT`, `/Ff`, and `/V` honor field-tree inheritance through the `/Parent` chain (ISO 32000-1 §12.7.3.1); merged field+widget dictionaries and multi-widget radio groups each yield one entry. Page attribution resolves through a per-call page-annotation map with a `/P` back-reference fallback. The walk is bounded by the package's standard depth cap and visited-set cycle guard; encrypted documents work transparently. Returns `(nil, nil)` for PDFs without `/AcroForm`. Safe for concurrent use with per-call transient state only.

- **`Reader.Attachments()` — embedded-file listing** — `Reader.Attachments() ([]Attachment, error)` returns all files embedded at the document level via the `/Names /EmbeddedFiles` name tree, in tree order. Each `Attachment` reports the filename (`/UF` preferred, `/F` fallback, then the name-tree key, with PDFDocEncoding/UTF-16BE decoding), the MIME type (`/Subtype` of the embedded stream, with `#XX` name escapes decoded), the declared uncompressed size (`/Params /Size`, 0 if absent), and a `Data()` thunk returning a fresh decoded (decompressed, decrypted) `io.ReadCloser` per call. Filespec entries without an embedded stream (external file references) are skipped. The tree walk carries the same depth cap and cycle guard as the named-destination walker. Returns `(nil, nil)` for documents with no embedded files. Page-level `/FileAttachment` annotations are out of scope for this release.

## v0.6.12 — 2026-06-07

### Added

- **`Page.Lines()` — visual line grouping** — `Page.Lines() ([]Line, error)` returns the text lines on a page in reading order (top-to-bottom, left-to-right). Each `Line` groups words that share a y-band, using the same criterion as `Page.Words()`: a new line starts when the Y-distance from the band anchor exceeds `max(fontSize×0.5, 1)` points. `Line.S` joins constituent words with a single space; `Line.X/Y` is the bottom-left corner in PDF coordinate space (Y increases upward); `Line.W/H` is the bounding box spanning all words on the line, including mixed-baseline glyphs. Returns `(nil, nil)` for pages with no extractable text; content-parse panics are recovered as errors, matching `Words()` semantics.

- **`Reader.Links()` — document-level link aggregation** — `Reader.Links() ([]LinkRef, error)` returns one `LinkRef` per `/Link` annotation across the document, in document order (ascending page number, `/Annots` array order within a page). Each `LinkRef` reports the source page (`FromPage`), the annotation rectangle in PDF coordinate space, the external target (`URI`) for URI actions, and the resolved 1-based target page (`ToPage`) for internal GoTo destinations, including named destinations resolved through the `/Names/Dests` name tree. Links whose action kind is unsupported (e.g. remote GoToR or Launch) are still reported with `URI` empty and `ToPage` zero, so no link is silently hidden. Returns `(nil, nil)` for documents without link annotations. Pages are visited with the same bounded null-slot handling as `Pages()`, so a malformed page count cannot force an unbounded scan; the result matches filtering `Page.Annotations()` page-by-page while building the page-number lookup only once per call.

### Fixed

- **`Page.Words()` mixed-baseline bounding boxes corrected** — when glyphs at different Y positions (e.g. a subscript or superscript) merge into one word, `Word.Y` is now the minimum baseline across all constituent glyphs and `Word.H` is the full vertical span. Previously, `Word.Y` was taken from the first glyph only and `Word.H` from its font size alone, silently excluding any glyph at a shifted baseline from the bounding box.

- **Security:** PS interpreter stacks in `ps.go` are now bounded. The dict stack is capped at 1 000 levels; the value stack is capped at 200 000 entries. Before this fix, a crafted ToUnicode CMap stream could exhaust memory via unbounded stack growth. No public API changed.

## v0.6.11 — 2026-06-06

### Added

- **`Page.Images()` — image draw metadata without decoding** — returns one `ImageRef` per image draw operation, including Image XObjects and inline images. Each ref reports the page-space bounding box after the active CTM, the primary declared XObject filter, and declared image width/height when available. The scanner recurses through Form XObjects with the existing depth cap, preserves partial refs when malformed content fails after a draw, and never decompresses or decodes image streams. `Page.ExtractionSummary().ImageCount` now shares the same metadata-only scanner, so image counts remain available when later text extraction fails.

### Fixed

- Inline image scanning now requires a real `EI` terminator before counting the draw operation, so unterminated `BI ... ID` payloads and false `EI` byte sequences inside payload data are not misclassified as complete images.

---

## v0.6.10 — 2026-06-06

### Added

- **Encryption fixture gap closure** — three new encrypted fixture files (rc4-r4-cfm-v2.pdf, aes256-r5.pdf, aes256-r6.pdf) extend the password-verification test matrix to cover RC4 in V=4 crypt filters and AES-256 R=5/R=6 modes. Verified against qpdf 12.x and Ghostscript.
- **`Page.ExtractionSummary()` — per-page extraction signals** — returns ingestion-ready fields (HasText, WordCount, ImageCount, page-scoped Warnings) without OCR or image decoding. Image-only page classification requires drawn evidence (Image XObject or inline BI..EI image pair) plus an error-free plain-text confirmation pass. Two new warning codes: `image_only_page` (page-scoped, emitted only by the summary) and `null_page_slot` (reader-level, emitted when Pages() skips a null page-tree slot). Page numbers resolve through a lazily cached page map (summary-only; metadata APIs keep transient builds).

---

## v0.6.9 — 2026-06-06

### Added

- **`Reader.Warnings()` — extraction diagnostics** — returns deterministic, deduplicated warnings for extraction problems that previously degraded silently: a missing or unparseable `/ToUnicode` CMap (including `Identity-H/V` CID fonts whose output is not real Unicode), CJK text decoded through an approximate charset fallback rather than the font's CMap program, unknown or unexpected `/Encoding` values, unmappable `/Differences` glyph names, font resources missing from a page, and unsupported stream filters (such as `/Crypt`) that silently empty a page's text. Each warning carries a stable code, a fixed human-readable message, and a bounded detail string; results are sorted, safe for concurrent use, and identical for the same operations regardless of page order or repetition — so indexing/RAG pipelines get confidence signals without parsing logs. Storage is bounded against adversarial documents (4096 entries with a `warnings_truncated` sentinel; detail strings are size-clamped).

### Changed

- **`DebugOn` documentation** now points to `Reader.Warnings` for programmatic diagnostics; a stray unconditional debug print in dictionary parsing now respects `DebugOn`.

---

## v0.6.8 — 2026-06-06

### Added

- **`Reader.XMP()` — raw XMP metadata** — returns the catalog's `/Metadata` stream as stored: typically a UTF-8 XMP packet with Dublin Core and custom-namespace fields that the classic `/Info` dictionary cannot carry. The bytes are returned **without validation** — parse them with standard XML tooling. Returns `(nil, nil)` when the catalog has no `/Metadata` entry or the stream is empty, and an **error** (rather than silently truncated data) when a metadata stream exceeds the library's 256 MiB decompression bound. Works transparently on encrypted documents, including `/EncryptMetadata false` files whose metadata is stored in cleartext.
- **`Reader.Fonts()` — document-level font inventory** — returns every distinct font referenced by the document's pages as a `FontInfo` (BaseFont name, top-level subtype, whether an embedded font program is present, and the 1-based page numbers where it appears). Resources inherited from ancestor page-tree nodes are included, and a font is reported as embedded when any instance of that name in the document carries a font program. Useful for pre-press auditing, accessibility checks, and extraction debugging. Fonts used only inside Form XObject or annotation appearance streams are not listed.

---

## v0.6.7 — 2026-06-06

### Added

- **Concurrent use of a Reader is documented and safe** — after `Open`/`NewReader` returns, the methods of `Reader` (and of the `Value`, `Page`, and `Outline` trees it produces) are safe for concurrent use by multiple goroutines, so pages of one document can be extracted in parallel from a single Reader. Post-open state is read-only except for an internal bounded cache, which synchronizes itself.
- **Per-class crypt filters for encrypted PDFs** — encrypted files whose stream and string classes use different crypt filters (`StmF ≠ StrF`), the pass-through `/Identity` filter, and the RC4 crypt filter inside V=4 encryption (`/CFM /V2`, common in Acrobat 6-era files) now open and decrypt; all three configurations were previously rejected as unsupported. Malformed crypt-filter entries fail closed rather than silently passing encrypted data through undecrypted.
- **Cleartext-metadata encrypted PDFs (`/EncryptMetadata false`)** — files encrypted with "don't encrypt metadata" (qpdf `--cleartext-metadata`, Acrobat's equivalent option) previously failed to open with AES-128 (wrong key derivation) and returned corrupted XMP metadata in every other mode. The key derivation now accounts for the flag and the metadata stream is returned verbatim. Verified against qpdf-generated AES-128 and AES-256 files with user, owner, and wrong passwords.
- **LZWDecode stream filter** — PDFs compressed with LZW (common in pre-Flate-era documents) previously failed with `unsupported PDF filter`; both `/EarlyChange` conventions now decode, verified against the Go standard library and qpdf as independent references.
- **Full predictor support for FlateDecode and LZWDecode** — all PNG row predictors (None/Sub/Up/Average/Paeth, with `/Colors` and `/BitsPerComponent` honored) and TIFF horizontal differencing now decode. Previously only PNG "Up" rows were accepted, and streams that legally mix row types — which many encoders emit — were rejected as malformed.
- **SASLprep password normalization for AES-256 PDFs** — passwords containing ligatures, combining accents, or exotic spaces now derive the correct key regardless of how the platform encoded the input (NFKC normalization plus the RFC 4013 character mappings). Previously only the literal byte sequence matched.
- **RunLengthDecode and ASCIIHexDecode stream filters** — PDFs whose content streams use either filter previously failed with `unsupported PDF filter`; both now decode per the spec's end-of-data and padding rules, so text extraction works on these files.
- **PDF 2.0 files open** — the `%PDF-2.0` header (ISO 32000-2) is now accepted; such files previously failed with `not a PDF file: invalid header`. Versions 1.0–1.7 behave exactly as before.
- **Hybrid-reference files** — PDFs written for backward compatibility with pre-1.5 readers carry a supplemental cross-reference stream (`/XRefStm`) alongside the classic xref table. It is now read, so objects stored in object streams — previously hidden and silently resolved to null in such files — extract correctly.
- **Owner password unlocks legacy encrypted files** — RC4- and AES-128-encrypted PDFs (encryption V≤4) now open with the owner password as well as the user password, matching the existing AES-256 behavior. Verified against Ghostscript-generated encrypted files covering 40- and 128-bit RC4, and a qpdf-generated AES-128 file covering the V=4 path end-to-end.

### Changed

- **Object resolution is cached** — the Reader now memoizes resolved objects and decoded object streams in a bounded internal cache (entry and byte caps; entries are evicted, never invalidated, since a PDF is immutable while open). Repeated extraction from the same Reader runs up to 16× faster with ~95% less allocation, and workloads that dereference every object in the file (forms, attachments, whole-document dumps) decode each object stream once instead of once per resident object — 3–4× faster with up to 86% less allocation on object-stream-heavy files. Single-pass text extraction is unchanged (within ±2%). Truncated or corrupt object streams are never cached, so malformed-file behavior is byte-identical to before.

---

## v0.6.6 — 2026-06-05

### Added

- **`Page.Words()`** — extract a page's text as individual words with tight bounding boxes, in reading order (left-to-right, top-to-bottom). Each `Word` carries its text plus an (X, Y, width, height) box in PDF coordinate space. Words split on spaces and inter-glyph gaps, so sub/superscripts and words that span kerning boundaries are handled correctly. Useful for search-result highlighting, RAG chunking, and layout-aware extraction.
- **`Page.Annotations()` and `Reader.Dest()`** — read a page's annotations and resolve named destinations. `Page.Annotations()` returns every annotation on a page as a structured `Annotation` value carrying its type, rectangle, link URI, GoTo target page, and `/Contents` text: `/Link` annotations expose their URI-action target or resolve an internal GoTo jump (an explicit destination array, a `/Names/Dests` name-tree entry, or a direct `/Dest`) to a 1-based page number, while `/Text` notes expose their comment body. `Reader.Dest(name)` resolves a named destination to a 1-based page number, returning the new `ErrDestNotFound` sentinel when the name is absent. Useful for crawling hyperlinks, extracting citation URLs, and following table-of-contents jumps. (Name-object destinations and the legacy catalog `/Dests` dictionary are not yet resolved.)

### Security

- **Security:** opening a malformed PDF can no longer hang the process or exhaust memory at open time. A crafted cross-reference table or stream with a cyclic `/Prev` chain previously looped forever, and an xref stream with an oversized `/W` array (e.g. `[1e9, 1e9, 1e9]`) could trigger a multi-gigabyte allocation; both are now rejected and `OpenBytes` / `NewReader` returns a clean error.
- **Security:** reading pages, inherited page attributes (e.g. `MediaBox`, `Resources`), the document outline, or compressed objects from a malformed PDF can no longer hang or crash the process. Cyclic or pathologically deep page-tree (`/Kids`), `/Parent`, object-stream (`/Extends`), and outline (`/First`, `/Next`) chains are now depth-bounded and degrade to a best-effort result instead of looping forever or overflowing the stack.
- **Security:** reading a malformed PDF through a public getter — a page (`Reader.Page`), the page count (`Reader.NumPage`), the trailer, the outline, or a page's fonts (`Page.Fonts`/`Font`) — can no longer crash the process. Previously a single corrupt or unparseable indirect-object body let a parser panic escape these getters; such a body now degrades to a null/empty result, while a structurally broken file still fails `OpenBytes` / `NewReader` exactly as before. A composite `Value` taken from an `Interpret` callback also no longer crashes when `.Key` / `.Index` follows an indirect reference through it.
- **Security:** a successfully-opened malformed PDF can no longer cause a CPU hang during post-open extraction via an inflated count or a cyclic object-stream chain. An object stream with an oversized `/N` entry count, or a page tree whose root `/Pages /Count` is wildly inflated (e.g. near `2^63`), previously drove an effectively unbounded loop when resolving a compressed object or building the page map (reading the outline, page count, or full text). Both counts are now bounded — the object-stream index scan is confined to the index section (so an over-claimed `/N` cannot tokenize object-body bytes or false-match an id from them), a cyclic `/Extends` object-stream chain is detected instead of re-scanned, and the page-count loops are clamped and skip past isolated broken nodes — so extraction stays a bounded best-effort result.
- **Security:** the object-stream index scan no longer resolves the wrong object via integer narrowing. The lookup compared `uint32(id)` against the requested object number, so a crafted index entry whose signed or oversized id narrows to the target (for example `-4294967289`, which narrows to object 7) could resolve an object the index never named and return attacker-selected bytes. The lookup now matches only an in-range, non-narrowing id and rejects negative or overflowing entry offsets.

### Changed

- Text extraction from CJK PDFs — and any document whose fonts use a `/ToUnicode` CMap — is now dramatically faster and far lighter on memory. A font's character map is parsed once per font instead of being re-parsed on every text-font (`Tf`) operator; on a 22-page Traditional Chinese document this cut extraction time by ~19×, memory use by ~51×, and allocations by ~29×.
- **Text inside Form XObjects now reports page-space coordinates.** `Content()` and `Words()` previously interpreted a Form XObject with the identity transform, so its text came back at form-local coordinates — ignoring both the form's `/Matrix` and the transform in effect where the form was invoked (`Do`). Those are now concatenated, so the `X`, `Y`, and `FontSize` of XObject text reflect its real position on the page. This is a correctness fix, but code that depended on the previous form-local values will see different coordinates for text drawn inside forms. (`GetTextByRow` / `GetTextByColumn` use a separate code path that does not track coordinate transforms, so their XObject coordinates are unchanged.)

### Fixed

- Text drawn after a `Q` (restore-graphics-state) operator is now decoded with the correct font's encoding. The interpreter restored the current font on `Q` but not its text encoder, so when a `q … Tf … Q` block changed the font and the following text relied on the restored outer font, `Content()` / `Texts` / `GetStyledTexts` / `Words` decoded those characters through the inner block's encoder and returned garbled `Text.S` (while `Text.Font` still named the correct outer font). The encoder is now saved and restored together with the font.

---

## v0.6.5 — 2026-06-04

### Added

- **AES-256 encryption** — GoPDF now opens PDFs encrypted with the modern AES-256 Standard security handler (encryption V=5, revisions R=5 and R=6, as produced by Acrobat 9+ and required by PDF 2.0 / ISO 32000-2). Such files are decrypted transparently through `OpenBytes` / `NewReaderEncrypted` using the empty, user, or owner password — no new API. Previously they failed with `unsupported PDF: encryption version V=5`.

### Security

- **Security:** `applyFilter` no longer panics on a negative FlateDecode `/Columns` value — a crafted `/Columns -2` previously triggered an invalid slice allocation when the stream was read; negative values are now rejected.
- **Security:** AES-CBC decryption now fully validates PKCS#7 padding (every padding byte, in constant time) instead of checking only the final byte, rejecting malformed padding that was previously accepted as valid.
- **Security:** AES stream decryption now bounds its in-memory read and returns an explicit error on an oversized stream instead of silently truncating it, preventing unbounded memory use on crafted PDFs.
- **Security:** CMap parser no longer panics on a codespace range entry with a key longer than 4 bytes — the malformed entry is now rejected and parsing stops gracefully instead of indexing past the end of an internal fixed-size array.
- **Security:** CMap parser no longer panics when a bfrange destination is an empty string (`<>`) — affected character codes now map to a replacement rune instead of triggering an out-of-bounds slice index.
- **Security:** `Content()` no longer panics on malformed PDF content streams — operator calls with wrong argument counts (e.g. a `Td` with one operand instead of two) are now caught and return whatever text and rectangles were extracted before the fault, preventing denial-of-service via crafted PDFs.
- **Security:** `Content()` no longer panics when a `Q` (restore graphics state) operator appears with no matching `q` (save) — the unmatched restore is silently skipped and parsing continues, so subsequent content in the same stream is still extracted.

### Fixed

- `GetTextByRow` / `GetTextByColumn` no longer panic when a PDF content stream contains a `Tm` (text-matrix) operator with fewer than 6 operands — the malformed operator is now silently skipped and extraction continues.

### Changed

- `Text.S`, `Content`, `Content()`, `GetPlainText`, `GetTextByColumn`, and `GetTextByRow` now document that extracted text is returned as verbatim UTF-8 with no escaping applied — callers are responsible for escaping at their output sink before embedding in HTML, shell commands, or any other context-sensitive environment (e.g. `html.EscapeString` for HTML output).

---

## v0.6.4 — 2026-06-03

### Security

- **Security:** Password verification in `verifyEncryptKey` now uses constant-time comparison — eliminates a timing side-channel that could have allowed password oracle attacks.
- Per-object encryption key is now correctly truncated per PDF spec §7.6.2 — decryption no longer produces garbage output for PDFs encrypted with keys shorter than 128 bits (40-bit or 64-bit RC4).
- `decryptString` now strips PKCS7 padding after AES-CBC decryption — previously every AES-encrypted string had 1–16 trailing garbage bytes, silently corrupting dict lookups and string comparisons.
- `decryptStream` AES branch now handles PKCS7 padding correctly and returns an error on cipher failure instead of silently passing raw encrypted bytes to callers.

### Fixed

- `GetPlainText` no longer errors on PDFs that use the `"` (double-quote) text-show operator — the operator previously panicked or emitted a word-spacing number instead of the text string.

---

## v0.6.3 — 2026-06-02

### Changed
- Renamed project from `pdf` to **GoPDF** — module path is now `github.com/Detective-XH/gopdf`; update your import paths accordingly. The package name remains `pdf`.

### Fixed
- TJ kerning arrays now correctly insert word spaces when the kern gap indicates a word boundary, fixing silent word concatenation in `Content()`, `GetStyledTexts()`, `GetTextByRow()`, and `GetTextByColumn()`.

---

## v0.6.2 — 2026-06-02

### Changed
- `encoderForCMapName` switch replaced with a package-level lookup map — all 30 CMap name keys preserved, no behaviour change.
- `gofmt -s` applied across the codebase.

---

## v0.6.1 — 2026-06-02

### Added
- `Pages() iter.Seq2[int, Page]` and `Texts() iter.Seq[Text]` — lazy range-over-func iterators for streaming page and text access (Go 1.23+).
- `Page.MediaBox()` and `Page.CropBox()` — page dimension accessors; `CropBox` falls back to `MediaBox` when absent.

### Fixed
- **Security:** CMap parser hardened against malformed input — panics converted to recoverable errors; entry count capped at 65,536 to prevent resource exhaustion.
- **Security:** Cross-reference stream `Size` field is now bounds-checked before allocation — prevents memory exhaustion from crafted PDFs.
- **Security:** FlateDecode streams limited to 256 MB — prevents decompression bombs.
- **Security:** AES decryption block alignment validated before decoding — prevents panic on malformed ciphertext.
- **Security:** Cross-reference object numbers capped at the PDF spec maximum (8,388,607).
- **Security:** Maximum object nesting depth enforced to prevent stack overflow.
- Form XObject text extraction — content inside Form XObjects is now included in all extraction paths ([#67](https://github.com/ledongthuc/pdf/issues/67), [#26](https://github.com/ledongthuc/pdf/issues/26)).

### Internal
- Large-scale file decomposition (god-file split of `lex.go`, `xref.go`, `value.go`, `read.go`, `page.go`, `cmap.go` into focused units) and cyclomatic-complexity reduction across the parser; Go idiom modernization. No behaviour change.

---

## v0.6.0 — 2026-06-02

### Added
- CJK text extraction: Shift-JIS, UCS-2 BE, GBK / GB-EUC / GBKp-EUC, Big5-ETen / ETenms, UHC / KSC-EUC / UHC-HW CMap decoders.
- Document metadata API (`r.Info()`) — title, author, creation date, modification date, and other `/Info` fields.
- Outline page numbers (`Outline.Page`) resolved to 1-based page numbers.
- Context/cancellation support on `GetPlainText` and `GetStyledTexts`.

### Fixed
- Per-page font map in `GetPlainText` — stale encoder reuse across pages with the same font name is fixed ([#60](https://github.com/ledongthuc/pdf/issues/60)).
- Inline image crash and CPU spin — PDFs with inline images no longer hang or crash ([#57](https://github.com/ledongthuc/pdf/issues/57)).
- Text position tracking — `GetTextByRow` / `GetTextByColumn` X/Y coordinates were always 0; `Td`, `TD`, `T*`, `TL`, and `BT` operators are now tracked correctly ([#18](https://github.com/ledongthuc/pdf/issues/18), [#27](https://github.com/ledongthuc/pdf/issues/27)).
- `%%EOF` search window expanded with a fallback backward scan — PDFs with appended digital-signature blocks now parse correctly ([#20](https://github.com/ledongthuc/pdf/issues/20)).
- Spurious `\n` from `BT` operator in `GetPlainText` removed ([#48](https://github.com/ledongthuc/pdf/issues/48)).
- Text sort stability — equal-Y glyphs preserve document stream order ([#16](https://github.com/ledongthuc/pdf/issues/16)).
- PDF header accepts space or tab after the version string — unblocks output from tools such as libtiff/tiff2pdf ([#22](https://github.com/ledongthuc/pdf/issues/22)).
- Zero-length zlib streams no longer trigger decompression errors.
- `ToUnicode` CMap now checked before `Encoding` in font decoder selection — fixes garbled text on some PDFs.
- Chinese and Korean CJK decoding: GBK, Big5, UniGB-UCS2-H, UniCNS-UCS2-H, UniKS-UCS2-H now decode correctly ([#44](https://github.com/ledongthuc/pdf/issues/44), [#55](https://github.com/ledongthuc/pdf/issues/55), [#21](https://github.com/ledongthuc/pdf/issues/21)).
- CJK crash on mixed CJK/Latin PDFs — `dictEncoder` rewritten to handle the collision ([#30](https://github.com/ledongthuc/pdf/issues/30)).
- `GetTextByRow` no longer returns disordered or empty rows ([#16](https://github.com/ledongthuc/pdf/issues/16), [#27](https://github.com/ledongthuc/pdf/issues/27)).
- Multi-stream arrays use `io.MultiReader` — prevents out-of-memory on large PDFs.

> `OpenBytes([]byte)` (in-memory PDF parsing) shipped in **v0.5.0**, before this changelog's coverage begins.
