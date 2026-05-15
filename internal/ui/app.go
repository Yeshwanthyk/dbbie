package ui

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/theme"
)

type Options struct {
	Kind       db.Kind
	DB         *sql.DB
	Theme      theme.Theme
	Limit      int
	InitialSQL string
}

type tableRef struct {
	Schema string
	Name   string
}

type gridPos struct {
	Row int
	Col int
}

func Run(ctx context.Context, opts Options) error {
	opts = NormalizeOptions(opts)
	ApplyTViewTheme(opts.Theme)
	app := tview.NewApplication()
	state := newState(ctx, app, opts)
	if err := state.loadCatalog(); err != nil {
		return err
	}
	root := state.layout()
	state.runInitialSQL(opts.InitialSQL)
	app.SetInputCapture(state.captureInput)
	return app.SetRoot(root, true).EnableMouse(true).Run()
}

func NormalizeOptions(opts Options) Options {
	if opts.Limit <= 0 {
		opts.Limit = query.DefaultPreviewLimit
	}
	if opts.Limit > query.MaxPreviewLimit {
		opts.Limit = query.MaxPreviewLimit
	}
	return opts
}

func ApplyTViewTheme(t theme.Theme) {
	tview.Styles.PrimitiveBackgroundColor = t.TCell(t.Background)
	tview.Styles.ContrastBackgroundColor = t.TCell(t.Panel)
	tview.Styles.MoreContrastBackgroundColor = t.TCell(t.Primary)
	tview.Styles.BorderColor = t.TCell(t.Border)
	tview.Styles.TitleColor = t.TCell(t.Primary)
	tview.Styles.GraphicsColor = t.TCell(t.Border)
	tview.Styles.PrimaryTextColor = t.TCell(t.Text)
	tview.Styles.SecondaryTextColor = t.TCell(t.Muted)
	tview.Styles.TertiaryTextColor = t.TCell(t.Accent)
	tview.Styles.InverseTextColor = t.TCell(t.Background)
	tview.Styles.ContrastSecondaryTextColor = t.TCell(t.Muted)
}

type state struct {
	ctx     context.Context
	app     *tview.Application
	kind    db.Kind
	db      *sql.DB
	theme   theme.Theme
	limit   int
	header  *tview.TextView
	status  *tview.TextView
	tree    *tview.TreeView
	grid    *tview.Table
	query   *tview.InputField
	command *tview.InputField
	search  *tview.InputField

	lastResult    query.Result
	searchText    string
	searchMatches []gridPos
	searchIndex   int
}

func newState(ctx context.Context, app *tview.Application, opts Options) *state {
	return &state{
		ctx:   ctx,
		app:   app,
		kind:  opts.Kind,
		db:    opts.DB,
		theme: opts.Theme,
		limit: opts.Limit,
	}
}

func (s *state) loadCatalog() error {
	tables, err := catalog.NewInspector(s.kind, s.db).Tables(s.ctx)
	if err != nil {
		return err
	}
	root := tview.NewTreeNode("dbbie").
		SetTextStyle(s.theme.CellStyle(s.theme.Primary, s.theme.Panel)).
		SetSelectedTextStyle(s.theme.CellStyle(s.theme.Background, s.theme.Primary)).
		SetSelectable(false)
	schemas := map[string]*tview.TreeNode{}
	for _, tbl := range tables {
		schemaNode := schemas[tbl.Schema]
		if schemaNode == nil {
			schemaNode = tview.NewTreeNode(tbl.Schema).
				SetTextStyle(s.theme.CellStyle(s.theme.Accent, s.theme.Panel)).
				SetSelectedTextStyle(s.theme.CellStyle(s.theme.Background, s.theme.Primary)).
				SetSelectable(false)
			schemas[tbl.Schema] = schemaNode
			root.AddChild(schemaNode)
		}
		ref := tableRef{Schema: tbl.Schema, Name: tbl.Name}
		schemaNode.AddChild(tview.NewTreeNode(tbl.Name).
			SetReference(ref).
			SetTextStyle(s.theme.CellStyle(s.theme.Text, s.theme.Panel)).
			SetSelectedTextStyle(s.theme.CellStyle(s.theme.Background, s.theme.Primary)).
			SetSelectable(true))
	}
	s.tree = tview.NewTreeView().SetRoot(root).SetCurrentNode(root)
	s.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		ref, ok := node.GetReference().(tableRef)
		if !ok {
			return
		}
		s.openTable(ref)
	})
	return nil
}

