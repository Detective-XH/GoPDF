package pdf

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// recoverIntentionalParserPanic is the recover-boundary shim for fuzz targets
// that drive parser internals BELOW the public API's recover boundaries.
//
// GoPDF's lexer uses panic-as-error: buf.errorf (buf.go) raises panic(error)
// on malformed input, and the public API recovers it at two boundaries —
// NewReaderEncrypted (read.go, open path) and resolve (resolve.go, post-open
// getter path) — turning it into a clean error or a null Value. A fuzz target
// that calls readToken / readXrefTableData / readCmap-via-Interpret directly
// sits beneath those boundaries, so every malformed input raises an intentional
// errorf panic that is NOT a bug. Without this shim the fuzz engine flags the
// first stray delimiter as a crash and buries any genuine fault behind noise.
//
// Discriminator (order matters: runtime.Error embeds error, so it must be
// checked first): a runtime.Error — index-out-of-range, nil deref, bad type
// assertion, integer divide — is a real bug, so re-panic and let the fuzz
// engine record it. A plain error is the lexer's intentional malformed-input
// signal, so swallow it. Any other panic value is unexpected; re-panic
// conservatively.
//
// Targets that already run ABOVE a recover boundary (FuzzOpenAndExtract via
// OpenBytes) or over purely error-returning code (FuzzFilterFlateDecode) must
// NOT use this shim — there, any panic is genuinely a bug.
func recoverIntentionalParserPanic(t *testing.T) {
	t.Helper()
	r := recover()
	if r == nil {
		return
	}
	if _, ok := r.(runtime.Error); ok {
		panic(r) // genuine runtime fault — let the fuzz engine flag it
	}
	if _, ok := r.(error); ok {
		return // intentional errorf(error) panic on malformed input — expected
	}
	panic(r) // unknown panic value — treat conservatively as a real fault
}

// indexInto reads s[i]. It is a separate function so the static analyzers
// cannot constant-fold the access, letting the out-of-range call in the test
// below raise a genuine runtime bounds fault that survives pre-flight.
func indexInto(s []int, i int) int { return s[i] }

// TestRecoverIntentionalParserPanic locks recoverIntentionalParserPanic's
// discriminator contract. The active-fuzz runs only ever trigger the lexer's
// intentional errorf noise, so they exercise the swallow path but never the
// re-panic path — yet re-propagating a genuine fault is the whole reason the
// shim exists. A silently inverted discriminator would drop all fuzz signal and
// no other test would catch it, so verify every branch directly.
func TestRecoverIntentionalParserPanic(t *testing.T) {
	// run drives the shim via defer, either by panicking with panicValue or, when
	// trigger is set, by executing trigger to raise a real fault. It reports
	// whether the panic re-propagated past the shim and the recovered value.
	run := func(panicValue any, trigger func()) (rePanicked bool, recovered any) {
		defer func() {
			if r := recover(); r != nil {
				rePanicked, recovered = true, r
			}
		}()
		func() {
			defer recoverIntentionalParserPanic(t)
			if trigger != nil {
				trigger()
				return
			}
			panic(panicValue)
		}()
		return false, nil
	}

	t.Run("plain error is swallowed", func(t *testing.T) {
		if rePanicked, _ := run(fmt.Errorf("malformed input"), nil); rePanicked {
			t.Fatal("shim re-panicked on a plain error; the intentional errorf signal must be swallowed")
		}
	})

	t.Run("runtime fault re-propagates", func(t *testing.T) {
		rePanicked, recovered := run(nil, func() { indexInto([]int{}, 1) })
		if !rePanicked {
			t.Fatal("shim swallowed a runtime.Error; genuine faults must reach the fuzz engine")
		}
		if _, ok := recovered.(runtime.Error); !ok {
			t.Fatalf("re-propagated value is %T, want runtime.Error", recovered)
		}
	})

	t.Run("non-error panic re-propagates", func(t *testing.T) {
		rePanicked, recovered := run("bare string", nil)
		if !rePanicked {
			t.Fatal("shim swallowed a non-error panic; only the intentional errorf signal may be swallowed")
		}
		if s, ok := recovered.(string); !ok || s != "bare string" {
			t.Fatalf("re-propagated value is %#v, want \"bare string\"", recovered)
		}
	})
}

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
			//nolint:errcheck // Page.GetPlainText(nil fonts): nil map is valid; we only require extraction to terminate without panic.
			_, _ = p.GetPlainText(nil)
			//nolint:errcheck // same: Words must not panic or hang on malformed input
			_, _ = p.Words()
			_, _ = p.Blocks()
		})
	})
}
