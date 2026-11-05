package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/session"
	"github.com/yeshwanthyk/dbbie/internal/ui"
)

func command(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, stderr bytes.Buffer
	err := Run(context.Background(), args, &out, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	return out.String(), err
}

func TestQuerySessionAutoCreateEnvAndOverride(t *testing.T) {
	for _, viaEnv := range []bool{false, true} {
		t.Run(map[bool]string{false: "flag", true: "env"}[viaEnv], func(t *testing.T) {
			mock, _ := setupQuery(t)
			t.Setenv("DBBIE_SESSION", "")
			expectRead(mock)
			args := []string{"query", "--conn", "local", "--sql", "select 1 as x", "--why", "check one", "--title", "one"}
			if viaEnv {
				t.Setenv("DBBIE_SESSION", "trail")
			} else {
				t.Setenv("DBBIE_SESSION", "ignored")
				args = append(args, "--session", "trail")
			}
			out, err := command(t, args...)
			if err != nil {
				t.Fatalf("query: %s %v", out, err)
			}
			s, err := session.Open("trail")
			if err != nil {
				t.Fatal(err)
			}
			if s.Conn != "local" || s.Driver != "postgres" || s.CreatedBy != "agent" || len(s.Items) != 1 || s.Items[0].LogID != "q_0001" || s.Items[0].Title != "one" || s.Items[0].Why != "check one" || s.Items[0].AddedBy != "agent" {
				t.Fatalf("session: %+v", s)
			}
			entries := readEntries(t, "local")
			if entries[0].Session != "trail" {
				t.Fatalf("log: %+v", entries[0])
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuerySessionMismatchAndInvalidStopBeforeDB(t *testing.T) {
	setupQuery(t)
	t.Setenv("DBBIE_SESSION", "")
	if _, err := session.Create("other", "", "other", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) {
		t.Fatal("invalid session opened DB")
		return nil, nil
	}
	for _, name := range []string{"other", "BAD", "../escape"} {
		out, err := command(t, "query", "--conn", "local", "--session", name, "--sql", "select 1 as x")
		if err == nil {
			t.Fatal("expected invalid")
		}
		assertErrorJSON(t, []byte(out), "invalid")
	}
	if entries := readEntries(t, "local"); len(entries) != 0 {
		t.Fatalf("invalid commands logged: %+v", entries)
	}
}

func TestFailedQueriesStillAddedToSession(t *testing.T) {
	for _, tc := range []struct {
		sql, code string
		dbErr     error
	}{
		{"delete from widgets", "rejected", nil},
		{"select 1 as x", "db", errors.New("missing relation")},
		{"select 1 as x", "timeout", context.DeadlineExceeded},
	} {
		t.Run(tc.code, func(t *testing.T) {
			mock, _ := setupQuery(t)
			t.Setenv("DBBIE_SESSION", "trail")
			if tc.dbErr != nil {
				mock.ExpectBegin().WillReturnError(tc.dbErr)
			} else {
				openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) {
					t.Fatal("rejected query opened DB")
					return nil, nil
				}
			}
			out, err := command(t, "query", "--conn", "local", "--sql", tc.sql, "--why", "failed probe")
			if err == nil {
				t.Fatal("expected error")
			}
			assertErrorJSON(t, []byte(out), tc.code)
			s, err := session.Open("trail")
			if err != nil || len(s.Items) != 1 || s.Items[0].SQL != tc.sql || s.Items[0].LogID != "q_0001" {
				t.Fatalf("session: %+v %v", s, err)
			}
			out, err = command(t, "session", "show", "trail")
			if err != nil {
				t.Fatal(err)
			}
			var view sessionView
			if err := json.Unmarshal([]byte(out), &view); err != nil {
				t.Fatal(err)
			}
			if len(view.Items) != 1 || view.Items[0].Error == nil || view.Items[0].Error.Code != tc.code {
				t.Fatalf("show: %s", out)
			}
		})
	}
}

func TestSessionCommandsPinShowRemove(t *testing.T) {
	mock, _ := setupQuery(t)
	t.Setenv("DBBIE_SESSION", "")
	if out, err := command(t, "session", "new", "trail", "--conn", "local", "--title", "Probe"); err != nil {
		t.Fatalf("new: %s %v", out, err)
	}
	if out, err := command(t, "session", "new", "trail", "--conn", "local"); err == nil {
		t.Fatal("duplicate succeeded")
	} else {
		assertErrorJSON(t, []byte(out), "invalid")
	}
	expectRead(mock)
	if _, err := command(t, "query", "--conn", "local", "--sql", "select 1 as x", "--why", "inspect", "--title", "original"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"session", "add", "trail", "q_0001"},
		{"session", "add", "trail", "q_0001", "--title", "override"},
	} {
		if out, err := command(t, args...); err != nil {
			t.Fatalf("add: %s %v", out, err)
		}
	}
	out, err := command(t, "session", "show", "trail")
	if err != nil {
		t.Fatal(err)
	}
	var view sessionView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatal(err)
	}
	if view.Name != "trail" || len(view.Items) != 2 || view.Items[0].RowCount != 1 || view.Items[0].Title != "original" || view.Items[0].Why != "inspect" || view.Items[1].Title != "override" || strings.Contains(out, `"rows"`) {
		t.Fatalf("show: %s", out)
	}
	out, err = command(t, "session", "ls", "--json=false")
	if err != nil || !strings.Contains(out, "items=2") {
		t.Fatalf("human list: %s %v", out, err)
	}
	if _, err := command(t, "session", "rm", "trail", "--item", "1"); err != nil {
		t.Fatal(err)
	}
	s, err := session.Open("trail")
	if err != nil || len(s.Items) != 1 || s.Items[0].N != 2 {
		t.Fatalf("remove: %+v %v", s, err)
	}
	if out, err := command(t, "session", "rm", "trail", "--item", "0"); err == nil {
		t.Fatal("item=0 deleted session")
	} else {
		assertErrorJSON(t, []byte(out), "invalid")
	}
	if _, err := command(t, "session", "rm", "trail"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Open("trail"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	out, err = command(t, "session", "ls")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list: %s %v", out, err)
	}
}

func TestSessionRunReadOnlyUpdatesLogIDsAndContinuesAfterRejection(t *testing.T) {
	setupQuery(t)
	t.Setenv("DBBIE_SESSION", "unrelated")
	// Explicit session run must still log, even with query's no-log environment.
	t.Setenv("DBBIE_NO_LOG", "1")
	if _, err := session.Create("trail", "", "local", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"delete from widgets", "select 1 as x", "select 1 as x"} {
		if _, err := session.Add("trail", session.Item{SQL: sql}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	var mocks []sqlmock.Sqlmock
	openDatabase = func(ctx context.Context, kind db.Kind, dsn string) (*sql.DB, error) {
		calls++
		if kind != db.Postgres {
			t.Fatal("wrong driver")
		}
		conn, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		mock.ExpectQuery(`select \* from \(select 1 as x\) as dbbie_preview limit 3`).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		mock.ExpectRollback()
		mock.ExpectClose()
		mocks = append(mocks, mock)
		return conn, nil
	}
	out, err := command(t, "session", "run", "trail", "--limit", "2", "--timeout", "1s")
	if err == nil {
		t.Fatal("expected nonzero for rejected item")
	}
	// One JSON document, with per-item results and the standard error envelope.
	var result sessionRunOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("JSON: %s %v", out, err)
	}
	if result.Error == nil || result.Error.Code != "rejected" || len(result.Items) != 3 || result.Items[0].Error.Code != "rejected" || result.Items[1].Result.RowCount != 1 || calls != 2 {
		t.Fatalf("run: %s calls=%d", out, calls)
	}
	s, err := session.Open("trail")
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range s.Items {
		if item.LogID != result.Items[i].LogID || item.LogID == "" {
			t.Fatalf("logId not updated: %+v", s)
		}
	}
	entries := readEntries(t, "local")
	if len(entries) != 3 {
		t.Fatalf("entries: %+v", entries)
	}
	for i, e := range entries {
		// Item lets an open TUI refresh the matching session tab in place.
		if e.Session != "trail" || e.Actor != "agent" || e.Item != s.Items[i].N {
			t.Fatalf("entry: %+v", e)
		}
	}
	out, err = command(t, "session", "run", "trail", "--only", "2", "--limit", "2")
	if err != nil {
		t.Fatalf("only: %s %v", out, err)
	}
	s, err = session.Open("trail")
	if err != nil || s.Items[0].LogID != "q_0001" || s.Items[1].LogID != "q_0004" || s.Items[2].LogID != "q_0003" || calls != 3 {
		t.Fatalf("only changed wrong item: %+v %v calls=%d", s, err, calls)
	}
	for _, mock := range mocks {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
	out, err = command(t, "session", "run", "trail", "--only", "99")
	if err == nil {
		t.Fatal("missing item run succeeded")
	}
	assertErrorJSON(t, []byte(out), "invalid")
	out, err = command(t, "session", "run", "trail", "--allow-write")
	if err == nil {
		t.Fatal("session run accepted allow-write")
	}
	assertErrorJSON(t, []byte(out), "invalid")
}

func TestConnectRenameRemoveAndNoCredentials(t *testing.T) {
	setupQuery(t)
	out, err := command(t, "connect", "--rename", "local", "renamed")
	if err != nil || !strings.Contains(out, "renamed") || strings.Contains(out, "secret") {
		t.Fatalf("rename: %s %v", out, err)
	}
	p, _ := config.Path()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Find("local"); ok {
		t.Fatal("old connection retained")
	}
	if c, ok := cfg.Find("renamed"); !ok || !strings.Contains(c.DSN, "secret") {
		t.Fatal("credentials not retained privately")
	}
	if _, err := command(t, "connect", "other", "mysql", "mysql://private"); err != nil {
		t.Fatal(err)
	}
	if out, err := command(t, "connect", "--rename", "renamed", "other"); err == nil {
		t.Fatal("overwrote rename destination")
	} else {
		assertErrorJSON(t, []byte(out), "invalid")
	}
	out, err = command(t, "connect", "--rm", "renamed", "--json=false")
	if err != nil || !strings.Contains(out, "renamed") || strings.Contains(out, `"removed"`) {
		t.Fatalf("human remove: %s %v", out, err)
	}
	out, err = command(t, "connections")
	if err != nil || strings.Contains(out, "renamed") || strings.Contains(out, "private") || strings.Contains(out, "dsn") {
		t.Fatalf("connections: %s %v", out, err)
	}
	out, err = command(t, "connect", "--rm", "renamed")
	if err == nil {
		t.Fatal("missing remove succeeded")
	}
	assertErrorJSON(t, []byte(out), "invalid")
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode: %v %v", info, err)
	}
	info, err = os.Stat(filepath.Dir(p))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v %v", info, err)
	}
}

func TestQuerySaveUsesSavedNameAndSession(t *testing.T) {
	mock, state := setupQuery(t)
	t.Setenv("DBBIE_SESSION", "")
	expectRead(mock)
	dsn := "postgres://new:private@localhost/app"
	out, err := command(t, "query", "--driver", "postgres", "--dsn", dsn, "--save", "saved", "--session", "trail", "--sql", "select 1 as x")
	if err != nil || strings.Contains(out, "private") {
		t.Fatalf("save query: %s %v", out, err)
	}
	entries := readEntries(t, "saved")
	if len(entries) != 1 || entries[0].Conn != "saved" || entries[0].Session != "trail" {
		t.Fatalf("log: %+v", entries)
	}
	s, err := session.Open("trail")
	if err != nil || s.Conn != "saved" {
		t.Fatalf("session: %+v %v", s, err)
	}
	p, _ := config.Path()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := cfg.Find("saved"); !ok || c.DSN != dsn {
		t.Fatal("save missing")
	}
	data, err := os.ReadFile(filepath.Join(state, "sessions", "trail.json"))
	if err != nil || strings.Contains(string(data), dsn) {
		t.Fatalf("DSN in session: %s %v", data, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSavePassesSavedConnectionNameToTUI(t *testing.T) {
	mock, _ := setupQuery(t)
	mock.ExpectClose()
	original := runTUI
	t.Cleanup(func() { runTUI = original })
	called := false
	runTUI = func(ctx context.Context, opts ui.Options) error {
		called = true
		if opts.Connection != "saved" || opts.Kind != db.Postgres || opts.InitialSQL != "select 1" {
			t.Fatalf("options: %+v", opts)
		}
		return nil
	}
	out, err := command(t, "open", "--driver", "postgres", "--dsn", "postgres://private", "--save", "saved", "--sql", "select 1")
	if err != nil || !called || strings.Contains(out, "private") {
		t.Fatalf("open: %s %v called=%t", out, err, called)
	}
	p, _ := config.Path()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Find("saved"); !ok {
		t.Fatal("open did not save connection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveAndSessionInvalidCombinations(t *testing.T) {
	setupQuery(t)
	t.Setenv("DBBIE_SESSION", "")
	openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) {
		t.Fatal("invalid combination opened DB")
		return nil, nil
	}
	for _, args := range [][]string{
		{"query", "--conn", "local", "--save", "other", "--sql", "select 1"},
		{"query", "--driver", "postgres", "--dsn", "postgres://private", "--session", "trail", "--sql", "select 1"},
		{"query", "--conn", "local", "--session", "trail", "--no-log", "--sql", "select 1"},
		{"open", "--conn", "local", "--save", "other"},
		{"query", "--driver", "postgres", "--save", "saved", "--sql", "select 1"},
	} {
		out, err := command(t, args...)
		if err == nil {
			t.Fatalf("expected invalid: %v", args)
		}
		assertErrorJSON(t, []byte(out), "invalid")
	}
}

func TestSessionTextRedaction(t *testing.T) {
	mock, state := setupQuery(t)
	t.Setenv("DBBIE_SESSION", "")
	p, _ := config.Path()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	dsn := cfg.Connections[0].DSN
	expectRead(mock)
	if _, err := command(t, "query", "--conn", "local", "--session", "trail", "--sql", "select 1 as x", "--why", dsn, "--title", dsn); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(state, "sessions", "trail.json"))
	if err != nil || strings.Contains(string(data), "secret") || strings.Contains(string(data), dsn) {
		t.Fatalf("private text: %s %v", data, err)
	}
	out, err := command(t, "session", "show", "trail")
	if err != nil || strings.Contains(out, "secret") {
		t.Fatalf("show leak: %s %v", out, err)
	}
}

func TestSessionRunDatabaseFailureStillUpdates(t *testing.T) {
	mock, _ := setupQuery(t)
	t.Setenv("DBBIE_SESSION", "")
	if _, err := session.Create("trail", "", "local", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Add("trail", session.Item{SQL: "select 1 as x", LogID: "q_0099"}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin().WillReturnError(errors.New("database unavailable"))
	out, err := command(t, "session", "run", "trail")
	if err == nil {
		t.Fatal("expected error")
	}
	var result sessionRunOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("run: %s %v", out, err)
	}
	if result.Error.Code != "db" || result.Items[0].LogID != "q_0001" || result.Items[0].Error.Code != "db" {
		t.Fatalf("run: %s", out)
	}
	s, err := session.Open("trail")
	if err != nil || s.Items[0].LogID != "q_0001" {
		t.Fatalf("failed run lost logId: %+v %v", s, err)
	}
	entries := readEntries(t, "local")
	if len(entries) != 1 || entries[0].Error.Code != "db" || entries[0].Session != "trail" {
		t.Fatalf("log: %+v", entries)
	}
}

// qlog.Entry remains compatible with pre-session version-1 lines.
func TestOldQlogLineHasEmptySession(t *testing.T) {
	var e qlog.Entry
	if err := json.Unmarshal([]byte(`{"v":1,"id":"q_0001","conn":"local","sql":"select 1"}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Session != "" {
		t.Fatalf("old session: %q", e.Session)
	}
	data, err := json.Marshal(e)
	if err != nil || strings.Contains(string(data), `"session"`) {
		t.Fatalf("omitempty: %s %v", data, err)
	}
}
