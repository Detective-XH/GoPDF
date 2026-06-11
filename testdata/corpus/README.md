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
acquisition time); `hard/` entries are deliberate NEGATIVE fixtures — documents that
defeat current extraction — committed without goldens to anchor future
fallback-encoding work.

| File | Script | Source | License | Verified extraction |
|------|--------|--------|---------|---------------------|
| `cjk/udhr-zh-hans.pdf` | Chinese (Simplified) | OHCHR — Universal Declaration of Human Rights (Mandarin, Simplified) | Public domain (UDHR; OHCHR encourages reproduction) | 2,675 Han runes / 7 pp |
| `cjk/udhr-ja.pdf` | Japanese | OHCHR — UDHR (Japanese) | Public domain (UDHR) | 1,961 Hiragana runes / 9 pp |
| `cjk/udhr-ko.pdf` | Korean | OHCHR — UDHR (Korean) | Public domain (UDHR) | 3,344 Hangul runes / 7 pp |
| `cjk/irs-p850-zh-hant.pdf` | Chinese (Traditional) | IRS Publication 850 (EN-ZH-T) — English–Traditional-Chinese tax glossary | US-Gov work, public domain (17 U.S.C. §105) | 7,769 Han runes / 22 pp |
| `cyrillic/udhr-ru.pdf` | Russian (Cyrillic) | OHCHR — UDHR (Russian) | Public domain (UDHR) | 9,923 Cyrillic runes / 10 pp |
| `tables/nist-hb44-appc-2026.pdf` | English (Latin) | NIST Handbook 44 (2026 ed.) Appendix C — General Tables of Units of Measurement | US-Gov work, public domain (17 U.S.C. §105) | 55,512 normalized chars / 28 pp; clean Words()/Lines() with real glyph widths |
| `tables/irs-p55b-2025-excerpt.pdf` | English (Latin) | IRS Data Book 2025 (Pub 55-B), pages 40–55, excerpted with qpdf | US-Gov work, public domain (17 U.S.C. §105) | 68,696 normalized chars / 16 pp; numeric table cells extract, some row labels unmapped |
| `multicolumn/fr-2024-06543.pdf` | English (Latin) | Federal Register 89 FR 21528, doc 2024-06543 (Coast Guard ICR notice), govinfo.gov | US-Gov work, public domain (17 U.S.C. §105) | 13,661 normalized chars / 2 pp; dense 3-column body text, zero tables |
| `multicolumn/fr-2024-01353.pdf` | English (Latin) | Federal Register 89 FR 4633, doc 2024-01353 (NRC notice), govinfo.gov | US-Gov work, public domain (17 U.S.C. §105) | 6,570 normalized chars / 1 pp; dense 3-column body text, zero tables |
| `hard/bea-dici0724.pdf` | English (unmappable) | BEA — Direct Investment by Country and Industry, July 2024 release | US-Gov work, public domain (17 U.S.C. §105) | NEGATIVE fixture (no golden): subset fonts lack usable ToUnicode — geometry intact, text extracts as U+FFFD |
| `hard/irs-p1040-tax-tables-excerpt.pdf` | English (partial) | IRS Publication 1040 Tax Tables, pages 3–4, excerpted with qpdf | US-Gov work, public domain (17 U.S.C. §105) | NEGATIVE fixture (no golden): zero-advance glyph widths (W=0) + partial ToUnicode loss |
| `forms/irs-f1040-2025.pdf` | English (Latin) | IRS Form 1040 (2025 tax year) | US-Gov work, public domain (17 U.S.C. §105) | Blank AcroForm: `Reader.Fields()` → 199 terminal fields (deep dotted LiveCycle names, maxDepth 6); text layer extracts form labels; carries a rotated text run. See "Real AcroForm fixtures" below |
| `forms/uscourts-cv071-civil-cover.pdf` | English (Latin) | US District Court, C.D. Cal. — Civil Cover Sheet (form CV-071) | US-Gov work, public domain (17 U.S.C. §105) | Blank AcroForm: `Reader.Fields()` → 165 terminal fields with a real `/Parent` tree + `/DA` chains and Acrobat-derived `/T` label names. See "Real AcroForm fixtures" below |

### Source URLs

- UDHR translations: `https://www.ohchr.org/sites/default/files/UDHR/Documents/UDHR_Translations/{chn,jpn,kkn,rus}.pdf`
- IRS Publication 850 (Traditional Chinese): `https://www.irs.gov/pub/irs-pdf/p850enzt.pdf`
- NIST Handbook 44 Appendix C: `https://www.nist.gov/system/files/documents/2025/12/30/appc-26-HB44-20251222.pdf`
- IRS Data Book 2025 (full, 94 pp — repo carries a 16-page qpdf excerpt): `https://www.irs.gov/pub/irs-pdf/p55b.pdf`
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
  table-detection spike; `hard/` holds negative fixtures (no goldens) that document
  current extraction gaps for future fallback-encoding work.

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
| `geometry/rotated-90.pdf` | 90°-rotated Tm | text, no rotation warning | — |
| `geometry/vertical-cmap.pdf` | vertical `-V` CMap | text + `fallback_encoding`, no vertical warning | — |

The `geometry/` fixtures are warning-level only (no extraction golden): they lock that
a rotated or vertical page looks healthy today (`text` signal; FontSize collapses to
`Trm[0][0]=0` for the 90° run, and the `-V` CMap's WMode is unread). The fallback
encoding framework adds the rotated-text / vertical-writing-mode risk warnings these
fixtures will then trigger; they also satisfy the fixture half of the rotated/vertical
geometry gate.

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
