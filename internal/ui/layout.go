package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (s *state) input(label string) *tview.InputField {
	field := tview.NewInputField().SetLabel(label).SetFieldWidth(0).
		SetFieldBackgroundColor(s.theme.TCell(s.theme.Panel)).SetFieldTextColor(s.theme.TCell(s.theme.Text)).SetLabelColor(s.theme.TCell(s.theme.Accent))
	field.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	return field
}

func (s *state) text() *tview.TextView {
	view := tview.NewTextView().SetDynamicColors(true).SetTextColor(s.theme.TCell(s.theme.Text))
	view.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	return view
}

func (s *state) pane(box *tview.Box, title string) {
	box.SetBorder(true).SetBorderColor(s.theme.TCell(s.theme.Border)).SetBackgroundColor(s.theme.TCell(s.theme.Panel)).
		SetTitle(" "+title+" ").SetTitleColor(s.theme.TCell(s.theme.Muted)).SetTitleAlign(tview.AlignLeft).SetBorderPadding(0, 0, 1, 1)
}

func (s *state) bindFocus(box *tview.Box, pane *tview.Box) {
	box.SetFocusFunc(func() { pane.SetBorderColor(s.theme.TCell(s.theme.Primary)); s.refreshMode() })
	// Blur runs under Application's lock; never read app.GetFocus here.
	box.SetBlurFunc(func() { pane.SetBorderColor(s.theme.TCell(s.theme.Border)) })
}

func (s *state) layout() tview.Primitive {
	s.header, s.status, s.why, s.sqlView = s.text(), s.text(), s.text(), s.text()
	s.header.SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.status.SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.sqlView.SetWrap(true)
	s.grid = tview.NewTable().SetBorders(false).SetSeparator('│').SetSelectable(true, true).SetFixed(1, 0).
		SetEvaluateAllRows(true).SetSelectedStyle(s.theme.CellStyle(s.theme.Background, s.theme.Primary))
	s.grid.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	s.grid.SetSelectionChangedFunc(s.selectionChanged)
	s.grid.SetSelectedFunc(func(_, _ int) { s.openRecord() })
	s.cellInfo = s.text().SetWrap(true)
	s.recordView = s.text().SetWrap(true).SetScrollable(true)
	s.resultPages = tview.NewPages().AddPage("grid", s.grid, true, true).AddPage("record", s.recordView, true, false)
	s.resultPane = &resultsFrame{Flex: tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.resultPages, 0, 1, true).AddItem(s.cellInfo, 2, 0, false), state: s}
	s.pane(s.resultPane.Box, "results")
	s.sidebar = tview.NewTable().SetBorders(false).SetSelectable(true, false).
		SetSelectedStyle(s.theme.CellStyle(s.theme.Background, s.theme.Primary))
	s.sidebar.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	s.query = s.input("").SetChangedFunc(func(text string) {
		s.current().sql = text
		s.sqlView.SetText(tview.Escape(text))
	}).SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			s.runQuery(s.query.GetText())
		}
		s.focusResults()
	})
	s.command = s.input(":").SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			s.runCommand(s.command.GetText())
		}
		s.command.SetText("")
		if s.app.GetFocus() == s.command {
			s.focusResults()
		}
	})
	s.search = s.input("/ ").SetChangedFunc(s.searchGrid).SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEsc {
			s.search.SetText("")
		}
		s.focusResults()
	})
	s.tableFilter = s.input("/ ").SetChangedFunc(func(text string) { s.filterTables(text) }).SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEsc {
			s.tableFilter.SetText("")
		}
		s.focusTables()
	})
	s.sidePane = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.tableFilter, 1, 0, false).AddItem(s.sidebar, 0, 1, true)
	s.pane(s.sidePane.Box, "tables")
	s.queryPages = tview.NewPages().AddPage("sql", s.sqlView, true, true).AddPage("edit", s.query, true, false)
	s.queryPane = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.why, 0, 0, false).AddItem(s.queryPages, 0, 1, true)
	s.pane(s.queryPane.Box, "query")
	s.prompts = tview.NewFlex()
	s.modeLine = tview.NewFlex().AddItem(s.status, 0, 1, false).AddItem(s.prompts, 0, 0, false)
	right := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.queryPane, 6, 0, false).AddItem(s.resultPane, 0, 1, true)
	s.body = tview.NewFlex().AddItem(s.sidePane, 32, 0, true).AddItem(right, 0, 1, false)
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.header, 1, 0, false).AddItem(s.body, 0, 1, true).AddItem(s.modeLine, 1, 0, false)
	s.pages = tview.NewPages().AddPage("main", root, true, true)
	s.bindFocus(s.sidebar.Box, s.sidePane.Box)
	s.bindFocus(s.tableFilter.Box, s.sidePane.Box)
	s.bindFocus(s.grid.Box, s.resultPane.Box)
	s.bindFocus(s.recordView.Box, s.resultPane.Box)
	s.bindFocus(s.search.Box, s.resultPane.Box)
	s.bindFocus(s.query.Box, s.queryPane.Box)
	s.bindFocus(s.sqlView.Box, s.queryPane.Box)
	s.bindFocus(s.why.Box, s.queryPane.Box)
	s.command.SetFocusFunc(s.refreshMode)
	s.filterTables("")
	s.showTab()
	return s.pages
}

