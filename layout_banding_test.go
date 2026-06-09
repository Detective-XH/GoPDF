package pdf

import (
	"reflect"
	"testing"
)

// TestBandsByYJitterDeterministic guards the quantized Y sort key + deterministic
// flush tie-break (latent reading-order determinism hardening): three glyphs on
// one visual baseline, given out of x-order with sub-point y-jitter, must group
// into a single band ordered left-to-right, and a second pass over an identical
// input must produce identical bands. The jitter (~1e-3 pt) is far below the
// banding tolerance, so it must never split the line or perturb the ordering.
func TestBandsByYJitterDeterministic(t *testing.T) {
	mk := func() []Text {
		return []Text{
			{S: "c", X: 30, Y: 100.0009, FontSize: 10, W: 8},
			{S: "a", X: 10, Y: 100.0, FontSize: 10, W: 8},
			{S: "b", X: 20, Y: 99.9994, FontSize: 10, W: 8},
		}
	}
	bands := bandsByY(mk())
	if len(bands) != 1 {
		t.Fatalf("jittered co-linear glyphs must form one band, got %d: %v", len(bands), bands)
	}
	got := bands[0][0].S + bands[0][1].S + bands[0][2].S
	if got != "abc" {
		t.Errorf("band order = %q, want left-to-right %q", got, "abc")
	}
	if again := bandsByY(mk()); !reflect.DeepEqual(again, bands) {
		t.Errorf("non-deterministic banding:\n run1 = %v\n run2 = %v", bands, again)
	}
}

// TestJoinNeedsSpace locks the script-aware line-join rule: space-less CJK
// scripts (Han, Hiragana, Katakana) rejoin without a space, but Korean (Hangul)
// and any non-CJK boundary keep their space. Suppressing a Hangul boundary would
// destroy Korean word boundaries (Korean is space-separated), so it must not.
func TestJoinNeedsSpace(t *testing.T) {
	cases := []struct {
		left, right string
		want        bool // true => a space must be inserted
		desc        string
	}{
		{"对", "人", false, "Han-Han: space-less, suppress"},
		{"世界", "人権", false, "Han-Han run: suppress"},
		{"ア", "イ", false, "Katakana-Katakana: suppress"},
		{"の", "構", false, "Hiragana-Han: suppress"},
		{"모든", "인류", true, "Hangul-Hangul: Korean word boundary, keep space"},
		{"对", "A", true, "Han-Latin: keep space"},
		{"year", "2024", true, "Latin-digit: keep space"},
		{"年", "12", true, "Han-digit boundary: keep space"},
	}
	for _, c := range cases {
		if got := joinNeedsSpace(c.left, c.right); got != c.want {
			t.Errorf("joinNeedsSpace(%q,%q) = %v, want %v (%s)", c.left, c.right, got, c.want, c.desc)
		}
	}
}
