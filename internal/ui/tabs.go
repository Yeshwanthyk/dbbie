package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

// Every tab has the same shape; an origin makes it an agent tab, not a second
// tab kind. Results stay attached to their tab even when another tab is active.
type tab struct {
	title       string
	sql         string
	origin      *qlog.Entry
	sessionName string
	itemN       int
	sessionConn string
	why         string
	logID       string
	logConn     string
	notice      string
	result      query.Result
	err         error
	cursor      gridPos
	rowFilter   string
	equalFilter *cellFilter
	sorted      bool
	sortColumn  int
	sortDesc    bool
	record      bool
	cancel      context.CancelFunc
	started     time.Time
	runID       uint64
}

func (s *state) current() *tab { return s.tabs[s.active] }

func (s *state) saveTab() {
	if !s.current().record {
		s.current().cursor.Row, s.current().cursor.Col = s.grid.GetSelection()
	}
	s.current().sql = s.query.GetText()
}

func (s *state) newTab(title, sql string, from *qlog.Entry) {
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
	s.sqlView.SetText(s.renderSQL(t.sql))
	s.sqlRendered = [2]string{}
	s.queryPane.SetTitle(s.queryTitle(t))
	why := s.queryMeta(t)
	s.why.SetText(why)
	whyHeight := 0
	if why != "" {
		whyHeight = 1
	}
	s.queryPane.ResizeItem(s.why, whyHeight, 0)
	s.searchMatches = nil
	s.searchIndex = 0
	cursor := t.cursor
	s.searchGrid(t.rowFilter)
	if len(s.searchMatches) > 0 {
		for i, match := range s.searchMatches {
			if match == cursor {
				s.searchIndex = i
				s.applySearchMatch()
				break
			}
		}
	}
	s.grid.Select(min(max(1, cursor.Row), max(1, len(s.viewRows))), cursor.Col)
	if t.record {
		s.renderRecord()
		s.resultPages.SwitchToPage("record")
	} else {
		s.resultPages.SwitchToPage("grid")
	}
	if s.app.GetFocus() == s.grid || s.app.GetFocus() == s.recordView {
		s.focusResults()
	}
	s.refreshCell()
	if len(t.result.Columns) == 0 && t.err == nil {
		notice := t.notice
		if notice == "" {
			notice = "Open a table or run a query"
		}
		s.gridView.empty = notice
	}
	s.message = ""
	if t.err != nil {
		s.message = t.err.Error()
	}
	s.refreshHeader()
	s.refreshMode()
}

func (s *state) refreshHeader() {
	width := s.headerWidth
	if width == 0 {
		width = 120
	}
	s.refreshHeaderWidth(width)
}

func (s *state) refreshHeaderWidth(width int) {
	var out strings.Builder
	fmt.Fprintf(&out, "[%s:%s:b] dbbie [-:-:-] [%s]%s · %s [%s]●[-:-:-] ", s.theme.Background, s.theme.Primary, s.theme.Muted, tview.Escape(displayValue(s.connection)), s.kind, s.theme.Success)
	available := width - tview.TaggedStringWidth(out.String())
	// Keep the active tab in the viewport instead of clipping the bar at its
	// right edge. Short titles free space for neighboring tabs.
	first := 0
	for i := 0; i < s.active; i++ {
		needed := 0
		for j := first; j <= s.active; j++ {
			needed += min(28, tview.TaggedStringWidth(tview.Escape(displayValue(s.tabs[j].title)))+7)
		}
		if needed <= available {
			break
		}
		first++
	}
	if first > 0 {
		out.WriteString("[" + s.theme.Muted + "]‹ [-:-:-]")
		available -= 2
	}
	for i := first; i < len(s.tabs); i++ {
		t := s.tabs[i]
		fg, bg, attr := s.theme.Muted, s.theme.Background, "BU"
		if i == s.active {
			fg, bg, attr = s.theme.Text, s.theme.Selection, "bu"
		}
		marker := ""
		if t.origin != nil {
			fg = s.theme.Agent
			marker = "◆ "
		}
		title := t.title
		if t.origin != nil && t.origin.Session != "" && t.sessionName == "" {
			title = t.origin.Session + " · " + title
		}
		titleWidth := min(22, max(1, available-4-tview.TaggedStringWidth(marker)))
		label := fmt.Sprintf(" %d %s%s ", i+1, marker, truncateCells(displayValue(title), titleWidth))
		if tview.TaggedStringWidth(tview.Escape(label)) > available {
			break
		}
		// Underline is a separate tcell style property: '-' resets the
		// attribute mask but needs an explicit U to clear this decoration.
		fmt.Fprintf(&out, "[%s:%s:%s]%s[-:-:BU]", fg, bg, attr, tview.Escape(label))
		available -= tview.TaggedStringWidth(tview.Escape(label))
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

// queryTitle names what the tab is: who ran it and why, then its log handle.
func (s *state) queryTitle(t *tab) string {
	if t.origin == nil {
		if t.title != "" && t.title != "query" {
			return " query · " + tview.Escape(t.title) + " "
		}
		return " query "
	}
	parts := []string{"◆ " + t.origin.Actor}
	if t.origin.Title != "" {
		parts = append(parts, t.origin.Title)
	}
	ts := t.origin.TS.Local()
	stamp := ts.Format("15:04")
	if y, m, d := ts.Date(); y != time.Now().Year() || m != time.Now().Month() || d != time.Now().Day() {
		stamp = ts.Format("2006-01-02 15:04")
	}
	parts = append(parts, t.origin.ID+" "+stamp)
	return " " + tview.Escape(strings.Join(parts, " · ")) + " "
}

// queryMeta is the line above the SQL: the tables it touches, then session
// context and the agent's stated reason.
func (s *state) queryMeta(t *tab) string {
	var parts []string
	if tables := queryTables(t.sql); len(tables) > 0 {
		parts = append(parts, fmt.Sprintf("[%s::b]▤ %s[-:-:-]", s.theme.Primary, tview.Escape(strings.Join(tables, ", "))))
	}
	session, why := t.sessionName, t.why
	if session == "" && t.origin != nil {
		session = t.origin.Session
	}
	if t.origin != nil && why == "" {
		why = t.origin.Why
	}
	if session != "" {
		label := "◇ " + session
		if t.itemN > 0 {
			label += fmt.Sprintf(" #%d", t.itemN)
		}
		parts = append(parts, fmt.Sprintf("[%s]%s[-:-:-]", s.theme.Agent, tview.Escape(label)))
	}
	if why != "" {
		parts = append(parts, fmt.Sprintf("[%s]%s[-:-:-]", s.theme.Muted, tview.Escape(why)))
	}
	if t.origin != nil {
		s.agentNotice = ""
	}
	return strings.Join(parts, "   ")
}
