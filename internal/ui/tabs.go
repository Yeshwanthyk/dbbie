package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

// TODO(slice D): replace *origin with *qlog.Entry once slice A lands. Do not
// introduce a competing query-log package in the shell slice.
type origin struct{ ID, Why, Actor string }

// Every tab has the same shape; an origin makes it an agent tab, not a second
// tab kind. Results stay attached to their tab even when another tab is active.
type tab struct {
	title     string
	sql       string
	origin    *origin
	result    query.Result
	err       error
	cursor    gridPos
	rowFilter string
	cancel    context.CancelFunc
	started   time.Time
	runID     uint64
}

func (s *state) current() *tab { return s.tabs[s.active] }

func (s *state) saveTab() {
	s.current().cursor.Row, s.current().cursor.Col = s.grid.GetSelection()
	s.current().sql = s.query.GetText()
}

func (s *state) newTab(title, sql string, from *origin) {
	s.saveTab()
	s.tabs = append(s.tabs, &tab{title: title, sql: sql, origin: from})
	s.active = len(s.tabs) - 1
	s.showTab()
}

func (s *state) switchTab(index int) {
	if index < 0 || index >= len(s.tabs) || index == s.active {
		return
	}
	s.saveTab()
	s.active = index
	s.showTab()
}

func (s *state) closeTab() {
	if len(s.tabs) == 1 {
		s.quit()
		return
	}
	if cancel := s.current().cancel; cancel != nil {
		cancel()
	}
	s.tabs = append(s.tabs[:s.active], s.tabs[s.active+1:]...)
	if s.active == len(s.tabs) {
		s.active--
	}
	s.showTab()
}

func (s *state) showTab() {
	t := s.current()
	s.query.SetText(t.sql)
	s.sqlView.SetText(tview.Escape(t.sql))
	why := ""
	if t.origin != nil {
		why = fmt.Sprintf("[%s]%s · %s[::-]", s.theme.Agent, tview.Escape(t.origin.Actor), tview.Escape(t.origin.Why))
	}
	s.why.SetText(why)
	whyHeight := 0
	if t.origin != nil {
		whyHeight = 1
	}
	s.queryPane.ResizeItem(s.why, whyHeight, 0)
	s.searchMatches = nil
	s.searchIndex = 0
	s.searchGrid(t.rowFilter)
	if len(s.searchMatches) > 0 {
		for i, match := range s.searchMatches {
			if match == t.cursor {
				s.searchIndex = i
				s.applySearchMatch()
				break
			}
		}
	}
	s.grid.Select(t.cursor.Row, t.cursor.Col)
	if len(t.result.Columns) == 0 && t.err == nil {
		s.grid.Clear()
		s.grid.SetCell(0, 0, tview.NewTableCell("Open a table or run a query").SetSelectable(false).SetTextColor(s.theme.TCell(s.theme.Muted)))
	}
	s.message = ""
	if t.err != nil {
		s.message = t.err.Error()
	}
	s.refreshHeader()
	s.refreshMode()
}

func (s *state) refreshHeader() {
	var out strings.Builder
	fmt.Fprintf(&out, "[%s:%s:b] dbbie [::-] [%s]%s · %s [%s]●[::-] ", s.theme.Background, s.theme.Primary, s.theme.Muted, tview.Escape(s.connection), s.kind, s.theme.Success)
	for i, t := range s.tabs {
		fg, bg := s.theme.Muted, s.theme.Background
		if i == s.active {
			fg, bg = s.theme.Text, s.theme.Panel
		}
		marker := ""
		if t.origin != nil {
			fg = s.theme.Agent
			marker = "◆ "
		}
		attr := "-"
		if i == s.active {
			attr = "u"
		}
		fmt.Fprintf(&out, "[%s:%s:%s] %d %s%s [::-]", fg, bg, attr, i+1, marker, tview.Escape(t.title))
	}
	s.header.SetText(out.String())
}

func (s *state) agentTab(delta int) {
	for offset := 1; offset <= len(s.tabs); offset++ {
		i := (s.active + delta*offset + len(s.tabs)) % len(s.tabs)
		if s.tabs[i].origin != nil {
			s.switchTab(i)
			return
		}
	}
}
