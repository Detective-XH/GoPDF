// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import "strings"

// decodeKrutiDev010 is a pure-Go port of the SIL wsresources (MIT) KrutiDev010 TECkit mapping
// (scripts/Deva/legacy/kruti-dev-010/mappings/KrutiDev010.map, "Copyright (c) 2006 SIL International").
// See NOTICE. It recovers REAL searchable Unicode Devanagari from the legacy Kruti Dev 010 keyboard
// encoding, whose content-stream bytes otherwise decode to Latin gibberish on every existing path.
//
// Architecture (faithful to the source): the legacy bytes are reordered/normalized/unpacked at the
// BYTE level (passes 0–5: nasal reorder, duplicate normalize, half-form+vertbar, positional vowels +
// combined-glyph unpacking, ikar/reph adjacency, the main visual→logical syllable reorder), THEN mapped
// to Unicode (pass 6). The reorder runs on bytes — not on the mapped runes — because the source reorders
// whole-cluster tokens that are single bytes expanding to 3–5 runes (41→द्ध, 123→क्ष्), so reordering
// after expansion would require re-deriving cluster boundaries from an overloaded VIRAMA.
//
// ZERO_WIDTH_JOINER/NON_JOINER are intentionally NOT emitted (the goal is searchable Unicode for
// LLM/RAG; a ZWJ inside a conjunct breaks plain-word search). Only the forward (legacy→Unicode)
// direction is ported (the source's `<>`/`>` rules); reverse-only `<` rules are skipped. Codes with no
// rule fall back to the font's own decode (Latin), so an unmapped byte degrades to today's gibberish
// rather than dropping.
func decodeKrutiDev010(raw string, fallback TextEncoding) string {
	codes := make([]int, len(raw))
	for i := 0; i < len(raw); i++ {
		codes[i] = int(raw[i])
	}
	codes = kd010Pass0(codes) // re-order nasals (nasal after a following dependent vowel)
	codes = kd010Pass1(codes) // normalize duplicate / variant glyph codes
	codes = kd010Pass2(codes) // half-consonant + vertical-bar → full; input-infelicity fixes
	codes = kd010Pass3(codes) // positional dependent vowels + unpack combined glyphs
	codes = kd010Pass4(codes) // rearrange ikar+reph adjacency to feed the reorder
	codes = kd010Pass5(codes) // the main visual→logical syllable reorder
	return kd010Pass6(codes, fallback)
}

// --- byte-class definitions (the source's Define block) ---

const (
	kdIKAR    = 102 // short-i matra ि, stored pre-base (reordered)
	kdNUKTA   = 43
	kdREPH    = 90  // र् above-base marker, stored post-cluster (reordered to the front)
	kdVERTBAR = 107 // vertical bar; doubles as the aa-matra ा
)

