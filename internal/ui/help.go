package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Keep help centered but bounded by the terminal so short terminals can scroll
// through the whole keymap rather than clipping its last lines off screen.
type helpFrame struct {
	*tview.Flex
	column *tview.Flex
	help   *tview.TextView
}

func (f *helpFrame) Draw(screen tcell.Screen) {
	_, _, width, height := f.GetRect()
	f.ResizeItem(f.column, min(82, max(1, width-2)), 0)
	f.column.ResizeItem(f.help, min(38, max(1, height-2)), 0)
	f.Flex.Draw(screen)
}

func (s *state) toggleHelp() {
	if s.helpOpen {
		s.helpOpen = false
		// Pages delegates focus to the main layout when the modal is removed;
		// restore the original pane after that delegation.
		s.pages.RemovePage("help")
		s.app.SetFocus(s.helpReturn)
		return
	}
	s.helpReturn = s.app.GetFocus()
	s.helpOpen = true
	help := s.text().SetScrollable(true).SetWrap(true)
	s.pane(help.Box, "keys · Esc closes")
	s.bindFocus(help.Box, help.Box)
	help.SetText(fmt.Sprintf(`[%s::b]ANYWHERE[::-]
 Ctrl-h / Ctrl-l    tables / results
 gt / gT · 1–9      next / previous / numbered tab
 t                  fuzzy table finder
 i                  edit SQL · Enter run · Esc leave
 :                  command / SQL · :q close · :qa quit
 q                  close tab; quit on last tab (normal input only)
 Ctrl-c             cancel query; quit when idle
 ? · Esc            toggle help / close overlay
 Tab                focus results

[%s::b]TABLES[::-]
 j / k · ↑ / ↓      move through tables
 gg / G             first / last table
 /                  fuzzy filter · Enter keep · Esc clear
 Enter / o          open here / new tab (agent tabs preserved)
 K                  describe columns in new tab

[%s::b]RESULTS[::-]
 h j k l · arrows   move by cell
 w / b · 0 / $      next / previous · first / last column
 gg / G             first / last row
 Ctrl-d / Ctrl-u    half-page down / up
 / · n / N          row filter · next / previous match
 ]a / [a            next / previous agent tab

[%s]NEXT SLICE (C2) — not active yet[::-]
 Enter · Esc        record view / back
 yy / Y             copy cell / row JSON (OSC52)
 s / S              sort ascending / descending
 f                  re-query where column = value

 :theme             theme selection requires restart
`, s.theme.Primary, s.theme.Primary, s.theme.Primary, s.theme.Muted))
	column := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, 0, 1, false).AddItem(help, 38, 0, true).AddItem(nil, 0, 1, false)
	modal := &helpFrame{Flex: tview.NewFlex().AddItem(nil, 0, 1, false).AddItem(column, 82, 0, true).AddItem(nil, 0, 1, false), column: column, help: help}
	s.pages.AddPage("help", modal, true, true)
	s.app.SetFocus(help)
}
