# PDF Reader

[![License: BSD 3-Clause](https://img.shields.io/badge/License-BSD_3--Clause-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/Detective-XH/pdf.svg)](https://pkg.go.dev/github.com/Detective-XH/pdf)
[![Go Report Card](https://goreportcard.com/badge/github.com/Detective-XH/pdf)](https://goreportcard.com/report/github.com/Detective-XH/pdf)

A Go library for reading PDF files, with active CJK text extraction support.

Forked from [ledongthuc/pdf](https://github.com/ledongthuc/pdf) (upstream inactive since 2024).
Original lineage: [rsc/pdf](https://github.com/rsc/pdf).

## Features

- Plain text extraction with context/cancellation support
- Styled text extraction (font name, size, position)
- Text grouped by row
- Document metadata API (`/Info` dict: title, author, dates, …)
- Outline (table of contents) with resolved page numbers
- **CJK predefined CMap decoders**:
  - Japanese Shift-JIS (`90ms-RKSJ-H/V`, `90pv-RKSJ-H`)
  - CJK UCS-2 BE (`UniGB-UCS2-H/V`, `UniCNS-UCS2-H/V`, `UniJIS-UCS2-H/V`, `UniKS-UCS2-H/V`)
  - Simplified Chinese GBK / GB-EUC / GBKp-EUC (`GBK-EUC-H/V`, `GB-EUC-H/V`, `GBKp-EUC-H/V`)
  - Traditional Chinese Big5-ETen / ETenms (`ETen-B5-H/V`, `ETenms-B5-H/V`)
  - Korean UHC / KSC-EUC / UHC-HW (`KSCms-UHC-H/V`, `KSC-EUC-H/V`, `KSCms-UHC-HW-H/V`)

## Install

```bash
go get github.com/Detective-XH/pdf
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

	"github.com/Detective-XH/pdf"
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

	"github.com/Detective-XH/pdf"
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

	"github.com/Detective-XH/pdf"
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

## Fork status

| Area | Status |
|------|--------|
| Upstream sync | Merged through upstream@HEAD (2024) |
| Shift-JIS CMaps | Added |
| UCS-2 BE CMaps | Added |
| GBK / GB-EUC / GBKp-EUC CMaps | Added |
| Big5-ETen / ETenms CMaps | Added |
| UHC / KSC-EUC / UHC-HW CMaps | Added |
| Metadata API (`r.Info()`) | Added |
| Outline page numbers (`Outline.Page`) | Added |
| Context / cancellation | Added |
| Upstream PRs incorporated | #37, #42, #45, #58, #61, #63, #64, #66 |