func (s *state) focusResults() {
	s.pendingKey = 0
	s.queryPages.SwitchToPage("sql")
	if s.current().record {
		s.app.SetFocus(s.recordView)
	} else {
		s.app.SetFocus(s.grid)
	}
}

func (s *state) focusTables() {
	s.pendingKey = 0
	s.queryPages.SwitchToPage("sql")
	s.app.SetFocus(s.sidebar)
}

func (s *state) focusQuery() {
	s.queryPages.SwitchToPage("edit")
	s.app.SetFocus(s.query)
}

func (s *state) setMessage(message string) { s.message = message; s.refreshMode() }

func (s *state) refreshMode() {
	if s.status == nil {
		return
	}
	focus := s.app.GetFocus()
	mode, color, hints := "NORMAL", s.theme.Success, "hjkl move · Enter record · yy/Y copy · s/S sort · f equal · / filter · ? help"
	if s.current().record {
		mode, color, hints = "RECORD", s.theme.Agent, "j/k prev/next row · Esc back · yy cell · Y row JSON · Ctrl-d/u scroll"
	}
	s.modeLine.ResizeItem(s.status, 0, 1)
	s.modeLine.ResizeItem(s.prompts, 0, 0)
	s.prompts.Clear()
	switch focus {
	case s.sidebar:
		mode, color, hints = "TABLES", s.theme.Primary, "j/k move · Enter open · o new tab · K columns · / filter · Ctrl-l results"
	case s.tableFilter:
		mode, color, hints = "FILTER", s.theme.Accent, "type to filter tables · Enter keep · Esc clear"
	case s.search:
		mode, color, hints = "FILTER", s.theme.Accent, ""
		s.modeLine.ResizeItem(s.status, 10, 0)
		s.modeLine.ResizeItem(s.prompts, 0, 1)
		s.prompts.AddItem(s.search, 0, 1, true)
	case s.query:
		mode, color, hints = "INSERT", s.theme.Accent, "edit SQL · Enter run · Esc leave"
	case s.command:
		mode, color, hints = "COMMAND", s.theme.Agent, ""
		s.modeLine.ResizeItem(s.status, 11, 0)
		s.modeLine.ResizeItem(s.prompts, 0, 1)
		s.prompts.AddItem(s.command, 0, 1, true)
	}
	message := s.message
	if t := s.current(); t.cancel != nil {
		frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
		elapsed := time.Since(t.started)
		message = fmt.Sprintf("%c running… %s · Ctrl-c cancel", frames[int(elapsed.Milliseconds()/100)%len(frames)], elapsed.Round(100*time.Millisecond))
	}
	hints = s.formatHints(hints)
	if message != "" {
		hints = tview.Escape(message) + " · " + hints
	}
	s.status.SetText(fmt.Sprintf("[%s:%s:b] %s [::-] [%s]%s[::-]", s.theme.Background, color, mode, s.theme.Muted, hints))
}

func (s *state) formatHints(hints string) string {
	parts := strings.Split(hints, " · ")
	for i, part := range parts {
		key, rest, ok := strings.Cut(part, " ")
		if ok {
			parts[i] = fmt.Sprintf("[%s]%s[%s] %s", s.theme.Primary, tview.Escape(key), s.theme.Muted, tview.Escape(rest))
		}
	}
	return strings.Join(parts, " · ")
}
