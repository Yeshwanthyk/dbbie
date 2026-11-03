package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func TestUUIDsCollapseOnlyWhenPaneOverflows(t *testing.T) {
	s := newTestState(t, nil)
	uuid := "472c1ac4-f057-4e26-a994-01bde95be7c3"
	s.renderResult(query.Result{Columns: []query.Column{{Name: "a", Database: "UUID"}, {Name: "b", Database: "UUID"}, {Name: "c", Database: "UUID"}}, Rows: [][]any{{uuid, uuid, uuid}}})
	screen, text := drawShell(t, s)
	if s.grid.GetCell(1, 0).Text != "472c1ac4…" || !strings.Contains(text, "472c1ac4…") {
		t.Fatal("overflowing UUIDs did not compact")
	}
	// A larger pane restores full UUID values, not an already-shortened cache.
	screen.SetSize(200, 45)
	s.pages.SetRect(0, 0, 200, 45)
	s.pages.Draw(screen)
	if s.grid.GetCell(1, 0).Text != uuid {
		t.Fatal("UUID remained truncated after expanding pane")
	}
}

func TestNewResultResetsInheritedGridOffset(t *testing.T) {
	s := newTestState(t, nil)
	drawShell(t, s) // let tview's initial empty state track the bottom
	s.grid.SetOffset(50, 2)
	rows := make([][]any, 24)
	for i := range rows {
		rows[i] = []any{"other"}
	}
	rows[0] = []any{"first visible"}
	s.renderResult(query.Result{Columns: []query.Column{{Name: "a"}}, Rows: rows})
	_, text := drawShell(t, s)
	if !strings.Contains(text, "first visible") {
		t.Fatalf("selected first row hidden by inherited offset:\n%s", text)
	}
	row, col := s.grid.GetSelection()
	if row != 1 || col != 0 {
		t.Fatalf("cursor=(%d,%d)", row, col)
	}
}

func TestInspectorClipsToFooterNotResultsBody(t *testing.T) {
	s := newTestState(t, nil)
	s.renderResult(query.Result{Columns: []query.Column{{Name: "body", Database: "TEXT"}}, Rows: [][]any{{strings.Repeat("long value ", 100)}}})
	screen, _ := drawShell(t, s)
	x, y, width, height := s.cellInfo.GetRect()
	if height != 2 || width <= 0 {
		t.Fatalf("footer=%dx%d", width, height)
	}
	found := false
	for col := x; col < x+width; col++ {
		ch, _, _, _ := screen.GetContent(col, y+1)
		found = found || ch == 'l'
	}
	if !found {
		t.Fatal("full value did not wrap onto footer second line")
	}
	_, _, style, _ := screen.GetContent(x, y+height)
	fg, _, _ := style.Decompose()
	if fg != s.theme.TCell(s.theme.Primary) && fg != s.theme.TCell(s.theme.Border) && fg != tcell.ColorDefault {
		t.Fatal("footer text leaked through results border")
	}
}
