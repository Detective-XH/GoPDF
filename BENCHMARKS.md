# Benchmarks

How GoPDF compares with common Python PDF extractors on text and positioned-word
extraction — the operations GoPDF is built for. These are **speed** numbers;
extraction *correctness* is gated separately by the golden-output corpus
([Accuracy & test corpus](README.md#accuracy--test-corpus)), not by these
benchmarks.

**What is and isn't claimed.** GoPDF targets fast, deterministic *text and
structure* extraction for LLM/RAG and ingestion pipelines. It does **not** render
pages, decode images, or reconstruct tables (see
[Limitations](README.md#limitations)). Its word/line grouping is tuned for speed
and byte-determinism, **not** to match the layout-reconstruction quality of a
dedicated layout engine — validate output quality on your own documents. The
comparison below is matched-scope (text-vs-text, words-vs-words) so the result is
not "GoPDF wins because it does less".

## Setup

- Machine: Apple M4 Pro, macOS; Go (repo toolchain), Python 3.13 in an isolated venv.
- Libraries: pypdf 6.13, pdfminer.six 20251230, pdfplumber 0.11, PyMuPDF 1.27
  (MuPDF, C), pypdfium2 5.9 (PDFium, C), poppler `pdftotext` 26.04 (C).
- Task: open + extract from in-memory bytes, warm process. Output volume is
  comparable across tools (no tool extracts materially less), so speed is compared
  on equal work.
- Statistic (both medians over repeated runs, stated explicitly so the two sides
  are not conflated): **GoPDF** — `go test -bench -count=8` reduced with
  `benchstat`, which reports the **median** across the 8 runs (each run is Go's own
  average over its internal `b.N` iterations). **Python** — **median** of N
  in-process timings. The Go figure therefore carries an inner per-run average that
  the Python figure does not; the cross-tool margins below (1.3–2.8×) are wide
  enough that this does not change any ranking, but the two are not bit-identical
  methodologies.
- Files: real public-domain corpus fixtures (`testdata/corpus/`).

## Throughput (GoPDF, reproducible)

Plain-text extraction, GoPDF only (`go test -bench=BenchmarkExtractText -count=8`,
benchstat median):

| Document | Pages | Time | Pages/sec |
|---|---|---|---|
| `cjk/irs-p850-zh-hant.pdf` (Traditional Chinese) | 22 | 25.3 ms | ~870 |
| `cjk/udhr-ja.pdf` (Japanese) | 9 | 2.5 ms | ~3650 |
| `tables/nist-hb44-appc-2026.pdf` (English tables) | 28 | 24.7 ms | ~1130 |

## Matched-scope comparison

ms/doc, lower is faster. GoPDF is fastest on both axes — including against the
C-backed tools — at comparable (text) or greater (words) output volume.

### Text only (plain UTF-8, no positions)

| Document | **GoPDF** `GetPlainText` | pdftotext (C) | pypdf | pdfminer (raw) |
|---|---|---|---|---|
| irs-p850-zh-hant (22pp) | **25.3** | 34.2 | 356 | 1016 |
| udhr-ja (9pp) | **2.5** | 3.6 | 25.0 | 55.5 |
| nist-hb44 (28pp) | **24.7** | 52.2 | 430 | 1230 |

GoPDF is faster than poppler's `pdftotext` — a C text extractor doing the exact
same job — by 1.3–2.1×, and several times to ~40× faster than the pure-Python
tools. Extracted character counts are comparable (e.g. irs-p850: GoPDF ~41.8k,
others 40k–47k).

### Positioned words (coordinates per token)

GoPDF `Page.Words()` vs the other tools' word-level output:

| Document | **GoPDF** `Words()` | pymupdf words (C) | pdfminer (layout) | pdfplumber words |
|---|---|---|---|---|
| irs-p850-zh-hant (22pp) | **39.8** | 63.8 | 1018 | 1134 |
| udhr-ja (9pp) | **3.4** | 9.7 | 56.1 | 76.9 |
| nist-hb44 (28pp) | **42.3** | 84.8 | 1229 | 1702 |

GoPDF is faster than PyMuPDF's C `get_text("words")` by 1.6–2.8×, at
equal-or-greater token count (udhr-ja: 213 = 213 = 213 across GoPDF / PyMuPDF /
pdfplumber; nist and irs: GoPDF emits *more* word tokens — it segments CJK runs
more finely, which is a segmentation-philosophy difference, not higher quality).

### Note: pdfminer's cost is not its layout analysis

`pdfminer (raw, laparams=None)` ≈ `pdfminer (layout)` in time (1016≈1018,
55.5≈56.1, 1230≈1229). Disabling layout analysis does not speed pdfminer up, so
the gap to GoPDF is core parsing speed, not extra work pdfminer does.

## Honest caveats

- **Scope.** GoPDF does not detect table structure (pdfplumber) or render/
  rasterize pages (PyMuPDF, PDFium). Those are out of scope by design, not a
  benchmark cheat — if you need them, those tools are the right choice.
- **Layout quality is not measured — and is a work in progress.** These are
  *speed* numbers only. We have not benchmarked the *quality* of GoPDF's word/line
  grouping against other tools, and layout extraction is still being improved. On
  CJK it currently segments more aggressively (more, shorter tokens). Treat
  positioned output as best-effort and validate it on your own documents.
- **One machine, one corpus class.** Text/table PDFs (CJK + English) on one
  machine. Scanned/image documents need OCR (out of scope for every tool here).
  Re-run on your own documents before relying on the ratios.
- **Warm-process throughput** (interpreter/import startup excluded). For per-file
  CLI invocation, Python's startup cost widens GoPDF's lead; for long-lived
  ingestion, warm throughput as measured is the relevant number.

## Reproduce

### GoPDF side (committed benchmarks)

Run with `-count=8` and reduce with `benchstat`, which reports the **median** — so
the Go figures match the Python median methodology (`go install
golang.org/x/perf/cmd/benchstat@latest`):

```bash
go test -run '^$' -bench 'BenchmarkExtractText|BenchmarkExtractWords' -count=8 . > go.txt
benchstat go.txt
```

`BenchmarkExtractText`/`BenchmarkExtractWords` run a curated 3-file subset chosen for
the Python comparison above. The repository also carries broader **internal coverage
benchmarks** — `BenchmarkCorpusExtractText`/`…Words` (more real documents),
`BenchmarkExtractFields` (AcroForm `Reader.Fields()`), and `BenchmarkDecodePathExtract`
(encoder/fallback paths). Those are GoPDF-only regression gates, not part of this
cross-tool comparison; run `go test -bench=.` to see them all.

### Python side (isolated venv, no system pollution)

The comparison script is kept here rather than as a tracked `.py` file so the
repository stays pure Go. Save it as `compare.py`, then:

```bash
python3 -m venv .venv
DYLD_LIBRARY_PATH=/opt/homebrew/opt/expat/lib \
  .venv/bin/pip install pypdf pdfplumber pdfminer.six pymupdf pdftotext
DYLD_LIBRARY_PATH=/opt/homebrew/opt/expat/lib \
  .venv/bin/python compare.py testdata/corpus/cjk/irs-p850-zh-hant.pdf
```

`DYLD_LIBRARY_PATH` works around a Homebrew Python 3.13 / system libexpat mismatch
when venv binaries are invoked directly; the `pdftotext` binding needs poppler
(`brew install poppler`).

<details>
<summary><code>compare.py</code> — matched-scope comparison script</summary>

```python
"""GoPDF vs Python PDF extractors, matched scope (text-only and positioned words).

Method mirrors GoPDF's BenchmarkExtract*: open + extract from in-memory bytes,
warm interpreter, median of N. GoPDF numbers come from the Go benchmarks; this
prints the Python rows. Usage: python compare.py <file.pdf> [N]
"""
import io
import statistics
import sys
import time

PATH = sys.argv[1] if len(sys.argv) > 1 else "testdata/corpus/cjk/irs-p850-zh-hant.pdf"
N = int(sys.argv[2]) if len(sys.argv) > 2 else 10
with open(PATH, "rb") as f:
    DATA = f.read()


def pypdf_text():
    from pypdf import PdfReader
    r = PdfReader(io.BytesIO(DATA))
    return "".join((p.extract_text() or "") for p in r.pages)


def pdfminer_raw():
    from pdfminer.high_level import extract_text
    return extract_text(io.BytesIO(DATA), laparams=None)  # no layout analysis


def pdftotext_text():
    import pdftotext
    return "".join(pdftotext.PDF(io.BytesIO(DATA)))


def pdfminer_layout():
    from pdfminer.high_level import extract_text
    return extract_text(io.BytesIO(DATA))  # default LAParams = layout analysis


def pdfplumber_words():
    import pdfplumber
    n = 0
    with pdfplumber.open(io.BytesIO(DATA)) as pdf:
        for p in pdf.pages:
            n += len(p.extract_words())
    return f"{n} words"


def pymupdf_words():
    try:
        import pymupdf as fitz
    except ImportError:
        import fitz
    doc = fitz.open(stream=DATA, filetype="pdf")
    return f'{sum(len(p.get_text("words")) for p in doc)} words'


def npages():
    from pypdf import PdfReader
    return len(PdfReader(io.BytesIO(DATA)).pages)


def bench(name, fn):
    try:
        out = fn()
    except Exception as e:  # noqa: BLE001
        print(f"  {name:22} ERROR: {e}")
        return
    times = []
    for _ in range(N):
        t0 = time.perf_counter()
        fn()
        times.append(time.perf_counter() - t0)
    med = statistics.median(times)
    size = len(out) if isinstance(out, str) else out
    print(f"  {name:22} {med * 1000:9.1f} ms   {NPAGES / med:9.1f} pg/s   {size}")


NPAGES = npages()
print(f"\nfile: {PATH}  pages: {NPAGES}  iters: {N}")
print("\n[text-only]            ms/doc      pages/s   output")
for nm, fn in [("pypdf", pypdf_text), ("pdfminer (raw)", pdfminer_raw), ("pdftotext", pdftotext_text)]:
    bench(nm, fn)
print("\n[layout]               ms/doc      pages/s   output")
for nm, fn in [("pdfminer (layout)", pdfminer_layout), ("pdfplumber words", pdfplumber_words), ("pymupdf words", pymupdf_words)]:
    bench(nm, fn)
```

</details>
