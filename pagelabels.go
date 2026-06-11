package pdf

import (
	"sort"
	"strconv"
	"strings"
)

// maxLabelNumber caps the roman/letter conversion range. Values outside
// [1, maxLabelNumber] fall back to decimal to prevent a hostile /St
// (e.g. 9e18) from generating billions of characters.
const maxLabelNumber = 10000

// labelRange holds one entry from the /PageLabels number tree:
// the 0-based page-index start and the label-dict for that range.
type labelRange struct {
	start int
	dict  Value
}

// PageLabels returns the printed page label for every page: 1-based page N is
// index N-1 in the returned slice (len == NumPage()). Labels come from the
// document's /PageLabels number tree (PDF 32000-1 §12.4.2): an optional
// prefix (/P) + a numbering style (/S: decimal, upper/lower roman,
// upper/lower letters) + a per-range start value (/St, default 1).
//
// Returns nil when the document declares no /PageLabels tree (or none could
// be parsed) — callers then fall back to the 1-based page number. A page not
// covered by any label range gets "" at its index. Best-effort: malformed
// ranges are skipped, never error. Deterministic, safe for concurrent use,
// mutates no Reader/Page state.
func (r *Reader) PageLabels() []string {
	tree := r.Trailer().Key("Root").Key("PageLabels")
	if tree.IsNull() {
		return nil
	}

	n := r.NumPage()
	var ranges []labelRange
	collectNumberTree(tree, make(map[uint32]bool), 0, func(key int64, dict Value) {
		// Skip keys outside [0, n): a negative key is malformed (it would wrongly
		// cover page 0) and a key >= n covers no page. This also bounds the slice
		// against an adversarial /Nums.
		if key < 0 || key >= int64(n) {
			return
		}
		ranges = append(ranges, labelRange{start: int(key), dict: dict})
	})
	if len(ranges) == 0 {
		return nil
	}

	// Stable sort so that on a malformed duplicate-key /Nums the input order of
	// equal keys is preserved: combined with the forward sweep below, the LAST
	// /Nums entry for a duplicate key wins. (sort.Slice is also deterministic,
	// but for >12 equal keys its pdqsort path could let an earlier entry win —
	// SliceStable pins the last-entry-wins semantic regardless of count.)
	sort.SliceStable(ranges, func(i, j int) bool {
		return ranges[i].start < ranges[j].start
	})

	// Single forward sweep: page indices increase monotonically, so the covering
	// range index only advances — O(n + len(ranges)) instead of O(n × len(ranges)).
	out := make([]string, n)
	ri := -1
	for p := range n {
		for ri+1 < len(ranges) && ranges[ri+1].start <= p {
			ri++
		}
		if ri >= 0 {
			out[p] = formatLabel(ranges[ri], p)
		}
	}
	return out
}

// collectNumberTree walks a PDF number tree rooted at node and invokes fn for
// every (key, value) leaf pair in tree order. It mirrors collectNameTree
// (attachments.go) exactly but reads /Nums (integer keys) instead of /Names.
// The depth cap (maxLinkDepth) prevents stack overflow on a deeply nested
// tree; the visited set short-circuits /Kids cycles that point back to an
// ancestor node.
func collectNumberTree(node Value, seen map[uint32]bool, depth int, fn func(key int64, dict Value)) {
	if depth > maxLinkDepth {
		return
	}
	if id := node.ptr.id; id != 0 {
		if seen[id] {
			return
		}
		seen[id] = true
	}
	if nums := node.Key("Nums"); !nums.IsNull() {
		for i := 0; i+1 < nums.Len(); i += 2 {
			if nums.Index(i).Kind() == Integer {
				fn(nums.Index(i).Int64(), nums.Index(i+1))
			}
		}
	}
	kids := node.Key("Kids")
	for i := 0; i < kids.Len(); i++ {
		collectNumberTree(kids.Index(i), seen, depth+1, fn)
	}
}

// formatLabel renders the label for 0-based page index p using its covering
// range r. The number is computed in int64 to avoid 32-bit truncation and
// overflow on a hostile /St; roman/letter styles fall back to decimal outside
// [1, maxLabelNumber] so they cannot generate unbounded output.
func formatLabel(r labelRange, p int) string {
	// /St default is 1; read explicitly — Int64() returns 0 when absent, which
	// would start every default range at 0 (a whole-document off-by-one).
	st := int64(1)
	if v := r.dict.Key("St"); v.Kind() == Integer {
		st = v.Int64()
	}
	num := st + int64(p-r.start)

	prefix := r.dict.Key("P").Text()
	style := r.dict.Key("S").Name()

	// Decimal always renders the full number (digits are inherently bounded).
	if style == "D" {
		return prefix + strconv.FormatInt(num, 10)
	}
	// Roman/letters are only safe to build inside [1, maxLabelNumber]; outside
	// that window fall back to decimal so a hostile /St cannot explode the output.
	if style == "R" || style == "r" || style == "A" || style == "a" {
		if num < 1 || num > maxLabelNumber {
			return prefix + strconv.FormatInt(num, 10)
		}
		small := int(num) // safe: 1..maxLabelNumber
		switch style {
		case "R":
			return prefix + toRoman(small)
		case "r":
			return prefix + strings.ToLower(toRoman(small))
		case "A":
			return prefix + toLetters(small)
		case "a":
			return prefix + strings.ToLower(toLetters(small))
		}
	}
	// Absent or unrecognized /S: label is the prefix only (may be "").
	return prefix
}

// toRoman converts num to an uppercase Roman numeral string.
// If num is outside [1, maxLabelNumber], it returns the decimal representation
// to guard against hostile /St values producing unbounded output.
func toRoman(num int) string {
	if num < 1 || num > maxLabelNumber {
		return strconv.Itoa(num)
	}

	type romanPair struct {
		val int
		sym string
	}
	pairs := []romanPair{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}

	var b strings.Builder
	for _, p := range pairs {
		for num >= p.val {
			b.WriteString(p.sym)
			num -= p.val
		}
	}
	return b.String()
}

// toLetters converts num to an uppercase repeated-letter string per PDF §12.4.2:
// 1→A, 26→Z, 27→AA, 28→BB, 52→ZZ, 53→AAA.
// If num is outside [1, maxLabelNumber], it returns the decimal representation.
func toLetters(num int) string {
	if num < 1 || num > maxLabelNumber {
		return strconv.Itoa(num)
	}
	letter := byte('A' + (num-1)%26)
	repeat := (num-1)/26 + 1
	return strings.Repeat(string(letter), repeat)
}
