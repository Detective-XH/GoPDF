package pdf

import (
	"os"
	"testing"
)

func TestLatticeIntersect(t *testing.T) {
	// a '+' cross: one v-edge and one h-edge crossing at (50, -30) in top-origin.
	inters, _, _ := edgesToIntersections([]lEdge{
		{orient: 'v', x0: 50, x1: 50, top: -60, bottom: 0},
		{orient: 'h', x0: 0, x1: 100, top: -30, bottom: -30},
	}, 3, 3)
	if len(inters) != 1 {
		t.Fatalf("'+' cross: got %d intersections; want 1", len(inters))
	}
	if _, ok := inters[pointKey{q(50), q(-30)}]; !ok {
		t.Errorf("intersection not at (50,-30); got %v", inters)
	}
	// non-touching pair (v ends at -40, h at -10, gap > tol) -> none
	none, _, _ := edgesToIntersections([]lEdge{
		{orient: 'v', x0: 50, x1: 50, top: -60, bottom: -40},
		{orient: 'h', x0: 0, x1: 100, top: -10, bottom: -10},
	}, 3, 3)
	if len(none) != 0 {
		t.Errorf("non-touching: got %d intersections; want 0", len(none))
	}
}

func TestLatticeOneCell(t *testing.T) {
	// 4 edges forming a square (top-origin: top < bottom).
	square := []lEdge{
		{orient: 'h', x0: 0, x1: 100, top: 0, bottom: 0},
		{orient: 'h', x0: 0, x1: 100, top: 100, bottom: 100},
		{orient: 'v', x0: 0, x1: 0, top: 0, bottom: 100},
		{orient: 'v', x0: 100, x1: 100, top: 0, bottom: 100},
	}
	in, _, _ := edgesToIntersections(square, 3, 3)
	if cells := intersectionsToCells(in); len(cells) != 1 {
		t.Fatalf("square: got %d cells; want 1 (inters=%d)", len(cells), len(in))
	}
	// open shape (drop the right v-edge) -> no closed cell
	oi, _, _ := edgesToIntersections(square[:3], 3, 3)
	if oc := intersectionsToCells(oi); len(oc) != 0 {
		t.Errorf("open shape: got %d cells; want 0", len(oc))
	}
}

func TestLatticeCoverageIRS(t *testing.T) {
	f, err := os.Open("testdata/corpus/tables/irs-p55b-2025-excerpt.pdf")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	r, err := NewReader(f, fi.Size())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		tables := latticeTables(p.Content())
		if len(tables) == 0 {
			continue
		}
		// largest table on the page
		largest := tables[0]
		for _, tb := range tables[1:] {
			if len(tb) > len(largest) {
				largest = tb
			}
		}
		// reconstruct its grid dims by banding cell tops->rows, x0->cols (tol=4)
		var tops, x0s []float64
		for _, c := range largest {
			tops = append(tops, c.top)
			x0s = append(x0s, c.x0)
		}
		rows := len(cluster1D(tops, 4))
		cols := len(cluster1D(x0s, 4))
		t.Logf("IRS p55b p%-2d: tables=%d  largest=%d cells -> grid %dx%d", i, len(tables), len(largest), rows, cols)
	}
}
