package pdf

import (
	"context"
	"testing"
)

// signalExpect is the deterministic extraction-readiness signal a signals/ corpus
// fixture locks on page 1. The values were set from an empirical classification
// probe at implementation time (plans-conventions Honesty Rule), not from recon
// guesses. -1 means "do not assert".
type signalExpect struct {
	hasText       bool
	wordCountMin  int              // minimum Words() on page 1; 0 = no lower bound
	imageCount    int              // expected countDrawnImages; -1 = do not assert
	imageOnlyWarn bool             // expect WarningImageOnlyPage on page 1
	wantErr       bool             // ExtractionSummary returns an error (panic propagated past Words)
	gpErr         bool             // Reader.GetPlainText returns an error (independent of wantErr)
	signal        ExtractionSignal // expected Page.ExtractionSignal on page 1
}

// signalExpectations is the single source of truth for slice-1 signal contracts,
// keyed by fixture Path. Consumed by TestCorpusSignalFixtures AND the anchored
// arm in TestCorpusNoGoldenFixtures (DRY).
//
// gpErr keys on Reader.GetPlainText; wantErr keys on ExtractionSummary. The two are
// independent and DIVERGE for malformed-truncated: GetPlainText surfaces the
// TJ-without-array panic as an error (gpErr:true, Golden:"" in the manifest), yet
// ExtractionSummary's Words() pass is shielded by Content()'s recover and reports a
// clean-looking HasText=true from "delta" (wantErr:false). Asserting gpErr is what
// makes this fixture's defining behavior non-vacuous — ExtractionSummary alone cannot
// tell it apart from a clean page, so without gpErr the malformed anchor would pass
// equally for well-formed content and silently lose coverage if the panic-recover
// path ever changed.
//
// signal is keyed on Page.ExtractionSignal, whose text authority is
// the STRICT GetPlainText path. It therefore tracks gpErr, not HasText:
// malformed-truncated has HasText=true (Words recovers) yet signal=degraded
// (GetPlainText errors) — the same divergence gpErr already locks. Values were
// confirmed by an empirical classification probe at implementation time, per
// the plans-conventions Honesty Rule.
var signalExpectations = map[string]signalExpect{
	"signals/image-full-bleed.pdf":        {hasText: false, imageCount: 1, imageOnlyWarn: true, signal: SignalImageOnly},
	"signals/image-thumbnail.pdf":         {hasText: false, imageCount: 1, imageOnlyWarn: true, signal: SignalImageOnly},
	"signals/image-thumbnail-text.pdf":    {hasText: true, wordCountMin: 1, imageCount: 1, imageOnlyWarn: false, signal: SignalText},
	"signals/text-artifact-only.pdf":      {hasText: true, wordCountMin: 1, imageCount: 0, imageOnlyWarn: false, signal: SignalText},
	"signals/malformed-unclosed-bt.pdf":   {hasText: true, wordCountMin: 1, imageCount: 0, imageOnlyWarn: false, signal: SignalText},
	"signals/malformed-mismatched-qq.pdf": {hasText: true, wordCountMin: 1, imageCount: 0, imageOnlyWarn: false, signal: SignalText},
	"signals/malformed-truncated.pdf":     {hasText: true, wordCountMin: 1, imageCount: 0, imageOnlyWarn: false, gpErr: true, signal: SignalDegraded},
}

