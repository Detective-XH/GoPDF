package pdf

import "testing"

func TestNormalizeText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no ligature ascii", "plain ascii text", "plain ascii text"},
		{"ff", "oﬀer", "offer"},
		{"fi", "ﬁle", "file"},
		{"fl", "ﬂag", "flag"},
		{"ffi", "eﬃcient", "efficient"},
		{"ffl", "baﬄe", "baffle"},
		{"long st", "ﬅ", "st"},
		{"st", "ﬆ", "st"},
		{"all seven", "ﬀﬁﬂﬃﬄﬅﬆ", "fffiflffifflstst"},
		{"mixed in sentence", "the eﬃcient ﬂag ﬁle", "the efficient flag file"},
		// Targeted, NOT NFKC: these compatibility forms must pass through UNCHANGED.
		{"half not folded", "½ cup", "½ cup"},
		{"fullwidth not folded", "ＡＢ", "ＡＢ"},
		{"superscript not folded", "x²", "x²"},
		{"bare long s not folded", "meſer", "meſer"},
		// UTF-8 context: CJK + ligature, ensure byte handling is correct.
		{"cjk surround", "中ﬁ文", "中fi文"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeText(c.in); got != c.want {
				t.Errorf("NormalizeText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeTextIdempotent(t *testing.T) {
	for _, s := range []string{"", "plain", "eﬃcient ﬂag", "ﬅﬆ"} {
		once := NormalizeText(s)
		if twice := NormalizeText(once); twice != once {
			t.Errorf("not idempotent for %q: once=%q twice=%q", s, once, twice)
		}
	}
}

// No-ligature input must not allocate (the ContainsAny fast path returns s unchanged).
func TestNormalizeTextNoAllocWithoutLigature(t *testing.T) {
	s := "a reasonably long plain ascii string with no ligatures whatsoever"
	if n := testing.AllocsPerRun(100, func() { _ = NormalizeText(s) }); n != 0 {
		t.Errorf("NormalizeText allocated %v times on no-ligature input, want 0", n)
	}
}
