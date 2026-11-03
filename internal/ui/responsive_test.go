package ui

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/theme"
)

func drawAt(t *testing.T, s *state, width, height int) (tcell.SimulationScreen, []string) {
	t.Helper()
	ApplyTViewTheme(s.theme)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(width, height)
	s.pages.SetRect(0, 0, width, height)
	s.pages.Draw(screen)
	lines := make([]string, height)
	for y := range lines {
		var line strings.Builder
		for x := 0; x < width; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			line.WriteRune(r)
		}
		lines[y] = line.String()
	}
	return screen, lines
}

func TestModePillsAndHintsUseIndependentBackgroundsInEveryTheme(t *testing.T) {
	for _, name := range theme.Names() {
		t.Run(name, func(t *testing.T) {
			s := newTestState(t, nil)
			s.theme, _ = theme.Resolve(name)
			s.layout()
			s.focusResults()
			s.renderResult(query.Result{Columns: []query.Column{{Name: "currency"}}, Rows: [][]any{{"CAD"}, {"USD"}}, Elapsed: "7ms"})
			for _, mode := range []struct{ name, key, color string }{
				{"NORMAL", "hjkl", s.theme.Success},
				{"RECORD", "j/k", s.theme.Agent},
				{"FILTER", "Enter", s.theme.Accent},
				{"INSERT", "Ctrl-u", s.theme.Accent},
			} {
				switch mode.name {
				case "RECORD":
					enter(s)
				case "FILTER":
					escape(s)
					typeKeys(s, "/")
				case "INSERT":
					escape(s)
					typeKeys(s, "i")
				}
				screen, lines := drawAt(t, s, 100, 28)
				line := lines[27]
				if !strings.Contains(line, mode.name) || !strings.HasSuffix(strings.TrimRight(line, " "), "2 rows · 7ms") {
					t.Fatalf("mode/status missing: %q", line)
				}
				_, _, style, _ := screen.GetContent(1, 27)
				_, bg, _ := style.Decompose()
				if bg != s.theme.TCell(mode.color) {
					t.Fatalf("%s pill background=%v", mode.name, bg)
				}
				x := strings.Index(line, mode.key)
				if x < 0 {
					t.Fatalf("hint missing: %q", line)
				}
				_, _, style, _ = screen.GetContent(x, 27)
				fg, bg, _ := style.Decompose()
				if fg != s.theme.TCell(s.theme.Primary) || bg != s.theme.TCell(s.theme.Background) {
					t.Fatalf("%s hint inherits pill: fg=%v bg=%v", mode.name, fg, bg)
				}
			}
		})
	}
}

func TestCompactStatusSurvivesSortAndFilters(t *testing.T) {
	s := newTestState(t, nil)
	s.renderResult(query.Result{Columns: []query.Column{{Name: "currency"}}, Rows: [][]any{{"CAD"}, {"USD"}}, Elapsed: "7.4ms", Truncated: true})
	for _, keys := range []string{"s", "S", "/CAD"} {
		typeKeys(s, keys)
		if keys == "/CAD" {
			enter(s)
		}
		_, lines := drawAt(t, s, 100, 28)
		want := "2 rows · truncated · 7ms"
		if keys == "/CAD" {
			want = "1/2 rows · truncated · 7ms"
		}
		if !strings.HasSuffix(lines[27], want) || strings.Contains(lines[27], "truncated=") {
			t.Fatalf("status lost: %q", lines[27])
		}
	}
}

func TestModeDropsWholeHintsAndEscapesMessages(t *testing.T) {
	s := newTestState(t, nil)
	s.renderMode(45)
	line := s.status.GetText(true)
	if line != " NORMAL  hjkl move · Enter record · / filter" {
		t.Fatalf("clipped hint: %q", line)
	}
	s.setMessage("[red:green]untrusted")
	if !strings.Contains(s.status.GetText(true), "[red:green]untrusted") {
		t.Fatal("message interpreted as markup")
	}
}

func TestQueryPaneIsContentSizedAndRecordHasNoInspector(t *testing.T) {
	s := newTestState(t, nil)
	s.current().sql = "select 1"
	s.showTab()
	s.renderResult(query.Result{Columns: []query.Column{{Name: "currency", Database: "TEXT"}, {Name: "longer_key", Database: "INT8"}}, Rows: [][]any{{"CAD", 12}}})
	_, _ = drawAt(t, s, 120, 30)
	_, _, _, height := s.queryPane.GetRect()
	if height != 3 {
		t.Fatalf("one-line query height=%d, want 3", height)
	}
	enter(s)
	_, lines := drawAt(t, s, 120, 30)
	_, _, _, height = s.cellInfo.GetRect()
	if height != 0 {
		t.Fatal("record duplicates inspector")
	}
	text := strings.Join(lines, "\n")
	if strings.Count(text, "CAD") != 1 || !strings.Contains(text, "record · 1/1 rows") {
		t.Fatalf("record layout: %s", text)
	}
	record := s.recordView.GetText(true)
	if !strings.Contains(record, "  currency  TEXT  CAD") || !strings.Contains(record, "longer_key  INT8  12") {
		t.Fatalf("record columns not aligned: %q", record)
	}
	s.current().result.Rows[0][0] = strings.Repeat("long", 60)
	s.renderRecord()
	if !strings.Contains(s.recordView.GetText(true), "currency  TEXT\nlong") {
		t.Fatal("long value did not use stacked fallback")
	}
}