func (s *state) layout() tview.Primitive {
	s.header = tview.NewTextView().SetDynamicColors(true).SetTextColor(s.theme.TCell(s.theme.Text))
	s.header.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	s.header.SetText(fmt.Sprintf("[%s:%s:b] dbbie [::-] [%s]%s[::-]  [%s]theme=%s[::-]", s.theme.Background, s.theme.Primary, s.theme.Primary, s.kind, s.theme.Muted, s.theme.Name))
	s.status = tview.NewTextView().SetDynamicColors(true).SetTextColor(s.theme.TCell(s.theme.Muted))
	s.status.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	s.status.SetText(fmt.Sprintf("[%s]j/k[::-] move  [%s]enter[::-] open  [%s]:[::-] command/sql  [%s]/[::-] search  [%s]i[::-] query  [%s]q[::-] quit", s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Primary))
	s.grid = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, true).
		SetFixed(1, 0).
		SetSelectedStyle(s.theme.CellStyle(s.theme.Background, s.theme.Accent))
	s.query = tview.NewInputField().
		SetLabel("sql ").
		SetFieldWidth(0).
		SetFieldBackgroundColor(s.theme.TCell(s.theme.Panel)).
		SetFieldTextColor(s.theme.TCell(s.theme.Text)).
		SetLabelColor(s.theme.TCell(s.theme.Primary)).
		SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				s.runQuery(s.query.GetText())
			}
			s.app.SetFocus(s.grid)
		})
	s.command = tview.NewInputField().
		SetLabel(":").
		SetFieldWidth(0).
		SetFieldBackgroundColor(s.theme.TCell(s.theme.Panel)).
		SetFieldTextColor(s.theme.TCell(s.theme.Text)).
		SetLabelColor(s.theme.TCell(s.theme.Primary)).
		SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				s.runCommand(s.command.GetText())
			}
			s.command.SetText("")
			s.app.SetFocus(s.grid)
		})
	s.search = tview.NewInputField().
		SetLabel("/").
		SetFieldWidth(0).
		SetFieldBackgroundColor(s.theme.TCell(s.theme.Panel)).
		SetFieldTextColor(s.theme.TCell(s.theme.Text)).
		SetLabelColor(s.theme.TCell(s.theme.Primary)).
		SetChangedFunc(func(text string) {
			s.searchGrid(text)
		}).
		SetDoneFunc(func(key tcell.Key) {
			s.app.SetFocus(s.grid)
		})
	applyBoxTheme(s.tree, s.theme, "catalog")
	applyBoxTheme(s.grid, s.theme, "results")
	applyBoxTheme(s.query, s.theme, "query")
	applyBoxTheme(s.command, s.theme, "command")
	applyBoxTheme(s.search, s.theme, "search")
	s.tree.SetTitleAlign(tview.AlignLeft).SetBorderPadding(0, 0, 1, 1)
	s.grid.SetTitleAlign(tview.AlignLeft).SetBorderPadding(0, 0, 1, 1)
	s.query.SetTitleAlign(tview.AlignLeft).SetBorderPadding(0, 0, 1, 1)
	s.search.SetBorder(false).SetBorderPadding(0, 0, 1, 1)
	s.command.SetBorder(false).SetBorderPadding(0, 0, 1, 1)
	s.grid.SetCell(0, 0, tview.NewTableCell("Open a table or run a query").SetTextColor(s.theme.TCell(s.theme.Muted)).SetBackgroundColor(s.theme.TCell(s.theme.Panel)).SetExpansion(1))

	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.tree, 0, 1, true)
	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.query, 3, 0, false).
		AddItem(s.grid, 0, 1, false).
		AddItem(s.search, 1, 0, false).
		AddItem(s.command, 1, 0, false)
	body := tview.NewFlex().
		AddItem(left, 32, 0, true).
		AddItem(right, 0, 1, false)
	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.header, 1, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(s.status, 1, 0, false)
	return root
}

func applyBoxTheme(p interface {
	SetBorder(bool) *tview.Box
	SetBorderColor(tcell.Color) *tview.Box
	SetBackgroundColor(tcell.Color) *tview.Box
	SetTitle(string) *tview.Box
}, t theme.Theme, title string) {
	p.SetBorder(true).SetBorderColor(t.TCell(t.Border)).SetBackgroundColor(t.TCell(t.Panel)).SetTitle(title)
}

func (s *state) captureInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return event
	}
	if event.Key() == tcell.KeyTab {
		s.focusResults()
		return nil
	}
	focus := s.app.GetFocus()
	if focus == s.query || focus == s.command || focus == s.search {
		if event.Key() == tcell.KeyEsc {
			s.focusResults()
			return nil
		}
		return event
	}
	if event.Key() == tcell.KeyCtrlC {
		s.app.Stop()
		return nil
	}
	if event.Key() != tcell.KeyRune {
		return event
	}
	switch event.Rune() {
	case 'q':
		s.app.Stop()
		return nil
	case 'j':
		return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	case 'k':
		return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
	case 'h':
		return tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone)
	case 'l':
		return tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone)
	case ':':
		s.app.SetFocus(s.command)
		return nil
	case '/':
		s.app.SetFocus(s.search)
		s.status.SetText(fmt.Sprintf("[%s]search[::-] current results", s.theme.Primary))
		return nil
	case 'i':
		s.app.SetFocus(s.query)
		return nil
	case 'n':
		s.advanceSearch(1)
		return nil
	case 'N':
		s.advanceSearch(-1)
		return nil
	}
	return event
}

