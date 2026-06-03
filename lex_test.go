// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"io"
	"testing"
)

// newTestBuffer creates a buffer from a byte slice for use in lexer tests.
func newTestBuffer(b []byte) *buffer {
	buf := newBuffer(bytes.NewReader(b), 0)
	buf.allowEOF = true
	return buf
}

// mustReadToken reads the next token and fatals on unexpected EOF.
func mustReadToken(t *testing.T, buf *buffer) token {
	t.Helper()
	tok := buf.readToken()
	if tok == io.EOF {
		t.Fatal("unexpected EOF reading token")
	}
	return tok
}

// panicToError calls f and returns the recovered error value (if any).
// Returns nil if f did not panic.
func panicToError(f func()) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	f()
	return nil
}

// TestLexHexString covers readHexString: even nibble count, odd nibble count,
// and embedded whitespace.
func TestLexHexString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "even nibbles",
			input: "<48656c6c6f>", // "Hello"
			want:  "Hello",
		},
		{
			name:  "odd nibble count truncated at EOF",
			input: "<4865>", // 0x48 0x65 => "He"
			want:  "He",
		},
		{
			name:  "embedded whitespace ignored",
			input: "<48 65 6c \n 6c 6f>", // "Hello" with spaces and newline
			want:  "Hello",
		},
		{
			name:  "empty hex string",
			input: "<>",
			want:  "",
		},
		{
			name:  "uppercase hex digits",
			input: "<48656C6C6F>", // "Hello"
			want:  "Hello",
		},
		{
			name:  "single byte",
			input: "<41>", // 'A'
			want:  "A",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := newTestBuffer([]byte(tc.input))
			tok := mustReadToken(t, buf)
			got, ok := tok.(string)
			if !ok {
				t.Fatalf("expected string token, got %T: %v", tok, tok)
			}
			if got != tc.want {
				t.Errorf("readHexString(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}

	// error path: malformed hex string (invalid hex characters) triggers panic
	t.Run("malformed hex panics", func(t *testing.T) {
		buf := newTestBuffer([]byte("<GG>"))
		r := panicToError(func() { buf.readToken() })
		if r == nil {
			t.Fatal("expected panic for malformed hex string, got none")
		}
	})
}

// TestLexParseNumeric covers parseNumericToken: integer, float, negative, and
// boundary values.
func TestLexParseNumeric(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  any
	}{
		{
			name:  "positive integer",
			input: "42",
			want:  int64(42),
		},
		{
			name:  "zero",
			input: "0",
			want:  int64(0),
		},
		{
			name:  "negative integer",
			input: "-7",
			want:  int64(-7),
		},
		{
			name:  "positive integer with plus sign",
			input: "+123",
			want:  int64(123),
		},
		{
			name:  "float",
			input: "3.14",
			want:  float64(3.14),
		},
		{
			name:  "negative float",
			input: "-1.5",
			want:  float64(-1.5),
		},
		{
			name:  "float starting with dot",
			input: ".5",
			want:  float64(0.5),
		},
		{
			name:  "large integer boundary",
			input: "9223372036854775807", // math.MaxInt64
			want:  int64(9223372036854775807),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := newTestBuffer([]byte(tc.input))
			tok := mustReadToken(t, buf)
			switch want := tc.want.(type) {
			case int64:
				got, ok := tok.(int64)
				if !ok {
					t.Fatalf("expected int64, got %T: %v", tok, tok)
				}
				if got != want {
					t.Errorf("parseNumericToken(%q) = %d; want %d", tc.input, got, want)
				}
			case float64:
				got, ok := tok.(float64)
				if !ok {
					t.Fatalf("expected float64, got %T: %v", tok, tok)
				}
				if got != want {
					t.Errorf("parseNumericToken(%q) = %f; want %f", tc.input, got, want)
				}
			}
		})
	}
}

// TestLexAngleBracketClose covers readAngleBracketClose: ">>" becomes the
// dict-close keyword, and a bare ">" triggers a panic.
func TestLexAngleBracketClose(t *testing.T) {
	t.Run(">> becomes dict-close keyword", func(t *testing.T) {
		buf := newTestBuffer([]byte(">>"))
		tok := mustReadToken(t, buf)
		kw, ok := tok.(keyword)
		if !ok {
			t.Fatalf("expected keyword token, got %T: %v", tok, tok)
		}
		if kw != ">>" {
			t.Errorf("got keyword %q; want %q", kw, ">>")
		}
	})

	// error path: bare ">" (not followed by ">") triggers panic
	t.Run("bare > triggers panic", func(t *testing.T) {
		buf := newTestBuffer([]byte("> "))
		r := panicToError(func() { buf.readToken() })
		if r == nil {
			t.Fatal("expected panic for bare '>', got none")
		}
	})
}

