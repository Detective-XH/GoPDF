# Changelog

All notable changes to GoPDF are documented here (from v0.6.0 onward).
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## v0.6.5 — 2026-06-04

### Added

- **AES-256 encryption** — GoPDF now opens PDFs encrypted with the modern AES-256 Standard security handler (encryption V=5, revisions R=5 and R=6, as produced by Acrobat 9+ and required by PDF 2.0 / ISO 32000-2). Such files are decrypted transparently through `OpenBytes` / `NewReaderEncrypted` using the empty, user, or owner password — no new API. Previously they failed with `unsupported PDF: encryption version V=5`.

### Security

- **Security:** `applyFilter` no longer panics on a negative FlateDecode `/Columns` value — a crafted `/Columns -2` previously triggered an invalid slice allocation when the stream was read; negative values are now rejected.
- **Security:** AES-CBC decryption now fully validates PKCS#7 padding (every padding byte, in constant time) instead of checking only the final byte, rejecting malformed padding that was previously accepted as valid.
- **Security:** AES stream decryption now bounds its in-memory read and returns an explicit error on an oversized stream instead of silently truncating it, preventing unbounded memory use on crafted PDFs.
- **Security:** CMap parser no longer panics on a codespace range entry with a key longer than 4 bytes — the malformed entry is now rejected and parsing stops gracefully instead of indexing past the end of an internal fixed-size array.
- **Security:** CMap parser no longer panics when a bfrange destination is an empty string (`<>`) — affected character codes now map to a replacement rune instead of triggering an out-of-bounds slice index.
- **Security:** `Content()` no longer panics on malformed PDF content streams — operator calls with wrong argument counts (e.g. a `Td` with one operand instead of two) are now caught and return whatever text and rectangles were extracted before the fault, preventing denial-of-service via crafted PDFs.
- **Security:** `Content()` no longer panics when a `Q` (restore graphics state) operator appears with no matching `q` (save) — the unmatched restore is silently skipped and parsing continues, so subsequent content in the same stream is still extracted.

### Fixed

- `GetTextByRow` / `GetTextByColumn` no longer panic when a PDF content stream contains a `Tm` (text-matrix) operator with fewer than 6 operands — the malformed operator is now silently skipped and extraction continues.

### Changed

- `Text.S`, `Content`, `Content()`, `GetPlainText`, `GetTextByColumn`, and `GetTextByRow` now document that extracted text is returned as verbatim UTF-8 with no escaping applied — callers are responsible for escaping at their output sink before embedding in HTML, shell commands, or any other context-sensitive environment (e.g. `html.EscapeString` for HTML output).

---

## v0.6.4 — 2026-06-03

### Security

- **Security:** Password verification in `verifyEncryptKey` now uses constant-time comparison — eliminates a timing side-channel that could have allowed password oracle attacks.
- Per-object encryption key is now correctly truncated per PDF spec §7.6.2 — decryption no longer produces garbage output for PDFs encrypted with keys shorter than 128 bits (40-bit or 64-bit RC4).
- `decryptString` now strips PKCS7 padding after AES-CBC decryption — previously every AES-encrypted string had 1–16 trailing garbage bytes, silently corrupting dict lookups and string comparisons.
- `decryptStream` AES branch now handles PKCS7 padding correctly and returns an error on cipher failure instead of silently passing raw encrypted bytes to callers.

### Fixed

- `GetPlainText` no longer errors on PDFs that use the `"` (double-quote) text-show operator — the operator previously panicked or emitted a word-spacing number instead of the text string.

---

## v0.6.3 — 2026-06-02

### Changed
- Renamed project from `pdf` to **GoPDF** — module path is now `github.com/Detective-XH/gopdf`; update your import paths accordingly. The package name remains `pdf`.

### Fixed
- TJ kerning arrays now correctly insert word spaces when the kern gap indicates a word boundary, fixing silent word concatenation in `Content()`, `GetStyledTexts()`, `GetTextByRow()`, and `GetTextByColumn()`.

---

## v0.6.2 — 2026-06-02

### Changed
- `encoderForCMapName` switch replaced with a package-level lookup map — all 30 CMap name keys preserved, no behaviour change.
- `gofmt -s` applied across the codebase.

---

## v0.6.1 — 2026-06-02