func (s *state) focusResults() {
	s.app.SetFocus(s.grid)
	s.status.SetText(fmt.Sprintf("[%s]results[::-] arrows/hjkl move  [%s]/[::-] filter  [%s]i[::-] query  [%s]:[::-] command", s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Primary))
}

func (s *state) openTable(ref tableRef) {
	sqlText := fmt.Sprintf("select * from %s.%s", quoteIdent(s.kind, ref.Schema), quoteIdent(s.kind, ref.Name))
	s.query.SetText(sqlText)
	s.runQuery(sqlText)
}

func (s *state) runInitialSQL(sqlText string) {
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return
	}
	s.query.SetText(sqlText)
	s.runQuery(sqlText)
}

func (s *state) runQuery(sqlText string) {
	s.status.SetText("[yellow]running query...")
	start := time.Now()
	result, err := query.Runner{DB: s.db}.Run(s.ctx, query.Options{
		SQL:      sqlText,
		Limit:    s.limit,
		Timeout:  15 * time.Second,
		ReadOnly: true,
	})
	if err != nil {
		s.status.SetText(fmt.Sprintf("[red]%s", err))
		return
	}
	s.renderResult(result)
	s.status.SetText(fmt.Sprintf("[green]%s rows=%d truncated=%t elapsed=%s", result.ID, result.RowCount, result.Truncated, time.Since(start).Round(time.Millisecond)))
}

func (s *state) renderResult(result query.Result) {
	s.lastResult = result
	s.renderGrid(result)
}

func (s *state) renderGrid(result query.Result) {
	s.grid.Clear()
	for col, c := range result.Columns {
		s.grid.SetCell(0, col, tview.NewTableCell(c.Name).
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
			s.grid.SetCell(rowIdx+1, colIdx, tview.NewTableCell(value).
				SetTextColor(s.theme.TCell(s.theme.Text)).
				SetBackgroundColor(s.theme.TCell(bg)).
				SetMaxWidth(columnMaxWidth(result.Columns[colIdx].Name)).
				SetExpansion(1))
		}
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

func (s *state) runCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "q", "quit":
		s.app.Stop()
	case "theme":
		s.status.SetText("[gray]theme switching requires restart in this slice")
	case "query":
		s.app.SetFocus(s.query)
	default:
		if commandLooksLikeSQL(fields[0]) {
			s.query.SetText(cmd)
			s.runQuery(cmd)
			return
		}
		s.status.SetText(fmt.Sprintf("[red]unknown command: %s", fields[0]))
	}
}

func commandLooksLikeSQL(first string) bool {
	switch strings.ToLower(first) {
	case "select", "with", "show", "describe", "desc", "explain",
		"insert", "update", "delete", "merge", "drop", "alter", "truncate", "create",
		"grant", "revoke", "call", "execute", "do", "copy", "vacuum", "analyze",
		"set", "reset", "replace", "load":
		return true
	default:
		return false
	}
}

func (s *state) searchGrid(text string) {
	s.searchText = text
	s.searchMatches = nil
	s.searchIndex = 0
	if text == "" {
		s.renderGrid(s.lastResult)
		s.status.SetText(fmt.Sprintf("[%s]j/k[::-] move  [%s]enter[::-] open  [%s]:[::-] command/sql  [%s]/[::-] search  [%s]i[::-] query  [%s]q[::-] quit", s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Primary))
		return
	}
	if len(s.lastResult.Columns) == 0 {
		s.status.SetText(fmt.Sprintf("[%s]no results to filter[::-]", s.theme.Danger))
		return
	}
	lower := strings.ToLower(text)
	filtered := query.Result{
		ID:        s.lastResult.ID,
		Columns:   s.lastResult.Columns,
		Rows:      make([][]any, 0, len(s.lastResult.Rows)),
		Truncated: s.lastResult.Truncated,
		Elapsed:   s.lastResult.Elapsed,
		Executed:  s.lastResult.Executed,
	}
	for _, row := range s.lastResult.Rows {
		matchesRow := false
		filteredRow := len(filtered.Rows) + 1
		for colIdx := range s.lastResult.Columns {
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
	s.status.SetText(fmt.Sprintf("[%s]no match[::-] %s  [%s]0/%d rows[::-]", s.theme.Danger, text, s.theme.Muted, len(s.lastResult.Rows)))
}

func (s *state) advanceSearch(delta int) {
	if len(s.searchMatches) == 0 {
		if s.searchText != "" {
			s.searchGrid(s.searchText)
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
	s.status.SetText(fmt.Sprintf("[%s]filter[::-] %d/%d rows  [%s]match %d/%d[::-] row=%d column=%d  [%s]%s[::-]  [%s]n/N[::-] next/prev", s.theme.Accent, s.grid.GetRowCount()-1, len(s.lastResult.Rows), s.theme.Primary, s.searchIndex+1, len(s.searchMatches), active.Row, active.Col+1, s.theme.Muted, s.searchText, s.theme.Primary))
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

func quoteIdent(kind db.Kind, ident string) string {
	if kind == db.MySQL {
		return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
