package ui

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

type cellFilter struct {
	column int
	value  any
}

func (s *state) renderResult(result query.Result) {
	s.current().result = result
	s.current().err = nil
	s.current().record = false
	s.resultPages.SwitchToPage("grid")
	if s.app.GetFocus() == s.recordView {
		s.focusResults()
	}
	s.searchGrid(s.current().rowFilter)
	if len(s.searchMatches) == 0 {
		s.grid.Select(1, 0)
	}
}

func (s *state) renderGrid(result query.Result) {
	s.grid.SetOffset(0, 0)
	s.viewRows = result.Rows
	widths := columnWidths(result)
	if t := s.current(); t.sorted && t.sortColumn < len(widths) {
		header := truncateCells(displayValue(result.Columns[t.sortColumn].Name), maxColumnWidth-2) + " ▲"
		widths[t.sortColumn] = max(widths[t.sortColumn], cellWidth(header))
	}
	s.gridView.reset(widths, "0 rows")
	s.refreshCell()
}

// Derived views never mutate fetched rows. Sort is stable, with NULLs last in
// either direction, and both filters operate on the full (unshortened) value.
func (s *state) searchGrid(text string) {
	t := s.current()
	t.rowFilter = text
	s.searchMatches = nil
	s.searchIndex = 0
	filtered := t.result
	filtered.Rows = make([][]any, 0, len(t.result.Rows))
	for _, row := range t.result.Rows {
		if f := t.equalFilter; f != nil && !reflect.DeepEqual(rowValue(row, f.column), f.value) {
			continue
		}
		filtered.Rows = append(filtered.Rows, row)
	}
	if t.sorted && t.sortColumn < len(t.result.Columns) {
		col := t.sortColumn
		sort.SliceStable(filtered.Rows, func(i, j int) bool {
			a, b := rowValue(filtered.Rows[i], col), rowValue(filtered.Rows[j], col)
			if a == nil || b == nil {
				return a != nil && b == nil
			}
			cmp := compareValues(a, b, t.result.Columns[col])
			if t.sortDesc {
				return cmp > 0
			}
			return cmp < 0
		})
	}
	if text != "" {
		rows := filtered.Rows
		filtered.Rows = make([][]any, 0, len(rows))
		lower := strings.ToLower(text)
		for _, row := range rows {
			matches := []int{}
			for col := range t.result.Columns {
				if strings.Contains(strings.ToLower(fullValue(rowValue(row, col))), lower) {
					matches = append(matches, col)
				}
			}
			if len(matches) > 0 {
				filtered.Rows = append(filtered.Rows, row)
				for _, col := range matches {
					s.searchMatches = append(s.searchMatches, gridPos{Row: len(filtered.Rows), Col: col})
				}
			}
		}
	}
	filtered.RowCount = len(filtered.Rows)
	s.renderGrid(filtered)
	s.grid.Select(min(max(1, t.cursor.Row), max(1, len(s.viewRows))), min(max(0, t.cursor.Col), max(0, len(t.result.Columns)-1)))
	if t.err != nil {
		s.setMessage(t.err.Error())
	} else if len(s.searchMatches) > 0 {
		s.applySearchMatch()
	} else if text != "" {
		s.setMessage(fmt.Sprintf("no match %s", displayValue(text)))
	} else if t.equalFilter != nil {
		s.setMessage("equal filter · f/Esc clear")
	} else {
		s.setMessage("")
	}
}

func (s *state) sortGrid(desc bool) {
	if _, _, ok := s.cursorValue(); !ok {
		return
	}
	s.saveTab()
	_, col := s.grid.GetSelection()
	t := s.current()
	t.sorted, t.sortColumn, t.sortDesc = true, col, desc
	s.searchGrid(t.rowFilter)
}

func (s *state) toggleCellFilter() {
	t := s.current()
	if t.equalFilter != nil {
		t.equalFilter = nil
	} else {
		value, col, ok := s.cursorValue()
		if !ok {
			return
		}
		t.equalFilter = &cellFilter{column: col, value: value}
	}
	s.saveTab()
	t.cursor.Row = 1
	s.searchGrid(t.rowFilter)
}

func (s *state) advanceSearch(delta int) {
	if len(s.searchMatches) == 0 {
		return
	}
	s.searchIndex = (s.searchIndex + delta + len(s.searchMatches)) % len(s.searchMatches)
	s.applySearchMatch()
}

