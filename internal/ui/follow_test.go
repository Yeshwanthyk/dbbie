package ui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/theme"
)

func agentEntry(id string) qlog.Entry {
	return qlog.Entry{ID: id, TS: time.Date(2026, 11, 3, 11, 20, 0, 0, time.UTC),
		Source: "cli", Actor: "agent", Title: "currencies", Why: "check account currencies", SQL: "select currency, count(*) from accounts group by 1",
		Columns: []query.Column{{Name: "currency", Database: "TEXT"}, {Name: "count", Database: "INT8"}},
		Rows:    [][]any{{"USD", float64(42)}}, RowCount: 1, Truncated: true, ElapsedMs: 14}
}

func TestFollowCreatesStoredTabWithoutRunningSQL(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	e := agentEntry("q_0042")
	s.receiveEntry(e)
	if s.active != 1 || len(s.tabs) != 2 || s.current().origin == nil || s.current().sql != e.SQL || !reflect.DeepEqual(s.current().result.Rows, e.Rows) {
		t.Fatal("entry did not become a stored tab")
	}
	if s.current().result.ID != e.ID || s.current().result.Elapsed != "14ms" || !s.current().result.Truncated || s.grid.GetCell(1, 1).Text != "42" {
		t.Fatalf("stored result lost: %+v", s.current().result)
	}
	_, text := drawShell(t, s)
	for _, want := range []string{"◆", "AGENT", "agent", "q_0042", "2026-11-03", "currencies", "check account currencies", "USD"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q on screen:\n%s", want, text)
		}
	}
	s.receiveEntry(e)
	if len(s.tabs) != 2 {
		t.Fatal("duplicate log entry opened twice")
	}
	failed := agentEntry("q_0043")
	failed.Rows, failed.Columns = nil, nil
	failed.Error = &qlog.Error{Code: "db", Message: "stored database error"}
	s.receiveEntry(failed)
	_, text = drawShell(t, s)
	if !strings.Contains(text, "stored database error") {
		t.Fatal("stored error not rendered")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err) // no SQL was expected, including when selecting a stored tab
	}
}

func TestAgentTabNavigationWrapsAndSkipsHumanTabs(t *testing.T) {
	s := newTestState(t, nil)
	s.receiveEntry(agentEntry("q_0042"))
	s.newTab("human", "", nil)
	s.receiveEntry(agentEntry("q_0043"))
	typeKeys(s, "]a")
	if s.active != 1 {
		t.Fatal("]a did not wrap to first agent")
	}
	typeKeys(s, "[a")
	if s.active != 3 {
		t.Fatal("[a did not wrap to last agent")
	}
	typeKeys(s, "3[a")
	if s.active != 1 {
		t.Fatal("[a did not skip human tab")
	}
	typeKeys(s, "]a")
	if s.active != 3 {
		t.Fatal("]a did not skip human tab")
	}
	empty := newTestState(t, nil)
	typeKeys(empty, "]a[a")
	if empty.active != 0 || len(empty.tabs) != 1 {
		t.Fatal("navigation without agents changed tabs")
	}
}

func TestFollowDoesNotStealAnyInputOrHelpFocus(t *testing.T) {
	for _, mode := range []string{"sql", "command", "rows", "tables", "help"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestState(t, nil)
			seedTables(s)
			s.renderResult(query.Result{Columns: []query.Column{{Name: "name"}}, Rows: [][]any{{"alpha"}}})
			switch mode {
			case "sql":
				typeKeys(s, "iselect 1")
			case "command":
				typeKeys(s, ":select 1")
			case "rows":
				typeKeys(s, "/alpha")
			case "tables":
				typeKeys(s, "tbal")
			case "help":
				typeKeys(s, "?")
			}
			focus, current := s.app.GetFocus(), s.current()
			s.receiveEntry(agentEntry("q_0043"))
			if s.app.GetFocus() != focus || s.current() != current || len(s.tabs) != 2 {
				t.Fatal("follow stole input focus or replaced the active tab")
			}
			if !strings.Contains(s.status.GetText(true), "◆ new agent query q_0043 · ]a to view") {
				t.Fatal("missing non-intrusive agent notice")
			}
			if mode != "help" {
				_, text := drawShell(t, s)
				if !strings.Contains(text, "◆ new agent query q_0043 · ]a to view") {
					t.Fatalf("notice clipped while typing:\n%s", text)
				}
				typeKeys(s, "x")
				if field, ok := focus.(*tview.InputField); !ok || !strings.HasSuffix(field.GetText(), "x") {
					t.Fatal("literal typing was redirected")
				}
			}
			escape(s)
			typeKeys(s, "]a")
			if s.current().origin == nil || s.current().origin.ID != "q_0043" || s.agentNotice != "" {
				t.Fatal("notice did not lead to new agent tab")
			}
		})
	}
}