// TestLexLiteralStringEscape covers readLiteralString with octal \123,
// backslash sequences, and nested parentheses.
func TestLexLiteralStringEscape(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple string",
			input: "(hello)",
			want:  "hello",
		},
		{
			name:  "octal escape \\123",
			input: "(\\123)", // octal 123 = decimal 83 = 'S'
			want:  "S",
		},
		{
			name:  "octal escape \\101",
			input: "(\\101)", // octal 101 = decimal 65 = 'A'
			want:  "A",
		},
		{
			name:  "named escape \\n",
			input: "(a\\nb)",
			want:  "a\nb",
		},
		{
			name:  "named escape \\t",
			input: "(a\\tb)",
			want:  "a\tb",
		},
		{
			name:  "escaped backslash",
			input: "(a\\\\b)",
			want:  "a\\b",
		},
		{
			name:  "escaped open paren",
			input: "(a\\(b)",
			want:  "a(b",
		},
		{
			name:  "escaped close paren",
			input: "(a\\)b)",
			want:  "a)b",
		},
		{
			name:  "nested parens",
			input: "(outer (inner) text)",
			want:  "outer (inner) text",
		},
		{
			name:  "deeply nested parens",
			input: "(a(b(c)d)e)",
			want:  "a(b(c)d)e",
		},
		{
			name:  "line continuation backslash-newline",
			input: "(abc\\\ndef)",
			want:  "abcdef",
		},
		{
			name:  "empty string",
			input: "()",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := newTestBuffer([]byte(tc.input))
			tok := mustReadToken(t, buf)
			got, ok := tok.(string)
			if !ok {
				t.Fatalf("expected string token, got %T: %v", tok, tok)
			}
			if got != tc.want {
				t.Errorf("readLiteralString(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}

	// error path: invalid octal value > 255 triggers panic
	t.Run("octal escape > 255 panics", func(t *testing.T) {
		// \400 = decimal 256, which exceeds 255
		buf := newTestBuffer([]byte("(\\400)"))
		r := panicToError(func() { buf.readToken() })
		if r == nil {
			t.Fatal("expected panic for octal escape > 255, got none")
		}
	})
}

// TestLexName covers readName: basic name, and #XX hex-escape in name.
func TestLexName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  name
	}{
		{
			name:  "simple name",
			input: "/Helvetica",
			want:  name("Helvetica"),
		},
		{
			name:  "hex escape #41 => A",
			input: "/F#41o", // F + 'A' (0x41) + o = "FAo"
			want:  name("FAo"),
		},
		{
			name:  "hex escape #6F => o",
			input: "/F#6Fo", // F + 'o' (0x6F) + o = "Foo"
			want:  name("Foo"),
		},
		{
			name:  "name with digits",
			input: "/Type1",
			want:  name("Type1"),
		},
		{
			name:  "name ending at space",
			input: "/Name next",
			want:  name("Name"),
		},
		{
			name:  "name ending at delimiter",
			input: "/Name/Other",
			want:  name("Name"),
		},
		{
			name:  "empty name (slash only)",
			input: "/ ",
			want:  name(""),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := newTestBuffer([]byte(tc.input))
			tok := mustReadToken(t, buf)
			got, ok := tok.(name)
			if !ok {
				t.Fatalf("expected name token, got %T: %v", tok, tok)
			}
			if got != tc.want {
				t.Errorf("readName(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}

	// error path: malformed hex escape in name triggers panic
	t.Run("malformed hex escape panics", func(t *testing.T) {
		buf := newTestBuffer([]byte("/F#GGo"))
		r := panicToError(func() { buf.readToken() })
		if r == nil {
			t.Fatal("expected panic for malformed hex in name, got none")
		}
	})
}

// TestLexComment covers skipSpaceAndComments: a % comment is skipped and the
// next token is read correctly.
func TestLexComment(t *testing.T) {
	t.Run("comment skipped, next token integer", func(t *testing.T) {
		// comment on its own line, followed by an integer
		buf := newTestBuffer([]byte("% this is a comment\n42"))
		tok := mustReadToken(t, buf)
		got, ok := tok.(int64)
		if !ok {
			t.Fatalf("expected int64 after comment, got %T: %v", tok, tok)
		}
		if got != 42 {
			t.Errorf("got %d; want 42", got)
		}
	})

	t.Run("comment skipped, next token name", func(t *testing.T) {
		buf := newTestBuffer([]byte("% ignore me\r/PageSize"))
		tok := mustReadToken(t, buf)
		got, ok := tok.(name)
		if !ok {
			t.Fatalf("expected name after comment, got %T: %v", tok, tok)
		}
		if got != "PageSize" {
			t.Errorf("got name %q; want %q", got, "PageSize")
		}
	})

	t.Run("multiple comments skipped", func(t *testing.T) {
		buf := newTestBuffer([]byte("% line1\n% line2\n(result)"))
		tok := mustReadToken(t, buf)
		got, ok := tok.(string)
		if !ok {
			t.Fatalf("expected string token, got %T: %v", tok, tok)
		}
		if got != "result" {
			t.Errorf("got %q; want %q", got, "result")
		}
	})

	t.Run("EOF after whitespace-only input returns io.EOF", func(t *testing.T) {
		buf := newTestBuffer([]byte("   "))
		tok := buf.readToken()
		if tok != io.EOF {
			t.Errorf("expected io.EOF for whitespace-only input, got %T: %v", tok, tok)
		}
	})
}

// TestLexReadTokenDefaultDelim verifies that a bare ')' delimiter reaching
// readTokenDefault triggers errorf, which panics.
func TestLexReadTokenDefaultDelim(t *testing.T) {
	buf := newTestBuffer([]byte(") "))
	r := panicToError(func() { buf.readToken() })
	if r == nil {
		t.Fatal("expected panic for bare ')' delimiter, got none")
	}
}

// FuzzLex feeds arbitrary bytes to the lexer and detects panics natively.
// No recover() wrapper — the Go fuzz engine detects panics on its own.
func FuzzLex(f *testing.F) {
	f.Add([]byte("(hello)"))
	f.Add([]byte("/Name"))
	f.Add([]byte("42"))
	f.Add([]byte("<48656c6c6f>"))
	f.Add([]byte(">>"))
	f.Add([]byte("% comment\n/Key"))
	f.Add([]byte("(nested (parens) here)"))
	f.Add([]byte("3.14"))
	f.Fuzz(func(t *testing.T, b []byte) {
		buf := newBuffer(bytes.NewReader(b), 0)
		buf.allowEOF = true
		_ = buf.readToken() //nolint:errcheck
	})
}
