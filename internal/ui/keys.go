package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

func (s *state) captureInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return nil
	}
	if event.Key() == tcell.KeyF24 {
		// A private redraw wake-up expires clipboard feedback on the UI loop.
		s.expireCopyMessage()
		return nil
	}
	s.expireCopyMessage()
	if s.quitting {
		return nil
	}
	if event.Key() == tcell.KeyCtrlC {
		s.cancelOrQuit()
		return nil
	}
	if s.helpOpen {
		if event.Key() == tcell.KeyEsc || (event.Key() == tcell.KeyRune && event.Rune() == '?') {
			s.toggleHelp()
			return nil
		}
		return event
	}
	if event.Key() == tcell.KeyCtrlH {
		s.focusTables()
		return nil
	}
	if event.Key() == tcell.KeyCtrlL || event.Key() == tcell.KeyTab {
		s.focusResults()
		return nil
	}
	focus := s.app.GetFocus()
	if focus == s.query || focus == s.command || focus == s.search || focus == s.tableFilter {
		if focus == s.query && event.Key() == tcell.KeyCtrlU {
			s.query.SetText("")
			return nil
		}
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyEnter {
			// Dispatch completion here as well as on the widgets so capture-only
			// tests exercise the same transition as the live event loop.
			switch focus {
			case s.query:
				s.finishQueryEdit(event.Key() == tcell.KeyEnter)
			case s.command:
				if event.Key() == tcell.KeyEnter {
					s.runCommand(s.command.GetText())
				}
				s.command.SetText("")
				if s.app.GetFocus() == s.command {
					s.focusResults()
				}
			case s.search:
				if event.Key() == tcell.KeyEsc {
					s.search.SetText("")
				}
				s.focusResults()
			case s.tableFilter:
				if event.Key() == tcell.KeyEsc {
					s.tableFilter.SetText("")
				}
				s.focusTables()
			}
			return nil
		}
		return event // Literal input, including q, t, digits and Vim motions.
	}
	if event.Key() == tcell.KeyEsc {
		s.pendingKey = 0
		if focus == s.recordView {
			s.closeRecord()
		} else if focus == s.grid {
			s.current().equalFilter = nil
			s.search.SetText("")
			s.searchGrid("")
		} else {
			s.tableFilter.SetText("")
		}
		return nil
	}
	if event.Key() == tcell.KeyCtrlD && focus == s.grid {
		s.moveGrid(halfPage(s.grid), 0)
		return nil
	}
	if event.Key() == tcell.KeyCtrlU && focus == s.grid {
		s.moveGrid(-halfPage(s.grid), 0)
		return nil
	}
	if event.Key() == tcell.KeyEnter && focus == s.sidebar {
		s.openSelectedTable(false)
		return nil
	}
	if event.Key() == tcell.KeyEnter && focus == s.grid {
		s.openRecord()
		return nil
	}
	if focus == s.recordView && (event.Key() == tcell.KeyDown || event.Key() == tcell.KeyUp) {
		delta := 1
		if event.Key() == tcell.KeyUp {
			delta = -1
		}
		s.moveGrid(delta, 0)
		return nil
	}
	if event.Key() != tcell.KeyRune {
		s.pendingKey = 0
		if focus == s.sidebar {
			switch event.Key() {
			case tcell.KeyDown:
				s.moveSidebar(1)
				return nil
			case tcell.KeyUp:
				s.moveSidebar(-1)
				return nil
			}
		}
		return event
	}
	key := event.Rune()
	if prefix := s.pendingKey; prefix != 0 {
		s.pendingKey = 0
		switch {
		case prefix == 'y' && key == 'y' && (focus == s.grid || focus == s.recordView):
			s.yank(false)
			return nil
		case prefix == 'g' && key == 't':
			s.switchTab((s.active + 1) % len(s.tabs))
			return nil
		case prefix == 'g' && key == 'T':
			s.switchTab((s.active + len(s.tabs) - 1) % len(s.tabs))
			return nil
		case prefix == 'g' && key == 'g':
			if focus == s.sidebar {
				s.sidebar.Select(0, 0)
			} else {
				_, col := s.grid.GetSelection()
				s.grid.Select(1, col)
			}
			return nil
		case prefix == ']' && key == 'a':
			s.agentTab(1)
			return nil
		case prefix == '[' && key == 'a':
			s.agentTab(-1)
			return nil
		}
	}
	switch key {
	case 'g', '[', ']':
		s.pendingKey = key
		return nil
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		s.switchTab(int(key - '1'))
		return nil
	case 'q':
		s.closeTab()
		return nil
	case 't':
		s.focusTables()
		s.tableFilter.SetText("")
		s.app.SetFocus(s.tableFilter)
		return nil
	case 'i':
		s.focusQuery()
		return nil
	case ':':
		s.command.SetText("")
		s.app.SetFocus(s.command)
		return nil
	case '?':
		s.toggleHelp()
		return nil
	case '/':
		if focus == s.sidebar {
			s.app.SetFocus(s.tableFilter)
		} else {
			s.search.SetText(s.current().rowFilter)
			s.app.SetFocus(s.search)
		}
		return nil
	}
	if focus == s.sidebar {
		switch key {
		case 'j':
			s.moveSidebar(1)
		case 'k':
			s.moveSidebar(-1)
		case 'G':
			s.sidebar.Select(max(0, len(s.visibleTables)-1), 0)
		case 'o':
			s.openSelectedTable(true)
		case 'K':
			s.describeSelectedTable()
		default:
			return event
		}
		return nil
	}
	if (focus == s.grid || focus == s.recordView) && s.resultKey(key) {
		return nil
	}
	if focus == s.recordView {
		switch key {
		case 'j':
			s.moveGrid(1, 0)
		case 'k':
			s.moveGrid(-1, 0)
		default:
			return event
		}
		return nil
	}
	if focus == s.grid {
		switch key {
		case 'h', 'b':
			s.moveGrid(0, -1)
		case 'j':
			s.moveGrid(1, 0)
		case 'k':
			s.moveGrid(-1, 0)
		case 'l', 'w':
			s.moveGrid(0, 1)
		case '0':
			row, _ := s.grid.GetSelection()
			s.grid.Select(row, 0)
		case '$':
			row, _ := s.grid.GetSelection()
			s.grid.Select(row, max(0, s.grid.GetColumnCount()-1))
		case 'G':
			_, col := s.grid.GetSelection()
			s.grid.Select(max(1, s.grid.GetRowCount()-1), col)
		case 'n':
			s.advanceSearch(1)
		case 'N':
			s.advanceSearch(-1)
		default:
			return event
		}
		return nil
	}
	return event
}

