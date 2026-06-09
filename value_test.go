package pdf

import (
	"strconv"
	"testing"
)

// utf16BEString encodes a Go string as a big-endian UTF-16 byte sequence
// (no BOM). Each rune <= 0xFFFF is encoded as two bytes.
func utf16BEString(s string) string {
	var b []byte
	for _, r := range s {
		b = append(b, byte(r>>8), byte(r))
	}
	return string(b)
}

// utf16BEWithBOM prepends the UTF-16 BE BOM (\xfe\xff) to an already-encoded
// big-endian UTF-16 byte sequence.
func utf16BEWithBOM(s string) string {
	return "\xfe\xff" + s
}

// TestValueBool checks that Bool() returns the underlying bool for a Bool-kind
// Value and returns false for non-Bool kinds.
func TestValueBool(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want bool
	}{
		{
			name: "true bool",
			v:    Value{nil, objptr{}, true},
			want: true,
		},
		{
			name: "false bool",
			v:    Value{nil, objptr{}, false},
			want: false,
		},
		{
			name: "non-bool integer returns false",
			v:    Value{nil, objptr{}, int64(1)},
			want: false,
		},
		{
			name: "non-bool string returns false",
			v:    Value{nil, objptr{}, "hello"},
			want: false,
		},
		{
			name: "null value returns false",
			v:    Value{nil, objptr{}, nil},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.Bool()
			if got != tt.want {
				t.Errorf("Bool() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValueString checks that String() returns the objfmt representation for
// each underlying kind.
func TestValueString(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want string
	}{
		{
			name: "null",
			v:    Value{nil, objptr{}, nil},
			want: "<nil>",
		},
		{
			name: "bool true",
			v:    Value{nil, objptr{}, true},
			want: "true",
		},
		{
			name: "bool false",
			v:    Value{nil, objptr{}, false},
			want: "false",
		},
		{
			name: "integer",
			v:    Value{nil, objptr{}, int64(42)},
			want: "42",
		},
		{
			name: "float64",
			v:    Value{nil, objptr{}, float64(3.14)},
			want: "3.14",
		},
		{
			name: "name",
			v:    Value{nil, objptr{}, name("Helvetica")},
			want: "/Helvetica",
		},
		{
			name: "plain ASCII string — objfmt quotes it",
			v:    Value{nil, objptr{}, "hello"},
			want: strconv.Quote("hello"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestValueTextFromUTF16 checks that TextFromUTF16 correctly decodes a
// big-endian UTF-16 string (no BOM) to UTF-8.
func TestValueTextFromUTF16(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want string
	}{
		{
			name: "ASCII text via UTF-16 BE",
			v:    Value{nil, objptr{}, utf16BEString("Hello")},
			want: "Hello",
		},
		{
			name: "non-ASCII Unicode via UTF-16 BE",
			v:    Value{nil, objptr{}, utf16BEString("café")},
			want: "café",
		},
		{
			name: "empty string returns empty",
			v:    Value{nil, objptr{}, ""},
			want: "",
		},
		{
			name: "odd-length string returns empty",
			v:    Value{nil, objptr{}, "\x00\x48\x00"},
			want: "",
		},
		{
			name: "non-String kind returns empty",
			v:    Value{nil, objptr{}, int64(99)},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.TextFromUTF16()
			if got != tt.want {
				t.Errorf("TextFromUTF16() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestValueFloat64Coerce checks that Float64() coerces an Integer-kind Value
// to float64, that a Real-kind Value is returned directly, and that other
// kinds return 0.
func TestValueFloat64Coerce(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want float64
	}{
		{
			name: "integer coerces to float64",
			v:    Value{nil, objptr{}, int64(7)},
			want: 7.0,
		},
		{
			name: "negative integer coerces to float64",
			v:    Value{nil, objptr{}, int64(-3)},
			want: -3.0,
		},
		{
			name: "zero integer coerces to float64",
			v:    Value{nil, objptr{}, int64(0)},
			want: 0.0,
		},
		{
			name: "real value returned directly",
			v:    Value{nil, objptr{}, float64(2.5)},
			want: 2.5,
		},
		{
			name: "non-numeric kind returns 0",
			v:    Value{nil, objptr{}, "text"},
			want: 0.0,
		},
		{
			name: "bool kind returns 0",
			v:    Value{nil, objptr{}, true},
			want: 0.0,
		},
		{
			name: "null kind returns 0",
			v:    Value{nil, objptr{}, nil},
			want: 0.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.Float64()
			if got != tt.want {
				t.Errorf("Float64() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValueKeys pins the nil-vs-empty contract documented in Value.Keys():
// a non-dict/non-stream Value returns nil; an empty dict returns a non-nil
// empty slice; a non-empty dict returns keys in sorted order.
func TestValueKeys(t *testing.T) {
	// Case 1: non-dict Value (Integer kind) → Keys() returns nil.
	nonDict := makeIntValue(42)
	if got := nonDict.Keys(); got != nil {
		t.Errorf("non-dict Keys() = %v, want nil", got)
	}

	// Case 2: empty dict Value → Keys() returns non-nil empty slice.
	emptyDict := filterMakeDict(map[string]any{})
	if emptyDict.Kind() != Dict {
		t.Fatalf("filterMakeDict({}) has Kind %v, want Dict", emptyDict.Kind())
	}
	gotEmpty := emptyDict.Keys()
	if gotEmpty == nil {
		t.Error("empty dict Keys() = nil, want non-nil empty slice")
	}
	if len(gotEmpty) != 0 {
		t.Errorf("empty dict Keys() = %v, want len 0", gotEmpty)
	}

	// Case 3: dict with two keys → Keys() returns them sorted.
	twoKeys := filterMakeDict(map[string]any{"B": int64(2), "A": int64(1)})
	gotTwo := twoKeys.Keys()
	want := []string{"A", "B"}
	if len(gotTwo) != len(want) {
		t.Fatalf("two-key dict Keys() = %v, want %v", gotTwo, want)
	}
	for i, k := range want {
		if gotTwo[i] != k {
			t.Errorf("Keys()[%d] = %q, want %q", i, gotTwo[i], k)
		}
	}
}

// TestValueInt64 pins the contract documented in Value.Int64():
// Integer-kind Values return their exact int64; any non-Integer kind returns 0.
func TestValueInt64(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want int64
	}{
		{
			name: "positive integer",
			v:    makeIntValue(99),
			want: 99,
		},
		{
			name: "negative integer",
			v:    makeIntValue(-7),
			want: -7,
		},
		{
			name: "zero integer",
			v:    makeIntValue(0),
			want: 0,
		},
		{
			name: "real (float64) kind returns 0",
			v:    Value{nil, objptr{}, float64(3.14)},
			want: 0,
		},
		{
			name: "string kind returns 0",
			v:    Value{nil, objptr{}, "hello"},
			want: 0,
		},
		{
			name: "null kind returns 0",
			v:    Value{nil, objptr{}, nil},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.Int64()
			if got != tt.want {
				t.Errorf("Int64() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestValueText checks that Text() correctly handles PDFDocEncoding strings
// and UTF-16 BOM-prefixed strings, and returns the raw string when neither
// encoding applies.
func TestValueText(t *testing.T) {
	// Build a UTF-16 BE with BOM string for "Hello".
	utf16Hello := utf16BEWithBOM(utf16BEString("Hello"))

	// Build a valid PDFDocEncoding string: pure ASCII in printable range,
	// all bytes map to themselves in pdfDocEncoding.
	pdfDocHello := "Hello"

	// A string with a byte that is invalid in PDFDocEncoding (0x00 maps to
	// noRune) and lacks a UTF-16 BOM — Text() returns it as-is.
	rawInvalid := "\x00raw"

	tests := []struct {
		name string
		v    Value
		want string
	}{
		{
			name: "PDFDocEncoding ASCII passthrough",
			v:    Value{nil, objptr{}, pdfDocHello},
			want: "Hello",
		},
		{
			name: "UTF-16 BE with BOM decoded to UTF-8",
			v:    Value{nil, objptr{}, utf16Hello},
			want: "Hello",
		},
		{
			name: "raw string without recognised encoding returned as-is",
			v:    Value{nil, objptr{}, rawInvalid},
			want: rawInvalid,
		},
		{
			name: "non-String kind returns empty",
			v:    Value{nil, objptr{}, int64(1)},
			want: "",
		},
		{
			name: "null kind returns empty",
			v:    Value{nil, objptr{}, nil},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.Text()
			if got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
		})
	}
}
