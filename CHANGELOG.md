# Changelog

All notable changes to GoPDF are documented here (from v0.6.0 onward).
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## v0.6.9 — 2026-06-06

### Added

- **`Reader.Warnings()` — extraction diagnostics** — returns deterministic, deduplicated warnings for extraction problems that previously degraded silently: a missing or unparseable `/ToUnicode` CMap (including `Identity-H/V` CID fonts whose output is not real Unicode), CJK text decoded through an approximate charset fallback rather than the font's CMap program, unknown or unexpected `/Encoding` values, unmappable `/Differences` glyph names, font resources missing from a page, and unsupported stream filters (such as `/Crypt`) that silently empty a page's text. Each warning carries a stable code, a fixed human-readable message, and a bounded detail string; results are sorted, safe for concurrent use, and identical for the same operations regardless of page order or repetition — so indexing/RAG pipelines get confidence signals without parsing logs. Storage is bounded against adversarial documents (4096 entries with a `warnings_truncated` sentinel; detail strings are size-clamped).

### Changed

- **`DebugOn` documentation** now points to `Reader.Warnings` for programmatic diagnostics; a stray unconditional debug print in dictionary parsing now respects `DebugOn`.

---

## v0.6.8 — 2026-06-06

### Added

- **`Reader.XMP()` — raw XMP metadata** — returns the catalog's `/Metadata` stream as stored: typically a UTF-8 XMP packet with Dublin Core and custom-namespace fields that the classic `/Info` dictionary cannot carry. The bytes are returned **without validation** — parse them with standard XML tooling. Returns `(nil, nil)` when the catalog has no `/Metadata` entry or the stream is empty, and an **error** (rather than silently truncated data) when a metadata stream exceeds the library's 256 MiB decompression bound. Works transparently on encrypted documents, including `/EncryptMetadata false` files whose metadata is stored in cleartext.
- **`Reader.Fonts()` — document-level font inventory** — returns every distinct font referenced by the document's pages as a `FontInfo` (BaseFont name, top-level subtype, whether an embedded font program is present, and the 1-based page numbers where it appears). Resources inherited from ancestor page-tree nodes are included, and a font is reported as embedded when any instance of that name in the document carries a font program. Useful for pre-press auditing, accessibility checks, and extraction debugging. Fonts used only inside Form XObject or annotation appearance streams are not listed.

---

## v0.6.7 — 2026-06-06

### Added

