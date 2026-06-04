# GoPDF Extraction Corpus

Fixtures for evidence-backed extraction tests and benchmarks (roadmap **Q-07-1**).
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

All fixtures below are real, public-domain documents with verified extractable text
layers. Extraction was confirmed end-to-end with `GetPlainText` during Q-07-1 planning.

| File | Script | Source | License | Verified extraction |
|------|--------|--------|---------|---------------------|
| `cjk/udhr-zh-hans.pdf` | Chinese (Simplified) | OHCHR — Universal Declaration of Human Rights (Mandarin, Simplified) | Public domain (UDHR; OHCHR encourages reproduction) | 2,675 Han runes / 7 pp |
| `cjk/udhr-ja.pdf` | Japanese | OHCHR — UDHR (Japanese) | Public domain (UDHR) | 1,961 Hiragana runes / 9 pp |
| `cjk/udhr-ko.pdf` | Korean | OHCHR — UDHR (Korean) | Public domain (UDHR) | 3,344 Hangul runes / 7 pp |
| `cjk/irs-p850-zh-hant.pdf` | Chinese (Traditional) | IRS Publication 850 (EN-ZH-T) — English–Traditional-Chinese tax glossary | US-Gov work, public domain (17 U.S.C. §105) | 7,769 Han runes / 22 pp |
| `cyrillic/udhr-ru.pdf` | Russian (Cyrillic) | OHCHR — UDHR (Russian) | Public domain (UDHR) | 9,923 Cyrillic runes / 10 pp |

### Source URLs

- UDHR translations: `https://www.ohchr.org/sites/default/files/UDHR/Documents/UDHR_Translations/{chn,jpn,kkn,rus}.pdf`
- IRS Publication 850 (Traditional Chinese): `https://www.irs.gov/pub/irs-pdf/p850enzt.pdf`

## Notes

- Real-PDF fixtures use **normalized** golden comparison (whitespace-normalized substring
  match), not byte-exact — real extraction output drifts on float formatting / ordering
  across platforms. See `compareNormalized` in `corpus_test.go`.
- Synthetic fixtures (plaintext, styled) are added by `TestCorpusRegenerate -update` and
  use byte-exact comparison.
- `cyrillic/` is a v0.8 forward-compat baseline for **F-08-3** (Cyrillic legacy encodings)
  and **Q-08-1** (hard-PDF corpus expansion).
