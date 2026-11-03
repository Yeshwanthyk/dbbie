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
	s.modeInfo = s.text().SetWrap(false).SetTextAlign(tview.AlignRight)
	s.modeInfo.SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.header.SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.status.SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.header.SetWrap(false)
	s.status.SetWrap(false)
	s.sqlView.SetWrap(true).SetWordWrap(false)
	s.why.SetWrap(true)
	s.grid = tview.NewTable().SetBorders(false).SetSeparator('│').SetSelectable(true, true).SetFixed(1, 0).
		SetEvaluateAllRows(true).SetSelectedStyle(s.theme.CellStyle(s.theme.Background, s.theme.Primary))
	s.grid.SetBordersColor(s.theme.TCell(s.theme.Border))
	s.grid.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	s.grid.SetSelectionChangedFunc(s.selectionChanged)
	s.grid.SetSelectedFunc(func(_, _ int) { s.openRecord() })
	s.cellInfo = s.text().SetWrap(true)
	s.recordView = s.text().SetWrap(true).SetScrollable(true)
	s.resultPages = tview.NewPages().AddPage("grid", s.grid, true, true).AddPage("record", s.recordView, true, false)
	s.resultPane = &resultsFrame{Flex: tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.resultPages, 0, 1, true).AddItem(s.cellInfo, 2, 0, false), state: s}
	s.resultPane.Box = tview.NewBox()
	s.pane(s.resultPane.Box, "results")
	s.sidebar = tview.NewTable().SetBorders(false).SetSelectable(true, false).
		SetSelectedStyle(s.theme.CellStyle(s.theme.Text, s.theme.Selection))
	s.sidebar.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	s.query = s.input("").SetChangedFunc(func(text string) {
		s.current().sql = text
		s.sqlView.SetText(tview.Escape(text))
	}).SetDoneFunc(func(key tcell.Key) { s.finishQueryEdit(key == tcell.KeyEnter) })
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
	s.tableFilter.SetPlaceholder("filter tables")
	s.tableFilter.SetPlaceholderTextColor(s.theme.TCell(s.theme.Muted))
	s.sidePane = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.tableFilter, 1, 0, false).AddItem(s.sidebar, 0, 1, true)
	s.sidePane.Box = tview.NewBox()
	s.pane(s.sidePane.Box, "tables")
	s.queryPages = tview.NewPages().AddPage("sql", s.sqlView, true, true).AddPage("edit", s.query, true, false)
	s.queryPane = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.why, 0, 0, false).AddItem(s.queryPages, 0, 1, true)
	s.queryPane.Box = tview.NewBox()
	s.pane(s.queryPane.Box, "query")
	s.prompts = tview.NewFlex()
	s.modeLine = tview.NewFlex().AddItem(s.status, 0, 1, false).AddItem(s.prompts, 0, 0, false)
	s.right = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.queryPane, 3, 0, false).AddItem(s.resultPane, 0, 1, true)
	s.body = tview.NewFlex().AddItem(s.sidePane, 32, 0, true).AddItem(nil, 1, 0, false).AddItem(s.right, 0, 1, false)
	mode := &modeFrame{Flex: s.modeLine, state: s}
	root := &shellFrame{Flex: tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.header, 1, 0, false).AddItem(s.body, 0, 1, true).AddItem(mode, 1, 0, false), state: s}
	s.pages = tview.NewPages().AddPage("main", root, true, true)
	s.bindFocus(s.sidebar.Box, s.sidePane.Box)
	s.bindFocus(s.tableFilter.Box, s.sidePane.Box)
	s.bindFocus(s.grid.Box, s.resultPane.Box)
	s.bindFocus(s.recordView.Box, s.resultPane.Box)
	s.bindFocus(s.search.Box, s.resultPane.Box)
	s.bindFocus(s.query.Box, s.queryPane.Box)
	s.bindFocus(s.sqlView.Box, s.queryPane.Box)
	s.bindFocus(s.why.Box, s.queryPane.Box)
	s.command.SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.command.SetFieldBackgroundColor(s.theme.TCell(s.theme.Background))
	s.search.SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.search.SetFieldBackgroundColor(s.theme.TCell(s.theme.Background))
	s.search.SetLabelColor(s.theme.TCell(s.theme.Primary))
	s.command.SetLabelColor(s.theme.TCell(s.theme.Primary))
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
	if s.current().origin != nil {
		s.newTab("query", s.current().sql, nil)
	}
	s.editOriginal, s.editing = s.query.GetText(), true
	// tview InputField has no select-all. Start a replacement draft, but keep
	// the original intact for Esc so a new query never appends to the old SQL.
	s.query.SetText("")
	s.setMessage("")
	s.queryPages.SwitchToPage("edit")
	s.app.SetFocus(s.query)
}

func (s *state) finishQueryEdit(run bool) {
	if !run && s.editing {
		s.query.SetText(s.editOriginal)
	}
	s.editing = false
	if run {
		s.runQuery(s.query.GetText())
	}
	s.focusResults()
}

func (s *state) setMessage(message string) { s.message = message; s.refreshMode() }

