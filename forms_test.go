// forms_test.go — tests for Reader.Fields() AcroForms extraction. All
// fixtures are synthetic (buildPDFFromObjects): gs cannot fill forms and qpdf
// has no form authoring; hand-crafted object bodies are the established
// pattern (see corpus_gen_test.go).
package pdf

import "testing"

// TestFieldsText: merged field+widget dict (§12.7.3.3) — one leaf carrying
// /FT, /T, /V, /Rect, and /P simultaneously.
func TestFieldsText(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [4 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Tx /T (name1) /V (Hello Form) /Rect [50 100 200 120] /P 3 0 R >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1", len(fields))
	}
	f := fields[0]
	if f.Name != "name1" {
		t.Errorf("Name = %q, want name1", f.Name)
	}
	if f.Type != FieldText {
		t.Errorf("Type = %d, want FieldText", f.Type)
	}
	if f.Value != "Hello Form" {
		t.Errorf("Value = %q, want Hello Form", f.Value)
	}
	if f.ReadOnly {
		t.Errorf("ReadOnly = true, want false")
	}
	if f.PageNum != 1 {
		t.Errorf("PageNum = %d, want 1", f.PageNum)
	}
	if f.Rect.Min.X != 50 || f.Rect.Min.Y != 100 || f.Rect.Max.X != 200 || f.Rect.Max.Y != 120 {
		t.Errorf("Rect = %+v, want {Min:{50 100} Max:{200 120}}", f.Rect)
	}
}

// TestFieldsCheckBox: checked and unchecked boxes; the unchecked one has NO
// /V (spec default "Off") and NO /P (page resolved via the /Annots map).
func TestFieldsCheckBox(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 5 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [4 0 R 5 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Btn /T (agree) /V /Yes /Rect [10 10 20 20] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Btn /T (subscribe) /Rect [10 30 20 40] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("len(Fields) = %d, want 2", len(fields))
	}
	if fields[0].Type != FieldCheckBox || fields[0].Value != "Yes" {
		t.Errorf("fields[0] = %+v, want FieldCheckBox value Yes", fields[0])
	}
	if fields[1].Type != FieldCheckBox || fields[1].Value != "Off" {
		t.Errorf("fields[1] = %+v, want FieldCheckBox value Off (absent /V)", fields[1])
	}
	if fields[0].PageNum != 1 || fields[1].PageNum != 1 {
		t.Errorf("PageNums = %d, %d; want 1, 1 (resolved via /Annots map, no /P)",
			fields[0].PageNum, fields[1].PageNum)
	}
}

// TestFieldsRadio: a radio GROUP — parent carries /FT /Ff /V /T; kids are
// /T-less widget annotations. Exactly one field must be emitted, with
// Rect/PageNum from the first widget.
func TestFieldsRadio(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [5 0 R 6 0 R] >>",
		"<< /FT /Btn /T (color) /Ff 32768 /V /Blue /Kids [5 0 R 6 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /Parent 4 0 R /Rect [10 10 20 20] >>",
		"<< /Type /Annot /Subtype /Widget /Parent 4 0 R /Rect [10 30 20 40] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1 (radio group is ONE field)", len(fields))
	}
	f := fields[0]
	if f.Name != "color" || f.Type != FieldRadio || f.Value != "Blue" {
		t.Errorf("field = %+v, want Name=color Type=FieldRadio Value=Blue", f)
	}
	if f.PageNum != 1 {
		t.Errorf("PageNum = %d, want 1", f.PageNum)
	}
	if f.Rect.Min.X != 10 || f.Rect.Min.Y != 10 {
		t.Errorf("Rect = %+v, want first widget's rect {10 10 20 20}", f.Rect)
	}
}

