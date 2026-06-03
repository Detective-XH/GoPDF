// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Pdfpasswd searches for the password for an encrypted PDF
// by trying all strings over a given alphabet up to a given length.
package main // import "rsc.io/pdf/pdfpasswd"

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/Detective-XH/gopdf"
)

var (
	alphabet  = flag.String("a", "0123456789", "alphabet")
	maxLength = flag.Int("m", 4, "max length")
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: pdfpasswd [-a alphabet] [-m maxlength] file\n")
	os.Exit(2)
}

type passwordIter struct {
	alpha string
	ctr   []int
	last  string
}

func newPasswordIter(alpha string, maxLen int) *passwordIter {
	return &passwordIter{alpha: alpha, ctr: make([]int, maxLen)}
}

func (it *passwordIter) next() string {
	inc(it.ctr, len(it.alpha)+1)
	for !valid(it.ctr) {
		inc(it.ctr, len(it.alpha)+1)
	}
	if done(it.ctr) {
		return ""
	}
	buf := make([]byte, len(it.ctr))
	var i int
	for i = 0; i < len(buf); i++ {
		if it.ctr[i] == 0 {
			break
		}
		buf[i] = it.alpha[it.ctr[i]-1]
	}
	it.last = string(buf[:i])
	println(it.last)
	return it.last
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("pdfpasswd: ")

	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 1 {
		usage()
	}

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	it := newPasswordIter(*alphabet, *maxLength)
	st, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}
	_, err = pdf.NewReaderEncrypted(f, st.Size(), it.next)
	if err != nil {
		if err == pdf.ErrInvalidPassword {
			log.Fatal("password not found")
		}
		log.Fatalf("reading pdf: %v", err)
	}
	fmt.Printf("password: %q\n", it.last)
}

func inc(ctr []int, n int) {
	for i := 0; i < len(ctr); i++ {
		ctr[i]++
		if ctr[i] < n {
			break
		}
		ctr[i] = 0
	}
}

func done(ctr []int) bool {
	for _, x := range ctr {
		if x != 0 {
			return false
		}
	}
	return true
}

func valid(ctr []int) bool {
	i := len(ctr)
	for i > 0 && ctr[i-1] == 0 {
		i--
	}
	for i--; i >= 0; i-- {
		if ctr[i] == 0 {
			return false
		}
	}
	return true
}
