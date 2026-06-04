package pdf

import (
	"testing"
	"time"
)

// FuzzOpenAndExtract is the broad net for the malformed-PDF DoS class.
//
// It fuzzes the open path and the extraction pipeline together: OpenBytes, then
// NumPage -> Page(1).GetPlainText/Words on a successful open.  OpenBytes runs
// behind a recover boundary; the extraction getters reach resolve(), whose Tier
// 3 recover boundary now degrades a malformed object body to a null Value rather
// than panicking, so a malformed input surfaces as an empty result rather than a
// crash.  The watchdog turns any future hang regression (a re-introduced /Prev
// or post-open link-chain cycle) into a test failure instead of a stalled run.
// It stays green under a long `go test -fuzz=FuzzOpenAndExtract` run,
// demonstrating both the open-time and post-open hardening hold.
func FuzzOpenAndExtract(f *testing.F) {
	f.Add(buildTextPDF("BT /F1 12 Tf (Hello) Tj ET"))
	f.Add(buildCyclicXrefTablePDF())

	f.Fuzz(func(t *testing.T, data []byte) {
		withWatchdog(t, "OpenAndExtract", 5*time.Second, func() {
			r, err := OpenBytes(data)
			if err != nil {
				return
			}
			_ = r.NumPage()
			p := r.Page(1)
			//nolint:errcheck // we only care that extraction terminates without panic/hang
			_, _ = p.GetPlainText(nil)
			//nolint:errcheck // same: Words must not panic or hang on malformed input
			_, _ = p.Words()
		})
	})
}
