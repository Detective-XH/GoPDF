// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

// decodeKrutiDev010Widths is a width-tracked port of decodeKrutiDev010 (legacy_krutidev010.go): the
// SAME pass0-6 transformations (reusing the SAME decision logic, lookup tables and helper functions —
// kd2Vertbar, kd2Infelicity, kd3Unpack, kdMatchCons, kd6TwoByte, kd6Single, kdIsNasal, kdIsDepVowel,
// kdIsDigit, kdAt — verbatim, zero behavioural drift), but operating on a parallel (code, width) pair
// instead of a bare code, so a CID's REAL /W-derived advance (cidWidth) survives the visual→logical
// reorder.
//
// Width-bookkeeping rule (generalizes the existing one-CID-to-many-runes precedent in
// content.go's layoutComposite): whenever a pass step consumes N input units and produces M output
// units (N==M for a pure reorder; N!=M for an unpack/merge step), the LAST output unit carries the SUM
// of all N consumed input widths, and every other output unit carries zero. A pure 1:1 step (the
// overwhelming common case: an ordinary consonant/vowel code with no merge/split rule) carries its
// single input's width through unchanged. This keeps the run's TOTAL advance invariant under reorder
// (permutation cannot change a sum), so a run's end-X — and therefore the next run's start-X, the
// quantity that establishes a word/cell boundary — lands exactly where the unmodified layoutComposite
// path would have put it; only the INTRA-run glyph-to-glyph split is approximated for the rare
// split/merge steps.
type kdUnit struct {
	code int
	w    float64
}

// kdGlyph is one final output rune (post pass-6) with its assigned width.
type kdGlyph struct {
	r rune
	w float64
}

func decodeKrutiDev010Widths(units []kdUnit, fallback TextEncoding) []kdGlyph {
	units = kd010Pass0W(units)
	units = kd010Pass1W(units)
	units = kd010Pass2W(units)
	units = kd010Pass3W(units)
	units = kd010Pass4W(units)
	units = kd010Pass5W(units)
	return kd010Pass6W(units, fallback)
}

func kdUnitCodes(in []kdUnit) []int {
	out := make([]int, len(in))
	for i, u := range in {
		out[i] = u.code
	}
	return out
}

func kdSumW(units []kdUnit) float64 {
	var total float64
	for _, u := range units {
		total += u.w
	}
	return total
}

// kdAssignLast zips codes (a pass's produced output codes) into kdUnits where every unit but the last
// carries zero width and the last carries total — the consumed input width(s), summed. See the
// width-bookkeeping rule above.
func kdAssignLast(codes []int, total float64) []kdUnit {
	out := make([]kdUnit, len(codes))
	for i, c := range codes {
		out[i] = kdUnit{code: c}
	}
	if len(out) > 0 {
		out[len(out)-1].w = total
	}
	return out
}

// --- Pass 0 (width-tracked): re-order nasals — pure permutation, widths travel with their unit. ---
func kd010Pass0W(in []kdUnit) []kdUnit {
	out := make([]kdUnit, 0, len(in))
	for i := 0; i < len(in); i++ {
		if i+1 < len(in) && kdIsNasal(in[i].code) && kdIsDepVowel(in[i+1].code) {
			out = append(out, in[i+1], in[i])
			i++
			continue
		}
		out = append(out, in[i])
	}
	return out
}

// --- Pass 1 (width-tracked): normalize duplicate/variant codes; collapsed duplicates fold their
// width into the surviving unit (so the run's total width is unchanged by the collapse). ---
func kd010Pass1W(in []kdUnit) []kdUnit {
	out := make([]kdUnit, 0, len(in))
	for _, u := range in {
		c := u.code
		if r, ok := kd1Remap[c]; ok {
			c = r
		}
		if (c == 97 || c == 161 || c == 87) && len(out) > 0 && out[len(out)-1].code == c {
			out[len(out)-1].w += u.w
			continue
		}
		out = append(out, kdUnit{code: c, w: u.w})
	}
	return out
}

// --- Pass 2 (width-tracked): half-consonant+vertbar / infelicity fixes, via the SAME kd2Vertbar /
// kd2Infelicity decision functions used by the unmodified transducer (called on the bare-code view of
// the buffered units), so the linguistic decision is byte-for-byte identical to production. ---
func kd010Pass2W(in []kdUnit) []kdUnit {
	codes := kdUnitCodes(in)
	out := make([]kdUnit, 0, len(in))
	for i := 0; i < len(in); i++ {
		if rep, adv := kd2Vertbar(codes, i); adv > 0 {
			out = append(out, kdAssignLast(rep, kdSumW(in[i:i+adv]))...)
			i += adv - 1
			continue
		}
		if rep, adv := kd2Infelicity(codes, i); adv > 0 {
			out = append(out, kdAssignLast(rep, kdSumW(in[i:i+adv]))...)
			i += adv - 1
			continue
		}
		out = append(out, in[i])
	}
	return out
}

// --- Pass 3 (width-tracked): positional dependent vowels + unpack combined glyphs, via kd3Unpack
// (same map the unmodified transducer uses). ---
func kd010Pass3W(in []kdUnit) []kdUnit {
	out := make([]kdUnit, 0, len(in)+8)
	for _, u := range in {
		switch u.code {
		case 162:
			out = append(out, kdUnit{code: 115, w: u.w})
		case 170:
			out = append(out, kdUnit{code: 122, w: u.w})
		default:
			if rep, ok := kd3Unpack[u.code]; ok {
				out = append(out, kdAssignLast(rep, u.w)...)
			} else {
				out = append(out, u)
			}
		}
	}
	return out
}

