// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Character-class predicates and related pure functions for PDF lexing.

package pdf

// namedEscapeByte maps a single-character escape letter to its decoded byte.
// Returns the decoded byte and true if the escape is a recognised named escape.
var namedEscapeByte = map[byte]byte{
	'n': '\n',
	'r': '\r',
	'b': '\b',
	't': '\t',
	'f': '\f',
}

func unhex(b byte) int {
	switch {
	case '0' <= b && b <= '9':
		return int(b) - '0'
	case 'a' <= b && b <= 'f':
		return int(b) - 'a' + 10
	case 'A' <= b && b <= 'F':
		return int(b) - 'A' + 10
	}
	return -1
}

func isSpace(b byte) bool {
	switch b {
	case '\x00', '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

func isDelim(b byte) bool {
	switch b {
	case '<', '>', '(', ')', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func stripSign(s string) string {
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		return s[1:]
	}
	return s
}

func isInteger(s string) bool {
	s = stripSign(s)
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || '9' < c {
			return false
		}
	}
	return true
}

func isReal(s string) bool {
	s = stripSign(s)
	if len(s) == 0 {
		return false
	}
	ndot := 0
	for _, c := range s {
		if c == '.' {
			ndot++
			continue
		}
		if c < '0' || '9' < c {
			return false
		}
	}
	return ndot == 1
}
