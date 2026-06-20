package pdf

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCorpusBeaDiciNoReplacementRunes asserts that the full plain-text extraction
// of hard/bea-dici0724.pdf contains ZERO U+FFFD (replacement rune) characters
// across all 12 pages.
//
// Why this test complements the golden snippet check in TestCorpusGolden:
//
// assertGolden / compareNormalized checks that each golden snippet is a substring
// of the normalized extraction — it cannot detect U+FFFD that appears in body or
// table regions NOT covered by those snippets. A future regression that
// reintroduces mis-decoded glyphs in uncovered spans would leave TestCorpusGolden
// green while silently corrupting output.
//
// This test locks the document-wide invariant: zero replacement runes. Any
// regression that reintroduces U+FFFD anywhere in the 12 pages will fail here,
// regardless of which page or span it touches.
//
// The invariant is the direct product of two fixes in plaintext.go:
//   - q/Q graphics-state save/restore (gstack): prevents the inner encoder from
//     bleeding past Q, which caused text shown after Q to be decoded through the
//     wrong 2-byte CMap → U+FFFD.
//   - writeSeparator noRune guard: prevents the T* line-move separator ("\n")
//     from being decoded through a 2-byte-codespace CMap; without the guard,
//     the 1-byte "\n" fails codespace lookup and returns noRune (U+FFFD).
func TestCorpusBeaDiciNoReplacementRunes(t *testing.T) {
	t.Parallel()
	// Locate the manifest entry — iterate rather than hard-coding an index so a
	// future manifest reorder cannot silently skip the fixture.
	var entry corpusEntry
	found := false
	for _, e := range corpusManifest {
		if e.Path == "hard/bea-dici0724.pdf" {
			entry = e
			found = true
			break
		}
	}
	if !found {
		t.Fatal("hard/bea-dici0724.pdf not found in corpusManifest — was it removed or renamed?")
	}

	r := loadCorpus(t, entry)
	got := extractPlainText(t, r)

	// Scan for the first U+FFFD and report it with surrounding context.
	const replacementRune = '�'
	idx := strings.IndexRune(got, replacementRune)
	if idx < 0 {
		return // invariant holds — no replacement runes anywhere
	}

	// Build a context window around the first occurrence for diagnosis.
	const windowBytes = 40
	lo := max(idx-windowBytes, 0)
	hi := min(idx+utf8.RuneLen(replacementRune)+windowBytes, len(got))
	// Ensure lo/hi are at valid UTF-8 boundaries.
	for lo > 0 && !utf8.RuneStart(got[lo]) {
		lo--
	}
	for hi < len(got) && !utf8.RuneStart(got[hi]) {
		hi++
	}
	context := got[lo:hi]

	t.Errorf(
		"hard/bea-dici0724.pdf: U+FFFD found at byte offset %d — "+
			"the q/Q encoder-restore or T* separator noRune guard has regressed.\n"+
			"Context (±%d bytes): %q",
		idx, windowBytes, context,
	)
}