func TestEditAgentQueryCreatesHumanTab(t *testing.T) {
	s := newTestState(t, nil)
	e := agentEntry("q_0042")
	s.receiveEntry(e)
	old := s.current()
	typeKeys(s, "i")
	typeKeys(s, " x")
	if old.sql != e.SQL || s.current() == old || s.current().origin != nil {
		t.Fatal("editing mutated the recorded query")
	}
}

func TestRecentEntriesAndFollowOffset(t *testing.T) {
	t.Setenv("DBBIE_STATE_DIR", t.TempDir())
	t.Setenv("DBBIE_NO_LOG", "0")
	l, err := qlog.Open("local")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if _, err := l.Append(agentEntry("")); err != nil {
			t.Fatal(err)
		}
	}
	offset, err := l.Size()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{5, 0, 2} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := newTestState(t, nil)
			s.connection = "local"
			if err := s.prepareLog(true, n); err != nil {
				t.Fatal(err)
			}
			if len(s.tabs) != n+1 || s.logOffset != offset {
				t.Fatalf("tabs=%d offset=%d, want %d/%d", len(s.tabs), s.logOffset, n+1, offset)
			}
			if n > 0 && s.tabs[len(s.tabs)-1].result.ID != "q_0007" {
				t.Fatal("did not restore newest entries")
			}
			var followCtx context.Context
			var finish func(error)
			s.follow = func(ctx context.Context, from int64, entry func(qlog.Entry), done func(error)) {
				followCtx, finish = ctx, done
				if from != offset {
					t.Fatalf("follow starts at %d, want %d", from, offset)
				}
				entry(agentEntry("q_0008"))
			}
			s.startFollow()
			s.quit()
			if followCtx.Err() != context.Canceled || !s.following {
				t.Fatal("quit did not cancel and drain follower")
			}
			finish(context.Canceled)
			if s.following {
				t.Fatal("follower completion not drained")
			}
		})
	}
}

