package pdf

import (
	"testing"
	"time"
)

// FuzzOpenAndExtract is the broad net for the malformed-PDF DoS class.
//
// This target fuzzes the open path only.  OpenBytes runs behind a recover
// boundary, so a malformed input surfaces as an error rather than a panic; the
// watchdog turns any future open-time hang regression (a re-introduced /Prev or
// other unbounded loop) into a test failure instead of a stalled run.  It stays
// green under a long `go test -fuzz=FuzzOpenAndExtract` run, demonstrating the
// open-time hardening holds.
//
// A later change will extend this target to the extraction pipeline
// (NumPage -> Page(1).Words()/GetPlainText) once those getters gain their own
// recover boundaries; today they can reach the still-unguarded resolve() panics
// and post-open link-chain cycles, which are out of scope here.
func FuzzOpenAndExtract(f *testing.F) {
	f.Add(buildTextPDF("BT /F1 12 Tf (Hello) Tj ET"))
	f.Add(buildCyclicXrefTablePDF())

	f.Fuzz(func(t *testing.T, data []byte) {
		withWatchdog(t, "Open", 5*time.Second, func() {
			//nolint:errcheck // we only care that Open terminates without panic/hang
			_, _ = OpenBytes(data)
		})
	})
}