func (s *state) moveSidebar(delta int) {
	row, _ := s.sidebar.GetSelection()
	s.sidebar.Select(min(max(0, row+delta), max(0, len(s.visibleTables)-1)), 0)
}

func (s *state) moveGrid(rows, cols int) {
	if len(s.viewRows) == 0 || s.current().err != nil {
		return
	}
	row, col := s.grid.GetSelection()
	s.grid.Select(min(max(1, row+rows), max(1, s.grid.GetRowCount()-1)), min(max(0, col+cols), max(0, s.grid.GetColumnCount()-1)))
}

func halfPage(p interface{ GetInnerRect() (int, int, int, int) }) int {
	_, _, _, height := p.GetInnerRect()
	return max(1, height/2)
}

func (s *state) runCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "q", "quit":
		s.closeTab()
	case "qa", "qall":
		s.quit()
	case "theme":
		s.setMessage("theme switching requires restart")
	case "query":
		s.focusQuery()
	default:
		if commandLooksLikeSQL(fields[0]) {
			// Human edits must not replace an agent's recorded query.
			if s.current().origin != nil {
				s.newTab("query", cmd, nil)
			}
			s.query.SetText(cmd)
			s.runQuery(cmd)
			return
		}
		s.setMessage(fmt.Sprintf("unknown command: %s", fields[0]))
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
