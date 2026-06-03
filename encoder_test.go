// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"testing"
)

// encoderAssertDecode is a helper that calls enc.Decode(raw) and compares
// the result to want, failing the test if they differ.
func encoderAssertDecode(t *testing.T, enc TextEncoding, raw string, want string) {
	t.Helper()
	got := enc.Decode(raw)
	if got != want {
		t.Errorf("Decode(%q) = %q; want %q", raw, got, want)
	}
}

// TestEncoderWinAnsi verifies that WinAnsiEncoding maps known byte values to
// their expected Unicode characters.
func TestEncoderWinAnsi(t *testing.T) {
	enc := encoderForCMapName("WinAnsiEncoding")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for WinAnsiEncoding")
	}

	encoderAssertDecode(t, enc, "A", "A")         // 0x41 -> U+0041
	encoderAssertDecode(t, enc, "Z", "Z")         // 0x5A -> U+005A
	encoderAssertDecode(t, enc, "a", "a")         // 0x61 -> U+0061
	encoderAssertDecode(t, enc, "z", "z")         // 0x7A -> U+007A
	encoderAssertDecode(t, enc, " ", " ")         // 0x20 -> U+0020 SPACE
	encoderAssertDecode(t, enc, "Hello", "Hello") // multi-byte ASCII pass-through

	// 0xC0 -> U+00C0 LATIN CAPITAL LETTER A WITH GRAVE
	encoderAssertDecode(t, enc, "\xC0", "À")
	// 0xE9 -> U+00E9 LATIN SMALL LETTER E WITH ACUTE
	encoderAssertDecode(t, enc, "\xE9", "é")
	// 0x80 -> U+20AC EURO SIGN (WinAnsi-specific, differs from ISO-8859-1)
	encoderAssertDecode(t, enc, "\x80", "€")
	// 0xFF -> U+00FF LATIN SMALL LETTER Y WITH DIAERESIS
	encoderAssertDecode(t, enc, "\xFF", "ÿ")

	// empty input must return empty string
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderMacRoman verifies that MacRomanEncoding maps known byte values to
// their expected Unicode characters.
func TestEncoderMacRoman(t *testing.T) {
	enc := encoderForCMapName("MacRomanEncoding")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for MacRomanEncoding")
	}

	// ASCII range is identity in MacRoman
	encoderAssertDecode(t, enc, "A", "A")
	encoderAssertDecode(t, enc, "z", "z")
	encoderAssertDecode(t, enc, " ", " ")

	// 0x80 -> U+00C4 LATIN CAPITAL LETTER A WITH DIAERESIS
	encoderAssertDecode(t, enc, "\x80", "Ä")
	// 0x8A -> U+00E4 LATIN SMALL LETTER A WITH DIAERESIS
	encoderAssertDecode(t, enc, "\x8A", "ä")
	// 0xA0 -> U+2020 DAGGER
	encoderAssertDecode(t, enc, "\xA0", "†")
	// 0xB9 -> U+03C0 GREEK SMALL LETTER PI
	encoderAssertDecode(t, enc, "\xB9", "π")
	// 0xFF -> U+02C7 CARON
	encoderAssertDecode(t, enc, "\xFF", "ˇ")

	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderShiftJIS verifies that a Shift-JIS CMap name ("90ms-RKSJ-H")
// decodes Shift-JIS byte sequences to the correct Unicode characters.
func TestEncoderShiftJIS(t *testing.T) {
	enc := encoderForCMapName("90ms-RKSJ-H")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for 90ms-RKSJ-H")
	}

	// ASCII pass-through
	encoderAssertDecode(t, enc, "A", "A")

	// U+3042 HIRAGANA LETTER A (あ) in Shift-JIS: 0x82 0xA0
	encoderAssertDecode(t, enc, "\x82\xA0", "あ")

	// U+65E5 CJK UNIFIED IDEOGRAPH 日 in Shift-JIS: 0x93 0xFA
	encoderAssertDecode(t, enc, "\x93\xFA", "日")

	// empty input
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderGBK verifies that a GBK CMap name ("GB-EUC-H") decodes GBK byte
// sequences to the correct Unicode characters.
func TestEncoderGBK(t *testing.T) {
	enc := encoderForCMapName("GB-EUC-H")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for GB-EUC-H")
	}

	// ASCII pass-through
	encoderAssertDecode(t, enc, "A", "A")

	// U+4E2D CJK UNIFIED IDEOGRAPH 中 in GBK: 0xD6 0xD0
	encoderAssertDecode(t, enc, "\xD6\xD0", "中")

	// empty input
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderGBKVariants checks that the GBK alias CMap names all resolve to
// the same underlying encoder and produce the same output.
func TestEncoderGBKVariants(t *testing.T) {
	encoderGBKNames := []string{
		"GB-EUC-H",
		"GB-EUC-V",
		"GBKp-EUC-H",
		"GBKp-EUC-V",
		"GBK-EUC-H",
		"GBK-EUC-V",
	}
	encoderGBKInput := "\xD6\xD0" // 中 in GBK
	for _, name := range encoderGBKNames {
		enc := encoderForCMapName(name)
		if enc == nil {
			t.Fatalf("encoderForCMapName returned nil for %s", name)
		}
		got := enc.Decode(encoderGBKInput)
		if got != "中" {
			t.Errorf("CMap %s: Decode(%q) = %q; want %q", name, encoderGBKInput, got, "中")
		}
	}
}

