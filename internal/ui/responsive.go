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
	side, gap := min(s.sideWidth, max(20, width/3)), 1
	if s.hideTables || s.zoomResults {
		side, gap = 0, 0
	} else {
		s.fitSidebar(max(1, side-4))
	}
	s.body.ResizeItem(s.sidePane, side, 0)
	s.body.ResizeItem(s.sideGap, gap, 0)
	queryWidth := max(1, width-side-gap-4)
	display := wrapSQL(layoutSQL(s.current().sql), queryWidth)
	if key := [2]string{display, s.theme.Name}; key != s.sqlRendered {
		s.sqlRendered = key
		s.sqlView.SetText(s.highlightSQL(display))
	}
	sqlHeight := min(8, wrappedLines(display, queryWidth))
	whyHeight := 0
	if s.why.GetText(true) != "" {
		whyHeight = 1
	}
	s.queryPane.ResizeItem(s.why, whyHeight, 0)
	queryHeight := min(2+sqlHeight+whyHeight, max(3, height/3))
	if s.zoomResults {
		queryHeight = 0
	}
	s.right.ResizeItem(s.queryPane, queryHeight, 0)
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
