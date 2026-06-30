package pdf

import (
	"strings"
	"testing"
)

// wc905 is a helper that encodes raw byte codes as a string and runs decodeWalkmanC905.
func wc905(codes ...int) string {
	b := make([]byte, len(codes))
	for i, c := range codes {
		b[i] = byte(c)
	}
	return decodeWalkmanC905(string(b), &byteEncoder{&winAnsiEncoding})
}

// TestDecodeWalkmanC905 verifies the SIL W-C-905 transducer against the six byte→glyph
// anchors confirmed by direct PDF READ (§2 of plans/LEGACY-WALKMAN-C905-PLAN.md)
// and two composed-word assertions derived from those same anchors.
func TestDecodeWalkmanC905(t *testing.T) {
	t.Run("anchor_r_ta", func(t *testing.T) {
		// 0x72 → U+0924 (त TA) — ByteClass[Cons] index 12
		got := wc905(0x72)
		if got != "त" {
			t.Errorf("0x72: got %q (%U), want %q", got, []rune(got), "त")
		}
	})
	t.Run("anchor_k_aa_matra", func(t *testing.T) {
		// 0x6B → U+093E (ा AA-matra) — single-byte rule
		got := wc905(0x6B)
		if got != "ा" {
			t.Errorf("0x6B: got %q (%U), want %q", got, []rune(got), "ा")
		}
	})
	t.Run("anchor_f_short_i", func(t *testing.T) {
		// 0x66 → U+093F (ि short-i LeftMark) — single-byte rule
		got := wc905(0x66)
		if got != "ि" {
			t.Errorf("0x66: got %q (%U), want %q", got, []rune(got), "ि")
		}
	})
	t.Run("anchor_y_la", func(t *testing.T) {
		// 0x79 → U+0932 (ल LA) — ByteClass[Cons] index 21
		got := wc905(0x79)
		if got != "ल" {
			t.Errorf("0x79: got %q (%U), want %q", got, []rune(got), "ल")
		}
	})
	t.Run("anchor_d_ka", func(t *testing.T) {
		// 0x64 → U+0915 (क KA) — ByteClass[Cons] index 0
		got := wc905(0x64)
		if got != "क" {
			t.Errorf("0x64: got %q (%U), want %q", got, []rune(got), "क")
		}
	})
	t.Run("anchor_l_sa", func(t *testing.T) {
		// 0x6C → U+0938 (स SA) — ByteClass[Cons] index 25
		got := wc905(0x6C)
		if got != "स" {
			t.Errorf("0x6C: got %q (%U), want %q", got, []rune(got), "स")
		}
	})

	t.Run("composed_taalika", func(t *testing.T) {
		// "rkfydk" (0x72 0x6B 0x66 0x79 0x64 0x6B) → तालिका
		// Pass1: त ा ि ल क ा
		// Pass3 rule2: ि before ल → ल ि  (offset 2,3 after consuming त,ा)
		// Final: त ा ल ि क ा = तालिका
		got := wc905(0x72, 0x6B, 0x66, 0x79, 0x64, 0x6B)
		want := "तालिका"
		if got != want {
			t.Errorf("rkfydk: got %q (%U), want %q (%U)", got, []rune(got), want, []rune(want))
		}
	})

	t.Run("composed_sakal", func(t *testing.T) {
		// "ldy" (0x6C 0x64 0x79) → सकल
		// Pass1: स क ल  (all in ByteClass[Cons], no multi-byte rule fires)
		// No nukta pairs. No reorder needed (all Cons, no LeftMark or Reph).
		got := wc905(0x6C, 0x64, 0x79)
		want := "सकल"
		if got != want {
			t.Errorf("ldy: got %q (%U), want %q (%U)", got, []rune(got), want, []rune(want))
		}
	})

	t.Run("pass2_nukta_ja", func(t *testing.T) {
		// U+091C (ज JA) + U+093C (nukta) → U+095B (ज़ precomposed) via Pass2 coalesce.
		// Byte route: 0x74 (ja via ByteClass[Cons]) + 0x2B (nukta).
		// The code emits U+095B (the precomposed form), not U+091C+U+093C.
		got := wc905(0x74, 0x2B)
		want := "ज़" // ज़  U+095B precomposed
		if got != want {
			t.Errorf("0x74 0x2B: got %q (%U), want %q (%U)", got, []rune(got), want, []rune(want))
		}
	})

	t.Run("pass3_reph", func(t *testing.T) {
		// ByteClass[Cons]=a + 0x6A(ra) + 0x7E(virama) → RA VIRAMA A  (reph to pre-base)
		// i.e. a base consonant followed by RA+VIRAMA gets reph prepended.
		// Example: 0x64 (ka) + 0x6A (ra) + 0x7E (virama) → र् क = reph form of क
		got := wc905(0x64, 0x6A, 0x7E)
		// Pass1: ka(U+0915) ra(U+0930) virama(U+094D)
		// Pass3 rule6: [Cons]=ka U+0930 U+094D → U+0930 U+094D ka
		want := "र्क"
		if got != want {
			t.Errorf("reph: got %q (%U), want %q (%U)", got, []rune(got), want, []rune(want))
		}
	})

	t.Run("two_byte_fa", func(t *testing.T) {
		// 0x69 0x51 → फ  (two-byte rule overrides Cons-zip single-byte 0x69→प)
		got := wc905(0x69, 0x51)
		want := "फ"
		if got != want {
			t.Errorf("0x69 0x51: got %q (%U), want %q (%U)", got, []rune(got), want, []rune(want))
		}
	})

	t.Run("three_byte_longer_match", func(t *testing.T) {
		// 0x69 0x51 0x2B → फ़  (3-byte overrides 2-byte 0x69 0x51 → फ)
		got := wc905(0x69, 0x51, 0x2B)
		want := "फ़" // फ़  U+095E precomposed
		if got != want {
			t.Errorf("0x69 0x51 0x2B: got %q (%U), want %q (%U)", got, []rune(got), want, []rune(want))
		}
	})

	t.Run("devdigits", func(t *testing.T) {
		// Devdigits zip: 0xFA→0, 0xFB→1, 0xFC→2 in Devanagari
		got := wc905(0xFA, 0xFB, 0xFC)
		want := "०१२"
		if got != want {
			t.Errorf("devdigits: got %q, want %q", got, want)
		}
	})

	t.Run("no_zwj_in_output", func(t *testing.T) {
		// Any byte that produces ZWJ/ZWNJ should not appear in the output.
		// Full decode of "rkfydk" (the tālika word) must not contain ZWJ/ZWNJ.
		got := wc905(0x72, 0x6B, 0x66, 0x79, 0x64, 0x6B)
		if strings.ContainsRune(got, 0x200D) || strings.ContainsRune(got, 0x200C) {
			t.Errorf("output contains ZWJ/ZWNJ: %q", got)
		}
	})
}
