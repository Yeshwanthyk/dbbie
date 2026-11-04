package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHelpFitsShortTerminalAndCanScrollToLastKey(t *testing.T) {
	s := newTestState(t, nil)
	typeKeys(s, "?")
	frame := s.pages.GetPage("help").(*helpFrame)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(80, 24)
	s.pages.SetRect(0, 0, 80, 24)
	s.pages.Draw(screen)
	_, _, width, height := frame.help.GetRect()
	if width > 80 || height > 24 {
		t.Fatalf("help size=%dx%d", width, height)
	}
	frame.help.ScrollToEnd()
	s.pages.Draw(screen)
	var text strings.Builder
	for y := 0; y < 24; y++ {
		for x := 0; x < 80; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			text.WriteRune(r)
		}
	}
	if !strings.Contains(text.String(), ":theme") {
		t.Fatal("last help key cannot be reached on a short terminal")
	}
}
