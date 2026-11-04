package ui

import (
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// gridView is the results grid's virtual tview content. tview asks only for
// the cells it draws, so a cell is formatted, truncated and styled on demand
// instead of for every fetched row on every render.
//
// The rows themselves stay in state.viewRows and the selection in the table;
// gridView owns only what is derived from them: the column widths (sampled
// once per result, fitted once per viewport width) and a per-cell format
// cache. reset and setWidths are the only writers and both drop the cache, so
// a cached cell always matches the current rows and widths.
type gridView struct {
	tview.TableContentReadOnly
	s        *state
	natural  []int // sampled widths for the current view rows
	widths   []int // natural widths fitted to the viewport
	viewport int   // inner width widths were fitted to; -1 means not yet
	cells    [][]gridCell
	empty    string // shown in place of rows when there are none
}

// gridCell is the row-independent part of a cell: everything except the
// zebra, selection and search backgrounds, which depend on the cursor.
type gridCell struct {
	text  string
	fg    tcell.Color
	right bool
	ready bool
}

func (g *gridView) reset(natural []int, empty string) {
	g.natural = natural
	g.empty = empty
	g.viewport = -1
	g.setWidths(append([]int(nil), natural...))
}

func (g *gridView) setWidths(widths []int) {
	g.widths = widths
	g.cells = make([][]gridCell, len(g.s.viewRows))
}

func (g *gridView) columnCount() int { return len(g.s.current().result.Columns) }

func (g *gridView) GetRowCount() int {
	if len(g.s.viewRows) == 0 {
		return g.emptyRow() + 1
	}
	return len(g.s.viewRows) + 1
}

func (g *gridView) GetColumnCount() int {
	return max(1, g.columnCount())
}

// The empty notice sits below the headers, or alone when there are none.
func (g *gridView) emptyRow() int {
	if g.columnCount() > 0 {
		return 1
	}
	return 0
}

func (g *gridView) GetCell(row, col int) *tview.TableCell {
	s := g.s
	if len(s.viewRows) == 0 && row == g.emptyRow() {
		if col != 0 {
			return nil
		}
		return tview.NewTableCell(escapeTags(g.empty)).SetSelectable(false).SetTextColor(s.theme.TCell(s.theme.Muted))
	}
	if col < 0 || col >= g.columnCount() || col >= len(g.widths) {
		return nil
	}
	if row == 0 {
		return tview.NewTableCell(g.header(col)).
			SetTextColor(s.theme.TCell(s.theme.Primary)).
			SetBackgroundColor(s.theme.TCell(s.theme.Panel)).
			SetAttributes(tcell.AttrBold | tcell.AttrUnderline).
			SetSelectable(false).SetMaxWidth(g.widths[col])
	}
	if row < 1 || row > len(s.viewRows) {
		return nil
	}
	c := g.format(row, col)
	cell := tview.NewTableCell(c.text).SetMaxWidth(g.widths[col])
	if c.right {
		cell.SetAlign(tview.AlignRight)
	}
	fg, bg := c.fg, s.theme.TCell(s.theme.Background)
	if row%2 == 0 {
		bg = s.theme.TCell(s.theme.Panel)
	}
	if selected, _ := s.grid.GetSelection(); row == selected {
		bg = s.theme.TCell(s.theme.Selection)
	}
	// Matches stay recognizable without painting every result bright yellow.
	if i, ok := s.searchMatchAt(row, col); ok {
		fg = s.theme.TCell(s.theme.Accent)
		if i == s.searchIndex {
			fg, bg = s.theme.TCell(s.theme.Background), s.theme.TCell(s.theme.Accent)
			cell.SetTransparency(false)
		}
	}
	return cell.SetStyle(tcell.StyleDefault.Foreground(fg).Background(bg)).
		SetSelectedStyle(s.theme.CellStyle(s.theme.Background, s.theme.Primary))
}

func (g *gridView) format(row, col int) gridCell {
	if g.cells[row-1] == nil {
		g.cells[row-1] = make([]gridCell, g.columnCount())
	}
	c := &g.cells[row-1][col]
	if c.ready {
		return *c
	}
	s := g.s
	column := s.current().result.Columns[col]
	value := rowValue(s.viewRows[row-1], col)
	c.text = escapeTags(gridValue(value, column, g.widths[col]))
	_, c.right = numericValue(value, column)
	c.fg = s.theme.TCell(s.theme.Text)
	if value == nil {
		c.fg = s.theme.TCell(s.theme.Muted)
	} else if b, ok := boolValue(value, column); ok {
		c.fg = s.theme.TCell(s.theme.Danger)
		if b {
			c.fg = s.theme.TCell(s.theme.Success)
		}
	}
	c.ready = true
	return *c
}

// Headers are padded to the column width so the fixed header row pins each
// column's width while scrolling, whatever the visible rows hold.
func (g *gridView) header(col int) string {
	t := g.s.current()
	marker := ""
	if t.sorted && t.sortColumn == col {
		marker = " ▲"
		if t.sortDesc {
			marker = " ▼"
		}
	}
	width := g.widths[col]
	name := truncateCells(displayValue(t.result.Columns[col].Name), max(1, width-len([]rune(marker)))) + marker
	// Types live in the footer inspector; showing them only where they happen to fit made headers uneven.
	return escapeTags(name) + strings.Repeat(" ", max(0, width-cellWidth(name)))
}

// searchMatches is ordered by row, then column, as searchGrid appends them.
func (s *state) searchMatchAt(row, col int) (int, bool) {
	matches := s.searchMatches
	i := sort.Search(len(matches), func(i int) bool {
		return matches[i].Row > row || matches[i].Row == row && matches[i].Col >= col
	})
	return i, i < len(matches) && matches[i] == gridPos{Row: row, Col: col}
}
