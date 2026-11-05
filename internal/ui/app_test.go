package ui

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/theme"
)

func TestQuoteIdentByDriver(t *testing.T) {
	if got := quoteIdent(db.Postgres, `user"data`); got != `"user""data"` {
		t.Fatalf("postgres quote = %q", got)
	}
	if got := quoteIdent(db.MySQL, "user`data"); got != "`user``data`" {
		t.Fatalf("mysql quote = %q", got)
	}
}

func TestRunOptionsCapLimit(t *testing.T) {
	opts := NormalizeOptions(Options{Limit: query.MaxPreviewLimit + 1})
	if opts.Limit != query.MaxPreviewLimit {
		t.Fatalf("limit = %d, want %d", opts.Limit, query.MaxPreviewLimit)
	}
}

func TestApplyTViewThemeSetsBackgrounds(t *testing.T) {
	resolved, err := theme.Resolve("light")
	if err != nil {
		t.Fatal(err)
	}
	ApplyTViewTheme(resolved)
	if tview.Styles.PrimitiveBackgroundColor != resolved.TCell(resolved.Background) {
		t.Fatal("primitive background was not set from theme")
	}
	if tview.Styles.ContrastBackgroundColor != resolved.TCell(resolved.Panel) {
		t.Fatal("panel background was not set from theme")
	}
}

func TestResultsGridSupportsCellNavigation(t *testing.T) {
	state := newTestState(t, nil)
	rows, cols := state.grid.GetSelectable()
	if !rows || !cols {
		t.Fatalf("selectable = rows:%t cols:%t, want both", rows, cols)
	}
}

func TestTabFocusesResultsGrid(t *testing.T) {
	state := newTestState(t, nil)
	state.app.SetFocus(state.query)

	if got := state.captureInput(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)); got != nil {
		t.Fatalf("capture returned %v, want nil", got)
	}
	if focus := state.app.GetFocus(); focus != state.grid {
		t.Fatalf("focus = %T, want results grid", focus)
	}
}

func TestCommandModeRunsReadOnlySQL(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	state := newTestState(t, conn)

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alpha")
	mock.ExpectBegin()
	mock.ExpectQuery("select \\* from \\(select id, name from users\\) as dbbie_preview limit 101").WillReturnRows(rows)
	mock.ExpectRollback()

	state.runCommand("select id, name from users")

	if got := state.query.GetText(); got != "select id, name from users" {
		t.Fatalf("query text = %q", got)
	}
	if state.grid.GetRowCount() != 2 {
		t.Fatalf("row count = %d, want header + one row", state.grid.GetRowCount())
	}
	if got := state.grid.GetCell(1, 1).Text; got != "alpha" {
		t.Fatalf("rendered cell = %q", got)
	}
	if strings.Contains(state.status.GetText(false), "unknown command") {
		t.Fatalf("status = %q", state.status.GetText(false))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInitialSQLRunsAtStartup(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	state := newTestState(t, conn)

	rows := sqlmock.NewRows([]string{"source"}).AddRow("qlaw.runner")
	mock.ExpectBegin()
	mock.ExpectQuery("select \\* from \\(select source from session_events\\) as dbbie_preview limit 101").WillReturnRows(rows)
	mock.ExpectRollback()

	state.runInitialSQL(" select source from session_events ")

	if got := state.query.GetText(); got != "select source from session_events" {
		t.Fatalf("query text = %q", got)
	}
	if got := state.grid.GetCell(1, 0).Text; got != "qlaw.runner" {
		t.Fatalf("rendered cell = %q", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCommandModeRoutesWriteSQLToReadOnlyRejection(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	state := newTestState(t, conn)

	state.runCommand("delete from users")

	status := state.status.GetText(false)
	if !strings.Contains(status, "query rejected") {
		t.Fatalf("status = %q, want read-only rejection", status)
	}
	if strings.Contains(status, "unknown command") {
		t.Fatalf("status = %q", status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSearchGridSelectsFirstMatchingCell(t *testing.T) {
	state := newTestState(t, nil)
	state.renderResult(query.Result{
		Columns: []query.Column{{Name: "id"}, {Name: "source"}},
		Rows: [][]any{
			{1, "qlaw.runner"},
			{2, "qlaw.linear"},
		},
	})

	state.searchGrid("RUNNER")

	row, col := state.grid.GetSelection()
	if row != 1 || col != 1 {
		t.Fatalf("selection = (%d,%d), want (1,1)", row, col)
	}
	if state.grid.GetRowCount() != 2 {
		t.Fatalf("row count = %d, want header + filtered row", state.grid.GetRowCount())
	}
	if !strings.Contains(state.status.GetText(true), "match 1/1") || !strings.Contains(state.status.GetText(true), "1/2 rows") {
		t.Fatalf("status = %q", state.status.GetText(false))
	}
	_, got, _ := state.grid.GetCell(1, 1).Style.Decompose()
	if got != state.theme.TCell(state.theme.Accent) {
		t.Fatalf("matched background = %v, want accent", got)
	}
}

func TestSearchGridReportsNoMatch(t *testing.T) {
	state := newTestState(t, nil)
	state.renderResult(query.Result{
		Columns: []query.Column{{Name: "id"}, {Name: "source"}},
		Rows:    [][]any{{1, "qlaw.runner"}},
	})

	state.searchGrid("missing")

	if !strings.Contains(state.status.GetText(false), "no match") || !strings.Contains(state.status.GetText(false), "missing") {
		t.Fatalf("status = %q", state.status.GetText(false))
	}
	if state.grid.GetRowCount() != 2 || state.grid.GetCell(1, 0).Text != "0 rows" {
		t.Fatalf("row count = %d, want header + 0 rows message", state.grid.GetRowCount())
	}
}

func TestSearchGridClearingFilterRestoresRows(t *testing.T) {
	state := newTestState(t, nil)
	state.renderResult(query.Result{
		Columns: []query.Column{{Name: "id"}, {Name: "source"}},
		Rows: [][]any{
			{1, "qlaw.runner"},
			{2, "qlaw.linear"},
		},
	})

	state.searchGrid("runner")
	state.searchGrid("")

	if state.grid.GetRowCount() != 3 {
		t.Fatalf("row count = %d, want header + all rows", state.grid.GetRowCount())
	}
}

func newTestState(t *testing.T, conn *sql.DB) *state {
	t.Helper()
	resolved, err := theme.Resolve("midnight")
	if err != nil {
		t.Fatal(err)
	}
	s := newState(context.Background(), tview.NewApplication(), Options{
		Kind:  db.Postgres,
		DB:    conn,
		Theme: resolved,
		Limit: query.DefaultPreviewLimit,
	})
	// Entries reach receiveEntry only from the log tail; most tests exercise
	// --follow. Session-refresh tests turn it off explicitly.
	s.followEnabled = true
	s.runner = func(ctx context.Context, work queryWork, _ func(), done func(query.Result, error)) {
		result, err := work(ctx)
		done(result, err)
	}
	_ = s.layout()
	s.focusResults()
	return s
}

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock, func()) {
	t.Helper()
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return conn, mock, func() { _ = conn.Close() }
}
