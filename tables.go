package pdf

// Table is one reconstructed ruled ("lattice") table on a page: a rectangular grid
// of cell strings. Cells[r][c] holds the text of the cell in row r, column c; an empty
// cell is the empty string. Rows run top-to-bottom, columns left-to-right in page order.
//
// Experimental: this API is additive-evolving (see API-STABILITY.md). Table may gain
// fields (for example cell bounding boxes) in a future minor release; the Cells field is
// stable. See EXAMPLES.md for usage.
type Table struct {
	// Cells is the reconstructed grid indexed Cells[row][col]. Every row has equal length.
	Cells [][]string
}

// Tables reconstructs the ruled (lattice) tables on the page.
//
// It builds a grid from the page's ruling lines — both stroked lines (Content.Stroke)
// and thin filled rectangles (Content.Rect) — recovers half-open edge columns whose outer
// vertical rule is absent but whose row rules overhang into them (the row-label and last
// data columns common in statistical tables), and assigns each word to its cell by
// geometric containment.
//
// Only ruled tables are detected: a table needs visible horizontal and vertical lines
// closing at least one cell. Borderless and partially-ruled tables yield no Table. An open
// edge column is recovered only where the table's row rules overhang into it; a column
// whose rules stop at the inner vertical is not recovered (a safe omission, not an error).
//
// Experimental: the detection geometry and the Table type are additive-evolving; see
// API-STABILITY.md and EXAMPLES.md. Tables returns the same error as Words.
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
