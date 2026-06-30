package pdf

import "testing"

// TestEncoderYieldsDevanagari proves the decode-change FP gate SEPARATES (not merely fires): an
// encoder already producing Devanagari is detected (→ remap declines), while a Latin encoder is not
// (→ remap proceeds). This is the stay-silent leg the Walkman round-trip (the fire leg) cannot cover.
func TestEncoderYieldsDevanagari(t *testing.T) {
	// A Latin encoder (WinAnsi) yields no Devanagari → gate stays silent → remap is allowed.
	if encoderYieldsDevanagari(&byteEncoder{&winAnsiEncoding}) {
		t.Errorf("WinAnsi encoder reported as yielding Devanagari; FP gate would wrongly DECLINE a real legacy font")
	}
	// An encoder that already maps a byte to Devanagari → gate fires → remap declines (no corruption).
	var devTable [256]rune
	copy(devTable[:], winAnsiEncoding[:])
	devTable['A'] = 0x0915 // क
	if !encoderYieldsDevanagari(&byteEncoder{&devTable}) {
		t.Errorf("encoder mapping a byte to Devanagari NOT detected; FP gate would wrongly REMAP and corrupt correct text")
	}
}

// TestDetectLegacyFontTextRecoveryAware proves the per-table legacy warning is RECOVERY-aware: a
// partially-remapped table (Devanagari + leftover Latin soup) must still flag (Confidence Low), not
// silently flip to High. All-Latin gibberish still flags (no-harm); a fully-recovered pure-Devanagari
// table does NOT flag.
func TestDetectLegacyFontTextRecoveryAware(t *testing.T) {
	const kruti = "KrutiDev010"
	cell := lCell{x0: 0, top: 0, x1: 1000, bottom: 1000}
	mk := func(items [][2]string) []Word {
		ws := make([]Word, 0, len(items))
		for _, it := range items {
			ws = append(ws, Word{S: it[0], Font: it[1], X: 10, Y: -50, W: 10, H: 10}) // center inside the cell
		}
		return ws
	}
	cases := []struct {
		name     string
		words    []Word
		wantWarn bool
	}{
		{"all-Latin gibberish flags (unchanged no-harm)",
			mk([][2]string{{"vkfFkZd", kruti}, {"lehkk", kruti}, {"ldy", kruti}}), true},
		{"partial Devanagari+Latin soup flags (the fix)",
			mk([][2]string{{"एनvks", kruti}, {"यूvk", kruti}, {"एलjk", kruti}}), true},
		{"fully-recovered pure Devanagari does NOT flag",
			mk([][2]string{{"एनयू", kruti}, {"एलएम", kruti}, {"राष्ट्रीय", kruti}}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := len(detectLegacyFontText([]lCell{cell}, c.words)) > 0
			if got != c.wantWarn {
				t.Errorf("legacy warning fired=%v, want %v", got, c.wantWarn)
			}
		})
	}
}

func TestLegacyVariantKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"JNXPQQ+Walkman-Chanakya905Bold", "walkmanchanakya905bold"},
		{"Walkman-Chanakya905Normal", "walkmanchanakya905normal"},
		{"ABCDEE+Kruti Dev 010", "krutidev010"},
		{"Kruti-Dev680", "krutidev680"},
		{"", ""},
	}
	for _, c := range cases {
		if got := legacyVariantKey(c.in); got != c.want {
			t.Errorf("legacyVariantKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