func TestHumanQueriesLoggedIncludingErrorsAndOptOut(t *testing.T) {
	for _, mode := range []string{"enabled", "flag", "env"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DBBIE_STATE_DIR", t.TempDir())
			t.Setenv("DBBIE_NO_LOG", "0")
			if mode == "env" {
				t.Setenv("DBBIE_NO_LOG", "1")
			}
			conn, mock, cleanup := mockDB(t)
			defer cleanup()
			s := newTestState(t, conn)
			s.connection = "local"
			s.noLog = mode != "enabled"
			// Following remains available even when writes are disabled.
			if err := s.prepareLog(true, 0); err != nil {
				t.Fatal(err)
			}
			expectQuery(mock, "select 1", sqlmock.NewRows([]string{"n"}).AddRow(1))
			typeKeys(s, ":select 1")
			enter(s)
			typeKeys(s, ":delete from accounts")
			enter(s)
			mock.ExpectBegin()
			mock.ExpectQuery("select \\* from").WillReturnError(errors.New("missing column"))
			mock.ExpectRollback()
			s.runQuery("select missing from accounts")
			entries, err := s.log.List(0)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "enabled" {
				if len(entries) != 0 {
					t.Fatal("opt-out wrote query log")
				}
			} else {
				if len(entries) != 3 {
					t.Fatalf("logged %d queries, want 3", len(entries))
				}
				e := entries[0]
				if e.Source != "tui" || e.Actor != "human" || e.Conn != "local" || e.Driver != "postgres" || e.SQL != "select 1" || e.RowCount != 1 || e.Rows[0][0] != float64(1) || e.Error != nil {
					t.Fatalf("incorrect human log: %+v", e)
				}
				if entries[1].Error == nil || entries[1].Error.Code != "rejected" || entries[2].Error == nil || entries[2].Error.Code != "db" || entries[2].Error.Message != "missing column" {
					t.Fatal("structured errors missing from log")
				}
				for _, e := range entries {
					s.receiveEntry(e)
				}
				if len(s.tabs) != 1 {
					t.Fatal("following own human log duplicated tabs")
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnLogEchoBeforeCompletionIsDeduplicated(t *testing.T) {
	s := newTestState(t, nil)
	var finish func(query.Result, error)
	s.runner = func(_ context.Context, _ queryWork, _ func(), done func(query.Result, error)) { finish = done }
	s.runQuery("select 1")
	own := agentEntry("q_0042")
	own.Source, own.Actor = "tui", "human"
	s.receiveEntry(own)
	if len(s.tabs) != 1 || len(s.deferred) != 1 {
		t.Fatal("own echo should wait for the pending completion")
	}
	// Unrelated agent entries must still appear immediately during a query.
	s.receiveEntry(agentEntry("q_0043"))
	if len(s.tabs) != 2 {
		t.Fatal("agent entry was delayed by a human query")
	}
	finish(query.Result{ID: "q_0042", Columns: own.Columns, Rows: own.Rows}, nil)
	if len(s.tabs) != 2 || len(s.deferred) != 0 {
		t.Fatal("own completion did not deduplicate and flush the echo")
	}
}

func TestLogEnvironmentAndConnectionLabels(t *testing.T) {
	t.Setenv("DBBIE_STATE_DIR", t.TempDir())
	t.Setenv("DBBIE_NO_LOG", "1")
	resolved, _ := theme.Resolve("midnight")
	for _, name := range []string{"local", ""} {
		s := newState(context.Background(), tview.NewApplication(), Options{Kind: db.Postgres, Theme: resolved, Connection: name})
		if !s.noLog {
			t.Fatal("environment opt-out ignored")
		}
		if err := s.prepareLog(false, 5); err != nil || s.log != nil {
			t.Fatal("disabled logging opened a file")
		}
		s.layout()
		want := name
		if want == "" {
			want = "adhoc"
		}
		if !strings.Contains(s.header.GetText(true), want+" · postgres") {
			t.Fatal("header does not show connection display name")
		}
	}
}

func TestLiveStartupQueryWithFollowAndNoLog(t *testing.T) {
	t.Setenv("DBBIE_STATE_DIR", t.TempDir())
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	s.noLog = true
	s.runner = s.backgroundRunner
	if err := s.prepareLog(true, 0); err != nil {
		t.Fatal(err)
	}
	expectQuery(mock, "select 1 as proof", sqlmock.NewRows([]string{"proof"}).AddRow(1))
	screen := tcell.NewSimulationScreen("UTF-8")
	s.app.SetScreen(screen)
	screen.SetSize(120, 40)
	s.app.SetRoot(s.pages, true).SetInputCapture(s.captureInput)
	s.configureStartup("select 1 as proof", true)
	rendered := make(chan struct{}, 1)
	s.app.SetAfterDrawFunc(func(tcell.Screen) {
		if s.current().result.RowCount == 1 {
			select {
			case rendered <- struct{}{}:
			default:
			}
		}
	})
	finished := make(chan error, 1)
	go func() { finished <- s.app.Run() }()
	select {
	case <-rendered:
	case <-time.After(3 * time.Second):
		t.Fatal("startup query froze the event loop before rendering")
	}
	s.app.QueueUpdateDraw(func() {
		if s.grid.GetCell(1, 0).Text != "1" || !s.following {
			t.Error("startup query or follower missing")
		}
		s.quit()
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("startup follower did not drain")
	}
	entries, err := s.log.List(0)
	if err != nil || len(entries) != 0 {
		t.Fatal("--no-log startup query wrote a log entry")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveFollowerQueuesEntriesAndCancelsOnQuit(t *testing.T) {
	s := newTestState(t, nil)
	s.logOffset = 321
	entered, released := make(chan struct{}), make(chan struct{})
	// Exercise the real event queue without depending on a 250ms file poll.
	s.follow = func(ctx context.Context, offset int64, entry func(qlog.Entry), done func(error)) {
		if offset != 321 {
			t.Error("wrong offset")
		}
		go func() {
			s.app.QueueUpdateDraw(func() { entry(agentEntry("q_0042")); close(entered) })
			<-ctx.Done()
			s.app.QueueUpdateDraw(func() { done(ctx.Err()) })
			close(released)
		}()
	}
	screen := tcell.NewSimulationScreen("UTF-8")
	s.app.SetScreen(screen)
	screen.SetSize(120, 40)
	s.app.SetRoot(s.pages, true).SetInputCapture(s.captureInput)
	started := false
	s.app.SetBeforeDrawFunc(func(tcell.Screen) bool {
		if !started {
			started = true
			s.startFollow()
		}
		return false
	})
	finished := make(chan error, 1)
	go func() { finished <- s.app.Run() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		s.app.Stop()
		t.Fatal("entry not queued")
	}
	s.app.QueueUpdateDraw(func() {
		if s.current().origin == nil || s.current().origin.ID != "q_0042" {
			t.Error("queued entry not rendered")
		}
		s.quit()
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		s.app.Stop()
		t.Fatal("quit did not drain follower")
	}
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("follower stranded after quit")
	}
}
