# GoPDF

[![Go CI](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml/badge.svg)](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml)
[![License: BSD 3-Clause](https://img.shields.io/badge/License-BSD_3--Clause-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/Detective-XH/gopdf.svg)](https://pkg.go.dev/github.com/Detective-XH/gopdf)
[![Go Report Card](https://goreportcard.com/badge/github.com/Detective-XH/gopdf)](https://goreportcard.com/report/github.com/Detective-XH/gopdf)

A Go-native PDF extraction toolkit for real-world documents: text, layout,
metadata, diagnostics, links, fonts, image draw signals, encrypted files, and
malformed-PDF resilience.

**Requires Go 1.25+** (`go.mod` directive).

## Background

GoPDF began as a maintained repair fork of [ledongthuc/pdf](https://github.com/ledongthuc/pdf)
(itself derived from [rsc/pdf](https://github.com/rsc/pdf)). Early releases focused on
closing long-standing extraction and robustness gaps in the upstream reader lineage —
including CJK/Cyrillic decoding, ToUnicode priority, Form XObject text omission, TJ
word-boundary handling, malformed-parser panics, AES-encrypted PDFs, and missing
regression coverage. It is now an independent project, aimed at being a dependable,
pure-Go upstream for PDF extraction and ingestion pipelines.

## Features

### Text and layout extraction

- Plain text extraction with context/cancellation support.
- Styled text extraction with font name, size, and position.
- Text grouped by row, plus word-level extraction with bounding boxes via `Page.Words()`.
- Nested **Form XObject** text is included and reported in page-space coordinates.
- TJ kerning arrays are interpreted as word gaps when spacing indicates a word boundary.
- Broad script coverage: Latin, **Cyrillic**, and CJK predefined CMaps.

### Ingestion signals and diagnostics

- `Page.ExtractionSummary()` reports page-level text/image readiness: `HasText`, `WordCount`, `ImageCount`, and page-scoped warnings.
- `Reader.Warnings()` returns deterministic diagnostics for silently degraded extraction, including missing or broken `/ToUnicode`, fallback CJK encodings, unknown encodings, unmappable glyphs, and unsupported stream filters.
- `Page.Images()` reports image draw metadata — page-space bounds, declared dimensions, and declared filters — without decoding image content.

### Metadata and document structure

- `Reader.Info()` and `Reader.XMP()` expose classic document metadata and raw XMP packets.
- `Reader.Fonts()` lists distinct document fonts, embedded-program presence, and pages where each font appears.
- `Page.Annotations()` extracts link and text annotations; `Reader.Dest()` resolves named destinations; `Reader.Links()` aggregates document links into `LinkRef` entries.
- Outlines expose resolved page numbers.
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
cookbook snippets covering words, image metadata, extraction summaries,
diagnostics, XMP, fonts, annotations, named destinations, and encrypted PDFs.

Runnable examples:

```bash
go run ./examples/read_plain_text
go run ./examples/read_text_with_styles
```

Quick start:

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

## Limitations

- Extraction only — no PDF creation, modification, or rendering.
- Image content is not decoded; `Page.Images()` reports draw metadata only.
- No AcroForms extraction yet (planned).

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the full history of fixes and additions.
