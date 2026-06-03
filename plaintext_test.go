package pdf

import (
	"testing"
)

// strVal builds a string-kind Value without a backing Reader (safe for unit tests
// that only call RawString/Kind and never Index into an array).
func strVal(s string) Value {
	return Value{nil, objptr{}, s}
}

// fltVal builds a float64-kind Value for numeric operands (e.g. Aw/Ac in ").
func fltVal(f float64) Value {
	return Value{nil, objptr{}, f}
}

// newPlainState returns a zero-depth state with nopEncoder (ASCII pass-through).
func newPlainState() *plainTextState {
	return &plainTextState{enc: &nopEncoder{}}
}

// --- handlePlainShow -------------------------------------------------------

// TestHandlePlainShowBT verifies BT is a no-op.
func TestHandlePlainShowBT(t *testing.T) {
	s := newPlainState()
	s.handlePlainShow("BT", nil)
	if got := s.buf.String(); got != "" {
		t.Errorf("BT: want empty string, got %q", got)
	}
}

// TestHandlePlainShowTStar verifies T* emits a newline.
func TestHandlePlainShowTStar(t *testing.T) {
	s := newPlainState()
	s.handlePlainShow("T*", nil)
	if got := s.buf.String(); got != "\n" {
		t.Errorf("T*: want newline, got %q", got)
	}
}

// TestHandlePlainShowTj verifies Tj emits the operand string.
func TestHandlePlainShowTj(t *testing.T) {
	s := newPlainState()
	s.handlePlainShow("Tj", []Value{strVal("hello")})
	if got := s.buf.String(); got != "hello" {
		t.Errorf("Tj: want \"hello\", got %q", got)
	}
}

// TestHandlePlainShowQuoteSingle verifies ' emits the operand string.
func TestHandlePlainShowQuoteSingle(t *testing.T) {
	s := newPlainState()
	s.handlePlainShow("'", []Value{strVal("world")})
	if got := s.buf.String(); got != "world" {
		t.Errorf("': want \"world\", got %q", got)
	}
}

// TestHandlePlainShowQuoteDouble verifies " emits args[2] (the string operand),
// not args[0] (Aw) — this was the panic/wrong-operand bug before the fix.
func TestHandlePlainShowQuoteDouble(t *testing.T) {
	s := newPlainState()
	s.handlePlainShow("\"", []Value{fltVal(0.1), fltVal(0.2), strVal("text")})
	if got := s.buf.String(); got != "text" {
		t.Errorf("\": want \"text\", got %q", got)
	}
}

// TestHandlePlainShowTJEmpty verifies TJ with an empty array is a no-op.
func TestHandlePlainShowTJEmpty(t *testing.T) {
	s := newPlainState()
	s.handlePlainShow("TJ", []Value{{nil, objptr{}, array{}}})
	if got := s.buf.String(); got != "" {
		t.Errorf("TJ(empty): want empty, got %q", got)
	}
}

// TestHandlePlainShowTJ verifies TJ with a mixed string/number array concatenates
// only the String elements and skips numeric kerning adjustments.
// A minimal *Reader{} (non-nil, empty xref) is safe here: Index calls
// r.resolve(ptr, x[i]); for string elements resolve skips the objptr branch
// and returns Value{r, parent, x} without any file or xref access.
func TestHandlePlainShowTJ(t *testing.T) {
	r := &Reader{} // non-nil so Index/resolve won't nil-panic
	// Mixed array: "AB", numeric kerning offset, "CD" — showArray must skip the int.
	arrVal := Value{r, objptr{}, array{"AB", int64(-100), "CD"}}
	s := newPlainState()
	s.handlePlainShow("TJ", []Value{arrVal})
	if got := s.buf.String(); got != "ABCD" {
		t.Errorf("TJ(mixed): want \"ABCD\", got %q", got)
	}
}

// --- handlePlainTf ---------------------------------------------------------

// TestPlainHandleTfNilEncoder verifies that when the font name is not found in
// the fonts map, handlePlainTf falls through to the else branch and sets enc to
// a nopEncoder.
func TestPlainHandleTfNilEncoder(t *testing.T) {
	s := &plainTextState{
		enc:   &nopEncoder{},
		fonts: make(map[string]*Font), // empty map — "F1" will not be found
	}
	args := []Value{{nil, objptr{}, name("F1")}, fltVal(12.0)}
	s.handlePlainTf(args)
	if _, ok := s.enc.(*nopEncoder); !ok {
		t.Errorf("handlePlainTf with missing font: want nopEncoder, got %T", s.enc)
	}
}

// TestPlainHandleTfBadArgCount verifies that handlePlainTf panics when called
// with a wrong number of arguments (fewer than 2).
func TestPlainHandleTfBadArgCount(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("handlePlainTf with 1 arg: expected panic, got none")
		}
	}()
	s := &plainTextState{enc: &nopEncoder{}, fonts: make(map[string]*Font)}
	s.handlePlainTf([]Value{fltVal(12.0)}) // only 1 arg → panic
}

// --- panic tests -----------------------------------------------------------

// TestHandlePlainShowBadArgCounts verifies that wrong operand counts panic with
// the expected messages for Tj, ', and ".
func TestHandlePlainShowBadArgCounts(t *testing.T) {
	cases := []struct {
		op   string
		args []Value
		name string
	}{
		{"Tj", nil, "Tj-noArgs"},
		{"Tj", []Value{strVal("a"), strVal("b")}, "Tj-twoArgs"},
		{"'", nil, "quote-single-noArgs"},
		{"'", []Value{strVal("a"), strVal("b")}, "quote-single-twoArgs"},
		{"\"", nil, "quote-double-noArgs"},
		{"\"", []Value{strVal("a"), strVal("b")}, "quote-double-twoArgs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected panic for wrong arg count, got none", tc.name)
				}
			}()
			newPlainState().handlePlainShow(tc.op, tc.args)
		})
	}
}