func TestColumnSizingRestoresHeadersBeforeLongValues(t *testing.T) {
	s := newTestState(t, nil)
	s.renderResult(query.Result{Columns: []query.Column{{Name: "description"}, {Name: "currency"}, {Name: "account_type"}}, Rows: [][]any{{strings.Repeat("x", 80), "CAD", "OPERATING"}}})
	s.fitColumns(36)
	if strings.Contains(s.grid.GetCell(0, 1).Text, "…") || strings.Contains(s.grid.GetCell(0, 2).Text, "…") {
		t.Fatal("short headers truncated instead of long value")
	}
	s.fitColumns(110)
	if s.grid.GetCell(1, 0).Text != strings.Repeat("x", 80) {
		t.Fatal("spare width did not restore truncated value")
	}
}

func TestSidebarEllipsisCountsAndSubtleSelection(t *testing.T) {
	s := newTestState(t, nil)
	s.tables = []catalog.Table{{Name: strings.Repeat("a", 80), EstimatedRows: 24}, {Name: "other", EstimatedRows: 2184}}
	s.filterTables("")
	s.focusTables()
	screen, lines := drawAt(t, s, 100, 28)
	if !strings.Contains(lines[3], "…    24") || !strings.Contains(lines[4], "2.2k") {
		t.Fatalf("sidebar counts/ellipsis: %q", lines[3:5])
	}
	_, _, style, _ := screen.GetContent(2, 3)
	fg, bg, _ := style.Decompose()
	if fg != s.theme.TCell(s.theme.Text) || bg != s.theme.TCell(s.theme.Selection) {
		t.Fatal("sidebar selection is not subtle/bright")
	}
}

func TestSQLReplacementClearCancelAndRun(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	s.query.SetText("select old from accounts")
	typeKeys(s, "iselect 1")
	if s.query.GetText() != "select 1" {
		t.Fatal("SQL edit appended to original")
	}
	press(s, tcell.KeyCtrlU, 0)
	if s.query.GetText() != "" {
		t.Fatal("Ctrl-u did not clear")
	}
	typeKeys(s, "draft")
	escape(s)
	if s.current().sql != "select old from accounts" || s.query.GetText() != "select old from accounts" {
		t.Fatal("Esc did not restore original SQL")
	}
	typeKeys(s, "iselect 1")
	expectQuery(mock, "select 1", sqlmock.NewRows([]string{"n"}).AddRow(1))
	enter(s)
	if s.current().sql != "select 1" || s.grid.GetCell(1, 0).Text != "1" || s.editing || s.app.GetFocus() != s.grid {
		t.Fatal("replacement not run")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInactiveTabsDoNotInheritActiveUnderline(t *testing.T) {
	s := newTestState(t, nil)
	s.newTab("second", "select 2", nil)
	s.switchTab(0)
	screen, lines := drawAt(t, s, 120, 30)
	x := strings.Index(lines[0], "second")
	if x < 0 {
		t.Fatal("inactive tab missing")
	}
	_, _, style, _ := screen.GetContent(x, 0)
	fg, bg, attr := style.Decompose()
	if fg != s.theme.TCell(s.theme.Muted) || bg != s.theme.TCell(s.theme.Background) || attr&tcell.AttrUnderline != 0 {
		t.Fatalf("inactive tab inherits selection: %v", style)
	}
}

func TestHelpObscuresShellAndUsesTwoColumns(t *testing.T) {
	s := newTestState(t, nil)
	typeKeys(s, "?")
	_, lines := drawAt(t, s, 120, 30)
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "dbbie") || strings.Contains(text, "results ·") {
		t.Fatal("shell bleeds through help")
	}
	if !strings.Contains(text, "ANYWHERE") || !strings.Contains(text, "RESULTS") || !strings.Contains(text, ":theme") {
		t.Fatalf("help missing keys: %s", text)
	}
	frame := s.pages.GetPage("help").(*helpFrame)
	_, _, width, height := frame.help.GetRect()
	if width != 100 || height != 23 {
		t.Fatalf("help card=%dx%d", width, height)
	}
	if tview.TaggedStringWidth(frame.help.GetText(false)) == 0 {
		t.Fatal("help empty")
	}
}
