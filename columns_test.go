package pdf

import (
	"reflect"
	"testing"
)

// band builds one y-band row of words at the given (x, width) positions, all
// sharing a placeholder text so meanGlyphWidth has runes to divide by.
func band(xs ...[2]float64) []Word {
	ws := make([]Word, len(xs))
	for i, p := range xs {
		ws[i] = Word{S: "ab", X: p[0], W: p[1]}
	}
	return ws
}

func TestColumnGuttersTwoColumn(t *testing.T) {
	var rows [][]Word
	for i := 0; i < 6; i++ {
		rows = append(rows, band([2]float64{10, 30}, [2]float64{210, 30}))
	}
	g, colGap := columnGutters(rows)
	if len(g) != 1 {
		t.Fatalf("want 1 gutter, got %d: %v", len(g), g)
	}
	if g[0] < 200 || g[0] > 220 {
		t.Errorf("gutter x=%.1f, want near 210", g[0])
	}
	if colGap <= 0 {
		t.Errorf("colGap = %.2f, want > 0", colGap)
	}
}

func TestColumnGuttersThreeColumn(t *testing.T) {
	var rows [][]Word
	for i := 0; i < 6; i++ {
		rows = append(rows, band([2]float64{10, 30}, [2]float64{210, 30}, [2]float64{410, 30}))
	}
	g, _ := columnGutters(rows)
	if len(g) != 2 {
		t.Fatalf("want 2 gutters, got %d: %v", len(g), g)
	}
}

func TestColumnGuttersSingleColumn(t *testing.T) {
	// Tightly-spaced words (gaps ~4 pt, far below colGap) on 8 bands: no gutter.
	var rows [][]Word
	for i := 0; i < 8; i++ {
		rows = append(rows, band([2]float64{10, 18}, [2]float64{32, 30}, [2]float64{66, 32}))
	}
	if g, _ := columnGutters(rows); len(g) != 0 {
		t.Fatalf("single-column page must yield no gutters, got %v", g)
	}
}

func TestColumnGuttersTooFewBands(t *testing.T) {
	var rows [][]Word
	for i := 0; i < colMinBands-1; i++ {
		rows = append(rows, band([2]float64{10, 30}, [2]float64{210, 30}))
	}
	if g, _ := columnGutters(rows); g != nil {
		t.Fatalf("fewer than colMinBands rows must yield nil, got %v", g)
	}
}

func TestColumnGuttersHighZeroFrac(t *testing.T) {
	// Most words carry no width: gap geometry is unreliable, so abort.
	var rows [][]Word
	for i := 0; i < 6; i++ {
		rows = append(rows, []Word{
			{S: "a", X: 10, W: 0}, {S: "b", X: 210, W: 0}, {S: "c", X: 410, W: 30},
		})
	}
	if g, _ := columnGutters(rows); g != nil {
		t.Fatalf("high zero-width fraction must yield nil, got %v", g)
	}
}

func TestColumnGuttersDeterministic(t *testing.T) {
	// A multi-cluster page: the running-mean clustering is order-sensitive, so
	// guard that a fixed input always produces the same boundaries.
	var rows [][]Word
	for i := 0; i < 8; i++ {
		rows = append(rows, band([2]float64{10, 30}, [2]float64{215, 30}, [2]float64{420, 30}))
	}
	g1, _ := columnGutters(rows)
	g2, _ := columnGutters(rows)
	if !reflect.DeepEqual(g1, g2) {
		t.Fatalf("non-deterministic gutters: %v vs %v", g1, g2)
	}
}

func TestSplitWordsByGutters(t *testing.T) {
	// No gutters: one segment.
	ws := band([2]float64{10, 30}, [2]float64{45, 30})
	if got := splitWordsByGutters(ws, nil, 10); len(got) != 1 {
		t.Fatalf("nil gutters must give 1 segment, got %d", len(got))
	}
	// A genuine two-column row (wide gap straddling the gutter) splits in two.
	ws = band([2]float64{10, 30}, [2]float64{210, 30})
	segs := splitWordsByGutters(ws, []float64{200}, 10)
	if len(segs) != 2 || len(segs[0]) != 1 || len(segs[1]) != 1 {
		t.Fatalf("two-column row must split into two single-word segments, got %v", segs)
	}
}

func TestSplitWordsByGuttersFullWidth(t *testing.T) {
	// A full-width row flowing continuously across the gutter x=200 with ordinary
	// word spacing (gaps ~5 pt < colGap=10): it must stay ONE segment. A naive
	// bucket-by-x split would wrongly fragment it at 200.
	ws := band(
		[2]float64{10, 30}, [2]float64{45, 30}, [2]float64{80, 30},
		[2]float64{115, 30}, [2]float64{150, 30}, [2]float64{185, 30},
		[2]float64{220, 30}, // crosses gutter 200; gap from prev (ends 215) is 5
	)
	segs := splitWordsByGutters(ws, []float64{200}, 10)
	if len(segs) != 1 {
		t.Fatalf("continuous full-width row must stay one segment, got %d: %v", len(segs), segs)
	}
}

func TestMeanGlyphWidth(t *testing.T) {
	rows := [][]Word{{
		{S: "ab", W: 20}, // 2 runes, width 20
		{S: "c", W: 0},   // zero-width: skipped from mean, counted in zeroFrac
		{S: "de", W: 10}, // 2 runes, width 10
	}}
	charW, zeroFrac := meanGlyphWidth(rows)
	if charW != 30.0/4.0 {
		t.Errorf("charW = %.3f, want %.3f", charW, 30.0/4.0)
	}
	if zeroFrac != 1.0/3.0 {
		t.Errorf("zeroFrac = %.3f, want %.3f", zeroFrac, 1.0/3.0)
	}
}
