package ui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

// Feed capture first, then the focused widget exactly as Application.Run does.
func press(s *state, key tcell.Key, r rune) {
	event := s.captureInput(tcell.NewEventKey(key, r, tcell.ModNone))
	if event != nil {
		if handler := s.app.GetFocus().InputHandler(); handler != nil {
			handler(event, func(p tview.Primitive) { s.app.SetFocus(p) })
		}
	}
}
func typeKeys(s *state, text string) {
	for _, r := range text {
		press(s, tcell.KeyRune, r)
	}
}
func enter(s *state)  { press(s, tcell.KeyEnter, 0) }
func escape(s *state) { press(s, tcell.KeyEsc, 0) }

func seedTables(s *state) {
	s.tables = []catalog.Table{
		{Schema: "public", Name: "auth_permission", EstimatedRows: 2184},
		{Schema: "public", Name: "balanceapi_account", EstimatedRows: 24},
		{Schema: "public", Name: "balanceapi_cardbalance", EstimatedRows: 0},
		{Schema: "public", Name: "unknown_view", EstimatedRows: -1},
	}
	s.filterTables("")
}
func expectQuery(mock sqlmock.Sqlmock, sql string, rows *sqlmock.Rows) {
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(query.BoundReadSQL(sql, query.DefaultPreviewLimit))).WillReturnRows(rows)
	mock.ExpectRollback()
}

