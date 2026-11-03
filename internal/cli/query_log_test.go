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
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func setupQuery(t *testing.T) (sqlmock.Sqlmock, string) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("DBBIE_STATE_DIR", state)
	t.Setenv("DBBIE_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("DBBIE_NO_LOG", "")
	t.Setenv("DBBIE_SESSION", "")
	t.Setenv("DBBIE_ACTOR", "")
	p, _ := config.Path()
	if err := config.Save(p, config.File{Connections: []config.Connection{{Name: "local", Driver: db.Postgres, DSN: "postgres://secret:password@localhost/app?sslmode=disable&test=1"}}}); err != nil {
		t.Fatal(err)
	}
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	old := openDatabase
	openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) { return conn, nil }
	t.Cleanup(func() { openDatabase = old; _ = conn.Close() })
	return mock, state
}

func expectRead(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery(`select \* from \(select 1 as x\) as dbbie_preview limit 101`).
		WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("x").OfType("INT", 1)).AddRow(1))
	mock.ExpectRollback()
}

func readEntries(t *testing.T, name string) []qlog.Entry {
	t.Helper()
	l, err := qlog.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := l.List(0)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestQueryLogsWhyActorAndID(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		args                 []string
		actorEnv, actor, why string
	}{
		{"why", []string{"--why", "find one", "--title", "one"}, "", "agent", "find one"},
		{"piped", nil, "", "agent", ""},
		{"actor override", []string{"--why", "find one"}, "reviewer", "reviewer", "find one"},
		{"empty why supplied", []string{"--why="}, "", "agent", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, _ := setupQuery(t)
			t.Setenv("DBBIE_ACTOR", tc.actorEnv)
			expectRead(mock)
			args := append([]string{"query", "--conn", "local", "--sql", "select 1 as x"}, tc.args...)
			var out bytes.Buffer
			if err := Run(context.Background(), args, &out, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			var result query.Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("JSON = %s: %v", out.String(), err)
			}
			entries := readEntries(t, "local")
			if len(entries) != 1 {
				t.Fatalf("entries = %v", entries)
			}
			e := entries[0]
			if result.ID != "q_0001" || e.ID != result.ID || e.Why != tc.why || e.Actor != tc.actor || e.Driver != "postgres" || e.Source != "cli" || e.SQL != "select 1 as x" || e.RowCount != 1 || e.Error != nil || e.ElapsedMs < 0 {
				t.Fatalf("result = %+v, entry = %+v", result, e)
			}
			if tc.name == "why" && e.Title != "one" {
				t.Fatalf("title = %q", e.Title)
			}
			if len(e.Rows) != 1 || e.Rows[0][0] != float64(1) || e.Columns[0].Database != "INT" {
				t.Fatalf("preview = %+v", e)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueryFailureCodesAndLogging(t *testing.T) {
	for _, tc := range []struct {
		name, sql, code string
		dbErr           error
		openErr         bool
	}{
		{"rejected", "delete from x", "rejected", nil, false},
		{"invalid", "", "invalid", nil, false},
		{"db", "select 1 as x", "db", errors.New("table does not exist"), false},
		{"timeout", "select 1 as x", "timeout", context.DeadlineExceeded, false},
		{"open db", "select 1 as x", "db", errors.New("postgres://secret:password@localhost/app?sslmode=disable&test=1"), true},
		{"open timeout", "select 1 as x", "timeout", context.DeadlineExceeded, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, _ := setupQuery(t)
			if tc.openErr {
				openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) { return nil, tc.dbErr }
			} else if tc.dbErr != nil {
				mock.ExpectBegin().WillReturnError(tc.dbErr)
			} else {
				openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) {
					t.Fatal("invalid/rejected SQL opened a database")
					return nil, nil
				}
			}
			var out, stderr bytes.Buffer
			err := Run(context.Background(), []string{"query", "--conn", "local", "--sql", tc.sql, "--why", "test"}, &out, &stderr)
			if err == nil {
				t.Fatal("expected failure")
			}
			assertErrorJSON(t, out.Bytes(), tc.code)
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr = %s", stderr.String())
			}
			entries := readEntries(t, "local")
			if len(entries) != 1 || entries[0].Error == nil || entries[0].Error.Code != tc.code || len(entries[0].Rows) != 0 {
				t.Fatalf("entries = %+v", entries)
			}
			if strings.Contains(out.String(), "secret") || strings.Contains(entries[0].Error.Message, "secret") {
				t.Fatal("DSN leaked")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertErrorJSON(t *testing.T, data []byte, code string) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("error JSON = %s: %v", data, err)
	}
	if len(envelope) != 1 {
		t.Fatalf("error envelope = %s", data)
	}
	var detail map[string]string
	if err := json.Unmarshal(envelope["error"], &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail) != 2 || detail["code"] != code || detail["message"] == "" {
		t.Fatalf("error detail = %v", detail)
	}
}

func TestQueryNoLogAndAdhoc(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  []string
		env    string
		logged bool
	}{
		{"flag", []string{"--no-log"}, "", false},
		{"env", nil, "1", false},
		{"env cannot be disabled", []string{"--no-log=false"}, "1", false},
		{"logged adhoc", nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, state := setupQuery(t)
			t.Setenv("DBBIE_NO_LOG", tc.env)
			expectRead(mock)
			var out bytes.Buffer
			args := append([]string{"query", "--driver", "postgres", "--dsn", "a-secret-dsn", "--sql", "select 1 as x"}, tc.flags...)
			if err := Run(context.Background(), args, &out, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			var result query.Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tc.logged {
				entries := readEntries(t, "adhoc")
				if len(entries) != 1 || result.ID != entries[0].ID || entries[0].Conn != "adhoc" {
					t.Fatalf("entries = %+v, result = %+v", entries, result)
				}
			} else {
				if !strings.HasPrefix(result.ID, "qry_") {
					t.Fatalf("unlogged id = %s", result.ID)
				}
				files, err := os.ReadDir(state)
				if err != nil || len(files) != 0 {
					t.Fatalf("no-log wrote files: %v, %v", files, err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLogListsAndShowPagesWithoutDB(t *testing.T) {
	_, _ = setupQuery(t)
	openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) {
		t.Fatal("log/show opened a DB")
		return nil, nil
	}
	l, err := qlog.Open("offline")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := l.Append(qlog.Entry{Actor: "agent", Why: "stored", SQL: "select x", Rows: [][]any{{1}, {2}, {3}}, RowCount: 3}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"log", "--conn", "offline", "--limit", "2"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var summaries []map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &summaries); err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || string(summaries[0]["id"]) != `"q_0002"` || len(summaries[0]) != 8 {
		t.Fatalf("summaries = %s", out.String())
	}
	if _, ok := summaries[0]["rows"]; ok {
		t.Fatal("log includes rows")
	}
	for _, tc := range []struct {
		name, offset, limit string
		wantRows            int
		first               float64
	}{
		{"middle", "1", "1", 1, 2},
		{"past end", "100", "10", 0, 0},
		{"zero page", "0", "0", 0, 0},
		{"clamp", "0", "1000000000", 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out.Reset()
			if err := Run(context.Background(), []string{"show", "q_0001", "--conn", "offline", "--offset", tc.offset, "--limit", tc.limit}, &out, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			var e qlog.Entry
			if err := json.Unmarshal(out.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			if e.RowCount != 3 || len(e.Rows) != tc.wantRows || (tc.wantRows > 0 && e.Rows[0][0] != tc.first) {
				t.Fatalf("page = %+v", e)
			}
		})
	}
	out.Reset()
	if err := Run(context.Background(), []string{"show", "--conn", "offline", "q_0001"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestTablesMatch(t *testing.T) {
	for _, tc := range []struct {
		glob  string
		names []string
	}{
		{"user*", []string{"users", "user_events"}},
		{"public.user*", []string{"users"}},
		{"*.orders", []string{"orders"}},
		{"nomatch", nil},
		{"", []string{"users", "user_events", "orders"}},
	} {
		t.Run(tc.glob, func(t *testing.T) {
			mock, _ := setupQuery(t)
			mock.ExpectQuery(`information_schema\.tables`).WillReturnRows(sqlmock.NewRows([]string{"schema", "name", "type"}).AddRow("public", "users", "BASE TABLE").AddRow("audit", "user_events", "BASE TABLE").AddRow("public", "orders", "BASE TABLE"))
			var out bytes.Buffer
			if err := Run(context.Background(), []string{"tables", "--conn", "local", "--match", tc.glob}, &out, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			var tables []catalog.Table
			if err := json.Unmarshal(out.Bytes(), &tables); err != nil {
				t.Fatal(err)
			}
			if len(tables) != len(tc.names) {
				t.Fatalf("tables = %s", out.String())
			}
			for i, name := range tc.names {
				if tables[i].Name != name {
					t.Fatalf("tables = %+v", tables)
				}
			}
			if len(tables) > 0 && !strings.Contains(out.String(), `"estimatedRows"`) {
				t.Fatal("estimatedRows missing")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJSONDefaultsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		code  string
		human bool
	}{
		{"unknown", []string{"bad-command"}, "invalid", false},
		{"bad glob", []string{"tables", "--match", "["}, "invalid", false},
		{"missing id", []string{"show", "missing", "--conn", "local"}, "invalid", false},
		{"negative offset", []string{"show", "q_0001", "--conn", "local", "--offset", "-1"}, "invalid", false},
		{"bad flag", []string{"query", "--limit", "bad"}, "invalid", false},
		{"explicit false", []string{"describe", "--json=false"}, "invalid", true},
		{"double flag last wins", []string{"describe", "--json", "--json=false"}, "invalid", true},
		{"fake json in sql", []string{"query", "--json=false", "--sql", "--json"}, "rejected", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = setupQuery(t)
			var out, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &out, &stderr)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.human {
				if out.Len() != 0 || err.Error() == "" {
					t.Fatalf("human output = %s, error = %v", out.String(), err)
				}
			} else {
				assertErrorJSON(t, out.Bytes(), tc.code)
			}
			if stderr.Len() != 0 {
				t.Fatalf("flag errors leaked stderr: %s", stderr.String())
			}
		})
	}
}

func TestHumanOutputOverride(t *testing.T) {
	mock, _ := setupQuery(t)
	expectRead(mock)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"query", "--conn", "local", "--sql", "select 1 as x", "--json=false"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "q_0001 rows=1") || strings.HasPrefix(out.String(), "{") {
		t.Fatalf("human output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"connections", "--json=false"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "local\tpostgres\n" {
		t.Fatalf("connections human = %s", out.String())
	}
}

func TestDSNsAreRedactedEverywhere(t *testing.T) {
	mock, state := setupQuery(t)
	p, _ := config.Path()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	dsn := cfg.Connections[0].DSN
	mock.ExpectBegin()
	mock.ExpectQuery("dbbie_preview").WillReturnRows(sqlmock.NewRows([]string{"dsn"}).AddRow(dsn))
	mock.ExpectRollback()
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"query", "--conn", "local", "--sql", "select '" + dsn + "'", "--title", dsn, "--why", dsn}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(state, "local.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{out.String(), string(data)} {
		if strings.Contains(text, "secret") || strings.Contains(text, "password") || strings.Contains(text, "postgres://") {
			t.Fatalf("DSN leaked: %s", text)
		}
	}
	out.Reset()
	err = Run(context.Background(), []string{"tables", "--driver", "postgres", "--dsn", dsn, "--limit", dsn}, &out, &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(out.String(), "secret") {
		t.Fatalf("flag error leaked: %s %v", out.String(), err)
	}
}

func TestQueryInvalidConnectionStillLogs(t *testing.T) {
	_, _ = setupQuery(t)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"query", "--conn", "missing", "--sql", "select 1"}, &out, &bytes.Buffer{}); err == nil {
		t.Fatal("expected invalid connection")
	}
	entries := readEntries(t, "missing")
	if len(entries) != 1 || entries[0].Error.Code != "invalid" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestQueryCanceledDeadlineLogsTimeout(t *testing.T) {
	_, _ = setupQuery(t)
	openDatabase = func(ctx context.Context, _ db.Kind, _ string) (*sql.DB, error) { return nil, ctx.Err() }
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	var out bytes.Buffer
	if err := Run(ctx, []string{"query", "--conn", "local", "--sql", "select 1"}, &out, &bytes.Buffer{}); err == nil {
		t.Fatal("expected timeout")
	}
	assertErrorJSON(t, out.Bytes(), "timeout")
	if entries := readEntries(t, "local"); len(entries) != 1 || entries[0].Error.Code != "timeout" {
		t.Fatalf("entries = %+v", entries)
	}
}
