// cmap_bfrange_bench_test.go — benchmark for the bfrange lookup path, which
// dominates CMaps that express ToUnicode as ranges rather than per-char bfchar.

package pdf

import (
	"fmt"
	"strings"
	"testing"
)

// buildBfrangeHeavyCmap returns a ToUnicode CMap stream with n contiguous 2-byte
// bfrange blocks of 16 codes each. It models the common CJK case where the
// ToUnicode mapping is expressed as ranges rather than per-character bfchar
// entries, so the bfrange lookup — not the bfchar lookup — dominates decoding.
// It goes through readCmap/Decode only, so it compiles unchanged against both
// the pre-fix (flat slice, linear scan) and bucketed (binary search) cmap.
func buildBfrangeHeavyCmap(n int) string {
	var sb strings.Builder
	sb.WriteString(standardCmapHeader)
	sb.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	fmt.Fprintf(&sb, "%d beginbfrange\n", n)
	for k := 0; k < n; k++ {
		lo := 0x1000 + k*16
		fmt.Fprintf(&sb, "<%04X> <%04X> <%04X>\n", lo, lo+15, 0x4000+k*16)
	}
	sb.WriteString("endbfrange\n")
	sb.WriteString(standardCmapFooter)
	return sb.String()
}

// BenchmarkCmapBfrangeHeavy decodes one code from the middle of every range, so
// each glyph drives a full bfrange lookup. Before the length-bucketed binary
// search this was O(glyphs × entries); the benchmark makes that scan visible.
func BenchmarkCmapBfrangeHeavy(b *testing.B) {
	const n = 2000
	m := readCmap(makeCmapStream(buildBfrangeHeavyCmap(n)))
	if m == nil {
		b.Fatal("readCmap returned nil for bfrange-heavy stream")
	}
	input := make([]byte, 0, n*2)
	for k := 0; k < n; k++ {
		code := 0x1000 + k*16 + 8 // middle of range k
		input = append(input, byte(code>>8), byte(code))
	}
	raw := string(input)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Decode(raw)
	}
}
