package pdf

import (
	"fmt"
	"testing"
)

// buildWideBfrangeCmap returns a CMap with a 2-byte codespace and a single
// bfrange entry whose source codes and destination are 2-byte values.
func buildWideBfrangeCmap(lo, hi, dst uint16) string {
	return fmt.Sprintf("%s1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n1 beginbfrange\n<%04X> <%04X> <%04X>\nendbfrange\n%s",
		standardCmapHeader, lo, hi, dst, standardCmapFooter)
}

// TestCmapDecodeBfrangeWide covers bfrange entries that span more than 256
// codes. The offset from the range start must be applied over the full code
// width, not only to the last byte; otherwise every code above 0x00FF decodes
// to its low byte (e.g. U+D569 -> U+0069). Some producers (for example
// go-pdf/fpdf with UTF-8 fonts) emit a single identity range <0000> <FFFF> <0000>.
func TestCmapDecodeBfrangeWide(t *testing.T) {
	tests := []struct {
		name        string
		lo, hi, dst uint16
		in          string
		want        rune
	}{
		{"identity ASCII", 0x0000, 0xFFFF, 0x0000, "\x00\x41", 0x0041},
		{"identity U+D569 (Hangul)", 0x0000, 0xFFFF, 0x0000, "\xD5\x69", 0xD569},
		{"identity U+AC00 (Hangul)", 0x0000, 0xFFFF, 0x0000, "\xAC\x00", 0xAC00},
		{"identity first code", 0x0000, 0xFFFF, 0x0000, "\x00\x00", 0x0000},
		{"identity last code", 0x0000, 0xFFFF, 0x0000, "\xFF\xFF", 0xFFFF},
		{"nonzero start, same dst", 0xAC00, 0xD7A3, 0xAC00, "\xD5\x5C", 0xD55C},
		{"offset dst across high byte", 0x0100, 0x02FF, 0x1000, "\x01\x80", 0x1080},
		{"offset dst across high byte, high step", 0x0100, 0x02FF, 0x1000, "\x02\x05", 0x1105},
		{"dst low byte carries into high byte", 0x0000, 0x01FF, 0x00FF, "\x00\x02", 0x0101},
		{"range within one high byte unchanged", 0x0000, 0x00FF, 0x0000, "\x00\x41", 0x0041},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := readCmap(makeCmapStream(buildWideBfrangeCmap(tt.lo, tt.hi, tt.dst)))
			if m == nil {
				t.Fatal("readCmap returned nil")
			}
			want := string(tt.want)
			if got := m.Decode(tt.in); got != want {
				t.Errorf("Decode(%q): got %+q, want %+q", tt.in, got, want)
			}
		})
	}
}