// TestFieldsHierarchical: nested group with INHERITED /FT, /Ff, /V — the
// child leaf carries only /T, /Rect, /Parent. Locks both the qualified-name
// build and §12.7.3.1 inheritance.
func TestFieldsHierarchical(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [5 0 R] >>",
		"<< /T (person) /FT /Tx /Ff 1 /V (inherited) /Kids [5 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /T (first) /Parent 4 0 R /Rect [1 2 3 4] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1", len(fields))
	}
	f := fields[0]
	if f.Name != "person.first" {
		t.Errorf("Name = %q, want person.first", f.Name)
	}
	if f.Type != FieldText {
		t.Errorf("Type = %d, want FieldText (inherited /FT)", f.Type)
	}
	if f.Value != "inherited" {
		t.Errorf("Value = %q, want inherited (inherited /V)", f.Value)
	}
	if !f.ReadOnly {
		t.Errorf("ReadOnly = false, want true (inherited /Ff bit 1)")
	}
	if f.PageNum != 1 {
		t.Errorf("PageNum = %d, want 1", f.PageNum)
	}
}

// TestFieldsChoice: combo (/Ch + Combo flag) and multi-select list (array /V).
func TestFieldsChoice(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 5 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [4 0 R 5 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Ch /T (state) /Ff 131072 /V (Blue) /Rect [0 0 1 1] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Ch /T (multi) /V [(A) (B)] /Rect [0 2 1 3] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("len(Fields) = %d, want 2", len(fields))
	}
	if fields[0].Type != FieldCombo || fields[0].Value != "Blue" {
		t.Errorf("fields[0] = %+v, want FieldCombo value Blue", fields[0])
	}
	if fields[1].Type != FieldList || fields[1].Value != "A, B" {
		t.Errorf("fields[1] = %+v, want FieldList value %q", fields[1], "A, B")
	}
}

// TestFieldsPushButton: pushbutton maps to FieldOther with empty Value.
func TestFieldsPushButton(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [4 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Btn /T (submit) /Ff 65536 /Rect [0 0 1 1] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1", len(fields))
	}
	if fields[0].Type != FieldOther || fields[0].Value != "" {
		t.Errorf("field = %+v, want FieldOther with empty Value", fields[0])
	}
}

// TestFieldsEmpty: a document without /AcroForm returns (nil, nil).
func TestFieldsEmpty(t *testing.T) {
	r, err := OpenBytes(buildURIAnnotationPDF("https://example.com"))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if fields != nil {
		t.Errorf("Fields() = %v, want nil for a document without AcroForm", fields)
	}
}

// TestFieldsSecondPageAttribution locks 1-based page attribution through the
// /Annots map across pages: Pages() yields 1-based indices, so a zero-based
// map would report 0 for page 1 and 1 for page 2 (adversarial-review round-1
// probe — neither widget carries /P, forcing the /Annots-map path).
func TestFieldsSecondPageAttribution(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [5 0 R 6 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /Annots [5 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /Annots [6 0 R] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Tx /T (p1) /V (a) /Rect [0 0 1 1] >>",
		"<< /Type /Annot /Subtype /Widget /FT /Tx /T (p2) /V (b) /Rect [0 2 1 3] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("len(Fields) = %d, want 2", len(fields))
	}
	if fields[0].PageNum != 1 {
		t.Errorf("fields[0].PageNum = %d, want 1", fields[0].PageNum)
	}
	if fields[1].PageNum != 2 {
		t.Errorf("fields[1].PageNum = %d, want 2", fields[1].PageNum)
	}
}

// TestFieldsCycle: a /Kids edge cycling back to an ancestor must terminate
// without emitting duplicates or hanging.
func TestFieldsCycle(t *testing.T) {
	data := buildPDFFromObjects([]string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] >> >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R >>",
		"<< /T (loop) /FT /Tx /Kids [5 0 R] >>",
		"<< /T (child) /Parent 4 0 R /Kids [4 0 R] >>",
	})
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	fields, err := r.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	// Node 5's kid (node 4) carries /T, so node 5 is internal; recursion into
	// node 4 hits the visited set and stops. No terminal field is reachable.
	if len(fields) != 0 {
		t.Errorf("Fields() = %v, want none (cycle must terminate cleanly)", fields)
	}
}
