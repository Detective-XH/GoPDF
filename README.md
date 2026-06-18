# GoPDF

[![Go CI](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml/badge.svg)](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml)
[![License: BSD 3-Clause](https://img.shields.io/badge/License-BSD_3--Clause-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/Detective-XH/gopdf.svg)](https://pkg.go.dev/github.com/Detective-XH/gopdf)
[![Go Report Card](https://goreportcard.com/badge/github.com/Detective-XH/gopdf)](https://goreportcard.com/report/github.com/Detective-XH/gopdf)

> **Deterministic, pure-Go PDF text & structure extraction for LLM/RAG and
> document-ingestion pipelines** — no CGo, no rendering, no OCR, no network.

GoPDF turns real-world PDFs into clean, inspectable structure — text, words and
lines with bounding boxes, form fields, attachments, links, metadata, fonts, and
image-draw signals — and reports, deterministically, *when* a page extracts
cleanly versus when it should be routed to OCR or flagged for review. It opens
modern encrypted files, survives malformed documents, and never silently guesses:
every degraded extraction comes with a diagnostic you can act on.

**Requires Go 1.25+** (`go.mod` directive).

## Quick start

```go
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/Detective-XH/gopdf"
)

func main() {
	f, r, err := pdf.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	text, err := r.GetPlainText(context.Background())
	if err != nil {
		panic(err)
	}
	_, _ = buf.ReadFrom(text)
	fmt.Println(buf.String())
}
```

## Contents

[Why GoPDF](#why-gopdf) · [At a glance](#at-a-glance) · [Features](#features) ·
[Install](#install) · [Examples](#examples) · [API stability](#api-stability) ·
[Limitations](#limitations) · [Accuracy](#accuracy--test-corpus) ·
[Releases](#releases--verification)

## Why GoPDF

- **Built for ingestion, not viewing.** Every API answers an ingestion question —
  what is the text, where is it on the page, how confident is the decode, does
  this page need OCR?
- **Confidence signals, not just bytes.** `Page.ExtractionSignal()` and
  `Reader.DocumentSummary()` classify each page (`text` / `image_only` / `empty` /
  `degraded`) and quantify decode quality, so a pipeline can index, route to OCR,
  or flag low-confidence pages — without parsing logs.
- **Survives the real world.** CJK and Cyrillic scripts, RC4 / AES-128 / AES-256
  encryption, hybrid cross-references, object streams, rotated and vertical-writing
  pages, and malformed PDFs are all handled with bounded, panic-safe, deterministic
  extraction.
- **Pure Go, drop-in.** No CGo and no external services — `go get` and ship. Safe
  for concurrent use after open.

## At a glance

| Need | API |
|------|-----|
| Plain text (context/cancellation aware) | `Reader.GetPlainText` |
| Styled text runs (font, size, position) | `Reader.GetStyledTexts` / `Page.Texts` |
| Words / visual lines with bounding boxes | `Page.Words` / `Page.Lines` |
| Column-major visual blocks (RAG chunking unit, experimental) | `Page.Blocks` |
| Ruled (lattice) table reconstruction (experimental) | `Page.Tables` |
| Rows / columns of text (legacy, deprecated) | `Page.Lines` / `Page.Words` (`Page.GetTextByRow` / `Page.GetTextByColumn` are deprecated) |
| Form field values (AcroForms, read-only) | `Reader.Fields` |
| Embedded file attachments | `Reader.Attachments` |
| Link annotations / document-wide links | `Page.Annotations` / `Reader.Links` |
| Named destinations / outline | `Reader.Dest` / `Reader.Outline` |
| Image draw metadata (no decoding) | `Page.Images` |
| Fonts / XMP / document info | `Reader.Fonts` / `Reader.XMP` / `Reader.Info` |
| Printed page labels (roman front matter, offsets) | `Reader.PageLabels` |
| Applied page rotation (`/Rotate`, upright display-space coords) | `Page.Rotate` |
| Page extraction readiness + warnings | `Page.ExtractionSummary` / `Reader.Warnings` |
| Extraction routing signals (text / image / empty / degraded) | `Page.ExtractionSignal` / `Reader.DocumentSummary` |
| Structured JSON debug export (PyMuPDF-dict shape, experimental) | `Page.DebugJSON` / `Reader.DebugJSON` |
| Encrypted PDFs (RC4, AES-128, AES-256) | `NewReaderEncrypted` |

## Performance

GoPDF is tuned for fast, deterministic text extraction. Extracting all text from a
22-page Traditional Chinese PDF takes ~25 ms (~870 pages/sec) on an Apple M4 Pro
(`go test -bench=BenchmarkCJKColdOpenExtract`).

In matched-scope benchmarking against common Python extractors on the same
documents, GoPDF was fastest at both plain-text and positioned-word extraction —
several times faster than pure-Python `pypdf` / `pdfminer.six` / `pdfplumber`, and
ahead of the C-backed `pdftotext` and PyMuPDF on this workload — while staying pure
Go. These are **speed** numbers only, covering text and word extraction: GoPDF does
not render pages or decode images, its ruled-table reconstruction (`Page.Tables()`) is
not part of these numbers, and its layout/word-grouping quality is a work in progress and
not benchmarked. See [BENCHMARKS.md](BENCHMARKS.md) for methodology, full tables,
and caveats.

## Background

GoPDF began as a maintained repair fork of [ledongthuc/pdf](https://github.com/ledongthuc/pdf)
(itself derived from [rsc/pdf](https://github.com/rsc/pdf)). Early releases focused on
closing long-standing extraction and robustness gaps in the upstream reader lineage —
including CJK/Cyrillic decoding, ToUnicode priority, Form XObject text omission, TJ
word-boundary handling, malformed-parser panics, AES-encrypted PDFs, and missing
regression coverage. It is now an independent project, aimed at being a dependable,
pure-Go upstream for PDF extraction and ingestion pipelines.

### Lineage and drop-in compatibility

GoPDF stays API-compatible with the call sites the lineage readers expose — the set
a `langchaingo` PDF loader uses (`NewReader`, `NewReaderEncrypted`, `NumPage`,
`Page`, `Page.Fonts`, `Page.Font`, `Page.GetPlainText`) is signature-identical and
frozen in [API-STABILITY.md](API-STABILITY.md), so adopting GoPDF from the older
readers is an import-path swap. Meanwhile the lineage packages have gone quiet and
GoPDF has closed extraction gaps still open upstream:

| | GoPDF | ledongthuc/pdf | dslipak/pdf |
|---|---|---|---|
| Maintenance | active | ~2–3 merges/yr | stalled since Jan 2024 |
| Form XObject text | extracted | open issue [#67](https://github.com/ledongthuc/pdf/issues/67) | — |
| CJK predefined CMaps (`UniGB-UCS2-H` …) | decoded | open issue [#55](https://github.com/ledongthuc/pdf/issues/55) | — |

`— = not evaluated.`

## Features

### Text and layout extraction

- Plain text extraction with context/cancellation support.
- Styled text extraction with font name, size, and position.
- Text grouped by row, plus word-level extraction with bounding boxes via `Page.Words()`.
- `Page.Blocks()` (experimental) groups lines into **column-major** visual blocks — read down each detected column in full — as the chunking unit for RAG pipelines. See [EXAMPLES.md](EXAMPLES.md).
- `Page.Content()` exposes the page's vector geometry — drawn rectangles (`Rect`) and stroked ruling lines / cell borders (`Stroke`, experimental) — in display space, the raw signal for table-grid detection without rendering. See [EXAMPLES.md](EXAMPLES.md).
- `Page.Tables()` (experimental) reconstructs **ruled (lattice) tables** — those drawn with visible cell borders — into a grid of cell strings (`Table.Cells[row][col]`), reading the ruling lines from `Content.Stroke` and thin `Content.Rect`. Reconstruction accuracy is locked by corpus regression gates on the documented scope (fully-ruled lattices plus structurally-recovered half-open edge columns); the output may still change as quality is stabilized across more table types. Borderless and partially-ruled/banded tables are out of scope — best-effort, not a contract: they yield no table or an incomplete grid. See [EXAMPLES.md](EXAMPLES.md).
- Nested **Form XObject** text is included and reported in page-space coordinates.
- TJ kerning arrays are interpreted as word gaps when spacing indicates a word boundary.
- Broad script coverage: Latin, **Cyrillic**, and CJK predefined CMaps.

### Ingestion signals and diagnostics

- `Page.ExtractionSignal()` and `Reader.DocumentSummary()` emit deterministic per-page and per-document routing signals for LLM/RAG pipelines: index text-bearing pages as-is, route image-only pages to OCR, and flag empty or degraded pages for review. `DocumentSummary` also carries `DecodeRatios` — per-page and document decode-quality ratios (missing `/ToUnicode`, charset fallback, unmapped glyphs) to re-route text that is present but unreliable.
- `Page.ExtractionSummary()` reports page-level text/image readiness: `HasText`, `WordCount`, `ImageCount`, `ImageCoverage` (image bbox area / page area — distinguishes a full-bleed scan from a thumbnail), and page-scoped warnings including `sparse_text` (a page whose only text is page furniture, e.g. a page number, so it still routes to OCR).
- `Reader.Warnings()` returns deterministic diagnostics for silently degraded extraction, including missing or broken `/ToUnicode`, fallback CJK encodings, unknown encodings, unmappable glyphs, and unsupported stream filters.
- `Page.Images()` reports image draw metadata — page-space bounds, declared dimensions, and declared filters — without decoding image content.
- `Page.DebugJSON()` and `Reader.DebugJSON()` (experimental) emit a structured JSON snapshot of the extracted text geometry, shaped like PyMuPDF's `get_text("dict")` (page → block → lines → word-spans with bounding boxes and a per-page `coord_origin`), for bbox-aware RAG chunking and citation. It is a thin projection over the stable primitives — only fields GoPDF actually computes — and carries the same in-band diagnostics (`image_only_page`, `non_finite_geometry`). See [EXAMPLES.md](EXAMPLES.md).

### Metadata and document structure

- `Reader.Info()` and `Reader.XMP()` expose classic document metadata and raw XMP packets.
- `Reader.Fonts()` lists distinct document fonts, embedded-program presence, and pages where each font appears.
- `Page.Annotations()` extracts link and text annotations; `Reader.Dest()` resolves named destinations; `Reader.Links()` aggregates document links into `LinkRef` entries.
- `Reader.Fields()` extracts AcroForm field values (text, checkbox, radio, choice) with page and bounding-box locations.
- `Reader.Attachments()` lists document-level embedded files (name, MIME type, declared size, decoded data).
- Outlines expose resolved page numbers.
- `Reader.PageLabels()` returns each page's *printed* label (roman-numeral front matter, an offset like "32", letter ranges) from the `/PageLabels` tree, so a citation can reference "page iv" rather than the 1-based position.
- `Page.MediaBox()` and `Page.CropBox()` resolve inherited page dimensions.

### Compatibility and safety

- Transparent Standard-security-handler decryption: RC4, AES-128, and AES-256, including owner-password unlocks, per-class crypt filters, cleartext metadata, and SASLprep-normalized passwords.
- Broad parser compatibility: PDF 2.0 headers, hybrid-reference files, object streams, and common stream filters including Flate, LZW, ASCII85, ASCIIHex, and RunLength.
- Resilient malformed-PDF behavior: bounded recursion/allocation, content-stream panic recovery, and deterministic best-effort extraction.
- Safe for concurrent use after open; repeated dereferencing is served from a bounded internal cache.
- `Pages() iter.Seq2[int, Page]`, `Texts() iter.Seq[Text]`, and `OpenBytes([]byte)` support streaming and in-memory workflows.

### CJK predefined CMap decoders

  - Japanese Shift-JIS (`90ms-RKSJ-H/V`, `90pv-RKSJ-H`)
  - CJK UCS-2 BE (`UniGB-UCS2-H/V`, `UniCNS-UCS2-H/V`, `UniJIS-UCS2-H/V`, `UniKS-UCS2-H/V`)
  - Simplified Chinese GBK / GB-EUC / GBKp-EUC (`GBK-EUC-H/V`, `GB-EUC-H/V`, `GBKp-EUC-H/V`)
  - Traditional Chinese Big5-ETen / ETenms (`ETen-B5-H/V`, `ETenms-B5-H/V`)
  - Korean UHC / KSC-EUC / UHC-HW (`KSCms-UHC-H/V`, `KSC-EUC-H/V`, `KSCms-UHC-HW-H/V`)

## Install

```bash
go get github.com/Detective-XH/gopdf
```

## Examples

See `examples/` for runnable programs and [EXAMPLES.md](EXAMPLES.md) for API
cookbook snippets covering words, blocks, image metadata, extraction summaries,
diagnostics, XMP, fonts, annotations, named destinations, link aggregation,
form fields, attachments, encrypted PDFs, and a langchaingo-style RAG loader.

Runnable examples:

```bash
go run ./examples/read_plain_text
go run ./examples/read_text_with_styles
go run ./examples/langchaingo_loader
```

The [Quick start](#quick-start) above is the smallest complete program; see
[EXAMPLES.md](EXAMPLES.md) for the full API cookbook.

## API stability

Exported APIs are tiered contracts — see [API-STABILITY.md](API-STABILITY.md)
for what is frozen today, what will only grow additively, and the v1.0 freeze
milestone. The geometry convention (PDF-native bottom-left, points) and the
screen-space conversion recipe are documented there too.

## Limitations

- Extraction only — no PDF creation, modification, or rendering.
- Image content is not decoded; `Page.Images()` reports draw metadata only.
- AcroForms extraction is read-only field values — no form filling or appearance rendering.
- `Reader.Attachments()` walks the document-level name tree only; page-level `/FileAttachment` annotations are not scanned.
- Table reconstruction (`Page.Tables()`, experimental) is limited to **ruled (lattice) tables** — interior cells closed by visible rules, plus structurally-recovered half-open edge columns. Borderless and partially-ruled/banded tables are out of documented scope (best-effort, not a contract — they may yield no table or an incomplete/merged grid); an earlier text-only spatial heuristic for borderless tables was evaluated against a real-document corpus and deferred for not meeting the cell-accuracy bar.
- Layout/word-position extraction (`Page.Words`, `Page.Lines`, `GetStyledTexts`) is available and fast, but its grouping **quality is a work in progress**: it is tuned for speed and determinism and has not been benchmarked for layout fidelity against dedicated layout tools (on CJK it currently segments more aggressively). Validate it on your own documents.

## Accuracy & test corpus

Extraction quality is validated against a curated corpus of real, public-domain PDFs
spanning CJK scripts (Simplified Chinese, Traditional Chinese, Japanese, Korean),
Cyrillic, multi-column layouts, and numeric tables — each with a recorded provenance
and a verified golden output locked by the test suite. Negative fixtures (`hard/`)
document current extraction gaps honestly: documents that defeat decoding today are
committed without goldens so any future improvement is caught as a regression gate,
not a silent surprise. See [`testdata/corpus/README.md`](testdata/corpus/README.md)
for the full provenance table and fixture inventory.

## Releases & Verification

Versions are published as **signed git tags** (mirrored as GitHub Releases —
Go module resolution only needs the tag). Tags are signed with one of the
maintainer's hardware-backed SSH keys; v0.8.0 was signed with:

```
256 SHA256:duCP4h22hb2oNAZMaFhUlpq0j8+qBbZuaXnS99yUhkY (ED25519-SK)
```

Verify a release tag — all of the maintainer's trusted keys are published on
the GitHub account, so the recipe below works for any release regardless of
which key signed it, and nothing needs to be copied from this README:

```bash
curl -s https://api.github.com/users/Detective-XH/ssh_signing_keys \
  | python3 -c "import json,sys; [print('*', k['key']) for k in json.load(sys.stdin)]" > allowed_signers
git -c gpg.ssh.allowedSignersFile=./allowed_signers tag -v v0.8.1
# expect: Good "git" signature for * with ED25519-SK key SHA256:duCP4h2...
```

Module integrity is independently guaranteed by the Go checksum database:

```bash
go mod download github.com/Detective-XH/gopdf@latest && go mod verify
```

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the full history of fixes and additions.
