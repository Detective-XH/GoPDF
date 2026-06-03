// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for gstate.go — matrix type and graphics state.
// All package-level identifiers carry the "gstate" prefix to avoid
// collisions in the package pdf namespace.

package pdf

import "testing"

func TestGstateMatrixMulIdentity(t *testing.T) {
	m := matrix{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}
	if got := m.mul(ident); got != m {
		t.Errorf("m.mul(ident) = %v; want %v", got, m)
	}
	if got := ident.mul(m); got != m {
		t.Errorf("ident.mul(m) = %v; want %v", got, m)
	}
}

func TestGstateMatrixMulKnown(t *testing.T) {
	// scale(2,3) × translate(4,5) — expected result computed by hand.
	scale := matrix{{2, 0, 0}, {0, 3, 0}, {0, 0, 1}}
	translate := matrix{{1, 0, 0}, {0, 1, 0}, {4, 5, 1}}
	want := matrix{{2, 0, 0}, {0, 3, 0}, {4, 5, 1}}
	if got := scale.mul(translate); got != want {
		t.Errorf("scale.mul(translate) = %v; want %v", got, want)
	}
}

func TestGstateMatrixMulAssociativity(t *testing.T) {
	a := matrix{{1, 2, 0}, {3, 4, 0}, {0, 0, 1}}
	b := matrix{{5, 6, 0}, {7, 8, 0}, {0, 0, 1}}
	c := matrix{{1, 0, 0}, {0, 1, 0}, {2, 3, 1}}
	lhs := a.mul(b).mul(c)
	rhs := a.mul(b.mul(c))
	if lhs != rhs {
		t.Errorf("(a×b)×c ≠ a×(b×c): %v vs %v", lhs, rhs)
	}
}

func TestGstateIdentIsIdentity(t *testing.T) {
	want := matrix{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	if ident != want {
		t.Errorf("ident = %v; want %v", ident, want)
	}
}
