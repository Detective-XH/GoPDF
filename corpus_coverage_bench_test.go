package pdf

import (
	"context"
	"io"
	"os"
	"testing"
)

// corpus_coverage_bench_test.go gates EXTRACTION THROUGHPUT across the real-document
// corpus categories that the BENCHMARKS.md comparison set (comparativeBenchFiles)
// deliberately omits. It exists so a regression in the table, multicolumn, Cyrillic,
// Korean, or Simplified-Chinese paths shows up in a benchmark instead of going
// unmeasured — the corpus grew well past the handful of fixtures the early
// benchmarks froze on.
//
// These are SPEED gates only; extraction correctness is the golden-output corpus's
// job (corpus_test.go), not these benchmarks.
//
// Relationship to the other benchmark sets:
//   - comparativeBenchFiles (comparative_bench_test.go) is a PUBLISHED contract:
//     BENCHMARKS.md hard-codes results for exactly those 3 files. It is NOT
//     extended here, and the names below are chosen so the `-bench` substring regex
//     `BenchmarkExtractText|BenchmarkExtractWords` cannot match them.
//   - corpus_bench_test.go holds the synthetic + single-CJK micro-benchmarks.
//
// Curated, not manifest-derived: the repo gives every benchmark a specific
// rationale (see the reserved-name note at the foot of corpus_bench_test.go), so
// this is an explicit list of confirmed-extractable fixtures rather than a
// `Golden != ""` sweep. The encoding/ decode-path probes ride along separately
// (decodePathBenchFiles / BenchmarkDecodePathExtract) as ENCODER-PATH sentinels:
// despite their tiny payloads they exercise the multibyte-CMap (Shift-JIS) and
// UCS-2 BE encoders and the Differences/unknown-encoding fallbacks, which have no
// other throughput coverage. Deliberately EXCLUDED:
//   - signals/ and geometry/ probes — correctness fixtures whose payloads (and,
//     for the malformed/image-only signals, error or empty output) measure open
//     overhead rather than extraction, and are gated elsewhere;
//   - hard/ fixtures — no stable golden; they document the extraction gap, not a
//     throughput target;
//   - the comparativeBenchFiles set — already benchmarked.

// coverageBenchFiles are the real-document corpus fixtures (relative to corpusRoot)
// NOT already covered by comparativeBenchFiles, each confirmed to extract on both
// the GetPlainText and Words() paths.
var coverageBenchFiles = []string{
	// CJK scripts beyond the comparativeBenchFiles pair (udhr-ja, irs-p850-zh-hant).
	"cjk/udhr-zh-hans.pdf", // Simplified Chinese
	"cjk/udhr-ko.pdf",      // Korean
	// Cyrillic encoding-fallback baseline.
	"cyrillic/udhr-ru.pdf",
	// Real table fixtures beyond nist-hb44 (the comparativeBenchFiles table file).
	"tables/irs-p55b-2025-excerpt.pdf",
	"tables/irs-db-t4-3-2025.pdf",
	"tables/eia-aer-t3-1-2011.pdf",
	"tables/epa-egrid2022-t1.pdf",
	"tables/irs-soi-inpre-t1-2022.pdf", // subset TrueType: 1-byte codes under a 2-byte ToUnicode codespace
	// Dense 3-column real documents (the table false-positive gate corpus).
	"multicolumn/fr-2024-06543.pdf",
	"multicolumn/fr-2024-01353.pdf",
}

// formsBenchFiles are the real blank AcroForm fixtures exercised by the Fields()
// path — a public API with no other benchmark coverage.
var formsBenchFiles = []string{
	"forms/irs-f1040-2025.pdf",             // 199 fields, deep dotted qualified names
	"forms/uscourts-cv071-civil-cover.pdf", // real /Parent field tree + /DA chains
}

// decodePathBenchFiles are the synthetic no-/ToUnicode decode-path fixtures (one per
// decode-path class). They are tiny, but each is the only throughput sentinel for an
// encoder/fallback the corpus audit flagged as benchmark-uncovered; their CORRECTNESS
// is gated by corpus_decodepath_test.go, not here.
var decodePathBenchFiles = []string{
	"encoding/predefined-identity.pdf", // Identity-H, no ToUnicode (missing_tounicode)
	"encoding/charset-shiftjis.pdf",    // Shift-JIS /90ms-RKSJ-H (multibyteCMapEncoder)
	"encoding/ucs2-be.pdf",             // UCS-2 BE /UniGB-UCS2-H (ucs2BEEncoder)
	"encoding/differences-partial.pdf", // Encoding /Differences unmappable glyph
	"encoding/unknown-name.pdf",        // unknown /Encoding name (pdfDoc fallback)
	"encoding/unmapped-glyph.pdf",      // ToUnicode under-covers its codespace (U+FFFD)
}

// BenchmarkCorpusExtractText measures cold open + full plain-text extraction across
// the real-document coverage corpus — the text axis omitted by BENCHMARKS.md.
func BenchmarkCorpusExtractText(b *testing.B) {
	for _, f := range coverageBenchFiles {
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

// BenchmarkCorpusExtractWords measures cold open + positioned-word extraction across
// all pages of the real-document coverage corpus — the layout axis omitted by
// BENCHMARKS.md. Speed only; word-grouping quality is not asserted here.
func BenchmarkCorpusExtractWords(b *testing.B) {
	for _, f := range coverageBenchFiles {
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

// BenchmarkExtractFields measures cold open + full AcroForm field-tree walk
// (Reader.Fields()) over the real blank-form fixtures — the forms axis that had
// no benchmark coverage.
func BenchmarkExtractFields(b *testing.B) {
	for _, f := range formsBenchFiles {
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
				if _, err := r.Fields(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecodePathExtract measures cold open + plain-text extraction over each
// synthetic decode-path fixture — the throughput sentinel for the multibyte-CMap,
// UCS-2 BE, and Differences/unknown-encoding fallbacks the comparison and coverage
// sets do not exercise. Tiny fixtures, so the number is encoder-path + open cost.
func BenchmarkDecodePathExtract(b *testing.B) {
	for _, f := range decodePathBenchFiles {
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
