package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func (s *state) renderResult(result query.Result) {
	s.current().result = result
	s.current().err = nil
	s.renderGrid(result)
	if s.current().rowFilter != "" {
		s.searchGrid(s.current().rowFilter)
	} else {
		s.grid.Select(1, 0)
	}
}

func (s *state) renderGrid(result query.Result) {
	s.grid.Clear()
	for col, c := range result.Columns {
		s.grid.SetCell(0, col, tview.NewTableCell(tview.Escape(c.Name)).
			SetTextColor(s.theme.TCell(s.theme.Primary)).
			SetBackgroundColor(s.theme.TCell(s.theme.Panel)).
			SetSelectable(false).
			SetMaxWidth(columnMaxWidth(c.Name)).
			SetExpansion(1))
	}
	for rowIdx, row := range result.Rows {
		bg := s.theme.Background
		if rowIdx%2 == 1 {
			bg = s.theme.Panel
		}
		for colIdx := range result.Columns {
			value := ""
			if colIdx < len(row) {
				value = fmt.Sprint(row[colIdx])
			}
			s.grid.SetCell(rowIdx+1, colIdx, tview.NewTableCell(tview.Escape(value)).
				SetTextColor(s.theme.TCell(s.theme.Text)).
				SetBackgroundColor(s.theme.TCell(bg)).
				SetMaxWidth(columnMaxWidth(result.Columns[colIdx].Name)).
				SetExpansion(1))
		}
	}
	if len(result.Rows) == 0 {
		row := 0
		if len(result.Columns) > 0 {
			row = 1
		}
		s.grid.SetCell(row, 0, tview.NewTableCell("0 rows").SetSelectable(false).SetTextColor(s.theme.TCell(s.theme.Muted)).SetExpansion(1))
	}
}

func columnMaxWidth(name string) int {
	switch name {
	case "detail", "payload", "metadata", "body", "content":
		return 56
	default:
		return 32
	}
}

func (s *state) searchGrid(text string) {
	s.current().rowFilter = text
	s.searchMatches = nil
	s.searchIndex = 0
	if text == "" {
		s.renderGrid(s.current().result)
		s.setMessage("")
		return
	}
	if len(s.current().result.Columns) == 0 {
		s.setMessage("no results to filter")
		return
	}
	lower := strings.ToLower(text)
	filtered := query.Result{
		ID:        s.current().result.ID,
		Columns:   s.current().result.Columns,
		Rows:      make([][]any, 0, len(s.current().result.Rows)),
		Truncated: s.current().result.Truncated,
		Elapsed:   s.current().result.Elapsed,
		Executed:  s.current().result.Executed,
	}
	for _, row := range s.current().result.Rows {
		matchesRow := false
		filteredRow := len(filtered.Rows) + 1
		for colIdx := range s.current().result.Columns {
			value := ""
			if colIdx < len(row) {
				value = fmt.Sprint(row[colIdx])
			}
			if strings.Contains(strings.ToLower(value), lower) {
				matchesRow = true
				s.searchMatches = append(s.searchMatches, gridPos{Row: filteredRow, Col: colIdx})
			}
		}
		if matchesRow {
			filtered.Rows = append(filtered.Rows, row)
		}
	}
	filtered.RowCount = len(filtered.Rows)
	s.renderGrid(filtered)
	if len(s.searchMatches) > 0 {
		s.applySearchMatch()
		return
	}
	s.setMessage(fmt.Sprintf("no match %s · 0/%d rows", text, len(s.current().result.Rows)))
}

func (s *state) advanceSearch(delta int) {
	if len(s.searchMatches) == 0 {
		if s.current().rowFilter != "" {
			s.searchGrid(s.current().rowFilter)
		}
		return
	}
	s.searchIndex = (s.searchIndex + delta + len(s.searchMatches)) % len(s.searchMatches)
	s.applySearchMatch()
}

func (s *state) applySearchMatch() {
	s.resetSearchStyles()
	for idx, match := range s.searchMatches {
		cell := s.grid.GetCell(match.Row, match.Col)
		if cell == nil {
			continue
		}
		bg := s.theme.Primary
		if idx == s.searchIndex {
			bg = s.theme.Accent
		}
		cell.SetTextColor(s.theme.TCell(s.theme.Background)).
			SetBackgroundColor(s.theme.TCell(bg)).
			SetSelectedStyle(s.theme.CellStyle(s.theme.Background, s.theme.Accent))
	}
	active := s.searchMatches[s.searchIndex]
	s.grid.Select(active.Row, active.Col)
	s.setMessage(fmt.Sprintf("filter %d/%d rows · match %d/%d row=%d column=%d · %s · n/N next/prev", s.grid.GetRowCount()-1, len(s.current().result.Rows), s.searchIndex+1, len(s.searchMatches), active.Row, active.Col+1, s.current().rowFilter))
}

func (s *state) resetSearchStyles() {
	for row := 1; row < s.grid.GetRowCount(); row++ {
		bg := s.theme.Background
		if row%2 == 0 {
			bg = s.theme.Panel
		}
		for col := 0; col < s.grid.GetColumnCount(); col++ {
			cell := s.grid.GetCell(row, col)
			if cell == nil {
				continue
			}
			cell.SetTextColor(s.theme.TCell(s.theme.Text)).
				SetBackgroundColor(s.theme.TCell(bg)).
				SetSelectedStyle(tcell.StyleDefault)
		}
	}
}
