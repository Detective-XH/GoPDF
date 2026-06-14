package pdf

// tables_lattice_open_test.go — open edge-column recovery with STRUCTURAL EVIDENCE gate.
//
// Problem: latticeTables only recovers CLOSED cells — columns bounded on all four sides
// by ruling-line intersections. When a table's outer vertical rules are absent (e.g. the
// left label column and the right data column in IRS SOI Table 1), those columns are
// silently dropped.
//
// Solution: three-sided "half-open" cells gated by STRUCTURAL EVIDENCE (h-rule overhang).
// An open edge column is admitted only when the horizontal row rules that bound its bands
// actually extend past the inner vertical boundary (vMin/vMax) into the candidate region
// by more than overhangTol (= 2×snapTol = 6 pt). Text-bbox sets the outer width; rules
// gate existence. This eliminates the NIST p23 false-positive (sidebar rules stop at vMin,
// only ~1 pt "overhang" = snap noise < overhangTol).
//
// Structural evidence gates whether an open column exists; the open-side words' text
// bbox (clamped to the MediaBox) sets how wide it is.
