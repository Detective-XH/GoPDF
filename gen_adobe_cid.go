//go:build ignore

// Command gen_adobe_cid generates cidunicode_japan1.go: the Adobe-Japan1
// CID->Unicode table consumed by adobeCIDEncoder (decode-path for Identity-H/V
// Adobe-Japan1 CIDFonts that carry no /ToUnicode CMap).
//
// It fetches Adobe's authoritative cid2code.txt at a pinned commit, asserts a
// hard-coded SHA-256 of the fetched bytes (reproducibility guard: the 2.7 MB
// input is NOT committed; the generated ~50 KB []uint16 IS), parses the CID and
// UniJIS-UCS2 columns (with a UniJIS-UTF32 BMP fallback when UCS2 is absent),
// strips Adobe's trailing "v" vertical-variant annotation to the base scalar,
// and emits a []uint16 indexed by CID (0 = no mapping).
//
// Run manually (needs network) -- never in CI; the generated .go is committed:
//
//	go run gen_adobe_cid.go
//
// //go:build ignore keeps it out of go build / vet / lint, so goreportcard is
// unaffected.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	sourceCommit = "f5cf3bca7fdfeaceb77aa82847e974f2306c20b4"
	sourceURL    = "https://raw.githubusercontent.com/adobe-type-tools/cmap-resources/" +
		sourceCommit + "/Adobe-Japan1-7/cid2code.txt"
	sourceSHA256 = "5c4bc3adfecc348e67c36c8572d247b1ab3b42f281c6aedddf82bcf1db847a00"
	outFile      = "cidunicode_japan1.go"
)

func main() {
	data, err := fetch(sourceURL)
	if err != nil {
		fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != sourceSHA256 {
		fatal(fmt.Errorf("SHA-256 mismatch: fetched %s, pinned %s -- refusing to generate", got, sourceSHA256))
	}
	table, st := parse(data)
	if err := emit(outFile, table); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s\n", outFile)
	fmt.Printf("  maxCID=%d  tableLen=%d\n", len(table)-1, len(table))
	fmt.Printf("  mapped:  clean=%d vStripped=%d utf32Rescued=%d  (total=%d)\n",
		st.clean, st.vstrip, st.utf32, st.clean+st.vstrip+st.utf32)
	fmt.Printf("  skipped: suppPlane=%d starUnmapped=%d other=%d\n", st.supp, st.star, st.other)
}

type stats struct {
	clean  int // UniJIS-UCS2 present, plain BMP hex
	vstrip int // UniJIS-UCS2 present with trailing "v" vertical annotation -> base scalar
	utf32  int // UniJIS-UCS2 absent ("*"), rescued from a BMP UniJIS-UTF32 value
	supp   int // mapping exists but is supplementary-plane (>0xFFFF) -> can't fit uint16
	star   int // no UCS2 and no usable UTF32 -> genuinely unmapped
	other  int // unparseable token (should be ~0; logged so a parse drift can't hide)
}

const (
	kClean = iota
	kV
	kUTF32
	kSupp
	kStar
	kOther
)

// parse reads cid2code.txt and returns the CID->Unicode table plus bucket stats.
// The header is the first non-comment ("#") line; columns are tab-delimited.
func parse(data []byte) ([]uint16, stats) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	iCID, iUCS2, iUTF32 := -1, -1, -1
	haveHeader := false
	var table []uint16
	var st stats
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if !haveHeader {
			var ok bool
			if iCID, iUCS2, iUTF32, ok = findColumns(cols); !ok {
				fatal(fmt.Errorf("header columns not found (CID=%d UniJIS-UCS2=%d UniJIS-UTF32=%d)", iCID, iUCS2, iUTF32))
			}
			haveHeader = true
			continue
		}
		if len(cols) <= iUTF32 {
			continue
		}
		cid, err := strconv.Atoi(cols[iCID])
		if err != nil || cid < 0 {
			continue
		}
		r, kind := resolve(cols[iUCS2], cols[iUTF32])
		if st.tally(kind) {
			for cid >= len(table) {
				table = append(table, 0)
			}
			table[cid] = r
		}
	}
	if err := sc.Err(); err != nil {
		fatal(err)
	}
	if !haveHeader {
		fatal(fmt.Errorf("no header row found in cid2code.txt"))
	}
	return table, st
}

// findColumns locates the CID, UniJIS-UCS2 and UniJIS-UTF32 column indices in a
// tab-split header row; ok is false if any is missing.
func findColumns(cols []string) (iCID, iUCS2, iUTF32 int, ok bool) {
	iCID, iUCS2, iUTF32 = -1, -1, -1
	for i, c := range cols {
		switch c {
		case "CID":
			iCID = i
		case "UniJIS-UCS2":
			iUCS2 = i
		case "UniJIS-UTF32":
			iUTF32 = i
		}
	}
	return iCID, iUCS2, iUTF32, iCID >= 0 && iUCS2 >= 0 && iUTF32 >= 0
}

