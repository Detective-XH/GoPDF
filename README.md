# GoPDF

[![Go CI](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml/badge.svg)](https://github.com/Detective-XH/gopdf/actions/workflows/ci.yml)
[![License: BSD 3-Clause](https://img.shields.io/badge/License-BSD_3--Clause-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/Detective-XH/gopdf.svg)](https://pkg.go.dev/github.com/Detective-XH/gopdf)
[![Go Report Card](https://goreportcard.com/badge/github.com/Detective-XH/gopdf)](https://goreportcard.com/report/github.com/Detective-XH/gopdf)

A Go library for reading PDF files, with active CJK text extraction support.

**Requires Go 1.25+** (`go.mod` directive).

Originally forked from [ledongthuc/pdf](https://github.com/ledongthuc/pdf); now an independent project.
Original lineage: [rsc/pdf](https://github.com/rsc/pdf).

## Features

- Plain text extraction with context/cancellation support
- Styled text extraction (font name, size, position)
- Text grouped by row
- `Pages() iter.Seq2[int, Page]` and `Texts() iter.Seq[Text]` — lazy iterators for streaming access (Go 1.23+)
- `OpenBytes([]byte)` — parse a PDF from an in-memory byte slice
- `Page.MediaBox()` and `Page.CropBox()` — page dimensions with inheritance-chain resolution
- Document metadata API (`r.Info()`: title, author, dates, …)
- Outline (table of contents) with resolved page numbers
- **Encrypted PDF support** — transparent decryption of Standard-security-handler files: RC4 (40/128-bit, V=1/2), AES-128 (V=4, AESV2), and **AES-256** (V=5, R=5/R=6 — Acrobat 9+ and PDF 2.0 / ISO 32000-2). Open with the empty, user, or owner password via `NewReaderEncrypted`.
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
- Requires Go 1.23+ for `Pages()` / `Texts()` iterators; all other APIs work on Go 1.21+.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the full history of fixes and additions.
