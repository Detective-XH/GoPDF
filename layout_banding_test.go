package pdf

import (
	"reflect"
	"sort"
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
	texts := mk()
	bands := bandsByY(texts)
	if len(bands) != 1 {
		t.Fatalf("jittered co-linear glyphs must form one band, got %d: %v", len(bands), bands)
	}
	got := texts[bands[0][0]].S + texts[bands[0][1]].S + texts[bands[0][2]].S
	if got != "abc" {
		t.Errorf("band order = %q, want left-to-right %q", got, "abc")
	}
	// Determinism: a second pass over an identical (separate) input yields the
	// same index bands. bandsByY does not mutate its input, so both inputs stay
	// in document order and the permutations match.
	if again := bandsByY(mk()); !reflect.DeepEqual(again, bands) {
		t.Errorf("non-deterministic banding:\n run1 = %v\n run2 = %v", bands, again)
	}
}

// TestBandsByYMatchesReference locks the bandsByY copy-elimination: the optimized
// index bands must name the same glyphs, in the same order, as the prior
// value-copy implementation produced — across every corpus page — and bandsByY
// must not mutate its input (the non-mutating contract this optimization relies
// on and strengthens).
func TestBandsByYMatchesReference(t *testing.T) {
	for _, e := range corpusManifest {
		t.Run(e.Path, func(t *testing.T) {
			r := loadCorpus(t, e)
			for i := 1; i <= r.NumPage(); i++ {
				p := r.Page(i)
				if p.V.IsNull() {
					continue
				}
				texts := p.Content().Text
				if len(texts) == 0 {
					continue
				}
				before := append([]Text(nil), texts...)
				ref := bandsByYReference(append([]Text(nil), texts...)) // [][]Text, prior behaviour
				got := bandsByY(texts)                                  // [][]int over texts
				if len(got) != len(ref) {
					t.Fatalf("page %d: band count %d != ref %d", i, len(got), len(ref))
				}
				for b := range got {
					if len(got[b]) != len(ref[b]) {
						t.Fatalf("page %d band %d: len %d != ref %d", i, b, len(got[b]), len(ref[b]))
					}
					for k, bi := range got[b] {
						if texts[bi] != ref[b][k] {
							t.Errorf("page %d band %d glyph %d: %+v != ref %+v", i, b, k, texts[bi], ref[b][k])
						}
					}
				}
				if !reflect.DeepEqual(before, texts) {
					t.Errorf("page %d: bandsByY mutated its input (non-mutating contract broken)", i)
				}
			}
		})
	}
}

// bandsByYReference reproduces the pre-optimization bandsByY (value-copy bands
// over a defensive copy) so the optimized index version can be diffed against the
// exact prior behaviour. Test-only calibration reference.
func bandsByYReference(texts []Text) [][]Text {
	texts = append([]Text(nil), texts...)
	sort.SliceStable(texts, func(i, j int) bool {
		if qi, qj := quantize(texts[i].Y), quantize(texts[j].Y); qi != qj {
			return qi > qj
		}
		return texts[i].X < texts[j].X
	})
	var bands [][]Text
	var band []Text
	flush := func() {
		sort.SliceStable(band, func(i, j int) bool {
			if quantize(band[i].X) != quantize(band[j].X) {
				return band[i].X < band[j].X
			}
			return band[i].Y > band[j].Y
		})
		bands = append(bands, band)
		band = nil
	}
	for _, t := range texts {
		if len(band) == 0 {
			band = append(band, t)
			continue
		}
		tol := band[0].FontSize * 0.5
		if tol < 1 {
			tol = 1
		}
		if band[0].Y-t.Y > tol {
			flush()
		}
		band = append(band, t)
	}
	if len(band) > 0 {
		flush()
	}
	return bands
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
