# GoPDF Extraction Corpus

Fixtures for evidence-backed extraction tests and benchmarks.
This is the substrate; each feature PR adds its own fixtures under the convention below.

Some of these files (`cjk/irs-p850-zh-hant.pdf`, `cjk/udhr-ja.pdf`,
`tables/nist-hb44-appc-2026.pdf`) also serve as inputs for the GoPDF-vs-Python
performance comparison — see [`BENCHMARKS.md`](../../BENCHMARKS.md).

## Convention

- Layout: `testdata/corpus/<category>/<name>.pdf` (+ `<name>.golden.txt` when golden-tested).
- The Go registry `corpusManifest` in `../../corpus_test.go` is the authoritative,
  machine-readable manifest (path, compare mode, source, license, feature). This README
  is the human-readable provenance table and must mirror it.
- `go` ignores `testdata/` for builds, so fixtures never affect `go build ./...`.
- The pre-commit PII scan is configured to **skip `testdata/`** (curated public-domain
  content); do not place secrets or personal data here regardless.

## Provenance & License

All fixtures below are real, public-domain documents. Golden-tested entries have
verified extractable text layers (confirmed end-to-end with `GetPlainText` at
acquisition time); most `hard/` entries start as deliberate NEGATIVE fixtures — documents that
defeat current extraction — committed without goldens to anchor future fallback-encoding
work. A `hard/` entry is promoted to a golden-tested entry once its underlying bug is
fixed and the extracted text is confirmed correct.

