package pdf

import (
	"strings"
	"testing"
)

// TestERPPeriodPoisonRecovered: ERP page 1 font /T1_0 poisons period 0x2E to U+FFFD in
// ToUnicode; its /Encoding (WinAnsi) maps 0x2E -> U+002E. Validated against pdftotext /
// published ERP B-1 (the 1973 row reads "4.0"). Recon: all 3194 page-1 U+FFFD are this one
// poisoned code (the page has no Form XObjects), so after the fix page 1 carries zero U+FFFD.
func TestERPPeriodPoisonRecovered(t *testing.T) {
	fh, r, err := Open("testdata/corpus/tables/erp-2024-tb1-gdp-pctchg.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()
	got, err := r.Page(1).GetPlainText(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "4.0") {
		t.Errorf("ERP period not recovered: %q not found", "4.0")
	}
	if n := strings.Count(got, "�"); n != 0 {
		t.Errorf("ERP page 1 still has %d U+FFFD after poison fallback (want 0)", n)
	}
}