// assertPageSignal locks the page-1 signal for a signals/ fixture and asserts
// determinism (two passes agree) and the no-panic guarantee (the call returning at
// all proves Content/ExtractionSummary recovered rather than crashing the process).
func assertPageSignal(t *testing.T, r *Reader, exp signalExpect) {
	t.Helper()
	// GetPlainText axis — independent of ExtractionSummary. This locks the DEFINING
	// behavior of fixtures whose malformation only surfaces here: malformed-truncated
	// panics in the TJ handler and GetPlainText reports it as an error, while
	// ExtractionSummary recovers to a clean-looking signal. Without this assertion the
	// malformed anchor would be vacuous. For every other fixture gpErr is false, so
	// this also confirms GetPlainText does not error on them.
	if _, gpErr := r.GetPlainText(context.Background()); (gpErr != nil) != exp.gpErr {
		t.Errorf("GetPlainText error present = %v, want %v (err=%v)", gpErr != nil, exp.gpErr, gpErr)
	}
	s1, err1 := r.Page(1).ExtractionSummary()
	s2, err2 := r.Page(1).ExtractionSummary()
	if (err1 == nil) != (err2 == nil) {
		t.Fatalf("ExtractionSummary error determinism: err1=%v err2=%v", err1, err2)
	}
	if exp.wantErr {
		if err1 == nil {
			t.Fatalf("ExtractionSummary: want error (panic-recovered), got nil; summary=%+v", s1)
		}
		return // a failed stream yields no reliable signal fields
	}
	if err1 != nil {
		t.Fatalf("ExtractionSummary: unexpected error: %v", err1)
	}
	assertSummaryDeterministic(t, s1, s2)
	assertSignalFields(t, s1, exp)
	assertSignalValue(t, r.Page(1), exp.signal)
}

// assertSignalValue locks Page.ExtractionSignal: deterministic across
// two calls and equal to the fixture's expected routing signal. Split out of
// assertPageSignal for the gocyclo budget.
func assertSignalValue(t *testing.T, p Page, want ExtractionSignal) {
	t.Helper()
	got1 := p.ExtractionSignal()
	got2 := p.ExtractionSignal()
	if got1 != got2 {
		t.Fatalf("ExtractionSignal not deterministic: %q vs %q", got1, got2)
	}
	if got1 != want {
		t.Errorf("ExtractionSignal = %q, want %q", got1, want)
	}
}

// assertSummaryDeterministic compares the SCALAR fields of two ExtractionSummary
// passes. PageExtractionSummary contains a Warnings []ExtractionWarning slice, so it
// is not comparable (`s1 != s2` would not compile). Warnings is also EXCLUDED BY
// DESIGN, not merely for comparability: ExtractionSummary mutates the Reader's warning
// set via r.warn(), so a second call on the same Reader can append a duplicate
// WarningImageOnlyPage. Diffing Warnings here would be a false determinism failure —
// do NOT "fix" this into a deep Warnings comparison. Split out for the gocyclo budget.
func assertSummaryDeterministic(t *testing.T, s1, s2 PageExtractionSummary) {
	t.Helper()
	if s1.HasText != s2.HasText || s1.WordCount != s2.WordCount ||
		s1.ImageCount != s2.ImageCount || s1.Page != s2.Page {
		t.Fatalf("ExtractionSummary scalar fields not deterministic: %+v vs %+v", s1, s2)
	}
}

// assertSignalFields checks one summary against the expected signal. Split out of
// assertPageSignal for the gocyclo budget.
func assertSignalFields(t *testing.T, s PageExtractionSummary, exp signalExpect) {
	t.Helper()
	if s.HasText != exp.hasText {
		t.Errorf("HasText = %v, want %v (summary=%+v)", s.HasText, exp.hasText, s)
	}
	if exp.wordCountMin > 0 && s.WordCount < exp.wordCountMin {
		t.Errorf("WordCount = %d, want >= %d", s.WordCount, exp.wordCountMin)
	}
	if exp.imageCount >= 0 && s.ImageCount != exp.imageCount {
		t.Errorf("ImageCount = %d, want %d", s.ImageCount, exp.imageCount)
	}
	got := false
	for _, w := range s.Warnings {
		if w.Code == WarningImageOnlyPage {
			got = true
		}
	}
	if got != exp.imageOnlyWarn {
		t.Errorf("WarningImageOnlyPage present = %v, want %v (warnings=%+v)", got, exp.imageOnlyWarn, s.Warnings)
	}
}

// TestCorpusSignalFixtures is the consumer-facing characterization layer for the
// signal fixtures. The quality-score and image/scanned-classifier work extend
// signalExpectations + assertPageSignal as the signal definitions grow.
func TestCorpusSignalFixtures(t *testing.T) {
	for _, e := range corpusManifest {
		exp, ok := signalExpectations[e.Path]
		if !ok {
			continue
		}
		t.Run(e.Path, func(t *testing.T) {
			assertPageSignal(t, loadCorpus(t, e), exp)
		})
	}
}