| File | Script | Source | License | Verified extraction |
|------|--------|--------|---------|---------------------|
| `cjk/udhr-zh-hans.pdf` | Chinese (Simplified) | OHCHR — Universal Declaration of Human Rights (Mandarin, Simplified) | Public domain (UDHR; OHCHR encourages reproduction) | 2,675 Han runes / 7 pp |
| `cjk/udhr-ja.pdf` | Japanese | OHCHR — UDHR (Japanese) | Public domain (UDHR) | 1,961 Hiragana runes / 9 pp |
| `cjk/udhr-ko.pdf` | Korean | OHCHR — UDHR (Korean) | Public domain (UDHR) | 3,344 Hangul runes / 7 pp |
| `cjk/irs-p850-zh-hant.pdf` | Chinese (Traditional) | IRS Publication 850 (EN-ZH-T) — English–Traditional-Chinese tax glossary | US-Gov work, public domain (17 U.S.C. §105) | 7,769 Han runes / 22 pp |
| `cyrillic/udhr-ru.pdf` | Russian (Cyrillic) | OHCHR — UDHR (Russian) | Public domain (UDHR) | 9,923 Cyrillic runes / 10 pp |
| `tables/nist-hb44-appc-2026.pdf` | English (Latin) | NIST Handbook 44 (2026 ed.) Appendix C — General Tables of Units of Measurement | US-Gov work, public domain (17 U.S.C. §105) | 55,512 normalized chars / 28 pp; clean Words()/Lines() with real glyph widths |
| `tables/irs-p55b-2025-excerpt.pdf` | English (Latin) | IRS Data Book 2025 (Pub 55-B), pages 40–55, excerpted with qpdf | US-Gov work, public domain (17 U.S.C. §105) | 68,696 normalized chars / 16 pp; numeric table cells extract, some row labels unmapped |
| `tables/irs-db-t4-3-2025.pdf` | English (Latin) | IRS Data Book 2025 (Pub 55-B), p.72 — Table 4-3 (Appeals Workload), single-page qpdf excerpt | US-Gov work, public domain (17 U.S.C. §105) | Partial text layer: numeric cells extract, some row labels U+FFFD. Cell-grid ground truth in `.cellgrid.tsv` (10×4) — see "Cell-grid accuracy corpus" below |
| `tables/eia-aer-t3-1-2011.pdf` | English (Latin) | EIA Annual Energy Review 2011, Table 3.1 (Fossil Fuel Production Prices) | US-Gov work, public domain (17 U.S.C. §105) | Clean text layer; two-tier spanning header. Cell-grid ground truth in `.cellgrid.tsv` (45×10) with as-printed `R`/`(s)`/`2011P`/en-dash tokens — see "Cell-grid accuracy corpus" below |
| `tables/epa-egrid2022-t1.pdf` | English (Latin) | EPA eGRID2022 Summary Tables, Table 1 (Subregion Output Emission Rates), p.2 | US-Gov work, public domain (17 U.S.C. §105) | **Held-out STROKE-bordered full lattice** (both `Content.Stroke` rules and thin `Content.Rect`); 3-tier header. Cell-grid ground truth in `.cellgrid.tsv` (31×17): ASCII-joined sub/superscript unit headers (`CO2`/`CH4`/`N2O`/`NOX`), bare-number data cells — see "Cell-grid accuracy corpus" below |
| `tables/irs-soi-inpre-t1-2022.pdf` | English (Latin) | IRS SOI Bulletin (Spring 2024), Table 1 (Individual Income Tax Returns, Preliminary Data, TY2022), p.6 (printed p.8), single-page qpdf excerpt | US-Gov work, public domain (17 U.S.C. §105) | **Held-out RECT-bordered split-column** page-face (cols 1–5 of an 11-col table); 3-tier header. Data fonts are subset TrueType (`CIDFont+F1`) whose ToUnicode declares a 2-byte codespace over 1-byte codes — the regression fixture for **simple-font 1-byte ToUnicode decode** (text was U+FFFD before that fix). Cell-grid ground truth in `.cellgrid.tsv` (51×6) with as-printed asterisks (`* 5,178`)/footnotes/negatives — see "Cell-grid accuracy corpus" below |
| `multicolumn/fr-2024-06543.pdf` | English (Latin) | Federal Register 89 FR 21528, doc 2024-06543 (Coast Guard ICR notice), govinfo.gov | US-Gov work, public domain (17 U.S.C. §105) | 13,661 normalized chars / 2 pp; dense 3-column body text, zero tables |
| `multicolumn/fr-2024-01353.pdf` | English (Latin) | Federal Register 89 FR 4633, doc 2024-01353 (NRC notice), govinfo.gov | US-Gov work, public domain (17 U.S.C. §105) | 6,570 normalized chars / 1 pp; dense 3-column body text, zero tables |
| `hard/bea-dici0724.pdf` | English (Latin) | BEA — Direct Investment by Country and Industry, July 2024 release | US-Gov work, public domain (17 U.S.C. §105) | 49,630 chars / 12 pp; subset fonts whose T\* line-moves were previously mis-decoded to U+FFFD under a 2-byte CMap (fixed) — zero replacement runes across all 12 pages; locked by golden snippets + a document-wide no-U+FFFD assertion (`TestCorpusBeaDiciNoReplacementRunes`) |
| `hard/irs-p1040-tax-tables-excerpt.pdf` | English (partial) | IRS Publication 1040 Tax Tables, pages 3–4, excerpted with qpdf | US-Gov work, public domain (17 U.S.C. §105) | NEGATIVE fixture (no golden): zero-advance glyph widths (W=0) + partial ToUnicode loss |
| `forms/irs-f1040-2025.pdf` | English (Latin) | IRS Form 1040 (2025 tax year) | US-Gov work, public domain (17 U.S.C. §105) | Blank AcroForm: `Reader.Fields()` → 199 terminal fields (deep dotted LiveCycle names, maxDepth 6); text layer extracts form labels; carries a rotated text run. See "Real AcroForm fixtures" below |
| `forms/uscourts-cv071-civil-cover.pdf` | English (Latin) | US District Court, C.D. Cal. — Civil Cover Sheet (form CV-071) | US-Gov work, public domain (17 U.S.C. §105) | Blank AcroForm: `Reader.Fields()` → 165 terminal fields with a real `/Parent` tree + `/DA` chains and Acrobat-derived `/T` label names. See "Real AcroForm fixtures" below |

### Source URLs

