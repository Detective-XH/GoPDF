# Filled AcroForm fixture — search (negative result, 2026-06-11)

This note records a multi-channel search for a real-world fixture that would exercise
`Reader.Fields()` against a **genuinely *filled* public-domain AcroForm** — a form a
human actually completed, with real entered values. It is durable audit knowledge: the
search came up empty, so the *filled* half of the AcroForm-corpus-fixture gate stays
**won't-promote-unless-reported**, and this file exists so the question does not have to
be re-investigated from scratch.

The **structural** half of the gate *did* ship: two real public-domain US-Gov forms,
both **blank**, now lock `Reader.Fields()` against real field-tree structure, qualified
names, types, page attribution, and generator quirks — see `irs-f1040-2025.pdf` (199
fields, Adobe LiveCycle deep dotted names, maxDepth 6) and `uscourts-cv071-civil-cover.pdf`
(165 fields, real `/Parent` tree + `/DA` chains), locked by `TestCorpusFormFixtures`. What
remains unobtainable is a *filled* one.

## What would qualify

A real form whose AcroForm carries **entered data**, that we may redistribute. Precisely,
**all** of:

- a live `/AcroForm` with terminal fields (`Reader.Fields()` returns > 0); **and**
- **genuinely filled** — at least some fields hold real entered content: non-empty text,
  a *checked* box / chosen radio / selected choice. The decisive metric is
  **`realFilled`** = fields whose `Value != "" && Value != "Off"` (see Reproduction); **and**
- **public-domain or otherwise redistributable** — `testdata/` is curated public-domain
  content only, and the pre-commit PII scan *skips* `testdata/`, so this is a hard gate; **and**
- **no PII** in the field values or document metadata (`/Info`, XMP).

### What does NOT qualify (and why)

| Pattern | `Reader.Fields()` signature | Why it is not the gap |
|---|---|---|
| Blank fillable form (the default agency download) | `total > 0`, `realFilled = 0` (empty text + `Off` checkboxes) | Not filled. The `/V` value-decode path is untested by it; that path is already covered by the synthetic `forms_test.go` cases. |
| "Completed sample" published by an agency | `total = 0` | Almost always **flattened** — the entered data is burned into page content and the form fields are stripped. No live AcroForm to walk. |
| Real filed document (court/EDGAR/regulations.gov) | `total = 0` (flattened) or scanned image | Filing systems flatten or scan for finality; and a private filer's data is neither US-Gov work nor PII-free. |
| XFA (Adobe LiveCycle dynamic) form | `total > 0`, `realFilled ≈ 0` | The data lives in the XFA XML stream, not the classic AcroForm `/V`; `Reader.Fields()` (AcroForm-only, by design) sees empty fields. |
| Any genuinely-filled real form | — | Carries PII (names, SSNs, addresses) and is not public-domain. This is the binding reason filled+PD do not co-occur. |

## Search scope and result

Four channels, fanned out 2026-06-11. Every candidate was downloaded and measured with
the `Reader.Fields()` harness (Reproduction, below); the gate-relevant column is
**realFilled** (entered data), **not** raw non-empty-`/V` count — an early version of the
harness counted unchecked-checkbox `"Off"` defaults as "filled" and produced false
positives (every blank form looked filled). Correcting to `realFilled` exposed the truth.

| Channel | Sources swept | Candidates measured | realFilled > 0 with real data | Qualifiers |
|---|---|---:|---:|---:|
| A — US federal judiciary | uscourts.gov (bankruptcy / AO / district forms; CACD CV-series) | ~5 | 0 | 0 |
| B — US federal agencies | IRS, SSA, VA, USCIS, GSA, OPM | ~7 | 0 | 0 |
| C — OSS project test corpora | mozilla/pdf.js, py-pdf/pypdf, py-pdf/sample-files, pdfminer.six, pdfplumber, pikepdf | ~12 | 0 | 0 |
| D — court records / Wikimedia / open data | CourtListener·RECAP, Wikimedia Commons, regulations.gov, SEC EDGAR, vendor samples | ~12 | 0 | 0 |
| (local) user-supplied documents | 2 personal files offered as candidates | 2 | 0 | 0 |
| **Total** | | **~38** | **0** | **0** |

**Result: zero qualifiers.** No public corpus, agency site, court record, or
permissively-licensed project test suite yielded a genuinely-filled AcroForm that is also
redistributable. The constraint *filled + public-domain + live (not flattened/XFA/scanned)*
is structurally absent in the wild — a consequence of how forms are distributed (blank
templates) and completed (flattened, and full of PII).

## Informative near-misses

