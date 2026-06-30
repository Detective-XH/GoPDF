package pdf

import (
	"strings"
	"testing"
)

// kd decodes a sequence of raw KrutiDev010 byte codes through the transducer, using a WinAnsi byte
// fallback (the Latin decode an unmapped code degrades to).
func kd(codes ...int) string {
	b := make([]byte, len(codes))
	for i, c := range codes {
		b[i] = byte(c)
	}
	return decodeKrutiDev010(string(b), &byteEncoder{&winAnsiEncoding})
}

// TestKrutiDev010Anchor pins the 8 stub-derived entries (the alignment anchor for the ported tables).
func TestKrutiDev010Anchor(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{44, "ए"}, {117, "न"}, {108, "स"}, {121, "ल"}, {101, "म"}, {59, "य"}, {119, "ू"}, {45, "."},
	}
	for _, c := range cases {
		if got := kd(c.code); got != c.want {
			t.Errorf("kd(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestKrutiDev010ShortIReorder: the pre-base short-i (IKAR=102) moves AFTER its consonant cluster.
func TestKrutiDev010ShortIReorder(t *testing.T) {
	if got := kd(102, 100); got != "कि" { // ि क -> क ि
		t.Errorf("ki = %q, want कि", got)
	}
	if got := kd(102, 111, 100, 107); got != "विका" { // ि व क ा -> व ि क ा (the talika/vikas pattern)
		t.Errorf("vika = %q, want विका", got)
	}
}

// TestKrutiDev010Reph: the above-base reph (REPH=90), stored AFTER the consonant it sits over, moves to
// the FRONT of its syllable. Bytes क म <reph> render as कर्म (the reph belongs to the म-syllable).
func TestKrutiDev010Reph(t *testing.T) {
	if got := kd(100, 101, 90); got != "कर्म" {
		t.Errorf("karma = %q, want कर्म", got)
	}
}

// TestKrutiDev010Conjuncts: single-byte stack glyphs expand to consonant+virama clusters (recount).
func TestKrutiDev010Conjuncts(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{41, "द्ध"}, {193, "प्र"}, {61, "त्र"}, {123, "क्ष्"}, {243, "स्त्र"}, {233, "न्न"}, {90, "र्"},
	}
	for _, c := range cases {
		if got := kd(c.code); got != c.want {
			t.Errorf("kd(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}

// TestKrutiDev010Nukta: the nukta sign (43) maps to U+093C.
func TestKrutiDev010Nukta(t *testing.T) {
	if got := kd(43); got != "़" {
		t.Errorf("nukta = %q, want ़", got)
	}
}

// TestKrutiDev010NasalReorder: a nasal stored before a dependent vowel (Pass 0) moves after it.
// क anusvar ekar (100 97 115) -> क ekar anusvar = कें.
func TestKrutiDev010NasalReorder(t *testing.T) {
	if got := kd(100, 97, 115); got != "कें" {
		t.Errorf("nasal reorder = %q, want कें", got)
	}
}

// TestKrutiDev010DigitsPunct: digits, danda, and the visarga/colon context rule.
func TestKrutiDev010DigitsPunct(t *testing.T) {
	if got := kd(48, 57); got != "09" {
		t.Errorf("digits = %q, want 09", got)
	}
	if got := kd(131); got != "१" {
		t.Errorf("deva-1 = %q, want १", got)
	}
	if got := kd(65); got != "।" {
		t.Errorf("danda = %q, want ।", got)
	}
	if got := kd(37); got != "ः" { // visarga in isolation
		t.Errorf("visarga = %q, want ः", got)
	}
	if got := kd(53, 37); got != "5:" { // 37 after a digit is a colon
		t.Errorf("colon-after-digit = %q, want 5:", got)
	}
}

// TestKrutiDev010NoZWJ: the searchable-Unicode goal forbids ZWJ/ZWNJ in the output, even for the
// conjunct/half-form glyphs whose source rules emit them.
func TestKrutiDev010NoZWJ(t *testing.T) {
	for _, code := range []int{123, 153, 218, 126, 243} { // conjuncts/halant whose source rules carry ZWJ
		if out := kd(code); strings.ContainsAny(out, "\u200d\u200c") { // ZERO_WIDTH_JOINER / NON_JOINER
			t.Errorf("kd(%d) = %q contains ZWJ/ZWNJ", code, out)
		}
	}
}

// TestKrutiDev010Fallback: a code with no rule degrades to the font's own (Latin) decode, never dropped.
func TestKrutiDev010Fallback(t *testing.T) {
	if got := kd(250); got != "ú" { // WinAnsi 0xFA
		t.Errorf("fallback = %q, want ú", got)
	}
}