// kdSet builds an int membership set.
func kdSet(xs ...int) map[int]bool {
	m := make(map[int]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

var (
	kdHConlyHForms = []int{34, 39, 46, 47, 63, 70, 91, 184}
	kdHConlySpec   = []int{159, 218, 123}
	kdHCalsoSpec   = []int{153, 171}
	kdHCalsoFForms = []int{67, 68, 69, 72, 73, 182, 76, 79, 80, 82, 84, 85, 88, 89, 184, 186, 214}
	kdFCalsoHForms = []int{99, 100, 101, 210, 105, 81, 108, 111, 112, 114, 116, 117, 120, 121, 59, 103, 62}
	kdFCall        = []int{59, 60, 62, 66, 71, 77, 78, 81, 86, 99, 100, 101, 103, 105, 106, 108, 110, 111, 112, 114, 116, 117, 120, 121, 165, 179, 196, 210, 211}
	kdSCall        = []int{41, 61, 74, 75, 124, 125, 163, 193, 204, 205, 206, 207, 212, 216, 221, 224, 225, 227, 228, 230, 233, 240, 243, 244, 152}
	kdDepVowelAll  = []int{37, 104, 83, 96, 164, 119, kdVERTBAR, 115, 113, 168, 169}
	kdNall         = []int{97, 161, 87}
	kdPCall        = []int{122, 170, kdNUKTA}

	// HC (passes 4/5) = all half-consonant forms; C = full consonants ∪ stacks; PC = post-consonant.
	kdHCset      = kdSet(append(append(append(append([]int{}, kdHConlyHForms...), kdHConlySpec...), kdHCalsoSpec...), kdHCalsoFForms...)...)
	kdCset       = kdSet(append(append([]int{}, kdFCall...), kdSCall...)...)
	kdPCset      = kdSet(kdPCall...)
	kdDepVowel   = kdSet(kdDepVowelAll...)
	kdNasalSet   = kdSet(kdNall...)
	kdHConlySet  = kdSet(append(append([]int{}, kdHConlyHForms...), kdHConlySpec...)...) // pass-2 HConly
	kdHCalsoSet  = kdSet(kdHCalsoFForms...)                                              // pass-2 HCalso
	kdHCalsoToFC = func() map[int]int {                                                  // pass-2 [HC] VERTBAR → [FC]
		m := make(map[int]int, len(kdHCalsoFForms))
		for i, b := range kdHCalsoFForms {
			m[b] = kdFCalsoHForms[i]
		}
		return m
	}()
)

func kdIsNasal(c int) bool    { return kdNasalSet[c] }
func kdIsDepVowel(c int) bool { return kdDepVowel[c] }
func kdIsHC(c int) bool       { return kdHCset[c] }
func kdIsC(c int) bool        { return kdCset[c] }
func kdIsPC(c int) bool       { return kdPCset[c] }
func kdIsDigit(c int) bool    { return (c >= 48 && c <= 57) || (c >= 131 && c <= 140) }

// kdMatchCons matches a consonant cluster `[HC]* ( C | HC VERTBAR ) [PC]?` starting at i and returns the
// end index (exclusive), or i when no cluster begins there.
func kdMatchCons(in []int, i int) int {
	j, matched := i, false
	for j < len(in) {
		if kdIsC(in[j]) {
			j++
			matched = true
			break
		}
		if j+1 < len(in) && kdIsHC(in[j]) && in[j+1] == kdVERTBAR {
			j += 2
			matched = true
			break
		}
		if kdIsHC(in[j]) {
			j++ // an HC in the [HC]* prefix
			continue
		}
		break
	}
	if !matched {
		return i
	}
	if j < len(in) && kdIsPC(in[j]) {
		j++ // optional post-consonant (low-r / tent-r / nukta)
	}
	return j
}

// --- Pass 0: re-order nasals (nasal before a dependent vowel → after it) ---
func kd010Pass0(in []int) []int {
	out := make([]int, 0, len(in))
	for i := 0; i < len(in); i++ {
		if i+1 < len(in) && kdIsNasal(in[i]) && kdIsDepVowel(in[i+1]) {
			out = append(out, in[i+1], in[i])
			i++
			continue
		}
		out = append(out, in[i])
	}
	return out
}

// --- Pass 1: normalize duplicate / variant glyph codes ---
var kd1Remap = map[int]int{
	147: 39, 148: 39, 145: 34, 146: 34, 167: 164, 174: 168, 180: 165, 203: 47, 209: 151,
	144: 210, 217: 159, 231: 193, 232: 47, 234: 205, 235: 206, 236: 207, 237: 204, 238: 211,
	239: 212, 245: 155, 246: 152, 247: 214, 248: 191,
}

func kd010Pass1(in []int) []int {
	out := make([]int, 0, len(in))
	for _, c := range in {
		if r, ok := kd1Remap[c]; ok {
			c = r
		}
		// collapse a run of one nasal type (the source's `97+ > 97`, `161+`, `87+`).
		if (c == 97 || c == 161 || c == 87) && len(out) > 0 && out[len(out)-1] == c {
			continue
		}
		out = append(out, c)
	}
	return out
}

// --- Pass 2: half-consonant + vertical-bar → full; input-infelicity fixes ---
func kd010Pass2(in []int) []int {
	out := make([]int, 0, len(in))
	for i := 0; i < len(in); i++ {
		if rep, adv := kd2Vertbar(in, i); adv > 0 {
			out = append(out, rep...)
			i += adv - 1
			continue
		}
		if rep, adv := kd2Infelicity(in, i); adv > 0 {
			out = append(out, rep...)
			i += adv - 1
			continue
		}
		out = append(out, in[i])
	}
	return out
}

// kd2Vertbar handles the vertical-bar (full-form) and okar/aukar rules. Returns the replacement codes
// and the number of input codes consumed (0 = no match).
func kd2Vertbar(in []int, i int) ([]int, int) {
	c := in[i]
	nxt, nxt2 := kdAt(in, i+1), kdAt(in, i+2)
	if nxt == kdVERTBAR {
		if fc, ok := kdHCalsoToFC[c]; ok {
			return []int{fc}, 2
		}
		if c == 171 {
			return []int{61}, 2
		}
		if c == 153 {
			return []int{233}, 2
		}
	}
	if kdHConlySet[c] && (nxt == 174 || nxt == 168) {
		return []int{c, kdVERTBAR, 115}, 2
	}
	if kdHConlySet[c] && nxt == 169 {
		return []int{c, kdVERTBAR, 83}, 2
	}
	if kdHCalsoSet[c] && nxt == 122 && nxt2 == kdVERTBAR {
		return []int{c, kdVERTBAR, 122}, 3
	}
	return nil, 0
}

// kd2Infelicity handles the source's input-typo fixes (longest match first).
func kd2Infelicity(in []int, i int) ([]int, int) {
	c := in[i]
	nxt, nxt2 := kdAt(in, i+1), kdAt(in, i+2)
	if c == 107 && nxt == 87 && nxt2 == 97 {
		return []int{107, 161}, 3
	}
	switch {
	case c == 107 && nxt == 115:
		return []int{168}, 2
	case c == 107 && nxt == 83:
		return []int{169}, 2
	case c == 98 && nxt == 90:
		return []int{195}, 2
	case c == 87 && nxt == 97:
		return []int{161}, 2
	case c == 107 && nxt == 87:
		return []int{130}, 2
	}
	return nil, 0
}

func kdAt(in []int, i int) int {
	if i < 0 || i >= len(in) {
		return -1
	}
	return in[i]
}

// --- Pass 3: positional dependent vowels + unpack combined glyphs ---
// 162→115 (left-ekar) and 170→122 (tent-r low-r) are unconditional in the forward direction; the
// source's context only disambiguates the reverse direction (the font emits these codes only in context).
var kd3Unpack = map[int][]int{
	35: {106, 113}, 58: {106, 119}, 150: {110, 96}, 151: {100, 96}, 226: {103, 96},
	198: {102, 90}, 199: {102, 97}, 200: {104, 97}, 201: {102, 90, 97}, 202: {104, 90},
	155: {83, 90, 97}, 177: {90, 97},
}

func kd010Pass3(in []int) []int {
	out := make([]int, 0, len(in)+8)
	for _, c := range in {
		switch c {
		case 162:
			out = append(out, 115)
		case 170:
			out = append(out, 122)
		default:
			if u, ok := kd3Unpack[c]; ok {
				out = append(out, u...)
			} else {
				out = append(out, c)
			}
		}
	}
	return out
}

// --- Pass 4: rearrange ikar+reph adjacency (IKAR REPH? N? cons → IKAR cons REPH? N?) ---
func kd010Pass4(in []int) []int {
	out := make([]int, 0, len(in))
	for i := 0; i < len(in); {
		if in[i] == kdIKAR {
			j := i + 1
			reph := false
			nasVal := -1
			if j < len(in) && in[j] == kdREPH {
				reph = true
				j++
			}
			if j < len(in) && kdIsNasal(in[j]) {
				nasVal = in[j]
				j++
			}
			ce := kdMatchCons(in, j)
			if ce > j && (reph || nasVal >= 0) {
				out = append(out, kdIKAR)
				out = append(out, in[j:ce]...)
				if reph {
					out = append(out, kdREPH)
				}
				if nasVal >= 0 {
					out = append(out, nasVal)
				}
				i = ce
				continue
			}
		}
		out = append(out, in[i])
		i++
	}
	return out
}

// --- Pass 5: the main visual→logical syllable reorder ---
// IKAR? cons NUKTA? DepVowel? REPH? N?  →  REPH? cons NUKTA? IKAR? DepVowel? N?
type kd5Syl struct {
	ikar               bool
	cs, ce             int
	nukta, vwl, nasVal int
	reph               bool
}

func kd010Pass5(in []int) []int {
	out := make([]int, 0, len(in))
	for i := 0; i < len(in); {
		s, ni, ok := kd5Match(in, i)
		if !ok {
			out = append(out, in[i]) // a lone ikar or a non-cluster code passes through
			i++
			continue
		}
		out = kd5Emit(out, in, s)
		i = ni
	}
	return out
}

// kd5Match matches one syllable at i. ok is false (and ni unused) when no consonant cluster begins
// there — including a lone IKAR, which the caller then emits verbatim.
func kd5Match(in []int, i int) (kd5Syl, int, bool) {
	s := kd5Syl{nukta: -1, vwl: -1, nasVal: -1}
	if in[i] == kdIKAR {
		s.ikar = true
		i++
	}
	s.cs = i
	s.ce = kdMatchCons(in, i)
	if s.ce == s.cs {
		return s, 0, false
	}
	return s, kd5Markers(in, s.ce, &s), true
}

// kd5Markers collects the optional post-cluster markers (nukta, dependent vowel, reph, nasal).
func kd5Markers(in []int, i int, s *kd5Syl) int {
	if i < len(in) && in[i] == kdNUKTA {
		s.nukta = kdNUKTA
		i++
	}
	if i < len(in) && kdIsDepVowel(in[i]) {
		s.vwl = in[i]
		i++
	}
	if i < len(in) && in[i] == kdREPH {
		s.reph = true
		i++
	}
	if i < len(in) && kdIsNasal(in[i]) {
		s.nasVal = in[i]
		i++
	}
	return i
}

// kd5Emit appends the syllable in Unicode order: reph, cluster, nukta, ikar, vowel, nasal.
func kd5Emit(out, in []int, s kd5Syl) []int {
	if s.reph {
		out = append(out, kdREPH)
	}
	out = append(out, in[s.cs:s.ce]...)
	if s.nukta >= 0 {
		out = append(out, s.nukta)
	}
	if s.ikar {
		out = append(out, kdIKAR)
	}
	if s.vwl >= 0 {
		out = append(out, s.vwl)
	}
	if s.nasVal >= 0 {
		out = append(out, s.nasVal)
	}
	return out
}

// --- Pass 6: byte → Unicode (ZWJ/ZWNJ stripped; fallback for unmapped) ---
func kd010Pass6(in []int, fallback TextEncoding) string {
	var sb strings.Builder
	for i := 0; i < len(in); i++ {
		c := in[i]
		if s, ok := kd6TwoByte(c, kdAt(in, i+1)); ok {
			sb.WriteString(s)
			i++
			continue
		}
		if c == 37 { // visarga, or a colon when it follows a digit
			if i > 0 && kdIsDigit(in[i-1]) {
				sb.WriteByte(':')
			} else {
				sb.WriteString("ः")
			}
			continue
		}
		if s, ok := kd6Single[c]; ok {
			sb.WriteString(s)
			continue
		}
		if c >= 0 && c <= 32 { // CTL / space passthrough
			sb.WriteByte(byte(c))
			continue
		}
		sb.WriteString(fallback.Decode(string([]byte{byte(c)})))
	}
	return sb.String()
}

// kd6TwoByte resolves the two-code Pass-6 rules (fixed pairs + the half-form/vertbar and nukta classes).
func kd6TwoByte(c, nxt int) (string, bool) {
	if nxt < 0 {
		return "", false
	}
	if s, ok := kd6Pair[c<<8|nxt]; ok {
		return s, true
	}
	if nxt == kdVERTBAR {
		if r, ok := kdFConlyFull[c]; ok { // half-only form + vertbar → full consonant
			return r, true
		}
	}
	if nxt == kdNUKTA {
		if r, ok := kdHCalsoFull[c]; ok { // half form + nukta → full + nukta + virama
			return r + "़्", true
		}
	}
	return "", false
}

// kd6Pair: fixed two-code → string rules.
var kd6Pair = map[int]string{
	65<<8 | 65:         "॥", // DOUBLE_DANDA
	44<<8 | 115:        "ऐ",
	118<<8 | kdVERTBAR: "आ",
	118<<8 | 168:       "ओ",
	118<<8 | 169:       "औ",
	123<<8 | kdVERTBAR: "क्ष",
	159<<8 | kdVERTBAR: "त्त",
}

// kdFConlyFull: HConlyHForms + vertbar → the full consonant.
var kdFConlyFull = map[int]string{
	34: "ष", 39: "श", 46: "ण", 47: "ध", 63: "घ", 70: "थ", 91: "ख", 184: "य",
}

// kdHCalsoFull: HCalsoFForms → the full consonant (used by the +nukta rule).
var kdHCalsoFull = map[int]string{
	67: "ब", 68: "क", 69: "म", 72: "भ", 73: "प", 182: "फ", 76: "स", 79: "व", 80: "च",
	82: "त", 84: "ज", 85: "न", 88: "ग", 89: "ल", 184: "य", 186: "ह", 214: "झ",
}

// kd6Single: the single-byte Pass-6 map (OneToOne digit/punct/consonant/vowel classes + bare half-forms
// + the stack/conjunct glyphs). ZWJ/ZWNJ stripped. Built once; the classes are disjoint by construction.
var kd6Single = map[int]string{
	// OneToOneDigit
	48: "0", 49: "1", 50: "2", 51: "3", 52: "4", 53: "5", 54: "6", 55: "7", 56: "8", 57: "9",
	131: "१", 132: "२", 133: "३", 134: "४", 135: "५", 136: "६", 137: "७", 138: "८", 139: "९", 140: "०",
	// OneToOnePunct
	33: "!", 36: "+", 38: "-", 40: ";", 45: ".", 92: "?", 93: ",", 94: "‘", 64: "/", 187: "÷",
	188: "(", 189: ")", 42: "’", 190: "=", 191: "{", 192: "}", 65: "।", 222: "”", 223: "“", 229: "॰", 219: "•",
	// OneToOneCs (37=visarga is handled in Pass 6 for the colon context)
	43: "़", 59: "य", 60: "ढ", 62: "झ", 66: "ठ", 71: "ळ", 77: "ड", 78: "छ", 81: "फ", 86: "ट",
	99: "ब", 100: "क", 101: "म", 103: "ह", 105: "प", 106: "र", 108: "स", 110: "द", 111: "व",
	112: "च", 114: "त", 116: "ज", 117: "न", 120: "ग", 121: "ल", 165: "ञ", 179: "ङ", 196: "घ", 210: "भ",
	// OneToOneVs
	118: "अ", 98: "इ", 195: "ई", 109: "उ", 197: "ऊ", 95: "ऋ", 44: "ए", 102: "ि", 104: "ी", 113: "ु",
	119: "ू", 96: "ृ", 115: "े", 83: "ै", 168: "ो", 169: "ौ", 183: "ऽ", 87: "ॅ", 97: "ं", 161: "ँ",
	107: "ा", 164: "ॄ", 130: "ॉ",
	// bare half-only forms → full + virama (HConlyHForms)
	34: "ष्", 39: "श्", 46: "ण्", 47: "ध्", 63: "घ्", 70: "थ्", 91: "ख्", 184: "य्",
	// bare half forms that also have full forms (HCalsoFForms) → full + virama
	67: "ब्", 68: "क्", 69: "म्", 72: "भ्", 73: "प्", 182: "फ्", 76: "स्", 79: "व्", 80: "च्",
	82: "त्", 84: "ज्", 85: "न्", 88: "ग्", 89: "ल्", 186: "ह्", 214: "झ्",
	// stack / conjunct / multi-rune glyphs (ZWJ/ZWNJ stripped)
	41: "द्ध", 61: "त्र", 74: "श्र", 75: "ज्ञ", 90: "र्", 122: "्र", 123: "क्ष्", 124: "द्य", 125: "द्व",
	126: "्", 152: "द्भ", 153: "न्न्", 159: "त्त्", 163: "ख्र", 171: "त्र्", 193: "प्र", 204: "द्द",
	205: "ट्ट", 206: "ट्ठ", 207: "ड्ड", 211: "य्", 212: "ड्ढ", 216: "क्र", 218: "र्", 220: "श्",
	221: "फ्र", 224: "ह्न", 225: "ह्य", 227: "ह्म", 228: "क्त", 230: "द्र", 233: "न्न", 240: "ठ्ठ",
	243: "स्त्र", 244: "क्क",
}
