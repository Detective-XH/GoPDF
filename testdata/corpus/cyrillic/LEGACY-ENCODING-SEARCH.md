# Legacy Cyrillic named-encoding — fixture search (negative result, 2026-06-09)

This note records a corpus-wide search for a real-world fixture that would exercise
**fallback decoding of a legacy Cyrillic code page declared by name** (Windows-1251,
KOI8-R, KOI8-U, ISO-8859-5, MacCyrillic). It is durable audit knowledge: the search
came up empty, and this file exists so the question does not have to be
re-investigated from scratch.

## What would qualify

A font whose text would today decode incorrectly *only* because GoPDF has no table
for a named legacy Cyrillic code page. Precisely, **all** of:

- the font's `/Encoding` is a bare **name** (not a dictionary), **or** an `/Encoding`
  dictionary whose `/BaseEncoding` is a name; **and**
- that name is **not** one of the encodings GoPDF already resolves —
  `WinAnsiEncoding`, `MacRomanEncoding`, `StandardEncoding`, `PDFDocEncoding`,
  `MacExpertEncoding`, `Identity-H`/`Identity-V`, or the predefined CJK CMaps
  (`90ms-RKSJ-*`, `Uni{GB,CNS,JIS,KS}-UCS2-*`, `GB*-EUC-*`, `ETen-B5-*`, `KSC*-*`);
  **and**
- the name denotes a Cyrillic code page (e.g. `Cyrillic`, `CP1251`, `Windows-1251`,
  `WinCyrillic`, `KOI8-R`, `KOI8-U`, `ISOLatinCyrillic`, `ISO8859-5`,
  `MacCyrillicEncoding`); **and**
- that font has **no** `/ToUnicode`.

### What does NOT qualify (and why)

| Pattern | Why it is not the gap |
|---|---|
| `/ToUnicode` present | Already extracts correctly (e.g. `udhr-ru.pdf` in this directory). |
| `/Encoding` dict with `/Differences` + standard/absent `/BaseEncoding` | Already handled: the `/Differences` glyph names resolve through the Adobe Glyph List (the name table carries the Cyrillic glyph names), even with no `/ToUnicode`. |
| `/WinAnsiEncoding` declared over Windows-1251 bytes | A misdeclaration, not a missing table. Unfixable without heuristic charset guessing, which is out of scope under the deterministic-extraction constraint. Informative, not addressable. |

## Search scope and result

Swept the test corpora of the mature, permissively-licensed PDF libraries plus a
hand-checked sweep of old public-domain non-Russian Cyrillic documents on the open web.

| Source | License | PDFs inspected | Cyrillic PDFs | Qualifiers |
|---|---|---:|---:|---:|
| pdfminer.six (`a18de2a`) | MIT | 56 | 0 | 0 |
| pypdf `resources/` (`c64016a`) | BSD-3-Clause | 92 | 3 | 0 |
| py-pdf/sample-files (`818dc01`) | CC-BY-SA-4.0 | 28 | 0 | 0 |
| Apache PDFBox test resources | Apache-2.0 | 152 | 0 | 0 |
| pdfplumber | MIT | 85 | 0 | 0 |
| mozilla/pdf.js `test/pdfs` | Apache-2.0 (third-party samples) | ~680 sampled | 1 | 0 |
| LaTeX3 test PDFs | LPPL | 4 | 0 | 0 |
| Open web (Bulgarian / Ukrainian / Serbian / Macedonian, public-domain) | various | 7 hand-checked | 4 | 0 |
| **Total** | | **~1,200** | **~12** | **0** |

**Result: zero qualifiers.** No PDF in any public corpus — nor any hand-checked
old non-Russian Cyrillic document — declares a legacy Cyrillic code page by name
without `/ToUnicode`. This confirms the gap is empirically absent in the wild, not
merely unrepresented in one library's tests.

## Informative near-misses

- **`issue20232.pdf`** — mozilla/pdf.js `test/pdfs`, Apache-2.0.
  GOST-font Russian text (139 Cyrillic chars), but the CID font is `Identity-H` with
  `/ToUnicode` — the modern path. Disqualified by `/ToUnicode`.
  `https://raw.githubusercontent.com/mozilla/pdf.js/master/test/pdfs/issue20232.pdf`

- **The Ukrainian Weekly, 2000 No. 35** — QuarkXPress 6.52, 2000; ~22,400 Cyrillic
  chars. The closest real-world legacy non-Russian Cyrillic document found. Its custom
  Svoboda fonts use `/BaseEncoding /MacRomanEncoding` with extensive `/Differences`
  (63 remappings) and **no** `/ToUnicode` — i.e. the **`/Differences` + Adobe Glyph
  List path** (the AGL name table carries the Cyrillic glyph names), not a bare named
  code page. Newspaper content
  (not public domain), so it cannot be imported; recorded only as evidence of what
  legacy Cyrillic PDFs actually do.
  `https://archive.ukrweekly.com/wp-content/uploads/The_Ukrainian_Weekly_2000-35.pdf`

## What legacy Cyrillic PDFs actually use

Across the ~12 Cyrillic PDFs found, the real mechanisms were, in order:

1. **`Identity-H` CID font + `/ToUnicode`** — modern tools (MS Word, InDesign, recent
   Ghostscript). Extracts correctly today.
2. **Custom embedded font + `/Differences` + Adobe Glyph List, no `/ToUnicode`** — legacy
   page-layout tools (QuarkXPress, PageMaker). Supported via the glyph-name path: the
   `/Differences` names resolve through the Adobe Glyph List name table, which carries the
   Cyrillic glyph names (a design fact — not verified end-to-end against a real document
   here; see Implication).
3. **Misdeclared `/WinAnsiEncoding` over Windows-1251 bytes** — the classic corruption;
   unfixable under the deterministic constraint.

A bare named Cyrillic code page (`/Encoding /CP1251`, `/KOI8-R`, …) is non-conformant
PDF, which is why conformant producers never emit it and the addressable gap does not
appear.

## Implication

Fallback decoding for named legacy Cyrillic code pages stays gated. Its remaining
blocker is a qualifying sample, and this search shows one is not obtainable from public
corpora. Promotion would require either (a) a user-reported real document exhibiting the
signature, or (b) an explicitly *synthetic* fixture — which would validate the table
plumbing but, being hand-authored, would not constitute the real-world evidence the gate
asks for. The higher-value Cyrillic follow-up suggested by this search is instead to
validate the existing `/Differences` + Adobe Glyph List path against a real
multi-thousand-glyph Cyrillic document (blocked here only by licensing, not feasibility).

## Reproduction

For any candidate PDF `f` (tools: qpdf, pdffonts, pdftotext, python3):

```sh
# raw named encodings in font dicts
qpdf --qdf --object-streams=disable "$f" - 2>/dev/null \
  | grep -aoE '/(Encoding|BaseEncoding)[[:space:]]*/[A-Za-z0-9._+-]+' | sort | uniq -c
# /Differences and /ToUnicode presence
qpdf --qdf --object-streams=disable "$f" - 2>/dev/null | grep -ac '/Differences'
qpdf --qdf --object-streams=disable "$f" - 2>/dev/null | grep -ac '/ToUnicode'
# per-font encoding + ToUnicode ("uni") column
pdffonts "$f"
# Cyrillic content count (U+0400..U+04FF)
pdftotext "$f" - 2>/dev/null \
  | python3 -c "import sys;t=sys.stdin.read();print(sum(1 for c in t if 0x400<=ord(c)<=0x4FF))"
```