All were measured; each is disqualified for a concrete reason. (`realFilled` values shown.)

- **`USCIS Form I-130`** (uscis.gov, US-Gov PD) — `total 450, realFilled 14`. The 14
  non-`Off` values are **PDF417 barcode strings** baked into the template
  (`"I-130|04/01/24|N"`, one per page) plus a couple of metadata fields — *template data*,
  not anything a user entered. Blank in substance.
- **`VA Form 20-0995`** (vba.va.gov, US-Gov PD) — `total 136, realFilled 5`. The 5 are
  `RadioButtonList = "Yes"` **template defaults**, not user selections.
- **`uscourts CV-071`** (cacd.uscourts.gov, US-Gov PD) — `total 165, realFilled 1`. The one
  non-`Off` value is a Combo holding a single space `" "`. Committed anyway as a *blank
  structural* fixture (its value lies in the real `/Parent` tree + `/DA` chains, not in data).
- **`SF424_page2.pdf`** (pypdf `resources/`, license unclear) — `realFilled 7`, but the
  values are spaces and a single agency name; not meaningful entered data, and provenance
  is not clean PD.
- **Two user-supplied local documents.** One was a browser print-to-PDF of a government web
  confirmation page — **not an AcroForm at all** (`total 0`). The other was a DocuSign-signed
  contract that had been **flattened** (`total 1`, empty) — the visible "filled" data is
  static page content, the form fields are gone. Both also carry personal data and are not
  public-domain, so neither is usable; recorded only as evidence that even real *completed*
  documents hit the same flattened / non-AcroForm wall.

## What real "completed" forms actually are

Across every channel, a document that *looks* completed was, in order of frequency:

1. **Flattened** — entered data rendered into the content stream, form fields stripped
   (`total = 0`). The norm for court filings and agency "sample completed" PDFs.
2. **Blank fillable** — live fields, no data (`realFilled = 0`). The norm for agency
   downloads.
3. **XFA dynamic** — data in the XFA XML, AcroForm `/V` empty (`realFilled ≈ 0`).
4. **Scanned image** — no live fields at all.

A genuinely-filled, non-flattened, classic-AcroForm document with real values exists only
as someone's private paperwork — which is, by definition, PII-bearing and not public-domain.

## Implication

The *filled* AcroForm fixture stays gated, **won't-promote-unless-reported**. Its remaining
blocker is a qualifying sample, and this search shows one is not obtainable from public
sources. Promotion would require either (a) a user-reported real document that is both
filled and redistributable (vanishingly unlikely, since filled implies PII), or (b) an
explicitly *synthetic* / self-filled fixture — which would validate the `/V` value-decode
plumbing but, being hand-made, would not be the real-world evidence the gate asks for, and
that plumbing is already covered by `forms_test.go`. The higher-value follow-up is the one
already taken: ship the **blank** real forms for the *structural* risk (field-tree
inheritance, `/DA` chains, generator quirks) the synthetic fixtures cannot capture.

## Reproduction

For any candidate PDF `f` (tools: the GoPDF `pdf` package, qpdf, pdfinfo):

```go
// realFilled = entered data; "Off" is the spec default for an UNCHECKED box, not data.
data, _ := os.ReadFile(f)
r, _ := pdf.OpenBytes(data)
fields, _ := r.Fields()
total, realFilled := len(fields), 0
for _, fld := range fields {
    if fld.Value != "" && fld.Value != "Off" {
        realFilled++ // text content, a checked box, or a chosen option
    }
}
// QUALIFIES only if realFilled > 0 with values that are real entered data
// (not barcodes, single spaces, or template defaults).
```

```sh
# structure: live AcroForm? flattened? XFA? field-tree depth / DA chains?
qpdf --qdf --object-streams=disable "$f" - 2>/dev/null | grep -ac '/AcroForm'
qpdf --qdf --object-streams=disable "$f" - 2>/dev/null | grep -ac '/XFA'      # >0 => XFA, /V likely empty
qpdf --qdf --object-streams=disable "$f" - 2>/dev/null | grep -ac '/Parent'   # /Parent tree depth
qpdf --qdf --object-streams=disable "$f" - 2>/dev/null | grep -ac '/DA'       # default-appearance chains
# PII gate (testdata's pre-commit scan SKIPS this dir — check by hand):
pdfinfo "$f" | grep -iE '^Author'                                            # person name? -> strip or reject
grep -aoiE '[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}' "$f" | sort -u             # emails? -> reject
# If committing a real form, strip metadata first:
#   qpdf --remove-info --remove-metadata "$f" clean.pdf
```
