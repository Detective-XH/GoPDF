package pdf

import (
	"context"
	"io"
	"os"
	"testing"
)

// comparativeBenchFiles are the corpus fixtures (relative to corpusRoot) used by
// BENCHMARKS.md to compare GoPDF with Python PDF extractors. They span CJK and
// English, text and tables.
var comparativeBenchFiles = []string{
	"cjk/irs-p850-zh-hant.pdf",
	"cjk/udhr-ja.pdf",
	"tables/nist-hb44-appc-2026.pdf",
}

// BenchmarkExtractText measures cold open + full plain-text extraction from
// in-memory bytes — the text-only axis of BENCHMARKS.md.
func BenchmarkExtractText(b *testing.B) {
	for _, f := range comparativeBenchFiles {
		data, err := os.ReadFile(corpusPath(f))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(f, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				r, err := OpenBytes(data)
				if err != nil {
					b.Fatal(err)
				}
				rd, err := r.GetPlainText(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, rd)
			}
		})
	}
}

// BenchmarkExtractWords measures cold open + positioned-word extraction across all
// pages — the layout axis of BENCHMARKS.md. Speed only; word-grouping quality is
// a work in progress and not asserted here.
func BenchmarkExtractWords(b *testing.B) {
	for _, f := range comparativeBenchFiles {
		data, err := os.ReadFile(corpusPath(f))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(f, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				r, err := OpenBytes(data)
				if err != nil {
					b.Fatal(err)
				}
				for pi := 1; pi <= r.NumPage(); pi++ {
					p := r.Page(pi)
					if p.V.IsNull() {
						continue
					}
					if _, err := p.Words(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