- UDHR translations: `https://www.ohchr.org/sites/default/files/UDHR/Documents/UDHR_Translations/{chn,jpn,kkn,rus}.pdf`
- IRS Publication 850 (Traditional Chinese): `https://www.irs.gov/pub/irs-pdf/p850enzt.pdf`
- NIST Handbook 44 Appendix C: `https://www.nist.gov/system/files/documents/2025/12/30/appc-26-HB44-20251222.pdf`
- IRS Data Book 2025 (full, 94 pp — repo carries a 16-page qpdf excerpt `irs-p55b-2025-excerpt.pdf` AND a single-page p.72 excerpt `irs-db-t4-3-2025.pdf`): `https://www.irs.gov/pub/irs-pdf/p55b.pdf`
- EIA Annual Energy Review 2011, Table 3.1 (single-page PDF): `https://www.eia.gov/totalenergy/data/annual/pdf/sec3_3.pdf`
- EPA eGRID2022 Summary Tables (5-page PDF; Table 1 on p.2): `https://www.epa.gov/system/files/documents/2024-01/egrid2022_summary_tables.pdf`
- IRS SOI Bulletin Table 1 (26-page PDF; repo carries a single-page p.6 = printed p.8 excerpt `irs-soi-inpre-t1-2022.pdf`): `https://www.irs.gov/pub/irs-soi/soi-a-inpre-id2401.pdf`
- Cell-grid companion datasets (authoring cross-checks; URLs recorded, **not committed** — no consumer in-tree yet):
  IRS Data Book 2025 Table 4-3 XLSX `https://www.irs.gov/pub/irs-soi/25db-4-03-ap.xlsx`;
  EIA AER 2011 Table 3.1 (HTML served as `.xls`) `https://www.eia.gov/totalenergy/data/annual/xls/stb0301.xls`;
  EPA eGRID2022 Summary Tables XLSX `https://www.epa.gov/system/files/documents/2024-01/egrid2022_summary_tables.xlsx`;
  IRS SOI Table 1 companion (legacy BIFF `.xls`) `https://www.irs.gov/pub/irs-soi/22in01pl.xls`
- Federal Register notices: `https://www.govinfo.gov/content/pkg/FR-2024-03-28/pdf/2024-06543.pdf`, `https://www.govinfo.gov/content/pkg/FR-2024-01-24/pdf/2024-01353.pdf`
- BEA Direct Investment release: `https://www.bea.gov/sites/default/files/2024-07/dici0724.pdf`
- IRS Publication 1040 (full — repo carries a 2-page qpdf excerpt): `https://www.irs.gov/pub/irs-pdf/p1040.pdf`
- IRS Form 1040 (2025 tax year): `https://www.irs.gov/pub/irs-pdf/f1040.pdf`
- C.D. Cal. Civil Cover Sheet CV-071: `https://apps.cacd.uscourts.gov/cm-api/dwwwroot/CV-071.pdf`
- Evaluated and NOT committed (recorded so future acquisition starts from evidence):
  Census Statistical Abstract population tables
  (`https://www2.census.gov/library/publications/2011/compendia/statab/131ed/tables/pop.pdf`,
  3.6 MB, 62 pp) — same missing-ToUnicode failure class as `hard/bea-dici0724.pdf`,
  redundant at 3.5× the size; CBO Outlook 2025 (`cbo.gov` 403s non-browser fetches);
  OMB Analytical Perspectives FY2027 (158 pp, 5.9 MB — too large for a fixture).

## Notes

- Real-PDF fixtures use **normalized** golden comparison (whitespace-normalized substring
  match), not byte-exact — real extraction output drifts on float formatting / ordering
  across platforms. See `compareNormalized` in `corpus_test.go`.
- Synthetic fixtures (plaintext, styled) are added by `TestCorpusRegenerate -update` and
  use byte-exact comparison.
- `cyrillic/` is a forward-compat baseline for planned Cyrillic legacy-encoding
  fallback work and hard-PDF corpus expansion. Research notes — the (negative-result)
  search for a real PDF that declares a legacy Cyrillic encoding by name without
  `/ToUnicode`, with a self-contained reproduction recipe:
  [`cyrillic/LEGACY-ENCODING-SEARCH.md`](cyrillic/LEGACY-ENCODING-SEARCH.md).
- `tables/` and `multicolumn/` are the accuracy and false-positive surfaces for the
  table-detection spike; `hard/` holds fixtures for difficult real-world PDFs — some are
  negative (no golden, documenting current extraction gaps) and some are promoted once the
  underlying bug is fixed and the text is confirmed fully extractable.

## Real AcroForm fixtures (`forms/`)

Real, public-domain US-Gov forms committed to give `Reader.Fields()` its first
real-world coverage — until now it was validated only against hand-crafted
synthetic fixtures (`forms_test.go`), whose field trees encode our own mental
model. Both forms are **blank** (no entered data). A 4-channel search
(2026-06-11) for a genuinely *filled* public-domain AcroForm found none: agencies
publish blank fillables, "completed" samples are flattened (fields stripped),
real filings are flattened/scanned or XFA, and any form with real entered data
carries PII and is not redistributable. A blank real form still exercises the
risk the gate names — real field-tree structure, qualified names, types, page
attribution, and generator quirks — while the `/V` value-decoding path stays
covered by the synthetic `forms_test.go` cases.

