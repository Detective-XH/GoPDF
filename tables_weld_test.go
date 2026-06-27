// tables_weld_test.go — locks weldStraddlingDigits, the post-cell-assembly recovery of a trailing
// space-thousands group that overflows a ruled cell's right wall (cz-czso p477: data typeset on a
// wider pitch than the rules, so a number's last group straddles the column rule, its center landing
// outside the cell). The weld re-attaches such a center-miss DIGIT group to the numeric cell it
// overflows, under a deliberately narrow predicate (all-digit token + all-digit anchor + within
// colClusterTol + becomes the cell's rightmost token) proven necessary by a corpus A/B FP sweep.
package pdf

import (
	"reflect"
	"testing"
)

// row at top-origin band [-10,-2]: a word with Y=3,H=4 has center 5 → ay=-5 ∈ [-10,-2].
func weldWord(s string, x, w float64) Word { return Word{S: s, X: x, W: w, Y: 3, H: 4} }

func TestWeldStraddlingDigits(t *testing.T) {
	// Two ruled columns, one row: col0 [0,50], col1 [50,100].
	cells := []lCell{
		{x0: 0, top: -10, x1: 50, bottom: -2},
		{x0: 50, top: -10, x1: 100, bottom: -2},
	}

	t.Run("trailing_digit_group_welded", func(t *testing.T) {
		// col1 first group "34" (right 98, center 93, placed); trailing group "56" overflows the
		// wall: left edge 99 < 100 (inside) but center 104 > 100 (center-miss). All-digit, anchor
		// "34" all-digit, gap 98→99 = 1 ≤ colClusterTol, becomes rightmost → welded → "34 56".
		words := []Word{weldWord("12", 20, 10), weldWord("34", 88, 10), weldWord("56", 99, 10)}
		got := reconstructGrid(cells, words)
		want := [][]string{{"12", "34 56"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("trailing_digit_group_welded: got %v, want %v", got, want)
		}
	})

	t.Run("non_digit_token_not_welded", func(t *testing.T) {
		// A center-miss token that is NOT all digits (a fused unit suffix, a label, a dot-leader)
		// is never welded — the FP class the all-digit gate kills (cz-czso "480thous.").
		words := []Word{weldWord("12", 20, 10), weldWord("34", 88, 10), weldWord("56thous.", 99, 30)}
		got := reconstructGrid(cells, words)
		want := [][]string{{"12", "34"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("non_digit_token_not_welded: got %v, want %v (fused suffix must not weld)", got, want)
		}
	})

	t.Run("non_digit_anchor_not_welded", func(t *testing.T) {
		// The left neighbour must itself be an all-digit word: we only extend a NUMBER. Here the
		// anchor "Q4" is not all-digit, so the digit miss "56" is not welded into it.
		words := []Word{weldWord("12", 20, 10), weldWord("Q4", 88, 10), weldWord("56", 99, 10)}
		got := reconstructGrid(cells, words)
		want := [][]string{{"12", "Q4"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("non_digit_anchor_not_welded: got %v, want %v", got, want)
		}
	})

	t.Run("digit_miss_right_of_cell_welded_at_tail", func(t *testing.T) {
		// Two placed digit words in col1 ("34" center 65, "78" right 98 center 93) plus a trailing
		// miss "90" (X=99, center 104 > 100 → center-miss). The anchor is the rightmost placed
		// word "78" (right 98), gap 1 ≤ tol, and no col1 word lies right of 99 → welded at the tail.
		words := []Word{
			weldWord("12", 20, 10),
			weldWord("34", 58, 10), // col1, center 63, placed
			weldWord("78", 88, 10), // col1, center 93, placed, rightmost
			weldWord("90", 99, 10), // miss, center 104, abuts 78 → welded
		}
		got := reconstructGrid(cells, words)
		want := [][]string{{"12", "34 78 90"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("digit_miss_right_of_cell_welded_at_tail: got %v, want %v", got, want)
		}
	})

	t.Run("wholly_outside_cell_not_welded", func(t *testing.T) {
		// A digit that lies WHOLLY outside the anchor cell (left edge past the right wall) must NOT
		// weld even when it abuts the anchor word within colClusterTol — it does not straddle the
		// wall, so it is a separate token (standalone count / footnote / adjacent narrow column),
		// not an overflow continuation. anchor "34" right 98 (in col1, x1=100); "56" left 101 > 100
		// (outside), gap 101-98=3 ≤ tol, but w.X ≥ cellX1 → straddle gate rejects.
		words := []Word{weldWord("12", 20, 10), weldWord("34", 88, 10), weldWord("56", 101, 8)}
		got := reconstructGrid(cells, words)
		want := [][]string{{"12", "34"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("wholly_outside_cell_not_welded: got %v, want %v (no straddle → no weld)", got, want)
		}
	})

	t.Run("gap_over_tol_not_welded", func(t *testing.T) {
		// gap to the anchor exceeds colClusterTol (4.0): not a trailing group → not welded.
		words := []Word{weldWord("12", 20, 10), weldWord("34", 88, 10), weldWord("56", 103, 10)}
		got := reconstructGrid(cells, words)
		want := [][]string{{"12", "34"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("gap_over_tol_not_welded: got %v, want %v", got, want)
		}
	})

	t.Run("center_contained_unchanged", func(t *testing.T) {
		// A word whose center is inside a cell is assigned by the primary pass; the weld is a no-op.
		words := []Word{weldWord("12", 20, 10), weldWord("34", 70, 10)}
		got := reconstructGrid(cells, words)
		want := [][]string{{"12", "34"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("center_contained_unchanged: got %v, want %v", got, want)
		}
	})
}
