package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
)

func drawShell(t *testing.T, s *state) (tcell.SimulationScreen, string) {
	t.Helper()
	ApplyTViewTheme(s.theme)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 45)
	s.pages.SetRect(0, 0, 120, 45)
	s.pages.Draw(screen)
	screen.Show()
	var text strings.Builder
	for y := 0; y < 45; y++ {
		for x := 0; x < 120; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			text.WriteRune(r)
		}
		text.WriteByte('\n')
	}
	return screen, text.String()
}

func TestLongTableNameDoesNotHideCounts(t *testing.T) {
	s := newTestState(t, nil)
	s.tables = []catalog.Table{{Schema: "public", Name: strings.Repeat("very_long_table_name", 5), EstimatedRows: 2184}, {Schema: "public", Name: "empty_table", EstimatedRows: 0}}
	s.filterTables("")
	s.focusTables()
	screen, text := drawShell(t, s)
	if !strings.Contains(text, "2.2k") || !strings.Contains(text, "empty") {
		t.Fatalf("counts clipped from live layout:\n%s", text)
	}
	_, _, style, _ := screen.GetContent(0, 1)
	fg, _, _ := style.Decompose()
	if fg != s.theme.TCell(s.theme.Primary) {
		t.Fatal("focused pane border not Primary")
	}
	if strings.ContainsAny(text, "╔╗╚╝║═") {
		t.Fatal("double focus border remains")
	}
	if tview.Borders.TopLeftFocus != '╭' {
		t.Fatal("focus corner not rounded")
	}
}

func TestAgentWhyAndSQLRenderAboveResults(t *testing.T) {
	s := newTestState(t, nil)
	s.current().origin = &origin{ID: "q_42", Actor: "agent", Why: "check empty tables"}
	s.current().sql = "select 42"
	s.showTab()
	_, text := drawShell(t, s)
	why, sql, results := strings.Index(text, "check empty tables"), strings.Index(text, "select 42"), strings.Index(text, "results")
	if why < 0 || sql < why || results < sql || !strings.Contains(text, "◆") {
		t.Fatalf("origin/query layout incorrect:\n%s", text)
	}
	s.newTab("human", "select 1", nil)
	if s.why.GetText(false) != "" {
		t.Fatal("why leaked to human tab")
	}
}
