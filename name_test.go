// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for name.go — PDF glyph-name to Unicode code-point map.
// All package-level identifiers carry the "name" prefix to avoid
// collisions in the package pdf namespace.

package pdf

import "testing"

func TestNameToRuneSpot(t *testing.T) {
	cases := []struct {
		glyph string
		want  rune
	}{
		{"nbspace", 0x00A0},
		{"copyright", 0x00A9},
		{"degree", 0x00B0},
		{"alpha", 0x03B1},
		{"Euro", 0x20AC},
		{"bullet", 0x2022},
		{"trademark", 0x2122},
		{"infinity", 0x221E},
		{"endash", 0x2013},
		{"emdash", 0x2014},
	}
	for _, tc := range cases {
		got, ok := nameToRune[tc.glyph]
		if !ok {
			t.Errorf("nameToRune[%q] missing; want U+%04X", tc.glyph, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("nameToRune[%q] = U+%04X; want U+%04X", tc.glyph, got, tc.want)
		}
	}
}

func TestNameToRuneCount(t *testing.T) {
	if n := len(nameToRune); n < 4000 {
		t.Errorf("len(nameToRune) = %d; want >= 4000", n)
	}
}

func TestNameToRuneAbsent(t *testing.T) {
	if r := nameToRune["__nonexistent__"]; r != 0 {
		t.Errorf("nameToRune[\"__nonexistent__\"] = U+%04X; want 0", r)
	}
}