### Added
- `Pages() iter.Seq2[int, Page]` and `Texts() iter.Seq[Text]` — lazy range-over-func iterators for streaming page and text access (Go 1.23+).
- `Page.MediaBox()` and `Page.CropBox()` — page dimension accessors; `CropBox` falls back to `MediaBox` when absent.

### Fixed
- **Security:** CMap parser hardened against malformed input — panics converted to recoverable errors; entry count capped at 65,536 to prevent resource exhaustion.
- **Security:** Cross-reference stream `Size` field is now bounds-checked before allocation — prevents memory exhaustion from crafted PDFs.
- **Security:** FlateDecode streams limited to 256 MB — prevents decompression bombs.
- **Security:** AES decryption block alignment validated before decoding — prevents panic on malformed ciphertext.
- **Security:** Cross-reference object numbers capped at the PDF spec maximum (8,388,607).
- **Security:** Maximum object nesting depth enforced to prevent stack overflow.
- Form XObject text extraction — content inside Form XObjects is now included in all extraction paths ([#67](https://github.com/ledongthuc/pdf/issues/67), [#26](https://github.com/ledongthuc/pdf/issues/26)).

### Internal
- Large-scale file decomposition (god-file split of `lex.go`, `xref.go`, `value.go`, `read.go`, `page.go`, `cmap.go` into focused units) and cyclomatic-complexity reduction across the parser; Go idiom modernization. No behaviour change.

---

## v0.6.0 — 2026-06-02

### Added
- CJK text extraction: Shift-JIS, UCS-2 BE, GBK / GB-EUC / GBKp-EUC, Big5-ETen / ETenms, UHC / KSC-EUC / UHC-HW CMap decoders.
- Document metadata API (`r.Info()`) — title, author, creation date, modification date, and other `/Info` fields.
- Outline page numbers (`Outline.Page`) resolved to 1-based page numbers.
- Context/cancellation support on `GetPlainText` and `GetStyledTexts`.

### Fixed
- Per-page font map in `GetPlainText` — stale encoder reuse across pages with the same font name is fixed ([#60](https://github.com/ledongthuc/pdf/issues/60)).
- Inline image crash and CPU spin — PDFs with inline images no longer hang or crash ([#57](https://github.com/ledongthuc/pdf/issues/57)).
- Text position tracking — `GetTextByRow` / `GetTextByColumn` X/Y coordinates were always 0; `Td`, `TD`, `T*`, `TL`, and `BT` operators are now tracked correctly ([#18](https://github.com/ledongthuc/pdf/issues/18), [#27](https://github.com/ledongthuc/pdf/issues/27)).
- `%%EOF` search window expanded with a fallback backward scan — PDFs with appended digital-signature blocks now parse correctly ([#20](https://github.com/ledongthuc/pdf/issues/20)).
- Spurious `\n` from `BT` operator in `GetPlainText` removed ([#48](https://github.com/ledongthuc/pdf/issues/48)).
- Text sort stability — equal-Y glyphs preserve document stream order ([#16](https://github.com/ledongthuc/pdf/issues/16)).
- PDF header accepts space or tab after the version string — unblocks output from tools such as libtiff/tiff2pdf ([#22](https://github.com/ledongthuc/pdf/issues/22)).
- Zero-length zlib streams no longer trigger decompression errors.
- `ToUnicode` CMap now checked before `Encoding` in font decoder selection — fixes garbled text on some PDFs.
- Chinese and Korean CJK decoding: GBK, Big5, UniGB-UCS2-H, UniCNS-UCS2-H, UniKS-UCS2-H now decode correctly ([#44](https://github.com/ledongthuc/pdf/issues/44), [#55](https://github.com/ledongthuc/pdf/issues/55), [#21](https://github.com/ledongthuc/pdf/issues/21)).
- CJK crash on mixed CJK/Latin PDFs — `dictEncoder` rewritten to handle the collision ([#30](https://github.com/ledongthuc/pdf/issues/30)).
- `GetTextByRow` no longer returns disordered or empty rows ([#16](https://github.com/ledongthuc/pdf/issues/16), [#27](https://github.com/ledongthuc/pdf/issues/27)).
- Multi-stream arrays use `io.MultiReader` — prevents out-of-memory on large PDFs.

> `OpenBytes([]byte)` (in-memory PDF parsing) shipped in **v0.5.0**, before this changelog's coverage begins.
