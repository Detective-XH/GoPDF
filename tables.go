package pdf

// Table is one reconstructed ruled ("lattice") table on a page: a rectangular grid
// of cell strings. Cells[r][c] holds the text of the cell in row r, column c; an empty
// cell is the empty string. Rows run top-to-bottom, columns left-to-right in page order.
//
// Experimental: the API is additive-evolving (see API-STABILITY.md). Cells is the stable
// core; Table may gain fields (for example cell bounding boxes) in a future minor release,
// and the detection output may still change as extraction quality stabilizes. See EXAMPLES.md.
type Table struct {
	// Cells is the reconstructed grid indexed Cells[row][col]. Every row has equal length.
	Cells [][]string
}

// Tables reconstructs the ruled ("lattice") tables on the page as grids of cell strings.
//
// It builds a grid from the page's ruling lines — both stroked lines (Content.Stroke)
// and thin filled rectangles (Content.Rect) — recovers half-open edge columns whose outer
// vertical rule is absent but whose row rules overhang into them (the row-label and last
// data columns common in statistical tables), and assigns each word to its cell by
// geometric containment.
//
// Documented scope: ruled lattices — interior cells closed by a visible rule between
// adjacent rows AND adjacent columns, plus the half-open edge columns recovered below (a
// row-label or last-data column whose outer vertical rule is absent but whose row rules
// overhang the inner vertical). On that scope reconstruction is locked against regression
// by corpus accuracy gates (the determinism promise in API-STABILITY.md). Outside it the
// result is best-effort, not a contract:
//
//   - A borderless table (no closing rules) yields no Table.
//   - A partially-ruled or banded table — ruled only at group boundaries, with rows
//     separated by shading rather than rules (common in statistical tables) — may yield
//     no Table or a structurally incomplete or merged grid. Treat such a grid as advisory.
//
// An open edge column is recovered only where the table's row rules overhang into it; a
// column whose rules stop at the inner vertical is not recovered (a safe omission).
//
// Verbatim caveat: a superscript renders at a distinct vertical position and font size, so
// it extracts as a spaced token (for example "cm²" becomes "cm 2"). This is specific to
// Y-offset glyph transitions, not a general spacing artifact; cell content — the right
// value in the right cell — is unaffected.
//
// Experimental: the detection geometry and the Table type are additive-evolving, and the
// reconstruction output may still change as extraction quality is stabilized across the
// real-world table distribution (see API-STABILITY.md). Tables returns the same error as
// Words. See EXAMPLES.md for usage.
func (p Page) Tables() ([]Table, error) {
	words, err := p.Words()
	if err != nil {
		return nil, err
	}
	c := p.Content()
	media := p.MediaBox()
	lattices := latticeTablesOpen(c, words, media)
	tables := make([]Table, 0, len(lattices))
	for _, cells := range lattices {
		grid := reconstructGrid(cells, words)
		if len(grid) == 0 {
			continue
		}
		tables = append(tables, Table{Cells: grid})
	}
	return tables, nil
}
