package pdf

import "strings"

// ligatures is the set of Latin typographic ligatures NormalizeText folds: U+FB00–U+FB06.
// Used as a cheap presence pre-check so the common no-ligature input returns without allocating.
const ligatures = "ﬀﬁﬂﬃﬄﬅﬆ"

// ligatureReplacer decomposes U+FB00–U+FB06 to their ASCII constituents. FB05 (long-s + t)
// and FB06 (s + t) both fold to "st"; see NormalizeText for the FB05 long-s note.
var ligatureReplacer = strings.NewReplacer(
	"ﬀ", "ff",
	"ﬁ", "fi",
	"ﬂ", "fl",
	"ﬃ", "ffi",
	"ﬄ", "ffl",
	"ﬅ", "st",
	"ﬆ", "st",
)

// NormalizeText returns s with the Latin typographic ligatures U+FB00–U+FB06 decomposed
// to their ASCII constituents; every other rune is returned unchanged.
//
//	ﬀ U+FB00 → "ff"   ﬁ U+FB01 → "fi"   ﬂ U+FB02 → "fl"
//	ﬃ U+FB03 → "ffi"  ﬄ U+FB04 → "ffl"  ﬅ U+FB05 → "st"  ﬆ U+FB06 → "st"
//
// It is the opt-in, targeted alternative to blanket Unicode NFKC normalization: NFKC would
// additionally fold non-ligature compatibility forms (½ → 1⁄2, fullwidth digits, superscripts)
// that are usually wrong for extracted text, so GoPDF never normalizes during extraction —
// callers apply NormalizeText explicitly when they want ligatures folded for search/RAG.
//
// U+FB05 (LATIN SMALL LIGATURE LONG S T) folds to "st": its long-s (U+017F) is mapped to "s",
// slightly beyond a pure ligature split, because search/RAG callers want "st". Stand-alone
// long-s (U+017F) elsewhere is untouched — only the FB05 ligature is affected.
//
// NormalizeText performs no allocation when s contains none of U+FB00–U+FB06. It is safe for
// concurrent use and does not touch Reader/Page state.
func NormalizeText(s string) string {
	if !strings.ContainsAny(s, ligatures) {
		return s
	}
	return ligatureReplacer.Replace(s)
}
