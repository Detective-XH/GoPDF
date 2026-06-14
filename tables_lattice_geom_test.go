package pdf

import (
	"math"
	"testing"
)

func TestLatticeThinRectCollapse(t *testing.T) {
	// A 200pt-wide, 0.5pt-tall horizontal filled rect (a ruling line). Its two h-borders are
	// 0.5pt apart and must collapse to exactly one h-edge under snap(y_tol=3); its two 0.5pt
	// v-borders are dropped by the post-merge length filter (<3pt). This is the NIST mechanism.
	r := Rect{Min: Point{X: 100, Y: 500}, Max: Point{X: 300, Y: 500.5}}
	merged := mergeEdges(rectToEdges(r), 3, 3)
	var hCount, vCount int
	for _, e := range merged {
		if e.orient == 'h' {
			hCount++
		} else {
			vCount++
		}
	}
	if hCount != 1 || vCount != 0 {
		t.Fatalf("thin h-rect: got %d h-edges, %d v-edges; want 1 h, 0 v; merged=%+v", hCount, vCount, merged)
	}
	if got := merged[0].top; math.Abs(got-(-500.25)) > 0.5 {
		t.Errorf("collapsed h-edge top=%v; want ~%v (rect mid-Y, top-origin)", got, -500.25)
	}
}

func TestLatticeSnap(t *testing.T) {
	near := snapEdges([]lEdge{
		{orient: 'v', x0: 100, x1: 100, top: 0, bottom: 50},
		{orient: 'v', x0: 102, x1: 102, top: 0, bottom: 50},
	}, 3)
	if near[0].x0 != near[1].x0 {
		t.Errorf("2pt-apart v-edges: x=%v,%v; want equal (snapped)", near[0].x0, near[1].x0)
	}
	far := snapEdges([]lEdge{
		{orient: 'v', x0: 100, x1: 100, top: 0, bottom: 50},
		{orient: 'v', x0: 104, x1: 104, top: 0, bottom: 50},
	}, 3)
	if far[0].x0 == far[1].x0 {
		t.Errorf("4pt-apart v-edges: both x=%v; want distinct (not snapped)", far[0].x0)
	}
}
