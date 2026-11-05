package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// A wide terminal gets two columns; a narrow one gets a scrollable key list.
// The full-screen background obscures the shell so borders never collide.
type helpFrame struct {
	*tview.Flex
	column    *tview.Flex
	help      *tview.TextView
	state     *state
	textWidth int
	lines     int
}

type helpKey struct{ key, description string }

var helpLeft = []helpKey{
	{"", "ANYWHERE"},
	{"Ctrl-h / Ctrl-l", "tables / results"},
	{"gt / gT · 1–9", "next / prev / numbered tab"},
	{"t", "fuzzy table finder"},
	{"i", "replace SQL; Esc restores"},
	{"r", "re-run tab; read-only + logged"},
	{":", "command / SQL"},
	{":q / :qa", "close tab / quit"},
	{"q", "close tab; quit on last"},
	{"Ctrl-c", "cancel query; quit if idle"},
	{"? / Esc", "help / close overlay"},
	{"Tab", "focus results"},
	{"z / Z", "hide tables / results only"},
	{"", ""},
	{"", "TABLES"},
	{"j / k · ↑ / ↓", "move through tables"},
	{"gg / G", "first / last table"},
	{"/", "fuzzy filter names"},
	{"Enter / Esc", "keep / clear filter"},
	{"Enter / o", "open here / new tab"},
	{"K", "describe columns"},
}

var helpRight = []helpKey{
	{"", "RESULTS"},
	{"hjkl · arrows", "move by cell"},
	{"w / b · 0 / $", "next / prev / first / last"},
	{"gg / G", "first / last row"},
	{"Ctrl-d / Ctrl-u", "half-page down / up"},
	{"/ · n / N", "filter; next / prev match"},
	{"]a / [a", "next / prev agent tab"},
	{"Enter / Esc", "record view / back"},
	{"j / k", "prev / next record"},
	{"yy / Y", "copy cell / row JSON"},
	{"s / S", "sort ascending / descending"},
	{"f / Esc", "equal-cell filter / clear"},
	{"Ctrl-u", "clear SQL draft"},
	{"Enter / Esc", "run / cancel and restore"},
	{"", ""},
	{"", "SESSIONS / CONNECTIONS"},
	{":save NAME [title]", "save all tabs; new session"},
	{":add", "add tab to active session"},
	{":sessions", "fuzzy session picker"},
	{":conn", "saved connection picker"},
	{":theme", "selection needs restart"},
}

func (s *state) helpColumn(keys []helpKey, width int) []string {
	lines := make([]string, len(keys))
	for i, entry := range keys {
		if entry.key == "" {
			lines[i] = fmt.Sprintf("[%s::b]%s[-:-:-]", s.theme.Muted, entry.description)
			continue
		}
		key := entry.key + strings.Repeat(" ", max(1, 18-tview.TaggedStringWidth(tview.Escape(entry.key))))
		description := truncateCells(entry.description, max(1, width-tview.TaggedStringWidth(tview.Escape(key))))
		lines[i] = fmt.Sprintf("[%s]%s[-:-:-][%s]%s[-:-:-]", s.theme.Primary, tview.Escape(key), s.theme.Text, tview.Escape(description))
	}
	return lines
}

func (f *helpFrame) Draw(screen tcell.Screen) {
	_, _, width, height := f.GetRect()
	cardWidth := min(100, max(1, width-4))
	innerWidth := max(1, cardWidth-6)
	if innerWidth != f.textWidth {
		f.textWidth = innerWidth
		var lines []string
		if innerWidth >= 90 {
			colWidth := (innerWidth - 4) / 2
			left, right := f.state.helpColumn(helpLeft, colWidth), f.state.helpColumn(helpRight, colWidth)
			for i := 0; i < max(len(left), len(right)); i++ {
				l, r := "", ""
				if i < len(left) {
					l = left[i]
				}
				if i < len(right) {
					r = right[i]
				}
				lines = append(lines, l+strings.Repeat(" ", max(4, colWidth+4-tview.TaggedStringWidth(l)))+r)
			}
		} else {
			lines = append(f.state.helpColumn(helpLeft, innerWidth), "")
			lines = append(lines, f.state.helpColumn(helpRight, innerWidth)...)
		}
		f.lines = len(lines)
		f.help.SetText(strings.Join(lines, "\n"))
	}
	f.ResizeItem(f.column, cardWidth, 0)
	f.column.ResizeItem(f.help, min(f.lines+4, max(1, height-4)), 0)
	f.Flex.Draw(screen)
}

func (s *state) toggleHelp() {
	if s.helpOpen {
		s.helpOpen = false
		s.pages.RemovePage("help")
		s.app.SetFocus(s.helpReturn)
		return
	}
	s.helpReturn = s.app.GetFocus()
	s.helpOpen = true
	help := s.text().SetScrollable(true).SetWrap(false)
	s.pane(help.Box, "keys · Esc closes")
	help.SetBorderPadding(1, 1, 2, 2)
	s.bindFocus(help.Box, help.Box)
	column := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, 0, 1, false).AddItem(help, 23, 0, true).AddItem(nil, 0, 1, false)
	modal := &helpFrame{Flex: tview.NewFlex().AddItem(nil, 0, 1, false).AddItem(column, 100, 0, true).AddItem(nil, 0, 1, false), column: column, help: help, state: s}
	// Flex does not clear its background by default; explicitly make the
	// modal and its vertical margins opaque, not just the help text itself.
	modal.Box = tview.NewBox().SetBackgroundColor(s.theme.TCell(s.theme.Background))
	column.Box = tview.NewBox().SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.pages.AddPage("help", modal, true, true)
	s.app.SetFocus(help)
}
