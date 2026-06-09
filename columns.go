// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"math"
	"sort"
	"unicode/utf8"
)

// Column detection groups a page's words into vertical text columns by finding
// the x-positions where a new word repeatedly starts after a wide horizontal
// gap. A gap-start that recurs across a large fraction of the page's y-bands
// marks a real inter-column gutter, which distinguishes genuine multi-column
// layout from the incidental wide gaps inside ordinary single-column prose.
// The helpers are independent of any line- or table-specific logic so later
// reading-order and table reconstruction can reuse them.
//
// Thresholds were derived empirically from the dense multi-column and CJK
// corpus fixtures: real inter-column gutters recur in 37-53% of bands while the
// widest spurious gap recurs in <=14%, and tightly-set CJK pages produce no gap
// wide enough to register at all.
const (
	colGapFactor   = 1.5  // a gutter gap must exceed colGapFactor * meanGlyphWidth
	colMergeFactor = 2.0  // gap-starts within colMergeFactor * meanGlyphWidth cluster together
	colSupportFrac = 0.25 // a gutter must recur in >= this fraction of the page's bands
	colMinSupport  = 3    // ... and in at least this many bands (absolute floor)
	colMinBands    = 4    // pages with fewer bands are not treated as multi-column
	colMaxZeroFrac = 0.50 // abort if > 50% of words carry no width (gap geometry unreliable)
)

// meanGlyphWidth returns the mean glyph advance per rune across all words,
// skipping zero-width words (missing font width metrics), and the fraction of
// words that had zero width. Callers reject pages whose zeroFrac is too high:
// without reliable widths the inter-word gaps that drive column detection
// cannot be measured.
func meanGlyphWidth(rows [][]Word) (charW, zeroFrac float64) {
	var sum float64
	var runes, total, zero int
	for _, row := range rows {
		for _, w := range row {
			total++
			if w.W <= 0 {
				zero++
				continue
			}
			sum += w.W
			runes += utf8.RuneCountInString(w.S)
		}
	}
	if total > 0 {
		zeroFrac = float64(zero) / float64(total)
	}
	if runes == 0 {
		return 0, zeroFrac
	}
	return sum / float64(runes), zeroFrac
}

type gapCluster struct {
	x       float64 // support-weighted running mean of member gap-starts
	support int
}

// clusterGapStarts walks each band left-to-right and clusters the x-position of
// every word that starts after a gap wider than colGap. A start joins an
// existing cluster when within mergeTol of its running mean, otherwise it seeds
// a new one. Bands arrive in a fixed top-to-bottom order (from bandsByY) and
// words in a fixed left-to-right order, so the running-mean accumulation is
// deterministic.
func clusterGapStarts(rows [][]Word, colGap, mergeTol float64) []gapCluster {
	var clusters []gapCluster
	for _, row := range rows {
		for i := 1; i < len(row); i++ {
			if gap := row[i].X - (row[i-1].X + row[i-1].W); gap <= colGap {
				continue
			}
			x := row[i].X
			matched := false
			for j := range clusters {
				if x >= clusters[j].x-mergeTol && x <= clusters[j].x+mergeTol {
					clusters[j].x = (clusters[j].x*float64(clusters[j].support) + x) /
						float64(clusters[j].support+1)
					clusters[j].support++
					matched = true
					break
				}
			}
			if !matched {
				clusters = append(clusters, gapCluster{x: x, support: 1})
			}
		}
	}
	return clusters
}

// columnGutters returns the sorted x-positions of the inter-column gutters on a
// page whose words are grouped into y-bands (rows), and the gap width that
// defines a gutter crossing (colGap, returned so per-band splitting can reuse
// it without recomputing the mean glyph width). It returns (nil, 0) when the
// page is not confidently multi-column: too few bands, unreliable width
// geometry, or no gap-start position that recurs often enough.
func columnGutters(rows [][]Word) (gutters []float64, colGap float64) {
	if len(rows) < colMinBands {
		return nil, 0
	}
	charW, zeroFrac := meanGlyphWidth(rows)
	if charW <= 0 || zeroFrac > colMaxZeroFrac {
		return nil, 0
	}
	colGap = colGapFactor * charW
	mergeTol := colMergeFactor * charW
	clusters := clusterGapStarts(rows, colGap, mergeTol)

	minSupport := colMinSupport
	if ceil := int(math.Ceil(colSupportFrac * float64(len(rows)))); ceil > minSupport {
		minSupport = ceil
	}
	var bounds []float64
	for _, c := range clusters {
		if c.support >= minSupport {
			bounds = append(bounds, c.x)
		}
	}
	sort.Float64s(bounds)
	return mergeAdjacent(bounds, mergeTol), colGap
}

// mergeAdjacent collapses sorted boundaries that ended up within tol of each
// other (the running-mean clustering can leave two near-duplicate survivors
// when one gutter's mean drifts) into their midpoint.
func mergeAdjacent(xs []float64, tol float64) []float64 {
	if len(xs) == 0 {
		return nil
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if x-out[len(out)-1] <= tol {
			out[len(out)-1] = (out[len(out)-1] + x) / 2
			continue
		}
		out = append(out, x)
	}
	return out
}

// splitWordsByGutters splits one band's words (sorted left-to-right) into
// per-column segments. A split happens between two adjacent words ONLY when
// they fall on opposite sides of a detected gutter AND a real inter-column gap
// (> colGap) separates them. A full-width row — a masthead, title, or section
// heading that flows continuously across the gutter x-positions with ordinary
// word spacing — has no such gap and so stays a single segment; only a genuine
// two/three-column row, which carries the wide inter-column gap, is split. This
// is the "x-cluster split within a band": split where the band actually has the
// gap, not merely at the page's gutter positions. With no gutters the whole
// band is one segment (single-column behaviour preserved). A word straddling a
// gutter stays whole (no gap precedes the next word).
func splitWordsByGutters(ws []Word, gutters []float64, colGap float64) [][]Word {
	if len(gutters) == 0 {
		return [][]Word{ws}
	}
	out := [][]Word{{ws[0]}}
	for i := 1; i < len(ws); i++ {
		prev, w := ws[i-1], ws[i]
		gap := w.X - (prev.X + prev.W)
		if gap > colGap && columnOf(prev.X, gutters) != columnOf(w.X, gutters) {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], w)
	}
	return out
}

// columnOf returns the index of the column that x falls in: 0 left of
// gutters[0], i+1 in [gutters[i], gutters[i+1]).
func columnOf(x float64, gutters []float64) int {
	col := 0
	for col < len(gutters) && x >= gutters[col]-1e-6 {
		col++
	}
	return col
}
