package pdf

import (
	"bytes"
	"os"
	"testing"
)

// BenchmarkTables measures the end-to-end cost of Page.Tables() on a representative
// fully-ruled corpus table. It is the A/B baseline for the per-table confidence/warnings
// work: the detector (detectTableWarnings), region bbox (cellsUnionRect), and roll-up run
// once per reconstructed table inside this path, so a regression here would surface their
// added cost against the reconstruction it shares with master.
//
//	go test -bench BenchmarkTables -benchmem -run '^$' -count 10 .
func BenchmarkTables(b *testing.B) {
	data, err := os.ReadFile(corpusPath("tables/epa-egrid2022-t1.pdf"))
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	r, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		b.Fatalf("NewReader: %v", err)
	}
	pg := r.Page(1)

	b.ReportAllocs()
	for b.Loop() {
		tables, err := pg.Tables()
		if err != nil {
			b.Fatalf("Tables: %v", err)
		}
		if len(tables) == 0 {
			b.Fatal("no tables — fixture should yield at least one")
		}
	}
}
