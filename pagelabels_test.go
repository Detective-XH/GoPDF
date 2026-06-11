// pagelabels_test.go — tests for Reader.PageLabels() and helper functions.
//
// Fixtures are synthetic, built with buildPDFFromObjects (page_test.go:512).
// Each fixture exercises one aspect of the /PageLabels number tree
// (PDF 32000-1 §12.4.2).
package pdf

import (
	"fmt"
	"os"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// Helper: build a K-page PDF body (no content streams — just structure).
// ---------------------------------------------------------------------------

// kPageKids returns a /Kids array string like "[3 0 R 4 0 R ...]" for K pages
// starting at object startObj.
func kPageKids(k, startObj int) string {
	s := "["
	for i := range k {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d 0 R", startObj+i)
	}
	s += "]"
	return s
}

// buildLabeledPDF assembles a minimal PDF with K pages and a /PageLabels entry
// in the Catalog pointing to the LAST object (labelTreeBody).
//
// Object layout:
//
//	1 — Catalog  (with /PageLabels pointing at last obj)
//	2 — Pages
//	3..2+K — Page objects
//	3+K — number-tree root
func buildLabeledPDF(k int, labelTreeBody string) []byte {
	treeObjNum := 3 + k
	objs := make([]string, treeObjNum)

	// obj 1: Catalog
	objs[0] = fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /PageLabels %d 0 R >>", treeObjNum)

	// obj 2: Pages
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids %s /Count %d >>", kPageKids(k, 3), k)

	// obj 3..2+K: page objects
	for i := range k {
		objs[2+i] = "<< /Type /Page /Parent 2 0 R >>"
	}

	// last obj: number-tree root
	objs[treeObjNum-1] = labelTreeBody

	return buildPDFFromObjects(objs)
}

// buildLabeledPDFWithKids assembles a minimal PDF with K pages and a
// /PageLabels entry that uses a /Kids indirection.
//
// Object layout:
//
//	1 — Catalog  (with /PageLabels pointing at obj treeObjNum)
//	2 — Pages
//	3..2+K — Page objects
//	3+K   — number-tree root (has /Kids pointing at 4+K)
//	4+K   — number-tree child (has /Nums)
func buildLabeledPDFWithKids(k int, childNums string) []byte {
	rootObjNum := 3 + k
	childObjNum := 4 + k
	objs := make([]string, childObjNum)

	// obj 1: Catalog
	objs[0] = fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /PageLabels %d 0 R >>", rootObjNum)

	// obj 2: Pages
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids %s /Count %d >>", kPageKids(k, 3), k)

	// obj 3..2+K: page objects
	for i := range k {
		objs[2+i] = "<< /Type /Page /Parent 2 0 R >>"
	}

	// number-tree root: only /Kids, no /Nums
	objs[rootObjNum-1] = fmt.Sprintf("<< /Kids [%d 0 R] >>", childObjNum)

	// number-tree child: actual /Nums
	objs[childObjNum-1] = fmt.Sprintf("<< /Nums %s >>", childNums)

	return buildPDFFromObjects(objs)
}

// buildNoLabelPDF assembles a minimal PDF with K pages and NO /PageLabels.
func buildNoLabelPDF(k int) []byte {
	objs := make([]string, 2+k)

	// obj 1: Catalog — no /PageLabels
	objs[0] = "<< /Type /Catalog /Pages 2 0 R >>"

	// obj 2: Pages
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids %s /Count %d >>", kPageKids(k, 3), k)

	// obj 3..2+K: page objects
	for i := range k {
		objs[2+i] = "<< /Type /Page /Parent 2 0 R >>"
	}

	return buildPDFFromObjects(objs)
}

// ---------------------------------------------------------------------------
// Fixture tests
// ---------------------------------------------------------------------------

// TestPageLabelsMixedFrontMatter exercises two ranges: lower-roman for the
// first 5 pages, decimal from page 6.
//
// Number tree root: << /Nums [0 << /S /r >> 5 << /S /D >>] >>
// Expected: ["i","ii","iii","iv","v","1","2","3","4"]
func TestPageLabelsMixedFrontMatter(t *testing.T) {
	data := buildLabeledPDF(9,
		`<< /Nums [0 << /S /r >> 5 << /S /D >> ] >>`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{"i", "ii", "iii", "iv", "v", "1", "2", "3", "4"}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v", got, want)
	}
}

// TestPageLabelsPrefixStRoman exercises /P prefix, /St start value, and
// upper-Roman ranges. Range 0: decimal with prefix "A-" starting at 1;
// range 3: upper-Roman starting at 1.
//
// Number tree root: << /Nums [0 << /S /D /P (A-) /St 1 >> 3 << /S /R >>] >>
// Expected: ["A-1","A-2","A-3","I","II"]
func TestPageLabelsPrefixStRoman(t *testing.T) {
	data := buildLabeledPDF(5,
		`<< /Nums [0 << /S /D /P (A-) /St 1 >> 3 << /S /R >> ] >>`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{"A-1", "A-2", "A-3", "I", "II"}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v", got, want)
	}
}

// TestPageLabelsLettersStRollover exercises the /A (upper letters) style
// with /St 25 so the sequence crosses the Z→AA boundary.
//
// Number tree root: << /Nums [0 << /S /A /St 25 >>] >>
// Expected: ["Y","Z","AA","BB"]
func TestPageLabelsLettersStRollover(t *testing.T) {
	data := buildLabeledPDF(4,
		`<< /Nums [0 << /S /A /St 25 >> ] >>`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{"Y", "Z", "AA", "BB"}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v", got, want)
	}
}

// TestPageLabelsPrefixOnlyNoS exercises a range with only /P (no /S),
// which produces a prefix-only label for the first page, then a decimal
// range from page 2 onward.
//
// Number tree root: << /Nums [0 << /P (Cover) >> 1 << /S /D >>] >>
// Expected: ["Cover","1","2","3"]
func TestPageLabelsPrefixOnlyNoS(t *testing.T) {
	data := buildLabeledPDF(4,
		`<< /Nums [0 << /P (Cover) >> 1 << /S /D >> ] >>`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{"Cover", "1", "2", "3"}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v", got, want)
	}
}

// TestPageLabelsAbsent verifies that PageLabels() returns nil when the
// Catalog has no /PageLabels key.
func TestPageLabelsAbsent(t *testing.T) {
	data := buildNoLabelPDF(3)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got := r.PageLabels()
	if got != nil {
		t.Errorf("PageLabels() = %v, want nil for document without /PageLabels", got)
	}
}

// TestPageLabelsKidsRecursion exercises the /Kids indirection in the number
// tree (one level of recursion). The root has only /Kids; the child has /Nums.
//
// Object layout:
//
//	root: << /Kids [child 0 R] >>
//	child: << /Nums [0 << /S /r >> 2 << /S /D >>] >>
//
// Expected: ["i","ii","1","2"]
func TestPageLabelsKidsRecursion(t *testing.T) {
	data := buildLabeledPDFWithKids(4,
		`[0 << /S /r >> 2 << /S /D >> ]`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{"i", "ii", "1", "2"}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v", got, want)
	}
}

// TestPageLabelsBeforeFirstRange verifies that pages before the first label
// range's start get "" (the only range begins at page index 2).
//
// Number tree root: << /Nums [2 << /S /D >>] >>
// Expected: ["","","1","2"]
func TestPageLabelsBeforeFirstRange(t *testing.T) {
	data := buildLabeledPDF(4,
		`<< /Nums [2 << /S /D >> ] >>`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{"", "", "1", "2"}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v", got, want)
	}
}

// TestPageLabelsNegativeKeySkipped verifies that a malformed negative number-tree
// key is skipped — it must not wrongly cover page 0. With only a negative key,
// no range survives, so PageLabels returns nil.
//
// Number tree root: << /Nums [-5 << /S /r >>] >>
func TestPageLabelsNegativeKeySkipped(t *testing.T) {
	data := buildLabeledPDF(2,
		`<< /Nums [-5 << /S /r >> ] >>`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if got := r.PageLabels(); got != nil {
		t.Errorf("PageLabels() = %v, want nil (negative key must be skipped)", got)
	}
}

// TestPageLabelsDuplicateKeyLastWins documents the chosen behavior on a
// malformed duplicate-key /Nums: two entries share key 0 (roman then decimal),
// and the LAST entry wins, so the decimal range covers both pages. This pins
// the last-entry-wins semantic (stable sort + forward sweep) and that a
// duplicate key is handled without panic or unbounded output.
//
// Note: this small 2-entry case sorts identically under sort.Slice and
// sort.SliceStable (Go's insertion-sort path is stable for ≤12 elements), so it
// is NOT a tripwire that would fail on a sort.SliceStable→sort.Slice revert —
// it asserts the observable contract, not the sort implementation.
//
// Number tree root: << /Nums [0 << /S /r >> 0 << /S /D >>] >>
// Expected: ["1","2"]  (last entry — decimal — wins)
func TestPageLabelsDuplicateKeyLastWins(t *testing.T) {
	data := buildLabeledPDF(2,
		`<< /Nums [0 << /S /r >> 0 << /S /D >> ] >>`,
	)
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{"1", "2"}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v (last duplicate-key entry must win)", got, want)
	}
}

// TestPageLabelsRealCorpusIRS validates PageLabels() against a real public PDF
// (IRS Publication 55-B, 2025 excerpt). These pages carry the printed labels
// "32".."47" via the document's /PageLabels tree, even though they sit at
// file-positions 1..16 in the excerpt (and were lifted from file-pages 40..55
// of the source — see testdata/corpus/README.md). That gap between printed
// label and file position is exactly what the feature exposes. This is the
// real-world regression gate behind the synthetic fixtures above.
func TestPageLabelsRealCorpusIRS(t *testing.T) {
	data, err := os.ReadFile(corpusPath("tables/irs-p55b-2025-excerpt.pdf"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want := []string{
		"32", "33", "34", "35", "36", "37", "38", "39",
		"40", "41", "42", "43", "44", "45", "46", "47",
	}
	got := r.PageLabels()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PageLabels() = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Unit tests — toRoman
// ---------------------------------------------------------------------------

func TestToRoman(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "I"},
		{4, "IV"},
		{9, "IX"},
		{40, "XL"},
		{49, "XLIX"},
		{90, "XC"},
		{400, "CD"},
		{1990, "MCMXC"},
		// boundary: cap at maxLabelNumber (10000); above → decimal
		{maxLabelNumber, "MMMMMMMMMM"},
		{maxLabelNumber + 1, "10001"},
		{20000, "20000"},
		// below 1 → decimal
		{0, "0"},
		{-1, "-1"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d", tc.n), func(t *testing.T) {
			got := toRoman(tc.n)
			if got != tc.want {
				t.Errorf("toRoman(%d) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Unit tests — toLetters
// ---------------------------------------------------------------------------

func TestToLetters(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "A"},
		{26, "Z"},
		{27, "AA"},
		{28, "BB"},
		{52, "ZZ"},
		{53, "AAA"},
		// boundary: cap at maxLabelNumber (10000); above → decimal
		{maxLabelNumber + 1, "10001"},
		{20000, "20000"},
		// below 1 → decimal
		{0, "0"},
		{-1, "-1"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d", tc.n), func(t *testing.T) {
			got := toLetters(tc.n)
			if got != tc.want {
				t.Errorf("toLetters(%d) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}
