package pdf

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// formFixtures are the real, public-domain AcroForm corpus fixtures. Each is a
// BLANK government form (no entered data — realFilled is ~0) committed to lock
// Reader.Fields() against REAL field-tree structure, qualified names, types,
// page attribution, and generator quirks that the synthetic forms_test.go
// fixtures cannot capture. Provenance/license live in corpusManifest and
// testdata/corpus/README.md; this list adds the field-golden path + a top-level
// field-count sentinel.
var formFixtures = []struct {
	Path   string // relative to corpusRoot
	Golden string // relative .fields-golden.txt path
	Want   int    // expected terminal-field count (cheap structural sentinel)
}{
	{"forms/irs-f1040-2025.pdf", "forms/irs-f1040-2025.fields-golden.txt", 199},
	{"forms/uscourts-cv071-civil-cover.pdf", "forms/uscourts-cv071-civil-cover.fields-golden.txt", 165},
}

// fieldTypeName maps a FieldType to a stable golden token.
func fieldTypeName(t FieldType) string {
	switch t {
	case FieldText:
		return "Text"
	case FieldCheckBox:
		return "CheckBox"
	case FieldRadio:
		return "Radio"
	case FieldCombo:
		return "Combo"
	case FieldList:
		return "List"
	case FieldOther:
		return "Other"
	default:
		return fmt.Sprintf("FieldType(%d)", int(t))
	}
}

// serializeFields renders Reader.Fields() output as a deterministic, byte-exact
// golden: one tab-separated line per terminal field, in extraction order
// (/Fields array order, depth-first). %q quotes Name/Value so empty strings,
// spaces, and the unusual /T label-derived names some generators emit stay
// visible and unambiguous; %g formats Rect coordinates without platform drift.
// Safe to (re)generate from these fixtures: they are blank, so values are empty
// or the "Off" checkbox default and names are template/label identifiers — no
// PII. A FILLED forms fixture must NOT reuse this -update path (it would bake
// entered data into a committed golden).
func serializeFields(fields []FormField) string {
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "p%d\t%s\t%q\t%q\tro=%t\t[%g %g %g %g]\n",
			f.PageNum, fieldTypeName(f.Type), f.Name, f.Value, f.ReadOnly,
			f.Rect.Min.X, f.Rect.Min.Y, f.Rect.Max.X, f.Rect.Max.Y)
	}
	return b.String()
}

// TestCorpusFormFixtures locks Reader.Fields() against real government AcroForms.
// The text layer of each fixture is sentineled by TestCorpusGolden (corpus_test.go)
// via its manifest entry; this test asserts the AcroForm field inventory itself.
// Run `go test -run TestCorpusFormFixtures -update` to regenerate the goldens.
func TestCorpusFormFixtures(t *testing.T) {
	for _, ff := range formFixtures {
		t.Run(ff.Path, func(t *testing.T) {
			data, err := os.ReadFile(corpusPath(ff.Path))
			if err != nil {
				t.Fatalf("read fixture %s: %v", ff.Path, err)
			}
			r, err := OpenBytes(data)
			if err != nil {
				t.Fatalf("open fixture %s: %v", ff.Path, err)
			}
			fields, err := r.Fields()
			if err != nil {
				t.Fatalf("Fields(): %v", err)
			}
			if len(fields) != ff.Want {
				t.Errorf("len(Fields()) = %d, want %d", len(fields), ff.Want)
			}
			got := serializeFields(fields)
			goldenFile := corpusPath(ff.Golden)
			if *updateGolden {
				if err := os.WriteFile(goldenFile, []byte(got), 0o600); err != nil {
					t.Fatalf("update golden %s: %v", ff.Golden, err)
				}
				return
			}
			//nolint:gosec // G304: goldenFile is a fixed corpus path from formFixtures, not user input
			want, err := os.ReadFile(goldenFile)
			if err != nil {
				t.Fatalf("read golden %s: %v", ff.Golden, err)
			}
			if got != string(want) {
				t.Errorf("%s: fields-golden mismatch (got %d lines, want %d); run -update to regenerate and inspect the diff",
					ff.Path, strings.Count(got, "\n"), strings.Count(string(want), "\n"))
			}
		})
	}
}