// tally records one resolved row's bucket and reports whether it produced a table
// entry (true) or was skipped (false).
func (st *stats) tally(kind int) bool {
	switch kind {
	case kSupp:
		st.supp++
	case kStar:
		st.star++
	case kOther:
		st.other++
	case kV:
		st.vstrip++
		return true
	case kUTF32:
		st.utf32++
		return true
	default:
		st.clean++
		return true
	}
	return false
}

// isCJKRadical reports whether v lies in the Kangxi Radicals (U+2F00-2FDF) or CJK
// Radicals Supplement (U+2E80-2EFF) compatibility blocks. In a multi-value row like
// "2f47,65e5" the radical (2f47 = KANGXI RADICAL SUN) is the compatibility
// presentation form and the unified ideograph (65e5 = 日) is the correct
// text-extraction character -- what independent engines (pdftotext, via Adobe's
// UniJIS CMap) emit. We skip only these two narrow blocks, NOT general NFKC
// (which would also fold meaningful CJK fullwidth/halfwidth distinctions).
func isCJKRadical(v uint64) bool {
	return (v >= 0x2E80 && v <= 0x2EFF) || (v >= 0x2F00 && v <= 0x2FDF)
}

// pickBMP scans comma-separated UCS2/UTF32 alternatives (stripping the trailing
// "v" vertical annotation per token) and returns the chosen BMP scalar: the first
// non-radical value, or the first BMP value when every alternative is a radical.
// ok is false when no BMP value exists; sawSupp reports a supplementary-plane
// value (used only to classify a row that yields no BMP value).
func pickBMP(src string) (val uint64, vertical, sawSupp, ok bool) {
	var first uint64
	var firstVert, haveFirst bool
	for t := range strings.SplitSeq(src, ",") {
		t, vert := strings.CutSuffix(t, "v") // Adobe vertical annotation -> base scalar
		v, err := strconv.ParseUint(t, 16, 32)
		if err != nil || v == 0 {
			continue
		}
		if v > 0xFFFF {
			sawSupp = true
			continue
		}
		if !haveFirst {
			first, firstVert, haveFirst = v, vert, true
		}
		if !isCJKRadical(v) {
			return v, vert, sawSupp, true
		}
	}
	return first, firstVert, sawSupp, haveFirst
}

// resolve maps one row's UniJIS-UCS2 (preferred) / UniJIS-UTF32 (BMP fallback) to a
// BMP scalar via pickBMP and classifies the result for the stats buckets. Adobe
// encodes a one-to-many glyph as a comma list and a vertical presentation form with
// a trailing "v" (handled in pickBMP).
func resolve(ucs2, utf32 string) (uint16, int) {
	src, rescued := ucs2, false
	if src == "*" {
		src, rescued = utf32, true
		if src == "*" {
			return 0, kStar
		}
	}
	v, vertical, sawSupp, ok := pickBMP(src)
	if !ok {
		if sawSupp {
			return 0, kSupp
		}
		return 0, kOther
	}
	switch {
	case rescued:
		return uint16(v), kUTF32
	case vertical:
		return uint16(v), kV
	default:
		return uint16(v), kClean
	}
}

// emit writes the generated, gofmt-clean Go source.
func emit(path string, table []uint16) error {
	var b bytes.Buffer
	b.WriteString(generatedHeader)
	b.WriteString("package pdf\n\n")
	b.WriteString("// adobeJapan1CIDToUnicode maps an Adobe-Japan1 CID (index) to its BMP Unicode\n")
	b.WriteString("// scalar, or 0 when the CID has no usable Unicode mapping. It is consumed by\n")
	b.WriteString("// adobeCIDEncoder. Multi-value rows prefer the unified ideograph over its\n")
	b.WriteString("// compatibility radical, and vertical (\"v\") forms map to the base scalar.\n")
	b.WriteString("// Code generated by gen_adobe_cid.go from cid2code.txt; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("//\tsource: " + sourceURL + "\n")
	b.WriteString("//\tcommit: " + sourceCommit + "\n")
	b.WriteString("//\tsha256: " + sourceSHA256 + "\n")
	b.WriteString("var adobeJapan1CIDToUnicode = []uint16{\n")
	const perLine = 12
	for i, v := range table {
		if i%perLine == 0 {
			b.WriteString("\t")
		}
		fmt.Fprintf(&b, "0x%04x,", v)
		if (i+1)%perLine == 0 || i == len(table)-1 {
			b.WriteString("\n")
		} else {
			b.WriteString(" ")
		}
	}
	b.WriteString("}\n")
	out, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}

// generatedHeader is the license/provenance comment block prepended to the
// generated file (Adobe cid2code.txt is BSD-3-Clause; see the repo NOTICE).
const generatedHeader = `// Code generated by gen_adobe_cid.go from Adobe-Japan1-7/cid2code.txt; DO NOT EDIT.
//
// The CID->Unicode data below is derived from Adobe's cid2code.txt, distributed
// under the BSD 3-Clause License in github.com/adobe-type-tools/cmap-resources.
// See the repo-root NOTICE file for the full attribution and license text.
//
// Copyright 1990-2023 Adobe. All rights reserved.

`

func fetch(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec // pinned, SHA-256-verified URL
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen_adobe_cid:", err)
	os.Exit(1)
}