- **Concurrent use of a Reader is documented and safe** — after `Open`/`NewReader` returns, the methods of `Reader` (and of the `Value`, `Page`, and `Outline` trees it produces) are safe for concurrent use by multiple goroutines, so pages of one document can be extracted in parallel from a single Reader. Post-open state is read-only except for an internal bounded cache, which synchronizes itself.
- **Per-class crypt filters for encrypted PDFs** — encrypted files whose stream and string classes use different crypt filters (`StmF ≠ StrF`), the pass-through `/Identity` filter, and the RC4 crypt filter inside V=4 encryption (`/CFM /V2`, common in Acrobat 6-era files) now open and decrypt; all three configurations were previously rejected as unsupported. Malformed crypt-filter entries fail closed rather than silently passing encrypted data through undecrypted.
- **Cleartext-metadata encrypted PDFs (`/EncryptMetadata false`)** — files encrypted with "don't encrypt metadata" (qpdf `--cleartext-metadata`, Acrobat's equivalent option) previously failed to open with AES-128 (wrong key derivation) and returned corrupted XMP metadata in every other mode. The key derivation now accounts for the flag and the metadata stream is returned verbatim. Verified against qpdf-generated AES-128 and AES-256 files with user, owner, and wrong passwords.
- **LZWDecode stream filter** — PDFs compressed with LZW (common in pre-Flate-era documents) previously failed with `unsupported PDF filter`; both `/EarlyChange` conventions now decode, verified against the Go standard library and qpdf as independent references.
- **Full predictor support for FlateDecode and LZWDecode** — all PNG row predictors (None/Sub/Up/Average/Paeth, with `/Colors` and `/BitsPerComponent` honored) and TIFF horizontal differencing now decode. Previously only PNG "Up" rows were accepted, and streams that legally mix row types — which many encoders emit — were rejected as malformed.
- **SASLprep password normalization for AES-256 PDFs** — passwords containing ligatures, combining accents, or exotic spaces now derive the correct key regardless of how the platform encoded the input (NFKC normalization plus the RFC 4013 character mappings). Previously only the literal byte sequence matched.
- **RunLengthDecode and ASCIIHexDecode stream filters** — PDFs whose content streams use either filter previously failed with `unsupported PDF filter`; both now decode per the spec's end-of-data and padding rules, so text extraction works on these files.
- **PDF 2.0 files open** — the `%PDF-2.0` header (ISO 32000-2) is now accepted; such files previously failed with `not a PDF file: invalid header`. Versions 1.0–1.7 behave exactly as before.
- **Hybrid-reference files** — PDFs written for backward compatibility with pre-1.5 readers carry a supplemental cross-reference stream (`/XRefStm`) alongside the classic xref table. It is now read, so objects stored in object streams — previously hidden and silently resolved to null in such files — extract correctly.
- **Owner password unlocks legacy encrypted files** — RC4- and AES-128-encrypted PDFs (encryption V≤4) now open with the owner password as well as the user password, matching the existing AES-256 behavior. Verified against Ghostscript-generated encrypted files covering 40- and 128-bit RC4, and a qpdf-generated AES-128 file covering the V=4 path end-to-end.

### Changed

- **Object resolution is cached** — the Reader now memoizes resolved objects and decoded object streams in a bounded internal cache (entry and byte caps; entries are evicted, never invalidated, since a PDF is immutable while open). Repeated extraction from the same Reader runs up to 16× faster with ~95% less allocation, and workloads that dereference every object in the file (forms, attachments, whole-document dumps) decode each object stream once instead of once per resident object — 3–4× faster with up to 86% less allocation on object-stream-heavy files. Single-pass text extraction is unchanged (within ±2%). Truncated or corrupt object streams are never cached, so malformed-file behavior is byte-identical to before.

---

## v0.6.6 — 2026-06-05

### Added

- **`Page.Words()`** — extract a page's text as individual words with tight bounding boxes, in reading order (left-to-right, top-to-bottom). Each `Word` carries its text plus an (X, Y, width, height) box in PDF coordinate space. Words split on spaces and inter-glyph gaps, so sub/superscripts and words that span kerning boundaries are handled correctly. Useful for search-result highlighting, RAG chunking, and layout-aware extraction.
- **`Page.Annotations()` and `Reader.Dest()`** — read a page's annotations and resolve named destinations. `Page.Annotations()` returns every annotation on a page as a structured `Annotation` value carrying its type, rectangle, link URI, GoTo target page, and `/Contents` text: `/Link` annotations expose their URI-action target or resolve an internal GoTo jump (an explicit destination array, a `/Names/Dests` name-tree entry, or a direct `/Dest`) to a 1-based page number, while `/Text` notes expose their comment body. `Reader.Dest(name)` resolves a named destination to a 1-based page number, returning the new `ErrDestNotFound` sentinel when the name is absent. Useful for crawling hyperlinks, extracting citation URLs, and following table-of-contents jumps. (Name-object destinations and the legacy catalog `/Dests` dictionary are not yet resolved.)

### Security

- **Security:** opening a malformed PDF can no longer hang the process or exhaust memory at open time. A crafted cross-reference table or stream with a cyclic `/Prev` chain previously looped forever, and an xref stream with an oversized `/W` array (e.g. `[1e9, 1e9, 1e9]`) could trigger a multi-gigabyte allocation; both are now rejected and `OpenBytes` / `NewReader` returns a clean error.
- **Security:** reading pages, inherited page attributes (e.g. `MediaBox`, `Resources`), the document outline, or compressed objects from a malformed PDF can no longer hang or crash the process. Cyclic or pathologically deep page-tree (`/Kids`), `/Parent`, object-stream (`/Extends`), and outline (`/First`, `/Next`) chains are now depth-bounded and degrade to a best-effort result instead of looping forever or overflowing the stack.
- **Security:** reading a malformed PDF through a public getter — a page (`Reader.Page`), the page count (`Reader.NumPage`), the trailer, the outline, or a page's fonts (`Page.Fonts`/`Font`) — can no longer crash the process. Previously a single corrupt or unparseable indirect-object body let a parser panic escape these getters; such a body now degrades to a null/empty result, while a structurally broken file still fails `OpenBytes` / `NewReader` exactly as before. A composite `Value` taken from an `Interpret` callback also no longer crashes when `.Key` / `.Index` follows an indirect reference through it.
- **Security:** a successfully-opened malformed PDF can no longer cause a CPU hang during post-open extraction via an inflated count or a cyclic object-stream chain. An object stream with an oversized `/N` entry count, or a page tree whose root `/Pages /Count` is wildly inflated (e.g. near `2^63`), previously drove an effectively unbounded loop when resolving a compressed object or building the page map (reading the outline, page count, or full text). Both counts are now bounded — the object-stream index scan is confined to the index section (so an over-claimed `/N` cannot tokenize object-body bytes or false-match an id from them), a cyclic `/Extends` object-stream chain is detected instead of re-scanned, and the page-count loops are clamped and skip past isolated broken nodes — so extraction stays a bounded best-effort result.
- **Security:** the object-stream index scan no longer resolves the wrong object via integer narrowing. The lookup compared `uint32(id)` against the requested object number, so a crafted index entry whose signed or oversized id narrows to the target (for example `-4294967289`, which narrows to object 7) could resolve an object the index never named and return attacker-selected bytes. The lookup now matches only an in-range, non-narrowing id and rejects negative or overflowing entry offsets.

### Changed

- Text extraction from CJK PDFs — and any document whose fonts use a `/ToUnicode` CMap — is now dramatically faster and far lighter on memory. A font's character map is parsed once per font instead of being re-parsed on every text-font (`Tf`) operator; on a 22-page Traditional Chinese document this cut extraction time by ~19×, memory use by ~51×, and allocations by ~29×.
- **Text inside Form XObjects now reports page-space coordinates.** `Content()` and `Words()` previously interpreted a Form XObject with the identity transform, so its text came back at form-local coordinates — ignoring both the form's `/Matrix` and the transform in effect where the form was invoked (`Do`). Those are now concatenated, so the `X`, `Y`, and `FontSize` of XObject text reflect its real position on the page. This is a correctness fix, but code that depended on the previous form-local values will see different coordinates for text drawn inside forms. (`GetTextByRow` / `GetTextByColumn` use a separate code path that does not track coordinate transforms, so their XObject coordinates are unchanged.)

### Fixed

- Text drawn after a `Q` (restore-graphics-state) operator is now decoded with the correct font's encoding. The interpreter restored the current font on `Q` but not its text encoder, so when a `q … Tf … Q` block changed the font and the following text relied on the restored outer font, `Content()` / `Texts` / `GetStyledTexts` / `Words` decoded those characters through the inner block's encoder and returned garbled `Text.S` (while `Text.Font` still named the correct outer font). The encoder is now saved and restored together with the font.

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