`uscourts-cv071-civil-cover.pdf` had its document metadata stripped
(`qpdf --remove-info --remove-metadata`) before commit to remove a personal name
(the template's author) from the `/Info` `Author` and XMP `dc:creator` fields. The
AcroForm body, page content, and fonts are unmodified — `Reader.Fields()` output
and the text layer are byte-identical before and after, so both goldens are
unaffected. `irs-f1040-2025.pdf` is committed verbatim (its `/Info Author` is an
IRS org code, not a person).

Each fixture is locked twice: its **text layer** is sentineled by `TestCorpusGolden`
(the `.golden.txt` carries representative body labels spanning every page — not just
the page-1 header — so dropping a later page or body text fails it), and its
**AcroForm field inventory** by `TestCorpusFormFixtures` (`corpus_forms_test.go`) against a
byte-exact `.fields-golden.txt` (one line per terminal field: page, type,
qualified name, value, read-only, rect). Regenerate the field goldens with
`go test -run TestCorpusFormFixtures -update`. No PII: the forms are blank, so the
goldens carry only template/label field names and empty / `Off` values.

| File | Fields | Structure locked | Golden |
|------|--------|------------------|--------|
| `forms/irs-f1040-2025.pdf` | 199 | Adobe LiveCycle flat AcroForm; deep dotted qualified names (`topmostSubform[0].Page1[0].f1_01[0]`, maxDepth 6); mixed Text/CheckBox; carries a rotated text run (registered in `rotatedCorpusFixtures`) | `forms/irs-f1040-2025.golden.txt` (text) + `.fields-golden.txt` (fields) |
| `forms/uscourts-cv071-civil-cover.pdf` | 165 | Real `/Parent` field tree + `/DA` default-appearance chains; Acrobat-derived `/T` names that are full label strings (a real generator quirk); Text/CheckBox/Radio/Combo mix | `forms/uscourts-cv071-civil-cover.golden.txt` (text) + `.fields-golden.txt` (fields) |

## Cell-grid accuracy corpus (`tables/*.cellgrid.tsv`)

Held-out ground-truth grids for table cell-segmentation accuracy. Each `.cellgrid.tsv`
records a real table's `(row, col) → value` layout, authored **independently of GoPDF**
so it can later score a table detector without circularity. **There is no table-detector
API in-tree yet**, so this is *not* an accuracy gate today: the consuming test
(`../../corpus_cellgrid_test.go`) validates structural **integrity** only — it parses each
grid, checks the declared `dims`/`header_rows` match the actual tab-field layout, confirms
the cited source PDF is in `corpusManifest` and its `pdf_page` is in range, and forbids
orphan grids. The extraction-vs-ground-truth scorer is a follow-up blocked on that API.

**Anti-circularity (independence).** Ground truth is authored from (a) the page rendered to
an image and read visually, and (b) a published companion dataset (XLSX/HTML) — **never**
from GoPDF `Words()`/`Lines()`/`Blocks()` output. NIST HB44 is deliberately **excluded**
from this set (it was the cell-seg spike's tuning source; scoring against it would flatter
the number) — it stays a bordered/lattice example only.

**`.cellgrid.tsv` v1 format.** Tab = column, newline = row. Leading `#` lines are a
metadata/comment block the parser skips; the block declares `dims=<rows>x<cols>` and
`header_rows=<n>`, and the parser asserts every grid row has exactly `cols` tab-separated
fields (so trailing empty cells are explicit, never lost). Values are **as printed on the
page** (thousands separators, footnote markers `[n]`, special tokens like `(s)` and en-dash
kept; the companion confirms the digits but the page is the stored form). A multi-tier
header uses multiple `header_rows`: a spanning cell's text sits in its leftmost (or, for a
header cell spanning rows, topmost) spanned column, the rest empty. An out-of-scope cell may
carry a `# known-ceiling: <reason> @ r<row>c<col>` marker so a future scorer attributes the
miss to an absent capability, not broken segmentation.

