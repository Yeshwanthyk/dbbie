package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func (s *state) loadCatalog() error {
	tables, err := catalog.NewInspector(s.kind, s.db).Tables(s.ctx)
	if err != nil {
		return err
	}
	// Slice B fills planner estimates. Until then the catalog's zero value is
	// rendered according to the shared contract, not guessed by the UI.
	s.tables = tables
	return nil
}

func (s *state) tableName(table catalog.Table) string {
	if s.multiSchema && table.Schema != "" {
		return table.Schema + "." + table.Name
	}
	return table.Name
}

func compactRows(n int64) string {
	if n < 0 {
		return ""
	}
	if n == 0 {
		return "empty"
	}
	for _, unit := range []struct {
		size   int64
		suffix string
	}{{1_000_000_000, "b"}, {1_000_000, "m"}, {1_000, "k"}} {
		if n >= unit.size {
			return fmt.Sprintf("%.1f%s", float64(n)/float64(unit.size), unit.suffix)
		}
	}
	return strconv.FormatInt(n, 10)
}

// fuzzyMatches returns rune positions, avoiding byte offsets for UTF-8 names.
func fuzzyMatches(name, filter string) ([]int, bool) {
	needle := []rune(strings.ToLower(filter))
	positions := make([]int, 0, len(needle))
	for i, r := range []rune(name) {
		if len(positions) < len(needle) && unicode.ToLower(r) == needle[len(positions)] {
			positions = append(positions, i)
		}
	}
	return positions, len(positions) == len(needle)
}

func (s *state) filterTables(filter string) {
	schemas := make(map[string]bool)
	for _, table := range s.tables {
		schemas[table.Schema] = true
	}
	s.multiSchema = len(schemas) > 1
	s.sidebar.Clear()
	s.visibleTables = nil
	longest := 0
	for _, table := range s.tables {
		name := s.tableName(table)
		longest = max(longest, tview.TaggedStringWidth(tview.Escape(name)))
		positions, ok := fuzzyMatches(name, filter)
		if !ok {
			continue
		}
		row := len(s.visibleTables)
		s.visibleTables = append(s.visibleTables, table)
		base := s.theme.Text
		if table.EstimatedRows == 0 {
			base = s.theme.Muted
		}
		s.sidebar.SetCell(row, 0, tview.NewTableCell(s.highlightTableName(name, positions, base)).SetTextColor(s.theme.TCell(base)).SetExpansion(1))
		s.sidebar.SetCell(row, 1, tview.NewTableCell(compactRows(table.EstimatedRows)).SetTextColor(s.theme.TCell(s.theme.Muted)).SetAlign(tview.AlignRight))
	}
	s.sideWidth = min(44, max(24, longest+10))
	s.fitSidebar(s.sideWidth - 4)
	s.body.ResizeItem(s.sidePane, s.sideWidth, 0)
	s.sidePane.SetTitle(fmt.Sprintf(" tables %d/%d ", len(s.visibleTables), len(s.tables)))
	if len(s.visibleTables) == 0 {
		s.sidebar.SetCell(0, 0, tview.NewTableCell("no tables match").SetSelectable(false).SetTextColor(s.theme.TCell(s.theme.Muted)))
	}
	s.sidebar.Select(0, 0).SetOffset(0, 0)
}

func (s *state) highlightTableName(name string, positions []int, base string) string {
	var highlighted strings.Builder
	next := 0
	for i, r := range []rune(name) {
		if next < len(positions) && positions[next] == i {
			fmt.Fprintf(&highlighted, "[%s::b]%s[%s::-]", s.theme.Accent, tview.Escape(string(r)), base)
			next++
		} else {
			highlighted.WriteString(tview.Escape(string(r)))
		}
	}
	return highlighted.String()
}

func (s *state) fitSidebar(width int) {
	countWidth := 5 // reserve the same right-aligned column for empty / counts
	for _, table := range s.visibleTables {
		countWidth = max(countWidth, len(compactRows(table.EstimatedRows)))
	}
	nameWidth := max(1, width-countWidth-1)
	for row, table := range s.visibleTables {
		name := truncateCells(s.tableName(table), nameWidth)
		positions, _ := fuzzyMatches(s.tableName(table), s.tableFilter.GetText())
		base := s.theme.Text
		if table.EstimatedRows == 0 {
			base = s.theme.Muted
		}
		s.sidebar.GetCell(row, 0).SetText(s.highlightTableName(name, positions, base)).SetMaxWidth(nameWidth)
		s.sidebar.GetCell(row, 1).SetText(fmt.Sprintf("%*s", countWidth, compactRows(table.EstimatedRows))).SetMaxWidth(countWidth)
	}
}

func (s *state) selectedTable() (catalog.Table, bool) {
	row, _ := s.sidebar.GetSelection()
	if row < 0 || row >= len(s.visibleTables) {
		return catalog.Table{}, false
	}
	return s.visibleTables[row], true
}

func (s *state) openSelectedTable(newTab bool) {
	table, ok := s.selectedTable()
	if !ok {
		return
	}
	if newTab || s.current().origin != nil || s.current().sessionName != "" {
		s.newTab(table.Name, "", nil)
	}
	s.current().title = table.Name
	s.openTable(tableRef{Schema: table.Schema, Name: table.Name})
	s.focusResults()
}

func (s *state) openTable(ref tableRef) {
	sqlText := "select * from "
	if ref.Schema != "" {
		sqlText += quoteIdent(s.kind, ref.Schema) + "."
	}
	sqlText += quoteIdent(s.kind, ref.Name)
	s.query.SetText(sqlText)
	s.runQuery(sqlText)
}

func (s *state) describeSelectedTable() {
	table, ok := s.selectedTable()
	if !ok {
		return
	}
	s.newTab(table.Name+" columns", "", nil)
	inspector := catalog.NewInspector(s.kind, s.db)
	s.startWork(s.current(), func(ctx context.Context) (query.Result, error) {
		columns, err := inspector.Columns(ctx, table.Schema, table.Name)
		if err != nil {
			return query.Result{}, err
		}
		result := query.Result{Columns: []query.Column{{Name: "column"}, {Name: "type"}, {Name: "nullable"}, {Name: "default"}}, RowCount: len(columns)}
		for _, c := range columns {
			result.Rows = append(result.Rows, []any{c.Name, c.DataType, c.Nullable, c.Default})
		}
		return result, nil
	})
	s.focusResults()
}