func TestTabsOpenSwitchCloseThroughCapture(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	seedTables(s)
	for i := 0; i < 2; i++ {
		press(s, tcell.KeyCtrlH, 0)
		expectQuery(mock, `select * from "public"."auth_permission"`, sqlmock.NewRows([]string{"id"}).AddRow(i))
		typeKeys(s, "o")
	}
	if len(s.tabs) != 3 || s.active != 2 {
		t.Fatalf("tabs=%d active=%d", len(s.tabs), s.active)
	}
	typeKeys(s, "gt")
	if s.active != 0 {
		t.Fatal("gt did not wrap")
	}
	typeKeys(s, "gT")
	if s.active != 2 {
		t.Fatal("gT did not wrap")
	}
	typeKeys(s, "2")
	if s.active != 1 {
		t.Fatal("digit did not select tab")
	}
	typeKeys(s, "9")
	if s.active != 1 {
		t.Fatal("out-of-range digit changed tab")
	}
	typeKeys(s, ":q")
	enter(s)
	if len(s.tabs) != 2 || s.quitting {
		t.Fatal(":q should close, not quit")
	}
	typeKeys(s, "q")
	if len(s.tabs) != 1 || s.quitting {
		t.Fatal("q should close with multiple tabs")
	}
	typeKeys(s, "q")
	if !s.quitting {
		t.Fatal("q should quit on last tab")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQAllQuitsAndInputModesKeepLiteralKeys(t *testing.T) {
	s := newTestState(t, nil)
	typeKeys(s, "i")
	typeKeys(s, "q1gt?")
	if s.query.GetText() != "q1gt?" || s.quitting || len(s.tabs) != 1 {
		t.Fatal("insert mode intercepted literal keys")
	}
	escape(s)
	s.newTab("other", "", nil)
	typeKeys(s, ":qa")
	enter(s)
	if !s.quitting {
		t.Fatal(":qa did not quit")
	}
}

func TestSidebarFuzzyFilterEnterAndOriginProtection(t *testing.T) {
	for _, agent := range []bool{false, true} {
		t.Run(fmt.Sprint(agent), func(t *testing.T) {
			conn, mock, cleanup := mockDB(t)
			defer cleanup()
			s := newTestState(t, conn)
			seedTables(s)
			old := s.current()
			if agent {
				old.origin = &qlog.Entry{ID: "q_0042", Actor: "agent", Why: "find accounts"}
				old.sql = "select 42"
				s.showTab()
			}
			typeKeys(s, "tbalacct")
			if s.app.GetFocus() != s.tableFilter || len(s.visibleTables) != 1 || s.visibleTables[0].Name != "balanceapi_account" {
				t.Fatalf("fuzzy tables=%v", s.visibleTables)
			}
			if !strings.Contains(s.sidebar.GetCell(0, 0).Text, s.theme.Accent) {
				t.Fatal("matched characters not highlighted")
			}
			if !strings.Contains(s.sidePane.GetTitle(), "1/4") {
				t.Fatal("missing filter count")
			}
			enter(s)
			if s.app.GetFocus() != s.sidebar {
				t.Fatal("first Enter should keep filter and leave input")
			}
			sql := `select * from "public"."balanceapi_account"`
			expectQuery(mock, sql, sqlmock.NewRows([]string{"id"}).AddRow(4))
			enter(s)
			if s.query.GetText() != sql || s.app.GetFocus() != s.grid {
				t.Fatal("table not opened into results")
			}
			if agent {
				if len(s.tabs) != 2 || old.sql != "select 42" || s.current().origin != nil {
					t.Fatal("agent tab replaced")
				}
				typeKeys(s, "1")
				if !strings.Contains(s.why.GetText(false), "find accounts") || !strings.Contains(s.header.GetText(false), "◆") {
					t.Fatal("agent origin not shown")
				}
			} else if len(s.tabs) != 1 {
				t.Fatal("Enter should reuse a human tab")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaneAwareFiltersFocusAndModes(t *testing.T) {
	s := newTestState(t, nil)
	seedTables(s)
	s.renderResult(query.Result{Columns: []query.Column{{Name: "name"}}, Rows: [][]any{{"alpha"}, {"beta"}, {"alphabet"}}})
	press(s, tcell.KeyCtrlH, 0)
	if s.app.GetFocus() != s.sidebar || !strings.Contains(s.status.GetText(false), "TABLES") {
		t.Fatal("Ctrl-h did not focus tables")
	}
	typeKeys(s, "/bal")
	if len(s.visibleTables) != 2 || s.current().rowFilter != "" {
		t.Fatal("sidebar slash touched results")
	}
	escape(s)
	if len(s.visibleTables) != 4 {
		t.Fatal("Esc did not clear table filter")
	}
	press(s, tcell.KeyCtrlL, 0)
	if s.app.GetFocus() != s.grid {
		t.Fatal("Ctrl-l did not focus results")
	}
	typeKeys(s, "/alpha")
	enter(s)
	if s.current().rowFilter != "alpha" || s.grid.GetRowCount() != 3 || len(s.visibleTables) != 4 {
		t.Fatal("results slash touched sidebar")
	}
	typeKeys(s, "n")
	row, _ := s.grid.GetSelection()
	if row != 2 {
		t.Fatal("n did not select next match")
	}
	typeKeys(s, "N")
	row, _ = s.grid.GetSelection()
	if row != 1 {
		t.Fatal("N did not select previous match")
	}
	typeKeys(s, "/")
	escape(s)
	if s.current().rowFilter != "" || s.grid.GetRowCount() != 4 {
		t.Fatal("Esc did not clear row filter")
	}
	typeKeys(s, "i")
	if !strings.Contains(s.status.GetText(false), "INSERT") {
		t.Fatal("missing INSERT mode")
	}
	press(s, tcell.KeyCtrlH, 0)
	if s.app.GetFocus() != s.sidebar {
		t.Fatal("Ctrl-h should work from editor")
	}
}

func TestSidebarNavigationCountsAndSchemas(t *testing.T) {
	s := newTestState(t, nil)
	seedTables(s)
	s.focusTables()
	if s.sidebar.GetCell(0, 0).Text != "auth_permission" {
		t.Fatal("single schema should omit prefix")
	}
	if strings.TrimSpace(s.sidebar.GetCell(0, 1).Text) != "2.2k" || strings.TrimSpace(s.sidebar.GetCell(2, 1).Text) != "empty" || strings.TrimSpace(s.sidebar.GetCell(3, 1).Text) != "" {
		t.Fatal("incorrect counts")
	}
	emptyFG, _, _ := s.sidebar.GetCell(2, 0).Style.Decompose()
	if emptyFG != s.theme.TCell(s.theme.Muted) {
		t.Fatal("empty table not dimmed")
	}
	typeKeys(s, "jjk")
	row, _ := s.sidebar.GetSelection()
	if row != 1 {
		t.Fatal("j/k failed")
	}
	typeKeys(s, "G")
	row, _ = s.sidebar.GetSelection()
	if row != 3 {
		t.Fatal("G failed")
	}
	typeKeys(s, "gg")
	row, _ = s.sidebar.GetSelection()
	if row != 0 {
		t.Fatal("gg failed")
	}
	s.tables = append(s.tables, catalog.Table{Schema: "audit", Name: strings.Repeat("z", 70), EstimatedRows: -1})
	s.filterTables("")
	if s.sidebar.GetCell(0, 0).Text != "public.auth_permission" || s.sideWidth != 44 {
		t.Fatal("multi-schema prefix / width clamp failed")
	}
	s.tables = nil
	s.filterTables("")
	if s.sideWidth != 24 {
		t.Fatal("minimum sidebar width failed")
	}
	enter(s)
	typeKeys(s, "oK") // no table: no query and no panic
	if len(s.tabs) != 1 {
		t.Fatal("opened a missing table")
	}
}

func TestFuzzyUnicodeAndCompactCounts(t *testing.T) {
	positions, ok := fuzzyMatches("Éclair_帳戶", "é帳")
	if !ok || len(positions) != 2 || positions[1] != 7 {
		t.Fatalf("UTF-8 positions=%v match=%t", positions, ok)
	}
	if _, ok := fuzzyMatches("account", "az"); ok {
		t.Fatal("not a subsequence")
	}
	for n, want := range map[int64]string{-1: "", 0: "empty", 999: "999", 2184: "2.2k", 2_300_000: "2.3m", 1_000_000_000: "1.0b"} {
		if got := compactRows(n); got != want {
			t.Fatalf("count %d=%q want %q", n, got, want)
		}
	}
}

func TestDescribeColumnsRunsThroughCapture(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	seedTables(s)
	s.focusTables()
	mock.ExpectQuery("select table_schema, table_name, column_name").WithArgs("public", "auth_permission").WillReturnRows(
		sqlmock.NewRows([]string{"table_schema", "table_name", "column_name", "ordinal_position", "data_type", "is_nullable", "column_default"}).AddRow("public", "auth_permission", "id", 1, "integer", "NO", "nextval('id_seq')"))
	typeKeys(s, "K")
	if len(s.tabs) != 2 || s.current().title != "auth_permission columns" || s.grid.GetCell(1, 1).Text != "integer" {
		t.Fatal("K did not render column catalog")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHelpOverlayToggleRestoresPane(t *testing.T) {
	s := newTestState(t, nil)
	for _, pane := range []tview.Primitive{s.grid, s.sidebar} {
		s.app.SetFocus(pane)
		typeKeys(s, "?")
		if !s.helpOpen || !s.pages.HasPage("help") {
			t.Fatal("missing help overlay")
		}
		escape(s)
		if s.helpOpen || s.pages.HasPage("help") || s.app.GetFocus() != pane {
			t.Fatalf("help did not restore %T", pane)
		}
		typeKeys(s, "??")
		if s.helpOpen {
			t.Fatal("? did not toggle help off")
		}
	}
}

func TestTabKeepsResultsCursorAndFilter(t *testing.T) {
	s := newTestState(t, nil)
	s.renderResult(query.Result{Columns: []query.Column{{Name: "a"}, {Name: "b"}}, Rows: [][]any{{"alpha", "x"}, {"alphabet", "y"}}})
	typeKeys(s, "/alpha")
	enter(s)
	typeKeys(s, "nl")
	row, col := s.grid.GetSelection()
	old := s.current()
	s.newTab("second", "select 2", nil)
	typeKeys(s, "1")
	gotRow, gotCol := s.grid.GetSelection()
	if s.current() != old || old.rowFilter != "alpha" || gotRow != row || gotCol != col {
		t.Fatalf("tab state lost: filter=%q cursor=(%d,%d), want (%d,%d)", old.rowFilter, gotRow, gotCol, row, col)
	}
}

func TestEmptyResultShowsZeroRows(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	expectQuery(mock, "select id from empty_table", sqlmock.NewRows([]string{"id"}))
	typeKeys(s, ":select id from empty_table")
	enter(s)
	if got := s.grid.GetCell(1, 0).Text; got != "0 rows" {
		t.Fatalf("empty grid=%q", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPerTabCancelAndStaleCompletion(t *testing.T) {
	s := newTestState(t, nil)
	type held struct {
		ctx  context.Context
		done func(query.Result, error)
	}
	var runs []held
	s.runner = func(ctx context.Context, _ queryWork, _ func(), done func(query.Result, error)) {
		runs = append(runs, held{ctx, done})
	}
	typeKeys(s, "i")
	typeKeys(s, "select 1")
	enter(s)
	if s.pending != 1 || !strings.Contains(s.status.GetText(false), "running…") {
		t.Fatal("query not running")
	}
	press(s, tcell.KeyCtrlC, 0)
	if runs[0].ctx.Err() != context.Canceled || s.quitting {
		t.Fatal("Ctrl-c should cancel not quit")
	}
	s.runQuery("select 2")
	runs[0].done(query.Result{}, errors.New("old error"))
	if s.current().err != nil || s.current().cancel == nil {
		t.Fatal("stale completion replaced new run")
	}
	original := s.current()
	s.newTab("other", "", nil)
	runs[1].done(query.Result{Columns: []query.Column{{Name: "n"}}, Rows: [][]any{{2}}}, nil)
	if s.current().result.RowCount != 0 || original.result.Rows[0][0] != 2 {
		t.Fatal("background completion populated wrong tab")
	}
	typeKeys(s, "1")
	if s.grid.GetCell(1, 0).Text != "2" {
		t.Fatal("background result lost")
	}
	press(s, tcell.KeyCtrlC, 0)
	if !s.quitting {
		t.Fatal("idle Ctrl-c should quit")
	}
}

func TestClosingRunningTabCancelsOnlyThatTab(t *testing.T) {
	s := newTestState(t, nil)
	var contexts []context.Context
	var completions []func(query.Result, error)
	s.runner = func(ctx context.Context, _ queryWork, _ func(), done func(query.Result, error)) {
		contexts = append(contexts, ctx)
		completions = append(completions, done)
	}
	s.runQuery("select 1")
	s.newTab("second", "", nil)
	s.runQuery("select 2")
	typeKeys(s, "q")
	if contexts[1].Err() != context.Canceled || contexts[0].Err() != nil {
		t.Fatal("close canceled wrong tab")
	}
	completions[1](query.Result{}, context.Canceled)
	typeKeys(s, ":qa")
	enter(s)
	if contexts[0].Err() != context.Canceled {
		t.Fatal("quit did not cancel remaining query")
	}
	completions[0](query.Result{}, context.Canceled)
	if s.pending != 0 {
		t.Fatal("workers not drained")
	}
}

func TestLiveBackgroundQueryRemainsResponsiveAndCancels(t *testing.T) {
	started := make(chan struct{})
	conn, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		err := sqlmock.QueryMatcherRegexp.Match(expected, actual)
		if err == nil {
			close(started)
		}
		return err
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s := newTestState(t, conn)
	s.runner = s.backgroundRunner
	screen := tcell.NewSimulationScreen("UTF-8")
	s.app.SetScreen(screen)
	screen.SetSize(120, 40)
	s.app.SetRoot(s.pages, true).SetInputCapture(s.captureInput)
	ready := make(chan struct{}, 1)
	s.app.SetAfterDrawFunc(func(tcell.Screen) {
		select {
		case ready <- struct{}{}:
		default:
		}
	})
	finished := make(chan error, 1)
	go func() { finished <- s.app.Run() }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("event loop did not start")
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(query.BoundReadSQL("select 1", 100))).WillDelayFor(5 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	mock.ExpectRollback()
	s.app.QueueUpdateDraw(func() {
		typeKeys(s, "i")
		typeKeys(s, "select 1")
		enter(s)
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		s.app.Stop()
		t.Fatal("query did not start")
	}
	// A queued UI update can still run while the database operation is pending.
	s.app.QueueUpdateDraw(func() {
		typeKeys(s, "?")
		if !s.helpOpen {
			t.Error("slow query froze help")
		}
		escape(s)
		press(s, tcell.KeyCtrlC, 0)
		if s.quitting {
			t.Error("cancel quit the UI")
		}
		s.quit() // drains the canceled worker before stopping tview
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		s.app.Stop()
		t.Fatal("cancel did not release the event loop")
	}
}
