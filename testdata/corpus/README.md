# GoPDF Extraction Corpus

Fixtures for evidence-backed extraction tests and benchmarks.
This is the substrate; each feature PR adds its own fixtures under the convention below.

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

### Source URLs

- UDHR translations: `https://www.ohchr.org/sites/default/files/UDHR/Documents/UDHR_Translations/{chn,jpn,kkn,rus}.pdf`
- IRS Publication 850 (Traditional Chinese): `https://www.irs.gov/pub/irs-pdf/p850enzt.pdf`
- NIST Handbook 44 Appendix C: `https://www.nist.gov/system/files/documents/2025/12/30/appc-26-HB44-20251222.pdf`
- IRS Data Book 2025 (full, 94 pp — repo carries a 16-page qpdf excerpt): `https://www.irs.gov/pub/irs-pdf/p55b.pdf`
- Federal Register notices: `https://www.govinfo.gov/content/pkg/FR-2024-03-28/pdf/2024-06543.pdf`, `https://www.govinfo.gov/content/pkg/FR-2024-01-24/pdf/2024-01353.pdf`
- BEA Direct Investment release: `https://www.bea.gov/sites/default/files/2024-07/dici0724.pdf`
- IRS Publication 1040 (full — repo carries a 2-page qpdf excerpt): `https://www.irs.gov/pub/irs-pdf/p1040.pdf`
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
  fallback work and hard-PDF corpus expansion.
- `tables/` and `multicolumn/` are the accuracy and false-positive surfaces for the
  table-detection spike; `hard/` holds negative fixtures (no goldens) that document
  current extraction gaps for future fallback-encoding work.

## Synthetic extraction-signal fixtures (`signals/`)

Byte-exact synthetic fixtures (added by `TestCorpusRegenerate -update`) feeding the
v0.8 extraction-readiness signals — the extraction quality score and the image/scanned
page classifier. They are **not** real documents and carry no provenance/license
obligations. Image streams are never-decoded `/Length 0` stubs. The signal each fixture
locks today is asserted by `TestCorpusSignalFixtures` (and `TestCorpusNoGoldenFixtures`
for the no-golden entries) in `../../corpus_signals_test.go`.

| File | Type | Locked signal today | Golden | Consumer |
|------|------|---------------------|--------|----------|
| `signals/image-full-bleed.pdf` | image-only, coverage ~1.0 | HasText=false, ImageCount=1, image_only warning | — | classifier |
| `signals/image-thumbnail.pdf` | image-only, coverage ~0.0074 | same as full-bleed (v1 cannot distinguish) | — | classifier |
| `signals/image-thumbnail-text.pdf` | mixed image + text | HasText=true, ImageCount=1, no warning | `body text run` | classifier / quality score |
| `signals/text-artifact-only.pdf` | sparse text (page number at extremity) | HasText=true (v1 sparse-text gap), ImageCount=0 | `12` | classifier / quality score |
| `signals/malformed-unclosed-bt.pdf` | malformed: BT without ET | tolerated, deterministic partial text, no panic | `alpha beta` | quality score |
| `signals/malformed-mismatched-qq.pdf` | malformed: excess Q | tolerated, deterministic text, no panic | `gamma` | quality score |
| `signals/malformed-truncated.pdf` | malformed: TJ without array (empty-TJ, not a byte-level cut) | GetPlainText errors; ExtractionSummary recovers to HasText=true (silent-ok gap); no panic | — | quality score |

The `malformed-truncated` entry deliberately records a divergence: `GetPlainText`
surfaces the panic as an error (so it is no-golden), while `ExtractionSummary` reports
a clean-looking `HasText=true` from the partial `delta` run. That silent-ok gap is the
behavior the quality-score work must reconcile; this slice locks it as the current truth.