func (s *state) refreshMode() {
	if s.status == nil {
		return
	}
	focus := s.app.GetFocus()
	mode, color, hints := "NORMAL", s.theme.Success, "hjkl move · Enter record · / filter · i SQL · gt tab · yy/Y copy · s/S sort · ? help"
	if s.current().rowFilter != "" || s.current().equalFilter != nil {
		hints = "n/N match · Esc clear · / filter · Enter record · i SQL · ? help"
	}
	if s.current().record {
		mode, color, hints = "RECORD", s.theme.Agent, "j/k prev/next row · Esc back · yy cell · Y row JSON · Ctrl-d/u scroll"
	}
	s.modePrompt = nil
	switch focus {
	case s.sidebar:
		mode, color, hints = "TABLES", s.theme.Primary, "j/k move · Enter open · o new tab · / filter · K columns · Ctrl-l results"
	case s.tableFilter:
		mode, color, hints = "FILTER", s.theme.Accent, "Enter keep · Esc clear"
	case s.search:
		mode, color, hints = "FILTER", s.theme.Accent, "Enter keep · Esc clear"
		s.modePrompt = s.search
	case s.query:
		mode, color, hints = "INSERT", s.theme.Accent, "Ctrl-u clear · Enter run · Esc cancel"
	case s.command:
		mode, color, hints = "COMMAND", s.theme.Agent, "Enter run · Esc cancel"
		s.modePrompt = s.command
	}
	if s.current().origin != nil {
		mode += " ◆ AGENT"
		color = s.theme.Agent
	}
	s.modeTag, s.modeColor, s.modeHints = mode, color, hints
	width := s.modeWidth
	if width == 0 {
		width = 120
	}
	s.renderMode(width)
}

// Draw-time formatting uses cached focus state. Application holds its mutex
// while drawing, so calling GetFocus from Draw would deadlock the event loop.
func (s *state) renderMode(width int) {
	s.prompts.Clear()
	s.modeLine.ResizeItem(s.status, 0, 1)
	s.modeLine.ResizeItem(s.prompts, 0, 0)
	pill := fmt.Sprintf("[%s:%s:b] %s [-:-:-]", s.theme.Background, s.modeColor, s.modeTag)
	info := s.compactStatus()
	message := s.message
	if s.modePrompt == s.search {
		// The live input already shows the filter. Do not duplicate it and
		// squeeze Enter/Esc hints out of smaller terminals.
		message = ""
	}
	if t := s.current(); t.cancel != nil {
		frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
		elapsed := time.Since(t.started)
		message = fmt.Sprintf("%c running… %s · Ctrl-c cancel", frames[int(elapsed.Milliseconds()/100)%len(frames)], elapsed.Round(100*time.Millisecond))
	}
	if s.agentNotice != "" {
		message = s.agentNotice
	}
	budget := max(0, width-tview.TaggedStringWidth(pill)-tview.TaggedStringWidth(info)-2)
	if s.modePrompt != nil {
		budget = max(0, budget-min(32, max(12, width/3)))
	}
	message = truncateCells(displayValue(message), budget)
	used := tview.TaggedStringWidth(tview.Escape(message))
	var kept []string
	for _, part := range strings.Split(s.modeHints, " · ") {
		w := tview.TaggedStringWidth(tview.Escape(part))
		sep := 0
		if used > 0 {
			sep = 3
		}
		if used+sep+w > budget {
			break
		}
		kept = append(kept, part)
		used += sep + w
	}
	hints := s.formatHints(strings.Join(kept, " · "))
	if message != "" {
		if hints != "" {
			hints = tview.Escape(message) + " · " + hints
		} else {
			hints = tview.Escape(message)
		}
	}
	out := pill + " [" + s.theme.Muted + "]" + hints + "[-:-:-]"
	if s.modePrompt != nil {
		// Keep typing visible between the mode pill and persistent right status.
		leftWidth := tview.TaggedStringWidth(out) + 1
		promptWidth := max(1, width-leftWidth-tview.TaggedStringWidth(info)-1)
		s.modeLine.ResizeItem(s.status, leftWidth, 0)
		s.modeLine.ResizeItem(s.prompts, 0, 1)
		s.prompts.AddItem(s.modePrompt, promptWidth, 0, true)
		if info != "" {
			s.modeInfo.SetText("[" + s.theme.Muted + "]" + info + "[-:-:-]")
			s.prompts.AddItem(s.modeInfo, 0, 1, false)
		}
	} else if info != "" {
		gap := max(1, width-tview.TaggedStringWidth(out)-tview.TaggedStringWidth(info))
		out += strings.Repeat(" ", gap) + "[" + s.theme.Muted + "]" + info + "[-:-:-]"
	}
	s.status.SetText(out)
}

func (s *state) compactStatus() string {
	t := s.current()
	if t.err != nil {
		return "error"
	}
	if len(t.result.Columns) == 0 {
		return ""
	}
	rows := fmt.Sprintf("%d rows", len(s.viewRows))
	if t.rowFilter != "" || t.equalFilter != nil {
		rows = fmt.Sprintf("%d/%d rows", len(s.viewRows), len(t.result.Rows))
	}
	if t.result.Truncated {
		rows += " · truncated"
	}
	if elapsed, err := time.ParseDuration(t.result.Elapsed); err == nil {
		rows += " · " + elapsed.Round(time.Millisecond).String()
	}
	return rows
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