func (s *state) applySearchMatch() {
	active := s.searchMatches[s.searchIndex]
	s.grid.Select(active.Row, active.Col)
	s.setMessage(fmt.Sprintf("/ %s · match %d/%d", displayValue(s.current().rowFilter), s.searchIndex+1, len(s.searchMatches)))
}

func (s *state) cursorValue() (any, int, bool) {
	row, col := s.grid.GetSelection()
	if row < 1 || row > len(s.viewRows) || col < 0 || col >= len(s.current().result.Columns) || s.current().err != nil {
		return nil, col, false
	}
	return rowValue(s.viewRows[row-1], col), col, true
}

func (s *state) selectionChanged(row, col int) {
	s.current().cursor = gridPos{Row: row, Col: col}
	s.refreshCell()
	if s.current().record {
		s.renderRecord()
	}
}

func (s *state) refreshCell() {
	if s.cellInfo == nil {
		return
	}
	value, col, ok := s.cursorValue()
	if !ok {
		s.cellInfo.SetText("")
		title := " results · 0 rows "
		if s.current().err != nil {
			title = " results · error "
		} else if len(s.viewRows) > 0 {
			title = fmt.Sprintf(" results · %d rows ", len(s.viewRows))
		} else if len(s.current().result.Columns) == 0 {
			title = " results "
		}
		s.resultPane.SetTitle(title)
		return
	}
	column := s.current().result.Columns[col]
	row, _ := s.grid.GetSelection()
	s.cellInfo.SetText(fmt.Sprintf("\n[%s]%s[-:-:-] · [%s]%s[-:-:-] · %s", s.theme.Primary, tview.Escape(displayValue(column.Name)), s.theme.Muted, tview.Escape(column.Database), tview.Escape(displayValue(fullValue(value)))))
	s.resultPane.SetTitle(fmt.Sprintf(" results · %d/%d rows · col %d/%d ", row, len(s.viewRows), col+1, len(s.current().result.Columns)))
	if s.current().record {
		s.resultPane.SetTitle(fmt.Sprintf(" record · %d/%d rows ", row, len(s.viewRows)))
	}
}

func (s *state) openRecord() {
	if _, _, ok := s.cursorValue(); !ok {
		return
	}
	s.current().record = true
	s.renderRecord()
	s.resultPages.SwitchToPage("record")
	s.app.SetFocus(s.recordView)
	s.refreshMode()
}

func (s *state) renderRecord() {
	row, _ := s.grid.GetSelection()
	if row < 1 || row > len(s.viewRows) {
		s.recordView.SetText("0 rows")
		return
	}
	columns := s.current().result.Columns
	keyWidth, typeWidth := 0, 0
	for _, column := range columns {
		keyWidth = max(keyWidth, tview.TaggedStringWidth(tview.Escape(displayValue(column.Name))))
		typeWidth = max(typeWidth, tview.TaggedStringWidth(tview.Escape(displayValue(column.Database))))
	}
	width := s.recordWidth
	if width == 0 {
		width = 80
	}
	var out strings.Builder
	for col, column := range columns {
		value := rowValue(s.viewRows[row-1], col)
		fg := s.theme.Text
		if value == nil {
			fg = s.theme.Muted
		}
		key, typ, text := displayValue(column.Name), displayValue(column.Database), displayValue(fullValue(value))
		if keyWidth+typeWidth+4+tview.TaggedStringWidth(tview.Escape(text)) <= width {
			key = strings.Repeat(" ", keyWidth-tview.TaggedStringWidth(tview.Escape(key))) + key
			typ += strings.Repeat(" ", typeWidth-tview.TaggedStringWidth(tview.Escape(typ)))
			fmt.Fprintf(&out, "[%s]%s[-:-:-]  [%s]%s[-:-:-]  [%s]%s[-:-:-]\n", s.theme.Primary, tview.Escape(key), s.theme.Muted, tview.Escape(typ), fg, tview.Escape(text))
		} else {
			fmt.Fprintf(&out, "[%s]%s[-:-:-]  [%s]%s[-:-:-]\n[%s]%s[-:-:-]\n\n", s.theme.Primary, tview.Escape(key), s.theme.Muted, tview.Escape(typ), fg, tview.Escape(text))
		}
	}
	s.resultPane.SetTitle(fmt.Sprintf(" record · %d/%d rows ", row, len(s.viewRows)))
	s.recordView.SetText(out.String()).ScrollToBeginning()
}

func (s *state) closeRecord() {
	s.current().record = false
	s.resultPages.SwitchToPage("grid")
	s.refreshCell()
	s.focusResults()
}
