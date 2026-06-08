// Decode-path classification: attribute every decoded glyph to the path by which
// its text encoder was selected (parsed ToUnicode, charset fallback, missing
// mapping, …) and tally silently-unmapped glyphs, without extending the
// TextEncoding interface. The per-page counters here feed the extraction quality
// ratios; the classification mirrors the encoder-selection warnings 1:1 so a
// counter and the warning that fires never disagree.

package pdf

// encSource classifies how the active text encoder was selected, so each decoded
// glyph can be attributed to a decode path WITHOUT extending the TextEncoding
// interface (a frozen stability contract). It rides alongside the encoder through
// the graphics state.
type encSource uint8

const (
	encSourceUnset            encSource = iota // no font selected yet / nopEncoder
	encSourceToUnicode                         // parsed /ToUnicode CMap (accurate Unicode)
	encSourceMissingToUnicode                  // Identity CMap or unparsable /ToUnicode → byte table
	encSourceFallback                          // predefined CMap decoded via charset approximation
	encSourceDict                              // /Encoding dictionary (BaseEncoding + Differences)
	encSourceUnsupported                       // unknown/odd /Encoding → PDFDocEncoding fallback
	encSourceSimple                            // declared base byte encoding (WinAnsi/MacRoman/PDFDoc)
	numEncSource
)

// decodeCounters accumulates one interpreter run's decoded-rune counts per
// encSource plus the number of U+FFFD replacement runes emitted (silent
// unmapped-glyph loss). It is plain data: copied freely, merged across recursed
// Form XObject sub-states, and read out by the cross-sink agreement test and the
// per-page extraction ratios.
type decodeCounters struct {
	glyphs   [numEncSource]int // decoded runes attributed to each encSource
	unmapped int               // U+FFFD runes in decoded output (any source)
}

// record attributes one decoded show-string to src and tallies its U+FFFD runes.
// It is called ONLY for genuine content strings — never interpreter-synthesised
// separators (TJ kerning spaces, TJ-trailing or T* newlines) — so the content and
// plaintext sinks accumulate identical counts for the same content.
func (c *decodeCounters) record(src encSource, decoded string) {
	for _, r := range decoded {
		c.glyphs[src]++
		if r == noRune {
			c.unmapped++
		}
	}
}

// merge folds a recursed Form XObject sub-state's counters into the parent, so
// XObject text is counted on both decode paths. Without it, XObject content would
// be counted on neither sink and "instrumented identically" would be silently
// false for forms.
func (c *decodeCounters) merge(o decodeCounters) {
	for i := range c.glyphs {
		c.glyphs[i] += o.glyphs[i]
	}
	c.unmapped += o.unmapped
}