// TestEncoderBig5 verifies that a Big5 CMap name ("ETen-B5-H") decodes Big5
// byte sequences to the correct Unicode characters.
func TestEncoderBig5(t *testing.T) {
	enc := encoderForCMapName("ETen-B5-H")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for ETen-B5-H")
	}

	// ASCII pass-through
	encoderAssertDecode(t, enc, "A", "A")

	// U+4E2D CJK UNIFIED IDEOGRAPH 中 in Big5: 0xA4 0xA4
	encoderAssertDecode(t, enc, "\xA4\xA4", "中")

	// empty input
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderEUCKR verifies that a EUC-KR CMap name ("KSCms-UHC-H") decodes
// EUC-KR byte sequences to the correct Unicode characters.
func TestEncoderEUCKR(t *testing.T) {
	enc := encoderForCMapName("KSCms-UHC-H")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for KSCms-UHC-H")
	}

	// ASCII pass-through
	encoderAssertDecode(t, enc, "A", "A")

	// U+AC00 HANGUL SYLLABLE GA (가) in EUC-KR: 0xB0 0xA1
	encoderAssertDecode(t, enc, "\xB0\xA1", "가")

	// empty input
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderUCS2BE verifies that a Uni*-UCS2-* CMap name ("UniJIS-UCS2-H")
// decodes big-endian 2-byte UCS-2 pairs to their corresponding BMP runes.
func TestEncoderUCS2BE(t *testing.T) {
	enc := encoderForCMapName("UniJIS-UCS2-H")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for UniJIS-UCS2-H")
	}

	// U+0041 LATIN CAPITAL LETTER A as UCS-2 BE: 0x00 0x41
	encoderAssertDecode(t, enc, "\x00\x41", "A")

	// U+4E2D CJK UNIFIED IDEOGRAPH 中: 0x4E 0x2D
	encoderAssertDecode(t, enc, "\x4E\x2D", "中")

	// U+3042 HIRAGANA LETTER A (あ): 0x30 0x42
	encoderAssertDecode(t, enc, "\x30\x42", "あ")

	// two code points: A then 中
	encoderAssertDecode(t, enc, "\x00\x41\x4E\x2D", "A中")

	// odd-length input — trailing byte is silently dropped (pairs only)
	got := enc.Decode("\x00\x41\x4E")
	if got != "A" {
		t.Errorf("UCS2BE odd-length: Decode(%q) = %q; want %q", "\x00\x41\x4E", got, "A")
	}

	// empty input
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderUCS2BEAllVariants checks that all Uni*-UCS2-* CMap names resolve
// to the same ucs2BEEncoder behaviour.
func TestEncoderUCS2BEAllVariants(t *testing.T) {
	encoderUCS2Names := []string{
		"UniGB-UCS2-H",
		"UniGB-UCS2-V",
		"UniCNS-UCS2-H",
		"UniCNS-UCS2-V",
		"UniJIS-UCS2-H",
		"UniJIS-UCS2-V",
		"UniKS-UCS2-H",
		"UniKS-UCS2-V",
	}
	encoderUCS2Input := "\x4E\x2D" // U+4E2D 中
	for _, name := range encoderUCS2Names {
		enc := encoderForCMapName(name)
		if enc == nil {
			t.Fatalf("encoderForCMapName returned nil for %s", name)
		}
		got := enc.Decode(encoderUCS2Input)
		if got != "中" {
			t.Errorf("CMap %s: Decode(%q) = %q; want %q", name, encoderUCS2Input, got, "中")
		}
	}
}

// TestEncoderUnknown verifies that an unrecognised CMap name falls back to
// pdfDocEncoding (which is identity for printable ASCII).
func TestEncoderUnknown(t *testing.T) {
	enc := encoderForCMapName("NonExistent-CMap-Name-XYZ")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for unknown name")
	}

	// pdfDocEncoding is identity for ASCII printable range (0x20-0x7E)
	encoderAssertDecode(t, enc, "Hello", "Hello")
	encoderAssertDecode(t, enc, "Test 123", "Test 123")
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderIdentityH verifies that Identity-H resolves and uses pdfDocEncoding.
func TestEncoderIdentityH(t *testing.T) {
	enc := encoderForCMapName("Identity-H")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil for Identity-H")
	}

	// pdfDocEncoding: ASCII printable range is identity
	encoderAssertDecode(t, enc, "A", "A")
	encoderAssertDecode(t, enc, "Hello World", "Hello World")
	encoderAssertDecode(t, enc, "", "")
}