| File | Bucket | Dims (`r×c`) | Header rows | Companion (cross-check) | Notes |
|------|--------|-------------|-------------|-------------------------|-------|
| `tables/irs-db-t4-3-2025.cellgrid.tsv` | borderless clean | 10×4 | 1 | `25db-4-03-ap.xlsx` (27/27 data cells matched) | The printed `(1)/(2)/(3)` column-number band is a column-index aid, omitted as data |
| `tables/eia-aer-t3-1-2011.cellgrid.tsv` | two-tier spanning header + special values | 45×10 | 2 | `stb0301.xls` (full annual series; PDF is the selected-years subset) | `R`-prefix=Revised, `2011P`=Preliminary, `(s)`=magnitude < 0.05%, en-dash pair (1949)=Not applicable |
| `tables/epa-egrid2022-t1.cellgrid.tsv` | **bordered/lattice (stroke)** + 3-tier header | 31×17 | 3 | `egrid2022_summary_tables.xlsx` (Table 1 sheet; 28 data rows × 15 numeric cols generated from XLSX, round-half-up to printed precision, render cross-verified) | First **stroke-bordered** held-out fixture (`Content.Stroke`+thin `Content.Rect`); data cells bare numbers; ASCII-joined sub/superscript unit headers (`CO2`/`CH4`/`N2O`/`CO2e`/`NOX`/`SO2`) per the 2026-06-14 format amendment |
| `tables/irs-soi-inpre-t1-2022.cellgrid.tsv` | **rect-bordered split-column** + 3-tier header | 51×6 | 3 | `22in01pl.xls` (sheet `TAB1`, rows 57–104; 32 data rows × 5 numeric cols generated from the BIFF `.xls`, round-half-away-from-zero to printed precision, render cross-verified) | Page-face = cols 1–5 of an 11-col table (split across facing pages); item labels stored trimmed; as-printed asterisks (`* 5,178` small-sample), footnote markers (`Under $15,000 [1]`), negatives (`-65,622,639`); one `# known-ceiling` multi-line wrapped section label. Data fonts (`CIDFont+F1` subset TrueType, 2-byte ToUnicode codespace over 1-byte codes) — regression fixture for the **simple-font 1-byte ToUnicode decode** |

Both PDFs had their document metadata stripped (`qpdf --remove-info --remove-metadata`)
before commit to remove embedded `/Info` + XMP author fields — the EIA file's `/Info`
`/Author` named the template's author (a personal name), and the XMP `dc:creator` block was
dropped from both. The page content and text layer are unmodified (only `/ModDate` remains
in `/Info`), so the `.golden.txt` snippets and `.cellgrid.tsv` grids are unaffected. This
matters because the PII scan skips `testdata/`, so a personal name here would otherwise
bypass automated checks.

Companion datasets are recorded by URL above and **not committed** (no in-tree consumer
yet; they are authoring cross-checks). The grids match the companions value-by-value and the
rendered page visually.

## Synthetic extraction-signal fixtures (`signals/`)

Byte-exact synthetic fixtures (added by `TestCorpusRegenerate -update`) feeding the
extraction-readiness signals — the extraction quality score and the image/scanned
page classifier. They are **not** real documents and carry no provenance/license
obligations. Image streams are never-decoded `/Length 0` stubs. The signal each fixture
locks today is asserted by `TestCorpusSignalFixtures` (and `TestCorpusNoGoldenFixtures`
for the no-golden entries) in `../../corpus_signals_test.go`.

| File | Type | Locked signal today | Golden | Consumer |
|------|------|---------------------|--------|----------|
| `signals/image-full-bleed.pdf` | image-only, coverage ~1.0 | HasText=false, ImageCount=1, ImageCoverage~1.0, image_only warning | — | classifier |
| `signals/image-thumbnail.pdf` | image-only, coverage ~0.0074 | HasText=false, ImageCount=1, ImageCoverage~0.0074 (distinct from full-bleed) | — | classifier |
| `signals/image-thumbnail-text.pdf` | mixed image + text | HasText=true, ImageCount=1, ImageCoverage~0.0074, no warning | `body text run` | classifier / quality score |
| `signals/text-artifact-only.pdf` | sparse text (page number at bottom extremity) | HasText=true, ImageCount=0, fires sparse_text warning | `12` | classifier / quality score |
| `signals/text-numeric-center.pdf` | page-number token at page centre (margin-band negative) | HasText=true, ImageCount=0, NO sparse_text warning | — | classifier |
| `signals/malformed-unclosed-bt.pdf` | malformed: BT without ET | tolerated, deterministic partial text, no panic | `alpha beta` | quality score |
| `signals/malformed-mismatched-qq.pdf` | malformed: excess Q | tolerated, deterministic text, no panic | `gamma` | quality score |
| `signals/malformed-truncated.pdf` | malformed: TJ without array (empty-TJ, not a byte-level cut) | GetPlainText errors; ExtractionSummary recovers to HasText=true (silent-ok gap); no panic | — | quality score |

