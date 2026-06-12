// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

type matrix [3][3]float64

var ident = matrix{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

func (x matrix) mul(y matrix) matrix {
	var z matrix
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				z[i][j] += x[i][k] * y[k][j]
			}
		}
	}
	return z
}

type gstate struct {
	Tc    float64
	Tw    float64
	Th    float64
	Tl    float64
	Tf    Font
	Tfs   float64
	Tmode int
	Trise float64
	Tm    matrix
	Tlm   matrix
	CTM   matrix
	// enc is the text encoder selected by the most recent Tf operator. It is
	// part of the graphics state — paired with Tf — so the q/Q operators save
	// and restore it together. Keeping enc outside the graphics state would let
	// Q restore Tf while leaving enc pointing at the inner block's font, so text
	// shown after a Q would decode through the wrong encoder.
	enc TextEncoding
	// encSource is enc's decode-path classification, paired with enc so q/Q
	// save and restore it together (a font set inside q…Q must not leak its
	// classification past the Q that restores the outer font).
	encSource encSource
	// vertical reports whether the current font selects vertical writing mode
	// (WMode 1), identified by a predefined-CMap "-V" suffix. Like enc/encSource
	// it is part of the graphics state, so q/Q save and restore it with the font
	// (a vertical font set inside q…Q must not leak its advance direction past the
	// Q that restores an outer horizontal font). It governs only the intra-run
	// advance direction in layoutDecoded / interpretTJArray; inter-line leading
	// (T*/TD/TL) and layout grouping stay horizontal until WS3.
	vertical bool
}

// advance translates the text matrix by (dx, dy) text-space units, the
// displacement after a shown glyph or a TJ numeric adjustment. Horizontal writing
// advances along x; vertical writing (a -V CMap) advances along y.
func (g *gstate) advance(dx, dy float64) {
	g.Tm = matrix{{1, 0, 0}, {0, 1, 0}, {dx, dy, 1}}.mul(g.Tm)
}

// xobjMaxDepth caps Form XObject recursion to guard against malformed PDFs
// that contain cyclic or deeply nested XObject references.
const xobjMaxDepth = 10
