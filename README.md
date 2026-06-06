# GoPDF

[![Go CI](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml/badge.svg)](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml)
[![License: BSD 3-Clause](https://img.shields.io/badge/License-BSD_3--Clause-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/Detective-XH/gopdf.svg)](https://pkg.go.dev/github.com/Detective-XH/gopdf)
[![Go Report Card](https://goreportcard.com/badge/github.com/Detective-XH/gopdf)](https://goreportcard.com/report/github.com/Detective-XH/gopdf)

A Go-native library for extracting text from real-world PDFs — robust across
scripts (Latin, CJK, Cyrillic), encodings, multi-font and multi-page documents,
and malformed input.

**Requires Go 1.25+** (`go.mod` directive).

## Background

GoPDF began as a maintained repair fork of [ledongthuc/pdf](https://github.com/ledongthuc/pdf)
(itself derived from [rsc/pdf](https://github.com/rsc/pdf)). Early releases focused on
closing long-standing extraction and robustness gaps in the upstream reader lineage —
including CJK/Cyrillic decoding, ToUnicode priority, Form XObject text omission, TJ
word-boundary handling, malformed-parser panics, AES-encrypted PDFs, and missing
regression coverage. It is now an independent project, aimed at being a dependable,
pure-Go upstream for PDF text extraction.

## Features

- Plain text extraction with context/cancellation support
- Styled text extraction (font name, size, position)
- Text grouped by row
- Word-level extraction with bounding boxes (`Page.Words()`) — words in reading order, each with an (X, Y, width, height) box in PDF coordinate space
- Multi-font and multi-page extraction, verified against a multilingual regression corpus
- Broad script coverage — Latin, **Cyrillic**, and CJK (see the CMap list below)
- Nested **Form XObject** text — content drawn via the `Do` operator is not dropped
- TJ word-boundary handling — kerning-based inter-glyph spacing is preserved as word gaps
- Resilient to malformed content streams — recovers the text decoded before a fault instead of panicking
- `Pages() iter.Seq2[int, Page]` and `Texts() iter.Seq[Text]` — lazy iterators for streaming access (Go 1.23+)
- `OpenBytes([]byte)` — parse a PDF from an in-memory byte slice
- `Page.MediaBox()` and `Page.CropBox()` — page dimensions with inheritance-chain resolution
- Document metadata API (`r.Info()`: title, author, dates, …)
- Raw XMP metadata (`Reader.XMP()`) — the catalog's `/Metadata` packet as stored, for Dublin Core / custom-namespace fields beyond `/Info`
- Document font inventory (`Reader.Fonts()`) — every distinct font with subtype, embedded-program presence, and the pages where it appears
- Outline (table of contents) with resolved page numbers
- Annotations (`Page.Annotations()`) — `/Link` hyperlinks (URI targets and internal GoTo destination page) and `/Text` notes, each with its rectangle
- Named-destination lookup (`Reader.Dest()`) — resolve a named destination to a 1-based page number
- **Safe for concurrent use** — after open, one `Reader`'s pages can be extracted in parallel by multiple goroutines; repeated dereferencing is served from a bounded internal cache
- **Encrypted PDF support** — transparent decryption of Standard-security-handler files: RC4 (40/128-bit, V=1/2), AES-128 (V=4, AESV2), and **AES-256** (V=5, R=5/R=6 — Acrobat 9+ and PDF 2.0 / ISO 32000-2). Per-class crypt filters (`/Identity`, `StmF ≠ StrF`) and cleartext metadata (`/EncryptMetadata false`) are handled; passwords are SASLprep-normalized. Open with the empty, user, or owner password via `NewReaderEncrypted`.
- **Broad file compatibility** — PDF 2.0 headers, hybrid-reference files (`/XRefStm`), and the common stream filters: Flate and LZW (full PNG/TIFF predictor set), ASCII85, ASCIIHex, RunLength
- **CJK predefined CMap decoders**:
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

See the `examples/` folder for runnable programs.

### Read plain text

```go
package main

import (
	"bytes"
	"context"
	"fmt"

	"github.com/Detective-XH/gopdf"
)

func main() {
	f, r, err := pdf.Open("./sample.pdf")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	var buf bytes.Buffer
	b, err := r.GetPlainText(context.Background())
	if err != nil {
		panic(err)
	}
	buf.ReadFrom(b)
	fmt.Println(buf.String())
}
```

### Read styled text

```go
package main

import (
	"context"
	"fmt"

	"github.com/Detective-XH/gopdf"
)

func main() {
	f, r, err := pdf.Open("./sample.pdf")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	sentences, err := r.GetStyledTexts(context.Background())
	if err != nil {
		panic(err)
	}
	for _, s := range sentences {
		fmt.Printf("font=%s size=%.1f x=%.1f y=%.1f text=%s\n",
			s.Font, s.FontSize, s.X, s.Y, s.S)
	}
}
```

### Read text by row

```go
package main

import (
	"fmt"
	"os"

	"github.com/Detective-XH/gopdf"
)

func main() {
	f, r, err := pdf.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()

	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, _ := p.GetTextByRow()
		for _, row := range rows {
			fmt.Printf("row %d:", row.Position)
			for _, word := range row.Content {
				fmt.Printf(" %s", word.S)
			}
			fmt.Println()
		}
	}
}
```

## Limitations

- Text extraction only — no PDF creation, modification, or rendering.
- Image content is not decoded (location metadata via `Page.Images()` is planned).
- No AcroForms extraction yet (planned).

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the full history of fixes and additions.