// TestEncoderShiftJISVariants checks that all registered Shift-JIS CMap names
// decode the same input consistently.
func TestEncoderShiftJISVariants(t *testing.T) {
	encoderShiftJISNames := []string{
		"90ms-RKSJ-H",
		"90ms-RKSJ-V",
		"90pv-RKSJ-H",
	}
	// U+3042 HIRAGANA LETTER A (あ) in Shift-JIS: 0x82 0xA0
	encoderShiftJISInput := "\x82\xA0"
	for _, name := range encoderShiftJISNames {
		enc := encoderForCMapName(name)
		if enc == nil {
			t.Fatalf("encoderForCMapName returned nil for %s", name)
		}
		got := enc.Decode(encoderShiftJISInput)
		if got != "あ" {
			t.Errorf("CMap %s: Decode(%q) = %q; want %q", name, encoderShiftJISInput, got, "あ")
		}
	}
}

// TestEncoderEUCKRVariants checks that all registered EUC-KR CMap names decode
// the same input consistently.
func TestEncoderEUCKRVariants(t *testing.T) {
	encoderEUCKRNames := []string{
		"KSCms-UHC-H",
		"KSCms-UHC-V",
		"KSC-EUC-H",
		"KSC-EUC-V",
		"KSCms-UHC-HW-H",
		"KSCms-UHC-HW-V",
	}
	// U+AC00 가 in EUC-KR: 0xB0 0xA1
	encoderEUCKRInput := "\xB0\xA1"
	for _, name := range encoderEUCKRNames {
		enc := encoderForCMapName(name)
		if enc == nil {
			t.Fatalf("encoderForCMapName returned nil for %s", name)
		}
		got := enc.Decode(encoderEUCKRInput)
		if got != "가" {
			t.Errorf("CMap %s: Decode(%q) = %q; want %q", name, encoderEUCKRInput, got, "가")
		}
	}
}

// TestEncoderBig5Variants checks that all registered Big5 CMap names decode
// the same input consistently.
func TestEncoderBig5Variants(t *testing.T) {
	encoderBig5Names := []string{
		"ETen-B5-H",
		"ETen-B5-V",
		"ETenms-B5-H",
		"ETenms-B5-V",
	}
	// U+4E2D 中 in Big5: 0xA4 0xA4
	encoderBig5Input := "\xA4\xA4"
	for _, name := range encoderBig5Names {
		enc := encoderForCMapName(name)
		if enc == nil {
			t.Fatalf("encoderForCMapName returned nil for %s", name)
		}
		got := enc.Decode(encoderBig5Input)
		if got != "中" {
			t.Errorf("CMap %s: Decode(%q) = %q; want %q", name, encoderBig5Input, got, "中")
		}
	}
}

// TestEncoderWinAnsiMultiByte verifies that WinAnsiEncoding handles a sequence
// of multiple non-ASCII bytes correctly.
func TestEncoderWinAnsiMultiByte(t *testing.T) {
	enc := encoderForCMapName("WinAnsiEncoding")

	// 0xC0 (U+00C0 À) + 0xE9 (U+00E9 é) + 0x41 ('A')
	encoderAssertDecode(t, enc, "\xC0\xE9\x41", "ÀéA")
}

// TestEncoderUnknownWithDebug verifies that the DebugOn branch in encoderForCMapName
// is exercised and does not panic; the returned encoder still works.
func TestEncoderUnknownWithDebug(t *testing.T) {
	prev := DebugOn
	DebugOn = true
	defer func() { DebugOn = prev }()

	enc := encoderForCMapName("Totally-Unknown-CMap-For-Debug")
	if enc == nil {
		t.Fatal("encoderForCMapName returned nil under DebugOn=true")
	}
	// pdfDocEncoding: ASCII printable range is identity
	encoderAssertDecode(t, enc, "Hi", "Hi")
}

// TestEncoderMacRomanMultiByte verifies that MacRomanEncoding handles a sequence
// of multiple non-ASCII bytes correctly.
func TestEncoderMacRomanMultiByte(t *testing.T) {
	enc := encoderForCMapName("MacRomanEncoding")

	// 0x80 (U+00C4 Ä) + 0x41 ('A') + 0xB9 (U+03C0 π)
	encoderAssertDecode(t, enc, "\x80\x41\xB9", "ÄAπ")
}
