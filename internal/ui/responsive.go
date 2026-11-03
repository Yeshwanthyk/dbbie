package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Geometry is derived on every draw, including a terminal resize, without
// touching focus or tab authority. Widgets still own input and scrolling.
type shellFrame struct {
	*tview.Flex
	state *state
}

func (f *shellFrame) Draw(screen tcell.Screen) {
	s := f.state
	_, _, width, height := f.GetRect()
	side := min(s.sideWidth, max(20, width/3))
	s.body.ResizeItem(s.sidePane, side, 0)
	s.fitSidebar(max(1, side-4))
	queryWidth := max(1, width-side-5)
	sqlHeight := min(3, wrappedLines(s.current().sql, queryWidth))
	whyHeight := 0
	if s.current().origin != nil {
		whyHeight = min(2, wrappedLines(s.why.GetText(true), queryWidth))
	}
	s.queryPane.ResizeItem(s.why, whyHeight, 0)
	s.right.ResizeItem(s.queryPane, min(2+sqlHeight+whyHeight, max(3, height/3)), 0)
	s.headerWidth = width
	s.refreshHeaderWidth(width)
	f.Flex.Draw(screen)
}

type modeFrame struct {
	*tview.Flex
	state *state
}

func (f *modeFrame) Draw(screen tcell.Screen) {
	_, _, width, _ := f.GetRect()
	f.state.modeWidth = width
	f.state.renderMode(width)
	f.Flex.Draw(screen)
}

func wrappedLines(text string, width int) int {
	lines := 0
	for _, line := range strings.Split(text, "\n") {
		lines += max(1, (tview.TaggedStringWidth(tview.Escape(line))+width-1)/width)
	}
	return max(1, lines)
}
