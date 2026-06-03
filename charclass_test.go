// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for charclass.go — character-class predicates and pure functions.
// All package-level identifiers carry the "charclass" prefix to avoid
// collisions in the package pdf namespace.

package pdf

import "testing"

func TestCharclassUnhex(t *testing.T) {
	cases := []struct {
		in   byte
		want int
	}{
		{'0', 0},
		{'5', 5},
		{'9', 9},
		{'a', 10},
		{'e', 14},
		{'f', 15},
		{'A', 10},
		{'E', 14},
		{'F', 15},
		{'g', -1},
		{'Z', -1},
		{' ', -1},
		{'!', -1},
	}
	for _, tc := range cases {
		if got := unhex(tc.in); got != tc.want {
			t.Errorf("unhex(%q) = %d; want %d", tc.in, got, tc.want)
		}
	}
}

func TestCharclassIsSpace(t *testing.T) {
	for _, b := range []byte{'\x00', '\t', '\n', '\f', '\r', ' '} {
		if !isSpace(b) {
			t.Errorf("isSpace(%#02x) = false; want true", b)
		}
	}
	for _, b := range []byte{'A', '/', '0', '(', '%', '~'} {
		if isSpace(b) {
			t.Errorf("isSpace(%#02x) = true; want false", b)
		}
	}
}

func TestCharclassIsDelim(t *testing.T) {
	for _, b := range []byte{'<', '>', '(', ')', '[', ']', '{', '}', '/', '%'} {
		if !isDelim(b) {
			t.Errorf("isDelim(%q) = false; want true", b)
		}
	}
	for _, b := range []byte{'A', '0', ' ', '\n', '-', '+'} {
		if isDelim(b) {
			t.Errorf("isDelim(%q) = true; want false", b)
		}
	}
}

func TestCharclassStripSign(t *testing.T) {
	cases := []struct{ in, want string }{
		{"+42", "42"},
		{"-3", "3"},
		{"0", "0"},
		{"", ""},
		{"abc", "abc"},
		{"++1", "+1"},
	}
	for _, tc := range cases {
		if got := stripSign(tc.in); got != tc.want {
			t.Errorf("stripSign(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestCharclassIsInteger(t *testing.T) {
	for _, s := range []string{"0", "42", "-7", "+100", "9999"} {
		if !isInteger(s) {
			t.Errorf("isInteger(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"", "3.14", "1e5", "abc", "-", "+"} {
		if isInteger(s) {
			t.Errorf("isInteger(%q) = true; want false", s)
		}
	}
}

func TestCharclassIsReal(t *testing.T) {
	for _, s := range []string{"3.14", "-0.5", "+1.0", "0.0", "99.0"} {
		if !isReal(s) {
			t.Errorf("isReal(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"", "42", "1.2.3", "abc", "-", "+"} {
		if isReal(s) {
			t.Errorf("isReal(%q) = true; want false", s)
		}
	}
}
