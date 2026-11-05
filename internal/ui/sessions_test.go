package ui

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gdamore/tcell/v2"
	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/session"
)

func sessionState(t *testing.T, conn *sql.DB) *state {
	t.Helper()
	t.Setenv("DBBIE_STATE_DIR", t.TempDir())
	t.Setenv("DBBIE_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("DBBIE_NO_LOG", "0")
	s := newTestState(t, conn)
	s.connection = "local"
	s.connectionSaved = true
	s.connections = []config.PublicConnection{{Name: "local", Driver: db.Postgres}}
	if err := s.prepareLog(false, 0); err != nil {
		t.Fatal(err)
	}
	return s
}

func createTrail(t *testing.T, s *state, name string) qlog.Entry {
	t.Helper()
	if _, err := session.Create(name, "investigation", "local", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	e, err := s.log.Append(qlog.Entry{Driver: "postgres", Source: "cli", Actor: "agent", SQL: "select 7", Session: name,
		Columns: []query.Column{{Name: "stored"}}, Rows: [][]any{{7}}, RowCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Add(name, session.Item{SQL: e.SQL, Title: "stored result", Why: "inspect stored preview", LogID: e.ID}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestOpenSessionReadsStoredResultsWithoutRunningSQL(t *testing.T) {
	s := sessionState(t, nil)
	createTrail(t, s, "demo")
	// Remove a middle item to prove #n uses stable identity, not tab position.
	second, err := session.Add("demo", session.Item{SQL: "select 8"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Remove("demo", second.N); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Add("demo", session.Item{SQL: "select 9", LogID: "q_9999", Why: "missing preview"}); err != nil {
		t.Fatal(err)
	}
	s.runner = func(context.Context, queryWork, func(), func(query.Result, error)) {
		t.Fatal("opening a session ran SQL")
	}
	s.openSession("demo")
	if len(s.tabs) != 2 || s.active != 0 || s.current().title != "stored result" || s.grid.GetCell(1, 0).Text != "7" {
		t.Fatalf("stored tabs not restored: %+v", s.tabs)
	}
	if got := s.why.GetText(true); got != "◇ demo #1   inspect stored preview" {
		t.Fatalf("why=%q", got)
	}
	typeKeys(s, "2")
	if s.current().title != "#3" || !strings.Contains(s.grid.GetCell(0, 0).Text, "no stored result · r to run") || s.current().err != nil {
		t.Fatal("missing entry should be a runnable placeholder with stable item number")
	}
	if got := s.why.GetText(true); got != "◇ demo #3   missing preview" {
		t.Fatalf("why=%q", got)
	}
}

func TestRerunUpdatesSessionLogIDInPlaceAndLogsRejections(t *testing.T) {
	for _, sqlText := range []string{"select 7", "delete from accounts"} {
		t.Run(sqlText, func(t *testing.T) {
			conn, mock, cleanup := mockDB(t)
			defer cleanup()
			s := sessionState(t, conn)
			old := createTrail(t, s, "demo")
			if sqlText != old.SQL {
				if _, err := session.Add("demo", session.Item{SQL: sqlText}); err != nil {
					t.Fatal(err)
				}
			}
			s.openSession("demo")
			if sqlText != old.SQL {
				s.switchTab(1)
			}
			tab := s.current()
			s.noLog = true // r, like session run, must still log.
			if sqlText == old.SQL {
				expectQuery(mock, sqlText, sqlmock.NewRows([]string{"fresh"}).AddRow(8))
			}
			typeKeys(s, "r")
			if s.current() != tab || len(s.tabs) != 1+boolInt(sqlText != old.SQL) {
				t.Fatal("r opened another tab")
			}
			trail, err := session.Open("demo")
			if err != nil {
				t.Fatal(err)
			}
			item := trail.Items[s.active]
			if item.LogID == "" || item.LogID == old.ID || item.LogID != tab.logID {
				t.Fatalf("log id not updated: %+v", item)
			}
			e, err := s.log.Get(item.LogID)
			if err != nil {
				t.Fatal(err)
			}
			if e.Session != "demo" || e.Source != "tui" || e.Actor != "human" || e.SQL != sqlText {
				t.Fatalf("bad logged rerun: %+v", e)
			}
			if sqlText == old.SQL && s.grid.GetCell(1, 0).Text != "8" {
				t.Fatal("fresh result not rendered")
			}
			if sqlText != old.SQL && (e.Error == nil || e.Error.Code != "rejected") {
				t.Fatal("rejected rerun not logged")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestAdhocConnectionCannotSaveSessionEvenWhenItsDisplayNameIsSaved(t *testing.T) {
	conn, _, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	s.connection, s.connectionSaved = "adhoc", false
	s.connections = []config.PublicConnection{{Name: "adhoc", Driver: db.Postgres}}
	s.current().sql = "select 1"
	s.saveSession("not-saved", "")
	if _, err := session.Open("not-saved"); !errors.Is(err, session.ErrNotFound) || !strings.Contains(s.message, "require a saved connection") {
		t.Fatal("ad-hoc handle was mistaken for a saved connection")
	}
}

func TestSaveAndAddThroughCommandMode(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	expectQuery(mock, "select 1", sqlmock.NewRows([]string{"n"}).AddRow(1))
	s.runQuery("select 1")
	firstID := s.current().logID
	s.newTab("second", "select 2", nil) // SQL without a stored result is preserved.
	typeKeys(s, ":save new-trail Human investigation")
	enter(s)
	trail, err := session.Open("new-trail")
	if err != nil {
		t.Fatal(err)
	}
	if trail.Title != "Human investigation" || trail.Conn != "local" || len(trail.Items) != 2 || trail.Items[0].LogID != firstID || trail.Items[1].SQL != "select 2" || trail.Items[1].LogID != "" {
		t.Fatalf("saved trail=%+v", trail)
	}
	if s.activeSession != "new-trail" || s.tabs[0].itemN != 1 || s.tabs[1].itemN != 2 {
		t.Fatal("saved tabs not bound to items")
	}
	s.newTab("third", "select 3", nil)
	typeKeys(s, ":add")
	enter(s)
	trail, err = session.Open("new-trail")
	if err != nil {
		t.Fatal(err)
	}
	if len(trail.Items) != 3 || trail.Items[2].SQL != "select 3" || s.current().itemN != 3 {
		t.Fatal(":add did not add and bind current tab")
	}
	_, lines := drawAt(t, s, 120, 30)
	if !strings.Contains(strings.Join(lines, "\n"), "session new-trail · #3") {
		t.Fatal("human session attribution is not visible on screen")
	}
	typeKeys(s, ":save new-trail")
	enter(s)
	if !strings.Contains(s.message, "already exists") {
		t.Fatalf("existing name error not visible: %s", s.message)
	}
	typeKeys(s, ":save Bad/Name")
	enter(s)
	if !strings.Contains(s.message, "invalid session name") {
		t.Fatalf("invalid name error not visible: %s", s.message)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPickerFuzzyAndEnterRestoresTabs(t *testing.T) {
	s := sessionState(t, nil)
	createTrail(t, s, "refund-bug")
	if _, err := session.Create("unrelated", "other", "local", "postgres", "human"); err != nil {
		t.Fatal(err)
	}
	typeKeys(s, ":sessions")
	enter(s)
	if s.picker == nil || len(s.picker.visible) != 2 {
		t.Fatal("picker missing sessions")
	}
	typeKeys(s, "rfbg")
	if len(s.picker.visible) != 1 || !strings.Contains(s.picker.visible[0].detail, "investigation · 1 items · ") {
		t.Fatal("session metadata / fuzzy filter missing")
	}
	enter(s)
	if s.picker != nil || len(s.tabs) != 1 || s.activeSession != "refund-bug" || s.grid.GetCell(1, 0).Text != "7" {
		t.Fatal("picker Enter did not open stored session")
	}
}

func TestConnectionPickerIsPublicAndSwitchKeepsTabs(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, nil)
	createTrail(t, s, "demo")
	s.openSession("demo")
	original := s.current()
	cfg := config.File{Connections: []config.Connection{{Name: "local", Driver: db.Postgres, DSN: "postgres://password@host/db"}, {Name: "staging", Driver: db.Postgres, DSN: "secret-staging-dsn"}}}
	s.connections = cfg.PublicConnections()
	s.openConnection = func(_ context.Context, name string) (db.Kind, *sql.DB, error) {
		if name != "staging" {
			t.Fatalf("switch requested %q", name)
		}
		return db.Postgres, conn, nil
	}
	mock.ExpectQuery("select t.table_schema").WillReturnRows(sqlmock.NewRows([]string{"schema", "name", "type"}).AddRow("public", "fresh_table", "BASE TABLE"))
	typeKeys(s, ":conn")
	enter(s)
	for _, item := range s.picker.items {
		if strings.Contains(item.name+item.detail, "secret") || strings.Contains(item.name+item.detail, "password") || item.detail != "postgres" {
			t.Fatal("connection picker exposed more than name/driver")
		}
	}
	_, lines := drawAt(t, s, 120, 30)
	if strings.Contains(strings.Join(lines, "\n"), "dsn") || strings.Contains(strings.Join(lines, "\n"), "password") {
		t.Fatal("rendered picker contains DSNs")
	}
	typeKeys(s, "stag")
	if s.picker.input.GetText() != "stag" || len(s.picker.visible) != 1 || s.picker.visible[0].name != "staging" {
		t.Fatalf("filter input=%q visible=%+v focus=%T", s.picker.input.GetText(), s.picker.visible, s.app.GetFocus())
	}
	enter(s)
	if s.connection != "staging" || s.current() != original || len(s.tabs) != 1 || len(s.tables) != 1 || s.tables[0].Name != "fresh_table" || !strings.Contains(s.header.GetText(true), "staging · postgres") {
		t.Fatalf("switch did not reload catalog/keep tabs/header: conn=%s sameTab=%t tabs=%d tables=%+v header=%q message=%q", s.connection, s.current() == original, len(s.tabs), s.tables, s.header.GetText(true), s.message)
	}
	before := original.logID
	typeKeys(s, "r")
	if original.logID != before || !strings.Contains(s.message, "session uses local") {
		t.Fatal("session reran on the wrong connection")
	}
	typeKeys(s, ":add")
	enter(s)
	if !strings.Contains(s.message, "active session uses local") {
		t.Fatal("cross-connection add accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionFailurePreservesWorkspace(t *testing.T) {
	s := sessionState(t, nil)
	s.openConnection = func(context.Context, string) (db.Kind, *sql.DB, error) {
		return "", nil, errors.New("could not open database connection")
	}
	old := s.current()
	s.switchConnection("bad", nil)
	if s.connection != "local" || s.current() != old || s.pending != 0 || s.connecting {
		t.Fatal("failed switch changed workspace or leaked pending work")
	}
}

func TestFollowTabDisplaysSessionName(t *testing.T) {
	s := newTestState(t, nil)
	e := agentEntry("q_0010")
	e.Session = "refund-bug"
	s.receiveEntry(e)
	if !strings.Contains(s.why.GetText(true), "◇ refund-bug") || !strings.Contains(s.header.GetText(true), "◆ refund-bug") {
		t.Fatal("follow session attribution missing")
	}
}

func TestRerunFollowedSessionEntryUpdatesPinnedItem(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	e := createTrail(t, s, "demo")
	s.receiveEntry(e)
	expectQuery(mock, "select 7", sqlmock.NewRows([]string{"fresh"}).AddRow(8))
	typeKeys(s, "r")
	trail, err := session.Open("demo")
	if err != nil {
		t.Fatal(err)
	}
	if trail.Items[0].LogID == e.ID || trail.Items[0].LogID != s.current().logID || s.current().itemN != 1 {
		t.Fatal("followed session item was not updated")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRerunWithoutLoggingEnabledStillCreatesLog(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	s.noLog, s.log = true, nil
	s.current().sql = "select 1"
	expectQuery(mock, "select 1", sqlmock.NewRows([]string{"n"}).AddRow(1))
	typeKeys(s, "r")
	entries, err := s.log.List(0)
	if err != nil || len(entries) != 1 || s.current().logID != entries[0].ID {
		t.Fatal("r did not create a stored result with --no-log")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryUIAndLogRedactConfiguredCredentials(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	path, _ := config.Path()
	secret := "opaque-private-credential"
	if err := config.Save(path, config.File{Connections: []config.Connection{{Name: "local", Driver: db.Postgres, DSN: secret}}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("select \\*").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(secret))
	mock.ExpectRollback()
	s.runQuery("select '" + secret + "' as value")
	entries, err := s.log.List(0)
	if err != nil || len(entries) != 1 {
		t.Fatal("query not logged")
	}
	if strings.Contains(entries[0].SQL, secret) || entries[0].Rows[0][0] != "[redacted]" || !strings.Contains(s.grid.GetCell(1, 0).Text, "redacted") || strings.Contains(s.grid.GetCell(1, 0).Text, secret) {
		t.Fatal("query output/log contains configured credentials")
	}
	if strings.Contains(s.sqlView.GetText(true), secret) || strings.Contains(s.current().sql, secret) {
		t.Fatal("UI query text contains configured credentials")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOldFollowerDrainsWithoutChangingNewConnection(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	s.followEnabled = true
	type callbacks struct {
		ctx   context.Context
		entry func(qlog.Entry)
		done  func(error)
	}
	var followers []callbacks
	s.follow = func(ctx context.Context, _ int64, entry func(qlog.Entry), done func(error)) {
		followers = append(followers, callbacks{ctx, entry, done})
	}
	s.startFollow()
	s.openConnection = func(context.Context, string) (db.Kind, *sql.DB, error) { return db.Postgres, conn, nil }
	mock.ExpectQuery("select t.table_schema").WillReturnRows(sqlmock.NewRows([]string{"schema", "name", "type"}))
	s.switchConnection("other", nil)
	if len(followers) != 2 || followers[0].ctx.Err() != context.Canceled {
		t.Fatal("switch did not replace follower")
	}
	followers[0].entry(agentEntry("q_0042"))
	if len(s.tabs) != 1 {
		t.Fatal("old connection entry leaked into new workspace")
	}
	followers[0].done(context.Canceled)
	if !s.following || s.followWorkers != 1 {
		t.Fatal("old completion stopped new follower")
	}
	followers[1].entry(agentEntry("q_0042"))
	if len(s.tabs) != 2 {
		t.Fatal("new follower's connection-scoped id was suppressed")
	}
	s.quit()
	followers[1].done(context.Canceled)
	if s.followWorkers != 0 || s.following {
		t.Fatal("followers did not drain")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStartupPickerSelectionRestoresRecentFollowEntries(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, nil)
	log, err := qlog.Open("other")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := log.Append(agentEntry("")); err != nil {
			t.Fatal(err)
		}
	}
	s.awaitingConnection, s.followEnabled, s.startupRecent = true, true, 2
	s.openConnection = func(context.Context, string) (db.Kind, *sql.DB, error) { return db.Postgres, conn, nil }
	var finish func(error)
	s.follow = func(_ context.Context, from int64, _ func(qlog.Entry), done func(error)) {
		finish = done
		if from == 0 {
			t.Fatal("startup follower did not take initial log offset")
		}
	}
	mock.ExpectQuery("select t.table_schema").WillReturnRows(sqlmock.NewRows([]string{"schema", "name", "type"}))
	s.switchConnection("other", nil)
	if len(s.tabs) != 3 || s.tabs[1].logID != "q_0002" || s.tabs[2].logID != "q_0003" || s.awaitingConnection {
		t.Fatal("startup connection selection did not preserve --recent")
	}
	s.quit()
	finish(context.Canceled)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionSwitchCanBeCanceledWithoutQuitting(t *testing.T) {
	conn, _, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	s.openConnection = func(context.Context, string) (db.Kind, *sql.DB, error) { return db.Postgres, conn, nil }
	var workCtx context.Context
	var finish func(query.Result, error)
	s.runner = func(ctx context.Context, _ queryWork, _ func(), done func(query.Result, error)) {
		workCtx, finish = ctx, done
	}
	s.switchConnection("other", nil)
	press(s, tcell.KeyCtrlC, 0)
	if workCtx.Err() != context.Canceled || s.quitting {
		t.Fatal("Ctrl-c did not cancel the connection switch in place")
	}
	finish(query.Result{}, context.Canceled)
	if s.pending != 0 || s.connectCancel != nil || s.connection != "local" || s.connecting {
		t.Fatal("canceled switch leaked work or changed connection")
	}
}

func TestStartupConnectionPickerAppearsWithoutOpeningDatabase(t *testing.T) {
	s := newTestState(t, nil)
	s.connection = "choose connection"
	s.connections = []config.PublicConnection{{Name: "one", Driver: db.Postgres}, {Name: "two", Driver: db.MySQL}}
	screen := tcell.NewSimulationScreen("UTF-8")
	s.app.SetScreen(screen)
	screen.SetSize(120, 30)
	s.app.SetRoot(s.pages, true).SetInputCapture(s.captureInput)
	s.configureStartup("", false)
	shown := make(chan struct{}, 1)
	s.app.SetAfterDrawFunc(func(tcell.Screen) {
		if s.picker != nil {
			select {
			case shown <- struct{}{}:
			default:
			}
		}
	})
	finished := make(chan error, 1)
	go func() { finished <- s.app.Run() }()
	select {
	case <-shown:
	case <-time.After(3 * time.Second):
		t.Fatal("startup picker not shown")
	}
	s.app.QueueUpdateDraw(func() {
		if len(s.picker.visible) != 2 {
			t.Error("wrong startup names")
		}
		escape(s)
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("startup picker cancellation did not quit")
	}
}

func TestSaveSkipsBlankTabsAndAddRefusesDuplicates(t *testing.T) {
	conn, _, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	s.current().sql = "  " // the untouched starting tab
	s.saveSession("empty", "")
	if _, err := session.Open("empty"); !errors.Is(err, session.ErrNotFound) || !strings.Contains(s.message, "nothing to save") {
		t.Fatalf("blank-only save created a session: %s", s.message)
	}
	s.newTab("real", "select 2", nil)
	s.saveSession("mixed", "")
	trail, err := session.Open("mixed")
	if err != nil || len(trail.Items) != 1 || trail.Items[0].SQL != "select 2" {
		t.Fatalf("trail=%+v err=%v", trail, err)
	}
	if s.tabs[0].sessionName != "" || s.tabs[1].itemN != 1 {
		t.Fatal("blank tab was bound to a session item")
	}
	typeKeys(s, ":add")
	enter(s)
	if trail, _ = session.Open("mixed"); len(trail.Items) != 1 || !strings.Contains(s.message, "already in session mixed") {
		t.Fatalf(":add duplicated the tab: items=%d msg=%s", len(trail.Items), s.message)
	}
	s.newTab("blank", "", nil)
	typeKeys(s, ":add")
	enter(s)
	if trail, _ = session.Open("mixed"); len(trail.Items) != 1 || !strings.Contains(s.message, "no SQL") {
		t.Fatalf(":add stored a blank tab: msg=%s", s.message)
	}
}

func TestRerunShowsResultWhenSessionItemIsGone(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := sessionState(t, conn)
	createTrail(t, s, "demo")
	s.openSession("demo")
	if err := session.Remove("demo", 1); err != nil { // another terminal removed it
		t.Fatal(err)
	}
	expectQuery(mock, "select 7", sqlmock.NewRows([]string{"fresh"}).AddRow(8))
	typeKeys(s, "r")
	if s.current().err != nil || s.grid.GetCell(1, 0).Text != "8" {
		t.Fatalf("successful rerun shown as failure: err=%v", s.current().err)
	}
	if !strings.Contains(s.message, "was not updated") {
		t.Fatalf("missing warning: %s", s.message)
	}
}

func TestSessionRerunEntryRefreshesOpenTabInPlace(t *testing.T) {
	s := sessionState(t, nil)
	s.followEnabled = false // session refresh must not depend on --follow
	old := createTrail(t, s, "demo")
	if _, err := session.Add("demo", session.Item{SQL: "select 9", Title: "second"}); err != nil {
		t.Fatal(err)
	}
	s.openSession("demo")
	tabs := len(s.tabs)
	fresh := qlog.Entry{ID: "q_9001", Conn: "local", Source: "cli", Actor: "agent", Session: "demo", Item: 1,
		SQL: "select 7", Columns: []query.Column{{Name: "stored"}}, Rows: [][]any{{8}}, RowCount: 1}
	s.receiveEntry(fresh)
	if len(s.tabs) != tabs || s.active != 0 || s.current().logID != "q_9001" || s.grid.GetCell(1, 0).Text != "8" {
		t.Fatalf("rerun did not refresh tab #1 in place: tabs=%d logID=%s cell=%q", len(s.tabs), s.current().logID, s.grid.GetCell(1, 0).Text)
	}
	if !strings.Contains(s.message, "↻ #1 refreshed by agent") || s.current().logID == old.ID {
		t.Fatalf("message=%q", s.message)
	}
	other := qlog.Entry{ID: "q_9002", Conn: "local", Source: "cli", Actor: "agent", SQL: "select 1"}
	s.receiveEntry(other)
	if len(s.tabs) != tabs {
		t.Fatal("without --follow, unrelated entries must not open tabs")
	}
	wrongConn := fresh
	wrongConn.ID, wrongConn.Conn = "q_9003", "prod"
	s.receiveEntry(wrongConn)
	if s.current().logID != "q_9001" {
		t.Fatal("an entry from another connection must not refresh this session")
	}
}