The `malformed-truncated` entry deliberately records a divergence: `GetPlainText`
surfaces the panic as an error (so it is no-golden), while `ExtractionSummary` reports
a clean-looking `HasText=true` from the partial `delta` run. That silent-ok gap is the
behavior the quality-score work must reconcile; this slice locks it as the current truth.

## Synthetic decode-path + geometry fixtures (`encoding/`, `geometry/`)

Byte-exact synthetic fixtures (added by `TestCorpusRegenerate -update`) feeding the
fallback encoding framework. Each `encoding/` fixture omits a usable
`/ToUnicode` so the extractor takes exactly one decode-path class; the `geometry/`
fixtures exercise rotated and vertical text. They are **not** real documents and carry
no provenance/license obligations. The page signal and the **document-scoped** encoder
warning each fixture fires today are asserted by `TestCorpusDecodePathFixtures` (and,
for the no-golden `geometry/` entries, `TestCorpusNoGoldenFixtures`) in
`../../corpus_decodepath_test.go`. Encoder-selection warnings are document-scoped, so
they appear in `DocumentSummary().Warnings` / `Reader.Warnings()`, not in
`PageExtractionSummary.Warnings`.

| File | Decode path / geometry | Locked signal today | Golden |
|------|------------------------|---------------------|--------|
| `encoding/predefined-identity.pdf` | predefined CMap (`/Identity-H`) | text + `missing_tounicode` | `identity` |
| `encoding/charset-shiftjis.pdf` | charset fallback (Shift-JIS) | text + `fallback_encoding` | `あ` |
| `encoding/ucs2-be.pdf` | UCS-2 BE (`/UniGB-UCS2-H`) | text + `fallback_encoding` | `中` |
| `encoding/differences-partial.pdf` | dict `/Differences` (1 lost) | text + `missing_glyph_mapping` | `differ` |
| `encoding/unknown-name.pdf` | unknown name → pdfDoc | text + `unsupported_encoding` | `unknown` |
| `encoding/unmapped-glyph.pdf` | ToUnicode under-coverage | text, U+FFFD in output (silent) | `A` + U+FFFD |
| `geometry/rotated-90.pdf` | 90°-rotated Tm | text + `rotated_text` (FontSize→0) | — |
| `geometry/vertical-cmap.pdf` | vertical `-V` CMap | text + `fallback_encoding` + `vertical_writing_mode` | — |
| `geometry/page-rotate-90.pdf` | same Tm + page `/Rotate 90` | text, **no** `rotated_text` (/Rotate cancels it) | — |

The `geometry/` fixtures are warning-level only (no extraction golden). `rotated-90.pdf`
locks the rotated-text risk warning (its 90° `Tm` collapses `FontSize` to `Trm[0][0]=0`);
`vertical-cmap.pdf` locks the fallback-encoding + vertical-writing-mode warnings (its `-V`
advance is now vertical). `page-rotate-90.pdf` is the contrast case: the **same** rotated
content plus a page `/Rotate 90` that GoPDF now **honors** (composed into the base CTM) —
the rotation cancels back to an upright display-space baseline, so `FontSize` recovers and
the rotated-text warning does **not** fire. Together they satisfy the fixture half of the
rotated/vertical geometry gate.

## Lines() reading-order characterization (committed FR + UDHR fixtures)

`corpus_lines_test.go` runs `Page.Lines()` over the multicolumn Federal Register
fixtures (`multicolumn/fr-*.pdf`) and the CJK UDHR fixtures (`cjk/udhr-{ja,zh-hans,
ko}.pdf`) and locks today's reading-order behaviour: a character-conservation
invariant (`Lines()` drops/invents no glyph vs `Content()`), plus sentinels for the
current column interleaving (tokens from physically separate columns co-occur in one
`Line.S`) and CJK intra-line spacing (zh-hans/ko split per glyph/syllable into
space-joined lines; ja stays contiguous). These are characterization tests — **no
fixtures or goldens are added** (the locked values live inline in the test, keyed by
fixture path). The reading-order stabilisation work updates the sentinels when it
intentionally changes line grouping; until then they document the gap.