// --- Pass 4 (width-tracked): rearrange ikar+reph adjacency — pure permutation of the consumed units
// (IKAR, optional REPH, optional nasal, consonant cluster), via kdMatchCons on the bare-code view. ---
func kd010Pass4W(in []kdUnit) []kdUnit {
	codes := kdUnitCodes(in)
	out := make([]kdUnit, 0, len(in))
	for i := 0; i < len(in); {
		if codes[i] == kdIKAR {
			j := i + 1
			reph := false
			rephIdx := -1
			nasIdx := -1
			if j < len(in) && codes[j] == kdREPH {
				reph = true
				rephIdx = j
				j++
			}
			if j < len(in) && kdIsNasal(codes[j]) {
				nasIdx = j
				j++
			}
			ce := kdMatchCons(codes, j)
			if ce > j && (reph || nasIdx >= 0) {
				out = append(out, in[i])
				out = append(out, in[j:ce]...)
				if reph {
					out = append(out, in[rephIdx])
				}
				if nasIdx >= 0 {
					out = append(out, in[nasIdx])
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

// --- Pass 5 (width-tracked): the main visual→logical syllable reorder — pure permutation of the
// matched syllable's units (tracked by INDEX here, rather than by VALUE as kd5Syl does, so the
// width-bearing unit travels with its code to its reordered position). ---
type kd5SylW struct {
	ikarIdx                          int
	cs, ce                           int
	nuktaIdx, vwlIdx, nasIdx, rephIx int
}

func kd010Pass5W(in []kdUnit) []kdUnit {
	codes := kdUnitCodes(in)
	out := make([]kdUnit, 0, len(in))
	for i := 0; i < len(in); {
		s, ni, ok := kd5MatchW(codes, i)
		if !ok {
			out = append(out, in[i])
			i++
			continue
		}
		out = kd5EmitW(out, in, s)
		i = ni
	}
	return out
}

func kd5MatchW(codes []int, i int) (kd5SylW, int, bool) {
	s := kd5SylW{ikarIdx: -1, nuktaIdx: -1, vwlIdx: -1, nasIdx: -1, rephIx: -1}
	if codes[i] == kdIKAR {
		s.ikarIdx = i
		i++
	}
	s.cs = i
	s.ce = kdMatchCons(codes, i)
	if s.ce == s.cs {
		return s, 0, false
	}
	ni := kd5MarkersW(codes, s.ce, &s)
	return s, ni, true
}

func kd5MarkersW(codes []int, i int, s *kd5SylW) int {
	if i < len(codes) && codes[i] == kdNUKTA {
		s.nuktaIdx = i
		i++
	}
	if i < len(codes) && kdIsDepVowel(codes[i]) {
		s.vwlIdx = i
		i++
	}
	if i < len(codes) && codes[i] == kdREPH {
		s.rephIx = i
		i++
	}
	if i < len(codes) && kdIsNasal(codes[i]) {
		s.nasIdx = i
		i++
	}
	return i
}

func kd5EmitW(out, in []kdUnit, s kd5SylW) []kdUnit {
	if s.rephIx >= 0 {
		out = append(out, in[s.rephIx])
	}
	out = append(out, in[s.cs:s.ce]...)
	if s.nuktaIdx >= 0 {
		out = append(out, in[s.nuktaIdx])
	}
	if s.ikarIdx >= 0 {
		out = append(out, in[s.ikarIdx])
	}
	if s.vwlIdx >= 0 {
		out = append(out, in[s.vwlIdx])
	}
	if s.nasIdx >= 0 {
		out = append(out, in[s.nasIdx])
	}
	return out
}

// --- Pass 6 (width-tracked): code → Unicode rune(s), via kd6TwoByte/kd6Single (same tables the
// unmodified transducer uses). A code/pair that maps to several runes assigns zero width to every
// rune but the last, which carries the consumed unit(s)' summed width — generalizing the existing
// one-CID-to-many-runes precedent in layoutComposite across a multi-CID transducer step too. ---
func kd010Pass6W(in []kdUnit, fallback TextEncoding) []kdGlyph {
	codes := kdUnitCodes(in)
	var out []kdGlyph
	for i := 0; i < len(in); i++ {
		c := codes[i]
		if c == int(noRune) {
			out = append(out, kdGlyph{r: noRune, w: in[i].w})
			continue
		}
		if s, ok := kd6TwoByte(c, kdAt(codes, i+1)); ok {
			out = append(out, kdRunesW(s, in[i].w+in[i+1].w)...)
			i++
			continue
		}
		if c == 37 {
			if i > 0 && kdIsDigit(codes[i-1]) {
				out = append(out, kdGlyph{r: ':', w: in[i].w})
			} else {
				out = append(out, kdGlyph{r: 'ः', w: in[i].w})
			}
			continue
		}
		if s, ok := kd6Single[c]; ok {
			out = append(out, kdRunesW(s, in[i].w)...)
			continue
		}
		if c >= 0 && c <= 32 {
			out = append(out, kdGlyph{r: rune(c), w: in[i].w})
			continue
		}
		out = append(out, kdRunesW(fallback.Decode(string([]byte{byte(c)})), in[i].w)...)
	}
	return out
}

// kdRunesW splits s into runes, assigning zero width to every rune but the last, which carries total.
func kdRunesW(s string, total float64) []kdGlyph {
	rs := []rune(s)
	out := make([]kdGlyph, len(rs))
	for i, r := range rs {
		out[i] = kdGlyph{r: r}
	}
	if len(out) > 0 {
		out[len(out)-1].w = total
	}
	return out
}
